// Package memlog implements the append-only event log used to share memories
// across developers via the codebase's .yullu/ directory.
//
// Memory mutations are serialized as JSON event files under
// .yullu/events/, one event per file. Filenames are sortable by time
// (RFC-3339-ish prefix + short ID suffix), so readers can apply events in
// order and track progress via a watermark.
//
// The on-disk format is the single source of truth for cross-machine sync;
// the local SQLite store is a materialized projection.
package memlog

import (
	"time"

	"github.com/google/uuid"
)

// EventType discriminates the event payload. Embedding vectors live inside
// create / update events under the Vectors map - there is no separate
// "embedding" type.
type EventType string

const (
	EventCreate EventType = "create"
	EventUpdate EventType = "update"
	EventDelete EventType = "delete"
)

// Event is a single record in the log. Pointer fields are used for partial
// updates so "field absent" (no change) can be distinguished from "field
// present but empty" - e.g. Tags = &[]string{} clears tags, Tags = nil leaves
// them alone.
//
// Vectors is a map keyed by embedder ID ("provider:model", e.g.
// "ollama:nomic-embed-text") so a single event can carry vectors from
// multiple models. When content changes (create event, or update with
// Content != nil), any previously-known vectors are stale because they
// reference an older content snapshot. When content doesn't change
// (update with only Vectors set), the map merges into the existing one.
type Event struct {
	// ID uniquely identifies this event. Distinct from MemoryID.
	ID string `json:"id"`
	// Type tells consumers how to interpret the payload fields.
	Type EventType `json:"type"`
	// MemoryID is the cross-machine UUID of the memory being mutated.
	MemoryID string `json:"memory_id"`
	// Timestamp is when the event was produced. Used for ordering and
	// last-write-wins resolution.
	Timestamp time.Time `json:"ts"`

	// Content and Tags carry the current snapshot of the memory.
	Content *string   `json:"content,omitempty"`
	Tags    *[]string `json:"tags,omitempty"`

	// Vectors is the map of embedder ID -> vector. Present when the author
	// is publishing one or more embeddings (their own model, typically).
	// Multiple keys are allowed but the common case is a single entry.
	Vectors map[string][]float32 `json:"vectors,omitempty"`

	// Author is the optional human or machine identity that produced this
	// event. Useful for debugging diffs across the team.
	Author string `json:"author,omitempty"`
}

// NewCreateEvent builds a create event for a new memory. Pass an empty
// vectors map (or nil) if you don't want to publish an embedding alongside
// the content - teammates will have to embed locally.
func NewCreateEvent(memoryUUID, content string, tags []string, vectors map[string][]float32) Event {
	if tags == nil {
		tags = []string{}
	}
	return Event{
		ID:        uuid.NewString(),
		Type:      EventCreate,
		MemoryID:  memoryUUID,
		Timestamp: time.Now().UTC(),
		Content:   &content,
		Tags:      &tags,
		Vectors:   vectors,
	}
}

// NewUpdateEvent builds an update event. Any of content, tags, or vectors
// may be omitted (nil) - at least one should be set for the event to do
// anything. Setting content invalidates vectors from earlier events; passing
// the new vectors in the same event keeps things atomic.
func NewUpdateEvent(memoryUUID string, content *string, tags *[]string, vectors map[string][]float32) Event {
	return Event{
		ID:        uuid.NewString(),
		Type:      EventUpdate,
		MemoryID:  memoryUUID,
		Timestamp: time.Now().UTC(),
		Content:   content,
		Tags:      tags,
		Vectors:   vectors,
	}
}

// NewVectorEvent builds an update event that only carries vectors. Used by
// reconcile when a teammate's content arrives without a matching vector and
// the local server has to embed it itself - the freshly-computed vector
// gets published so the next teammate on the same model skips the work.
func NewVectorEvent(memoryUUID string, vectors map[string][]float32) Event {
	return NewUpdateEvent(memoryUUID, nil, nil, vectors)
}

// NewDeleteEvent builds a delete event for an existing memory.
func NewDeleteEvent(memoryUUID string) Event {
	return Event{
		ID:        uuid.NewString(),
		Type:      EventDelete,
		MemoryID:  memoryUUID,
		Timestamp: time.Now().UTC(),
	}
}
