package server

import (
	"context"
	"fmt"
	"os"

	"github.com/tomanagle/yullu/internal/memlog"
	"github.com/tomanagle/yullu/internal/scope"
)

// ReconcileResult summarizes what a reconcile pass did. The numbers are
// surfaced via the reconcile_memories MCP tool and the startup log line.
type ReconcileResult struct {
	ProjectID          string   `json:"project_id"`
	EventsScanned      int      `json:"events_scanned"`
	Created            int      `json:"created"`
	Updated            int      `json:"updated"`
	Deleted            int      `json:"deleted"`
	EmbeddingsReused   int      `json:"embeddings_reused"`
	EmbeddingsComputed int      `json:"embeddings_computed"`
	LocalOnlyLogged    int      `json:"local_only_logged"`
	Watermark          string   `json:"watermark"`
	ParseErrors        []string `json:"parse_errors,omitempty"`
	NoEventDir         bool     `json:"no_event_dir,omitempty"`
}

// memoryState is the target state for a memory derived by scanning the event
// log: replay every new event for one UUID and ask "what should the local
// DB look like after this reconcile?"
//
// vectorsByModel only holds vectors that are still valid for the current
// content. Any content-changing event wipes the map (vectors for older
// content are stale by definition); subsequent vector-only events merge in.
type memoryState struct {
	contentSet     bool
	content        string
	tagsSet        bool
	tags           []string
	deleted        bool
	vectorsByModel map[string][]float32
}

// Reconcile pulls entries from .yullu/logs/ and applies any that haven't
// been processed yet for the current project. Also writes remember entries
// for any local memories that don't have one (e.g. pre-log rows from before
// sync was enabled), so the rest of the team can see them.
//
// Strategy: two passes over the event log.
//  1. Replay every event > watermark into an in-memory per-UUID state. This
//     collapses create+update+update into a single "target" state per memory
//     and tracks which model embeddings are still valid for the current
//     content (anything earlier than the last content change is dropped).
//  2. Apply the target state to the local DB, reusing a logged vector for
//     our embedder when available and falling back to a fresh local embed
//     otherwise. Fresh embeddings get published if log_embeddings is on,
//     so teammates using the same model can skip the work next time.
//
// Safe to call repeatedly: events <= the stored watermark are skipped, the
// apply step is idempotent (insert-or-update by UUID), and delete of a
// missing memory is a no-op.
func (s *Server) Reconcile(ctx context.Context) (*ReconcileResult, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve cwd: %w", err)
	}
	projectID, err := scope.Resolve(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolve project: %w", err)
	}
	res := &ReconcileResult{ProjectID: projectID}

	// Use per-project resolved sync config so a project that overrides
	// sync_dir (e.g. .memories instead of .yullu) reconciles from the
	// right directory. Global config is the fallback for unknown projects.
	syncCfg := s.resolveProject(projectID).Sync

	gitRoot := scope.GitRoot(cwd)
	if gitRoot == "" || !syncCfg.Enabled {
		res.NoEventDir = true
		return res, nil
	}

	reader := memlog.NewReader(gitRoot, syncCfg.Dir)
	entries, parseErrs, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	for _, pe := range parseErrs {
		res.ParseErrors = append(res.ParseErrors, pe.Error())
	}
	res.EventsScanned = len(entries)

	watermark, err := s.store.Watermark(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("read watermark: %w", err)
	}

	ourEmbedderID := s.embedder.ID()
	ourDim := s.embedder.Dim()

	// Pass 1: scan. Track every UUID that has ever had a create event (for
	// local-only-publish later). Build target state for events > watermark.
	knownCreates := make(map[string]bool, len(entries))
	states := make(map[string]*memoryState)
	uuidOrder := make([]string, 0)
	newWatermark := watermark

	for _, entry := range entries {
		e := entry.Event
		if e.Type == memlog.EventRemember {
			knownCreates[e.MemoryID] = true
		}
		if entry.Filename <= watermark {
			continue
		}
		newWatermark = entry.Filename

		st, exists := states[e.MemoryID]
		if !exists {
			st = &memoryState{}
			states[e.MemoryID] = st
			uuidOrder = append(uuidOrder, e.MemoryID)
		}
		s.applyEventToState(entry, st, ourEmbedderID, ourDim)
	}

	// Pass 2: apply target state per UUID.
	for _, uid := range uuidOrder {
		st := states[uid]
		if err := s.applyState(ctx, projectID, uid, st, res); err != nil {
			return res, fmt.Errorf("apply %s: %w", uid, err)
		}
	}

	// Backfill: any local row without a create event in the log needs one.
	locals, err := s.store.ListAll(ctx, projectID)
	if err != nil {
		return res, fmt.Errorf("list locals: %w", err)
	}
	for _, m := range locals {
		if knownCreates[m.UUID] || s.writer == nil {
			continue
		}
		// If sync logs embeddings and we have a local vector, ship it
		// inside the create event so teammates with the same model can
		// reuse it immediately.
		vectors := map[string][]float32(nil)
		if s.cfg.Sync.LogEmbeddings {
			vec, err := s.store.GetVector(ctx, m.ID)
			if err != nil {
				s.logger.Warn("read local vector failed", "uuid", m.UUID, "err", err.Error())
			} else if vec != nil {
				vectors = map[string][]float32{ourEmbedderID: vec}
			}
		}
		event := memlog.NewRememberEvent(m.UUID, m.Content, m.Tags, vectors)
		if err := s.writer.Write(event); err != nil {
			return res, fmt.Errorf("publish local-only memory %s: %w", m.UUID, err)
		}
		res.LocalOnlyLogged++
	}

	if newWatermark != watermark {
		if err := s.store.SetWatermark(ctx, projectID, newWatermark); err != nil {
			return res, fmt.Errorf("save watermark: %w", err)
		}
	}
	res.Watermark = newWatermark
	return res, nil
}

