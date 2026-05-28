import { useState } from "react";
import { RefreshCw } from "lucide-react";

import { useMemoryGraph, useStatus } from "@/lib/queries";
import { useProjectScope } from "@/lib/project-scope";
import { GRAPH_COLORS, MemoryGraphCanvas, useDecoratedGraph } from "@/components/memory-graph";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";

export function GraphPage() {
  const { data: status } = useStatus();
  const { project } = useProjectScope();
  const [showTagEdges, setShowTagEdges] = useState(true);
  const [showSimEdges, setShowSimEdges] = useState(true);

  const graphQuery = useMemoryGraph(project);
  const { nodes, links } = useDecoratedGraph(graphQuery.data, {
    showTagEdges,
    showSimEdges,
  });

  if (status && !status.ready) {
    return (
      <p className="text-sm text-muted-foreground">
        Finish setup on the Memories page before viewing the graph.
      </p>
    );
  }

  return (
    <div className="flex h-full flex-col gap-4">
      <Card className="shrink-0">
        <CardContent className="flex flex-wrap items-center gap-4 p-3">
          <div className="flex flex-1 items-center gap-2">
            <Switch checked={showTagEdges} onCheckedChange={setShowTagEdges} id="tag-edges" />
            <Label htmlFor="tag-edges" className="cursor-pointer text-sm">
              Tag edges
            </Label>
          </div>
          <div className="flex items-center gap-2">
            <Switch checked={showSimEdges} onCheckedChange={setShowSimEdges} id="sim-edges" />
            <Label htmlFor="sim-edges" className="cursor-pointer text-sm">
              Similarity edges
            </Label>
          </div>

          <Button
            variant="outline"
            size="sm"
            onClick={() => graphQuery.refetch()}
            disabled={graphQuery.isFetching}
          >
            <RefreshCw className={graphQuery.isFetching ? "animate-spin" : ""} />
            Refresh
          </Button>
        </CardContent>
      </Card>

      <div className="flex flex-1 gap-4 overflow-hidden">
        <Card className="flex flex-1 overflow-hidden">
          <CardContent className="h-full w-full p-0">
            <MemoryGraphCanvas
              nodes={nodes}
              links={links}
              loading={graphQuery.isLoading}
              error={graphQuery.error ? String(graphQuery.error) : null}
            />
          </CardContent>
        </Card>
      </div>

      <Legend nodeCount={nodes.length} edgeCount={links.length} />
    </div>
  );
}

function Legend({ nodeCount, edgeCount }: { nodeCount: number; edgeCount: number }) {
  return (
    <div className="flex items-center gap-4 text-xs text-muted-foreground">
      <span>
        {nodeCount} {nodeCount === 1 ? "memory" : "memories"} · {edgeCount}{" "}
        {edgeCount === 1 ? "edge" : "edges"}
      </span>
      <span className="flex items-center gap-1.5">
        <span
          className="inline-block h-2 w-6 rounded"
          style={{ background: GRAPH_COLORS.simLink }}
        />
        similarity
      </span>
      <span className="flex items-center gap-1.5">
        <span
          className="inline-block h-2 w-6 rounded"
          style={{ background: GRAPH_COLORS.tagLink }}
        />
        shared tag
      </span>
      <span className="flex items-center gap-1.5">Node size ∝ recall count</span>
    </div>
  );
}
