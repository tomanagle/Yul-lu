import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

/** cn merges tailwind class strings, deduplicating conflicting utilities. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
