// Package store persists memories and their embeddings in SQLite + sqlite-vec.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"

	"github.com/tomanagle/yullu/internal/ai"
)

func init() {
	// Register sqlite-vec on every SQLite connection opened in this process.
	vec.Auto()
}

// Memory is a stored note about a codebase.
//
// ID is the local autoincrement primary key - fast for joins to memory_vectors
// but not portable across machines.
// UUID is the stable, machine-independent identifier used in the event log
// (.yullu/logs). All cross-machine references use the UUID.
type Memory struct {
	ID        int64     `json:"id"`
	UUID      string    `json:"uuid"`
	ProjectID string    `json:"project_id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Score     float64   `json:"score,omitempty"` // populated only by Search
	// Rating is the user-assigned quality score (1–10) for the review queue.
	// Nil means un-rated. Memories rated ≤ 5 are moved to rejected_memories
	// and removed from this table — so a non-nil Rating here is always ≥ 6.
	Rating        *int   `json:"rating,omitempty"`
	RatingComment string `json:"rating_comment,omitempty"`
	// Category groups memories by the shape of fact they carry — process,
	// decision, gotcha, domain, style. Lets the agent ask for only the
	// categories relevant to its current task instead of fetching the
	// whole pool. Empty means "not yet classified" (pre-category memories
	// or a dream pass that emitted an unknown value).
	Category MemoryCategory `json:"category,omitempty"`
}

// MemoryCategory is the content-shape axis used by the agent at retrieval
// time. The set is deliberately small (5) so the agent can hold all of
// them in working memory and so the user doesn't face decision fatigue
// when reviewing. Memories that don't fit any of these should usually be
// dropped, not invented into a new category.
type MemoryCategory string

const (
	// CategoryProcess: how to do things in this repo — commands, file
	// layout, naming, where new code goes, testing recipes.
	CategoryProcess MemoryCategory = "process"
	// CategoryDecision: why we made the choices we made — architectural
	// trade-offs, rejected alternatives, "we tried X and went back to Y".
	CategoryDecision MemoryCategory = "decision"
	// CategoryGotcha: what bites — non-obvious constraints, API quirks,
	// "must always X or it breaks", concurrency rules, performance traps.
	CategoryGotcha MemoryCategory = "gotcha"
	// CategoryDomain: what words mean here — glossary terms, business
	// invariants, entity relationships, domain-specific semantics.
	CategoryDomain MemoryCategory = "domain"
	// CategoryStyle: what the project looks and feels like — UI component
	// patterns, copy tone, accessibility rules, visual language.
	CategoryStyle MemoryCategory = "style"
)

// IsValidCategory reports whether c is one of the canonical categories.
// Used by the dream pipeline to filter out reasoner-emitted values that
// don't match the enum (we store empty rather than the invalid string so
// retrieval queries don't have to defend against typos).
func IsValidCategory(c MemoryCategory) bool {
	switch c {
	case CategoryProcess, CategoryDecision, CategoryGotcha, CategoryDomain, CategoryStyle:
		return true
	}
	return false
}

// RejectedMemory is a memory the user scored ≤ 5. The row that was in
// `memories` is gone; this is the anti-example signal we replay into
// future dream prompts.
type RejectedMemory struct {
	ID         int64     `json:"id"`
	ProjectID  string    `json:"project_id"`
	Content    string    `json:"content"`
	Tags       []string  `json:"tags,omitempty"`
	Rating     int       `json:"rating"`
	Comment    string    `json:"comment,omitempty"`
	RejectedAt time.Time `json:"rejected_at"`
}

// Store wraps the database and the configured embedding dimension.
type Store struct {
	db                   *sql.DB
	dim                  int
	recordMemoryEventErr error // last error from recordMemoryEvent (best-effort logging)
}

// OpenReadOnly opens an existing store without requiring the caller to
// supply an embedder. The embedder ID + dim are read from the meta table
// that the writer-side Open populates. Use this from CLI subcommands
// that only need to query memories (export, dump, etc.) so the user
// doesn't have to have a working API key just to read what's already
// in the DB.
//
// Errors if the file doesn't exist or has no meta yet — there's no
// point reading a DB that was never written to.
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open store for read: %w", err)
	}
	dsn := "file:" + path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&mode=ro"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}

	ctx := context.Background()
	// Probe the meta table directly. If the file isn't a real Yul'lu DB
	// (or hasn't been initialised yet) this errors cleanly.
	storedDimStr, ok, err := s.getMeta(ctx, "embed_dim")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read meta.embed_dim: %w", err)
	}
	if !ok {
		_ = db.Close()
		return nil, fmt.Errorf("db at %s has no embedder identity yet — has anything been stored?", path)
	}
	var dim int
	if _, err := fmt.Sscanf(storedDimStr, "%d", &dim); err != nil || dim <= 0 {
		_ = db.Close()
		return nil, fmt.Errorf("db meta.embed_dim is malformed (%q)", storedDimStr)
	}
	s.dim = dim
	return s, nil
}

// MustOpen opens the store and panics on failure. Use in process startup
// where a working store is required for the service to function.
func MustOpen(path string, embedderID string, embedderDim int) *Store {
	s, err := Open(path, embedderID, embedderDim)
	if err != nil {
		panic(fmt.Errorf("open store at %s: %w", path, err))
	}
	return s
}

// Open opens or creates the SQLite database at path, ensures schema, and
// validates that embedderID matches the one previously used (if any).
// embedderDim must be the vector dimensionality the caller will write.
func Open(path string, embedderID string, embedderDim int) (*Store, error) {
	if embedderDim <= 0 {
		return nil, fmt.Errorf("embedder dimension must be > 0, got %d", embedderDim)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	dsn := "file:" + path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; sqlite-vec virtual tables don't love concurrent writers

	s := &Store{db: db, dim: embedderDim}
	if err := s.init(embedderID); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init(embedderID string) error {
	ctx := context.Background()

	// Probe basic connectivity first so a disk/permissions failure doesn't
	// get mis-reported as "sqlite-vec missing". SQLite opens the file lazily
	// on first query - this is where you'd see "disk I/O error: no such
	// file or directory" if the path is wrong or unwritable.
	var ping int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&ping); err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	// Now confirm sqlite-vec is loaded. A failure here means the binary was
	// built without cgo, or the asg017/sqlite-vec-go-bindings auto-extension
	// didn't register.
	var vecVersion string
	if err := s.db.QueryRowContext(ctx, "SELECT vec_version()").Scan(&vecVersion); err != nil {
		return fmt.Errorf("sqlite-vec extension not loaded: %w", err)
	}

	schema := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid TEXT,
			project_id TEXT NOT NULL,
			content TEXT NOT NULL,
			tags_json TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			rating INTEGER,
			rating_comment TEXT,
			category TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_project ON memories(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated ON memories(project_id, updated_at DESC)`,
		// Note: idx_memories_project_category needs the category column,
		// which is added via ALTER below for older DBs. CREATE INDEX runs
		// AFTER the ALTER migration. Don't move it into this block — on a
		// pre-category DB, the index creation would fail before the
		// column exists.
		// Negative training signal for the dream prompt. When a user rates a
		// memory ≤ 5, the row is moved here and the original is deleted —
		// the memory is gone from retrieval / dream context, but the example
		// + the user's reason live on as anti-examples in future dream
		// passes. `rejected_at` is unix-ms so it sorts alongside everything
		// else in this DB.
		`CREATE TABLE IF NOT EXISTS rejected_memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT NOT NULL,
			content TEXT NOT NULL,
			tags_json TEXT NOT NULL DEFAULT '[]',
			rating INTEGER NOT NULL,
			comment TEXT,
			rejected_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rejected_project_at ON rejected_memories(project_id, rejected_at DESC)`,
		`CREATE TABLE IF NOT EXISTS usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			kind TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cost_microcents_usd INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			items INTEGER NOT NULL DEFAULT 0,
			ok INTEGER NOT NULL DEFAULT 1,
			error_msg TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_at ON usage(at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_provider_model ON usage(provider, model)`,
		// The dream buffer: LLM-recorded conversation turns awaiting processing.
		// Never published to .yullu/logs/ - raw conversation may be private.
		// Rows are deleted once dreaming has extracted memories from them.
		`CREATE TABLE IF NOT EXISTS session_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_messages_session ON session_messages(session_id, at)`,
		`CREATE INDEX IF NOT EXISTS idx_session_messages_project ON session_messages(project_id, at)`,
		// Memory lifecycle event log: created / updated / deleted / recalled,
		// scoped to a project. Local-only (never sync'd) - pure observability.
		// Driven by Insert/Update/Delete/Search/SearchText via recordMemoryEvent.
		`CREATE TABLE IF NOT EXISTS memory_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			memory_id INTEGER,
			project_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			metadata_json TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_events_at ON memory_events(at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_events_kind ON memory_events(kind, at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_events_memory ON memory_events(memory_id, at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_events_project ON memory_events(project_id, at DESC)`,
		// Dream-pass log: one row per non-skipped dream run, recording the
		// counts the reasoner produced. Used by the Stats dashboard to show
		// "how active is the dreamer" without needing to scan memory_events.
		// Local-only - never sync'd. `at` is a unix milli timestamp so
		// integer comparisons match the surrounding tables.
		`CREATE TABLE IF NOT EXISTS dream_passes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			project_id TEXT NOT NULL,
			sessions_processed INTEGER NOT NULL DEFAULT 0,
			messages_processed INTEGER NOT NULL DEFAULT 0,
			ops_created INTEGER NOT NULL DEFAULT 0,
			ops_updated INTEGER NOT NULL DEFAULT 0,
			ops_deleted INTEGER NOT NULL DEFAULT 0,
			ops_skipped INTEGER NOT NULL DEFAULT 0,
			errors_json TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dream_passes_at ON dream_passes(at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_dream_passes_project ON dream_passes(project_id, at DESC)`,
		// Full-text search index over memory content + tags. Free, local,
		// BM25-ranked. Backfilled from `memories` on first creation; kept in
		// sync by the triggers below.
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(content, tags)`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, content, tags) VALUES (new.id, new.content, new.tags_json);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_au AFTER UPDATE ON memories BEGIN
			UPDATE memories_fts SET content = new.content, tags = new.tags_json WHERE rowid = new.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_ad AFTER DELETE ON memories BEGIN
			DELETE FROM memories_fts WHERE rowid = old.id;
		END`,
	}
	for _, q := range schema {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("apply schema: %w: %s", err, q)
		}
	}

	// Migrate older DBs: add the uuid column, backfill, then enforce uniqueness.
	if err := s.migrateMemoryUUIDs(ctx); err != nil {
		return err
	}

	// Migrate older DBs: add the rating + category columns. CREATE TABLE
	// IF NOT EXISTS won't add new columns to an existing table, so we
	// run ALTER idempotently here. SQLite errors with "duplicate column
	// name" when the column already exists — swallowed via
	// columnAlreadyExists.
	for _, alter := range []string{
		`ALTER TABLE memories ADD COLUMN rating INTEGER`,
		`ALTER TABLE memories ADD COLUMN rating_comment TEXT`,
		`ALTER TABLE memories ADD COLUMN category TEXT`,
	} {
		if _, err := s.db.ExecContext(ctx, alter); err != nil && !columnAlreadyExists(err) {
			return fmt.Errorf("migrate memory columns: %w: %s", err, alter)
		}
	}

	// Indexes that depend on migrated columns. CREATE INDEX IF NOT EXISTS
	// is idempotent; safe to run on every boot. Must run AFTER the ALTER
	// migrations above — on a pre-category DB the column doesn't exist
	// until the ALTER runs, and CREATE INDEX over a missing column
	// errors immediately.
	postMigrationIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_project_category ON memories(project_id, category)`,
	}
	for _, idx := range postMigrationIndexes {
		if _, err := s.db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("apply post-migration index: %w: %s", err, idx)
		}
	}

	// Populate the FTS index on first run (the virtual table was just
	// created and the triggers will keep new rows in sync, but existing
	// memories need a one-shot backfill).
	if err := s.populateFTSIfEmpty(ctx); err != nil {
		return err
	}

	// Migrate older DBs: convert cost_usd (float dollars) to cost_microcents_usd
	// (int64 microcents). Storing currency as float was prone to drift on
	// large aggregations; microcents keeps storage exact while preserving
	// sub-cent precision for individual calls.
	if err := s.migrateUsageCostToMicrocents(ctx); err != nil {
		return err
	}

	// The vec0 virtual table is dimension-bound at creation. Check meta first
	// so we can refuse a mismatching configuration with a clear error.
	storedDim, dimOK, err := s.getMeta(ctx, "embed_dim")
	if err != nil {
		return err
	}
	storedID, idOK, err := s.getMeta(ctx, "embed_id")
	if err != nil {
		return err
	}
	if dimOK && storedDim != fmt.Sprintf("%d", s.dim) {
		return fmt.Errorf("embedding dimension mismatch: db was created with dim=%s, current embedder produces dim=%d. "+
			"Delete the db or switch back to the original embedder", storedDim, s.dim)
	}
	if idOK && storedID != embedderID {
		return fmt.Errorf("embedding model mismatch: db was created with %q, current embedder is %q. "+
			"Delete the db or switch back to the original model", storedID, embedderID)
	}

	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_vectors USING vec0(
			memory_id INTEGER PRIMARY KEY,
			embedding FLOAT[%d]
		)`, s.dim)); err != nil {
		return fmt.Errorf("create memory_vectors: %w", err)
	}

	if !dimOK {
		if err := s.setMeta(ctx, "embed_dim", fmt.Sprintf("%d", s.dim)); err != nil {
			return err
		}
	}
	if !idOK {
		if err := s.setMeta(ctx, "embed_id", embedderID); err != nil {
			return err
		}
	}
	return nil
}

