import { useState } from "react";
import { Check, Copy, RefreshCw } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { toastError } from "@/lib/errors";
import { cn } from "@/lib/utils";
import { MCP, type MCPStatus } from "@/mygo";

const key = ["mcp"] as const;

/** Snippet is a block of configuration with a copy button. */
function Snippet({ title, text }: { title: string; text: string }) {
  const [done, setDone] = useState(false);
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <div className="text-sm font-medium">{title}</div>
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs"
          onClick={() =>
            navigator.clipboard.writeText(text).then(() => {
              setDone(true);
              setTimeout(() => setDone(false), 1200);
            })
          }
        >
          {done ? <Check className="size-3" /> : <Copy className="size-3" />}
          {done ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="bg-muted selectable overflow-x-auto rounded-lg p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">
        {text}
      </pre>
    </div>
  );
}

function claudeCommand(s: MCPStatus) {
  // user scope: every project can deploy, and the token stays out of the
  // repository (project scope writes .mcp.json).
  return `claude mcp add --scope user --transport http berth ${s.url} --header "Authorization: Bearer ${s.token}"`;
}

function jsonConfig(s: MCPStatus) {
  const cfg = { mcpServers: { berth: { type: "http", url: s.url, headers: { Authorization: `Bearer ${s.token}` } } } };
  return JSON.stringify(cfg, null, 2);
}

export function MCPDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: key, queryFn: () => MCP.status(), enabled: open });
  const toggle = useMutation({
    mutationFn: (on: boolean) => MCP.setEnabled(on),
    onSuccess: (s) => qc.setQueryData(key, s),
    onError: (e) => toastError(e),
  });
  const commands = useMutation({
    mutationFn: (on: boolean) => MCP.setCommands(on),
    onSuccess: (s) => qc.setQueryData(key, s),
    onError: (e) => toastError(e),
  });
  const reset = useMutation({
    mutationFn: () => MCP.resetToken(),
    onSuccess: (s) => {
      qc.setQueryData(key, s);
      toast.success("已生成新 token", { description: "旧 token 已失效，请更新 agent 里的配置" });
    },
    onError: (e) => toastError(e),
  });
  const s = status.data;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Agent 接入（MCP）</DialogTitle>
          <DialogDescription>
            让 Claude Code、Cursor 等 agent 查看和管理应用：新建、发版、回滚、启停、看日志、改配置。只监听本机，Berth 打开时可用。
          </DialogDescription>
        </DialogHeader>

        <label className="flex items-center justify-between gap-3 rounded-lg border p-3">
          <div className="min-w-0">
            <div className="text-sm font-medium">启用 MCP 服务</div>
            <div className={cn("truncate text-xs", s?.error ? "text-destructive" : "text-muted-foreground")}>
              {s?.error || (s?.running ? `运行中 · ${s.url}` : "未启用")}
            </div>
          </div>
          <Switch
            checked={!!s?.enabled}
            disabled={!s || toggle.isPending}
            onCheckedChange={(on) => toggle.mutate(on)}
          />
        </label>

        {s?.enabled && s.token && (
          <>
            <label className="flex items-center justify-between gap-3 rounded-lg border p-3">
              <div className="min-w-0">
                <div className="text-sm font-medium">允许执行只读命令</div>
                <div className="text-muted-foreground text-xs">
                  agent 可在服务器上运行 cat、grep、tail、journalctl、ps、ss、sqlite3（只读打开）等命令排查问题，可用管道组合。命令先经白名单校验，改不了服务器，但能读到登录用户可读的所有文件。
                </div>
              </div>
              <Switch
                checked={s.commands}
                disabled={commands.isPending}
                onCheckedChange={(on) => commands.mutate(on)}
              />
            </label>
            <Snippet title="Claude Code" text={claudeCommand(s)} />
            <Snippet title="其他客户端（JSON 配置）" text={jsonConfig(s)} />
            <div className="text-muted-foreground flex items-center justify-between gap-3 text-xs">
              <span>agent 不能添加或删除服务器，也拿不到密码和私钥。token 泄露时重新生成即可。</span>
              <Button variant="outline" size="sm" disabled={reset.isPending} onClick={() => reset.mutate()}>
                <RefreshCw />
                重新生成 token
              </Button>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
