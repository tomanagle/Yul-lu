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
	id1, err := st.Insert(ctx, "", projectA, "remember thing one", []string{"a"}, []float32{1, 0, 0, 0}, "")
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	id2, err := st.Insert(ctx, "", projectA, "remember thing two", nil, []float32{0, 1, 0, 0}, "")
	if err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	idB, err := st.Insert(ctx, "", projectB, "different project", nil, []float32{1, 0, 0, 0}, "")
	if err != nil {
		t.Fatalf("insert B: %v", err)
	}
	if id1 == 0 || id2 == 0 || idB == 0 {
		t.Fatalf("ids should be nonzero: %d %d %d", id1, id2, idB)
	}

	// Search projectA with a query close to vec1; expect id1 first and idB excluded.
	hits, err := st.Search(ctx, projectA, []float32{0.9, 0.1, 0, 0}, "", 2, nil, 0)
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
	hits, err = st.Search(ctx, projectA, []float32{0, 0, 1, 0}, "", 1, nil, 0)
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

func TestSearchSimilarityRankAndFloor(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "sim.db"), "stub:test", 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	const proj = "/p"

	// Three memories at known angles from the query [1,0,0,0]:
	//   exact = identical (cos 1.0), near = cos 0.8, ortho = orthogonal (cos 0).
	// Vectors are deliberately non-unit ([0.8,0.6] etc.) to exercise the
	// insert-time normalization the similarity math depends on.
	exact, err := st.Insert(ctx, "", proj, "exact", nil, []float32{1, 0, 0, 0}, "")
	if err != nil {
		t.Fatalf("insert exact: %v", err)
	}
	if _, err := st.Insert(ctx, "", proj, "near", nil, []float32{0.8, 0.6, 0, 0}, ""); err != nil {
		t.Fatalf("insert near: %v", err)
	}
	if _, err := st.Insert(ctx, "", proj, "ortho", nil, []float32{0, 1, 0, 0}, ""); err != nil {
		t.Fatalf("insert ortho: %v", err)
	}

	query := []float32{1, 0, 0, 0}

	// No floor: all three come back, ranked by closeness with contiguous
	// 1-based Rank and a descending Similarity bounded to [0,1].
	hits, err := st.Search(ctx, proj, query, "q", 5, nil, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d", len(hits))
	}
	if hits[0].ID != exact {
		t.Fatalf("want exact match ranked first, got id=%d", hits[0].ID)
	}
	for i, h := range hits {
		if h.Rank != i+1 {
			t.Fatalf("hit %d: want rank %d, got %d", i, i+1, h.Rank)
		}
		if h.Similarity < 0 || h.Similarity > 1 {
			t.Fatalf("similarity out of [0,1]: %f", h.Similarity)
		}
		if i > 0 && h.Similarity > hits[i-1].Similarity {
			t.Fatalf("similarity not descending: %f after %f", h.Similarity, hits[i-1].Similarity)
		}
	}
	if hits[0].Similarity < 0.99 {
		t.Fatalf("exact match should score ~1.0, got %f", hits[0].Similarity)
	}

	// Floor at 0.95 keeps only the exact match (near≈0.8, ortho≈0 are cut).
	hits, err = st.Search(ctx, proj, query, "q", 5, nil, 0.95)
	if err != nil {
		t.Fatalf("search with floor: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != exact {
		t.Fatalf("floor 0.95 should keep only the exact match, got %+v", hits)
	}

	// A floor above every match returns nothing rather than padding with
	// the nearest-but-weak hit — the whole point of the similarity floor.
	hits, err = st.Search(ctx, proj, []float32{0, 0, 0, 1}, "unrelated", 5, nil, 0.5)
	if err != nil {
		t.Fatalf("search no-match: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("floor should yield zero hits for an unrelated query, got %d", len(hits))
	}
}

func TestRetrievalAnalyticsGrouping(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "ret.db"), "stub:test", 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	const proj = "/p"

	id1, err := st.Insert(ctx, "", proj, "the auth memory", nil, []float32{1, 0, 0, 0}, "gotcha")
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	// cos([1,0,0,0],[0.6,0.8,0,0]) = 0.6, so a 0.8 floor cleanly drops it.
	id2, err := st.Insert(ctx, "", proj, "second memory", nil, []float32{0.6, 0.8, 0, 0}, "")
	if err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	// (1) No floor: one retrieve call → one group, both memories injected,
	// rank-ordered, each carrying the query/similarity from the event.
	if _, err := st.Search(ctx, proj, []float32{1, 0, 0, 0}, "how does auth work", 5, nil, 0); err != nil {
		t.Fatalf("search: %v", err)
	}
	groups, err := st.ListRetrievals(ctx, proj, 10)
	if err != nil {
		t.Fatalf("list retrievals: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("one retrieve call should be one group, got %d", len(groups))
	}
	g := groups[0]
	if g.Query != "how does auth work" {
		t.Fatalf("group query not decoded from metadata: %q", g.Query)
	}
	if g.RecallID == "" {
		t.Fatalf("group should carry a recall_id")
	}
	if len(g.Memories) != 2 {
		t.Fatalf("want 2 memories in the group, got %d", len(g.Memories))
	}
	if g.Memories[0].Rank != 1 || g.Memories[1].Rank != 2 {
		t.Fatalf("memories not rank-ordered: %+v", g.Memories)
	}
	if g.Memories[0].MemoryID != id1 {
		t.Fatalf("rank-1 should be the closest memory %d, got %d", id1, g.Memories[0].MemoryID)
	}
	if g.Memories[0].Content != "the auth memory" {
		t.Fatalf("memory content not joined: %q", g.Memories[0].Content)
	}
	if g.Memories[0].Similarity <= 0 {
		t.Fatalf("similarity should be > 0, got %f", g.Memories[0].Similarity)
	}
	if !g.Memories[0].Injected || !g.Memories[1].Injected {
		t.Fatalf("with no floor both candidates should be injected: %+v", g.Memories)
	}

	// (2) A floor drops the weaker candidate. The agent only gets id1, but the
	// dropped near-miss (id2) still appears in the group, flagged !Injected.
	hits, err := st.Search(ctx, proj, []float32{1, 0, 0, 0}, "auth with floor", 5, nil, 0.8)
	if err != nil {
		t.Fatalf("search with floor: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != id1 {
		t.Fatalf("floor 0.8 should return only id1 to the caller, got %+v", hits)
	}
	groups, _ = st.ListRetrievals(ctx, proj, 10)
	if len(groups) != 2 {
		t.Fatalf("two retrieve calls should yield two groups, got %d", len(groups))
	}
	gf := groups[0] // newest first
	if gf.Query != "auth with floor" {
		t.Fatalf("newest group should sort first, got %q", gf.Query)
	}
	if len(gf.Memories) != 2 {
		t.Fatalf("group should record both candidates (injected + dropped), got %d", len(gf.Memories))
	}
	var inj, drop *RetrievalMemory
	for i := range gf.Memories {
		switch gf.Memories[i].MemoryID {
		case id1:
			inj = &gf.Memories[i]
		case id2:
			drop = &gf.Memories[i]
		}
	}
	if inj == nil || !inj.Injected {
		t.Fatalf("id1 should be present and injected: %+v", gf.Memories)
	}
	if drop == nil || drop.Injected {
		t.Fatalf("id2 should be present and flagged dropped (not injected): %+v", gf.Memories)
	}
}
