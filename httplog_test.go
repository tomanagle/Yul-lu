package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithRequestLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ok", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("GET /api/err", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"db locked"}`))
	})
	mux.HandleFunc("GET /api/boom", func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("spa"))
	})

	h := withRequestLogging(mux, logger)

	do := func(target string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		return rr
	}

	if rr := do("/api/ok?project_id=p1"); rr.Code != http.StatusOK {
		t.Fatalf("ok: want 200, got %d", rr.Code)
	}
	if rr := do("/api/err"); rr.Code != http.StatusInternalServerError {
		t.Fatalf("err: want 500, got %d", rr.Code)
	}
	if rr := do("/api/boom"); rr.Code != http.StatusInternalServerError {
		t.Fatalf("boom: want 500 (panic recovered), got %d", rr.Code)
	}
	// Successful SPA/static serve (route "/") is intentionally not logged.
	if rr := do("/index.html"); rr.Code != http.StatusOK {
		t.Fatalf("spa: want 200, got %d", rr.Code)
	}

	var lines []map[string]any
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line not JSON: %q (%v)", sc.Text(), err)
		}
		lines = append(lines, m)
	}

	// 3 lines: ok + err + boom. The SPA success is skipped.
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d: %v", len(lines), lines)
	}

	ok := lines[0]
	if ok["msg"] != "http_request" || ok["method"] != "GET" || ok["route"] != "GET /api/ok" {
		t.Fatalf("ok line wrong shape: %v", ok)
	}
	if ok["path"] != "/api/ok" || ok["status"].(float64) != 200 || ok["level"] != "INFO" {
		t.Fatalf("ok line fields: %v", ok)
	}
	if _, has := ok["duration_ms"]; !has {
		t.Fatalf("ok line missing duration_ms: %v", ok)
	}
	if ok["project_id"] != "p1" {
		t.Fatalf("ok line missing project_id business context: %v", ok)
	}

	er := lines[1]
	if er["status"].(float64) != 500 || er["level"] != "ERROR" || er["error"] != "db locked" {
		t.Fatalf("err line should be 500/ERROR with the handler message: %v", er)
	}

	boom := lines[2]
	if boom["status"].(float64) != 500 || boom["level"] != "ERROR" {
		t.Fatalf("panic line should be 500/ERROR: %v", boom)
	}
	if s, _ := boom["error"].(string); !strings.Contains(s, "panic: kaboom") {
		t.Fatalf("panic line should carry the panic value: %v", boom["error"])
	}
}
