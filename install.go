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
	// Install the UserPromptSubmit hook so relevant memories get
	// injected before every prompt — turns memory retrieval from
	// "the agent decides to ask" into "the agent always sees what's
	// relevant".
	if err := installClaudeUserPromptHook(dir); err != nil {
		fmt.Printf("claude: user-prompt hook setup failed: %v\n", err)
		fmt.Println("claude: skill + MCP are still set up; memory injection will depend on the model calling retrieve_memories")
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
	if err := uninstallClaudeUserPromptHook(claudeDir()); err != nil {
		fmt.Printf("claude: user-prompt hook removal failed: %v\n", err)
	}
	return nil
}

// installClaudeStopHook adds a Stop-event hook to ~/.claude/settings.json
// that invokes `yullu record-turn`. Thin wrapper over upsertClaudeHook —
// see that function for the shared install logic.
func installClaudeStopHook(dir string) error {
	yulluBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate yullu binary: %w", err)
	}
	return upsertClaudeHook(dir, "Stop", "yullu record-turn", yulluBin+" record-turn")
}

// installClaudeUserPromptHook adds a UserPromptSubmit hook that runs
// `yullu inject` before every user prompt. The hook POSTs the user's
// prompt to the local Yul'lu server, gets back the top-K relevant
// memories for the project, and prints them as ambient context the
// model sees before responding.
func installClaudeUserPromptHook(dir string) error {
	yulluBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate yullu binary: %w", err)
	}
	return upsertClaudeHook(dir, "UserPromptSubmit", "yullu inject", yulluBin+" inject")
}

// upsertClaudeHook is the shared install path for any Claude Code hook
// keyed by event name (Stop, UserPromptSubmit, …). marker is the
// command-substring we use to detect our own entry (so we can rewrite
// stale binary paths rather than appending duplicates). command is the
// full command line to install. Existing entries from other tools are
// preserved — we merge into the per-event hook list rather than
// replacing it.
//
// Why JSON merge instead of a sentinel-marker rewrite: Claude Code's
// settings.json is user-owned and can hold hooks from other tools
// (linter runners, deploy notifiers, etc.). Clobbering would silently
// break them.
func upsertClaudeHook(dir, eventName, marker, command string) error {
	settingsPath := filepath.Join(dir, "settings.json")
	settings, err := loadJSONFile(settingsPath)
	if err != nil {
		return err
	}

	// hooks.<EventName> is an array of {matcher, hooks: [{type, command}]} groups.
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	eventList, _ := hooks[eventName].([]any)

	// Three cases:
	//   1. No yullu entry yet — append one.
	//   2. A yullu entry exists with the same command — no-op.
	//   3. A yullu entry exists with a *different* binary path (e.g. the
	//      old `./bin/yullu …` after a `make refresh` rebuilt
	//      $GOPATH/bin/yullu) — rewrite its command in place.
	// Case 3 is why `make refresh` exists: without it, the hook silently
	// fires the wrong binary forever.
	updated, changed := updateYulluHookCommand(eventList, marker, command)
	switch {
	case changed:
		hooks[eventName] = updated
		if err := writeJSONFile(settingsPath, settings); err != nil {
			return err
		}
		fmt.Printf("claude: updated %s hook command in %s\n", eventName, settingsPath)
		return nil
	case hookAlreadyConfigured(eventList, marker):
		fmt.Printf("claude: %s hook already up-to-date, leaving alone\n", eventName)
		return nil
	}
	eventList = append(eventList, map[string]any{
		"matcher": "*",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
			},
		},
	})
	hooks[eventName] = eventList

	if err := writeJSONFile(settingsPath, settings); err != nil {
		return err
	}
	fmt.Printf("claude: added %s hook to %s\n", eventName, settingsPath)
	return nil
}

// updateYulluHookCommand rewrites any existing yullu hook entry whose
// command contains `marker` but differs from `want`. Returns the
// (possibly-mutated) list and whether anything changed.
func updateYulluHookCommand(list []any, marker, want string) ([]any, bool) {
	changed := false
	for _, entry := range list {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hookList, _ := obj["hooks"].([]any)
		for _, h := range hookList {
			hookObj, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hookObj["command"].(string)
			if !strings.Contains(cmd, marker) {
				continue
			}
			if cmd == want {
				continue
			}
			hookObj["command"] = want
			changed = true
		}
	}
	return list, changed
}

// hookAlreadyConfigured reports whether the event-hook list already has
// an entry whose command contains `marker`. Used by the upsert path to
// distinguish "no-op, leave alone" from "first install, append".
func hookAlreadyConfigured(list []any, marker string) bool {
	for _, entry := range list {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hookList, _ := obj["hooks"].([]any)
		for _, h := range hookList {
			hookObj, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hookObj["command"].(string)
			if strings.Contains(cmd, marker) {
				return true
			}
		}
	}
	return false
}

// uninstallClaudeStopHook removes any Stop entries whose command is our
// `yullu record-turn` invocation. Thin wrapper over removeClaudeHook.
func uninstallClaudeStopHook(dir string) error {
	return removeClaudeHook(dir, "Stop", "yullu record-turn")
}

// uninstallClaudeUserPromptHook removes any UserPromptSubmit entries
// whose command is our `yullu inject` invocation.
func uninstallClaudeUserPromptHook(dir string) error {
	return removeClaudeHook(dir, "UserPromptSubmit", "yullu inject")
}

// removeClaudeHook is the shared uninstall path. Filters out any hook
// entries under hooks.<eventName> whose command contains `marker`.
// Other tools' hooks are untouched.
func removeClaudeHook(dir, eventName, marker string) error {
	settingsPath := filepath.Join(dir, "settings.json")
	settings, err := loadJSONFile(settingsPath)
	if err != nil || settings == nil {
		return nil // nothing to remove
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	eventList, _ := hooks[eventName].([]any)
	if len(eventList) == 0 {
		return nil
	}
	filtered := eventList[:0]
	for _, entry := range eventList {
		if entryReferencesMarker(entry, marker) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if len(filtered) == 0 {
		delete(hooks, eventName)
	} else {
		hooks[eventName] = filtered
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	if err := writeJSONFile(settingsPath, settings); err != nil {
		return err
	}
	fmt.Printf("claude: removed yullu %s hook from %s\n", eventName, settingsPath)
	return nil
}

// entryReferencesMarker reports whether any of the hook entry's
// commands contain the given marker substring. Used by the remove path
// to decide which entries to drop.
func entryReferencesMarker(entry any, marker string) bool {
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
		if strings.Contains(cmd, marker) {
			return true
		}
	}
	return false
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
