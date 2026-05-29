import { useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Check, Pencil, RefreshCw, Search, Trash2, X } from "lucide-react";

import { useDeleteMemory, useMemories, useRetry, useStatus, useUpdateMemory } from "@/lib/queries";
import { useProjectScope } from "@/lib/project-scope";
import { relativeTime, shortUUID } from "@/lib/format";
import type { Memory, MemoryCategory } from "@/lib/schemas";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

// Display order for the category sections. Matches the SKILL.md ordering
// so users see categories laid out the same way the agent reasons about
// them. "uncategorised" is a sentinel for memories with no category set —
// rendered last so it doesn't compete with the named sections.
const CATEGORY_ORDER: (MemoryCategory | "uncategorised")[] = [
  "process",
  "decision",
  "gotcha",
  "domain",
  "style",
  "uncategorised",
];

// One-line definitions shown under each section header so a user
// reviewing the dashboard learns the categories without leaving the
// page. Kept short — full definitions live in SKILL.md.
const CATEGORY_BLURB: Record<MemoryCategory | "uncategorised", string> = {
  process: "How to do things in this repo — commands, conventions, layout.",
  decision: "Why we made the choices we made.",
  gotcha: "What bites — non-obvious constraints, API quirks, must-always rules.",
  domain: "What words mean here — terms, invariants, semantics.",
  style: "Visual language — UI patterns, copy tone, layout density.",
  uncategorised: "Memories awaiting classification. Rate or edit to assign a category.",
};

