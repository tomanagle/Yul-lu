import { useEffect, useState } from "react";
import { ArrowUpRight, Brain, LayoutDashboard, Moon, RefreshCw, Sparkles } from "lucide-react";

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
import {
  CompositionDonut,
  DayBars,
  MemoryEventsChart,
  UsageByModelChart,
  UsageCostChart,
  type DonutSegment,
} from "@/components/charts";
import { MemoryGraphCanvas, useDecoratedGraph } from "@/components/memory-graph";
import { useThemeTokens } from "@/lib/use-theme-tokens";
import { relativeTime, shortUUID } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { PageLayout } from "@/components/page-layout";
import { cn } from "@/lib/utils";

// Date-range presets — the four windows cover ~all the questions a dashboard
// answers. Persisted to localStorage so a reload restores the pick.
const RANGES = [
  { label: "7D", days: 7 },
  { label: "30D", days: 30 },
  { label: "90D", days: 90 },
  { label: "1Y", days: 365 },
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
  return RANGES.find((r) => r.days === days)?.label ?? `${days}D`;
}

export function StatsPage() {
  const { data: status } = useStatus();
  const { project } = useProjectScope();
  const [days, setDays] = useState<number>(loadRange);
  const t = useThemeTokens();

  useEffect(() => {
    window.localStorage.setItem(RANGE_KEY, String(days));
  }, [days]);

  const statsQuery = useMemoryStats(project);
  const eventsQuery = useMemoryEventsByDay(project, days);
  const usageDayQuery = useUsageByDay(days);
  const usageSummaryQuery = useUsageSummary(days * 24);
  const graphQuery = useMemoryGraph(project);
  const graphDecorated = useDecoratedGraph(graphQuery.data);
  // IMPORTANT: every hook runs on every render — keep queries above the
  // early return so the hook count never changes.
  const sessionQuery = useSessionStats(project);
  const dreamStatsQuery = useDreamStats(project, days);
  const dreamPassesQuery = useDreamPasses(project);

  const scopeLabel = project || "all projects";

  if (!status?.ready) {
    return (
      <p className="text-sm text-muted-foreground">
        Finish setup on the Memories page before viewing stats.
      </p>
    );
  }

  const stats = statsQuery.data;
  const events = eventsQuery.data ?? [];

  const donutSegments: DonutSegment[] = stats
    ? [
        { name: "Created", value: stats.counts.created_all, color: t.chart1 },
        { name: "Recalled", value: stats.counts.recalled_all, color: t.chart3 },
        { name: "Updated", value: stats.counts.updated_all, color: t.chart2 },
        { name: "Deleted", value: stats.counts.deleted_all, color: t.chart6 },
      ]
    : [];

  return (
    <PageLayout
      icon={LayoutDashboard}
      title="Dashboard"
      description={
        <>
          Memory activity for <span className="text-foreground/80">{scopeLabel}</span>.
        </>
      }
      actions={<RangeSelect value={days} onChange={setDays} />}
      fullWidth
    >
      {!stats && <p className="text-sm text-muted-foreground">Loading…</p>}

      {stats && (
        <>
          {/* Overview — the headline numbers, at a glance. */}
          <section className="space-y-3">
            <SectionHeading>Overview</SectionHeading>
            <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
              <KpiCard
                label="Total memories"
                value={stats.total_memories}
                sub={`${stats.counts.created_week} created this week`}
                icon={<Brain className="h-5 w-5" />}
                tone="primary"
              />
              <KpiCard
                label="Recalled today"
                value={stats.counts.recalled_day}
                sub={`${stats.counts.recalled_week} this week`}
                icon={<Sparkles className="h-5 w-5" />}
                tone="violet"
              />
              <KpiCard
                label="Created today"
                value={stats.counts.created_day}
                sub={`${stats.counts.created_all} all time`}
                icon={<ArrowUpRight className="h-5 w-5" />}
                tone="teal"
              />
              <KpiCard
                label="Dream passes"
                value={dreamStatsQuery.data?.passes ?? 0}
                sub={`${dreamStatsQuery.data?.ops_created ?? 0} memories made`}
                icon={<Moon className="h-5 w-5" />}
                tone="amber"
              />
            </div>
          </section>

          {/* Memory activity — what's created/recalled, over time and as a mix. */}
          <section className="space-y-3">
            <SectionHeading>Memory activity</SectionHeading>
            <div className="grid gap-4 lg:grid-cols-3">
              <Card className="lg:col-span-2">
                <CardHeader className="pb-2">
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <CardTitle className="text-base">Activity over time</CardTitle>
                      <CardDescription>
                        Daily lifecycle events · last {rangeLabel(days)}, stacked.
                      </CardDescription>
                    </div>
                    {stats.counts.created_week > 0 && (
                      <span className="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">
                        <ArrowUpRight className="h-3.5 w-3.5" />+{stats.counts.created_week} this
                        week
                      </span>
                    )}
                  </div>
                </CardHeader>
                <CardContent>
                  <MemoryEventsChart data={events} />
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-base">Composition</CardTitle>
                  <CardDescription>Lifecycle event mix, all time.</CardDescription>
                </CardHeader>
                <CardContent>
                  <CompositionDonut
                    segments={donutSegments}
                    centerValue={stats.total_memories.toLocaleString()}
                    centerLabel="memories"
                  />
                  <ul className="mt-2 grid grid-cols-2 gap-1.5">
                    {donutSegments.map((s) => (
                      <li key={s.name} className="flex items-center gap-2 text-xs">
                        <span
                          className="h-2.5 w-2.5 shrink-0 rounded-full"
                          style={{ background: s.color }}
                        />
                        <span className="text-muted-foreground">{s.name}</span>
                        <span className="yullu-tabular ml-auto font-medium text-foreground">
                          {s.value.toLocaleString()}
                        </span>
                      </li>
                    ))}
                  </ul>
                </CardContent>
              </Card>
            </div>

            <div className="grid gap-4 lg:grid-cols-3">
              <Card className="lg:col-span-2">
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

              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-base">Recalls per day</CardTitle>
                  <CardDescription>How often memories were retrieved.</CardDescription>
                </CardHeader>
                <CardContent>
                  <DayBars data={events} dataKey="recalled" />
                </CardContent>
              </Card>
            </div>
          </section>

          {/* Dreaming — buffer + extraction activity, and the per-cycle log. */}
          <section className="space-y-3">
            <SectionHeading>Dreaming</SectionHeading>
            <DreamingCard
              range={rangeLabel(days)}
              buffer={sessionQuery.data}
              stats={dreamStatsQuery.data}
            />
            <DreamPassesCard passes={dreamPassesQuery.data ?? []} />
          </section>

          {/* Usage & cost — what the embedder/reasoner spent. */}
          <section className="space-y-3">
            <SectionHeading>Usage &amp; cost</SectionHeading>
            <div className="grid gap-4 lg:grid-cols-3">
              <Card className="lg:col-span-2">
                <CardHeader className="pb-2">
                  <CardTitle className="text-base">Cost over time</CardTitle>
                  <CardDescription>
                    Daily cost in cents and call count · last {rangeLabel(days)}.
                  </CardDescription>
                </CardHeader>
                <CardContent>
                  <UsageCostChart data={usageDayQuery.data ?? []} />
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-base">By provider/model</CardTitle>
                  <CardDescription>Calls + cost · last {rangeLabel(days)}.</CardDescription>
                </CardHeader>
                <CardContent>
                  <UsageByModelChart data={usageSummaryQuery.data ?? []} />
                </CardContent>
              </Card>
            </div>
          </section>

          {/* Graph — exploratory view of how memories connect. */}
          <section className="space-y-3">
            <SectionHeading>Graph</SectionHeading>
            <Card className="overflow-hidden">
              <CardHeader className="pb-2">
                <div className="flex items-baseline justify-between gap-3">
                  <div>
                    <CardTitle className="text-base">Memory graph</CardTitle>
                    <CardDescription>
                      Nodes are memories; edges connect by shared tag (faint) or embedding
                      similarity. Larger node = more recalls.
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
          </section>
        </>
      )}
    </PageLayout>
  );
}

