package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// runInstall handles `yullu install [target…] [--service|--yes]`.
// Targets: "claude" (auto-detected from ~/.claude), "codex" (auto-detected
// from ~/.codex), "cursor" (explicit only — ~/.cursor is shared with VS
// Code Cursor IDE). `--service` skips the prompt and installs the
// launchd/systemd auto-start unit; `--yes` does both that AND any future
// interactive prompts.
func runInstall(args []string) int {
	// Tiny flag parser — separate flags from target names. Order doesn't
	// matter; flags may appear anywhere.
	autoYes := false
	withService := false
	noService := false
	targetArgs := args[:0:0]
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			autoYes = true
		case "--service":
			withService = true
		case "--no-service":
			noService = true
		default:
			targetArgs = append(targetArgs, a)
		}
	}

	targets, err := resolveTargets(targetArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(targets) == 0 {
		fmt.Println("yullu: no supported assistants detected (looked for ~/.claude and ~/.codex).")
		fmt.Println("Pass a target explicitly: yullu install claude | codex | cursor")
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
		case "cursor":
			if err := installCursor(); err != nil {
				fmt.Fprintf(os.Stderr, "cursor: %v\n", err)
				continue
			}
			any = true
		}
	}
	if !any {
		return 1
	}

	// Service install. By default, prompt; --service skips the prompt
	// (good for scripts), --no-service skips installation entirely.
	if !noService {
		if withService {
			if err := installService(true); err != nil {
				fmt.Fprintf(os.Stderr, "service: %v\n", err)
			}
		} else {
			if err := installService(autoYes); err != nil {
				fmt.Fprintf(os.Stderr, "service: %v\n", err)
			}
		}
	}

	fmt.Println()
	fmt.Println("Start the server with `yullu` (or `make start`) if you skipped the service install.")
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
		case "cursor":
			if err := uninstallCursor(); err != nil {
				fmt.Fprintf(os.Stderr, "cursor: %v\n", err)
			}
		}
	}
	if err := uninstallService(); err != nil {
		fmt.Fprintf(os.Stderr, "service: %v\n", err)
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
		case "claude", "codex", "cursor":
		default:
			return nil, fmt.Errorf("unknown target %q (supported: claude, codex, cursor)", t)
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

	// Install the Stop hook so record_messages fires deterministically at
	// the end of every turn — no relying on the model to remember.
	if err := installClaudeStopHook(dir); err != nil {
		fmt.Printf("claude: stop hook setup failed: %v\n", err)
		fmt.Println("claude: skill + MCP are still set up; recording will depend on the model honouring SKILL.md")
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
	if err := uninstallClaudeStopHook(claudeDir()); err != nil {
		fmt.Printf("claude: stop hook removal failed: %v\n", err)
	}
	return nil
}

// installClaudeStopHook adds a Stop-event hook to ~/.claude/settings.json
// that invokes `yullu record-turn`. The hook is matched on "*" (every
// turn). Existing entries from other tools are preserved — we merge our
// entry into hooks.Stop rather than replacing the whole list.
//
// Why JSON merge instead of a sentinel-marker rewrite: Claude Code's
// settings.json is user-owned and can hold hooks from other tools (linter
// runners, deploy notifiers, etc.). Clobbering would silently break them.
func installClaudeStopHook(dir string) error {
	yulluBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate yullu binary: %w", err)
	}
	settingsPath := filepath.Join(dir, "settings.json")
	settings, err := loadJSONFile(settingsPath)
	if err != nil {
		return err
	}

	// hooks.Stop is an array of {matcher, hooks: [{type, command}]} groups.
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	stopList, _ := hooks["Stop"].([]any)

	command := yulluBin + " record-turn"
	if stopAlreadyConfigured(stopList, command) {
		fmt.Println("claude: Stop hook already present, leaving alone")
		return nil
	}
	stopList = append(stopList, map[string]any{
		"matcher": "*",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
			},
		},
	})
	hooks["Stop"] = stopList

	if err := writeJSONFile(settingsPath, settings); err != nil {
		return err
	}
	fmt.Printf("claude: added Stop hook to %s\n", settingsPath)
	return nil
}

