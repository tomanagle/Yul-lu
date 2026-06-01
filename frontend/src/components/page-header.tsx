import type { LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

// PageHeader is the shared, full-width page header: a real <header> with an
// <h1>, an optional tinted icon chip (tying into the sidebar's icon language),
// a constrained lede for readability, an optional actions slot, and a hairline
// rule that delineates it from the page body. Replaces the old "Card wrapping
// just a title" look, which read as an empty floating box rather than a header.
export function PageHeader({
  icon: Icon,
  title,
  description,
  actions,
  className,
  contentClassName,
}: {
  icon?: LucideIcon;
  title: string;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
  // Constrains the header's CONTENT (title/lede) while the bottom rule stays
  // full-width. Pages with a narrow body pass their column width here (e.g.
  // "mx-auto max-w-3xl") so the title lines up with the content below but the
  // divider still runs edge-to-edge. Omit on full-width pages.
  contentClassName?: string;
}) {
  return (
    <header
      className={cn(
        "relative border-b border-border/60 pb-5",
        "duration-300 animate-in fade-in-50 slide-in-from-top-1",
        className,
      )}
    >
      <div className={cn(contentClassName)}>
        <div className="flex items-start justify-between gap-4">
          <div className="flex items-center gap-3">
            {Icon && (
              <span
                aria-hidden
                className={cn(
                  "grid h-11 w-11 shrink-0 place-items-center rounded-2xl",
                  "bg-gradient-to-br from-primary/15 to-accent/10 text-primary",
                  "ring-1 ring-inset ring-primary/20 yullu-ring-primary",
                )}
              >
                <Icon className="h-5 w-5" />
              </span>
            )}
            <h1 className="yullu-display text-2xl leading-tight tracking-tight text-foreground sm:text-3xl">
              {title}
            </h1>
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </div>
        {description && (
          <p className="mt-3 max-w-2xl text-sm leading-relaxed text-muted-foreground">
            {description}
          </p>
        )}
      </div>
    </header>
  );
}
