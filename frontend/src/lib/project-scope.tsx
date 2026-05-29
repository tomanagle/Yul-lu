// Project scope is a sidebar-level setting: every data page (Stats,
// Memories, Graph, Dreaming) shares the same selected project, so users
// don't have to re-pick it when switching tabs. Held in context rather than
// a URL search-param to keep the diff small; if we later want sharable
// links a search-param adapter can replace this.

import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

import { useProjects } from "./queries";

// "" = "all projects". Aligns with how the backend interprets an empty
// project_id (no filter). The picker exposes a "" string to consumers;
// internally we use `null` to mean "not initialised yet" so the auto-
// default effect can distinguish "user picked All projects" (which we
// must respect) from "first load, no choice made" (default to the first
// project so a brand-new install has something to look at).
type ProjectScope = {
  project: string;
  setProject: (p: string) => void;
};

const Ctx = createContext<ProjectScope | null>(null);

const STORAGE_KEY = "yullu.project";

export function ProjectScopeProvider({ children }: { children: ReactNode }) {
  const { data: projects } = useProjects();
  // null = no choice yet; string (incl. "") = the user's chosen scope.
  // localStorage having the key (even with value "") means a choice was
  // made on a previous visit, so we honour "" as "All projects".
  const [project, setProjectState] = useState<string | null>(() => {
    if (typeof window === "undefined") return null;
    const raw = window.localStorage.getItem(STORAGE_KEY);
    return raw === null ? null : raw;
  });

  // Default to the first project once the list lands, but ONLY if the
  // user hasn't made a choice yet. Previously this also ran whenever
  // `project === ""`, which silently clobbered explicit "All projects"
  // back to projects[0] on every re-render.
  useEffect(() => {
    if (project !== null) return;
    if (!projects || projects.length === 0) return;
    setProjectState(projects[0]);
  }, [projects, project]);

  const setProject = (p: string) => {
    setProjectState(p);
    if (typeof window !== "undefined") {
      window.localStorage.setItem(STORAGE_KEY, p);
    }
  };

  // Surface "" to consumers while we're still null — keeps the existing
  // contract (project: string) and the backend's "" = all-projects
  // convention. Once the effect lands, the real value replaces it.
  return <Ctx.Provider value={{ project: project ?? "", setProject }}>{children}</Ctx.Provider>;
}

export function useProjectScope(): ProjectScope {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error("useProjectScope must be used inside ProjectScopeProvider");
  }
  return ctx;
}
