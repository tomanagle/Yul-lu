package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/tomanagle/yullu/internal/ai"
	"github.com/tomanagle/yullu/internal/memlog"
	"github.com/tomanagle/yullu/internal/scope"
	"github.com/tomanagle/yullu/internal/store"
)

// fakeReasoner returns a configurable response (or error) on each Reason
// call. Used to drive the dream pipeline deterministically.
type fakeReasoner struct {
	id       string
	response string
	err      error
	calls    int64
}

func (f *fakeReasoner) ID() string { return f.id }
func (f *fakeReasoner) Reason(_ context.Context, _ ai.ReasonRequest) (string, error) {
	atomic.AddInt64(&f.calls, 1)
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func (f *fakeReasoner) callCount() int64 { return atomic.LoadInt64(&f.calls) }

func TestDreamParseResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOps int
		wantErr bool
	}{
		{
			name:    "plain JSON",
			input:   `{"operations":[{"op":"create","content":"x","reasoning":"y"}]}`,
			wantOps: 1,
		},
		{
			name:    "fenced JSON",
			input:   "```json\n{\"operations\":[{\"op\":\"create\",\"content\":\"x\",\"reasoning\":\"y\"}]}\n```",
			wantOps: 1,
		},
		{
			name:    "fence without language",
			input:   "```\n{\"operations\":[]}\n```",
			wantOps: 0,
		},
		{
			name:    "prose around JSON",
			input:   "Here you go:\n{\"operations\":[{\"op\":\"delete\",\"uuid\":\"abc\",\"reasoning\":\"obsolete\"}]}\nDone.",
			wantOps: 1,
		},
		{
			name:    "empty operations",
			input:   `{"operations":[]}`,
			wantOps: 0,
		},
		{
			name:    "no JSON at all",
			input:   "I can't help with that.",
			wantErr: true,
		},
		{
			name:    "malformed JSON",
			input:   "{not json}",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops, err := parseDreamResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (ops=%v)", ops)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(ops) != tt.wantOps {
				t.Errorf("want %d ops, got %d (%+v)", tt.wantOps, len(ops), ops)
			}
		})
	}
}

