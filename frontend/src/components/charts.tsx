// Recharts wrappers wired to the live theme tokens (useThemeTokens), so every
// axis/grid/tooltip/series colour tracks the OS light/dark setting. Kept in
// one file because the charts share axis + tooltip styling.

import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import type { DailyMemoryEvents, DailyUsage, UsageBucket } from "@/lib/schemas";
import { useThemeTokens, type ThemeTokens } from "@/lib/use-theme-tokens";

function axisStyle(t: ThemeTokens) {
  return { fontSize: 11, fill: t.axis } as const;
}

function tooltipStyle(t: ThemeTokens): React.CSSProperties {
  return {
    backgroundColor: t.tooltipBg,
    border: `1px solid ${t.tooltipBorder}`,
    borderRadius: 12,
    fontSize: 12,
    color: t.foreground,
    boxShadow: "0 10px 30px -12px rgba(0,0,0,0.45)",
  };
}

function shortDay(iso: string): string {
  // "2026-05-27" -> "May 27"
  const d = new Date(iso + "T00:00:00");
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// ---- Hero area chart: stacked daily lifecycle, soft gradient fills --------

export function MemoryEventsChart({ data }: { data: DailyMemoryEvents[] }) {
  const t = useThemeTokens();
  const series = [
    { key: "created", color: t.chart1 },
    { key: "recalled", color: t.chart3 },
    { key: "updated", color: t.chart2 },
    { key: "deleted", color: t.chart6 },
  ];
  return (
    <ResponsiveContainer width="100%" height={260}>
      <AreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <defs>
          {series.map((s) => (
            <linearGradient key={s.key} id={`grad-${s.key}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={s.color} stopOpacity={0.45} />
              <stop offset="100%" stopColor={s.color} stopOpacity={0.02} />
            </linearGradient>
          ))}
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke={t.grid} vertical={false} />
        <XAxis
          dataKey="day"
          tickFormatter={shortDay}
          tickLine={false}
          axisLine={false}
          {...axisStyle(t)}
        />
        <YAxis
          allowDecimals={false}
          tickLine={false}
          axisLine={false}
          width={32}
          {...axisStyle(t)}
        />
        <Tooltip
          contentStyle={tooltipStyle(t)}
          labelFormatter={shortDay}
          cursor={{ stroke: t.grid }}
        />
        <Legend wrapperStyle={{ fontSize: 12, paddingTop: 8 }} iconType="circle" />
        {series.map((s) => (
          <Area
            key={s.key}
            type="monotone"
            dataKey={s.key}
            stackId="1"
            stroke={s.color}
            strokeWidth={2}
            fill={`url(#grad-${s.key})`}
          />
        ))}
      </AreaChart>
    </ResponsiveContainer>
  );
}

// ---- Single-series area: one metric over time, the "Total balance" look ---

export function TrendArea({
  data,
  dataKey,
  height = 220,
}: {
  data: { day: string; [k: string]: number | string }[];
  dataKey: string;
  height?: number;
}) {
  const t = useThemeTokens();
  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <defs>
          <linearGradient id="grad-trend" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={t.chart1} stopOpacity={0.4} />
            <stop offset="100%" stopColor={t.chart1} stopOpacity={0.02} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke={t.grid} vertical={false} />
        <XAxis
          dataKey="day"
          tickFormatter={shortDay}
          tickLine={false}
          axisLine={false}
          {...axisStyle(t)}
        />
        <YAxis
          allowDecimals={false}
          tickLine={false}
          axisLine={false}
          width={32}
          {...axisStyle(t)}
        />
        <Tooltip
          contentStyle={tooltipStyle(t)}
          labelFormatter={shortDay}
          cursor={{ stroke: t.grid }}
        />
        <Area
          type="monotone"
          dataKey={dataKey}
          stroke={t.chart1}
          strokeWidth={2.5}
          fill="url(#grad-trend)"
          dot={false}
          activeDot={{ r: 4, strokeWidth: 0 }}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

// ---- Composition donut: segments + a big centred headline -----------------

export type DonutSegment = { name: string; value: number; color: string };

export function CompositionDonut({
  segments,
  centerValue,
  centerLabel,
  height = 220,
}: {
  segments: DonutSegment[];
  centerValue: string;
  centerLabel?: string;
  height?: number;
}) {
  const t = useThemeTokens();
  const total = segments.reduce((a, s) => a + s.value, 0);
  return (
    <div className="relative" style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <PieChart>
          <Pie
            data={total > 0 ? segments : [{ name: "empty", value: 1, color: t.grid }]}
            dataKey="value"
            nameKey="name"
            cx="50%"
            cy="50%"
            innerRadius="68%"
            outerRadius="100%"
            paddingAngle={total > 0 ? 3 : 0}
            stroke="none"
            cornerRadius={6}
          >
            {(total > 0 ? segments : [{ name: "empty", value: 1, color: t.grid }]).map((s, i) => (
              <Cell key={i} fill={s.color} />
            ))}
          </Pie>
          {total > 0 && <Tooltip contentStyle={tooltipStyle(t)} />}
        </PieChart>
      </ResponsiveContainer>
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
        <span className="yullu-display text-3xl text-foreground">{centerValue}</span>
        {centerLabel && (
          <span className="yullu-label mt-1 text-muted-foreground">{centerLabel}</span>
        )}
      </div>
    </div>
  );
}

// ---- Day bars: rounded vertical bars, the "Active User" look --------------

export function DayBars({
  data,
  dataKey,
  height = 220,
}: {
  data: { day: string; [k: string]: number | string }[];
  dataKey: string;
  height?: number;
}) {
  const t = useThemeTokens();
  const peak = data.reduce((m, d) => Math.max(m, Number(d[dataKey] ?? 0)), 0);
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }} barCategoryGap="28%">
        <CartesianGrid strokeDasharray="3 3" stroke={t.grid} vertical={false} />
        <XAxis
          dataKey="day"
          tickFormatter={shortDay}
          tickLine={false}
          axisLine={false}
          {...axisStyle(t)}
        />
        <YAxis
          allowDecimals={false}
          tickLine={false}
          axisLine={false}
          width={32}
          {...axisStyle(t)}
        />
        <Tooltip
          contentStyle={tooltipStyle(t)}
          labelFormatter={shortDay}
          cursor={{ fill: t.grid, fillOpacity: 0.3 }}
        />
        <Bar dataKey={dataKey} radius={[6, 6, 6, 6]} maxBarSize={26}>
          {data.map((d, i) => (
            <Cell
              key={i}
              // Brighten the peak day; mute the rest — echoes the reference's
              // single highlighted column.
              fill={Number(d[dataKey] ?? 0) === peak && peak > 0 ? t.chart1 : t.chart2}
              fillOpacity={Number(d[dataKey] ?? 0) === peak && peak > 0 ? 1 : 0.55}
            />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

// ---- Usage cost line + by-model bars (carried over, now theme-aware) ------

export function UsageCostChart({ data }: { data: DailyUsage[] }) {
  const t = useThemeTokens();
  const chartData = data.map((d) => ({
    ...d,
    cost_cents: (d.cost_microcents_usd ?? 0) / 1_000_000,
  }));

  return (
    <ResponsiveContainer width="100%" height={240}>
      <LineChart data={chartData} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke={t.grid} vertical={false} />
        <XAxis
          dataKey="day"
          tickFormatter={shortDay}
          tickLine={false}
          axisLine={false}
          {...axisStyle(t)}
        />
        <YAxis
          tickFormatter={(v: number) => `${v.toFixed(1)}¢`}
          tickLine={false}
          axisLine={false}
          {...axisStyle(t)}
        />
        <Tooltip
          contentStyle={tooltipStyle(t)}
          labelFormatter={shortDay}
          cursor={{ stroke: t.grid }}
          formatter={(value: number, name: string) => {
            if (name === "Cost") {
              const dollars = value / 100;
              return [`$${dollars.toFixed(6)}`, name];
            }
            return [value, name];
          }}
        />
        <Legend wrapperStyle={{ fontSize: 12, paddingTop: 8 }} iconType="circle" />
        <Line
          type="monotone"
          dataKey="cost_cents"
          name="Cost"
          stroke={t.chart1}
          strokeWidth={2.5}
          dot={false}
        />
        <Line
          type="monotone"
          dataKey="calls"
          name="Calls"
          stroke={t.chart3}
          strokeWidth={2.5}
          dot={false}
        />
      </LineChart>
    </ResponsiveContainer>
  );
}

export function UsageByModelChart({ data }: { data: UsageBucket[] }) {
  const t = useThemeTokens();
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
    .map((row) => ({ ...row, cost_dollars: row.cost / 100_000_000 }));

  if (chartData.length === 0) {
    return <p className="py-8 text-center text-sm text-muted-foreground">No usage yet.</p>;
  }

  return (
    <ResponsiveContainer width="100%" height={Math.max(220, chartData.length * 38)}>
      <BarChart
        data={chartData}
        layout="vertical"
        margin={{ top: 8, right: 16, left: 16, bottom: 0 }}
      >
        <CartesianGrid strokeDasharray="3 3" stroke={t.grid} horizontal={false} />
        <XAxis
          type="number"
          allowDecimals={false}
          tickLine={false}
          axisLine={false}
          {...axisStyle(t)}
        />
        <YAxis
          type="category"
          dataKey="name"
          width={170}
          tickLine={false}
          axisLine={false}
          {...axisStyle(t)}
        />
        <Tooltip
          contentStyle={tooltipStyle(t)}
          cursor={{ fill: t.grid, fillOpacity: 0.3 }}
          formatter={(value: number, name: string) => {
            if (name === "Cost") return [`$${value.toFixed(6)}`, name];
            return [value, name];
          }}
        />
        <Legend wrapperStyle={{ fontSize: 12, paddingTop: 8 }} iconType="circle" />
        <Bar dataKey="calls" name="Calls" fill={t.chart1} radius={[0, 4, 4, 0]} />
        <Bar dataKey="cost_dollars" name="Cost" fill={t.chart3} radius={[0, 4, 4, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
}
