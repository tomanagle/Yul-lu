package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoreCRUDAndSearch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	const dim = 4
	st, err := Open(dbPath, "stub:test", dim)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	projectA := "/path/to/projectA"
	projectB := "/path/to/projectB"

	// Insert distinct memories with hand-crafted unit vectors so we can
	// reason about which one a query lands closest to.
	id1, err := st.Insert(ctx, "", projectA, "remember thing one", []string{"a"}, []float32{1, 0, 0, 0})
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	id2, err := st.Insert(ctx, "", projectA, "remember thing two", nil, []float32{0, 1, 0, 0})
	if err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	idB, err := st.Insert(ctx, "", projectB, "different project", nil, []float32{1, 0, 0, 0})
	if err != nil {
		t.Fatalf("insert B: %v", err)
	}
	if id1 == 0 || id2 == 0 || idB == 0 {
		t.Fatalf("ids should be nonzero: %d %d %d", id1, id2, idB)
	}

	// Search projectA with a query close to vec1; expect id1 first and idB excluded.
	hits, err := st.Search(ctx, projectA, []float32{0.9, 0.1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != id1 {
		t.Fatalf("expected top hit %d, got %+v", id1, hits)
	}
	for _, h := range hits {
		if h.ID == idB {
			t.Fatalf("project scoping leaked: idB %d returned in projectA search", idB)
		}
	}

	// List for projectA returns 2 entries.
	list, err := st.List(ctx, projectA, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 in projectA, got %d", len(list))
	}

	// Update content + vector. Re-search should now return updated content.
	newContent := "remember thing one - updated"
	if err := st.Update(ctx, id1, &newContent, nil, []float32{0, 0, 1, 0}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := st.Get(ctx, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != newContent {
		t.Fatalf("content not updated: %q", got.Content)
	}
	hits, err = st.Search(ctx, projectA, []float32{0, 0, 1, 0}, 1)
	if err != nil {
		t.Fatalf("search after update: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != id1 {
		t.Fatalf("expected updated vector to match: %+v", hits)
	}

	// Delete and confirm gone.
	if err := st.Delete(ctx, id2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = st.List(ctx, projectA, 10)
	if len(list) != 1 {
		t.Fatalf("expected 1 after delete, got %d", len(list))
	}

	// Reopening with a mismatched embed_id must error.
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := Open(dbPath, "stub:other", dim); err == nil {
		t.Fatalf("expected embedder mismatch error, got nil")
	}
	// Reopening with mismatched dim must error.
	if _, err := Open(dbPath, "stub:test", dim+1); err == nil {
		t.Fatalf("expected dim mismatch error, got nil")
	}
	// Reopening with matching config must succeed.
	st2, err := Open(dbPath, "stub:test", dim)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = st2.Close()
}
