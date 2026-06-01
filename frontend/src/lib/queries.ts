// Thin TanStack Query layer over the REST API. Components stay free of
// side-effect logic; data fetching, cache invalidation, and optimistic
// updates live here.

import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationOptions,
} from "@tanstack/react-query";

import {
  DeleteMemory,
  Dream,
  GetBufferedSessions,
  GetConfig,
  GetDreamPasses,
  GetDreamProgress,
  GetDreamPrompt,
  GetDreamStats,
  GetMemoryEventsByDay,
  GetMemoryGraph,
  GetMemoryStats,
  GetProjectOverrides,
  GetRetrievals,
  GetSessionStats,
  GetUnratedMemories,
  GetUsageByDay,
  GetUsageSummary,
  ListMemories,
  ListProjects,
  RateMemory,
  Retry,
  SaveConfig,
  SaveDreamPrompt,
  SaveProjectOverrides,
  SearchMemories,
  Status,
  UpdateMemory,
} from "./api";
import type {
  ConfigView,
  DreamResult,
  Memory,
  ProjectOverridePayload,
  ProjectOverridesView,
  Status as StatusT,
} from "./schemas";

// Centralised query keys so invalidation is grep-able.
export const qk = {
  status: ["status"] as const,
  config: ["config"] as const,
  projects: ["projects"] as const,
  memories: (projectID: string, query: string = "") => ["memories", projectID, query] as const,
  sessionStats: (projectID: string) => ["session-stats", projectID] as const,
  memoryStats: (projectID: string) => ["memory-stats", projectID] as const,
  memoryEventsByDay: (projectID: string, days: number) =>
    ["memory-events-by-day", projectID, days] as const,
  usageByDay: (days: number) => ["usage-by-day", days] as const,
  usageSummary: (sinceHours: number) => ["usage-summary", sinceHours] as const,
  memoryGraph: (projectID: string) => ["memory-graph", projectID] as const,
  dreamStats: (projectID: string, days: number) => ["dream-stats", projectID, days] as const,
  projectOverrides: (projectID: string) => ["project-overrides", projectID] as const,
  bufferedSessions: (projectID: string) => ["buffered-sessions", projectID] as const,
  dreamPrompt: ["dream-prompt"] as const,
  dreamProgress: ["dream-progress"] as const,
  dreamPasses: (projectID: string) => ["dream-passes", projectID] as const,
  unratedMemories: (projectID: string) => ["unrated-memories", projectID] as const,
  retrievals: (projectID: string) => ["retrievals", projectID] as const,
};

// Polling cadences. Local SQLite queries via Wails are cheap (sub-ms), so we
// can poll aggressively to make the UI feel realtime without much cost.
// Polling cadences. These are background refreshes — the data also refetches
// when you act on it (mutations invalidate) — so they can be lazy. Several
// queries share each tier, so short intervals mean a burst of requests every
// few seconds; keep them slow.
const POLL_FAST = 10_000; // memories list, dream buffer — fairly live, not spammy
const POLL_SLOW = 30_000; // dashboard aggregates (stats, usage, graph) — change slowly
const POLL_IDLE = 30_000; // session-buffer counts — change rarely, fine to poll lazily

export function useStatus() {
  return useQuery({ queryKey: qk.status, queryFn: () => Status() });
}

export function useRetry() {
  const qc = useQueryClient();
  return useMutation<StatusT, Error, void>({
    mutationFn: () => Retry(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.status });
      qc.invalidateQueries({ queryKey: qk.projects });
    },
  });
}

export function useConfig() {
  return useQuery({ queryKey: qk.config, queryFn: () => GetConfig() });
}

