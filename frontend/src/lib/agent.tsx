import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Activity as ActivityIcon,
  FileCog,
  FilePen,
  FileText,
  FolderOpen,
  Gauge,
  History,
  LayoutGrid,
  Network,
  PackagePlus,
  Power,
  Rocket,
  ScrollText,
  Server,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  StickyNote,
  Trash2,
  Undo2,
  type LucideIcon,
} from "lucide-react";
import { events, type Activity } from "@/mygo";

/** Call is a tool call of an agent, as shown in the window. */
export interface Call extends Activity {
  /** when it started, in ms */
  start: number;
  /** fading out before it is dropped */
  leaving?: boolean;
}

interface ToolInfo {
  label: string;
  icon: LucideIcon;
  /** writes changes what the pages show: refresh them when the call ends */
  writes?: boolean;
}

const tools: Record<string, ToolInfo> = {
  list_servers: { label: "查看服务器列表", icon: Server },
  server_overview: { label: "查看服务器概况", icon: Gauge },
  list_ports: { label: "查看端口", icon: Network },
  list_apps: { label: "查看应用列表", icon: LayoutGrid },
  get_app: { label: "读取应用设置", icon: FileCog },
  list_releases: { label: "查看发布记录", icon: History },
  query_logs: { label: "查询日志", icon: ScrollText },
  list_shared_files: { label: "浏览数据文件", icon: FolderOpen },
  read_shared_file: { label: "读取文件", icon: FileText },
  write_shared_file: { label: "写入文件", icon: FilePen, writes: true },
  create_app: { label: "创建应用", icon: PackagePlus, writes: true },
  update_app: { label: "修改应用设置", icon: Settings2, writes: true },
  set_note: { label: "更新备注", icon: StickyNote, writes: true },
  app_action: { label: "启停应用", icon: Power, writes: true },
  check_deploy: { label: "检查发布文件", icon: ShieldCheck },
  deploy: { label: "部署新版本", icon: Rocket, writes: true },
  rollback: { label: "回滚版本", icon: Undo2, writes: true },
  delete_app: { label: "删除应用", icon: Trash2, writes: true },
  run_command: { label: "执行只读命令", icon: SquareTerminal },
};

const actions: Record<string, string> = { start: "启动应用", stop: "停止应用", restart: "重启应用" };

export function toolInfo(c: Activity): ToolInfo {
  const t = tools[c.tool] ?? { label: c.tool, icon: ActivityIcon };
  if (c.tool === "app_action" && c.detail && actions[c.detail]) return { ...t, label: actions[c.detail] };
  return t;
}

/** How long an ended call stays on screen. */
const linger = { done: 2400, failed: 6000 } as const;
const fade = 220;

const AgentContext = createContext<Call[]>([]);

/** AgentProvider follows the tool calls of agents connected over MCP. */
export function AgentProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const [calls, setCalls] = useState<Call[]>([]);
  const timers = useRef(new Set<ReturnType<typeof setTimeout>>());

  useEffect(() => {
    const later = (ms: number, fn: () => void) => {
      const t = setTimeout(() => {
        timers.current.delete(t);
        fn();
      }, ms);
      timers.current.add(t);
    };
    const off = events.agentActivity.on((a) => {
      if (a.state === "running") {
        setCalls((cs) => [...cs.slice(-19), { ...a, start: Date.now() }]);
        return;
      }
      setCalls((cs) => cs.map((c) => (c.id === a.id ? { ...c, ...a } : c)));
      if (a.state === "done" && a.server && tools[a.tool]?.writes) {
        for (const k of ["apps", "app", "releases", "ports", "shared"]) qc.invalidateQueries({ queryKey: [k, a.server] });
      }
      later(linger[a.state], () => {
        setCalls((cs) => cs.map((c) => (c.id === a.id ? { ...c, leaving: true } : c)));
        later(fade, () => setCalls((cs) => cs.filter((c) => c.id !== a.id)));
      });
    });
    const pending = timers.current;
    return () => {
      off();
      pending.forEach(clearTimeout);
      pending.clear();
    };
  }, [qc]);

  return <AgentContext.Provider value={calls}>{children}</AgentContext.Provider>;
}

export function useAgentCalls() {
  return useContext(AgentContext);
}
