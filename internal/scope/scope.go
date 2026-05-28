// Package scope resolves a stable project identifier for the working directory.
package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolve returns a stable project identifier for start. Resolution order:
//  1. The canonical form of the git "origin" remote URL (e.g.
//     github.com/owner/repo), if the directory is inside a git repo with a
//     configured origin. This makes memories portable across clones and
//     machines, since the same repo always resolves to the same ID regardless
//     of where it lives on disk or whether it was cloned via SSH or HTTPS.
//  2. The absolute path of the git root, if inside a git repo with no origin.
//  3. The absolute path of start, if not inside a git repo.
func Resolve(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	gitRoot := findGitRoot(abs)
	if gitRoot == "" {
		return abs, nil
	}
	if origin := remoteOriginURL(gitRoot); origin != "" {
		return canonicalizeOrigin(origin), nil
	}
	return gitRoot, nil
}

// GitRoot returns the absolute path of the nearest git repository root walking
// upward from start, or "" if start is not inside a git repo. Useful for
// callers that need the working-tree path (e.g. to write into .yullu/),
// rather than the canonical project ID that Resolve produces.
func GitRoot(start string) string {
	abs, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	return findGitRoot(abs)
}

// findGitRoot walks up from dir looking for a .git entry (directory for normal
// clones, file for worktrees/submodules). Returns "" if none found.
func findGitRoot(dir string) string {
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// remoteOriginURL reads the URL of the "origin" remote, or "" if there is no
// origin (or git isn't available). Errors are intentionally swallowed: a
// missing remote falls back to a path-based ID, which is a usable degradation.
func remoteOriginURL(gitRoot string) string {
	out, err := exec.Command("git", "-C", gitRoot, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// canonicalizeOrigin reduces an origin URL to a canonical "host/path" form so
// the same repo cloned via SSH and HTTPS produces the same project ID.
//
//	git@github.com:owner/repo.git      -> github.com/owner/repo
//	https://github.com/owner/repo.git  -> github.com/owner/repo
//	ssh://git@github.com/owner/repo    -> github.com/owner/repo
func canonicalizeOrigin(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")

	// SCP-style SSH: user@host:path
	if !strings.Contains(url, "://") {
		if _, after, ok := strings.Cut(url, "@"); ok {
			if host, path, ok := strings.Cut(after, ":"); ok {
				return host + "/" + path
			}
		}
		return url
	}

	// Schemed URL: strip scheme and userinfo.
	_, rest, _ := strings.Cut(url, "://")
	if _, after, ok := strings.Cut(rest, "@"); ok {
		rest = after
	}
	return rest
}
