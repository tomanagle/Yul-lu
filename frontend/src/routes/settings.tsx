import { useCallback, useEffect, useState } from "react";

import {
  useConfig,
  useProjectOverrides,
  useSaveConfig,
  useSaveProjectOverrides,
} from "@/lib/queries";
import { useProjectScope } from "@/lib/project-scope";
import type { ConfigView, ProjectOverridePayload } from "@/lib/schemas";
import { cn } from "@/lib/utils";

// Model catalogs: keep in sync with internal/ai/pricing.go and registry.go.
// Empty value means "use the provider's default" - that's what the Go side
// expects when no model is set.
const EMBEDDING_MODELS: Record<string, { value: string; label: string }[]> = {
  voyage: [
    { value: "", label: "voyage-code-3 (default)" },
    { value: "voyage-code-3", label: "voyage-code-3" },
    { value: "voyage-3-large", label: "voyage-3-large" },
    { value: "voyage-3", label: "voyage-3" },
    { value: "voyage-3-lite", label: "voyage-3-lite" },
  ],
  openai: [
    { value: "", label: "text-embedding-3-small (default)" },
    { value: "text-embedding-3-small", label: "text-embedding-3-small" },
    { value: "text-embedding-3-large", label: "text-embedding-3-large" },
    { value: "text-embedding-ada-002", label: "text-embedding-ada-002" },
  ],
};

const REASONING_MODELS: Record<string, { value: string; label: string }[]> = {
  anthropic: [
    { value: "", label: "claude-haiku-4-5 (default)" },
    { value: "claude-haiku-4-5", label: "claude-haiku-4-5" },
    { value: "claude-sonnet-4-6", label: "claude-sonnet-4-6" },
    { value: "claude-opus-4-7", label: "claude-opus-4-7" },
  ],
  openai: [
    { value: "", label: "gpt-4o-mini (default)" },
    { value: "gpt-4o-mini", label: "gpt-4o-mini" },
    { value: "gpt-4o", label: "gpt-4o" },
  ],
};

// Radix Select can't have value="" on an item, so we map the blank "default"
// option to this sentinel at the UI layer.
const MODEL_DEFAULT = "__default__";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";

// SettingsPage is a tabbed shell. The Global tab edits the defaults that
// apply to every project; the Project tab edits per-project overrides for
// the currently-scoped project. Tabs persist their selection to localStorage
// so a reload doesn't bounce the user back to Global.
const SETTINGS_TAB_KEY = "yullu.settings.tab";

export function SettingsPage() {
  const { project } = useProjectScope();
  const [tab, setTab] = useState<"global" | "project">(() => {
    if (typeof window === "undefined") return "global";
    return (window.localStorage.getItem(SETTINGS_TAB_KEY) as "global" | "project") ?? "global";
  });

  useEffect(() => {
    window.localStorage.setItem(SETTINGS_TAB_KEY, tab);
  }, [tab]);

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <div className="inline-flex items-center gap-0.5 rounded-md border border-border/40 bg-card/40 p-0.5">
        <TabButton active={tab === "global"} onClick={() => setTab("global")}>
          Global
        </TabButton>
        <TabButton
          active={tab === "project"}
          onClick={() => setTab("project")}
          disabled={!project}
          title={!project ? "Pick a project in the sidebar first" : undefined}
        >
          Project{project ? ":" : ""}
          {project && (
            <span className="ml-1 max-w-[200px] truncate font-mono text-[11px] opacity-70">
              {project}
            </span>
          )}
        </TabButton>
      </div>

      {tab === "global" && <GlobalSettings />}
      {tab === "project" && project && <ProjectSettings projectID={project} />}
      {tab === "project" && !project && (
        <p className="text-sm text-muted-foreground">
          Pick a project in the sidebar to edit its overrides.
        </p>
      )}
    </div>
  );
}

type TabButtonProps = {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
  disabled?: boolean;
  title?: string;
};

function TabButton({ active, onClick, children, disabled, title }: TabButtonProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={cn(
        "flex items-center rounded px-3 py-1 text-xs font-medium transition-colors duration-150",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        active
          ? "bg-primary/15 text-foreground shadow-[inset_0_0_0_1px_hsl(244_76%_51%/0.35)]"
          : "text-muted-foreground hover:text-foreground",
        disabled && "cursor-not-allowed opacity-40 hover:text-muted-foreground",
      )}
    >
      {children}
    </button>
  );
}