export function useProjects() {
  return useQuery({
    queryKey: qk.projects,
    queryFn: () => ListProjects(),
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}

export function useMemories(projectID: string, query: string = "") {
  const trimmed = query.trim();
  return useQuery({
    queryKey: qk.memories(projectID, trimmed),
    queryFn: () =>
      trimmed ? SearchMemories(projectID, trimmed, 100) : ListMemories(projectID, 100),
    // Each keystroke changes the query key (the search term is part of it), so
    // without this the list would blank to a loading state on every character.
    // keepPreviousData holds the prior results on screen until the new ones
    // arrive — `isPlaceholderData` flags the in-between so the UI can dim them.
    placeholderData: keepPreviousData,
    // Local DB queries are cheap. Poll the unfiltered view often so new
    // memories appear within a couple of seconds. Search results are stable
    // per query, so disable polling there to avoid re-running the same vector
    // search in a loop.
    staleTime: 0,
    refetchInterval: trimmed ? false : POLL_FAST,
    refetchIntervalInBackground: false,
  });
}

export function useSessionStats(projectID: string) {
  return useQuery({
    queryKey: qk.sessionStats(projectID),
    queryFn: () => GetSessionStats(projectID),
    staleTime: POLL_IDLE,
    refetchInterval: POLL_IDLE,
    refetchIntervalInBackground: false,
  });
}

export function useMemoryStats(projectID: string) {
  return useQuery({
    queryKey: qk.memoryStats(projectID),
    queryFn: () => GetMemoryStats(projectID),
    staleTime: 0,
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}

export function useDreamStats(projectID: string, days: number = 30) {
  return useQuery({
    queryKey: qk.dreamStats(projectID, days),
    queryFn: () => GetDreamStats(projectID, days),
    staleTime: 0,
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}

export function useMemoryEventsByDay(projectID: string, days: number = 30) {
  return useQuery({
    queryKey: qk.memoryEventsByDay(projectID, days),
    queryFn: () => GetMemoryEventsByDay(projectID, days),
    staleTime: 0,
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}

export function useUsageByDay(days: number = 30) {
  return useQuery({
    queryKey: qk.usageByDay(days),
    queryFn: () => GetUsageByDay(days),
    staleTime: 0,
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}

export function useUsageSummary(sinceHours: number = 0) {
  return useQuery({
    queryKey: qk.usageSummary(sinceHours),
    queryFn: () => GetUsageSummary(sinceHours),
    staleTime: 0,
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}

export function useMemoryGraph(projectID: string) {
  return useQuery({
    queryKey: qk.memoryGraph(projectID),
    queryFn: () => GetMemoryGraph(projectID),
    // Graph computation does N+1 SQL queries - heavier than other endpoints,
    // and the layout is the same as long as memories haven't churned. Use a
    // long stale time and don't poll.
    staleTime: 30_000,
  });
}

export function useSaveConfig(options?: UseMutationOptions<StatusT, Error, ConfigView>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (cfg: ConfigView) => SaveConfig(cfg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.status });
      qc.invalidateQueries({ queryKey: qk.config });
    },
    ...options,
  });
}

export function useDeleteMemory(projectID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => DeleteMemory(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.memories(projectID) });
      qc.invalidateQueries({ queryKey: qk.projects });
    },
  });
}

export type UpdateMemoryInput = {
  id: number;
  content: string;
  tags: string[];
};

export function useUpdateMemory(projectID: string) {
  const qc = useQueryClient();
  return useMutation<Memory, Error, UpdateMemoryInput>({
    mutationFn: ({ id, content, tags }) => UpdateMemory(id, content, tags),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.memories(projectID) });
    },
  });
}

export function useProjectOverrides(projectID: string) {
  return useQuery({
    queryKey: qk.projectOverrides(projectID),
    queryFn: () => GetProjectOverrides(projectID),
    enabled: !!projectID,
    staleTime: 30_000,
  });
}

export function useSaveProjectOverrides(projectID: string) {
  const qc = useQueryClient();
  return useMutation<
    ProjectOverridesView,
    Error,
    { repo: ProjectOverridePayload; user: ProjectOverridePayload }
  >({
    mutationFn: ({ repo, user }) => SaveProjectOverrides(projectID, repo, user),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.projectOverrides(projectID) });
      // Saved overrides affect the resolved config the server uses for
      // dreaming/reconcile, so the next status / dream / etc. should
      // refetch too.
      qc.invalidateQueries({ queryKey: qk.status });
    },
  });
}

