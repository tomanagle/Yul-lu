// Code-based TanStack Router setup. The root layout is a collapsible left
// sidebar (brand, project picker, primary nav, settings, collapse handle)
// plus a top bar and the routed outlet on the right. The project picker
// lives in the sidebar because every data page is scoped to it.

import { useEffect, useState } from "react";
import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  useLocation,
} from "@tanstack/react-router";
import {
  Brain,
  ChevronLeft,
  ChevronRight,
  ClipboardCheck,
  FolderOpen,
  Network,
  Radar,
  Settings,
  Sparkles,
  type LucideIcon,
} from "lucide-react";

import { DreamingPage } from "@/routes/dreaming";
import { GraphPage } from "@/routes/graph";
import { MemoriesPage } from "@/routes/memories";
import { RetrievalsPage } from "@/routes/retrievals";
import { ReviewPage } from "@/routes/review";
import { SettingsPage } from "@/routes/settings";
import { StatsPage } from "@/routes/stats";
import { useProjects } from "@/lib/queries";
import { ProjectScopeProvider, useProjectScope } from "@/lib/project-scope";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

// Primary nav: data pages. Settings is split out below — it's secondary
// (configuration), so it sits at the bottom of the sidebar with a divider.
type NavEntry = { to: string; label: string; icon: LucideIcon };

// Stats lives at "/" (clickable brand at top of sidebar) so we don't
// double up on a "Home"-shaped link.
const PRIMARY_NAV: NavEntry[] = [
  { to: "/memories", label: "Memories", icon: Brain },
  { to: "/graph", label: "Graph", icon: Network },
  { to: "/dreaming", label: "Dreaming", icon: Sparkles },
  { to: "/review", label: "Review", icon: ClipboardCheck },
  { to: "/retrievals", label: "Retrievals", icon: Radar },
];

const SETTINGS_NAV: NavEntry = {
  to: "/settings",
  label: "Settings",
  icon: Settings,
};

const TITLES: Record<string, string> = Object.fromEntries(
  [...PRIMARY_NAV, SETTINGS_NAV].map((n) => [n.to, n.label]),
);

// Sentinel for "all projects" — Radix Select can't bind to an empty value.
const ALL_PROJECTS = "__all__";

const COLLAPSE_KEY = "yullu.sidebar.collapsed";

function loadCollapsed(): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(COLLAPSE_KEY) === "1";
}

function RootLayout() {
  const [collapsed, setCollapsed] = useState(loadCollapsed);

  useEffect(() => {
    window.localStorage.setItem(COLLAPSE_KEY, collapsed ? "1" : "0");
  }, [collapsed]);

  return (
    <ProjectScopeProvider>
      <div className="flex h-full bg-background text-foreground">
        <Sidebar collapsed={collapsed} />

        {/* Floating collapse tab — sits on the seam between the sidebar and
            the main content, anchored vertically near the top so it's easy
            to grab. Flips its chevron based on state. */}
        <CollapseHandle collapsed={collapsed} onToggle={() => setCollapsed((v) => !v)} />

        <div className="flex min-w-0 flex-1 flex-col">
          <TopBar />
          <main className="flex-1 overflow-auto p-6">
            <Outlet />
          </main>
        </div>
      </div>
    </ProjectScopeProvider>
  );
}

function Sidebar({ collapsed }: { collapsed: boolean }) {
  return (
    <aside
      className={cn(
        "relative flex flex-col border-r border-border/40 bg-card/60 backdrop-blur-sm",
        "transition-[width] duration-200 ease-out",
        collapsed ? "w-14" : "w-60",
      )}
    >
      <SidebarBrand collapsed={collapsed} />

      <ProjectPicker collapsed={collapsed} />

      <nav className="flex flex-1 flex-col gap-0.5 px-2 pt-2">
        {PRIMARY_NAV.map((entry) => (
          <NavItem key={entry.to} entry={entry} collapsed={collapsed} />
        ))}
      </nav>

      {/* Secondary nav — Settings sits at the bottom with a divider. The
          collapse button used to live here; it's a floating handle now. */}
      <div className="border-t border-border/40 px-2 py-2">
        <NavItem entry={SETTINGS_NAV} collapsed={collapsed} />
      </div>
    </aside>
  );
}

