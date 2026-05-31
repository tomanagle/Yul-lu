import { useEffect, useState } from "react";
import { ChevronDown, ChevronRight, RotateCcw, Sparkles } from "lucide-react";

import {
  useBufferedSessions,
  useDreamProgress,
  useConfig,
  useDream,
  useDreamPrompt,
  useSaveConfig,
  useSaveDreamPrompt,
  useSessionStats,
  useStatus,
} from "@/lib/queries";
import { useProjectScope } from "@/lib/project-scope";
import { shortUUID, relativeTime } from "@/lib/format";
import type { BufferedSession, ConfigView, DreamResult } from "@/lib/schemas";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

export function DreamingPage() {
  const { data: status } = useStatus();
  const { project } = useProjectScope();

  if (!status?.ready) {
    return (
      <p className=" text-muted-foreground">Finish setup on the Memories page before dreaming.</p>
    );
  }

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <DreamProgressCard />
      <NextPassCard />
      <BufferCard project={project} />
      <BufferedSessionsCard project={project} />
      <DreamPromptCard />
      <ScheduleCard />
    </div>
  );
}

// NextPassCard renders the scheduler's countdown to the next natural
// dream firing. Two triggers - interval (lastScheduledAt + interval) and
// idle (lastMessageAt + onIdleSeconds) - whichever fires first becomes
// the headline. When scheduler_enabled is false the card explains that
// dreams only happen manually.
function NextPassCard() {
  const { data } = useDreamProgress();
  // Re-render every second so the relative-time string ticks down. We
  // don't refetch - the times come from the existing /progress poll -
  // just nudge React to reformat against `now`.
  const [, force] = useState(0);
  useEffect(() => {
    const t = setInterval(() => force((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);

  if (!data) return null;

  if (!data.scheduler_enabled) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Next dream pass</CardTitle>
          <CardDescription>
            Scheduler is off - dreams only fire when you click "Dream now" or an MCP client calls{" "}
            <code className="text-[11px]">dream_now</code>. Turn it back on under Settings →
            Dreaming.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Next dream pass</CardTitle>
        <CardDescription>
          {data.next_at ? (
            <>
              Fires{" "}
              <span className="font-medium text-foreground">{relativeFuture(data.next_at)}</span> (
              {data.next_reason === "idle" ? "idle trigger" : "interval"}).
            </>
          ) : (
            "Waiting for activity - no trigger is currently armed."
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-1.5  text-muted-foreground">
        <ScheduleRow
          label={`Every ${formatSeconds(data.interval_seconds)}`}
          detail={
            data.next_interval_at
              ? `next ${relativeFuture(data.next_interval_at)}`
              : "no interval set"
          }
        />
        <ScheduleRow
          label={
            data.on_idle_seconds > 0
              ? `${formatSeconds(data.on_idle_seconds)} after last message`
              : "Idle trigger off"
          }
          detail={
            data.next_idle_at
              ? `next ${relativeFuture(data.next_idle_at)}`
              : data.on_idle_seconds > 0
                ? "no recorded messages yet"
                : ""
          }
        />
        {data.last_scheduled_at && (
          <ScheduleRow label="Last scheduled pass" detail={relativeTime(data.last_scheduled_at)} />
        )}
      </CardContent>
    </Card>
  );
}

function ScheduleRow({ label, detail }: { label: string; detail: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span>{label}</span>
      {detail && <span className="font-mono">{detail}</span>}
    </div>
  );
}

// relativeFuture is "in 2m" / "in 30s" / "any moment" for a future-or-past
// timestamp. The progress endpoint can return a next_at that's already
// elapsed if the scheduler hasn't ticked yet - show "any moment now"
// instead of negative deltas.
function relativeFuture(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "any moment now";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `in ${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `in ${m}m`;
  const h = Math.floor(m / 60);
  return `in ${h}h ${m % 60}m`;
}

function formatSeconds(s: number): string {
  if (s <= 0) return "0s";
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

// DreamProgressCard renders /api/dream/progress: a "Dreaming…" banner with
// the live counters while a pass runs, and a quiet "last pass finished N
// ago" summary when idle. Polls fast while running (1s) and slow when not
// (5s) - see useDreamProgress.
function DreamProgressCard() {
  const { data } = useDreamProgress();
  if (!data) return null;
  // Hide the card entirely until the first pass has ever run - no point
  // showing "idle since never" on a brand-new install.
  if (!data.running && !data.started_at) return null;

  const total = data.total_sessions;
  const done = data.completed_sessions;
  // Progress bar fills as completed/total. When total=0 (pass started but
  // no sessions found) show an empty bar - the pass will finish moments
  // later and the card flips to idle.
  const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;

  return (
    <Card className={cn(data.running && "border-primary/50 bg-primary/5")}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Sparkles
            className={cn(
              "h-4 w-4",
              data.running ? "animate-pulse text-primary" : "text-muted-foreground",
            )}
          />
          {data.running ? "Dreaming…" : "Last dream pass"}
          {data.running && (
            <Badge variant="outline" className="text-[10px]">
              {data.phase ?? "running"}
            </Badge>
          )}
        </CardTitle>
        <CardDescription>
          {data.running ? (
            data.current_session_id ? (
              <>
                Processing session{" "}
                <span className="font-mono">{shortUUID(data.current_session_id)}</span> ({done + 1}/
                {total || "?"})
              </>
            ) : (
              "Enumerating sessions…"
            )
          ) : data.finished_at ? (
            <>Finished {relativeTime(data.finished_at)}</>
          ) : (
            "Idle"
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {data.running && total > 0 && (
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div className="h-full bg-primary transition-all" style={{ width: `${pct}%` }} />
          </div>
        )}
        <div className="flex items-baseline gap-6">
          <Stat label="Sessions" value={done} />
          <Stat label="Messages" value={data.messages_processed} />
          <Stat label="Created" value={data.ops_created} />
          <Stat label="Updated" value={data.ops_updated} />
          <Stat label="Deleted" value={data.ops_deleted} />
        </div>
        {data.last_error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2  text-destructive">
            {data.last_error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function BufferCard({ project }: { project: string }) {
  const stats = useSessionStats(project);
  const { data: status } = useStatus();
  const dream = useDream(project);

  const sessions = stats.data?.sessions ?? 0;
  const messages = stats.data?.messages ?? 0;
  // Sampling-only mode: no direct reasoner configured. The desktop
  // button can't trigger a dream (no MCP client session in this
  // request context) - surface a notice instead of a broken button.
  const samplingOnly = !status?.reasoner;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Dream buffer</CardTitle>
        <CardDescription>
          Conversation turns recorded by your MCP client, waiting to be processed into memories.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-baseline gap-6">
          <Stat label="Sessions" value={sessions} />
          <Stat label="Messages" value={messages} />
        </div>

        {samplingOnly ? (
          <SamplingOnlyNotice messages={messages} />
        ) : (
          <>
            <Button
              onClick={() => dream.mutate()}
              disabled={dream.isPending || messages === 0}
              className="w-full"
            >
              <Sparkles className={dream.isPending ? "animate-pulse" : ""} />
              {dream.isPending ? "Dreaming…" : "Dream now"}
            </Button>

            {messages === 0 && (
              <p className=" text-muted-foreground">
                Nothing to dream about yet. Memories are extracted from messages your MCP client
                pushes via <code>record_messages</code>.
              </p>
            )}

            {dream.error && (
              <p className="rounded-md bg-destructive/10 px-3 py-2  text-destructive">
                {String(dream.error)}
              </p>
            )}

            {dream.data && !dream.error && <DreamResultPanel result={dream.data} />}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// SamplingOnlyNotice replaces the "Dream now" button when the user has
// no direct reasoner configured. Explains why the button is gone and
// what the assistant will do instead.
function SamplingOnlyNotice({ messages }: { messages: number }) {
  return (
    <div className="space-y-2 rounded-md border border-violet/30 bg-violet/5 p-3">
      <p className=" leading-relaxed">
        <span className="font-semibold text-violet-soft">MCP sampling mode.</span> Dreams use your
        AI client's subscription, so the desktop "Dream now" button is disabled - there's no client
        session in this request to sample against.
      </p>
      <p className=" text-muted-foreground">
        {messages > 0 ? (
          <>
            Your assistant will call <code>dream_now</code> automatically as the buffer fills (the
            shipped skill instructs it to fire at ≥ 20 messages or after ~30 minutes). Keep working
            - memories will appear once the assistant triggers a pass.
          </>
        ) : (
          <>
            Once your assistant records some turns via <code>record_messages</code>, it will call{" "}
            <code>dream_now</code> automatically to extract memories. No action needed here.
          </>
        )}
      </p>
      <p className=" text-muted-foreground">
        To enable the button + background dreaming, set a direct reasoner in{" "}
        <strong>Settings → Reasoning</strong> (Anthropic or OpenAI with an API key).
      </p>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className="text-3xl font-semibold tabular-nums">{value}</div>
      <div className=" uppercase tracking-wide text-muted-foreground">{label}</div>
    </div>
  );
}

function DreamResultPanel({ result }: { result: DreamResult }) {
  if (result.skipped) {
    return (
      <p className=" text-muted-foreground">
        Another dream pass is in flight - try again in a moment.
      </p>
    );
  }
  const errors: string[] = result.errors ?? [];
  return (
    <div className="space-y-2 rounded-md border bg-muted/30 p-3">
      <div className="flex flex-wrap items-center gap-2 ">
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
        <ul className="space-y-1  text-destructive">
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
  // Dirty flag stops a background useConfig refetch from clobbering
  // pending user edits. Resets onSubmit so the post-save invalidation
  // can refresh local state to the persisted server snapshot.
  const [dirty, setDirty] = useState(false);
  const save = useSaveConfig();

  useEffect(() => {
    if (!initial) return;
    if (!cfg || !dirty) setCfg(initial);
  }, [initial, cfg, dirty]);

  if (!cfg) return null;

  const update = (patch: Partial<ConfigView>) => {
    setCfg({ ...cfg, ...patch });
    setDirty(true);
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        setDirty(false);
        save.mutate(cfg);
      }}
    >
      <Card>
        <CardHeader>
          <CardTitle>Schedule</CardTitle>
          <CardDescription>
            How the background dreamer runs. Requires a direct reasoner (Anthropic or OpenAI) -
            sampling-via-client can't fire from a timer because there's no live client session.
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
              onChange={(e) => update({ dreaming_min_messages: Number(e.target.value) || 0 })}
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
              onChange={(e) => update({ dreaming_context_memories: Number(e.target.value) || 0 })}
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
              onChange={(e) => update({ dreaming_on_idle_seconds: Number(e.target.value) || 0 })}
            />
          </Field>

          {save.error && (
            <p className="rounded-md bg-destructive/10 px-3 py-2  text-destructive">
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
      {hint && <p className=" text-muted-foreground">{hint}</p>}
    </div>
  );
}

// ---------- Buffered sessions ----------

type BufferedSessionsCardProps = { project: string };

// BufferedSessionsCard lists every session in the dream buffer for the
// current project scope, each collapsible to its actual message
// content. Useful for "what would the next dream pass actually see?"
// and for confidence in the record_messages pipeline (you can verify
// the assistant is actually pushing turns).
function BufferedSessionsCard({ project }: BufferedSessionsCardProps) {
  const { data, isLoading } = useBufferedSessions(project);
  const sessions = data ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Buffered sessions</CardTitle>
        <CardDescription>
          Conversation turns waiting for the next dream pass. Click a session to expand. Each
          session is processed independently; pending messages are deleted after the dreamer applies
          its memory ops.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isLoading && <p className=" text-muted-foreground">Loading…</p>}
        {!isLoading && sessions.length === 0 && (
          <p className=" text-muted-foreground">
            Buffer is empty. Memories are extracted from messages your MCP client pushes via{" "}
            <code>record_messages</code> (or the Claude Code Stop hook).
          </p>
        )}
        {sessions.length > 0 && (
          <ul className="space-y-2">
            {sessions.map((s) => (
              <SessionRow key={s.session_id} session={s} />
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

type SessionRowProps = { session: BufferedSession };

function SessionRow({ session }: SessionRowProps) {
  const [open, setOpen] = useState(false);
  const lastAt = session.messages[session.messages.length - 1]?.at;
  return (
    <li className="rounded-md border border-border/40 bg-card/40">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "flex w-full items-center justify-between gap-3 rounded-md px-3 py-2 text-left",
          "transition-colors hover:bg-primary/5",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        )}
      >
        <div className="flex items-center gap-2 min-w-0">
          {open ? (
            <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="font-mono  text-foreground truncate">
            {shortUUID(session.session_id)}
          </span>
          <Badge variant="secondary" className="shrink-0 text-[10px]">
            {session.message_count} msg{session.message_count === 1 ? "" : "s"}
          </Badge>
        </div>
        {lastAt && (
          <span className="shrink-0 text-[10px] text-muted-foreground">
            last {relativeTime(lastAt)}
          </span>
        )}
      </button>
      {open && (
        <ol className="space-y-2 border-t border-border/40 px-3 py-3">
          {session.messages.map((m, i) => (
            <li key={i} className="flex gap-3 ">
              <span
                className={cn(
                  "shrink-0 rounded px-1.5 py-0.5 font-mono text-[10px] uppercase",
                  m.role === "user"
                    ? "bg-indigo-soft/15 text-indigo-soft"
                    : "bg-violet/15 text-violet-soft",
                )}
              >
                {m.role}
              </span>
              <p className="whitespace-pre-wrap leading-relaxed text-foreground/90">{m.content}</p>
            </li>
          ))}
        </ol>
      )}
    </li>
  );
}

// ---------- Dream prompt editor ----------

// DreamPromptCard lets the user inspect and edit the system prompt the
// reasoner sees on every dream pass. The default ships in the binary;
// saved overrides live at ~/.config/yullu/dream_prompt.txt and take
// effect immediately on the next pass (dream.go reads the file at
// call time). Empty save → reset to default.
function DreamPromptCard() {
  const { data, isLoading } = useDreamPrompt();
  const save = useSaveDreamPrompt();
  const [draft, setDraft] = useState<string | null>(null);

  useEffect(() => {
    if (data && draft === null) setDraft(data.prompt);
  }, [data, draft]);

  if (isLoading || !data || draft === null) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Dream prompt</CardTitle>
        </CardHeader>
        <CardContent>
          <p className=" text-muted-foreground">Loading…</p>
        </CardContent>
      </Card>
    );
  }

  const dirty = draft.trim() !== data.prompt.trim();
  const matchesDefault = draft.trim() === data.default.trim();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Dream prompt
          {data.is_custom && (
            <Badge variant="secondary" className="text-[10px]">
              customised
            </Badge>
          )}
        </CardTitle>
        <CardDescription>
          The system message the reasoner sees on every dream pass. Edit the rules for what counts
          as "worth remembering" - the strict-JSON output contract below is appended automatically
          and isn't editable (removing it would break dream-response parsing).
          {data.path && (
            <span className="ml-1 block font-mono text-[11px] opacity-70">
              stored at {data.path}
            </span>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <Textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={16}
          spellCheck={false}
          className="font-mono  leading-relaxed"
        />

        <div className="space-y-1.5">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            Appended automatically (read-only)
          </p>
          <pre className="overflow-x-auto rounded-md border border-dashed bg-muted/40 px-3 py-2 font-mono text-[11px] leading-relaxed text-muted-foreground">
            {data.output_format}
          </pre>
        </div>

        {save.error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2  text-destructive">
            {String(save.error)}
          </p>
        )}

        <div className="flex items-center justify-between gap-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              if (confirm("Reset the dream prompt to the built-in default?")) {
                save.mutate("");
                setDraft(data.default);
              }
            }}
            disabled={save.isPending || !data.is_custom}
            className="text-muted-foreground hover:text-foreground"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            Reset to default
          </Button>
          <Button
            type="button"
            onClick={() => save.mutate(draft)}
            disabled={save.isPending || !dirty}
          >
            {save.isPending ? "Saving…" : matchesDefault ? "Save (reverts to default)" : "Save"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
