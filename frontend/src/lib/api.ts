// Thin fetch wrapper for the Go REST API at /api/*. Each exported function
// mirrors a method on the Go App struct; the return types live in types.ts.
// Errors throw with the server-reported message so TanStack Query surfaces
// them as `query.error.message`.

// Type imports are aliased to avoid colliding with the exported function
// names below (e.g. the `Status` function returns a `StatusT`).
import type {
  ConfigView as ConfigViewT,
  DailyMemoryEvents as DailyMemoryEventsT,
  DailyUsage as DailyUsageT,
  DreamResult as DreamResultT,
  Memory as MemoryT,
  MemoryGraph as MemoryGraphT,
  MemoryStats as MemoryStatsT,
  SessionStats as SessionStatsT,
  Status as StatusT,
  UsageBucket as UsageBucketT,
} from "./types";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
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
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// List endpoints return {items: [...]} envelopes (see writeList in
// internal/handlers/json.go for the rationale). requestList unwraps so the
// public API surface still hands callers a plain array. When we want to
// surface pagination metadata later, we'll evolve this signature to return
// the whole envelope.
async function requestList<T>(
  path: string,
  init?: RequestInit,
): Promise<T[]> {
  const res = await request<{ items: T[] }>(path, init);
  return res.items ?? [];
}

export const Status = () => request<StatusT>("/api/status");
export const Retry = () => request<StatusT>("/api/retry", { method: "POST" });

export const GetConfig = () => request<ConfigViewT>("/api/config");
export const SaveConfig = (cfg: ConfigViewT) =>
  request<StatusT>("/api/config", {
    method: "POST",
    body: JSON.stringify(cfg),
  });

export const ListProjects = () => requestList<string>("/api/projects");

export function ListMemories(
  projectID: string,
  limit: number,
): Promise<MemoryT[]> {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  if (limit) params.set("limit", String(limit));
  return requestList<MemoryT>(`/api/memories?${params}`);
}

export function SearchMemories(
  projectID: string,
  query: string,
  limit: number,
): Promise<MemoryT[]> {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  params.set("q", query);
  if (limit) params.set("limit", String(limit));
  return requestList<MemoryT>(`/api/memories?${params}`);
}

export const UpdateMemory = (id: number, content: string, tags: string[]) =>
  request<MemoryT>(`/api/memories/${id}`, {
    method: "PUT",
    body: JSON.stringify({ content, tags }),
  });

export const DeleteMemory = (id: number) =>
  request<void>(`/api/memories/${id}`, { method: "DELETE" });

export const GetSessionStats = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return request<SessionStatsT>(`/api/sessions/stats?${params}`);
};

export const Dream = (projectID: string) =>
  request<DreamResultT>("/api/dream", {
    method: "POST",
    body: JSON.stringify({ project_id: projectID }),
  });

export const GetMemoryStats = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return request<MemoryStatsT>(`/api/stats?${params}`);
};

export const GetMemoryEventsByDay = (projectID: string, days: number) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  if (days) params.set("days", String(days));
  return requestList<DailyMemoryEventsT>(`/api/stats/events?${params}`);
};

export const GetUsageByDay = (days: number) => {
  const params = new URLSearchParams();
  if (days) params.set("days", String(days));
  return requestList<DailyUsageT>(`/api/usage/by-day?${params}`);
};

export const GetUsageSummary = (sinceHours: number) => {
  const params = new URLSearchParams();
  if (sinceHours) params.set("since_hours", String(sinceHours));
  return requestList<UsageBucketT>(`/api/usage/summary?${params}`);
};

export const GetMemoryGraph = (projectID: string) => {
  const params = new URLSearchParams();
  if (projectID) params.set("project_id", projectID);
  return request<MemoryGraphT>(`/api/graph?${params}`);
};
