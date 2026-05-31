// Retrievals review surface. Lists recent recalls (newest first) - every
// time a memory was returned by a vector search, with the query that pulled
// it, how close the match was (similarity + rank), and a thumbs up/down to
// record whether that retrieval was actually relevant.
//
// This rates the *retrieval*, not the memory: "was returning THIS memory for
// THIS query a good match?" - a different question from the 1–10 quality
// score on the Review page. A memory can be great but mis-retrieved, or a
// weak memory can be a perfect match. The verdict is keyed to the recall
// event, so the same memory can be judged good for one query and bad for
// another. The signal feeds threshold tuning and an eventual retrieval eval
// set; it stays local and is never synced.

import { useState } from "react";
import { ThumbsDown, ThumbsUp } from "lucide-react";

import { useProjectScope } from "@/lib/project-scope";
import { useRateRetrieval, useRetrievals } from "@/lib/queries";
import { relativeTime } from "@/lib/format";
import type { RetrievalEvent } from "@/lib/schemas";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
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
            Every recent recall - the memory that surfaced, the query that pulled it, and how close
            the match was. Mark each as a good or bad match. This judges the <em>retrieval</em>, not
            the memory's quality (that's the Review page): the same memory can be a great match for
            one query and noise for another. Verdicts stay local and help tune the match-score
            threshold in Settings.
          </CardDescription>
        </CardHeader>
      </Card>

      {!project && (
        <Card>
          <CardContent className="py-8 text-center  text-muted-foreground">
            Pick a project in the sidebar to see its retrievals.
          </CardContent>
        </Card>
      )}

      {project && isLoading && <p className=" text-muted-foreground">Loading…</p>}
      {project && !isLoading && (!data || data.length === 0) && (
        <Card>
          <CardContent className="py-8 text-center  text-muted-foreground">
            No retrievals yet. Memories show up here once they're returned by a search.
          </CardContent>
        </Card>
      )}
      {project &&
        data?.map((ev) => <RetrievalRow key={ev.event_id} event={ev} project={project} />)}
    </div>
  );
}

type RetrievalRowProps = {
  event: RetrievalEvent;
  project: string;
};

function RetrievalRow({ event, project }: RetrievalRowProps) {
  const rate = useRateRetrieval(project);
  const [comment, setComment] = useState(event.comment ?? "");

  const submit = (verdict: 1 | -1) => {
    rate.mutate({ eventID: event.event_id, verdict, comment: comment.trim() });
  };

  const pct = Math.round((event.similarity ?? 0) * 100);
  const deleted = !event.memory_content;

  return (
    <Card>
      <CardHeader>
        <CardTitle className=" font-mono leading-relaxed">
          {deleted ? (
            <span className="italic text-muted-foreground">(memory has since been deleted)</span>
          ) : (
            event.memory_content
          )}
        </CardTitle>
        <CardDescription className="flex flex-wrap items-center gap-2">
          {event.query && (
            <span className="max-w-full truncate">
              query: <span className="text-foreground/80">“{event.query}”</span>
            </span>
          )}
          <Badge variant="secondary" className="text-[10px]">
            {pct}% match
          </Badge>
          {event.rank ? (
            <Badge variant="secondary" className="text-[10px]">
              rank {event.rank}
            </Badge>
          ) : null}
          {event.memory_category ? (
            <Badge variant="secondary" className="text-[10px]">
              {event.memory_category}
            </Badge>
          ) : null}
          <span>recalled {relativeTime(event.at)}</span>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <Textarea
          placeholder="Optional: why was this a good or bad match for that query?"
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          rows={2}
          className=""
        />

        {rate.error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2  text-destructive">
            {String(rate.error)}
          </p>
        )}

        <div className="flex items-center justify-between">
          <span className="text-[11px] text-muted-foreground">
            {event.verdict === 1
              ? "Marked a good match."
              : event.verdict === -1
                ? "Marked a bad match."
                : "Was this a relevant result for the query?"}
          </span>
          <div className="flex items-center gap-2">
            <VerdictButton
              tone="bad"
              active={event.verdict === -1}
              disabled={rate.isPending}
              onClick={() => submit(-1)}
            />
            <VerdictButton
              tone="good"
              active={event.verdict === 1}
              disabled={rate.isPending}
              onClick={() => submit(1)}
            />
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

type VerdictButtonProps = {
  tone: "good" | "bad";
  active: boolean;
  disabled?: boolean;
  onClick: () => void;
};

function VerdictButton({ tone, active, disabled, onClick }: VerdictButtonProps) {
  const Icon = tone === "good" ? ThumbsUp : ThumbsDown;
  return (
    <Button
      type="button"
      size="sm"
      variant="outline"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "gap-1.5",
        tone === "good"
          ? active
            ? "border-primary bg-primary/15 text-foreground"
            : "border-primary/40 text-primary hover:bg-primary/10"
          : active
            ? "border-destructive bg-destructive/15 text-foreground"
            : "border-destructive/40 text-destructive hover:bg-destructive/10",
      )}
    >
      <Icon className="h-3.5 w-3.5" />
      {tone === "good" ? "Good match" : "Bad match"}
    </Button>
  );
}
