import { cn } from "@/lib/utils";

/** Logo is Berth's icon (design/icon.svg) without the macOS margins. */
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={cn("size-8", className)} aria-label="Berth">
      <defs>
        <linearGradient id="berth-logo-bg" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#14B8A6" />
          <stop offset="1" stopColor="#0B5A78" />
        </linearGradient>
      </defs>
      <rect width="24" height="24" rx="5.4" fill="url(#berth-logo-bg)" />
      <g transform="translate(5.71 5.71) scale(0.524)" fill="none" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d="M12 22V8" />
        <path d="M5 12H2a10 10 0 0 0 20 0h-3" />
        <circle cx="12" cy="5" r="3" />
      </g>
    </svg>
  );
}
