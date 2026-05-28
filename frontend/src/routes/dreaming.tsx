import { useEffect, useState } from "react";
import { Sparkles } from "lucide-react";

import {
  useConfig,
  useDream,
  useSaveConfig,
  useSessionStats,
  useStatus,
} from "@/lib/queries";
import { useProjectScope } from "@/lib/project-scope";
import type { ConfigView, DreamResult } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";

export function DreamingPage() {
  const { data: status } = useStatus();
  const { project } = useProjectScope();

  if (!status?.ready) {
    return (
      <p className="text-sm text-muted-foreground">
        Finish setup on the Memories page before dreaming.
      </p>
    );
  }

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <BufferCard project={project} />
      <ScheduleCard />
    </div>
  );
}

function BufferCard({ project }: { project: string }) {
  const stats = useSessionStats(project);
  const dream = useDream(project);

  const sessions = stats.data?.sessions ?? 0;
  const messages = stats.data?.messages ?? 0;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Dream buffer</CardTitle>
        <CardDescription>
          Conversation turns recorded by your MCP client, waiting to be
          processed into memories.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-baseline gap-6">
          <Stat label="Sessions" value={sessions} />
          <Stat label="Messages" value={messages} />
        </div>

        <Button
          onClick={() => dream.mutate()}
          disabled={dream.isPending || messages === 0}
          className="w-full"
        >
          <Sparkles className={dream.isPending ? "animate-pulse" : ""} />
          {dream.isPending ? "Dreaming…" : "Dream now"}
        </Button>

        {messages === 0 && (
          <p className="text-xs text-muted-foreground">
            Nothing to dream yet. Memories are extracted from messages your
            MCP client pushes via <code>record_messages</code>.
          </p>
        )}

        {dream.error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {String(dream.error)}
          </p>
        )}

        {dream.data && !dream.error && <DreamResultPanel result={dream.data} />}
      </CardContent>
    </Card>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className="text-3xl font-semibold tabular-nums">{value}</div>
      <div className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
    </div>
  );
}

function DreamResultPanel({ result }: { result: DreamResult }) {
  if (result.skipped) {
    return (
      <p className="text-xs text-muted-foreground">
        Another dream pass is in flight - try again in a moment.
      </p>
    );
  }
  const errors: string[] = result.errors ?? [];
  return (
    <div className="space-y-2 rounded-md border bg-muted/30 p-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="font-medium">Last result</span>
        <Badge variant="secondary">{result.sessions_processed ?? 0} sessions</Badge>
        <Badge variant="secondary">+{result.ops_created ?? 0} created</Badge>
        <Badge variant="secondary">~{result.ops_updated ?? 0} updated</Badge>
        <Badge variant="secondary">-{result.ops_deleted ?? 0} deleted</Badge>
        {(result.ops_skipped ?? 0) > 0 && (
          <Badge variant="outline">{result.ops_skipped} skipped</Badge>
        )}
      </div>
      {errors.length > 0 && (
        <ul className="space-y-1 text-xs text-destructive">
          {errors.map((e: string, i: number) => (
            <li key={i}>{e}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ScheduleCard() {
  const { data: initial } = useConfig();
  const [cfg, setCfg] = useState<ConfigView | null>(null);
  const save = useSaveConfig();

  useEffect(() => {
    if (initial && !cfg) setCfg(initial);
  }, [initial, cfg]);

  if (!cfg) return null;

  const update = (patch: Partial<ConfigView>) =>
    setCfg({ ...cfg, ...patch });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(cfg);
      }}
    >
      <Card>
        <CardHeader>
          <CardTitle>Schedule</CardTitle>
          <CardDescription>
            How the background dreamer runs. Requires a direct reasoner
            (Anthropic or OpenAI) - sampling-via-client can't fire from a
            timer because there's no live client session.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center justify-between rounded-md border p-3">
            <Label className="cursor-pointer">Background dreaming</Label>
            <Switch
              checked={cfg.dreaming_enabled}
              onCheckedChange={(v) => update({ dreaming_enabled: v })}
            />
          </div>

          <Field
            label="Interval"
            hint='Go duration string, e.g. "30m", "1h". The first pass fires shortly after the CLI starts.'
          >
            <Input
              value={cfg.dreaming_interval}
              onChange={(e) => update({ dreaming_interval: e.target.value })}
              placeholder="30m"
            />
          </Field>

          <Field
            label="Min messages per session"
            hint="Scheduled passes skip sessions below this floor. dream_now ignores it."
          >
            <Input
              type="number"
              min={0}
              value={cfg.dreaming_min_messages}
              onChange={(e) =>
                update({ dreaming_min_messages: Number(e.target.value) || 0 })
              }
            />
          </Field>

          <Field
            label="Context memories"
            hint="How many existing memories the reasoner sees per pass. More = better update/delete decisions, more tokens."
          >
            <Input
              type="number"
              min={0}
              value={cfg.dreaming_context_memories}
              onChange={(e) =>
                update({ dreaming_context_memories: Number(e.target.value) || 0 })
              }
            />
          </Field>

          <Field
            label="Idle trigger (seconds)"
            hint="If > 0, also dream when record_messages has been quiet for this long AND there are unprocessed messages. 0 disables."
          >
            <Input
              type="number"
              min={0}
              value={cfg.dreaming_on_idle_seconds}
              onChange={(e) =>
                update({ dreaming_on_idle_seconds: Number(e.target.value) || 0 })
              }
            />
          </Field>

          {save.error && (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {String(save.error)}
            </p>
          )}

          <div className="flex justify-end">
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </form>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}
