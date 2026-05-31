// Thin fetch wrapper for the Go REST API at /api/*. Each exported function
// mirrors a method on the Go App struct and parses the response through a
// zod schema (see lib/schemas.ts) - so wire-format drift between the
// server and the client surfaces as a thrown ZodError at the boundary
// instead of a silent undefined deep inside a chart.
//
// Errors throw with the server-reported message so TanStack Query surfaces
// them as `query.error.message`.

import type { z } from "zod";

import {
  BufferedSessionsListSchema,
  ConfigViewSchema,
  DailyMemoryEventsListSchema,
  DailyUsageListSchema,
  DreamPassesListSchema,
  DreamProgressSchema,
  DreamPromptViewSchema,
  DreamResultSchema,
  DreamStatsSchema,
  MemoriesListSchema,
  MemoryGraphSchema,
  MemorySchema,
  MemoryStatsSchema,
  ProjectOverridesViewSchema,
  ProjectsListSchema,
  RetrievalsListSchema,
  SessionStatsSchema,
  StatusSchema,
  UsageBucketListSchema,
  type ConfigView,
  type ProjectOverridePayload,
} from "./schemas";

// rawRequest is the unschema'd fetch - used by helpers below. Most
// callers want `request` (validated) or `requestList` (validated + unwrapped).
async function rawRequest(path: string, init?: RequestInit): Promise<unknown> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) msg = body.error;
    } catch {
      // Body wasn't JSON; keep the status-line message.
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined;
  return await res.json();
}

// request runs the response through `schema.parse`. The parse throws on a
// schema mismatch, which means a server-side struct change without a
// matching schema update fails loudly here instead of producing undefined
// values in components.
async function request<S extends z.ZodTypeAny>(
  schema: S,
  path: string,
  init?: RequestInit,
): Promise<z.infer<S>> {
  const body = await rawRequest(path, init);
  return schema.parse(body) as z.infer<S>;
}

// requestList unwraps the standard {items: [...]} envelope (see the
// writeList helper on the Go side). The schema's item type carries through
// to the return type - no extra generics needed at the call site.
async function requestList<T>(
  listSchema: z.ZodType<{ items: T[] }>,
  path: string,
  init?: RequestInit,
): Promise<T[]> {
  const body = await rawRequest(path, init);
  return listSchema.parse(body).items;
}

// ----- Endpoints -----------------------------------------------------------

export const Status = () => request(StatusSchema, "/api/status");
export const Retry = () => request(StatusSchema, "/api/retry", { method: "POST" });

export const GetConfig = () => request(ConfigViewSchema, "/api/config");
export const SaveConfig = (cfg: ConfigView) =>
  request(StatusSchema, "/api/config", {
    method: "POST",
    body: JSON.stringify(cfg),
  });

export const ListProjects = () => requestList(ProjectsListSchema, "/api/projects");

export function ListMemories(projectID: string, limit: number) {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  if (limit) params.set("limit", String(limit));
  return requestList(MemoriesListSchema, `/api/memories?${params}`);
}

export function SearchMemories(projectID: string, query: string, limit: number) {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  params.set("q", query);
  if (limit) params.set("limit", String(limit));
  return requestList(MemoriesListSchema, `/api/memories?${params}`);
}

export const UpdateMemory = (id: number, content: string, tags: string[]) =>
  request(MemorySchema, `/api/memories/${id}`, {
    method: "PUT",
    body: JSON.stringify({ content, tags }),
  });

export const DeleteMemory = (id: number) => rawRequest(`/api/memories/${id}`, { method: "DELETE" });

// Review queue: list un-rated + submit a rating. The rate endpoint's
// response is either the updated memory (rating ≥ 6) or {rejected: true}
// (rating ≤ 5 → row deleted, anti-example archived). We treat both as
// success and let the caller refetch the list.
export const GetUnratedMemories = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return requestList(MemoriesListSchema, `/api/memories/unrated?${params}`);
};

export const RateMemory = (id: number, rating: number, comment: string) =>
  rawRequest(`/api/memories/${id}/rate`, {
    method: "POST",
    body: JSON.stringify({ rating, comment }),
  });

// Retrieval analytics: the recall audit trail + per-recall relevance
// verdicts. RateRetrieval keys off the recall event id (not the memory);
// verdict is +1 (good match) / -1 (bad match). The response is a lightweight
// echo, so callers refetch the list on success.
export const GetRetrievals = (projectID: string, limit = 100) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  if (limit) params.set("limit", String(limit));
  return requestList(RetrievalsListSchema, `/api/retrievals?${params}`);
};

export const RateRetrieval = (eventID: number, verdict: 1 | -1, comment: string) =>
  rawRequest(`/api/retrievals/${eventID}/rate`, {
    method: "POST",
    body: JSON.stringify({ verdict, comment }),
  });

export const GetSessionStats = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return request(SessionStatsSchema, `/api/sessions/stats?${params}`);
};

export const Dream = (projectID: string) =>
  request(DreamResultSchema, "/api/dream", {
    method: "POST",
    body: JSON.stringify({ project_id: projectID }),
  });

export const GetDreamStats = (projectID: string, days: number) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  if (days) params.set("days", String(days));
  return request(DreamStatsSchema, `/api/dream/stats?${params}`);
};

export const GetMemoryStats = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return request(MemoryStatsSchema, `/api/stats?${params}`);
};

export const GetMemoryEventsByDay = (projectID: string, days: number) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  if (days) params.set("days", String(days));
  return requestList(DailyMemoryEventsListSchema, `/api/stats/events?${params}`);
};

export const GetUsageByDay = (days: number) => {
  const params = new URLSearchParams();
  if (days) params.set("days", String(days));
  return requestList(DailyUsageListSchema, `/api/usage/by-day?${params}`);
};

export const GetUsageSummary = (sinceHours: number) => {
  const params = new URLSearchParams();
  if (sinceHours) params.set("since_hours", String(sinceHours));
  return requestList(UsageBucketListSchema, `/api/usage/summary?${params}`);
};

export const GetMemoryGraph = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return request(MemoryGraphSchema, `/api/graph?${params}`);
};

export const GetProjectOverrides = (projectID: string) =>
  request(ProjectOverridesViewSchema, `/api/projects/${encodeURIComponent(projectID)}/overrides`);

export const SaveProjectOverrides = (
  projectID: string,
  repo: ProjectOverridePayload,
  user: ProjectOverridePayload,
) =>
  request(ProjectOverridesViewSchema, `/api/projects/${encodeURIComponent(projectID)}/overrides`, {
    method: "POST",
    body: JSON.stringify({ repo, user }),
  });

export const GetBufferedSessions = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return requestList(BufferedSessionsListSchema, `/api/sessions?${params}`);
};

export const GetDreamPrompt = () => request(DreamPromptViewSchema, "/api/dream/prompt");

export const SaveDreamPrompt = (prompt: string) =>
  request(DreamPromptViewSchema, "/api/dream/prompt", {
    method: "POST",
    body: JSON.stringify({ prompt }),
  });

export const GetDreamProgress = () => request(DreamProgressSchema, "/api/dream/progress");

export const GetDreamPasses = (projectID: string, limit = 30) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  params.set("limit", String(limit));
  return requestList(DreamPassesListSchema, `/api/dream/passes?${params}`);
};
