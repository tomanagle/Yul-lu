// Retrievals surface — read-only observability for what the agent actually
// received. Each entry is one retrieve call: the query, when it ran, and the
// memories that cleared the similarity threshold and were sent back, in rank
// order with their match %. This is the view for sanity-checking and tuning
// the retrieval threshold ("is the right memory ranking high? is noise
// getting in?"), grouped by query rather than one row per memory.

import { useProjectScope } from "@/lib/project-scope";
import { useRetrievals } from "@/lib/queries";
import { relativeTime } from "@/lib/format";
import type { RetrievalGroup, RetrievalMemory } from "@/lib/schemas";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

export function RetrievalsPage() {
  const { project } = useProjectScope();
  const { data, isLoading } = useRetrievals(project);

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Retrievals</CardTitle>
          <CardDescription>
            What the agent actually received. Each entry is one retrieve call — the query, and the
            memories that cleared the similarity threshold and were sent back, in rank order. Use it
            to tune the threshold in Settings: watch for noise getting in, or the right memory
            ranking just below the cut.
          </CardDescription>
        </CardHeader>
      </Card>

      {!project && (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            Pick a project in the sidebar to see its retrievals.
          </CardContent>
        </Card>
      )}

      {project && isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {project && !isLoading && (!data || data.length === 0) && (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            No retrievals yet. Entries appear here once a query returns memories to the agent.
          </CardContent>
        </Card>
      )}
      {project &&
        data?.map((g) => (
          <RetrievalGroupCard key={g.recall_id || `${g.query}-${g.at}`} group={g} />
        ))}
    </div>
  );
}

function RetrievalGroupCard({ group }: { group: RetrievalGroup }) {
  const total = group.memories.length;
  const sent = group.memories.filter((m) => m.injected !== false).length;
  return (
    <Card className="overflow-hidden">
      <div className="flex">
        {/* Accent spine. */}
        <div className="w-1 shrink-0 bg-primary/60" />
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-3 border-b border-border/60 px-4 py-3">
            <div className="min-w-0">
              <p className="font-medium text-foreground">
                <span className="text-muted-foreground">query:</span> “{group.query || "—"}”
              </p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {sent} of {total} sent to the agent
                {sent < total && " · rest dropped below the threshold"}
              </p>
            </div>
            <span className="shrink-0 whitespace-nowrap text-xs text-muted-foreground">
              {relativeTime(group.at)}
            </span>
          </div>
          <ul className="divide-y divide-border/40">
            {group.memories.map((m, i) => (
              <MemoryRow key={`${m.memory_id}-${i}`} memory={m} />
            ))}
          </ul>
        </div>
      </div>
    </Card>
  );
}

function MemoryRow({ memory }: { memory: RetrievalMemory }) {
  const pct = Math.round((memory.similarity ?? 0) * 100);
  const deleted = !memory.content;
  // Missing flag (legacy rows) → treat as injected.
  const injected = memory.injected !== false;
  const dotTitle = injected
    ? `Injected — sent to the agent (${pct}% match, rank #${memory.rank ?? "?"})`
    : `Dropped — ${pct}% was below the similarity threshold, so it was not sent`;
  return (
    <li className="flex gap-3 px-4 py-3">
      {/* Status dot: green = injected, red = dropped. Native title tooltip. */}
      <span
        title={dotTitle}
        aria-label={injected ? "Injected" : "Dropped"}
        className={cn(
          "mt-1.5 h-2.5 w-2.5 shrink-0 cursor-help rounded-full ring-2",
          injected ? "bg-emerald-500 ring-emerald-500/20" : "bg-rose-500 ring-rose-500/20",
        )}
      />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span
            className={cn(
              "yullu-tabular font-semibold",
              injected ? (pct >= 80 ? "text-primary" : "text-foreground") : "text-muted-foreground",
            )}
          >
            {pct}%
          </span>
          <span className="yullu-tabular text-muted-foreground">#{memory.rank ?? "?"}</span>
          {!injected && <span className="text-rose-500/90">dropped</span>}
          {memory.category && (
            <Badge variant="secondary" className="text-[10px]">
              {memory.category}
            </Badge>
          )}
        </div>
        <p
          className={cn(
            "mt-1 whitespace-pre-wrap break-words text-sm leading-relaxed",
            deleted ? "italic text-muted-foreground" : "text-foreground/90",
            !injected && "opacity-70",
          )}
        >
          {deleted ? "(memory has since been deleted)" : memory.content}
        </p>
      </div>
    </li>
  );
}