// applyEventToState folds one event into the per-memory state. Pulled out so
// the rules around content invalidating vectors are stated in one place.
//
// Rules:
//   - Create: content is set fresh, vectors map starts from this event's vectors.
//   - Update with content present: content change, prior vectors wiped, this
//     event's vectors become the new starting set.
//   - Update with no content: merge this event's vectors into the existing map.
//   - Delete: tombstone wins until a later create resurrects.
func (s *Server) applyEventToState(entry memlog.Entry, st *memoryState, ourEmbedderID string, ourDim int) {
	e := entry.Event
	switch e.Type {
	case memlog.EventRemember:
		st.deleted = false
		st.contentSet = true
		st.content = derefString(e.Content)
		st.tagsSet = true
		st.tags = derefStrings(e.Tags)
		st.vectorsByModel = s.acceptVectors(entry, e.Vectors, ourEmbedderID, ourDim, nil)
	case memlog.EventRevise:
		st.deleted = false
		if e.Content != nil {
			// Content change wipes old vectors.
			st.contentSet = true
			st.content = *e.Content
			st.vectorsByModel = s.acceptVectors(entry, e.Vectors, ourEmbedderID, ourDim, nil)
		} else if e.Vectors != nil {
			// No content change; merge new vectors into the existing map.
			st.vectorsByModel = s.acceptVectors(entry, e.Vectors, ourEmbedderID, ourDim, st.vectorsByModel)
		}
		if e.Tags != nil {
			st.tagsSet = true
			st.tags = *e.Tags
		}
	case memlog.EventForget:
		st.deleted = true
	}
}

// acceptVectors returns a new map that includes valid vectors from `incoming`
// merged over `base`. "Valid" means: reuse_embeddings is on (otherwise we
// ignore everything), the model is ours (other models are noise locally,
// though we preserve them in the event log so teammates on those models can
// use them), and the dim matches our embedder.
//
// Other models' vectors aren't stored in memoryState because reconcile only
// applies vectors that match the local embedder - but they remain in the
// event file on disk for teammates.
func (s *Server) acceptVectors(entry memlog.Entry, incoming map[string][]float32, ourEmbedderID string, ourDim int, base map[string][]float32) map[string][]float32 {
	if !s.cfg.Sync.ReuseEmbeddings {
		return base
	}
	vec, ok := incoming[ourEmbedderID]
	if !ok {
		return base
	}
	if len(vec) != ourDim {
		s.logger.Warn("ignoring vector with dim mismatch",
			"filename", entry.Filename,
			"model", ourEmbedderID,
			"event_dim", len(vec),
			"expected_dim", ourDim,
		)
		return base
	}
	out := base
	if out == nil {
		out = make(map[string][]float32, 1)
	}
	out[ourEmbedderID] = vec
	return out
}

