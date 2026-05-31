import { useEffect, useState } from "react";

// Recharts renders SVG and can't resolve `var(--x)` in presentation
// attributes, so we read the resolved theme tokens off :root at runtime and
// hand Recharts plain hsl() strings. Re-reads on a system light/dark flip
// (prefers-color-scheme change) so charts recolour without a reload.

export type ThemeTokens = {
  chart1: string;
  chart2: string;
  chart3: string;
  chart4: string;
  chart5: string;
  chart6: string;
  grid: string;
  axis: string;
  tooltipBg: string;
  tooltipBorder: string;
  foreground: string;
  mutedForeground: string;
  primary: string;
  accent: string;
};

// Fallback used during SSR / before the first paint. Mirrors the dark
// palette so a flash, if any, matches the most common case.
const FALLBACK: ThemeTokens = {
  chart1: "hsl(217 91% 62%)",
  chart2: "hsl(178 72% 48%)",
  chart3: "hsl(263 84% 70%)",
  chart4: "hsl(38 92% 58%)",
  chart5: "hsl(190 88% 55%)",
  chart6: "hsl(350 85% 68%)",
  grid: "hsl(222 18% 15%)",
  axis: "hsl(215 16% 56%)",
  tooltipBg: "hsl(222 42% 11%)",
  tooltipBorder: "hsl(222 18% 20%)",
  foreground: "hsl(210 40% 96%)",
  mutedForeground: "hsl(215 18% 62%)",
  primary: "hsl(217 91% 60%)",
  accent: "hsl(263 84% 67%)",
};

function read(): ThemeTokens {
  if (typeof window === "undefined") return FALLBACK;
  const s = getComputedStyle(document.documentElement);
  const v = (name: string) => {
    const raw = s.getPropertyValue(name).trim();
    return raw ? `hsl(${raw})` : "";
  };
  return {
    chart1: v("--chart-1"),
    chart2: v("--chart-2"),
    chart3: v("--chart-3"),
    chart4: v("--chart-4"),
    chart5: v("--chart-5"),
    chart6: v("--chart-6"),
    grid: v("--chart-grid"),
    axis: v("--chart-axis"),
    tooltipBg: v("--chart-tooltip-bg"),
    tooltipBorder: v("--chart-tooltip-border"),
    foreground: v("--foreground"),
    mutedForeground: v("--muted-foreground"),
    primary: v("--primary"),
    accent: v("--accent"),
  };
}

export function useThemeTokens(): ThemeTokens {
  const [tokens, setTokens] = useState<ThemeTokens>(read);
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setTokens(read());
    update();
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, []);
  return tokens;
}