// migrateMemoryUUIDs is idempotent: it adds the uuid column on older DBs,
// backfills any row missing one, then enforces uniqueness via an index.
// Safe to run on every open.
func (s *Store) migrateMemoryUUIDs(ctx context.Context) error {
	hasUUID, err := s.columnExists(ctx, "memories", "uuid")
	if err != nil {
		return fmt.Errorf("inspect memories: %w", err)
	}
	if !hasUUID {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE memories ADD COLUMN uuid TEXT`); err != nil {
			return fmt.Errorf("add uuid column: %w", err)
		}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM memories WHERE uuid IS NULL OR uuid = ''`)
	if err != nil {
		return fmt.Errorf("find memories to backfill: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if len(ids) > 0 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		stmt, err := tx.PrepareContext(ctx, `UPDATE memories SET uuid = ? WHERE id = ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, id := range ids {
			if _, err := stmt.ExecContext(ctx, uuid.NewString(), id); err != nil {
				return fmt.Errorf("backfill uuid for memory %d: %w", id, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_uuid ON memories(uuid)`); err != nil {
		return fmt.Errorf("create uuid index: %w", err)
	}
	return nil
}

// populateFTSIfEmpty backfills memories_fts from `memories` the first time
// the FTS table is created. Subsequent runs find it non-empty (triggers
// keep it in sync) and no-op.
func (s *Store) populateFTSIfEmpty(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories_fts`).Scan(&count); err != nil {
		return fmt.Errorf("check memories_fts count: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memories_fts(rowid, content, tags)
		 SELECT id, content, tags_json FROM memories`); err != nil {
		return fmt.Errorf("backfill memories_fts: %w", err)
	}
	return nil
}

// migrateUsageCostToMicrocents adds cost_microcents_usd if missing, copies
// any existing cost_usd values (multiplying by 10⁸), then drops the legacy
// column. Idempotent: on a fresh DB the new column already exists and the
// old one doesn't, so the function is a no-op.
func (s *Store) migrateUsageCostToMicrocents(ctx context.Context) error {
	hasNew, err := s.columnExists(ctx, "usage", "cost_microcents_usd")
	if err != nil {
		return fmt.Errorf("inspect usage: %w", err)
	}
	if !hasNew {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE usage ADD COLUMN cost_microcents_usd INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add cost_microcents_usd: %w", err)
		}
	}
	hasOld, err := s.columnExists(ctx, "usage", "cost_usd")
	if err != nil {
		return err
	}
	if !hasOld {
		return nil
	}
	// 1 USD = 10⁸ microcents. ROUND first to keep us honest about float drift.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE usage SET cost_microcents_usd = CAST(ROUND(cost_usd * 100000000) AS INTEGER)
		 WHERE cost_microcents_usd = 0 AND cost_usd > 0`); err != nil {
		return fmt.Errorf("backfill cost_microcents_usd: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE usage DROP COLUMN cost_usd`); err != nil {
		return fmt.Errorf("drop cost_usd: %w", err)
	}
	return nil
}

func (s *Store) columnExists(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Watermark returns the last event filename processed for the given project,
// or "" if no reconcile has run yet for this project. Filenames in
// .yullu/logs/ sort lexically by time, so callers can skip any event
// file <= the watermark on the next reconcile pass.
func (s *Store) Watermark(ctx context.Context, projectID string) (string, error) {
	v, _, err := s.getMeta(ctx, watermarkKey(projectID))
	return v, err
}

// SetWatermark records that reconcile has processed up to and including the
// given event filename for projectID.
func (s *Store) SetWatermark(ctx context.Context, projectID, filename string) error {
	return s.setMeta(ctx, watermarkKey(projectID), filename)
}

func watermarkKey(projectID string) string {
	return "last_event:" + projectID
}

func (s *Store) getMeta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *Store) setMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)", key, value)
	return err
}

// Insert stores a memory and its embedding under the given UUID, returning
// the assigned local ID. memoryUUID is the cross-machine identifier that
// also appears in event log entries - callers (the MCP handler, the event
// applier) generate it before writing the corresponding create event so the
// event and the row stay correlated. Pass "" to have one generated.
func (s *Store) Insert(ctx context.Context, memoryUUID, projectID, content string, tags []string, vector []float32, category MemoryCategory) (int64, error) {
	if len(vector) != s.dim {
		return 0, fmt.Errorf("vector dim %d != expected %d", len(vector), s.dim)
	}
	if memoryUUID == "" {
		memoryUUID = uuid.NewString()
	}
	tagsJSON, err := json.Marshal(defaultStrings(tags))
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()

	// Refuse to store an unknown category — better to write NULL and
	// surface the memory as "uncategorised" in the Review queue than to
	// pollute the enum with reasoner typos.
	var catCol sql.NullString
	if category != "" && IsValidCategory(category) {
		catCol = sql.NullString{String: string(category), Valid: true}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO memories(uuid, project_id, content, tags_json, created_at, updated_at, category) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		memoryUUID, projectID, content, string(tagsJSON), now, now, catCol)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	blob, err := vec.SerializeFloat32(vector)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_vectors(memory_id, embedding) VALUES (?, ?)`, id, blob); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.recordMemoryEvent(ctx, EventCreated, id, projectID, nil)
	return id, nil
}

// Update modifies a memory's content/tags and replaces its embedding.
// If newVector is nil, the embedding is left untouched.
func (s *Store) Update(ctx context.Context, id int64, content *string, tags *[]string, newVector []float32) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		existingContent string
		existingTags    string
		projectID       string
	)
	if err := tx.QueryRowContext(ctx, `SELECT content, tags_json, project_id FROM memories WHERE id = ?`, id).
		Scan(&existingContent, &existingTags, &projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("memory %d not found", id)
		}
		return err
	}
	if content != nil {
		existingContent = *content
	}
	if tags != nil {
		b, err := json.Marshal(defaultStrings(*tags))
		if err != nil {
			return err
		}
		existingTags = string(b)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE memories SET content = ?, tags_json = ?, updated_at = ? WHERE id = ?`,
		existingContent, existingTags, time.Now().Unix(), id); err != nil {
		return err
	}
	if newVector != nil {
		if len(newVector) != s.dim {
			return fmt.Errorf("vector dim %d != expected %d", len(newVector), s.dim)
		}
		blob, err := vec.SerializeFloat32(newVector)
		if err != nil {
			return err
		}
		// vec0 doesn't support UPDATE in older versions; replace via delete+insert.
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_vectors WHERE memory_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memory_vectors(memory_id, embedding) VALUES (?, ?)`, id, blob); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.recordMemoryEvent(ctx, EventUpdated, id, projectID, nil)
	return nil
}

// Delete removes a memory and its embedding.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Capture project_id before the row goes away so we can record the
	// deleted event with the right scope.
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM memories WHERE id = ?`, id).
		Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("memory %d not found", id)
		}
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_vectors WHERE memory_id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.recordMemoryEvent(ctx, EventDeleted, id, projectID, nil)
	return nil
}

// GetByUUID returns a single memory by its cross-machine UUID, or
// (nil, nil) if no such memory exists locally. Used by reconcile to apply
// events keyed on UUID without round-tripping through int IDs.
func (s *Store) GetByUUID(ctx context.Context, memoryUUID string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, uuid, project_id, content, tags_json, created_at, updated_at, rating, rating_comment, category FROM memories WHERE uuid = ?`, memoryUUID)
	m, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

// Get returns a single memory by ID.
func (s *Store) Get(ctx context.Context, id int64) (*Memory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, uuid, project_id, content, tags_json, created_at, updated_at, rating, rating_comment, category FROM memories WHERE id = ?`, id)
	m, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("memory %d not found", id)
		}
		return nil, err
	}
	return m, nil
}

