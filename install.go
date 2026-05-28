package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runInstall handles `yullu install [target…]`. Targets are "claude" and
// "codex"; with no args we install everything we detect.
func runInstall(args []string) int {
	targets, err := resolveTargets(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(targets) == 0 {
		fmt.Println("yullu: no supported assistants detected (looked for ~/.claude and ~/.codex).")
		fmt.Println("If you've installed Claude Code or Codex elsewhere, pass the target explicitly:")
		fmt.Println("  yullu install claude")
		fmt.Println("  yullu install codex")
		return 0
	}

	any := false
	for _, t := range targets {
		switch t {
		case "claude":
			if err := installClaude(); err != nil {
				fmt.Fprintf(os.Stderr, "claude: %v\n", err)
				continue
			}
			any = true
		case "codex":
			if err := installCodex(); err != nil {
				fmt.Fprintf(os.Stderr, "codex: %v\n", err)
				continue
			}
			any = true
		}
	}
	if !any {
		return 1
	}
	fmt.Println()
	fmt.Println("Start the server with `yullu` (or `make start`) so the MCP endpoint is reachable.")
	return 0
}

func runUninstall(args []string) int {
	targets, err := resolveTargets(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for _, t := range targets {
		switch t {
		case "claude":
			if err := uninstallClaude(); err != nil {
				fmt.Fprintf(os.Stderr, "claude: %v\n", err)
			}
		case "codex":
			if err := uninstallCodex(); err != nil {
				fmt.Fprintf(os.Stderr, "codex: %v\n", err)
			}
		}
	}
	return 0
}

// resolveTargets accepts explicit names (claude/codex) and falls back to
// auto-detecting based on which assistant config directories exist.
func resolveTargets(args []string) ([]string, error) {
	if len(args) == 0 {
		var detected []string
		if dirExists(claudeDir()) {
			detected = append(detected, "claude")
		}
		if dirExists(codexDir()) {
			detected = append(detected, "codex")
		}
		return detected, nil
	}
	for _, t := range args {
		switch t {
		case "claude", "codex":
		default:
			return nil, fmt.Errorf("unknown target %q (supported: claude, codex)", t)
		}
	}
	return args, nil
}

// ---------- Claude Code ----------

func claudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func installClaude() error {
	dir := claudeDir()
	if !dirExists(dir) {
		return fmt.Errorf("not found at %s - install Claude Code first (https://docs.claude.com/en/docs/agents-and-tools/claude-code)", dir)
	}

	// Write skill file at ~/.claude/skills/yullu/SKILL.md with frontmatter.
	skillDir := filepath.Join(dir, "skills", "yullu")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", skillDir, err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	content := claudeFrontmatter + "\n" + skillBody
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", skillPath, err)
	}
	fmt.Printf("claude: wrote skill to %s\n", skillPath)

	// Register MCP. Best-effort: if `claude` isn't on PATH or the call fails,
	// print the command for the user to run.
	if _, err := exec.LookPath("claude"); err == nil {
		out, err := exec.Command("claude", "mcp", "add", "yullu",
			"--transport", "http", "http://localhost:47823/mcp").CombinedOutput()
		trimmed := strings.TrimSpace(string(out))
		if err != nil {
			// Most common cause: already added. Show the output so the user can
			// confirm rather than failing the whole install.
			fmt.Printf("claude: `claude mcp add` reported: %s\n", trimmed)
		} else {
			if trimmed != "" {
				fmt.Printf("claude: %s\n", trimmed)
			}
			fmt.Println("claude: MCP server registered.")
		}
	} else {
		fmt.Println("claude: `claude` binary not on PATH - run this when you can:")
		fmt.Println("  claude mcp add yullu --transport http http://localhost:47823/mcp")
	}
	return nil
}

func uninstallClaude() error {
	skillDir := filepath.Join(claudeDir(), "skills", "yullu")
	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("remove %s: %w", skillDir, err)
	}
	fmt.Printf("claude: removed %s\n", skillDir)
	if _, err := exec.LookPath("claude"); err == nil {
		_ = exec.Command("claude", "mcp", "remove", "yullu").Run()
		fmt.Println("claude: ran `claude mcp remove yullu`")
	}
	return nil
}