func TestDreamRoundTrip(t *testing.T) {
	ctx := context.Background()
	gitRoot := setupRepo(t)
	projectID, err := scope.Resolve(gitRoot)
	if err != nil {
		t.Fatalf("resolve project: %v", err)
	}

	emb := &fakeEmbedder{id: "fake:emb", dim: 4}
	existingUUID := "existing-mem-uuid"

	response := `{
	  "operations": [
	    {"op": "create", "content": "We use Bun, not Node.", "tags": ["convention"], "reasoning": "explicit in chat"},
	    {"op": "update", "uuid": "existing-mem-uuid", "content": "updated content", "reasoning": "discussion clarified"},
	    {"op": "create", "content": "Tests live next to source files.", "reasoning": "project rule"}
	  ]
	}`
	reasoner := &fakeReasoner{id: "fake:reason", response: response}

	srv, st := newTestServer(t, emb, reasoner)
	// Insert the seed memory through the real store so reconcile finds it.
	if _, err := st.Insert(ctx, existingUUID, projectID, "old content", []string{"old"}, []float32{1, 2, 3, 4}, ""); err != nil {
		t.Fatalf("insert seed: %v", err)
	}

	sessionID := "dream-test-session"
	err = st.RecordSessionMessages(ctx, projectID, sessionID, []store.SessionMessageInput{
		{Role: "user", Content: "What runtime do we use?"},
		{Role: "assistant", Content: "Bun. Tests live next to source. Existing content was clarified to be 'updated content'."},
	})
	if err != nil {
		t.Fatalf("record messages: %v", err)
	}

	res, err := srv.Dream(ctx, DreamOptions{})
	if err != nil {
		t.Fatalf("dream: %v", err)
	}

	if res.OpsCreated != 2 {
		t.Errorf("want OpsCreated=2, got %d (%+v)", res.OpsCreated, res)
	}
	if res.OpsUpdated != 1 {
		t.Errorf("want OpsUpdated=1, got %d", res.OpsUpdated)
	}
	if res.MessagesProcessed != 2 {
		t.Errorf("want MessagesProcessed=2, got %d", res.MessagesProcessed)
	}
	if reasoner.callCount() != 1 {
		t.Errorf("want 1 reasoner call, got %d", reasoner.callCount())
	}

	// The two new memories should be in the store, plus the seed (updated).
	list, err := st.List(ctx, projectID, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("want 3 memories total, got %d", len(list))
	}
	updated, err := st.GetByUUID(ctx, existingUUID)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if updated == nil || updated.Content != "updated content" {
		t.Errorf("seed memory not updated; got %+v", updated)
	}

	// Session messages should be cleaned up.
	remaining, err := st.SessionMessages(ctx, projectID, sessionID)
	if err != nil {
		t.Fatalf("list session msgs: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("session messages should be deleted, got %d", len(remaining))
	}

	// Events should be on disk - 2 creates from the dream + 1 update.
	reader := memlog.NewReader(gitRoot, ".yullu")
	entries, _, err := reader.Read()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var creates, updates int
	for _, e := range entries {
		switch e.Event.Type {
		case memlog.EventRemember:
			creates++
		case memlog.EventRevise:
			updates++
		}
	}
	if creates < 2 {
		t.Errorf("want at least 2 create events on disk, got %d", creates)
	}
	if updates < 1 {
		t.Errorf("want at least 1 update event on disk, got %d", updates)
	}
}

func TestDreamReasonerErrorKeepsMessages(t *testing.T) {
	ctx := context.Background()
	gitRoot := setupRepo(t)
	projectID, err := scope.Resolve(gitRoot)
	if err != nil {
		t.Fatalf("resolve project: %v", err)
	}

	emb := &fakeEmbedder{id: "fake:emb", dim: 4}
	// Reasoner returns prose with no JSON - the parser should fail and the
	// session messages should remain in place for the next attempt.
	reasoner := &fakeReasoner{id: "fake:reason", response: "I cannot help with that."}

	srv, st := newTestServer(t, emb, reasoner)

	sessionID := "bad-response-session"
	err = st.RecordSessionMessages(ctx, projectID, sessionID, []store.SessionMessageInput{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	})
	if err != nil {
		t.Fatalf("record messages: %v", err)
	}

	res, err := srv.Dream(ctx, DreamOptions{})
	if err != nil {
		t.Fatalf("dream returned error: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected reasoner error to appear in result.Errors")
	}
	if res.OpsCreated != 0 || res.OpsUpdated != 0 || res.OpsDeleted != 0 {
		t.Errorf("no ops should apply on parse failure, got %+v", res)
	}

	remaining, err := st.SessionMessages(ctx, projectID, sessionID)
	if err != nil {
		t.Fatalf("list session msgs: %v", err)
	}
	if len(remaining) != 2 {
		t.Errorf("messages should be preserved on parse failure, got %d", len(remaining))
	}
}

func TestDreamReasonerCallError(t *testing.T) {
	// If the reasoner errors (network failure, rate limit), the session is
	// reported in result.Errors and its messages remain for retry.
	ctx := context.Background()
	gitRoot := setupRepo(t)
	projectID, _ := scope.Resolve(gitRoot)

	emb := &fakeEmbedder{id: "fake:emb", dim: 4}
	reasoner := &fakeReasoner{id: "fake:reason", err: errors.New("upstream timeout")}
	srv, st := newTestServer(t, emb, reasoner)

	sessionID := "transient-error-session"
	_ = st.RecordSessionMessages(ctx, projectID, sessionID, []store.SessionMessageInput{
		{Role: "user", Content: "x"},
	})

	res, err := srv.Dream(ctx, DreamOptions{})
	if err != nil {
		t.Fatalf("dream returned error: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected reasoner error to appear in result.Errors")
	}
	remaining, _ := st.SessionMessages(ctx, projectID, sessionID)
	if len(remaining) != 1 {
		t.Errorf("messages should survive reasoner error, got %d", len(remaining))
	}
}
