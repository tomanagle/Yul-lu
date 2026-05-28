// Package applog owns the project's structured-logging configuration.
//
// Packages don't depend on this package - they depend on *slog.Logger from
// the standard library. applog exists only as a single place to decide
// "where do logs go?", "what format?", and "what level?". main.go calls
// New() once at boot and injects the resulting logger into every component
// that needs to log.
package applog

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON logger writing to stderr at the level named by
// $YULLU_LOG_LEVEL (debug|info|warn|error). Defaults to info.
//
// stdio is reserved for the MCP JSON-RPC channel, so logs MUST go to stderr;
// callers should not rewire the destination.
func New() *slog.Logger {
	level := parseLevel(os.Getenv("YULLU_LOG_LEVEL"))
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With("service", "yullu")
}

// Discard returns a logger that drops every record. Useful in tests so
// production log lines don't clutter `go test -v` output.
func Discard() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
