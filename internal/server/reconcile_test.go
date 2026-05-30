package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/tomanagle/yullu/internal/ai"
	"github.com/tomanagle/yullu/internal/applog"
	"github.com/tomanagle/yullu/internal/config"
	"github.com/tomanagle/yullu/internal/memlog"
	"github.com/tomanagle/yullu/internal/scope"
	"github.com/tomanagle/yullu/internal/store"
)

// fakeEmbedder is a deterministic stand-in for a real embedder, configurable
// per "dev" so tests can simulate same-model and cross-model scenarios.
// Embed call count is exposed so tests can assert reuse vs. local compute.
type fakeEmbedder struct {
	id    string
	dim   int
	calls int64
}

func (f *fakeEmbedder) ID() string { return f.id }
func (f *fakeEmbedder) Dim() int   { return f.dim }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	atomic.AddInt64(&f.calls, int64(len(texts)))
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, f.dim)
		var sum byte
		for _, c := range []byte(t) {
			sum += c
		}
		for j := range v {
			v[j] = float32((int(sum) + j*7) % 23)
		}
		out[i] = v
	}
	return out, nil
}

func (f *fakeEmbedder) callCount() int64 { return atomic.LoadInt64(&f.calls) }

// setupRepo creates a tempdir containing a stub .git/ directory, chdirs into
// it for the duration of the test, and returns the absolute path. Servers
// constructed after this call resolve their git root + project ID to this
// directory via scope.Resolve.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	t.Chdir(dir)
	return dir
}

// newTestServer builds a Server backed by a fresh on-disk SQLite store with
// the given embedder + reasoner. The DB lives in t.TempDir(); the writer
// points at the .yullu/ directory under the current CWD (set by
// setupRepo). Pass a nil reasoner if the test doesn't exercise dreaming.
func newTestServer(t *testing.T, embedder ai.Embedder, reasoner ai.Reasoner) (*Server, *store.Store) {
	t.Helper()
	dbDir := t.TempDir()
	st, err := store.Open(filepath.Join(dbDir, "test.db"), embedder.ID(), embedder.Dim())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Dreaming disabled in tests so no background scheduler ever spawns.
	cfg := config.Config{
		Sync: config.SyncConfig{
			Enabled:         true,
			Dir:             ".yullu",
			LogEmbeddings:   true,
			ReuseEmbeddings: true,
		},
		Dreaming: config.DreamingConfig{},
	}
	srv := New(st, embedder, reasoner, cfg, applog.Discard())
	if srv.writer == nil {
		t.Fatalf("expected writer to be initialised inside a git repo")
	}
	return srv, st
}

// publishCreate simulates the work handleStore does after parsing the MCP
// request: embed, write a create event carrying the vector, insert.
// Returns the memory's local ID and UUID.
func publishCreate(t *testing.T, srv *Server, st *store.Store, projectID, content string, tags []string) (int64, string) {
	t.Helper()
	ctx := context.Background()
	vecs, err := srv.embedder.Embed(ctx, []string{content})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	memUUID := uuid.NewString()
	vectors := map[string][]float32{srv.embedder.ID(): vecs[0]}
	if err := srv.writer.Write(memlog.NewRememberEvent(memUUID, content, tags, vectors)); err != nil {
		t.Fatalf("write create event: %v", err)
	}
	id, err := st.Insert(ctx, memUUID, projectID, content, tags, vecs[0], "")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	return id, memUUID
}

