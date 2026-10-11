import type { ReactNode } from "react";

/** PageHeader is the top strip of a page, which moves the window. */
export function PageHeader({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <header className="drag bg-background/85 supports-[backdrop-filter]:bg-background/70 sticky top-0 z-10 flex h-12 shrink-0 items-center gap-3 border-b backdrop-blur-md pr-[max(var(--mygo-titlebar-inset-right,0px),0.75rem)] pl-4">
      <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>
      {actions && <div className="no-drag flex shrink-0 items-center gap-1">{actions}</div>}
    </header>
  );
}
