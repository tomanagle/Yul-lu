import { useEffect, useState } from "react";
import { Sparkles } from "lucide-react";

import {
  useDreamPasses,
  useDreamStats,
  useMemoryEventsByDay,
  useMemoryGraph,
  useMemoryStats,
  useSessionStats,
  useStatus,
  useUsageByDay,
  useUsageSummary,
} from "@/lib/queries";
import { useProjectScope } from "@/lib/project-scope";
import { MemoryEventsChart, UsageByModelChart, UsageCostChart } from "@/components/charts";
import { MemoryGraphCanvas, useDecoratedGraph } from "@/components/memory-graph";
import { relativeTime, shortUUID } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// Date-range presets. Custom calendar pickers can come later; the four
// preset windows cover ~all the questions a dashboard answers ("this week",
// "this month", "this quarter", "this year"). Stored in localStorage so a
// reload restores the user's pick.
const RANGES = [
  { label: "7d", days: 7 },
  { label: "30d", days: 30 },
  { label: "90d", days: 90 },
  { label: "1y", days: 365 },
] as const;

const RANGE_KEY = "yullu.stats.range";

function loadRange(): number {
  if (typeof window === "undefined") return 7;
  const saved = window.localStorage.getItem(RANGE_KEY);
  if (!saved) return 7;
  const n = parseInt(saved, 10);
  return RANGES.some((r) => r.days === n) ? n : 7;
}

function rangeLabel(days: number): string {
  return RANGES.find((r) => r.days === days)?.label ?? `${days}d`;
}

