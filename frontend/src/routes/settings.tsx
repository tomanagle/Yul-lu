import { useEffect, useState } from "react";

import { useConfig, useSaveConfig } from "@/lib/queries";
import type { ConfigView } from "@/lib/types";

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
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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

export function SettingsPage() {
  const { data: initial } = useConfig();
  const [cfg, setCfg] = useState<ConfigView | null>(null);
  const save = useSaveConfig();

  useEffect(() => {
    if (initial && !cfg) setCfg(initial);
  }, [initial, cfg]);

  if (!cfg) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const update = (patch: Partial<ConfigView>) =>
    setCfg({ ...cfg, ...patch });

  return (
    <form
      className="mx-auto max-w-2xl space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(cfg);
      }}
    >
      <Card>
        <CardHeader>
          <CardTitle>Embedding</CardTitle>
          <CardDescription>
            The provider that turns memory content into vectors.
          </CardDescription>
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
            Powers dreaming. Blank uses MCP sampling - your client (Claude
            Code, Codex) makes the LLM call via its own subscription.
            Configure a direct provider here to also enable background
            dreaming.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Field label="Provider">
            <Select
              value={cfg.reasoning_provider || "sampling"}
              onValueChange={(v) =>
                update({ reasoning_provider: v === "sampling" ? "" : v })
              }
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="sampling">
                  MCP sampling (use client subscription)
                </SelectItem>
                <SelectItem value="anthropic">Anthropic</SelectItem>
                <SelectItem value="openai">OpenAI</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Model">
            <ModelSelect
              models={
                cfg.reasoning_provider
                  ? REASONING_MODELS[cfg.reasoning_provider] ?? []
                  : []
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
          <CardDescription>
            Stored in plain text in your local config.toml.
          </CardDescription>
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
            Dreaming controls live on the Dreaming page - manual trigger,
            schedule, and the toggle.
          </p>
        </CardContent>
      </Card>

      {save.error && (
        <Card className="border-destructive/40 bg-destructive/10">
          <CardContent className="p-3 text-sm text-destructive">
            {String(save.error)}
          </CardContent>
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