// SidebarBrand doubles as the link to the Stats home page. Clicking the
// logo or wordmark goes to "/". Previously Stats was a sidebar nav item;
// folding it into the brand keeps the primary nav focused on the four
// data pages.
function SidebarBrand({ collapsed }: { collapsed: boolean }) {
  return (
    <Link
      to="/"
      className={cn(
        "flex items-center gap-2 px-4 pb-3 pt-5 transition-opacity duration-200",
        "rounded-md hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        collapsed && "justify-center px-0",
      )}
      title="Stats"
    >
      <span
        aria-hidden
        className="relative grid h-6 w-6 shrink-0 place-items-center rounded-full bg-primary/15"
      >
        <span className="absolute inset-0 rounded-full bg-gradient-to-br from-indigo-soft/40 via-violet/30 to-transparent" />
        <Brain className="relative h-3.5 w-3.5 text-violet-soft" />
      </span>
      {!collapsed && (
        <span className="text-sm font-semibold tracking-tight text-foreground">Yul'lu</span>
      )}
    </Link>
  );
}

// ProjectPicker is hidden when the sidebar is collapsed — there's no
// sensible 14px-wide UI for "choose between two long URL-like strings". The
// user can expand the sidebar to switch projects; the currently-selected
// project stays in context regardless.
function ProjectPicker({ collapsed }: { collapsed: boolean }) {
  const { data: projects } = useProjects();
  const { project, setProject } = useProjectScope();

  if (collapsed) {
    return (
      <div className="flex justify-center px-0 pb-2" title={project || "All projects"}>
        <FolderOpen className="h-4 w-4 text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="px-3 pb-2">
      <Select
        value={project || ALL_PROJECTS}
        onValueChange={(v) => setProject(v === ALL_PROJECTS ? "" : v)}
      >
        <SelectTrigger className="h-8 w-full border-border/40 bg-background/40 text-xs">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL_PROJECTS}>All projects</SelectItem>
          {(projects ?? []).map((p) => (
            <SelectItem key={p} value={p}>
              {p}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

function NavItem({ entry, collapsed }: { entry: NavEntry; collapsed: boolean }) {
  return (
    <Link
      to={entry.to}
      title={collapsed ? entry.label : undefined}
      className={cn(
        "group relative flex items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground",
        "transition-all duration-200 ease-out",
        "hover:bg-primary/10 hover:text-foreground",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0",
        collapsed && "justify-center px-0",
      )}
      activeProps={{
        className: "yullu-active-rail bg-primary/15 text-foreground",
      }}
    >
      <entry.icon className="h-4 w-4 shrink-0 transition-colors group-hover:text-violet-soft" />
      {!collapsed && <span className="truncate">{entry.label}</span>}
    </Link>
  );
}

// CollapseHandle is a slim floating tab anchored to the seam between the
// sidebar and the content area. Positioned absolutely so it doesn't push
// the layout; sits at viewport-relative top so it's reachable without
// scrolling. The chevron flips to indicate which way it'll collapse.
function CollapseHandle({ collapsed, onToggle }: { collapsed: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      className={cn(
        "group absolute top-6 z-20 flex h-10 w-5 items-center justify-center",
        "rounded-r-md border border-l-0 border-border/40 bg-card/70 backdrop-blur",
        "text-muted-foreground transition-all duration-200 ease-out",
        "hover:border-accent/40 hover:bg-card hover:text-foreground hover:shadow-[0_0_18px_-4px_hsl(263_84%_58%/0.6)]",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        // Slide with the sidebar — pinned to its right edge.
        collapsed ? "left-14" : "left-60",
      )}
      style={{ transitionProperty: "left, color, background-color, border-color, box-shadow" }}
    >
      {collapsed ? (
        <ChevronRight className="h-3.5 w-3.5" />
      ) : (
        <ChevronLeft className="h-3.5 w-3.5" />
      )}
    </button>
  );
}

function TopBar() {
  const location = useLocation();
  const title = TITLES[location.pathname] ?? "Yul'lu";
  return (
    <header className="flex h-12 items-center border-b border-border/40 bg-card/40 px-6 backdrop-blur-sm">
      <span className="text-sm font-semibold tracking-tight text-foreground">{title}</span>
    </header>
  );
}

const rootRoute = createRootRoute({ component: RootLayout });

// "/" is the Stats home page. The brand link in the sidebar points here.
// (Previously "/" redirected to "/stats"; that page is now folded into
// the index route since Stats has been dropped from the primary nav.)
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: StatsPage,
});

const memoriesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/memories",
  component: MemoriesPage,
});

const dreamingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/dreaming",
  component: DreamingPage,
});

const statsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/stats",
  component: StatsPage,
});

const graphRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/graph",
  component: GraphPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings",
  component: SettingsPage,
});

const reviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/review",
  component: ReviewPage,
});

const retrievalsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/retrievals",
  component: RetrievalsPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  memoriesRoute,
  dreamingRoute,
  statsRoute,
  graphRoute,
  reviewRoute,
  retrievalsRoute,
  settingsRoute,
]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
