package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// writeJSON marshals v as the response body with application/json. Errors
// from Encode are swallowed because the response status is already
// committed by the time encoding might fail.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeList wraps a collection in the standard {"items": [...]} envelope.
// Reasons we don't return bare arrays:
//   - top-level JSON arrays were historically a CSRF/JSON-hijacking risk
//     in older browsers that allowed JS to override the Array constructor;
//   - wrapping leaves room for pagination metadata (total, next_cursor)
//     without breaking the wire format later;
//   - keeps the API surface uniform — every endpoint returns an object.
//
// nil slices are coerced to [] so clients never see `{"items": null}`.
func writeList[T any](w http.ResponseWriter, items []T) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, map[string]any{"items": items})
}

// writeError sends a JSON error body with the given status. The shape
// matches what the frontend's fetch wrapper expects ({error: string}).
func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// atoiDefault parses an int from a query-string value, falling back to d
// when the value is empty or malformed. Empty + invalid both collapse to
// the default so handlers don't need to disambiguate.
func atoiDefault(s string, d int) int {
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}