// ListProjects returns every project_id that has at least one memory.
// Used by the desktop app to populate a project picker.
// ListProjects returns every project_id that has any activity recorded
// against it — memories OR buffered session messages OR completed dream
// passes. We union across all three sources so a project shows up in the
// sidebar the moment the user starts working in it, not just after the
// first dream pass creates a memory.
//
// Previously this only queried `memories`, which meant a freshly-recording
// project (lots of buffered turns, no dreams yet) was invisible — the
// sidebar dropdown was empty while the Dream buffer card showed messages.
func (s *Store) ListProjects(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id FROM memories
		UNION
		SELECT project_id FROM session_messages
		UNION
		SELECT project_id FROM dream_passes
		ORDER BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListAll returns every memory for projectID, oldest first. Used by reconcile
// to find local-only rows that need publishing. Ordering is chronological so
// older memories get earlier filenames when their create events are written.
func (s *Store) ListAll(ctx context.Context, projectID string) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, uuid, project_id, content, tags_json, created_at, updated_at, rating, rating_comment, category
		 FROM memories WHERE project_id = ? ORDER BY created_at ASC, id ASC`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// List returns the most recently updated memories for projectID, or across
// every project when projectID is empty. The empty-projectID path is the
// "show me everything" view used by the desktop UI.
func (s *Store) List(ctx context.Context, projectID string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	var (
		rows *sql.Rows
		err  error
	)
	if projectID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, uuid, project_id, content, tags_json, created_at, updated_at, rating, rating_comment, category
			 FROM memories ORDER BY updated_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, uuid, project_id, content, tags_json, created_at, updated_at, rating, rating_comment, category
			 FROM memories WHERE project_id = ? ORDER BY updated_at DESC LIMIT ?`,
			projectID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// ListUnrated returns memories awaiting a user rating, newest first.
// Powers the dedicated "Review" queue — the only memories the UI shows
// here are ones the user hasn't explicitly scored yet. Memories that
// have been rated (and survived, i.e. > 5) drop out of this list.
func (s *Store) ListUnrated(ctx context.Context, projectID string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, uuid, project_id, content, tags_json, created_at, updated_at, rating, rating_comment, category
		 FROM memories
		 WHERE project_id = ? AND rating IS NULL
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// RateMemory applies a user rating. Two branches:
//
//   - rating ≤ 5 → the memory is a bad example. Copy it to
//     rejected_memories (preserving the comment so future dream passes
//     can show it as an anti-example), then DELETE the row from
//     `memories`. The cascade triggers on memories delete the FTS row
//     and we manually delete the vector below.
//   - rating ≥ 6 → write rating + comment onto the existing row.
//
// Both paths run in a single transaction so a partial failure leaves
// the DB consistent.
func (s *Store) RateMemory(ctx context.Context, id int64, rating int, comment string) error {
	if rating < 1 || rating > 10 {
		return fmt.Errorf("rating must be 1..10, got %d", rating)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if rating > 5 {
		res, err := tx.ExecContext(ctx,
			`UPDATE memories SET rating = ?, rating_comment = ?, updated_at = ?
			 WHERE id = ?`,
			rating, comment, time.Now().Unix(), id)
		if err != nil {
			return fmt.Errorf("update rating: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("memory %d not found", id)
		}
		return tx.Commit()
	}

	// rating ≤ 5: rejection path. Read the row, copy to rejected_memories,
	// delete from memories + memory_vectors (the FTS triggers handle their
	// own cleanup). project_id is needed for the rejected table scope.
	var (
		projectID string
		content   string
		tagsJSON  string
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT project_id, content, tags_json FROM memories WHERE id = ?`, id,
	).Scan(&projectID, &content, &tagsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("memory %d not found", id)
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rejected_memories(project_id, content, tags_json, rating, comment, rejected_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, content, tagsJSON, rating, comment, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("archive rejection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_vectors WHERE memory_id = ?`, id); err != nil {
		return fmt.Errorf("delete vector: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Log the rejection as a "deleted" memory event so the stats dashboard
	// counts it alongside other deletions. The rejection table is the
	// durable record; memory_events is the observability stream.
	s.recordMemoryEvent(ctx, EventDeleted, id, projectID,
		map[string]any{"via": "rating", "rating": rating})
	return nil
}

// RecentRejected returns the N most recently rejected memories for the
// project, newest first. The dream reasoner injects these into its user
// prompt as concrete "do not produce memories like these" examples so
// the bad-example signal flows back into future passes.
func (s *Store) RecentRejected(ctx context.Context, projectID string, limit int) ([]RejectedMemory, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, content, tags_json, rating, comment, rejected_at
		 FROM rejected_memories
		 WHERE project_id = ?
		 ORDER BY rejected_at DESC, id DESC
		 LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RejectedMemory
	for rows.Next() {
		var (
			r        RejectedMemory
			tagsJSON string
			at       int64
			comment  sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Content, &tagsJSON, &r.Rating, &comment, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
		if comment.Valid {
			r.Comment = comment.String
		}
		r.RejectedAt = time.Unix(at, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetVector returns the stored embedding for memoryID, or nil if the row has
// no associated vector. Used by reconcile to republish vectors for legacy
// local-only rows without re-embedding.
func (s *Store) GetVector(ctx context.Context, memoryID int64) ([]float32, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT embedding FROM memory_vectors WHERE memory_id = ?`, memoryID).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// sqlite-vec stores raw little-endian float32 bytes.
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("vector blob length %d is not a multiple of 4", len(blob))
	}
	out := make([]float32, len(blob)/4)
	for i := range out {
		bits := uint32(blob[i*4]) | uint32(blob[i*4+1])<<8 |
			uint32(blob[i*4+2])<<16 | uint32(blob[i*4+3])<<24
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}

// Search returns the top-k nearest memories for projectID by vector distance.
// Lower Score = closer (sqlite-vec returns L2 distance by default).
// SearchText runs a free, local, BM25-ranked full-text search against the
// memories_fts virtual table. Multi-word queries get tokenised and each
// term is prefix-matched ("react" matches "reactive"). Empty projectID
// searches across all projects. Returns empty (no error) if the query has
// no usable tokens.
func (s *Store) SearchText(ctx context.Context, projectID, query string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	ftsQuery := sanitizeFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	sqlText := `
		SELECT m.id, m.uuid, m.project_id, m.content, m.tags_json, m.created_at, m.updated_at, m.rating, m.rating_comment, m.category
		FROM memories_fts
		JOIN memories m ON m.id = memories_fts.rowid
		WHERE memories_fts MATCH ?`
	args := []any{ftsQuery}
	if projectID != "" {
		sqlText += ` AND m.project_id = ?`
		args = append(args, projectID)
	}
	sqlText += ` ORDER BY bm25(memories_fts) LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, m := range out {
		s.recordMemoryEvent(ctx, EventRecalled, m.ID, m.ProjectID,
			map[string]any{"via": "fts", "query": query})
	}
	return out, nil
}

// sanitizeFTSQuery turns user input into a safe FTS5 query string. Each
// whitespace-separated word is stripped of FTS-special characters,
// double-quoted, and given a prefix wildcard. Whitespace between terms is
// implicit AND in FTS5's default tokenizer.
func sanitizeFTSQuery(input string) string {
	var b strings.Builder
	first := true
	for _, field := range strings.Fields(input) {
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
				return r
			}
			return -1
		}, field)
		if clean == "" {
			continue
		}
		if !first {
			b.WriteByte(' ')
		}
		first = false
		b.WriteByte('"')
		b.WriteString(clean)
		b.WriteString(`"*`)
	}
	return b.String()
}

// Search runs the vector KNN query, optionally filtered by category.
// categories is an OR set — passing {process, style} returns memories
// matching EITHER. Empty means no filter (every category, including
// uncategorised). Invalid categories are silently dropped so a typo
// from the caller can't crash the request.
//
// Over-fetch logic: vec0 KNN doesn't honor WHERE clauses on joined
// tables efficiently, so we ask for more rows than k and filter in Go.
// When a category filter is active we over-fetch more aggressively
// because the top-k by vector distance might mostly be wrong-category;
// without the bump the user can ask for k=5 style memories and get 2
// back because 3 of the top-5 nearest were process memories.
func (s *Store) Search(ctx context.Context, projectID string, query []float32, k int, categories []MemoryCategory) ([]Memory, error) {
	if len(query) != s.dim {
		return nil, fmt.Errorf("query dim %d != expected %d", len(query), s.dim)
	}
	if k <= 0 {
		k = 5
	}
	blob, err := vec.SerializeFloat32(query)
	if err != nil {
		return nil, err
	}

	// Validate + dedupe categories. Invalid entries are dropped silently
	// so a caller passing a string like "style,bogus" still gets the
	// "style" results instead of a hard error.
	validCats := make([]MemoryCategory, 0, len(categories))
	seen := make(map[MemoryCategory]bool, len(categories))
	for _, c := range categories {
		if !IsValidCategory(c) || seen[c] {
			continue
		}
		seen[c] = true
		validCats = append(validCats, c)
	}

	// Over-fetch ceiling — bumped when filtering. The 8x factor with a
	// hard cap of 256 keeps the in-memory filter cheap even on a project
	// with thousands of memories.
	overK := k * 4
	if len(validCats) > 0 {
		overK = k * 8
	}
	if overK < 32 {
		overK = 32
	}
	if overK > 256 {
		overK = 256
	}

	// Build the SQL with an optional IN clause for category filter. vec0
	// requires the MATCH + k literal before our additional predicates.
	sqlText := `
		SELECT m.id, m.uuid, m.project_id, m.content, m.tags_json, m.created_at, m.updated_at, m.category, v.distance
		FROM memory_vectors v
		JOIN memories m ON m.id = v.memory_id
		WHERE v.embedding MATCH ?
		  AND m.project_id = ?
		  AND k = ?`
	args := []any{blob, projectID, overK}
	if len(validCats) > 0 {
		placeholders := make([]string, len(validCats))
		for i, c := range validCats {
			placeholders[i] = "?"
			args = append(args, string(c))
		}
		sqlText += ` AND m.category IN (` + strings.Join(placeholders, ",") + `)`
	}
	sqlText += `
		ORDER BY v.distance
		LIMIT ?`
	args = append(args, k)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var (
			m        Memory
			tagsJSON string
			created  int64
			updated  int64
			category sql.NullString
			dist     float64
		)
		if err := rows.Scan(&m.ID, &m.UUID, &m.ProjectID, &m.Content, &tagsJSON, &created, &updated, &category, &dist); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tagsJSON), &m.Tags)
		m.CreatedAt = time.Unix(created, 0)
		m.UpdatedAt = time.Unix(updated, 0)
		if category.Valid {
			m.Category = MemoryCategory(category.String)
		}
		m.Score = dist
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, m := range out {
		s.recordMemoryEvent(ctx, EventRecalled, m.ID, m.ProjectID,
			map[string]any{"via": "vector"})
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMemory(s scanner) (*Memory, error) {
	var (
		m         Memory
		tagsJSON  string
		created   int64
		updated   int64
		rating    sql.NullInt64
		ratingCmt sql.NullString
		category  sql.NullString
	)
	if err := s.Scan(&m.ID, &m.UUID, &m.ProjectID, &m.Content, &tagsJSON, &created, &updated, &rating, &ratingCmt, &category); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &m.Tags)
	m.CreatedAt = time.Unix(created, 0)
	m.UpdatedAt = time.Unix(updated, 0)
	if rating.Valid {
		r := int(rating.Int64)
		m.Rating = &r
	}
	if ratingCmt.Valid {
		m.RatingComment = ratingCmt.String
	}
	if category.Valid {
		m.Category = MemoryCategory(category.String)
	}
	return &m, nil
}

func defaultStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MemoryEventKind enumerates the lifecycle events we log to the
// memory_events table. The values are also the SQL `kind` strings.
type MemoryEventKind string

const (
	EventCreated  MemoryEventKind = "created"
	EventUpdated  MemoryEventKind = "updated"
	EventDeleted  MemoryEventKind = "deleted"
	EventRecalled MemoryEventKind = "recalled"
)

// recordMemoryEvent inserts a row into memory_events. Errors are swallowed -
// telemetry must never fail the underlying operation. Pass an empty metadata
// map to omit the JSON column.
func (s *Store) recordMemoryEvent(ctx context.Context, kind MemoryEventKind, memoryID int64, projectID string, metadata map[string]any) {
	var metaJSON sql.NullString
	if len(metadata) > 0 {
		if b, err := json.Marshal(metadata); err == nil {
			metaJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	var memArg sql.NullInt64
	if memoryID > 0 {
		memArg = sql.NullInt64{Int64: memoryID, Valid: true}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_events(at, memory_id, project_id, kind, metadata_json) VALUES (?, ?, ?, ?, ?)`,
		time.Now().Unix(), memArg, projectID, string(kind), metaJSON); err != nil {
		s.recordMemoryEventErr = err
	}
}

// SessionMessage is one stored conversation turn awaiting dreaming.
type SessionMessage struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	ProjectID string    `json:"project_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	At        time.Time `json:"at"`
}

// SessionMessageInput is the unstamped form used when callers append new
// turns. The store assigns ID and timestamp.
type SessionMessageInput struct {
	Role    string
	Content string
}

// MemoryStats summarises memory lifecycle activity for one project. Counts
// are bucketed for today (since midnight local), the last 7 days, and
// all-time. TopRecalled lists the most-frequently-recalled memories with
// their recall count.
type MemoryStats struct {
	ProjectID     string      `json:"project_id"`
	TotalMemories int         `json:"total_memories"`
	Counts        StatCounts  `json:"counts"`
	TopRecalled   []TopMemory `json:"top_recalled,omitempty"`
}

// StatCounts buckets event counts by kind across three time windows.
type StatCounts struct {
	CreatedDay   int `json:"created_day"`
	CreatedWeek  int `json:"created_week"`
	CreatedAll   int `json:"created_all"`
	UpdatedDay   int `json:"updated_day"`
	UpdatedWeek  int `json:"updated_week"`
	UpdatedAll   int `json:"updated_all"`
	DeletedDay   int `json:"deleted_day"`
	DeletedWeek  int `json:"deleted_week"`
	DeletedAll   int `json:"deleted_all"`
	RecalledDay  int `json:"recalled_day"`
	RecalledWeek int `json:"recalled_week"`
	RecalledAll  int `json:"recalled_all"`
}

// TopMemory pairs a memory with its recall count.
type TopMemory struct {
	Memory Memory `json:"memory"`
	Count  int    `json:"count"`
}

// GetMemoryStats returns aggregated event counts for projectID. Pass empty
// projectID to span every project. Today's bucket starts at the most recent
// midnight in the server's local timezone.
func (s *Store) GetMemoryStats(ctx context.Context, projectID string) (MemoryStats, error) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	weekStart := now.Add(-7 * 24 * time.Hour).Unix()

	stats := MemoryStats{ProjectID: projectID}

	// Total memory count (alive - not deleted).
	totalQuery := `SELECT COUNT(*) FROM memories`
	totalArgs := []any{}
	if projectID != "" {
		totalQuery += ` WHERE project_id = ?`
		totalArgs = append(totalArgs, projectID)
	}
	if err := s.db.QueryRowContext(ctx, totalQuery, totalArgs...).Scan(&stats.TotalMemories); err != nil {
		return stats, fmt.Errorf("count memories: %w", err)
	}

	// Per-kind counts for the three windows in a single grouped query.
	countQuery := `
		SELECT kind,
		       SUM(CASE WHEN at >= ? THEN 1 ELSE 0 END) AS day,
		       SUM(CASE WHEN at >= ? THEN 1 ELSE 0 END) AS week,
		       COUNT(*) AS all_time
		FROM memory_events`
	countArgs := []any{dayStart, weekStart}
	if projectID != "" {
		countQuery += ` WHERE project_id = ?`
		countArgs = append(countArgs, projectID)
	}
	countQuery += ` GROUP BY kind`
	rows, err := s.db.QueryContext(ctx, countQuery, countArgs...)
	if err != nil {
		return stats, fmt.Errorf("count events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var day, week, all int
		if err := rows.Scan(&kind, &day, &week, &all); err != nil {
			return stats, err
		}
		switch MemoryEventKind(kind) {
		case EventCreated:
			stats.Counts.CreatedDay, stats.Counts.CreatedWeek, stats.Counts.CreatedAll = day, week, all
		case EventUpdated:
			stats.Counts.UpdatedDay, stats.Counts.UpdatedWeek, stats.Counts.UpdatedAll = day, week, all
		case EventDeleted:
			stats.Counts.DeletedDay, stats.Counts.DeletedWeek, stats.Counts.DeletedAll = day, week, all
		case EventRecalled:
			stats.Counts.RecalledDay, stats.Counts.RecalledWeek, stats.Counts.RecalledAll = day, week, all
		}
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}

	// Top recalled - join to memories so we can return the live row.
	// Skips orphans (memory deleted but events linger).
	topQuery := `
		SELECT m.id, m.uuid, m.project_id, m.content, m.tags_json, m.created_at, m.updated_at,
		       COUNT(*) AS recall_count
		FROM memory_events e
		JOIN memories m ON m.id = e.memory_id
		WHERE e.kind = 'recalled'`
	topArgs := []any{}
	if projectID != "" {
		topQuery += ` AND e.project_id = ?`
		topArgs = append(topArgs, projectID)
	}
	topQuery += ` GROUP BY m.id ORDER BY recall_count DESC, m.updated_at DESC LIMIT 10`
	topRows, err := s.db.QueryContext(ctx, topQuery, topArgs...)
	if err != nil {
		return stats, fmt.Errorf("top recalled: %w", err)
	}
	defer topRows.Close()
	for topRows.Next() {
		var (
			m        Memory
			tagsJSON string
			created  int64
			updated  int64
			count    int
		)
		if err := topRows.Scan(&m.ID, &m.UUID, &m.ProjectID, &m.Content, &tagsJSON, &created, &updated, &count); err != nil {
			return stats, err
		}
		_ = json.Unmarshal([]byte(tagsJSON), &m.Tags)
		m.CreatedAt = time.Unix(created, 0)
		m.UpdatedAt = time.Unix(updated, 0)
		stats.TopRecalled = append(stats.TopRecalled, TopMemory{Memory: m, Count: count})
	}
	return stats, topRows.Err()
}

// MemoryGraph describes a project's memory web for the desktop graph view.
// Nodes are memories; links are tag-overlap or embedding-similarity edges.
type MemoryGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Links []GraphLink `json:"links"`
}

// GraphNode is a memory with display-friendly fields. Content is truncated
// at the boundary so the frontend doesn't ship the full corpus when only a
// hover tooltip is needed.
type GraphNode struct {
	ID      int64    `json:"id"`
	UUID    string   `json:"uuid"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
	Recalls int      `json:"recalls"`
}

// GraphLink is an edge between two memories. Kind is "tag" or "similarity".
// Weight is 1.0 for tag links; for similarity links it's 1/(1+distance) so
// closer matches get heavier edges (force-directed layouts want >0 weights).
type GraphLink struct {
	Source int64   `json:"source"`
	Target int64   `json:"target"`
	Kind   string  `json:"kind"`
	Weight float64 `json:"weight"`
	Label  string  `json:"label,omitempty"`
}

// MemoryGraph returns the nodes + links for projectID. Empty projectID
// spans every project.
//
// Algorithm:
//  1. Load memories + their recall counts in one grouped query.
//  2. Tag edges: for each pair of memories sharing at least one tag, add an
//     edge labeled with the first shared tag. Dedup so each pair gets at
//     most one tag edge.
//  3. Similarity edges: for each memory, KNN against its own vector and add
//     edges to the top-3 (non-self) neighbours. Dedup against tag edges so
//     the layout doesn't get two springs between the same pair.
//
// Similarity probing reads the stored vector via GetVector and runs a
// non-logging KNN - using the public Search would inflate the recall
// counters which is exactly the data we're trying to visualise.
func (s *Store) MemoryGraph(ctx context.Context, projectID string) (MemoryGraph, error) {
	g := MemoryGraph{Nodes: []GraphNode{}, Links: []GraphLink{}}

	memoriesQuery := `
		SELECT m.id, m.uuid, m.content, m.tags_json,
		       COALESCE(SUM(CASE WHEN e.kind = 'recalled' THEN 1 ELSE 0 END), 0) AS recalls
		FROM memories m
		LEFT JOIN memory_events e ON e.memory_id = m.id`
	args := []any{}
	if projectID != "" {
		memoriesQuery += ` WHERE m.project_id = ?`
		args = append(args, projectID)
	}
	memoriesQuery += ` GROUP BY m.id ORDER BY m.id`

	rows, err := s.db.QueryContext(ctx, memoriesQuery, args...)
	if err != nil {
		return g, fmt.Errorf("load memories: %w", err)
	}
	defer rows.Close()

	type memRow struct {
		id      int64
		uuid    string
		content string
		tags    []string
		recalls int
	}
	var mems []memRow
	for rows.Next() {
		var (
			id       int64
			uuid     string
			content  string
			tagsJSON string
			recalls  int
		)
		if err := rows.Scan(&id, &uuid, &content, &tagsJSON, &recalls); err != nil {
			return g, err
		}
		var tags []string
		_ = json.Unmarshal([]byte(tagsJSON), &tags)
		mems = append(mems, memRow{id, uuid, content, tags, recalls})
	}
	if err := rows.Err(); err != nil {
		return g, err
	}

	// Build nodes; truncate content so we don't ship megabytes for a tooltip.
	for _, m := range mems {
		content := m.content
		if len(content) > 280 {
			content = content[:280] + "…"
		}
		g.Nodes = append(g.Nodes, GraphNode{
			ID: m.id, UUID: m.uuid, Content: content, Tags: m.tags, Recalls: m.recalls,
		})
	}

	// Tag edges. tagToMems maps a tag -> sorted slice of memory IDs. seen
	// tracks which (min,max) pairs already have an edge so we don't generate
	// multiple tag-edges for memories that share several tags.
	tagToMems := map[string][]int64{}
	for _, m := range mems {
		for _, t := range m.tags {
			tagToMems[t] = append(tagToMems[t], m.id)
		}
	}
	type pairKey struct{ a, b int64 }
	pairFor := func(x, y int64) pairKey {
		if x < y {
			return pairKey{x, y}
		}
		return pairKey{y, x}
	}
	seen := map[pairKey]bool{}
	for tag, ids := range tagToMems {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				k := pairFor(ids[i], ids[j])
				if seen[k] {
					continue
				}
				seen[k] = true
				g.Links = append(g.Links, GraphLink{
					Source: ids[i], Target: ids[j],
					Kind: "tag", Weight: 1.0, Label: tag,
				})
			}
		}
	}

	// Similarity edges via vec KNN per memory (top 3 non-self neighbours).
	// Skip if we don't have a vector for the memory yet (legacy rows, or
	// reconcile in progress).
	for _, m := range mems {
		vec, err := s.GetVector(ctx, m.id)
		if err != nil || vec == nil {
			continue
		}
		neighbours, err := s.knnByVector(ctx, projectID, vec, 4)
		if err != nil {
			return g, fmt.Errorf("knn for %d: %w", m.id, err)
		}
		added := 0
		for _, n := range neighbours {
			if n.ID == m.id {
				continue
			}
			k := pairFor(m.id, n.ID)
			if seen[k] {
				continue
			}
			seen[k] = true
			weight := 1.0 / (1.0 + n.Distance)
			g.Links = append(g.Links, GraphLink{
				Source: m.id, Target: n.ID,
				Kind: "similarity", Weight: weight,
			})
			added++
			if added >= 3 {
				break
			}
		}
	}

	return g, nil
}

type knnHit struct {
	ID       int64
	Distance float64
}

// knnByVector is the bare KNN call without the EventRecalled side-effect
// that Store.Search has. Used by MemoryGraph so similarity probing doesn't
// inflate the very recall counters we're visualising.
func (s *Store) knnByVector(ctx context.Context, projectID string, vector []float32, k int) ([]knnHit, error) {
	if len(vector) != s.dim {
		return nil, fmt.Errorf("vector dim %d != expected %d", len(vector), s.dim)
	}
	if k <= 0 {
		k = 4
	}
	blob, err := vec.SerializeFloat32(vector)
	if err != nil {
		return nil, err
	}
	overK := k * 4
	if overK < 16 {
		overK = 16
	}
	q := `
		SELECT m.id, v.distance
		FROM memory_vectors v
		JOIN memories m ON m.id = v.memory_id
		WHERE v.embedding MATCH ?
		  AND k = ?`
	args := []any{blob, overK}
	if projectID != "" {
		q += ` AND m.project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY v.distance LIMIT ?`
	args = append(args, k)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []knnHit
	for rows.Next() {
		var h knnHit
		if err := rows.Scan(&h.ID, &h.Distance); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DailyMemoryEvents is one row of the memory-events time series - counts
// per kind for a single calendar day in the server's local timezone.
type DailyMemoryEvents struct {
	Day      string `json:"day"` // YYYY-MM-DD
	Created  int    `json:"created"`
	Updated  int    `json:"updated"`
	Deleted  int    `json:"deleted"`
	Recalled int    `json:"recalled"`
}

// MemoryEventsByDay returns one DailyMemoryEvents per day in the trailing
// `days` window, oldest first. Gaps are filled with zeros so a chart can
// plot a continuous axis. projectID == "" spans every project.
func (s *Store) MemoryEventsByDay(ctx context.Context, projectID string, days int) ([]DailyMemoryEvents, error) {
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startDay := today.AddDate(0, 0, -(days - 1))

	// Pre-populate every day so the result has a continuous date axis.
	byDay := make(map[string]*DailyMemoryEvents, days)
	out := make([]DailyMemoryEvents, 0, days)
	for cursor := startDay; !cursor.After(today); cursor = cursor.AddDate(0, 0, 1) {
		day := cursor.Format("2006-01-02")
		byDay[day] = &DailyMemoryEvents{Day: day}
		out = append(out, DailyMemoryEvents{Day: day})
	}

	q := `SELECT date(at, 'unixepoch', 'localtime') AS day, kind, COUNT(*) AS c
	      FROM memory_events
	      WHERE at >= ?`
	args := []any{startDay.Unix()}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	q += ` GROUP BY day, kind`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day, kind string
		var count int
		if err := rows.Scan(&day, &kind, &count); err != nil {
			return nil, err
		}
		entry, ok := byDay[day]
		if !ok {
			continue
		}
		switch MemoryEventKind(kind) {
		case EventCreated:
			entry.Created = count
		case EventUpdated:
			entry.Updated = count
		case EventDeleted:
			entry.Deleted = count
		case EventRecalled:
			entry.Recalled = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Copy the populated map back into the ordered slice.
	for i, row := range out {
		if filled, ok := byDay[row.Day]; ok {
			out[i] = *filled
		}
	}
	return out, nil
}

// DailyUsage is one row of the LLM-usage time series - totals across all
// providers/models for a single calendar day.
type DailyUsage struct {
	Day               string `json:"day"`
	CostMicrocentsUSD int64  `json:"cost_microcents_usd"`
	Calls             int    `json:"calls"`
	InputTokens       int    `json:"input_tokens"`
	OutputTokens      int    `json:"output_tokens"`
}

// UsageByDay returns one DailyUsage per day in the trailing `days` window,
// oldest first. Gaps filled with zeros. Spans every project (LLM activity
// is user-global, not project-scoped).
func (s *Store) UsageByDay(ctx context.Context, days int) ([]DailyUsage, error) {
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startDay := today.AddDate(0, 0, -(days - 1))

	byDay := make(map[string]*DailyUsage, days)
	out := make([]DailyUsage, 0, days)
	for cursor := startDay; !cursor.After(today); cursor = cursor.AddDate(0, 0, 1) {
		day := cursor.Format("2006-01-02")
		byDay[day] = &DailyUsage{Day: day}
		out = append(out, DailyUsage{Day: day})
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT date(at, 'unixepoch', 'localtime') AS day,
		       COALESCE(SUM(cost_microcents_usd), 0) AS cost,
		       COUNT(*) AS calls,
		       COALESCE(SUM(input_tokens), 0) AS input_tokens,
		       COALESCE(SUM(output_tokens), 0) AS output_tokens
		FROM usage
		WHERE at >= ?
		GROUP BY day`, startDay.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var cost int64
		var calls, in, outTok int
		if err := rows.Scan(&day, &cost, &calls, &in, &outTok); err != nil {
			return nil, err
		}
		if entry, ok := byDay[day]; ok {
			entry.CostMicrocentsUSD = cost
			entry.Calls = calls
			entry.InputTokens = in
			entry.OutputTokens = outTok
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, row := range out {
		if filled, ok := byDay[row.Day]; ok {
			out[i] = *filled
		}
	}
	return out, nil
}

// CountSessionMessages returns (distinct session count, total message count)
// for the dream buffer in projectID. Used by the desktop UI to surface how
// much is queued ahead of a dream pass.
func (s *Store) CountSessionMessages(ctx context.Context, projectID string) (sessions int, messages int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT session_id), COUNT(*) FROM session_messages WHERE project_id = ?`,
		projectID).Scan(&sessions, &messages)
	return
}

// PendingMessageProjects returns every distinct project_id that has at
// least one buffered session message. Powers the scheduler's per-project
// poll: rather than dream the server's own CWD, the scheduler enumerates
// every project with pending work and considers each on its own timer.
func (s *Store) PendingMessageProjects(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT project_id FROM session_messages
		 WHERE project_id != '' ORDER BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SessionsWithMessages returns session IDs (for projectID) that currently
// have at least minMessages stored messages. Pass 0 or 1 to get every
// session with any messages. Used by the dreamer to find work.
func (s *Store) SessionsWithMessages(ctx context.Context, projectID string, minMessages int) ([]string, error) {
	if minMessages < 1 {
		minMessages = 1
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id FROM session_messages
		 WHERE project_id = ?
		 GROUP BY session_id
		 HAVING COUNT(*) >= ?
		 ORDER BY session_id`,
		projectID, minMessages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// sessionMessagesPerPassCap bounds how many session_messages rows a
// single SessionMessages call returns. The dreamer feeds these straight
// into a reasoner prompt; an extremely long Claude Code session could
// otherwise materialise megabytes into a Go slice and a single LLM
// request. 500 turns is well past anything a single dream pass should
// have to summarise — earlier turns will have already been processed by
// previous passes (which delete their rows). Acts as a belt-and-braces
// memory ceiling, not a primary correctness mechanism.
const sessionMessagesPerPassCap = 500

// SessionMessages returns the messages for (projectID, sessionID) in
// chronological order. The project filter is load-bearing: one Claude Code
// session can span several repos, so a session_id alone is NOT a project
// boundary. Without the project filter, a dream pass for project A would
// hand the reasoner messages from project B and create cross-contaminated
// memories.
//
// Capped at sessionMessagesPerPassCap rows. The cap is "oldest first" —
// if a session has more than that, we return the OLDEST cap (not the
// newest), because the dreamer cleans up rows after processing, so any
// build-up of unprocessed rows is at the tail of the buffer and the
// older ones are what we want to drain first.
func (s *Store) SessionMessages(ctx context.Context, projectID, sessionID string) ([]SessionMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, project_id, role, content, at
		 FROM session_messages WHERE project_id = ? AND session_id = ?
		 ORDER BY at ASC, id ASC
		 LIMIT ?`,
		projectID, sessionID, sessionMessagesPerPassCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionMessage
	for rows.Next() {
		var (
			m  SessionMessage
			ts int64
		)
		if err := rows.Scan(&m.ID, &m.SessionID, &m.ProjectID, &m.Role, &m.Content, &ts); err != nil {
			return nil, err
		}
		m.At = time.Unix(ts, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteSessionMessages removes the given message IDs in a single statement.
// Called after the dreamer has applied the reasoner's ops for those messages.
func (s *Store) DeleteSessionMessages(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	// Build the IN-list. ids are int64 we control, no injection risk.
	placeholders := make([]byte, 0, len(ids)*2-1)
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	q := "DELETE FROM session_messages WHERE id IN (" + string(placeholders) + ")"
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// RecordSessionMessages appends conversation turns for a given session.
// Used by the record_messages MCP tool. Messages live in the local DB only;
// they're never pushed to .yullu/logs/.
func (s *Store) RecordSessionMessages(ctx context.Context, projectID, sessionID string, msgs []SessionMessageInput) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO session_messages(session_id, project_id, role, content, at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, m := range msgs {
		if _, err := stmt.ExecContext(ctx, sessionID, projectID, m.Role, m.Content, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordUsage persists one model-call event. Satisfies ai.UsageRecorder.
// Errors are returned but callers (the providers) ignore them - recording
// must not break a model call.
func (s *Store) RecordUsage(ctx context.Context, e ai.UsageEvent) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	okInt := 0
	if e.OK {
		okInt = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage(at, provider, model, kind, input_tokens, output_tokens,
		                  cost_microcents_usd, latency_ms, items, ok, error_msg)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.Unix(), e.Provider, e.Model, e.Kind,
		e.InputTokens, e.OutputTokens, e.CostMicrocentsUSD, e.LatencyMs,
		e.Items, okInt, nullableString(e.ErrorMsg))
	return err
}

// UsageRow is one entry from the usage log. CostMicrocentsUSD is in USD
// microcents (10⁻⁶ cent); divide by 10⁶ for cents or 10⁸ for dollars.
type UsageRow struct {
	ID                int64     `json:"id"`
	At                time.Time `json:"at"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	Kind              string    `json:"kind"`
	InputTokens       int       `json:"input_tokens"`
	OutputTokens      int       `json:"output_tokens"`
	CostMicrocentsUSD int64     `json:"cost_microcents_usd"`
	LatencyMs         int64     `json:"latency_ms"`
	Items             int       `json:"items"`
	OK                bool      `json:"ok"`
	ErrorMsg          string    `json:"error_msg,omitempty"`
}

// UsageBucket aggregates usage by provider+model+kind over some interval.
type UsageBucket struct {
	Provider          string  `json:"provider"`
	Model             string  `json:"model"`
	Kind              string  `json:"kind"`
	Calls             int     `json:"calls"`
	Failures          int     `json:"failures"`
	InputTokens       int     `json:"input_tokens"`
	OutputTokens      int     `json:"output_tokens"`
	CostMicrocentsUSD int64   `json:"cost_microcents_usd"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
}

// UsageSummary groups usage events since `since`. If since is zero, returns
// totals across all time.
func (s *Store) UsageSummary(ctx context.Context, since time.Time) ([]UsageBucket, error) {
	query := `
		SELECT provider, model, kind,
		       COUNT(*) AS calls,
		       SUM(CASE WHEN ok = 0 THEN 1 ELSE 0 END) AS failures,
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cost_microcents_usd), 0),
		       COALESCE(AVG(latency_ms), 0)
		FROM usage`
	args := []any{}
	if !since.IsZero() {
		query += " WHERE at >= ?"
		args = append(args, since.Unix())
	}
	query += " GROUP BY provider, model, kind ORDER BY cost_microcents_usd DESC, calls DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageBucket
	for rows.Next() {
		var b UsageBucket
		if err := rows.Scan(&b.Provider, &b.Model, &b.Kind,
			&b.Calls, &b.Failures, &b.InputTokens, &b.OutputTokens,
			&b.CostMicrocentsUSD, &b.AvgLatencyMs); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UsageRecent returns the most recent usage rows (newest first).
func (s *Store) UsageRecent(ctx context.Context, limit int) ([]UsageRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, provider, model, kind, input_tokens, output_tokens,
		       cost_microcents_usd, latency_ms, items, ok, COALESCE(error_msg, '')
		FROM usage ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var (
			r      UsageRow
			ts     int64
			okInt  int
			errMsg string
		)
		if err := rows.Scan(&r.ID, &ts, &r.Provider, &r.Model, &r.Kind,
			&r.InputTokens, &r.OutputTokens, &r.CostMicrocentsUSD, &r.LatencyMs,
			&r.Items, &okInt, &errMsg); err != nil {
			return nil, err
		}
		r.At = time.Unix(ts, 0)
		r.OK = okInt == 1
		r.ErrorMsg = errMsg
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DreamPassRecord captures the result of one dream pass for telemetry.
// Sessions and message counts mirror the server.DreamResult shape but live
// here so the store layer doesn't import server.
type DreamPassRecord struct {
	ProjectID         string
	SessionsProcessed int
	MessagesProcessed int
	OpsCreated        int
	OpsUpdated        int
	OpsDeleted        int
	OpsSkipped        int
	Errors            []string
}

// RecordDreamPass inserts a row into dream_passes describing what a single
// dream pass accomplished. Failures are logged but never returned - this is
// telemetry; the dream itself already succeeded by the time we get here.
// Skipped passes (single-flight collisions) should NOT be recorded - they
// represent "nothing happened" and would pollute averages.
func (s *Store) RecordDreamPass(ctx context.Context, rec DreamPassRecord) {
	var errsJSON sql.NullString
	if len(rec.Errors) > 0 {
		if b, err := json.Marshal(rec.Errors); err == nil {
			errsJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO dream_passes(
			at, project_id,
			sessions_processed, messages_processed,
			ops_created, ops_updated, ops_deleted, ops_skipped,
			errors_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().Unix(), rec.ProjectID,
		rec.SessionsProcessed, rec.MessagesProcessed,
		rec.OpsCreated, rec.OpsUpdated, rec.OpsDeleted, rec.OpsSkipped,
		errsJSON,
	); err != nil {
		s.recordMemoryEventErr = err
	}
}

// DreamPass is one row from the dream_passes table — the per-cycle
// detail the Stats page renders alongside the aggregate totals. Errors
// is the raw JSON array surfaced as a slice; nil when the pass had none.
type DreamPass struct {
	ID                int64     `json:"id"`
	ProjectID         string    `json:"project_id"`
	At                time.Time `json:"at"`
	SessionsProcessed int       `json:"sessions_processed"`
	MessagesProcessed int       `json:"messages_processed"`
	OpsCreated        int       `json:"ops_created"`
	OpsUpdated        int       `json:"ops_updated"`
	OpsDeleted        int       `json:"ops_deleted"`
	OpsSkipped        int       `json:"ops_skipped"`
	Errors            []string  `json:"errors,omitempty"`
}

// ListDreamPasses returns the most recent dream passes for projectID,
// newest first. Powers the Stats page's per-cycle table. projectID == ""
// spans every project.
func (s *Store) ListDreamPasses(ctx context.Context, projectID string, limit int) ([]DreamPass, error) {
	if limit <= 0 {
		limit = 30
	}
	q := `SELECT id, project_id, at, sessions_processed, messages_processed,
		         ops_created, ops_updated, ops_deleted, ops_skipped, errors_json
		  FROM dream_passes`
	args := []any{}
	if projectID != "" {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DreamPass
	for rows.Next() {
		var (
			p       DreamPass
			at      int64
			errJSON sql.NullString
		)
		if err := rows.Scan(
			&p.ID, &p.ProjectID, &at,
			&p.SessionsProcessed, &p.MessagesProcessed,
			&p.OpsCreated, &p.OpsUpdated, &p.OpsDeleted, &p.OpsSkipped,
			&errJSON,
		); err != nil {
			return nil, err
		}
		p.At = time.Unix(at, 0)
		if errJSON.Valid && errJSON.String != "" && errJSON.String != "[]" {
			_ = json.Unmarshal([]byte(errJSON.String), &p.Errors)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DreamStats is the dashboard view of dream-pass activity over a window.
// LastPassAt is omitted from JSON when no passes ran in the window (the
// zero time.Time value), so clients can use absence as the empty signal
// instead of having to filter "0001-01-01T..." strings.
type DreamStats struct {
	ProjectID         string    `json:"project_id"`
	Passes            int       `json:"passes"`
	SessionsProcessed int       `json:"sessions_processed"`
	MessagesProcessed int       `json:"messages_processed"`
	OpsCreated        int       `json:"ops_created"`
	OpsUpdated        int       `json:"ops_updated"`
	OpsDeleted        int       `json:"ops_deleted"`
	OpsSkipped        int       `json:"ops_skipped"`
	Errors            int       `json:"errors"`
	LastPassAt        time.Time `json:"last_pass_at,omitzero"`
}

// DreamStats aggregates dream_passes rows over the trailing `days` window.
// projectID == "" spans every project. days <= 0 defaults to 30.
func (s *Store) DreamStats(ctx context.Context, projectID string, days int) (DreamStats, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()

	q := `SELECT
			COUNT(*),
			COALESCE(SUM(sessions_processed), 0),
			COALESCE(SUM(messages_processed), 0),
			COALESCE(SUM(ops_created), 0),
			COALESCE(SUM(ops_updated), 0),
			COALESCE(SUM(ops_deleted), 0),
			COALESCE(SUM(ops_skipped), 0),
			COALESCE(SUM(CASE WHEN errors_json IS NOT NULL AND errors_json != '[]' THEN 1 ELSE 0 END), 0),
			COALESCE(MAX(at), 0)
		FROM dream_passes
		WHERE at >= ?`
	args := []any{cutoff}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}

	var ds DreamStats
	ds.ProjectID = projectID
	var lastAt int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(
		&ds.Passes,
		&ds.SessionsProcessed,
		&ds.MessagesProcessed,
		&ds.OpsCreated,
		&ds.OpsUpdated,
		&ds.OpsDeleted,
		&ds.OpsSkipped,
		&ds.Errors,
		&lastAt,
	); err != nil {
		return DreamStats{}, err
	}
	if lastAt > 0 {
		ds.LastPassAt = time.Unix(lastAt, 0)
	}
	return ds, nil
}

// columnAlreadyExists reports whether err is SQLite's "duplicate column name"
// error from an idempotent ALTER TABLE ADD COLUMN. Used so each boot can
// re-run the migration without crashing on already-migrated DBs.
func columnAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}

// MustDefaultDBPath returns the OS-appropriate default database path and
// panics if home can't be resolved.
func MustDefaultDBPath() string {
	if env := os.Getenv("YULLU_DB"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("resolve home dir for default db path: %w", err))
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "yullu", "memories.db")
}
