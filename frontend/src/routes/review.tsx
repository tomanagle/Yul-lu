// Dedicated Review queue. Lists memories the user hasn't rated yet
// (newest first). Each card gets a 1–10 score row + comment textarea.
//
// Scoring rules — codified server-side in store.RateMemory, mirrored here
// in the UI affordance:
//   - 1–5  → reject. Memory is removed from `memories` and archived
//            into rejected_memories. The next dream pass sees it as an
//            anti-example. The comment is the "why" signal.
//   - 6–10 → keep + annotate. Memory stays; rating + comment are stored
//            on the row.
//
// We render the threshold visually: 1–5 buttons in destructive red,
// 6–10 in primary violet, so the user understands what their click does
// before they make it. The Save button is disabled until a rating is
// chosen — the comment is optional for 6–10 but strongly encouraged for
// 1–5 (we don't enforce; let the user be terse if they want).

import { useState } from "react";
import { ThumbsDown, ThumbsUp } from "lucide-react";

import { useProjectScope } from "@/lib/project-scope";
import { useRateMemory, useUnratedMemories } from "@/lib/queries";
import { relativeTime } from "@/lib/format";
import type { Memory } from "@/lib/schemas";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

export function ReviewPage() {
  const { project } = useProjectScope();
  const { data, isLoading } = useUnratedMemories(project);

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Review queue</CardTitle>
          <CardDescription>
            Rate memories on a 1–10 scale. 1–5 archives them as anti-examples
            for the next dream pass (the comment is the "why"). 6–10 keeps
            them with the rating attached. Rated memories drop out of this
            list; only un-rated ones surface here.
          </CardDescription>
        </CardHeader>
      </Card>

      {isLoading && (
        <p className="text-sm text-muted-foreground">Loading…</p>
      )}
      {!isLoading && (!data || data.length === 0) && (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            Nothing to review — every memory in this project has been rated.
          </CardContent>
        </Card>
      )}
      {data?.map((m) => (
        <ReviewRow key={m.id} memory={m} project={project} />
      ))}
    </div>
  );
}

type ReviewRowProps = {
  memory: Memory;
  project: string;
};

function ReviewRow({ memory, project }: ReviewRowProps) {
  const rate = useRateMemory(project);
  const [rating, setRating] = useState<number | null>(null);
  const [comment, setComment] = useState("");

  const submit = () => {
    if (rating === null) return;
    rate.mutate({ id: memory.id, rating, comment: comment.trim() });
  };

  const willReject = rating !== null && rating <= 5;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-mono leading-relaxed">
          {memory.content}
        </CardTitle>
        <CardDescription className="flex flex-wrap items-center gap-2">
          <span>created {relativeTime(memory.created_at)}</span>
          {memory.tags?.length ? (
            <span className="flex flex-wrap gap-1">
              {memory.tags.map((t) => (
                <Badge key={t} variant="secondary" className="text-[10px]">
                  {t}
                </Badge>
              ))}
            </span>
          ) : null}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-1.5">
          <ThumbsDown className="h-4 w-4 text-destructive/70" />
          {[1, 2, 3, 4, 5].map((n) => (
            <RatingButton
              key={n}
              value={n}
              selected={rating === n}
              tone="reject"
              onClick={() => setRating(n)}
            />
          ))}
          <span className="mx-2 h-5 w-px bg-border" />
          {[6, 7, 8, 9, 10].map((n) => (
            <RatingButton
              key={n}
              value={n}
              selected={rating === n}
              tone="keep"
              onClick={() => setRating(n)}
            />
          ))}
          <ThumbsUp className="h-4 w-4 text-primary/70" />
        </div>

        <Textarea
          placeholder={
            willReject
              ? "Why is this a bad memory? The next dream pass will see this as guidance."
              : "Optional note about why this rating."
          }
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          rows={2}
          className="text-xs"
        />

        {rate.error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {String(rate.error)}
          </p>
        )}

        <div className="flex items-center justify-between">
          <span className="text-[11px] text-muted-foreground">
            {rating === null
              ? "Pick a score 1–10."
              : willReject
                ? `Score ${rating}/10 — this will archive the memory as an anti-example.`
                : `Score ${rating}/10 — keep with rating attached.`}
          </span>
          <Button
            type="button"
            onClick={submit}
            disabled={rating === null || rate.isPending}
            variant={willReject ? "destructive" : "default"}
          >
            {rate.isPending ? "Saving…" : willReject ? "Reject + archive" : "Save rating"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

type RatingButtonProps = {
  value: number;
  selected: boolean;
  tone: "reject" | "keep";
  onClick: () => void;
};

function RatingButton({ value, selected, tone, onClick }: RatingButtonProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "h-7 w-7 rounded-md border text-xs font-medium transition-colors",
        tone === "reject"
          ? selected
            ? "border-destructive bg-destructive text-destructive-foreground"
            : "border-destructive/40 text-destructive hover:bg-destructive/10"
          : selected
            ? "border-primary bg-primary text-primary-foreground"
            : "border-primary/40 text-primary hover:bg-primary/10",
      )}
    >
      {value}
    </button>
  );
}
