import { cn } from "@/lib/utils";
import type { AppStatus, Status } from "@/mygo";

export function Dot({ className }: { className?: string }) {
  return <span className={cn("inline-block size-2 shrink-0 rounded-full", className)} />;
}

const serverColors: Record<Status, string> = {
  connected: "bg-success",
  connecting: "bg-warning animate-pulse",
  error: "bg-destructive",
  disconnected: "bg-muted-foreground/40",
};

export const serverLabels: Record<Status, string> = {
  connected: "已连接",
  connecting: "连接中",
  error: "连接失败",
  disconnected: "未连接",
};

export function ServerDot({ status, className }: { status: Status; className?: string }) {
  return <Dot className={cn(serverColors[status], className)} />;
}

/** appState sums up an app's unit for display. */
export function appState(s: AppStatus, current?: string): { label: string; color: string } {
  if (!current) return { label: "未发版", color: "bg-muted-foreground/40" };
  if (!s.installed) return { label: "未安装", color: "bg-destructive" };
  switch (s.active) {
    case "active":
      return { label: "运行中", color: "bg-success" };
    case "activating":
      return s.sub === "auto-restart"
        ? { label: "崩溃重启中", color: "bg-destructive animate-pulse" }
        : { label: "启动中", color: "bg-warning animate-pulse" };
    case "deactivating":
      return { label: "停止中", color: "bg-warning animate-pulse" };
    case "failed":
      return { label: "已失败", color: "bg-destructive" };
    default:
      return { label: "已停止", color: "bg-muted-foreground/40" };
  }
}

export function AppState({ status, current, className }: { status: AppStatus; current?: string; className?: string }) {
  const st = appState(status, current);
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <Dot className={st.color} />
      {st.label}
    </span>
  );
}