func TestSyncRoundTrip(t *testing.T) {
	ctx := context.Background()
	gitRoot := setupRepo(t)
	projectID, err := scope.Resolve(gitRoot)
	if err != nil {
		t.Fatalf("resolve project: %v", err)
	}

	embA := &fakeEmbedder{id: "openai:test", dim: 4}
	embB := &fakeEmbedder{id: "openai:test", dim: 4}

	srvA, stA := newTestServer(t, embA, nil)
	srvB, stB := newTestServer(t, embB, nil)

	// --- A creates a memory locally and publishes events. ---
	idA, memUUID := publishCreate(t, srvA, stA, projectID, "first memory", []string{"alpha"})
	if idA == 0 {
		t.Fatalf("A insert returned zero id")
	}

	embedsBeforeB := embB.callCount()

	// --- B reconciles. Same model, so should reuse A's embedding. ---
	res, err := srvB.Reconcile(ctx, "", "")
	if err != nil {
		t.Fatalf("B reconcile: %v", err)
	}
	if res.Created != 1 {
		t.Errorf("want Created=1, got %d (%+v)", res.Created, res)
	}
	if res.EmbeddingsReused != 1 {
		t.Errorf("want EmbeddingsReused=1, got %d", res.EmbeddingsReused)
	}
	if res.EmbeddingsComputed != 0 {
		t.Errorf("want EmbeddingsComputed=0, got %d", res.EmbeddingsComputed)
	}
	if got := embB.callCount() - embedsBeforeB; got != 0 {
		t.Errorf("B should not have called embedder when model matches; got %d calls", got)
	}

	m, err := stB.GetByUUID(ctx, memUUID)
	if err != nil {
		t.Fatalf("B GetByUUID: %v", err)
	}
	if m == nil {
		t.Fatalf("memory missing on B after reconcile")
	}
	if m.Content != "first memory" {
		t.Errorf("B content mismatch: %q", m.Content)
	}
	if !reflect.DeepEqual(m.Tags, []string{"alpha"}) {
		t.Errorf("B tags mismatch: %v", m.Tags)
	}

	// --- Idempotency: re-running reconcile is a no-op. ---
	res2, err := srvB.Reconcile(ctx, "", "")
	if err != nil {
		t.Fatalf("B reconcile #2: %v", err)
	}
	if res2.Created != 0 || res2.Updated != 0 || res2.Deleted != 0 ||
		res2.EmbeddingsReused != 0 || res2.EmbeddingsComputed != 0 {
		t.Errorf("second reconcile should be a no-op, got %+v", res2)
	}

	// --- A updates content; vector ships inside the update event. ---
	newContent := "first memory - updated"
	newVecs, err := embA.Embed(ctx, []string{newContent})
	if err != nil {
		t.Fatalf("A embed update: %v", err)
	}
	updateVectors := map[string][]float32{embA.ID(): newVecs[0]}
	if err := srvA.writer.Write(memlog.NewReviseEvent(memUUID, &newContent, nil, updateVectors)); err != nil {
		t.Fatalf("write update event: %v", err)
	}
	if err := stA.Update(ctx, idA, &newContent, nil, newVecs[0]); err != nil {
		t.Fatalf("A update: %v", err)
	}

	embedsBeforeB = embB.callCount()
	res3, err := srvB.Reconcile(ctx, "", "")
	if err != nil {
		t.Fatalf("B reconcile update: %v", err)
	}
	if res3.Updated != 1 {
		t.Errorf("want Updated=1, got %d (%+v)", res3.Updated, res3)
	}
	if res3.EmbeddingsReused != 1 {
		t.Errorf("want EmbeddingsReused=1 on update, got %d", res3.EmbeddingsReused)
	}
	if got := embB.callCount() - embedsBeforeB; got != 0 {
		t.Errorf("B should not embed on update when model matches; got %d calls", got)
	}

	mUpd, _ := stB.GetByUUID(ctx, memUUID)
	if mUpd == nil || mUpd.Content != newContent {
		t.Errorf("B did not receive updated content; got %+v", mUpd)
	}

	// --- A deletes; B sees the row disappear. ---
	if err := srvA.writer.Write(memlog.NewForgetEvent(memUUID)); err != nil {
		t.Fatalf("write delete event: %v", err)
	}
	if err := stA.Delete(ctx, idA); err != nil {
		t.Fatalf("A delete: %v", err)
	}

	res4, err := srvB.Reconcile(ctx, "", "")
	if err != nil {
		t.Fatalf("B reconcile delete: %v", err)
	}
	if res4.Deleted != 1 {
		t.Errorf("want Deleted=1, got %d (%+v)", res4.Deleted, res4)
	}
	gone, _ := stB.GetByUUID(ctx, memUUID)
	if gone != nil {
		t.Errorf("memory should be gone on B; got %+v", gone)
	}
}

func TestSyncCrossModelTriggersLocalEmbed(t *testing.T) {
	ctx := context.Background()
	gitRoot := setupRepo(t)
	projectID, err := scope.Resolve(gitRoot)
	if err != nil {
		t.Fatalf("resolve project: %v", err)
	}

	embA := &fakeEmbedder{id: "openai:test", dim: 4}
	embC := &fakeEmbedder{id: "ollama:other", dim: 4}

	srvA, stA := newTestServer(t, embA, nil)
	srvC, stC := newTestServer(t, embC, nil)

	_, memUUID := publishCreate(t, srvA, stA, projectID, "cross-model memory", []string{"x"})

	embedsBeforeC := embC.callCount()
	res, err := srvC.Reconcile(ctx, "", "")
	if err != nil {
		t.Fatalf("C reconcile: %v", err)
	}
	if res.Created != 1 {
		t.Errorf("want Created=1, got %d (%+v)", res.Created, res)
	}
	if res.EmbeddingsReused != 0 {
		t.Errorf("want EmbeddingsReused=0 (cross-model), got %d", res.EmbeddingsReused)
	}
	if res.EmbeddingsComputed != 1 {
		t.Errorf("want EmbeddingsComputed=1, got %d", res.EmbeddingsComputed)
	}
	if got := embC.callCount() - embedsBeforeC; got != 1 {
		t.Errorf("C should embed once when model differs; got %d calls", got)
	}

	m, _ := stC.GetByUUID(ctx, memUUID)
	if m == nil || m.Content != "cross-model memory" {
		t.Errorf("C did not receive the memory; got %+v", m)
	}

	// C should also have published its own vector via a vector-only update
	// event, so a future dev on the same model (ollama:other) can reuse it.
	reader := memlog.NewReader(gitRoot, ".yullu")
	entries, _, err := reader.Read()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var foundCVector bool
	for _, e := range entries {
		ev := e.Event
		// Vector-only update events have no content but a vectors map.
		if ev.Type != memlog.EventRevise || ev.MemoryID != memUUID || ev.Content != nil || ev.Vectors == nil {
			continue
		}
		if _, ok := ev.Vectors["ollama:other"]; ok {
			foundCVector = true
			break
		}
	}
	if !foundCVector {
		t.Errorf("expected C to publish a vector-only update event for model ollama:other")
	}
}