// SectionHeading labels each dashboard group so the page reads as organized
// sections rather than a flat stack of cards.
function SectionHeading({ children }: { children: React.ReactNode }) {
  return <h2 className="yullu-label text-muted-foreground">{children}</h2>;
}

// KpiCard — label + big number + a sub-line, with a tinted circular icon on
// the right. Mirrors the reference's Income/Saving/Expense tiles.
const TONES = {
  primary: "bg-primary/12 text-primary",
  violet: "bg-accent/12 text-accent",
  teal: "bg-chart-2/15 text-chart-2",
  amber: "bg-chart-4/15 text-chart-4",
} as const;

function KpiCard({
  label,
  value,
  sub,
  icon,
  tone,
}: {
  label: string;
  value: number;
  sub?: string;
  icon: React.ReactNode;
  tone: keyof typeof TONES;
}) {
  return (
    <Card className="p-5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="yullu-label text-muted-foreground">{label}</div>
          <div className="yullu-display mt-2 text-3xl text-foreground">
            {value.toLocaleString()}
          </div>
        </div>
        <span
          className={cn("grid h-11 w-11 shrink-0 place-items-center rounded-full", TONES[tone])}
        >
          {icon}
        </span>
      </div>
      {sub && (
        <div className="mt-3 flex items-center gap-1 text-xs text-muted-foreground">
          <ArrowUpRight className="h-3.5 w-3.5 text-primary" />
          {sub}
        </div>
      )}
    </Card>
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
          className="group flex items-start gap-3 rounded-xl border border-border/60 bg-muted/40 p-3 transition-colors hover:border-primary/40 hover:bg-muted/60"
        >
          <div className="yullu-tabular w-6 shrink-0 pt-0.5 text-center text-sm text-muted-foreground">
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
            <div className="yullu-tabular text-lg font-semibold text-accent">{row.count}</div>
            <div className="text-[10px] text-muted-foreground">recalls</div>
          </div>
        </li>
      ))}
    </ul>
  );
}

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
          <Stat label="Passes" value={stats?.passes ?? 0} accent="primary" />
          <Stat label="Sessions processed" value={stats?.sessions_processed ?? 0} />
          <Stat label="Created" value={stats?.ops_created ?? 0} accent="violet" />
          <Stat label="Updated" value={stats?.ops_updated ?? 0} />
          <Stat label="Deleted" value={stats?.ops_deleted ?? 0} />
        </div>
        {(stats?.errors ?? 0) > 0 && (
          <p className="mt-3 text-sm text-destructive">
            {stats?.errors} error{stats?.errors === 1 ? "" : "s"} during recent passes. Check the
            dreaming page for details.
          </p>
        )}
        {(buffer?.messages ?? 0) > 0 && (stats?.passes ?? 0) === 0 && (
          <p className="mt-3 text-sm text-muted-foreground">
            Messages are buffered but no dream pass has run in this window. Configure a direct
            reasoner in Settings or hit "Dream now" on the Dreaming page.
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
  accent?: "primary" | "violet";
}) {
  return (
    <div className="rounded-xl border border-border/60 bg-muted/40 p-3">
      <div
        className={cn(
          "yullu-display text-2xl text-foreground",
          accent === "primary" && "text-primary",
          accent === "violet" && "text-accent",
        )}
      >
        {value}
      </div>
      <div className="yullu-label mt-1 text-muted-foreground">{label}</div>
      {hint && <div className="mt-0.5 text-[10px] text-muted-foreground/80">{hint}</div>}
    </div>
  );
}