function GlobalSettings() {
  const { data: initial } = useConfig();
  const [cfg, setCfg] = useState<ConfigView | null>(null);
  // Tracks whether the user has touched the form since the last server
  // sync. Prevents `useEffect` below from clobbering an in-progress edit
  // with a freshly-refetched server snapshot — but DOES adopt the new
  // snapshot once the user is clean (e.g. after a successful save the
  // mutation invalidates, the query refetches, and we should display
  // the persisted values verbatim).
  const [dirty, setDirty] = useState(false);
  const save = useSaveConfig({
    onSuccess: () => setDirty(false),
  });

  useEffect(() => {
    if (!initial) return;
    // Initial hydration OR server-driven refresh while the form is
    // clean. The `!dirty` guard avoids clobbering pending user edits;
    // without it, post-save invalidation would still overwrite from
    // the server (which is fine, but a stale tab triggering invalidation
    // during typing would wipe in-flight changes).
    if (!cfg || !dirty) setCfg(initial);
  }, [initial, cfg, dirty]);

  if (!cfg) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const update = (patch: Partial<ConfigView>) => {
    setCfg({ ...cfg, ...patch });
    setDirty(true);
  };

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(cfg);
      }}
    >
      <Card>
        <CardHeader>
          <CardTitle>Embedding</CardTitle>
          <CardDescription>The provider that turns memory content into vectors.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Field label="Provider">
            <Select
              value={cfg.embedding_provider || "voyage"}
              onValueChange={(v) => update({ embedding_provider: v })}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="voyage">Voyage (recommended)</SelectItem>
                <SelectItem value="openai">OpenAI</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Model">
            <ModelSelect
              models={EMBEDDING_MODELS[cfg.embedding_provider || "voyage"] ?? []}
              value={cfg.embedding_model}
              onChange={(v) => update({ embedding_model: v })}
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Reasoning</CardTitle>
          <CardDescription>
            Powers dreaming. Two modes:
            <br />
            <br />
            <strong>MCP sampling</strong> (blank, default) — your AI client (Claude Code, Codex,
            Cursor) makes the LLM call against its own subscription. No API key needed, but{" "}
            <em>dreaming only runs when the assistant calls it</em>: the background scheduler and
            the "Dream now" button can't sample because they have no client session. The shipped
            skill prompts the assistant to call <code>dream_now</code> as the buffer fills, so this
            mode works fine for day-to-day use.
            <br />
            <br />
            <strong>Direct provider</strong> — set Anthropic or OpenAI with an API key. Enables
            background dreaming on a timer AND the "Dream now" button. yullu bills against your API
            key, not your subscription.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Field label="Provider">
            <Select
              value={cfg.reasoning_provider || "sampling"}
              onValueChange={(v) => update({ reasoning_provider: v === "sampling" ? "" : v })}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="sampling">MCP sampling (use client subscription)</SelectItem>
                <SelectItem value="anthropic">Anthropic</SelectItem>
                <SelectItem value="openai">OpenAI</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Model">
            <ModelSelect
              models={
                cfg.reasoning_provider ? (REASONING_MODELS[cfg.reasoning_provider] ?? []) : []
              }
              value={cfg.reasoning_model}
              onChange={(v) => update({ reasoning_model: v })}
              disabled={!cfg.reasoning_provider}
              disabledHint="Pick a provider above (sampling has no model selector - the client chooses)."
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>API keys</CardTitle>
          <CardDescription>Stored in plain text in your local config.toml.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Field label="Voyage">
            <Input
              type="password"
              value={cfg.voyage_api_key}
              onChange={(e) => update({ voyage_api_key: e.target.value })}
              placeholder="pa-..."
              autoComplete="off"
            />
          </Field>
          <Field label="OpenAI">
            <Input
              type="password"
              value={cfg.openai_api_key}
              onChange={(e) => update({ openai_api_key: e.target.value })}
              placeholder="sk-..."
              autoComplete="off"
            />
          </Field>
          <Field label="Anthropic">
            <Input
              type="password"
              value={cfg.anthropic_api_key}
              onChange={(e) => update({ anthropic_api_key: e.target.value })}
              placeholder="sk-ant-..."
              autoComplete="off"
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Features</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <ToggleRow
            label="Team sync via .yullu/"
            checked={cfg.sync_enabled}
            onChange={(v) => update({ sync_enabled: v })}
          />
          <p className="text-xs text-muted-foreground">
            Dreaming controls live on the Dreaming page - manual trigger, schedule, and the toggle.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Retrieval</CardTitle>
          <CardDescription>
            Filter weak matches out of memory search. The minimum match score is a cosine-similarity
            floor — a memory must score at least this high against the query to be returned. 0%
            disables the floor (always return the top matches). Higher is stricter: an unrelated
            prompt then pulls nothing instead of padding the results with noise.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Field
            label={`Minimum match score: ${Math.round((cfg.retrieval_min_similarity || 0) * 100)}%`}
            hint="0% = off. Try 50–65% to start, then raise it if irrelevant memories keep surfacing."
          >
            <input
              type="range"
              min={0}
              max={100}
              step={1}
              value={Math.round((cfg.retrieval_min_similarity || 0) * 100)}
              onChange={(e) => update({ retrieval_min_similarity: Number(e.target.value) / 100 })}
              className="w-full accent-primary"
            />
          </Field>
        </CardContent>
      </Card>

      {save.error && (
        <Card className="border-destructive/40 bg-destructive/10">
          <CardContent className="p-3 text-sm text-destructive">{String(save.error)}</CardContent>
        </Card>
      )}

      <div className="flex justify-end gap-2">
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
      </div>
    </form>
  );
}

