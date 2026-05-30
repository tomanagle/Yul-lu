package scope

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanonicalizeOrigin(t *testing.T) {
	testCases := []struct {
		name string
		in   string
		want string
	}{
		{"SCP-style SSH", "git@github.com:owner/repo.git", "github.com/owner/repo"},
		{"HTTPS with .git", "https://github.com/owner/repo.git", "github.com/owner/repo"},
		{"SSH scheme + userinfo", "ssh://git@github.com/owner/repo", "github.com/owner/repo"},
		{"Trailing slash stripped", "https://github.com/owner/repo/", "github.com/owner/repo"},
		{"GitLab subgroup HTTPS", "https://gitlab.com/group/sub/repo.git", "gitlab.com/group/sub/repo"},
		{"Plain hostname (no remote URL pattern)", "example.local", "example.local"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, canonicalizeOrigin(tc.in))
		})
	}
}

func TestResolveOriginURL(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir, "git@github.com:owner/repo.git")

	got, err := Resolve(dir)
	assert.NoError(t, err)
	assert.Equal(t, "github.com/owner/repo", got)
}

func TestResolveNoGitFallsBackToBasename(t *testing.T) {
	parent := t.TempDir()
	sub := filepath.Join(parent, "my-project")
	if err := makeDir(sub); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := Resolve(sub)
	assert.NoError(t, err)
	assert.Equal(t, "my-project", got)
}

func TestResolveGitNoOriginFallsBackToRepoBasename(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "no-origin-repo")
	if err := makeDir(repo); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// init without adding origin
	cmd := exec.Command("git", "-C", repo, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}

	got, err := Resolve(repo)
	assert.NoError(t, err)
	assert.Equal(t, "no-origin-repo", got, "basename of git root wins when origin is absent")
}

// initRepo is a tiny git-repo setup. Skips the test if git isn't available.
func initRepo(t *testing.T, dir, originURL string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-q", "-m", "init"},
		{"remote", "add", "origin", originURL},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
}

// makeDir is os.MkdirAll wrapped so the test reads cleanly.
func makeDir(p string) error {
	return exec.Command("mkdir", "-p", p).Run()
}
