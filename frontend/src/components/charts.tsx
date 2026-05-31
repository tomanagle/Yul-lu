// Thin Recharts wrappers that pick up our theme tokens via CSS vars. Kept in
// one file because all three charts share the same axis/tooltip styling.

import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import type { DailyMemoryEvents, DailyUsage, UsageBucket } from "@/lib/schemas";

// Chart colours mirror the night-indigo + dream-violet palette in index.css.
// Recharts SVG can't read CSS variables directly, so the values are inlined
// - keep these in lockstep with --primary / --accent / --muted-foreground.
const CHART_COLORS = {
  created: "#6366F1", // indigo-soft  - new memories
  recalled: "#A78BFA", // violet-soft  - retrievals
  updated: "#22D3EE", // cyan         - edits
  deleted: "#F87171", // soft red     - deletes
  primary: "#6366F1",
  accent: "#7C3AED",
};

const AXIS_STYLE = {
  fontSize: 11,
  fill: "hsl(215, 20%, 65%)", // matches --muted-foreground
};

const TOOLTIP_STYLE: React.CSSProperties = {
  backgroundColor: "hsl(222, 35%, 15%)", // card
  border: "1px solid hsl(232, 18%, 24%)", // border
  borderRadius: 8,
  fontSize: 12,
  color: "hsl(210, 40%, 96%)", // foreground
  boxShadow: "0 8px 24px hsl(222 47% 4% / 0.5)",
};

function shortDay(iso: string): string {
  // "2026-05-27" -> "May 27"
  const d = new Date(iso + "T00:00:00");
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function MemoryEventsChart({ data }: { data: DailyMemoryEvents[] }) {
  return (
    <ResponsiveContainer width="100%" height={240}>
      <AreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="hsl(222, 14%, 18%)" />
        <XAxis dataKey="day" tickFormatter={shortDay} {...AXIS_STYLE} />
        <YAxis allowDecimals={false} {...AXIS_STYLE} />
        <Tooltip contentStyle={TOOLTIP_STYLE} labelFormatter={shortDay} />
        <Legend wrapperStyle={{ fontSize: 12, paddingTop: 8 }} />
        <Area
          type="monotone"
          dataKey="created"
          stackId="1"
          stroke={CHART_COLORS.created}
          fill={CHART_COLORS.created}
          fillOpacity={0.35}
        />
        <Area
          type="monotone"
          dataKey="recalled"
          stackId="1"
          stroke={CHART_COLORS.recalled}
          fill={CHART_COLORS.recalled}
          fillOpacity={0.35}
        />
        <Area
          type="monotone"
          dataKey="updated"
          stackId="1"
          stroke={CHART_COLORS.updated}
          fill={CHART_COLORS.updated}
          fillOpacity={0.35}
        />
        <Area
          type="monotone"
          dataKey="deleted"
          stackId="1"
          stroke={CHART_COLORS.deleted}
          fill={CHART_COLORS.deleted}
          fillOpacity={0.35}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

export function UsageCostChart({ data }: { data: DailyUsage[] }) {
  // Show cents (microcents/10^6) on the axis - most days will be sub-cent
  // anyway. Tooltip shows fully formatted dollars.
  const chartData = data.map((d) => ({
    ...d,
    cost_cents: (d.cost_microcents_usd ?? 0) / 1_000_000,
  }));

  return (
    <ResponsiveContainer width="100%" height={240}>
      <LineChart data={chartData} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="hsl(222, 14%, 18%)" />
        <XAxis dataKey="day" tickFormatter={shortDay} {...AXIS_STYLE} />
        <YAxis tickFormatter={(v: number) => `${v.toFixed(1)}¢`} {...AXIS_STYLE} />
        <Tooltip
          contentStyle={TOOLTIP_STYLE}
          labelFormatter={shortDay}
          formatter={(value: number, name: string) => {
            if (name === "Cost") {
              const dollars = value / 100;
              return [`$${dollars.toFixed(6)}`, name];
            }
            return [value, name];
          }}
        />
        <Legend wrapperStyle={{ fontSize: 12, paddingTop: 8 }} />
        <Line
          type="monotone"
          dataKey="cost_cents"
          name="Cost"
          stroke={CHART_COLORS.primary}
          strokeWidth={2}
          dot={false}
        />
        <Line
          type="monotone"
          dataKey="calls"
          name="Calls"
          stroke={CHART_COLORS.accent}
          strokeWidth={2}
          dot={false}
        />
      </LineChart>
    </ResponsiveContainer>
  );
}

export function UsageByModelChart({ data }: { data: UsageBucket[] }) {
  // Aggregate provider+model totals; flatten the per-kind rows so a model
  // that has both embed+reason rows shows as one bar.
  const merged = new Map<string, { name: string; calls: number; cost: number }>();
  for (const row of data) {
    const key = `${row.provider}:${row.model}`;
    const prev = merged.get(key) ?? { name: key, calls: 0, cost: 0 };
    prev.calls += row.calls ?? 0;
    prev.cost += row.cost_microcents_usd ?? 0;
    merged.set(key, prev);
  }
  const chartData = Array.from(merged.values())
    .sort((a, b) => b.calls - a.calls)
    .slice(0, 8)
    .map((row) => ({
      ...row,
      cost_dollars: row.cost / 100_000_000,
    }));

  if (chartData.length === 0) {
    return <p className="py-8 text-center  text-muted-foreground">No usage yet.</p>;
  }

  return (
    <ResponsiveContainer width="100%" height={Math.max(220, chartData.length * 36)}>
      <BarChart
        data={chartData}
        layout="vertical"
        margin={{ top: 8, right: 16, left: 16, bottom: 0 }}
      >
        <CartesianGrid strokeDasharray="3 3" stroke="hsl(222, 14%, 18%)" />
        <XAxis type="number" allowDecimals={false} {...AXIS_STYLE} />
        <YAxis type="category" dataKey="name" width={170} {...AXIS_STYLE} />
        <Tooltip
          contentStyle={TOOLTIP_STYLE}
          formatter={(value: number, name: string) => {
            if (name === "Cost") return [`$${value.toFixed(6)}`, name];
            return [value, name];
          }}
        />
        <Legend wrapperStyle={{ fontSize: 12, paddingTop: 8 }} />
        <Bar dataKey="calls" name="Calls" fill={CHART_COLORS.primary} />
        <Bar dataKey="cost_dollars" name="Cost" fill={CHART_COLORS.accent} />
      </BarChart>
    </ResponsiveContainer>
  );
}
