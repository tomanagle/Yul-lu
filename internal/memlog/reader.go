package memlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Reader reads memory-log entries from a .yullu/logs/ directory.
type Reader struct {
	logsDir string
}

// NewReader returns a reader for the logs directory under gitRoot/syncDir.
// Returns nil if gitRoot is empty.
func NewReader(gitRoot, syncDir string) *Reader {
	if gitRoot == "" {
		return nil
	}
	if syncDir == "" {
		syncDir = ".yullu"
	}
	return &Reader{logsDir: filepath.Join(gitRoot, syncDir, "logs")}
}

// Dir returns the absolute path of the logs directory.
func (r *Reader) Dir() string { return r.logsDir }

// Entry pairs a parsed entry with the filename it was loaded from. The
// filename doubles as the watermark token — callers store the highest
// processed filename and skip anything <= it on the next pass.
type Entry struct {
	Filename string
	Event    Event
}

// Read returns all entries in the directory, sorted lexically by filename
// (which sorts by time given the filename format). Returns an empty slice
// if the directory doesn't exist.
//
// Files that fail to parse are skipped with a noted error; one bad entry
// shouldn't stop the whole log from being read. Malformed files are
// returned separately for the caller to surface.
func (r *Reader) Read() ([]Entry, []error, error) {
	dirEntries, err := os.ReadDir(r.logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read logs dir %s: %w", r.logsDir, err)
	}

	names := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		// Skip temp files written by the writer (atomic rename pattern).
		if strings.HasPrefix(name, ".tmp-") {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Entry, 0, len(names))
	var parseErrors []error
	for _, name := range names {
		path := filepath.Join(r.logsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("%s: read: %w", name, err))
			continue
		}
		var e Event
		if err := json.Unmarshal(data, &e); err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("%s: parse: %w", name, err))
			continue
		}
		out = append(out, Entry{Filename: name, Event: e})
	}
	return out, parseErrors, nil
}
