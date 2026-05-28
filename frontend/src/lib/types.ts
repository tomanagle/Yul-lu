// TypeScript mirror of the Go structs returned by /api/*. Kept hand-maintained
// so the frontend has no build-time dependency on a code generator - when a
// shape changes in Go, update this file.

export interface Status {
  ready: boolean;
  config_path: string;
  db_path: string;
  embedder?: string;
  message?: string;
  hint?: string;
}

export interface ConfigView {
  embedding_provider: string;
  embedding_model: string;
  reasoning_provider: string;
  reasoning_model: string;
  voyage_api_key: string;
  openai_api_key: string;
  anthropic_api_key: string;
  sync_enabled: boolean;
  dreaming_enabled: boolean;
  dreaming_interval: string;
  dreaming_min_messages: number;
  dreaming_context_memories: number;
  dreaming_on_idle_seconds: number;
}

export interface SessionStats {
  project_id: string;
  sessions: number;
  messages: number;
}

export interface Memory {
  id: number;
  uuid: string;
  project_id: string;
  content: string;
  tags?: string[];
  created_at: string;
  updated_at: string;
  score?: number;
}

export interface TopMemory {
  memory: Memory;
  count: number;
}

export interface StatCounts {
  created_day: number;
  created_week: number;
  created_all: number;
  updated_day: number;
  updated_week: number;
  updated_all: number;
  deleted_day: number;
  deleted_week: number;
  deleted_all: number;
  recalled_day: number;
  recalled_week: number;
  recalled_all: number;
}

export interface MemoryStats {
  project_id: string;
  total_memories: number;
  counts: StatCounts;
  top_recalled?: TopMemory[];
}

export interface DailyMemoryEvents {
  day: string;
  created: number;
  updated: number;
  deleted: number;
  recalled: number;
}

export interface DailyUsage {
  day: string;
  cost_microcents_usd: number;
  calls: number;
  input_tokens: number;
  output_tokens: number;
}

export interface UsageBucket {
  provider: string;
  model: string;
  kind: string;
  calls: number;
  failures: number;
  input_tokens: number;
  output_tokens: number;
  cost_microcents_usd: number;
  avg_latency_ms: number;
}

export interface GraphNode {
  id: number;
  uuid: string;
  content: string;
  tags: string[];
  recalls: number;
}

export interface GraphLink {
  source: number;
  target: number;
  kind: string;
  weight: number;
  label?: string;
}

export interface MemoryGraph {
  nodes: GraphNode[];
  links: GraphLink[];
}

export interface DreamSession {
  session_id: string;
  messages_processed: number;
  ops_created: number;
  ops_updated: number;
  ops_deleted: number;
  ops_skipped: number;
}

export interface DreamResult {
  project_id: string;
  sessions_processed: number;
  messages_processed: number;
  ops_created: number;
  ops_updated: number;
  ops_deleted: number;
  ops_skipped: number;
  sessions?: DreamSession[];
  errors?: string[];
  skipped?: boolean;
}