// applyState reconciles the local DB with the target state for one memory.
// Touches the embedder only when we don't have a usable logged vector.
func (s *Server) applyState(ctx context.Context, projectID, memoryUUID string, st *memoryState, res *ReconcileResult) error {
	existing, err := s.store.GetByUUID(ctx, memoryUUID)
	if err != nil {
		return err
	}

	if st.deleted {
		if existing == nil {
			return nil
		}
		if err := s.store.Delete(ctx, existing.ID); err != nil {
			return err
		}
		res.Deleted++
		return nil
	}

	// Resolve content/tags: prefer the new state, fall back to existing.
	content := st.content
	if !st.contentSet {
		if existing != nil {
			content = existing.Content
		}
	}
	tags := st.tags
	if !st.tagsSet {
		if existing != nil {
			tags = existing.Tags
		}
	}

	// Resolve vector.
	ourEmbedderID := s.embedder.ID()
	contentChanged := st.contentSet
	needFreshVector := existing == nil || contentChanged

	var vec []float32
	if needFreshVector {
		if v, ok := st.vectorsByModel[ourEmbedderID]; ok {
			vec = v
			res.EmbeddingsReused++
		} else {
			vecs, err := s.embedder.Embed(ctx, []string{content})
			if err != nil {
				return fmt.Errorf("embed: %w", err)
			}
			vec = vecs[0]
			res.EmbeddingsComputed++
			s.publishOwnVector(memoryUUID, vec)
		}
	} else if v, ok := st.vectorsByModel[ourEmbedderID]; ok {
		// Content didn't change but a vector for our model arrived - refresh.
		vec = v
		res.EmbeddingsReused++
	}

	if existing == nil {
		if _, err := s.store.Insert(ctx, memoryUUID, projectID, content, tags, vec, ""); err != nil {
			return err
		}
		res.Created++
		return nil
	}

	var contentPtr *string
	var tagsPtr *[]string
	if st.contentSet {
		contentPtr = &content
	}
	if st.tagsSet {
		tagsPtr = &tags
	}
	if contentPtr == nil && tagsPtr == nil && vec == nil {
		return nil
	}
	if err := s.store.Update(ctx, existing.ID, contentPtr, tagsPtr, vec); err != nil {
		return err
	}
	res.Updated++
	return nil
}

// publishOwnVector writes a vector-only update event so teammates on the same
// model can skip the embed call. No-op when logging is off or the writer is
// unavailable.
func (s *Server) publishOwnVector(memoryUUID string, vec []float32) {
	if !s.cfg.Sync.LogEmbeddings || s.writer == nil {
		return
	}
	event := memlog.NewVectorEvent(memoryUUID, map[string][]float32{s.embedder.ID(): vec})
	if err := s.writer.Write(event); err != nil {
		// Vector events are an optimization; failing to log one shouldn't
		// abort the operation that produced it.
		s.logger.Warn("write vector event failed", "uuid", memoryUUID, "err", err.Error())
	}
}

// LogReconcile runs Reconcile and logs the result. Convenience wrapper for
// startup auto-reconcile so main.go stays focused on dependency wiring.
func (s *Server) LogReconcile(ctx context.Context) {
	res, err := s.Reconcile(ctx)
	if err != nil {
		s.logger.Error("reconcile failed", "err", err.Error())
		return
	}
	if res.NoEventDir {
		return
	}
	s.logger.Info("reconcile completed",
		"project_id", res.ProjectID,
		"events_scanned", res.EventsScanned,
		"created", res.Created,
		"updated", res.Updated,
		"deleted", res.Deleted,
		"embeddings_reused", res.EmbeddingsReused,
		"embeddings_computed", res.EmbeddingsComputed,
		"local_only_logged", res.LocalOnlyLogged,
		"watermark", res.Watermark,
	)
	for _, pe := range res.ParseErrors {
		s.logger.Warn("reconcile parse error", "detail", pe)
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefStrings(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}
