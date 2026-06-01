package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

// commit is the VCS revision stamped into the binary at build time (empty for
// `go run` / VCS-less builds). Read once — it never changes at runtime. Part
// of the per-request environment context so a log line can be tied back to the
// exact build that produced it.
var commit = func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return ""
}()

// reqSeq is a per-process request counter — a cheap, allocation-free id to
// correlate a request's log line with anything a handler logs separately.
var reqSeq atomic.Uint64

// withRequestLogging wraps the mux in a canonical-log-line middleware: it
// emits exactly one structured "http_request" event per request at completion
// — method, matched route, concrete path, status, duration, response size,
// the project scope, and the handler's error message on failure — plus build
// context (version, commit). It also recovers panics, turning them into a 500
// + an error-level event instead of crashing the server.
//
// One wide event per request (canonical log lines) beats logs scattered
// through the handlers: everything about a request lands on a single
// queryable line.
func withRequestLogging(mux *http.ServeMux, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Matched pattern = low-cardinality route label (e.g.
		// "GET /api/memories/{id}"); the concrete path is logged separately as
		// a high-cardinality field. Resolved without affecting routing —
		// mux.ServeHTTP below does the real dispatch.
		_, route := mux.Handler(r)
		reqID := strconv.FormatUint(reqSeq.Add(1), 10)

		defer func() {
			status := rec.status
			panicVal := recover()
			if panicVal != nil {
				status = http.StatusInternalServerError
				if !rec.wroteHeader {
					rec.ResponseWriter.WriteHeader(status)
				}
			}

			// Drop the noise of successful static-asset / SPA serves (route
			// "/" is the catch-all SPA handler); keep API, MCP, and every error.
			if route == "/" && status < 400 && panicVal == nil {
				return
			}

			level := slog.LevelInfo
			if status >= 500 || panicVal != nil {
				level = slog.LevelError
			}

			// Float milliseconds with microsecond resolution: these local
			// SQLite-backed handlers are routinely sub-millisecond, so integer
			// ms always rounded to 0. e.g. a 420µs handler logs 0.42, not 0.
			durMs := float64(time.Since(start).Microseconds()) / 1000.0

			attrs := []any{
				"req_id", reqID,
				"method", r.Method,
				"route", route,
				"path", r.URL.Path,
				"status", status,
				"duration_ms", durMs,
				"bytes", rec.bytes,
				"remote", r.RemoteAddr,
				"version", version,
			}
			if commit != "" {
				attrs = append(attrs, "commit", commit)
			}
			// Business context that matters across this app: the project scope.
			if pid := r.URL.Query().Get("project_id"); pid != "" {
				attrs = append(attrs, "project_id", pid)
			}
			switch {
			case panicVal != nil:
				attrs = append(attrs, "error", fmt.Sprintf("panic: %v", panicVal),
					"stack", string(debug.Stack()))
			case status >= 400:
				// Handlers write failures as {"error": "..."}; surface the
				// message so the log line carries the actual cause.
				if msg := rec.errorMessage(); msg != "" {
					attrs = append(attrs, "error", msg)
				}
			}

			logger.Log(r.Context(), level, "http_request", attrs...)

			// Re-panic would already have unwound the stack; we've turned it
			// into a 500 + log, so swallow it here. (Matches net/http, which
			// also recovers per-connection, but this keeps our log honest.)
		}()

		mux.ServeHTTP(rec, r)
	})
}

// statusRecorder wraps http.ResponseWriter to capture the status code, the
// number of bytes written, and — for failures — a capped copy of the error
// body so the middleware can log the handler's actual error message.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
	errBuf      bytes.Buffer
}

const maxErrCapture = 2048

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	// Only buffer error bodies, and only up to a cap — success responses
	// (which can be large) are never copied.
	if r.status >= 400 {
		if room := maxErrCapture - r.errBuf.Len(); room > 0 {
			if len(b) <= room {
				r.errBuf.Write(b)
			} else {
				r.errBuf.Write(b[:room])
			}
		}
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush + Unwrap keep streaming handlers (the /mcp Streamable HTTP endpoint)
// and http.ResponseController (deadlines, hijack) working through the wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// errorMessage pulls {"error":"..."} out of a captured error body, falling
// back to a trimmed snippet of the raw body when it isn't that shape.
func (r *statusRecorder) errorMessage() string {
	b := bytes.TrimSpace(r.errBuf.Bytes())
	if len(b) == 0 {
		return ""
	}
	var parsed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &parsed) == nil && parsed.Error != "" {
		return parsed.Error
	}
	const max = 256
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}
