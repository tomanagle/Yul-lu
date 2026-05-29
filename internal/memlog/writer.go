package memlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Writer persists memory-log entries to a .yullu/logs/ directory.
//
// Construct with NewWriter(gitRoot, syncDir). It's safe to use from a single
// goroutine; concurrent writers to the same directory are fine across
// processes because each entry gets a unique filename.
type Writer struct {
	logsDir string
}

// NewWriter returns a writer that places entries under <gitRoot>/<syncDir>/logs.
// The directory is created on demand. Returns nil if gitRoot is empty - the
// caller should check and skip logging in that case.
func NewWriter(gitRoot, syncDir string) *Writer {
	if gitRoot == "" {
		return nil
	}
	if syncDir == "" {
		syncDir = ".yullu"
	}
	return &Writer{logsDir: filepath.Join(gitRoot, syncDir, "logs")}
}

// Dir returns the absolute path of the logs directory.
func (w *Writer) Dir() string { return w.logsDir }

// Write serializes e as JSON and writes it to a uniquely named file in the
// logs directory. The filename is "<iso-timestamp>-<short-id>.json" so the
// directory listing sorts lexically by time.
//
// The write is atomic on POSIX (write to a temp file in the same dir, then
// rename), so readers never observe a half-written entry.
func (w *Writer) Write(e Event) error {
	if err := os.MkdirAll(w.logsDir, 0o755); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	data = append(data, '\n')

	name := filename(e)
	final := filepath.Join(w.logsDir, name)
	tmp, err := os.CreateTemp(w.logsDir, ".tmp-"+name+"-*")
	if err != nil {
		return fmt.Errorf("create temp entry: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write entry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close entry: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("commit entry: %w", err)
	}
	return nil
}

// filename returns a sortable, filesystem-safe filename for the entry.
// Format: 20060102T150405.000000000Z-<8-char-id>.json
//
// Nanosecond precision matters: entries produced in rapid succession (e.g. a
// remember followed by its embedding revise) must order deterministically by
// filename, since lexical sort is how reconcile decides which embedding
// matches which content snapshot. Millisecond precision is not enough - two
// entries on a fast machine routinely land in the same millisecond, leaving
// the UUID suffix to decide order at random.
func filename(e Event) string {
	ts := e.Timestamp.UTC().Format("20060102T150405.000000000Z07:00")
	// Replace any timezone offset characters that are filesystem-unfriendly.
	ts = strings.ReplaceAll(ts, ":", "")
	short := e.ID
	if len(short) > 8 {
		short = strings.ReplaceAll(short, "-", "")
		if len(short) > 8 {
			short = short[:8]
		}
	}
	return ts + "-" + short + ".json"
}
