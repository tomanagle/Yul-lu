package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tomanagle/yullu/internal/scope"
	"github.com/tomanagle/yullu/internal/store"
)

// runExportAgentsMD implements `yullu export agents-md [--out PATH] [--project ID]`.
//
// Reads the local memory store, groups memories by category, and writes
// an AGENTS.md-shaped markdown file with one section per category. The
// idea is that any AI tool that respects AGENTS.md (Codex, Claude Code's
// repo-walking, etc.) gets the Yul'lu knowledge automatically without
// having to speak MCP. For users who don't want to use Yul'lu's tooling,
// the export is the entire value of the system.
//
// Defaults:
//   - Project: the cwd's resolved project id. Override with --project.
//   - Output:  ./AGENTS.md. Override with --out PATH (use "-" for stdout).
//
// Exit codes:
//   0 — wrote file (or stdout) successfully
//   1 — couldn't open the store / resolve project / write output
//   2 — usage error
func runExportAgentsMD(args []string) int {
	outPath := "AGENTS.md"
	projectID := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out", "-o":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "yullu export agents-md: --out needs a value")
				return 2
			}
			outPath = args[i+1]
			i++
		case "--project", "-p":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "yullu export agents-md: --project needs a value")
				return 2
			}
			projectID = args[i+1]
			i++
		case "--help", "-h":
			fmt.Println("Usage: yullu export agents-md [--out PATH] [--project ID]")
			fmt.Println()
			fmt.Println("Writes a categorised AGENTS.md from the Yul'lu memory store.")
			fmt.Println("Sections appear in the order: process, decision, gotcha, domain, style.")
			fmt.Println()
			fmt.Println("  --out PATH    Output path (default: ./AGENTS.md). Use '-' for stdout.")
			fmt.Println("  --project ID  Project to export. Defaults to the cwd's git remote.")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "yullu export agents-md: unknown flag: %s\n", args[i])
			return 2
		}
	}

	// OpenReadOnly avoids needing a working embedder — export is a
	// pure read path. A user without an API key can still dump their
	// memories to AGENTS.md.
	st, err := store.OpenReadOnly(store.MustDefaultDBPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "yullu export agents-md: open store:", err)
		return 1
	}
	defer st.Close()

	if projectID == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "yullu export agents-md: resolve cwd:", err)
			return 1
		}
		projectID, err = scope.Resolve(cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, "yullu export agents-md: resolve project:", err)
			return 1
		}
	}

	// Pull everything for the project. 10_000 is a deliberate ceiling
	// rather than "unlimited" — at that scale the output file is so
	// large that AGENTS.md stops being useful anyway. ListAll uses
	// chronological order; sort within each category by created_at so
	// the export is stable across runs.
	memories, err := st.ListAll(context.Background(), projectID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "yullu export agents-md: load memories:", err)
		return 1
	}

	body := buildAgentsMD(projectID, memories)

	if outPath == "-" {
		_, err = os.Stdout.WriteString(body)
		if err != nil {
			fmt.Fprintln(os.Stderr, "yullu export agents-md: write stdout:", err)
			return 1
		}
		return 0
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil && filepath.Dir(outPath) != "." {
		fmt.Fprintln(os.Stderr, "yullu export agents-md: create dir:", err)
		return 1
	}
	if err := os.WriteFile(outPath, []byte(body), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "yullu export agents-md: write:", err)
		return 1
	}
	fmt.Printf("yullu: wrote %d memories to %s\n", len(memories), outPath)
	return 0
}

// buildAgentsMD renders the markdown body. Sections follow the canonical
// category order; categories with no memories are omitted. Uncategorised
// memories land in a trailing "Uncategorised" section so they're visible
// (and not silently dropped) — the user can fix their category via the
// dashboard or by re-rating.
func buildAgentsMD(projectID string, memories []store.Memory) string {
	categoryOrder := []store.MemoryCategory{
		store.CategoryProcess,
		store.CategoryDecision,
		store.CategoryGotcha,
		store.CategoryDomain,
		store.CategoryStyle,
	}
	categoryTitle := map[store.MemoryCategory]string{
		store.CategoryProcess:  "Process",
		store.CategoryDecision: "Decisions",
		store.CategoryGotcha:   "Gotchas",
		store.CategoryDomain:   "Domain",
		store.CategoryStyle:    "Style",
	}
	categoryBlurb := map[store.MemoryCategory]string{
		store.CategoryProcess:  "How to do things in this repo — commands, conventions, file layout.",
		store.CategoryDecision: "Why we made the choices we made.",
		store.CategoryGotcha:   "Non-obvious constraints and surprises.",
		store.CategoryDomain:   "What words mean here — terms, invariants, semantics.",
		store.CategoryStyle:    "Visual language — UI patterns, copy tone, layout density.",
	}

	buckets := make(map[store.MemoryCategory][]store.Memory)
	var uncategorised []store.Memory
	for _, m := range memories {
		if m.Category == "" {
			uncategorised = append(uncategorised, m)
			continue
		}
		buckets[m.Category] = append(buckets[m.Category], m)
	}
	for k := range buckets {
		sortByCreated(buckets[k])
	}
	sortByCreated(uncategorised)

	var b strings.Builder
	fmt.Fprintf(&b, "# AGENTS.md\n\n")
	fmt.Fprintf(&b, "_Auto-exported from Yul'lu memory for `%s`._\n\n", projectID)
	fmt.Fprintf(&b, "This file is generated. Edits made by hand will be overwritten on the next `yullu export agents-md`. Update the memories via Yul'lu's dashboard or MCP tools instead.\n\n")

	for _, cat := range categoryOrder {
		rows := buckets[cat]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", categoryTitle[cat])
		fmt.Fprintf(&b, "_%s_\n\n", categoryBlurb[cat])
		for _, m := range rows {
			writeMemoryBullet(&b, m)
		}
		b.WriteString("\n")
	}

	if len(uncategorised) > 0 {
		fmt.Fprintf(&b, "## Uncategorised\n\n")
		fmt.Fprintf(&b, "_Memories awaiting classification. Rate or edit them in the dashboard to assign a category, and they'll move into the right section on the next export._\n\n")
		for _, m := range uncategorised {
			writeMemoryBullet(&b, m)
		}
		b.WriteString("\n")
	}

	if len(memories) == 0 {
		b.WriteString("_No memories recorded yet for this project. Start a session in Claude Code / Codex / Cursor with Yul'lu installed; memories will accumulate via the dream pass._\n")
	}

	return b.String()
}

// writeMemoryBullet renders one memory as a markdown bullet. Tags appear
// as inline-code suffixes — they're optional context, not the headline.
func writeMemoryBullet(b *strings.Builder, m store.Memory) {
	// Indent multi-line content under the bullet so markdown renderers
	// keep it inside the list item.
	lines := strings.Split(strings.TrimSpace(m.Content), "\n")
	if len(lines) == 0 {
		return
	}
	b.WriteString("- ")
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteString("\n  ")
		b.WriteString(l)
	}
	if len(m.Tags) > 0 {
		b.WriteString(" — ")
		for i, t := range m.Tags {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(b, "`%s`", t)
		}
	}
	b.WriteString("\n")
}

func sortByCreated(ms []store.Memory) {
	sort.SliceStable(ms, func(i, j int) bool {
		return ms[i].CreatedAt.Before(ms[j].CreatedAt)
	})
}
