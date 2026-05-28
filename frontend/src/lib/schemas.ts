// Zod schemas for every API response shape. Parsing through these on each
// fetch catches drift between the Go server and the React client at the
// boundary — a typo in a struct tag or a renamed field surfaces as a thrown
// ZodError instead of a silent `undefined` deep inside a chart.
//
// Type inference: every schema exports its TS type via `z.infer`, so
// downstream code imports the type and never needs to hand-write a matching
// interface. When the schema changes, the type updates automatically.
//
// Convention: ISO datetime strings stay typed as `z.string()` — the
// existing format/relativeTime helpers parse them lazily on render, and
// coercing to Date here would force every consumer to re-stringify or eat
// a deserialisation cost they don't need.

import { z } from "zod";

// ----- Status / Config -----------------------------------------------------

export const StatusSchema = z.object({
  ready: z.boolean(),
  config_path: z.string(),
  db_path: z.string(),
  embedder: z.string().optional(),
  message: z.string().optional(),
  hint: z.string().optional(),
});
export type Status = z.infer<typeof StatusSchema>;

export const ConfigViewSchema = z.object({
  embedding_provider: z.string(),
  embedding_model: z.string(),
  reasoning_provider: z.string(),
  reasoning_model: z.string(),
  voyage_api_key: z.string(),
  openai_api_key: z.string(),
  anthropic_api_key: z.string(),
  sync_enabled: z.boolean(),
  dreaming_enabled: z.boolean(),
  dreaming_interval: z.string(),
  dreaming_min_messages: z.number(),
  dreaming_context_memories: z.number(),
  dreaming_on_idle_seconds: z.number(),
});
export type ConfigView = z.infer<typeof ConfigViewSchema>;

// ----- Sessions / Dream ----------------------------------------------------

export const SessionStatsSchema = z.object({
  project_id: z.string(),
  sessions: z.number(),
  messages: z.number(),
});
export type SessionStats = z.infer<typeof SessionStatsSchema>;

export const DreamSessionSchema = z.object({
  session_id: z.string(),
  messages_processed: z.number(),
  ops_created: z.number(),
  ops_updated: z.number(),
  ops_deleted: z.number(),
  ops_skipped: z.number(),
});
export type DreamSession = z.infer<typeof DreamSessionSchema>;

export const DreamResultSchema = z.object({
  project_id: z.string(),
  sessions_processed: z.number(),
  messages_processed: z.number(),
  ops_created: z.number(),
  ops_updated: z.number(),
  ops_deleted: z.number(),
  ops_skipped: z.number(),
  sessions: z.array(DreamSessionSchema).optional(),
  errors: z.array(z.string()).optional(),
  skipped: z.boolean().optional(),
});
export type DreamResult = z.infer<typeof DreamResultSchema>;

export const DreamStatsSchema = z.object({
  project_id: z.string(),
  passes: z.number(),
  sessions_processed: z.number(),
  messages_processed: z.number(),
  ops_created: z.number(),
  ops_updated: z.number(),
  ops_deleted: z.number(),
  ops_skipped: z.number(),
  errors: z.number(),
  // omitzero on the Go side; the field is simply absent when there's no
  // recent pass, hence .optional() and not .nullable().
  last_pass_at: z.string().optional(),
});
export type DreamStats = z.infer<typeof DreamStatsSchema>;

// ----- Memories ------------------------------------------------------------

export const MemorySchema = z.object({
  id: z.number(),
  uuid: z.string(),
  project_id: z.string(),
  content: z.string(),
  tags: z.array(z.string()).optional(),
  created_at: z.string(),
  updated_at: z.string(),
  score: z.number().optional(),
});
export type Memory = z.infer<typeof MemorySchema>;

export const TopMemorySchema = z.object({
  memory: MemorySchema,
  count: z.number(),
});
export type TopMemory = z.infer<typeof TopMemorySchema>;

export const StatCountsSchema = z.object({
  created_day: z.number(),
  created_week: z.number(),
  created_all: z.number(),
  updated_day: z.number(),
  updated_week: z.number(),
  updated_all: z.number(),
  deleted_day: z.number(),
  deleted_week: z.number(),
  deleted_all: z.number(),
  recalled_day: z.number(),
  recalled_week: z.number(),
  recalled_all: z.number(),
});
export type StatCounts = z.infer<typeof StatCountsSchema>;

export const MemoryStatsSchema = z.object({
  project_id: z.string(),
  total_memories: z.number(),
  counts: StatCountsSchema,
  top_recalled: z.array(TopMemorySchema).optional(),
});
export type MemoryStats = z.infer<typeof MemoryStatsSchema>;

