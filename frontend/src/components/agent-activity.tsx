import { useEffect, useRef, useState } from "react";
import { Check, Sparkles, X } from "lucide-react";
import { useAgentCalls, toolInfo, type Call } from "@/lib/agent";
import { useNav } from "@/lib/nav";
import { useServers } from "@/lib/queries";
import { cn } from "@/lib/utils";

/** The most calls shown at once; older ones wait below the fold. */
const shown = 3;

/**
 * AgentActivity shows what agents connected over MCP are doing: a glow
 * around the window while a call runs, and a card per call at the bottom
 * right, where the toasts make room for them.
 */
export function AgentActivity() {
  const calls = useAgentCalls();
  const running = calls.some((c) => c.state === "running");
  const visible = calls.slice(-shown);
  const hidden = calls.length - visible.length;
  const stack = useRef<HTMLDivElement>(null);

  // Lift the toasts above the cards (index.css, --agent-stack).
  useEffect(() => {
    const el = stack.current!;
    const root = document.documentElement.style;
    const ro = new ResizeObserver(() => {
      const h = el.offsetHeight;
      root.setProperty("--agent-stack", h ? `${h + 8}px` : "0px");
    });
    ro.observe(el);
    return () => {
      ro.disconnect();
      root.removeProperty("--agent-stack");
    };
  }, []);

  return (
    <>
      <div aria-hidden className={cn("agent-glow", running && "agent-glow-on")} />
      <div
        ref={stack}
        className="pointer-events-none absolute right-6 bottom-6 z-40 flex w-[356px] max-w-[calc(100%-3rem)] flex-col items-end gap-1.5"
      >
        {hidden > 0 && (
          <div className="bg-popover/80 text-muted-foreground shadow-surface rounded-full border px-2.5 py-0.5 text-[11px] backdrop-blur">
            还有 {hidden} 个调用
          </div>
        )}
        {visible.map((c, i) => (
          <CallCard key={c.id} call={c} depth={visible.length - 1 - i} />
        ))}
      </div>
    </>
  );
}

function CallCard({ call, depth }: { call: Call; depth: number }) {
  const { go } = useNav();
  const servers = useServers();
  const server = servers.data?.find((s) => s.server.id === call.server)?.server.name;
  const { label, icon: Icon } = toolInfo(call);
  const running = call.state === "running";
  const failed = call.state === "failed";
  const where = [call.app, server].filter(Boolean).join(" · ");

  return (
    <button
      type="button"
      disabled={!call.server}
      onClick={() => call.server && go({ server: call.server, app: call.app || undefined })}
      title={failed ? call.error : call.detail}
      style={{ scale: 1 - depth * 0.04, opacity: call.leaving ? 0 : 1 - depth * 0.2, transformOrigin: "right bottom" }}
      className={cn(
        "agent-card animate-in fade-in slide-in-from-bottom-3 zoom-in-95 pointer-events-auto relative flex w-full items-center gap-3 rounded-2xl py-2 pr-3 pl-2 text-left duration-300",
        "bg-popover/85 text-popover-foreground shadow-raised backdrop-blur-xl transition-[opacity,scale,translate]",
        "disabled:cursor-default",
        running && "agent-card-running",
        failed && "agent-card-failed",
        call.leaving && "translate-y-2",
      )}
    >
      <div
        className={cn(
          "relative flex size-9 shrink-0 items-center justify-center rounded-xl text-white",
          failed ? "bg-destructive" : "agent-badge",
        )}
      >
        <Icon className="size-4.5" />
        {running && <Sparkles className="agent-sparkle absolute -top-1 -right-1 size-3.5 text-amber-300" />}
      </div>
      <div className="min-w-0 flex-1 leading-tight">
        <div className="flex items-baseline gap-2">
          <span className={cn("truncate text-sm font-medium", running && "agent-shimmer")}>{label}</span>
          {where && <span className="text-muted-foreground truncate text-xs">{where}</span>}
        </div>
        <div className={cn("mt-0.5 truncate text-xs", failed ? "text-destructive" : "text-muted-foreground")}>
          {failed && call.error ? (
            call.error
          ) : (
            <>
              <span>{call.client || "Agent"}</span>
              {call.detail && <span className="ml-1.5 font-mono opacity-80">{call.detail}</span>}
            </>
          )}
        </div>
      </div>
      <div className="text-muted-foreground flex shrink-0 items-center gap-1.5 text-xs tabular-nums">
        {running ? <Elapsed start={call.start} /> : call.millis != null && took(call.millis)}
        {running ? (
          <span className="agent-dots" aria-label="进行中">
            <i />
            <i />
            <i />
          </span>
        ) : failed ? (
          <X className="text-destructive size-4" />
        ) : (
          <Check className="text-success animate-in zoom-in-50 size-4 duration-300" />
        )}
      </div>
    </button>
  );
}

/** Elapsed counts the seconds of a running call, from the first one. */
function Elapsed({ start }: { start: number }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 200);
    return () => clearInterval(t);
  }, []);
  const ms = now - start;
  return ms < 1000 ? null : <span>{took(ms)}</span>;
}

function took(ms: number) {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60_000)}m${Math.floor((ms % 60_000) / 1000)}s`;
}