// uninstallClaudeStopHook removes any Stop entries whose command is our
// `yullu record-turn` invocation. Other tools' Stop hooks are untouched.
func uninstallClaudeStopHook(dir string) error {
	settingsPath := filepath.Join(dir, "settings.json")
	settings, err := loadJSONFile(settingsPath)
	if err != nil || settings == nil {
		return nil // nothing to remove
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	stopList, _ := hooks["Stop"].([]any)
	if len(stopList) == 0 {
		return nil
	}
	filtered := stopList[:0]
	for _, entry := range stopList {
		if entryReferencesYullu(entry) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if len(filtered) == 0 {
		delete(hooks, "Stop")
	} else {
		hooks["Stop"] = filtered
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	if err := writeJSONFile(settingsPath, settings); err != nil {
		return err
	}
	fmt.Printf("claude: removed yullu Stop hook from %s\n", settingsPath)
	return nil
}

// stopAlreadyConfigured reports whether the Stop hook list already has an
// entry pointing at our `yullu record-turn` command. Match is on the
// command string suffix so a user with a different yullu binary path
// (e.g. installed via brew vs `go install`) still gets recognised.
func stopAlreadyConfigured(list []any, command string) bool {
	for _, entry := range list {
		if entryReferencesYullu(entry) {
			_ = command // suffix match would go here if we want stricter checks
			return true
		}
	}
	return false
}

// entryReferencesYullu reports whether a Stop hook entry's command field
// contains "yullu record-turn". Loose match so brew/go-install/etc. binary
// paths all hit. Defensive about shape — settings.json is user-editable.
func entryReferencesYullu(entry any) bool {
	obj, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hookList, _ := obj["hooks"].([]any)
	for _, h := range hookList {
		hookObj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hookObj["command"].(string)
		if strings.Contains(cmd, "yullu record-turn") {
			return true
		}
	}
	return false
}

// loadJSONFile reads a JSON file as map[string]any. Missing file returns
// an empty map (the install path), unparseable returns an error so the
// installer surfaces a clear message rather than silently clobbering.
func loadJSONFile(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w (refusing to overwrite)", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// writeJSONFile pretty-prints a JSON file with 2-space indent and a
// trailing newline — matches how editors leave settings.json so we don't
// thrash the file on every install/uninstall.
func writeJSONFile(path string, data map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
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

	if err := installCodexMCP(dir); err != nil {
		fmt.Printf("codex: MCP registration failed: %v\n", err)
		fmt.Println("codex: add manually to ~/.codex/config.toml:")
		fmt.Println("    [mcp_servers.yullu]")
		fmt.Println("    url = \"http://localhost:47823/mcp\"")
	}
	return nil
}

// installCodexMCP writes the [mcp_servers.yullu] entry into the user's
// ~/.codex/config.toml. Existing mcp_servers entries are preserved. The
// rest of the file (non-MCP keys) is left untouched modulo TOML
// re-serialisation. This is the closest thing Codex has to `claude mcp add`.
//
// Codex's MCP config schema has shifted across versions; if the user's
// build wants something different (named keys vs. positional args,
// `command` instead of `url`, etc.), they can edit the file by hand and
// `yullu install` will leave existing entries alone.
func installCodexMCP(dir string) error {
	configPath := filepath.Join(dir, "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Decode into a generic map so we don't need a typed schema for the
	// whole Codex config (which has a moving target).
	cfg := map[string]any{}
	if len(raw) > 0 {
		if _, err := toml.Decode(string(raw), &cfg); err != nil {
			return fmt.Errorf("parse %s: %w (refusing to overwrite)", configPath, err)
		}
	}
	servers, _ := cfg["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, exists := servers["yullu"]; exists {
		fmt.Printf("codex: [mcp_servers.yullu] already present in %s, leaving alone\n", configPath)
		return nil
	}
	servers["yullu"] = map[string]any{
		"url": "http://localhost:47823/mcp",
	}
	cfg["mcp_servers"] = servers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := os.WriteFile(configPath, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("codex: added [mcp_servers.yullu] to %s\n", configPath)
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

// ---------- Cursor ----------

// cursorDir returns the path to Cursor's per-user config dir. Note: this
// is the same directory the VS Code-based Cursor IDE uses, so its mere
// presence does not imply the user uses Cursor's AI assistant. That's why
// `cursor` is an explicit install target — never auto-detected.
func cursorDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cursor")
}

// installCursor writes the Cursor MCP entry at ~/.cursor/mcp.json. Cursor
// doesn't expose post-turn hooks, so deterministic recording isn't
// available — the model has to honour the rules file. Rules are
// per-project in Cursor, so we don't write a global rules file; users who
// want Cursor to follow yullu's behaviour should commit a small file at
// `.cursor/rules/yullu.mdc` in their repo (one is printed for copy/paste).
func installCursor() error {
	dir := cursorDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	settings, err := loadJSONFile(mcpPath)
	if err != nil {
		return err
	}
	servers, _ := settings["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, exists := servers["yullu"]; exists {
		fmt.Printf("cursor: mcpServers.yullu already present in %s, leaving alone\n", mcpPath)
	} else {
		servers["yullu"] = map[string]any{
			"url": "http://localhost:47823/mcp",
		}
		settings["mcpServers"] = servers
		if err := writeJSONFile(mcpPath, settings); err != nil {
			return err
		}
		fmt.Printf("cursor: added mcpServers.yullu to %s\n", mcpPath)
	}

	// Cursor rules are per-project; print a snippet for the user to drop
	// into the repos where they want yullu's behaviour. We don't auto-write
	// because we don't know which project they intend.
	fmt.Println("cursor: to teach Cursor when to call yullu's tools, commit this file")
	fmt.Println("cursor: in the repo's .cursor/rules/yullu.mdc:")
	fmt.Println()
	fmt.Println("---")
	fmt.Println("---")
	fmt.Println(strings.SplitN(skillBody, "\n", 3)[1]) // skip the first heading line
	return nil
}

func uninstallCursor() error {
	mcpPath := filepath.Join(cursorDir(), "mcp.json")
	settings, err := loadJSONFile(mcpPath)
	if err != nil || settings == nil {
		return nil
	}
	servers, _ := settings["mcpServers"].(map[string]any)
	if servers == nil {
		return nil
	}
	if _, ok := servers["yullu"]; !ok {
		return nil
	}
	delete(servers, "yullu")
	if len(servers) == 0 {
		delete(settings, "mcpServers")
	} else {
		settings["mcpServers"] = servers
	}
	if err := writeJSONFile(mcpPath, settings); err != nil {
		return err
	}
	fmt.Printf("cursor: removed mcpServers.yullu from %s\n", mcpPath)
	return nil
}

// ---------- helpers ----------

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