// ----- Time series + usage -------------------------------------------------

export const DailyMemoryEventsSchema = z.object({
  day: z.string(),
  created: z.number(),
  updated: z.number(),
  deleted: z.number(),
  recalled: z.number(),
});
export type DailyMemoryEvents = z.infer<typeof DailyMemoryEventsSchema>;

export const DailyUsageSchema = z.object({
  day: z.string(),
  cost_microcents_usd: z.number(),
  calls: z.number(),
  input_tokens: z.number(),
  output_tokens: z.number(),
});
export type DailyUsage = z.infer<typeof DailyUsageSchema>;

export const UsageBucketSchema = z.object({
  provider: z.string(),
  model: z.string(),
  kind: z.string(),
  calls: z.number(),
  failures: z.number(),
  input_tokens: z.number(),
  output_tokens: z.number(),
  cost_microcents_usd: z.number(),
  avg_latency_ms: z.number(),
});
export type UsageBucket = z.infer<typeof UsageBucketSchema>;

// ----- Graph ---------------------------------------------------------------

export const GraphNodeSchema = z.object({
  id: z.number(),
  uuid: z.string(),
  content: z.string(),
  tags: z.array(z.string()),
  recalls: z.number(),
});
export type GraphNode = z.infer<typeof GraphNodeSchema>;

export const GraphLinkSchema = z.object({
  source: z.number(),
  target: z.number(),
  kind: z.string(),
  weight: z.number(),
  label: z.string().optional(),
});
export type GraphLink = z.infer<typeof GraphLinkSchema>;

export const MemoryGraphSchema = z.object({
  nodes: z.array(GraphNodeSchema),
  links: z.array(GraphLinkSchema),
});
export type MemoryGraph = z.infer<typeof MemoryGraphSchema>;

// ----- Project overrides ---------------------------------------------------

export const ProjectOverridePayloadSchema = z.object({
  reasoning_provider: z.string().optional(),
  reasoning_model: z.string().optional(),
  voyage_api_key: z.string().optional(),
  openai_api_key: z.string().optional(),
  anthropic_api_key: z.string().optional(),

  sync_enabled: z.boolean().optional(),
  sync_dir: z.string().optional(),
  sync_log_embeddings: z.boolean().optional(),
  sync_reuse_embeddings: z.boolean().optional(),
  sync_auto_reconcile_on_startup: z.boolean().optional(),

  dreaming_enabled: z.boolean().optional(),
  dreaming_interval: z.string().optional(),
  dreaming_min_messages: z.number().optional(),
  dreaming_context_memories: z.number().optional(),
  dreaming_on_idle_seconds: z.number().optional(),
});
export type ProjectOverridePayload = z.infer<typeof ProjectOverridePayloadSchema>;

export const EffectiveProjectConfigSchema = z.object({
  embedding_provider: z.string(),
  embedding_model: z.string(),
  reasoning_provider: z.string(),
  reasoning_model: z.string(),
  voyage_api_key: z.string(),
  openai_api_key: z.string(),
  anthropic_api_key: z.string(),
  sync_enabled: z.boolean(),
  sync_dir: z.string(),
  dreaming_enabled: z.boolean(),
  dreaming_interval: z.string(),
  dreaming_min_messages: z.number(),
  dreaming_context_memories: z.number(),
  dreaming_on_idle_seconds: z.number(),
});
export type EffectiveProjectConfig = z.infer<typeof EffectiveProjectConfigSchema>;

export const ProjectOverridesViewSchema = z.object({
  project_id: z.string(),
  repo: ProjectOverridePayloadSchema,
  user: ProjectOverridePayloadSchema,
  effective: EffectiveProjectConfigSchema,
  warnings: z.array(z.string()).optional(),
});
export type ProjectOverridesView = z.infer<typeof ProjectOverridesViewSchema>;

// ----- List envelopes ------------------------------------------------------

// listOf builds the {items: [...]} schema for a list endpoint. Used by
// requestList() in api.ts so list responses are validated end-to-end like
// every other response.
export const listOf = <T extends z.ZodTypeAny>(item: T) => z.object({ items: z.array(item) });

export const ProjectsListSchema = listOf(z.string());
export const MemoriesListSchema = listOf(MemorySchema);
export const DailyMemoryEventsListSchema = listOf(DailyMemoryEventsSchema);
export const DailyUsageListSchema = listOf(DailyUsageSchema);
export const UsageBucketListSchema = listOf(UsageBucketSchema);