// Segmented control over RANGES — the period pills (7D/30D/90D/1Y).
function RangeSelect({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  return (
    <div
      role="radiogroup"
      aria-label="Time range"
      className="inline-flex items-center gap-0.5 rounded-full border border-border/70 bg-card/60 p-1 shadow-card"
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
              "rounded-full px-3 py-1 text-xs font-semibold transition-colors duration-150",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active
                ? "bg-primary text-primary-foreground yullu-ring-primary"
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

function DreamPassesCard({ passes }: { passes: import("@/lib/schemas").DreamPass[] }) {
  const totalOpsCreated = passes.reduce((acc, p) => acc + p.ops_created, 0);
  const totalOpsUpdated = passes.reduce((acc, p) => acc + p.ops_updated, 0);
  const totalOpsDeleted = passes.reduce((acc, p) => acc + p.ops_deleted, 0);
  const totalOpsSkipped = passes.reduce((acc, p) => acc + p.ops_skipped, 0);
  const totalErrors = passes.reduce((acc, p) => acc + (p.errors?.length ?? 0), 0);
  const totalSessionsProcessed = passes.reduce((acc, p) => acc + p.sessions_processed, 0);
  const totalMessagesProcessed = passes.reduce((acc, p) => acc + p.messages_processed, 0);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <RefreshCw className="h-4 w-4 text-muted-foreground" />
          Dream cycles
        </CardTitle>
        <CardDescription>
          The {passes.length || "last few"} most recent passes. Skipped = ops the reasoner emitted
          that didn't apply (missing UUID, empty content, etc.).
        </CardDescription>
      </CardHeader>
      <CardContent>
        {passes.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No dream passes recorded yet for this scope.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-muted-foreground">
                <tr className="border-b border-border/60 text-left">
                  <th className="py-1.5 pr-3 font-medium">When</th>
                  <th className="py-1.5 pr-3 font-medium">
                    Sessions{" "}
                    <span className="text-muted-foreground">({totalSessionsProcessed})</span>
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    Messages{" "}
                    <span className="text-muted-foreground">({totalMessagesProcessed})</span>
                  </th>
                  <th className="py-1.5 pr-3 font-medium text-chart-2">
                    + Created <span className="text-muted-foreground">({totalOpsCreated})</span>
                  </th>
                  <th className="py-1.5 pr-3 font-medium text-chart-1">
                    ~ Updated <span className="text-muted-foreground">({totalOpsUpdated})</span>
                  </th>
                  <th className="py-1.5 pr-3 font-medium text-chart-6">
                    − Deleted <span className="text-muted-foreground">({totalOpsDeleted})</span>
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    Skipped <span className="text-muted-foreground">({totalOpsSkipped})</span>
                  </th>
                  <th className="py-1.5 font-medium">
                    Errors <span className="text-muted-foreground">({totalErrors})</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {passes.map((p) => (
                  <tr key={p.id} className="border-b border-border/30 last:border-0">
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
                        <span className="text-muted-foreground">-</span>
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