const claudeFrontmatter = `---
name: yullu
description: Persistent, semantically-searchable memory for the current codebase. Use to recall prior decisions, gotchas, and team context before answering codebase questions, and to save durable learnings for future sessions. Loads automatically whenever working in a project.
---`

// ---------- Codex CLI ----------

func codexDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// installCodex writes the skill content into ~/.codex/AGENTS.md between
// <!-- yullu:start --> and <!-- yullu:end --> markers (replacing the block
// if it already exists), and prints the MCP registration step for the user
// to apply to ~/.codex/config.toml. Codex's HTTP MCP config syntax has been
// less stable than Claude Code's, so we don't try to edit config.toml
// automatically.
func installCodex() error {
	dir := codexDir()
	if !dirExists(dir) {
		return fmt.Errorf("not found at %s - install Codex CLI first (https://github.com/openai/codex)", dir)
	}

	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := writeMarkerSection(agentsPath, "yullu", skillBody); err != nil {
		return fmt.Errorf("update %s: %w", agentsPath, err)
	}
	fmt.Printf("codex: wrote skill section to %s\n", agentsPath)

	fmt.Println("codex: add this to ~/.codex/config.toml under your MCP section:")
	fmt.Println()
	fmt.Println("    [mcp_servers.yullu]")
	fmt.Println("    url = \"http://localhost:47823/mcp\"")
	fmt.Println()
	fmt.Println("    # (If your Codex build uses a different MCP config schema, check")
	fmt.Println("    #  `codex --help` or the Codex docs for the equivalent HTTP MCP syntax.)")
	return nil
}

func uninstallCodex() error {
	agentsPath := filepath.Join(codexDir(), "AGENTS.md")
	if err := writeMarkerSection(agentsPath, "yullu", ""); err != nil {
		return fmt.Errorf("update %s: %w", agentsPath, err)
	}
	fmt.Printf("codex: removed skill section from %s (remove the [mcp_servers.yullu] entry from config.toml manually)\n", agentsPath)
	return nil
}

// writeMarkerSection inserts or replaces a named section in a markdown file,
// delimited by `<!-- name:start -->` and `<!-- name:end -->`. Empty content
// removes the section entirely. The rest of the file is preserved.
func writeMarkerSection(path, name, content string) error {
	start := "<!-- " + name + ":start -->"
	end := "<!-- " + name + ":end -->"

	var existing string
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	} else if !os.IsNotExist(err) {
		return err
	}

	before, after, hasSection := splitOnMarkers(existing, start, end)
	var section string
	if content != "" {
		section = start + "\n" + strings.TrimSpace(content) + "\n" + end + "\n"
	}

	var out string
	switch {
	case hasSection:
		out = before + section + after
	case existing == "":
		out = section
	default:
		// Append, separated by a blank line so the section reads cleanly.
		sep := "\n"
		if !strings.HasSuffix(existing, "\n") {
			sep = "\n\n"
		} else if !strings.HasSuffix(existing, "\n\n") {
			sep = "\n"
		}
		out = existing + sep + section
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

func splitOnMarkers(text, start, end string) (before, after string, ok bool) {
	si := strings.Index(text, start)
	if si < 0 {
		return text, "", false
	}
	ei := strings.Index(text[si:], end)
	if ei < 0 {
		// Start marker without an end - treat as no section, append fresh.
		return text, "", false
	}
	ei += si + len(end)
	// Eat the trailing newline that writeMarkerSection added.
	if ei < len(text) && text[ei] == '\n' {
		ei++
	}
	return text[:si], text[ei:], true
}

// ---------- helpers ----------

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