export function MemoriesPage() {
  const navigate = useNavigate();
  const { data: status } = useStatus();
  // Project is sidebar-owned. Empty = all projects.
  const { project } = useProjectScope();
  const [filter, setFilter] = useState("");
  // null = "All" — show every category sectioned. A specific category
  // collapses the view to that one section.
  const [activeCategory, setActiveCategory] = useState<MemoryCategory | "uncategorised" | null>(null);

  // The backend handles search via SQLite FTS5 (BM25-ranked, free).
  // Passing a non-empty filter switches useMemories from List to Search.
  const memoriesQuery = useMemories(project, filter);
  const deleteMemory = useDeleteMemory(project);
  const updateMemory = useUpdateMemory(project);

  const results = memoriesQuery.data ?? [];

  // Pre-bucket results by category once per render. Memo'd to avoid
  // re-shuffling on every keystroke when the user is just typing in the
  // filter box (which already refetches via the query).
  const grouped = useMemo(() => groupByCategory(results), [results]);

  if (status && !status.ready) {
    return <SetupCard status={status} onOpenSettings={() => navigate({ to: "/settings" })} />;
  }

  const showingCount = results.length;
  // Sections to render. When a specific category is active, restrict
  // to just that one (and only if it has entries — otherwise empty
  // state below handles the message).
  const visibleSections = CATEGORY_ORDER.filter((c) => {
    if (activeCategory !== null && c !== activeCategory) return false;
    return (grouped[c]?.length ?? 0) > 0;
  });

  return (
    <div className="space-y-4">
      <Toolbar
        filter={filter}
        onFilterChange={setFilter}
        loading={memoriesQuery.isFetching}
        onRefresh={() => memoriesQuery.refetch()}
      />

      <CategoryFilterPills
        active={activeCategory}
        onChange={setActiveCategory}
        counts={grouped}
      />

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>
          {filter
            ? `${showingCount} ${showingCount === 1 ? "match" : "matches"} for “${filter}”`
            : `${showingCount} ${showingCount === 1 ? "memory" : "memories"}`}
        </span>
        <span>
          {project ? (
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{project}</code>
          ) : (
            <span className="italic">all projects</span>
          )}
        </span>
      </div>

      {memoriesQuery.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {memoriesQuery.error && (
        <Card className="border-destructive/40 bg-destructive/10">
          <CardContent className="p-4 text-sm text-destructive">
            {String(memoriesQuery.error)}
          </CardContent>
        </Card>
      )}
      {!memoriesQuery.isLoading && showingCount === 0 && !filter && <EmptyState />}
      {!memoriesQuery.isLoading && showingCount === 0 && filter && (
        <p className="py-8 text-center text-sm text-muted-foreground">
          No memories match “{filter}”.
        </p>
      )}
      {!memoriesQuery.isLoading && showingCount > 0 && visibleSections.length === 0 && (
        <p className="py-8 text-center text-sm text-muted-foreground">
          No memories in this category.
        </p>
      )}

      <div className="space-y-6">
        {visibleSections.map((category) => (
          <section key={category} className="space-y-3">
            <CategorySectionHeader
              category={category}
              count={grouped[category]?.length ?? 0}
            />
            <ul className="space-y-3">
              {grouped[category]!.map((m) => (
                <li key={m.id}>
                  <MemoryCard
                    memory={m}
                    isSaving={updateMemory.isPending && updateMemory.variables?.id === m.id}
                    onSave={(content, tags) => updateMemory.mutateAsync({ id: m.id, content, tags })}
                    onDelete={() => {
                      if (confirm("Delete this memory?")) deleteMemory.mutate(m.id);
                    }}
                  />
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </div>
  );
}

// groupByCategory partitions a flat list into the five category buckets
// plus an "uncategorised" bucket. Returns a plain object keyed by category
// so the render side can index by the section key without optional chains
// scattered everywhere.
function groupByCategory(memories: Memory[]): Record<MemoryCategory | "uncategorised", Memory[]> {
  const buckets: Record<MemoryCategory | "uncategorised", Memory[]> = {
    process: [],
    decision: [],
    gotcha: [],
    domain: [],
    style: [],
    uncategorised: [],
  };
  for (const m of memories) {
    const key = (m.category ?? "uncategorised") as MemoryCategory | "uncategorised";
    if (buckets[key]) buckets[key].push(m);
    else buckets.uncategorised.push(m);
  }
  return buckets;
}

function CategoryFilterPills({
  active,
  onChange,
  counts,
}: {
  active: MemoryCategory | "uncategorised" | null;
  onChange: (v: MemoryCategory | "uncategorised" | null) => void;
  counts: Record<MemoryCategory | "uncategorised", Memory[]>;
}) {
  const total = Object.values(counts).reduce((acc, arr) => acc + arr.length, 0);
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Pill active={active === null} onClick={() => onChange(null)}>
        All <span className="ml-1 text-muted-foreground">{total}</span>
      </Pill>
      {CATEGORY_ORDER.map((c) => {
        const n = counts[c]?.length ?? 0;
        if (n === 0) return null;
        return (
          <Pill key={c} active={active === c} onClick={() => onChange(c)}>
            {c} <span className="ml-1 text-muted-foreground">{n}</span>
          </Pill>
        );
      })}
    </div>
  );
}

function Pill({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded-full border px-3 py-1 text-xs transition-colors",
        active
          ? "border-primary bg-primary/10 text-foreground"
          : "border-border/40 text-muted-foreground hover:border-border hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

function CategorySectionHeader({
  category,
  count,
}: {
  category: MemoryCategory | "uncategorised";
  count: number;
}) {
  return (
    <div className="flex items-baseline gap-2 border-b border-border/40 pb-1.5">
      <h2 className="text-sm font-medium capitalize text-foreground">{category}</h2>
      <span className="text-xs text-muted-foreground">{count}</span>
      <span className="ml-2 truncate text-[11px] text-muted-foreground/80">
        {CATEGORY_BLURB[category]}
      </span>
    </div>
  );
}

function Toolbar({
  filter,
  onFilterChange,
  loading,
  onRefresh,
}: {
  filter: string;
  onFilterChange: (v: string) => void;
  loading: boolean;
  onRefresh: () => void;
}) {
  return (
    <div className="flex gap-2">
      <div className="relative flex-1">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={filter}
          onChange={(e) => onFilterChange(e.target.value)}
          placeholder="Filter by content or tag…"
          className="pl-9"
        />
      </div>
      <Button variant="outline" onClick={onRefresh} disabled={loading}>
        <RefreshCw className={loading ? "animate-spin" : ""} />
        Refresh
      </Button>
    </div>
  );
}

type MemoryShape = {
  id: number;
  uuid: string;
  project_id: string;
  content: string;
  tags?: string[];
  created_at: string;
  updated_at: string;
  category?: MemoryCategory;
};

function MemoryCard({
  memory,
  isSaving,
  onSave,
  onDelete,
}: {
  memory: MemoryShape;
  isSaving: boolean;
  onSave: (content: string, tags: string[]) => Promise<unknown>;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);

  if (editing) {
    return (
      <MemoryEditor
        memory={memory}
        isSaving={isSaving}
        onCancel={() => setEditing(false)}
        onSave={async (content, tags) => {
          await onSave(content, tags);
          setEditing(false);
        }}
      />
    );
  }

  const updated = relativeTime(memory.updated_at);
  const created = relativeTime(memory.created_at);
  const edited = memory.created_at !== memory.updated_at;

  return (
    <Card className="group transition-colors hover:border-border/80">
      <CardContent className="space-y-3 p-4">
        <div className="flex items-start gap-3">
          <p className="flex-1 whitespace-pre-wrap text-sm leading-relaxed">{memory.content}</p>
          <div className="flex shrink-0 gap-1 opacity-0 transition-opacity group-hover:opacity-100">
            <Button
              size="icon"
              variant="ghost"
              className="h-7 w-7 text-muted-foreground hover:bg-accent"
              onClick={() => setEditing(true)}
              aria-label="Edit memory"
            >
              <Pencil className="h-3.5 w-3.5" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="h-7 w-7 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
              onClick={onDelete}
              aria-label="Delete memory"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>

        {(memory.category || (memory.tags ?? []).length > 0) && (
          <div className="flex flex-wrap items-center gap-1.5">
            {memory.category && (
              <Badge variant="outline" className="border-primary/40 text-primary/90">
                {memory.category}
              </Badge>
            )}
            {(memory.tags ?? []).map((t) => (
              <Badge key={t} variant="secondary">
                {t}
              </Badge>
            ))}
          </div>
        )}

        <div className="flex items-center gap-3 text-[11px] text-muted-foreground">
          <span title={`Local id: ${memory.id}`}>#{memory.id}</span>
          <span className="font-mono" title={memory.uuid}>
            {shortUUID(memory.uuid)}
          </span>
          {memory.project_id && (
            <span
              className="truncate font-mono"
              title={memory.project_id}
              style={{ maxWidth: "32ch" }}
            >
              {memory.project_id}
            </span>
          )}
          <span className="ml-auto whitespace-nowrap" title={memory.updated_at}>
            {edited ? `edited ${updated}` : `added ${created}`}
          </span>
        </div>
      </CardContent>
    </Card>
  );
}

function MemoryEditor({
  memory,
  isSaving,
  onSave,
  onCancel,
}: {
  memory: MemoryShape;
  isSaving: boolean;
  onSave: (content: string, tags: string[]) => Promise<void>;
  onCancel: () => void;
}) {
  const [content, setContent] = useState(memory.content);
  const [tagsInput, setTagsInput] = useState((memory.tags ?? []).join(", "));
  const [error, setError] = useState<string | null>(null);

  const dirty = content !== memory.content || tagsInput !== (memory.tags ?? []).join(", ");

  const handleSave = async () => {
    setError(null);
    const tags = tagsInput
      .split(",")
      .map((t) => t.trim())
      .filter(Boolean);
    try {
      await onSave(content, tags);
    } catch (e) {
      setError(String(e));
    }
  };

  return (
    <Card className="border-primary/40">
      <CardContent className="space-y-3 p-4">
        <Textarea
          value={content}
          onChange={(e) => setContent(e.target.value)}
          rows={Math.max(3, Math.min(12, content.split("\n").length + 1))}
          placeholder="Memory content…"
          autoFocus
          onKeyDown={(e) => {
            if (e.key === "Escape") onCancel();
            if ((e.metaKey || e.ctrlKey) && e.key === "Enter") handleSave();
          }}
        />
        <div className="space-y-1.5">
          <Input
            value={tagsInput}
            onChange={(e) => setTagsInput(e.target.value)}
            placeholder="Tags (comma-separated)"
          />
          <p className="text-[11px] text-muted-foreground">
            Cmd/Ctrl+Enter to save · Esc to cancel
          </p>
        </div>

        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{error}</p>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={isSaving}>
            <X className="h-3.5 w-3.5" />
            Cancel
          </Button>
          <Button size="sm" onClick={handleSave} disabled={!dirty || isSaving}>
            <Check className="h-3.5 w-3.5" />
            {isSaving ? "Saving…" : "Save"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function SetupCard({
  status,
  onOpenSettings,
}: {
  status: { message?: string; hint?: string; config_path?: string; db_path?: string };
  onOpenSettings: () => void;
}) {
  const retry = useRetry();
  return (
    <Card className="mx-auto max-w-xl">
      <CardContent className="space-y-3 p-6">
        <h2 className="text-lg font-semibold">Setup needed</h2>
        <p className="text-sm">{status.message}</p>
        {status.hint && (
          <p className="rounded-md bg-muted/50 px-3 py-2 text-sm text-muted-foreground">
            {status.hint}
          </p>
        )}
        <p className="text-xs text-muted-foreground">
          Config: <code className="rounded bg-muted px-1.5 py-0.5">{status.config_path}</code>
          <br />
          DB: <code className="rounded bg-muted px-1.5 py-0.5">{status.db_path}</code>
        </p>
        <div className="flex gap-2">
          <Button onClick={onOpenSettings}>Open Settings</Button>
          <Button variant="outline" onClick={() => retry.mutate()} disabled={retry.isPending}>
            <RefreshCw className={retry.isPending ? "animate-spin" : ""} />
            {retry.isPending ? "Retrying…" : "Retry"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function EmptyState() {
  return (
    <Card className="border-dashed">
      <CardContent className="p-10 text-center">
        <h3 className="text-base font-semibold">No memories yet</h3>
        <p className="mt-1 text-sm text-muted-foreground">
          Memories appear here as your MCP client calls <code>store_memory</code>, or as the dreamer
          extracts them from recorded conversations.
        </p>
      </CardContent>
    </Card>
  );
}