export function useDream(projectID: string) {
  const qc = useQueryClient();
  return useMutation<DreamResult, Error, void>({
    mutationFn: () => Dream(projectID),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.memories(projectID) });
      qc.invalidateQueries({ queryKey: qk.sessionStats(projectID) });
      qc.invalidateQueries({ queryKey: qk.bufferedSessions(projectID) });
      qc.invalidateQueries({ queryKey: qk.projects });
    },
  });
}

export function useBufferedSessions(projectID: string) {
  return useQuery({
    queryKey: qk.bufferedSessions(projectID),
    queryFn: () => GetBufferedSessions(projectID),
    staleTime: 0,
    refetchInterval: POLL_FAST,
    refetchIntervalInBackground: false,
  });
}

export function useDreamPrompt() {
  return useQuery({
    queryKey: qk.dreamPrompt,
    queryFn: () => GetDreamPrompt(),
    staleTime: 30_000,
  });
}

export function useSaveDreamPrompt() {
  const qc = useQueryClient();
  return useMutation<unknown, Error, string>({
    mutationFn: (prompt: string) => SaveDreamPrompt(prompt),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.dreamPrompt });
    },
  });
}

// useDreamProgress polls /api/dream/progress so the dashboard can show a
// live "Dreaming…" indicator. Refetch interval shortens while a pass is
// running (fast feedback) and stretches when idle (no point hammering
// the server for an unchanged snapshot).
export function useDreamProgress() {
  return useQuery({
    queryKey: qk.dreamProgress,
    queryFn: () => GetDreamProgress(),
    staleTime: 0,
    // Tight (1s) only while a pass is actively running so the progress bar is
    // live; otherwise idle-poll slowly — nothing's changing between passes.
    refetchInterval: (q) => (q.state.data?.running ? 1000 : POLL_IDLE),
  });
}

// useUnratedMemories powers the Review queue. Memories already rated
// drop out of the list; rejections (≤ 5) disappear because the server
// deletes the row outright.
export function useUnratedMemories(projectID: string) {
  return useQuery({
    queryKey: qk.unratedMemories(projectID),
    queryFn: () => GetUnratedMemories(projectID),
    enabled: !!projectID,
    staleTime: POLL_SLOW,
  });
}

// useRateMemory submits a rating. On success we invalidate the unrated
// list (so the just-rated row disappears) AND the regular memories list
// for the project (so survivors show their new rating + rejections
// disappear).
export function useRateMemory(projectID: string) {
  const qc = useQueryClient();
  return useMutation<unknown, Error, { id: number; rating: number; comment: string }>({
    mutationFn: ({ id, rating, comment }) => RateMemory(id, rating, comment),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.unratedMemories(projectID) });
      qc.invalidateQueries({ queryKey: qk.memories(projectID) });
      qc.invalidateQueries({ queryKey: qk.memoryStats(projectID) });
    },
  });
}

// useRetrievals powers the Retrievals surface: recent retrieve calls grouped
// by query, each with the memories that were sent to the agent.
export function useRetrievals(projectID: string) {
  return useQuery({
    queryKey: qk.retrievals(projectID),
    queryFn: () => GetRetrievals(projectID, 50),
    enabled: !!projectID,
    staleTime: 0,
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}

// useDreamPasses powers the Stats page's per-cycle table.
export function useDreamPasses(projectID: string) {
  return useQuery({
    queryKey: qk.dreamPasses(projectID),
    queryFn: () => GetDreamPasses(projectID, 30),
    staleTime: 0,
    refetchInterval: POLL_SLOW,
    refetchIntervalInBackground: false,
  });
}
