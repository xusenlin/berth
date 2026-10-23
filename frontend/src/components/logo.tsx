import { useId } from "react";

import { cn } from "@/lib/utils";

/**
 * Logo is Berth's icon (design/icon.svg) without the macOS margins: two
 * server racks stacked into a B. Its colours come from the palette's
 * --logo-from, --logo-to and --logo-tint, so it follows the theme.
 */
export function Logo({ className }: { className?: string }) {
  const id = useId();
  return (
    <svg viewBox="0 0 24 24" className={cn("size-8", className)} aria-label="Berth">
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" style={{ stopColor: "var(--logo-from)" }} />
          <stop offset="1" style={{ stopColor: "var(--logo-to)" }} />
        </linearGradient>
      </defs>
      <rect width="24" height="24" rx="5.4" fill={`url(#${id})`} />
      <g strokeWidth="0.8" strokeLinejoin="round">
        <path d="M6.5 5.25h7a3.25 3.25 0 0 1 0 6.5h-7z" fill="#fff" stroke="#fff" />
        <path d="M6.5 12.75h8.25a3.25 3.25 0 0 1 0 6.5H6.5z" style={{ fill: "var(--logo-tint)", stroke: "var(--logo-tint)" }} />
      </g>
      <circle cx="9.1" cy="8.5" r="1.1" style={{ fill: "var(--logo-to)" }} />
      <circle cx="9.1" cy="16" r="1.1" style={{ fill: "var(--logo-from)" }} />
    </svg>
  );
}
