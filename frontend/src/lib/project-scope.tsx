// Project scope is a sidebar-level setting: every data page (Stats,
// Memories, Graph, Dreaming) shares the same selected project, so users
// don't have to re-pick it when switching tabs. Held in context rather than
// a URL search-param to keep the diff small; if we later want sharable
// links a search-param adapter can replace this.

import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";

import { useProjects } from "./queries";

// "" = "all projects". Aligns with how the backend interprets an empty
// project_id (no filter).
type ProjectScope = {
  project: string;
  setProject: (p: string) => void;
};

const Ctx = createContext<ProjectScope | null>(null);

const STORAGE_KEY = "yullu.project";

export function ProjectScopeProvider({ children }: { children: ReactNode }) {
  const { data: projects } = useProjects();
  const [project, setProjectState] = useState<string>(() => {
    if (typeof window === "undefined") return "";
    return window.localStorage.getItem(STORAGE_KEY) ?? "";
  });

  // Default to the first project once the list lands. Skips if the user
  // already picked one (including "all projects" via the picker).
  useEffect(() => {
    if (!projects || projects.length === 0) return;
    if (project) return; // user has a value (real or persisted)
    setProjectState(projects[0]);
  }, [projects, project]);

  const setProject = (p: string) => {
    setProjectState(p);
    if (typeof window !== "undefined") {
      window.localStorage.setItem(STORAGE_KEY, p);
    }
  };

  return <Ctx.Provider value={{ project, setProject }}>{children}</Ctx.Provider>;
}

export function useProjectScope(): ProjectScope {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error("useProjectScope must be used inside ProjectScopeProvider");
  }
  return ctx;
}
