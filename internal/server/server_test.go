package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/ai/mock"
)

// initGitRepo creates a real-ish git repo in dir so scope.Resolve picks
// up an origin URL. Skips the test if git isn't installed.
func initGitRepo(t *testing.T, dir, originURL string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-q", "-m", "init"},
		{"remote", "add", "origin", originURL},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable / failed (%v): %s", err, out)
		}
	}
}

// resolveProjectID is the entry point of every MCP/REST call. Tests the
// priority chain (override → callerCwd → server cwd) AND that the
// side-effect upsert into project_locations happens — that registry is
// what writerForProject + the override-file path consult later.
func TestResolveProjectID(t *testing.T) {
	// Reuses fakeEmbedder from reconcile_test.go (same package).
	srv, st := newTestServer(t, &fakeEmbedder{id: "mock:test", dim: 4}, nil)
	ctx := context.Background()

	repoDir := t.TempDir()
	initGitRepo(t, repoDir, "git@github.com:owner/test-repo.git")

	testCases := []struct {
		name     string
		override string
		cwd      string
		wantProj string
		wantRoot string // empty = no upsert expected
	}{
		{
			name:     "explicit override wins, upserts when cwd present",
			override: "github.com/explicit/x",
			cwd:      repoDir,
			wantProj: "github.com/explicit/x",
			wantRoot: repoDir,
		},
		{
			name:     "callerCwd resolves to canonical origin + upserts",
			cwd:      repoDir,
			wantProj: "github.com/owner/test-repo",
			wantRoot: repoDir,
		},
		{
			name:     "override only, no cwd, no upsert",
			override: "github.com/override/only",
			wantProj: "github.com/override/only",
			wantRoot: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			gotProj, err := srv.resolveProjectID(tc.override, tc.cwd)
			assert.NoError(err)
			assert.Equal(tc.wantProj, gotProj)

			gotRoot, err := st.ProjectGitRoot(ctx, gotProj)
			assert.NoError(err)
			if tc.wantRoot != "" {
				// macOS resolves /var/folders/... -> /private/var/...
				wantAbs, _ := filepath.EvalSymlinks(tc.wantRoot)
				gotAbs, _ := filepath.EvalSymlinks(gotRoot)
				assert.Equal(wantAbs, gotAbs, "registry upserted")
			}
		})
	}
}

// shouldDreamProject is the scheduler's per-project gate. Pure function;
// table covers the interval + idle interactions.
func TestShouldDreamProject(t *testing.T) {
	srv, _ := newTestServer(t, &fakeEmbedder{id: "mock:test", dim: 4}, nil)
	now := time.Unix(1_700_000_000, 0)

	testCases := []struct {
		name      string
		lastDream time.Time
		lastMsg   time.Time
		interval  time.Duration
		idle      time.Duration
		want      bool
	}{
		{
			name:     "first-ever pass — zero lastDream fires immediately via interval",
			interval: time.Minute, want: true,
		},
		{
			name:      "interval elapsed",
			lastDream: now.Add(-2 * time.Minute), interval: time.Minute, want: true,
		},
		{
			name:      "interval not yet elapsed, idle disabled",
			lastDream: now.Add(-30 * time.Second), interval: time.Minute, want: false,
		},
		{
			name:      "idle triggers when lastMsg quiet long enough",
			lastDream: now.Add(-30 * time.Second), interval: 10 * time.Minute,
			lastMsg: now.Add(-time.Minute), idle: 30 * time.Second, want: true,
		},
		{
			name:      "idle suppressed when lastMsg too recent",
			lastDream: now.Add(-30 * time.Second), interval: 10 * time.Minute,
			lastMsg: now.Add(-10 * time.Second), idle: 30 * time.Second, want: false,
		},
		{
			name:      "idle=0 disables the idle trigger entirely",
			lastDream: now.Add(-30 * time.Second), interval: 10 * time.Minute,
			lastMsg: now.Add(-2 * time.Minute), idle: 0, want: false,
		},
		{
			name:      "interval=0 + idle=0 means never fire",
			lastDream: now.Add(-2 * time.Minute), interval: 0, idle: 0, want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := srv.shouldDreamProject(now, tc.lastDream, tc.lastMsg, tc.interval, tc.idle)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Project locations registry round-trip — covers the side-effect surface
// of resolveProjectID + the projectGitRoot fallback used by override
// file paths.
func TestProjectLocationsRegistry(t *testing.T) {
	_, st := newTestServer(t, &fakeEmbedder{id: "mock:test", dim: 4}, nil)
	ctx := context.Background()
	assert := assert.New(t)

	// Empty inputs are no-ops.
	assert.NoError(st.UpsertProjectLocation(ctx, "", "/some/path"))
	assert.NoError(st.UpsertProjectLocation(ctx, "p1", ""))

	got, err := st.ProjectGitRoot(ctx, "p1")
	assert.NoError(err)
	assert.Empty(got, "no row written for empty inputs")

	// Real upsert + read.
	assert.NoError(st.UpsertProjectLocation(ctx, "p1", "/a"))
	got, _ = st.ProjectGitRoot(ctx, "p1")
	assert.Equal("/a", got)

	// Most-recent upsert wins (project moved on disk).
	assert.NoError(st.UpsertProjectLocation(ctx, "p1", "/b"))
	got, _ = st.ProjectGitRoot(ctx, "p1")
	assert.Equal("/b", got)

	// Unknown project: empty + no error.
	got, err = st.ProjectGitRoot(ctx, "never-seen")
	assert.NoError(err)
	assert.Empty(got)
}

// Smoke test for the ai/mock package — locks in the canonical
// testify-mock usage so future ai-driven tests have a copyable template.
func TestAIMockEmbedder(t *testing.T) {
	m := &mock.EmbedderMock{}
	m.On("Dim").Return(4)
	m.On("ID").Return("mock:test")
	assert.Equal(t, 4, m.Dim())
	assert.Equal(t, "mock:test", m.ID())
	m.AssertExpectations(t)
}