function ModelSelect({
  models,
  value,
  onChange,
  disabled,
  disabledHint,
}: {
  models: { value: string; label: string }[];
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  disabledHint?: string;
}) {
  if (disabled || models.length === 0) {
    return (
      <p className="text-xs italic text-muted-foreground">
        {disabledHint ?? "No models available for this provider."}
      </p>
    );
  }
  return (
    <Select
      value={value || MODEL_DEFAULT}
      onValueChange={(v) => onChange(v === MODEL_DEFAULT ? "" : v)}
    >
      <SelectTrigger>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {models.map((m) => (
          <SelectItem key={m.value || MODEL_DEFAULT} value={m.value || MODEL_DEFAULT}>
            {m.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
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

function ToggleRow({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-md border p-3">
      <Label className="cursor-pointer">{label}</Label>
      <Switch checked={checked} onCheckedChange={onChange} />
    </div>
  );
}

// ----- Project-scoped overrides --------------------------------------------

type ProjectSettingsProps = {
  projectID: string;
};

// ProjectSettings edits the two override layers for one project. The layer
// each field belongs to is hard-coded (private vs team-shared) so the user
// can't accidentally commit secrets to the repo. Fields with no override
// fall through to the effective value, shown as a placeholder hint.
function ProjectSettings({ projectID }: ProjectSettingsProps) {
  const { data: overrides, isLoading } = useProjectOverrides(projectID);
  const save = useSaveProjectOverrides(projectID);

  // Local form state mirrors the two layers separately so we can POST them
  // back unchanged for fields the user didn't touch.
  const [repo, setRepo] = useState<ProjectOverridePayload | null>(null);
  const [user, setUser] = useState<ProjectOverridePayload | null>(null);
  // Dirty flag prevents server-driven refetches from clobbering pending
  // user edits. After a successful save the dirty flag resets and the
  // form re-syncs to the persisted server snapshot.
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (!overrides) return;
    // First hydration OR a clean refresh after save. Don't overwrite
    // mid-edit state.
    if ((repo === null && user === null) || !dirty) {
      setRepo({ ...overrides.repo });
      setUser({ ...overrides.user });
    }
  }, [overrides, repo, user, dirty]);

  const effective = overrides?.effective;

  // useCallback is the right tool here — useMemo would also work but
  // obscures intent. Empty deps because setRepo/setUser identity is
  // stable. The generic-typed inner arrow needs the trailing comma in
  // TSX (`<K,>`) to disambiguate from JSX.
  const repoSet = useCallback(
    <K extends keyof ProjectOverridePayload>(k: K, v: ProjectOverridePayload[K] | undefined) => {
      setRepo((prev) => (prev === null ? prev : { ...prev, [k]: v }));
      setDirty(true);
    },
    [],
  );
  const userSet = useCallback(
    <K extends keyof ProjectOverridePayload>(k: K, v: ProjectOverridePayload[K] | undefined) => {
      setUser((prev) => (prev === null ? prev : { ...prev, [k]: v }));
      setDirty(true);
    },
    [],
  );

  if (isLoading || !overrides || !repo || !user || !effective) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        // Mark clean immediately so the next useConfig refetch (triggered
        // by the mutation's invalidation) is allowed to overwrite local
        // state with the persisted server snapshot. Doing it onSubmit
        // rather than onSuccess avoids briefly showing the pre-save
        // value during the in-flight window.
        setDirty(false);
        save.mutate({ repo, user });
      }}
    >
      <Card>
        <CardHeader>
          <CardTitle>Team-shared (committed)</CardTitle>
          <CardDescription>
            Stored in{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
              .yullu/config.toml
            </code>{" "}
            inside the repo. Anyone who clones the project picks these up automatically. API keys
            can't go here — use the private section below for those.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <OverrideField
            label="Sync directory"
            inherited={effective.sync_dir}
            value={repo.sync_dir}
            onChange={(v) => repoSet("sync_dir", v)}
            placeholder=".yullu"
            hint='Where committed memory events live ("." prefix recommended).'
          />
          <OverrideToggle
            label="Dreaming enabled"
            inherited={effective.dreaming_enabled}
            value={repo.dreaming_enabled}
            onChange={(v) => repoSet("dreaming_enabled", v)}
          />
          <OverrideField
            label="Dreaming interval"
            inherited={effective.dreaming_interval}
            value={repo.dreaming_interval}
            onChange={(v) => repoSet("dreaming_interval", v)}
            placeholder="30m"
            hint='Go duration string ("15m", "1h").'
          />
          <OverrideNumber
            label="Min messages per session"
            inherited={effective.dreaming_min_messages}
            value={repo.dreaming_min_messages}
            onChange={(v) => repoSet("dreaming_min_messages", v)}
          />
          <OverrideNumber
            label="Context memories per pass"
            inherited={effective.dreaming_context_memories}
            value={repo.dreaming_context_memories}
            onChange={(v) => repoSet("dreaming_context_memories", v)}
          />
          <OverridePercent
            label="Minimum match score"
            inherited={effective.retrieval_min_similarity}
            value={repo.retrieval_min_similarity}
            onChange={(v) => repoSet("retrieval_min_similarity", v)}
            hint="Cosine-similarity floor for memory search on this project. 0% = off."
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Private (this machine)</CardTitle>
          <CardDescription>
            Stored in{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
              ~/.config/yullu/projects/…
            </code>
            . Never committed. Use this for project-specific API keys (e.g. a work key for one repo,
            personal for another).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <OverrideField
            label="Voyage API key"
            inherited={mask(effective.voyage_api_key)}
            value={user.voyage_api_key}
            onChange={(v) => userSet("voyage_api_key", v)}
            placeholder="pa-..."
            password
          />
          <OverrideField
            label="OpenAI API key"
            inherited={mask(effective.openai_api_key)}
            value={user.openai_api_key}
            onChange={(v) => userSet("openai_api_key", v)}
            placeholder="sk-..."
            password
          />
          <OverrideField
            label="Anthropic API key"
            inherited={mask(effective.anthropic_api_key)}
            value={user.anthropic_api_key}
            onChange={(v) => userSet("anthropic_api_key", v)}
            placeholder="sk-ant-..."
            password
          />
        </CardContent>
      </Card>

      {(overrides.warnings?.length ?? 0) > 0 && (
        <Card className="border-amber-500/40 bg-amber-500/10">
          <CardContent className="p-3 text-xs text-amber-300">
            {overrides.warnings!.map((w, i) => (
              <div key={i}>{w}</div>
            ))}
          </CardContent>
        </Card>
      )}

      {save.error && (
        <Card className="border-destructive/40 bg-destructive/10">
          <CardContent className="p-3 text-sm text-destructive">{String(save.error)}</CardContent>
        </Card>
      )}

      <div className="flex justify-end">
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save overrides"}
        </Button>
      </div>
    </form>
  );
}

// OverrideField is the shared text-input row. The toggle determines whether
// the field overrides the inherited value; when off, the input is disabled
// and the inherited value shows as the placeholder.
type OverrideFieldProps = {
  label: string;
  inherited: string;
  value: string | undefined;
  onChange: (v: string | undefined) => void;
  placeholder?: string;
  hint?: string;
  password?: boolean;
};

function OverrideField({
  label,
  inherited,
  value,
  onChange,
  placeholder,
  hint,
  password,
}: OverrideFieldProps) {
  const overridden = value !== undefined;
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label className="text-sm">{label}</Label>
        <div className="flex items-center gap-2">
          <Label className="cursor-pointer text-[11px] text-muted-foreground">Override</Label>
          <Switch
            checked={overridden}
            // Seed the override from the currently-inherited value when
            // toggling on so the user starts from a sensible state
            // instead of an empty string. Matches OverrideNumber's
            // behaviour and prevents accidental wipe of inherited keys.
            onCheckedChange={(on) => onChange(on ? (value ?? inherited) : undefined)}
          />
        </div>
      </div>
      <Input
        type={password ? "password" : "text"}
        value={overridden ? (value ?? inherited) : ""}
        onChange={(e) => onChange(e.target.value)}
        placeholder={overridden ? placeholder : `Inherits: ${inherited || "(empty)"}`}
        disabled={!overridden}
        autoComplete="off"
      />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

// OverrideToggle is a boolean override — switch + inherited badge.
function OverrideToggle({
  label,
  inherited,
  value,
  onChange,
}: {
  label: string;
  inherited: boolean;
  value: boolean | undefined;
  onChange: (v: boolean | undefined) => void;
}) {
  const overridden = value !== undefined;
  return (
    <div className="flex items-center justify-between rounded-md border p-3">
      <div>
        <Label className="cursor-pointer">{label}</Label>
        {!overridden && (
          <p className="text-[11px] text-muted-foreground">
            Inherits: {inherited ? "enabled" : "disabled"}
          </p>
        )}
      </div>
      <div className="flex items-center gap-3">
        {overridden && <Switch checked={!!value} onCheckedChange={(v) => onChange(v)} />}
        <Label className="cursor-pointer text-[11px] text-muted-foreground">Override</Label>
        <Switch
          checked={overridden}
          onCheckedChange={(on) => onChange(on ? !!value || inherited : undefined)}
        />
      </div>
    </div>
  );
}

// OverrideNumber is a numeric override field — same UX as OverrideField but
// stores a number (or undefined for "inherit"). Sentinel comparison handles
// the "user typed nothing" case without flipping to inherit.
function OverrideNumber({
  label,
  inherited,
  value,
  onChange,
}: {
  label: string;
  inherited: number;
  value: number | undefined;
  onChange: (v: number | undefined) => void;
}) {
  const overridden = value !== undefined;
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label className="text-sm">{label}</Label>
        <div className="flex items-center gap-2">
          <Label className="cursor-pointer text-[11px] text-muted-foreground">Override</Label>
          <Switch
            checked={overridden}
            onCheckedChange={(on) => onChange(on ? (value ?? inherited) : undefined)}
          />
        </div>
      </div>
      <Input
        type="number"
        min={0}
        value={overridden ? String(value ?? "") : ""}
        onChange={(e) => onChange(Number(e.target.value) || 0)}
        placeholder={overridden ? undefined : `Inherits: ${inherited}`}
        disabled={!overridden}
      />
    </div>
  );
}

// OverridePercent is a 0–1 similarity override surfaced as a 0–100% slider.
// Mirrors OverrideNumber's inherit/override UX but converts to percent for
// display so the user reasons in the same units as the global Settings tab.
function OverridePercent({
  label,
  inherited,
  value,
  onChange,
  hint,
}: {
  label: string;
  inherited: number; // 0–1
  value: number | undefined; // 0–1, or undefined to inherit
  onChange: (v: number | undefined) => void;
  hint?: string;
}) {
  const overridden = value !== undefined;
  const pct = (n: number) => Math.round(n * 100);
  const shown = overridden ? (value ?? 0) : inherited;
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label className="text-sm">{label}</Label>
        <div className="flex items-center gap-2">
          <Label className="cursor-pointer text-[11px] text-muted-foreground">Override</Label>
          <Switch
            checked={overridden}
            onCheckedChange={(on) => onChange(on ? (value ?? inherited) : undefined)}
          />
        </div>
      </div>
      <div className="flex items-center gap-3">
        <input
          type="range"
          min={0}
          max={100}
          step={1}
          value={pct(shown)}
          onChange={(e) => onChange(Number(e.target.value) / 100)}
          disabled={!overridden}
          className="w-full accent-primary disabled:opacity-40"
        />
        <span className="w-10 shrink-0 text-right font-mono text-xs text-muted-foreground">
          {pct(shown)}%
        </span>
      </div>
      {!overridden && (
        <p className="text-[11px] text-muted-foreground">Inherits: {pct(inherited)}%</p>
      )}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

// mask redacts API keys for display. Returns a string of asterisks the same
// length-class as the original ("(set)" vs "(empty)") so the user can tell
// at a glance whether there's an inherited value without seeing the secret.
function mask(s: string): string {
  if (!s) return "(empty)";
  return "•".repeat(8) + " (set)";
}
