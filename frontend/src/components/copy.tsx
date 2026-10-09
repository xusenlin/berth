import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";

/** Code shows a command or path with a copy button. */
export function Code({ children, className }: { children: string; className?: string }) {
  const [done, setDone] = useState(false);
  return (
    <span className={cn("bg-muted inline-flex max-w-full items-center gap-1 rounded px-1.5 py-0.5 font-mono text-xs", className)}>
      <span className="selectable truncate">{children}</span>
      <button
        type="button"
        className="text-muted-foreground hover:text-foreground shrink-0"
        title="复制"
        onClick={() => {
          navigator.clipboard.writeText(children).then(() => {
            setDone(true);
            setTimeout(() => setDone(false), 1200);
          });
        }}
      >
        {done ? <Check className="size-3" /> : <Copy className="size-3" />}
      </button>
    </span>
  );
}
