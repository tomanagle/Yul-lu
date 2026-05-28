// Shared force-graph canvas + colour palette used by both the standalone
// /graph route and the /stats dashboard tile. Pulling this out of graph.tsx
// keeps the two surfaces in lockstep - palette changes only need to happen
// here.

import { useEffect, useMemo, useRef, useState } from "react";
import ForceGraph2D, { type ForceGraphMethods } from "react-force-graph-2d";

import type { GraphLink as GraphLinkT, GraphNode as GraphNodeT, MemoryGraph } from "@/lib/schemas";
import { Badge } from "@/components/ui/badge";
import { shortUUID } from "@/lib/format";

// Force-graph paints directly on canvas, so colours are hard-coded here
// instead of reading CSS variables. Keep these in lockstep with the indigo
// + violet palette in index.css.
export const GRAPH_COLORS = {
  tagLink: "rgba(148, 163, 184, 0.28)", // slate-400, faint
  simLink: "rgba(167, 139, 250, 0.55)", // violet-soft
  nodeDefault: "#818CF8", // indigo-300
  nodeHover: "#A78BFA", // violet-soft
  label: "hsl(210, 40%, 96%)", // foreground
  background: "hsl(222, 47%, 9%)",
} as const;

// Stable palette for tag-derived node colours. All sit within the night
// theme - same hash → same colour across reloads.
const TAG_PALETTE = [
  "#818CF8", // indigo-300
  "#A78BFA", // violet-soft
  "#22D3EE", // cyan
  "#F472B6", // soft pink
  "#94A3B8", // slate
  "#60A5FA", // sky blue
  "#C4B5FD", // lavender
];

export function colorForTag(tag: string): string {
  let h = 0;
  for (let i = 0; i < tag.length; i++) {
    h = (h * 31 + tag.charCodeAt(i)) >>> 0;
  }
  return TAG_PALETTE[h % TAG_PALETTE.length];
}

// react-force-graph mutates the node objects it's given (adds x/y/vx/vy).
// We decorate with stable `__color` and `__label` so the canvas doesn't have
// to recompute either on every paint.
export type DecoratedGraphNode = GraphNodeT & {
  x?: number;
  y?: number;
  vx?: number;
  vy?: number;
  __color: string;
  __label: string;
};

export type DecoratedGraphLink = GraphLinkT & {
  source: number | DecoratedGraphNode;
  target: number | DecoratedGraphNode;
};

function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, n) + "…";
}

// useDecoratedGraph turns a raw MemoryGraph from the API into the structures
// the canvas wants, filtering edges by the optional show toggles.
export function useDecoratedGraph(
  graph: MemoryGraph | undefined,
  opts: { showTagEdges?: boolean; showSimEdges?: boolean } = {},
) {
  const { showTagEdges = true, showSimEdges = true } = opts;
  return useMemo(() => {
    const nodes = (graph?.nodes ?? []).map((n): DecoratedGraphNode => {
      const tag = (n.tags ?? [])[0];
      return {
        ...n,
        __color: tag ? colorForTag(tag) : GRAPH_COLORS.nodeDefault,
        __label: truncate(n.content, 60),
      };
    });
    const links: DecoratedGraphLink[] = (graph?.links ?? [])
      .filter(
        (l) => (l.kind === "tag" && showTagEdges) || (l.kind === "similarity" && showSimEdges),
      )
      .map((l) => ({ ...l }));
    return { nodes, links };
  }, [graph, showTagEdges, showSimEdges]);
}

// MemoryGraphCanvas renders the force-graph into whatever box wraps it.
// The container must establish its own size (h-* w-* / flex / aspect-ratio)
// - the canvas reads container dimensions via ResizeObserver and reflows.
export function MemoryGraphCanvas({
  nodes,
  links,
  loading,
  error,
}: {
  nodes: DecoratedGraphNode[];
  links: DecoratedGraphLink[];
  loading: boolean;
  error: string | null;
}) {
  const fgRef = useRef<ForceGraphMethods<DecoratedGraphNode, DecoratedGraphLink> | undefined>(
    undefined,
  );
  const containerRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const [hover, setHover] = useState<DecoratedGraphNode | null>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const update = () => setSize({ w: el.clientWidth, h: el.clientHeight });
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    if (fgRef.current && nodes.length > 0) {
      const t = setTimeout(() => fgRef.current?.zoomToFit(400, 40), 250);
      return () => clearTimeout(t);
    }
  }, [nodes.length, links.length]);

  const canRender = !loading && !error && nodes.length > 0 && size.w > 0 && size.h > 0;

  return (
    <div ref={containerRef} className="relative h-full w-full">
      {error && (
        <div className="absolute left-4 top-4 rounded-md bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      )}
      {!error && loading && (
        <p className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 text-sm text-muted-foreground">
          Loading graph…
        </p>
      )}
      {!error && !loading && nodes.length === 0 && (
        <p className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 text-sm text-muted-foreground">
          No memories to graph yet.
        </p>
      )}

      {canRender && (
        <ForceGraph2D<DecoratedGraphNode, DecoratedGraphLink>
          ref={fgRef}
          width={size.w}
          height={size.h}
          graphData={{ nodes, links }}
          backgroundColor={GRAPH_COLORS.background}
          nodeRelSize={4}
          nodeVal={(n) => 4 + Math.min(20, n.recalls ?? 0)}
          nodeLabel={(n) => n.__label}
          linkColor={(l) => (l.kind === "tag" ? GRAPH_COLORS.tagLink : GRAPH_COLORS.simLink)}
          linkWidth={(l) => (l.kind === "similarity" ? 1.2 : 0.6)}
          linkDirectionalParticles={0}
          cooldownTicks={120}
          onNodeHover={(n) => setHover(n ?? null)}
          nodeCanvasObjectMode={() => "after"}
          nodeCanvasObject={(node, ctx, globalScale) => {
            const r = Math.sqrt(4 + Math.min(20, node.recalls ?? 0)) * 2;
            const isHover = hover && hover.id === node.id;
            ctx.beginPath();
            ctx.arc(node.x ?? 0, node.y ?? 0, r, 0, 2 * Math.PI);
            ctx.fillStyle = isHover ? GRAPH_COLORS.nodeHover : node.__color;
            ctx.fill();
            if (isHover) {
              ctx.lineWidth = 2 / globalScale;
              ctx.strokeStyle = "white";
              ctx.stroke();
            }
            if (globalScale > 1.4) {
              ctx.font = `${10 / globalScale}px -apple-system, sans-serif`;
              ctx.fillStyle = GRAPH_COLORS.label;
              ctx.textAlign = "center";
              ctx.textBaseline = "top";
              ctx.fillText(node.__label, node.x ?? 0, (node.y ?? 0) + r + 2);
            }
          }}
        />
      )}

      {hover && <HoverCard node={hover} />}
    </div>
  );
}

function HoverCard({ node }: { node: DecoratedGraphNode }) {
  return (
    <div className="pointer-events-none absolute left-4 top-4 z-10 max-w-md rounded-md border border-border/40 bg-card/95 p-3 shadow-lg backdrop-blur">
      <p className="text-sm leading-relaxed">{node.content}</p>
      {(node.tags ?? []).length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {node.tags!.map((t) => (
            <Badge key={t} variant="secondary">
              {t}
            </Badge>
          ))}
        </div>
      )}
      <p className="mt-2 font-mono text-[11px] text-muted-foreground">
        {shortUUID(node.uuid)} · {node.recalls ?? 0} recalls
      </p>
    </div>
  );
}
