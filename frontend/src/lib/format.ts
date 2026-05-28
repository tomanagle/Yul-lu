// Small formatting helpers used by the UI. Kept dep-free so we don't pull
// in a full date library just for one relative-time string.

/**
 * relativeTime renders an ISO timestamp (or Date) as a short, human-friendly
 * string: "12s ago", "4m ago", "3h ago", "2d ago", or the absolute date
 * for anything older than a week.
 */
export function relativeTime(input: string | Date | undefined): string {
  if (!input) return "";
  const date = typeof input === "string" ? new Date(input) : input;
  if (Number.isNaN(date.getTime())) return String(input);

  const diffMs = Date.now() - date.getTime();
  const sec = Math.round(diffMs / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.round(hr / 24);
  if (day < 7) return `${day}d ago`;
  return date.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

/** shortUUID truncates a UUID for compact display (keeps the first segment). */
export function shortUUID(uuid: string | undefined): string {
  if (!uuid) return "";
  const i = uuid.indexOf("-");
  return i > 0 ? uuid.slice(0, i) : uuid.slice(0, 8);
}