export function StatsPage() {
  const { data: status } = useStatus();
  // Project scope is owned by the sidebar; every data page reads it from
  // context so a single picker drives the whole UI.
  const { project } = useProjectScope();
  const [days, setDays] = useState<number>(loadRange);

  useEffect(() => {
    window.localStorage.setItem(RANGE_KEY, String(days));
  }, [days]);

  const statsQuery = useMemoryStats(project);
  const eventsQuery = useMemoryEventsByDay(project, days);
  const usageDayQuery = useUsageByDay(days);
  // Provider/model bar chart honours the same range, expressed in hours.
  const usageSummaryQuery = useUsageSummary(days * 24);
  const graphQuery = useMemoryGraph(project);
  const graphDecorated = useDecoratedGraph(graphQuery.data);
  // Dreaming activity: live buffer + persisted history over the range.
  const sessionQuery = useSessionStats(project);
  const dreamStatsQuery = useDreamStats(project, days);

  if (!status?.ready) {
    return (
      <p className="text-sm text-muted-foreground">
        Finish setup on the Memories page before viewing stats.
      </p>
    );
  }

  const stats = statsQuery.data;
  const dreamPassesQuery = useDreamPasses(project);

  return (
    <div className="space-y-6">
      {/* Range picker, right-aligned. Project picker is in the sidebar.
          Removed the Refresh button — every query auto-refetches on an
          interval, so manual refresh was redundant. */}
      <div className="flex items-center justify-end gap-3">
        <RangeSelect value={days} onChange={setDays} />
      </div>

      {!stats && <p className="text-sm text-muted-foreground">Loading…</p>}

      {stats && (
        <>
          {/* KPI strip - 5 hero tiles, top-line metrics. */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
            <KpiTile
              label="Memories"
              value={stats.total_memories}
              sub="total in store"
              accent="indigo"
            />
            <KpiTile
              label="Recalled today"
              value={stats.counts.recalled_day}
              sub={`${stats.counts.recalled_week} this week`}
              accent="violet"
              icon={<Sparkles className="h-3.5 w-3.5" />}
            />
            <KpiTile
              label="Created today"
              value={stats.counts.created_day}
              sub={`${stats.counts.created_week} this week`}
            />
            <KpiTile
              label="Updated today"
              value={stats.counts.updated_day}
              sub={`${stats.counts.updated_week} this week`}
            />
            <KpiTile
              label="Deleted today"
              value={stats.counts.deleted_day}
              sub={`${stats.counts.deleted_week} this week`}
            />
          </div>

          {/* Dreaming activity - live buffer + persisted aggregate over the
              selected range. If the dreamer is healthy, "messages buffered"
              stays small (gets drained) and "passes" grows steadily. */}
          <DreamingCard
            range={rangeLabel(days)}
            buffer={sessionQuery.data}
            stats={dreamStatsQuery.data}
          />

          {/* Per-cycle history - the last N dream passes. Surfaces which
              passes were noisy (lots of skipped ops, errors) vs which
              actually produced memories. */}
          <DreamPassesCard passes={dreamPassesQuery.data ?? []} />

          {/* 2-up charts row */}
          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-base">Memory events</CardTitle>
                <CardDescription>
                  Daily lifecycle counts - last {rangeLabel(days)}, stacked.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <MemoryEventsChart data={eventsQuery.data ?? []} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-base">LLM usage</CardTitle>
                <CardDescription>
                  Daily cost in cents and call count - last {rangeLabel(days)}.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <UsageCostChart data={usageDayQuery.data ?? []} />
              </CardContent>
            </Card>
          </div>

          {/* Memory graph - full-width hero tile */}
          <Card className="overflow-hidden">
            <CardHeader className="pb-2">
              <div className="flex items-baseline justify-between gap-3">
                <div>
                  <CardTitle className="text-base">Memory graph</CardTitle>
                  <CardDescription>
                    Nodes are memories; edges connect by shared tag (faint) or embedding similarity
                    (violet). Larger node = more recalls.
                  </CardDescription>
                </div>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {graphDecorated.nodes.length} memories · {graphDecorated.links.length} edges
                </span>
              </div>
            </CardHeader>
            <CardContent className="p-0">
              <div className="h-[420px]">
                <MemoryGraphCanvas
                  nodes={graphDecorated.nodes}
                  links={graphDecorated.links}
                  loading={graphQuery.isLoading}
                  error={graphQuery.error ? String(graphQuery.error) : null}
                />
              </div>
            </CardContent>
          </Card>

          {/* 2-up bottom - recalls + providers */}
          <div className="grid gap-4 lg:grid-cols-5">
            <Card className="lg:col-span-3">
              <CardHeader className="pb-2">
                <CardTitle className="text-base">Top recalled</CardTitle>
                <CardDescription>
                  Memories returned by retrieve_memories most often.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <TopRecalled rows={stats.top_recalled ?? []} />
              </CardContent>
            </Card>

            <Card className="lg:col-span-2">
              <CardHeader className="pb-2">
                <CardTitle className="text-base">By provider/model</CardTitle>
                <CardDescription>Calls + cost, last {rangeLabel(days)}.</CardDescription>
              </CardHeader>
              <CardContent>
                <UsageByModelChart data={usageSummaryQuery.data ?? []} />
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </div>
  );
}

function KpiTile({
  label,
  value,
  sub,
  accent,
  icon,
}: {
  label: string;
  value: number;
  sub?: string;
  accent?: "indigo" | "violet";
  icon?: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "relative overflow-hidden rounded-lg border border-border/50 bg-card/60 p-4",
        // Soft tinted glow only on the highlighted tiles - keeps the rest
        // visually quiet so the eye lands on the headline metrics.
        accent === "indigo" &&
          "border-primary/30 bg-gradient-to-br from-primary/10 via-card/60 to-card/60",
        accent === "violet" &&
          "border-accent/30 bg-gradient-to-br from-accent/10 via-card/60 to-card/60",
      )}
    >
      <div className="flex items-baseline gap-2">
        <div className="yullu-display text-3xl text-foreground">{value}</div>
        {icon && (
          <span
            className={cn(
              "text-muted-foreground",
              accent === "violet" && "text-violet-soft",
              accent === "indigo" && "text-indigo-soft",
            )}
          >
            {icon}
          </span>
        )}
      </div>
      <div className="yullu-label mt-2 text-muted-foreground">{label}</div>
      {sub && <div className="mt-0.5 text-[11px] text-muted-foreground/80">{sub}</div>}
    </div>
  );
}

function TopRecalled({
  rows,
}: {
  rows: {
    memory: { id: number; uuid: string; content: string; tags?: string[]; updated_at: string };
    count: number;
  }[];
}) {
  if (rows.length === 0) {
    return (
      <p className="py-4 text-sm text-muted-foreground">
        No recalls yet. Memories appear here once retrieve_memories starts surfacing them.
      </p>
    );
  }
  return (
    <ul className="space-y-2">
      {rows.slice(0, 5).map((row, i) => (
        <li
          key={row.memory.id}
          className="group flex items-start gap-3 rounded-md border border-border/40 bg-muted/30 p-3 transition-colors hover:border-accent/40 hover:bg-muted/50"
        >
          <div className="yullu-tabular w-6 shrink-0 pt-0.5 text-center text-xs text-muted-foreground">
            {i + 1}
          </div>
          <div className="min-w-0 flex-1 space-y-1">
            <p className="line-clamp-2 text-sm leading-relaxed">{row.memory.content}</p>
            {(row.memory.tags ?? []).length > 0 && (
              <div className="flex flex-wrap gap-1">
                {row.memory.tags!.slice(0, 3).map((t) => (
                  <Badge key={t} variant="secondary" className="px-1.5 py-0 text-[10px]">
                    {t}
                  </Badge>
                ))}
              </div>
            )}
            <div className="flex gap-2 text-[10px] text-muted-foreground/80">
              <span className="font-mono">{shortUUID(row.memory.uuid)}</span>
              <span>·</span>
              <span>{relativeTime(row.memory.updated_at)}</span>
            </div>
          </div>
          <div className="shrink-0 text-right">
            <div className="yullu-tabular text-lg font-semibold text-violet-soft">{row.count}</div>
            <div className="text-[10px] text-muted-foreground">recalls</div>
          </div>
        </li>
      ))}
    </ul>
  );
}

// DreamingCard surfaces both live state (current buffer) and historical
// aggregate (passes / ops over the range). Importing the types inline here
// keeps the component co-located with the page; if it grows much further
// it can move to components/.
function DreamingCard({
  range,
  buffer,
  stats,
}: {
  range: string;
  buffer?: { sessions: number; messages: number };
  stats?: {
    passes: number;
    sessions_processed: number;
    messages_processed: number;
    ops_created: number;
    ops_updated: number;
    ops_deleted: number;
    errors: number;
    last_pass_at?: string;
  };
}) {
  const lastPass =
    stats?.last_pass_at && stats.last_pass_at !== "" ? relativeTime(stats.last_pass_at) : "never";

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-baseline justify-between gap-3">
          <div>
            <CardTitle className="text-base">Dreaming</CardTitle>
            <CardDescription>
              Live buffer of recorded messages + dream-pass activity over the last {range}.
            </CardDescription>
          </div>
          <span className="shrink-0 text-xs text-muted-foreground">last pass: {lastPass}</span>
        </div>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-7">
          <Stat label="Buffered sessions" value={buffer?.sessions ?? 0} />
          <Stat
            label="Buffered messages"
            value={buffer?.messages ?? 0}
            hint={(buffer?.messages ?? 0) === 0 ? "Nothing waiting" : "Waiting to dream"}
          />
          <Stat label="Passes" value={stats?.passes ?? 0} accent="indigo" />
          <Stat label="Sessions processed" value={stats?.sessions_processed ?? 0} />
          <Stat label="Created" value={stats?.ops_created ?? 0} accent="violet" />
          <Stat label="Updated" value={stats?.ops_updated ?? 0} />
          <Stat label="Deleted" value={stats?.ops_deleted ?? 0} />
        </div>
        {(stats?.errors ?? 0) > 0 && (
          <p className="mt-3 text-xs text-destructive">
            {stats?.errors} error{stats?.errors === 1 ? "" : "s"} during recent passes. Check the
            dreaming page for details.
          </p>
        )}
        {(buffer?.messages ?? 0) > 0 && (stats?.passes ?? 0) === 0 && (
          <p className="mt-3 text-xs text-muted-foreground">
            Messages are buffered but no dream pass has run in this window. Configure a direct
            reasoner in Settings or hit “Dream now” on the Dreaming page.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function Stat({
  label,
  value,
  hint,
  accent,
}: {
  label: string;
  value: number;
  hint?: string;
  accent?: "indigo" | "violet";
}) {
  return (
    <div className="rounded-md border border-border/40 bg-card/40 p-3">
      <div
        className={cn(
          "yullu-display text-2xl",
          accent === "indigo" && "text-indigo-soft",
          accent === "violet" && "text-violet-soft",
        )}
      >
        {value}
      </div>
      <div className="yullu-label mt-1 text-muted-foreground">{label}</div>
      {hint && <div className="mt-0.5 text-[10px] text-muted-foreground/80">{hint}</div>}
    </div>
  );
}

// Segmented control over RANGES. Tailwind handles the active/hover states;
// the parent owns the value and persists it (see loadRange / RANGE_KEY).
function RangeSelect({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  return (
    <div
      role="radiogroup"
      aria-label="Time range"
      className="inline-flex items-center gap-0.5 rounded-md border border-border/40 bg-card/40 p-0.5"
    >
      {RANGES.map((r) => {
        const active = value === r.days;
        return (
          <button
            key={r.days}
            role="radio"
            aria-checked={active}
            onClick={() => onChange(r.days)}
            className={cn(
              "rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active
                ? "bg-primary/15 text-foreground shadow-[inset_0_0_0_1px_hsl(244_76%_51%/0.35)]"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {r.label}
          </button>
        );
      })}
    </div>
  );
}

// DreamPassesCard shows the most-recent N dream passes as a compact
// table. Each row = one cycle: when, sessions/messages processed, ops
// breakdown. Errors get a small badge so they're easy to spot without
// having to expand anything. Empty state covers brand-new installs.
function DreamPassesCard({ passes }: { passes: import("@/lib/schemas").DreamPass[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Dream cycles</CardTitle>
        <CardDescription>
          The {passes.length || "last few"} most recent passes. Skipped =
          ops the reasoner emitted that didn't apply (missing UUID, empty
          content, etc.).
        </CardDescription>
      </CardHeader>
      <CardContent>
        {passes.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            No dream passes recorded yet for this scope.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead className="text-muted-foreground">
                <tr className="border-b border-border/40 text-left">
                  <th className="py-1.5 pr-3 font-medium">When</th>
                  <th className="py-1.5 pr-3 font-medium">Sessions</th>
                  <th className="py-1.5 pr-3 font-medium">Messages</th>
                  <th className="py-1.5 pr-3 font-medium text-emerald-400/80">+ Created</th>
                  <th className="py-1.5 pr-3 font-medium text-sky-400/80">~ Updated</th>
                  <th className="py-1.5 pr-3 font-medium text-rose-400/80">− Deleted</th>
                  <th className="py-1.5 pr-3 font-medium">Skipped</th>
                  <th className="py-1.5 font-medium">Errors</th>
                </tr>
              </thead>
              <tbody>
                {passes.map((p) => (
                  <tr key={p.id} className="border-b border-border/20 last:border-0">
                    <td className="py-1.5 pr-3 font-mono text-[11px] text-muted-foreground">
                      {relativeTime(p.at)}
                    </td>
                    <td className="py-1.5 pr-3">{p.sessions_processed}</td>
                    <td className="py-1.5 pr-3">{p.messages_processed}</td>
                    <td className="py-1.5 pr-3">{p.ops_created}</td>
                    <td className="py-1.5 pr-3">{p.ops_updated}</td>
                    <td className="py-1.5 pr-3">{p.ops_deleted}</td>
                    <td className="py-1.5 pr-3 text-muted-foreground">{p.ops_skipped}</td>
                    <td className="py-1.5">
                      {p.errors?.length ? (
                        <Badge variant="destructive" className="text-[10px]">
                          {p.errors.length}
                        </Badge>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
