import type { LucideIcon } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { cn } from "@/lib/utils";

// PageLayout is the standard shell every route should use so pages stay
// consistent: a full-width PageHeader (icon + title + description, optional
// actions) with an edge-to-edge bottom rule, above a body whose width is
// either the standard reading column or the full content area. The header
// content and the body share the same column so the title lines up with the
// content even when the divider runs full-width.
export function PageLayout({
  title,
  icon,
  description,
  actions,
  fullWidth = false,
  children,
}: {
  title: string;
  icon?: LucideIcon;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  // false (default) → constrained reading column; true → full content width.
  fullWidth?: boolean;
  children: React.ReactNode;
}) {
  const column = fullWidth ? undefined : "mx-auto max-w-3xl";
  return (
    <div className="space-y-4">
      <PageHeader
        icon={icon}
        title={title}
        description={description}
        actions={actions}
        contentClassName={column}
      />
      <div className={cn("space-y-4", column)}>{children}</div>
    </div>
  );
}
