package memlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Writer persists events to a .yullu/events/ directory.
//
// Construct with NewWriter(gitRoot, syncDir). It's safe to use from a single
// goroutine; concurrent writers to the same directory are fine across
// processes because each event gets a unique filename.
type Writer struct {
	eventsDir string
}

// NewWriter returns a writer that places events under <gitRoot>/<syncDir>/events.
// The directory is created on demand. Returns nil if gitRoot is empty - the
// caller should check and skip event logging in that case.
func NewWriter(gitRoot, syncDir string) *Writer {
	if gitRoot == "" {
		return nil
	}
	if syncDir == "" {
		syncDir = ".yullu"
	}
	return &Writer{eventsDir: filepath.Join(gitRoot, syncDir, "events")}
}

// Dir returns the absolute path of the events directory.
func (w *Writer) Dir() string { return w.eventsDir }

// Write serializes e as JSON and writes it to a uniquely named file in the
// events directory. The filename is "<iso-timestamp>-<short-id>.json" so the
// directory listing sorts lexically by time.
//
// The write is atomic on POSIX (write to a temp file in the same dir, then
// rename), so readers never observe a half-written event.
func (w *Writer) Write(e Event) error {
	if err := os.MkdirAll(w.eventsDir, 0o755); err != nil {
		return fmt.Errorf("create events dir: %w", err)
	}

	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	data = append(data, '\n')

	name := filename(e)
	final := filepath.Join(w.eventsDir, name)
	tmp, err := os.CreateTemp(w.eventsDir, ".tmp-"+name+"-*")
	if err != nil {
		return fmt.Errorf("create temp event: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write event: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close event: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("commit event: %w", err)
	}
	return nil
}

// filename returns a sortable, filesystem-safe filename for the event.
// Format: 20060102T150405.000000000Z-<8-char-id>.json
//
// Nanosecond precision matters: events produced in rapid succession (e.g. a
// create followed by its embedding event) must order deterministically by
// filename, since lexical sort is how reconcile decides which embedding
// matches which content snapshot. Millisecond precision is not enough - two
// events on a fast machine routinely land in the same millisecond, leaving
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
