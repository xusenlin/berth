import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Channel } from "mygo-runtime";
import { CircleCheck, FolderOpen, Loader2, ShieldAlert, TriangleAlert } from "lucide-react";
import { applyEvent, Steps, type StepView } from "@/components/steps";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { message } from "@/lib/errors";
import { bytes, releaseDate } from "@/lib/format";
import { Apps, type App, type Progress, type Saved } from "@/mygo";

export const backupLabels: Record<string, string> = {
  prepare: "准备",
  pause: "暂停应用",
  archive: "打包并下载",
  logs: "导出日志",
  resume: "恢复运行",
  save: "保存文件",
  check: "检查备份",
  upload: "上传",
  link: "链接持久路径",
  switch: "切换版本",
  configure: "写入配置",
  start: "启动",
  health: "健康检查",
};

/** usesDatabase guesses whether an app keeps a database in shared/. */
function usesDatabase(a: App) {
  return a.preset === "pocketbase" || a.shared.some((p) => /pb_data|\.db$|\.sqlite|data/i.test(p));
}

function Option({ title, hint, children }: { title: string; hint: string; children: React.ReactNode }) {
  return (
    <label className="flex items-start justify-between gap-4 rounded-lg border p-3">
      <div>
        <div className="text-sm font-medium">{title}</div>
        <div className="text-muted-foreground mt-0.5 text-xs">{hint}</div>
      </div>
      {children}
    </label>
  );
}

/** BackupDialog downloads a backup of an app to this computer. */
export function BackupDialog({ serverId, app, open, onOpenChange }: { serverId: string; app: App; open: boolean; onOpenChange: (o: boolean) => void }) {
  const est = useQuery({
    queryKey: ["backup-estimate", serverId, app.name],
    queryFn: () => Apps.backupEstimate(serverId, app.name),
    enabled: open,
    staleTime: 0,
  });
  const [all, setAll] = useState(false);
  const [logs, setLogs] = useState(false);
  const [days, setDays] = useState(7);
  const [pause, setPause] = useState(usesDatabase(app));
  const [running, setRunning] = useState(false);
  const [steps, setSteps] = useState<StepView[]>([]);
  const [saved, setSaved] = useState<Saved | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setSteps([]);
      setSaved(null);
      setError("");
      setPause(usesDatabase(app));
    }
  }, [open, app]);

  const e = est.data;
  const size = e ? e.sharedSize + (all ? e.releasesSize : e.releaseSize) : 0;

  const start = async () => {
    const dest = await Apps.pickBackupDest(serverId, app.name);
    if (!dest) return;
    setRunning(true);
    setSteps([]);
    setSaved(null);
    setError("");
    const ch = new Channel<Progress>((ev) => setSteps((s) => applyEvent(s, ev)));
    try {
      setSaved(await Apps.backup(serverId, app.name, dest, { allReleases: all, logDays: logs ? days : 0, pause }, ch));
    } catch (err) {
      setError(message(err));
    } finally {
      setRunning(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !running && onOpenChange(o)}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>下载 {app.name} 的备份</DialogTitle>
          <DialogDescription>打包 app.json、shared/ 中的数据和当前版本的程序，可在任意服务器上恢复。</DialogDescription>
        </DialogHeader>

        {!steps.length ? (
          <div className="space-y-3">
            <div className="bg-muted/40 grid grid-cols-3 gap-3 rounded-lg border p-3 text-sm">
              <div>
                <div className="text-muted-foreground text-xs">shared/ 数据</div>
                <div className="tabular-nums">{e ? bytes(e.sharedSize) : "…"}</div>
              </div>
              <div>
                <div className="text-muted-foreground text-xs">当前版本</div>
                <div className="truncate tabular-nums">{e ? (e.release ? `${releaseDate(e.release).slice(5, 16)} · ${bytes(e.releaseSize)}` : "无") : "…"}</div>
              </div>
              <div>
                <div className="text-muted-foreground text-xs">预计大小（压缩前）</div>
                <div className="tabular-nums">{e ? bytes(size) : "…"}</div>
              </div>
            </div>
            {est.isError && <p className="text-destructive text-xs">{message(est.error)}</p>}

            <Option title="备份时暂停应用" hint={`停止服务、打包后立即启动，保证数据库文件一致；期间应用不可访问${e && !e.running ? "（应用当前未运行）" : ""}`}>
              <Switch checked={pause} onCheckedChange={setPause} />
            </Option>
            <Option title="包含全部历史版本" hint={e ? `${e.releases} 个版本，共 ${bytes(e.releasesSize)}；默认只含当前版本` : "默认只含当前版本"}>
              <Switch checked={all} onCheckedChange={setAll} />
            </Option>
            <Option title="导出日志" hint="附带最近几天的 journal 日志">
              <div className="flex items-center gap-2">
                {logs && (
                  <span className="flex items-center gap-1 text-xs">
                    最近
                    <Input type="number" min={1} max={365} className="h-7 w-14 px-2" value={days} onChange={(ev) => setDays(Math.max(1, Number(ev.target.value) || 1))} />天
                  </span>
                )}
                <Switch checked={logs} onCheckedChange={setLogs} />
              </div>
            </Option>
            <p className="text-muted-foreground flex items-start gap-1.5 text-xs">
              <ShieldAlert className="mt-0.5 size-3.5 shrink-0" />
              备份包含环境变量和 shared/ 中的全部数据，可能有密钥或用户数据，请妥善保管。
            </p>
          </div>
        ) : (
          <div className="space-y-3">
            <div className="rounded-lg border p-4">
              <Steps steps={steps} labels={backupLabels} />
            </div>
            {saved && (
              <Alert>
                <CircleCheck className="text-success!" />
                <AlertTitle>备份完成 · {bytes(saved.size)}</AlertTitle>
                <AlertDescription>
                  <span className="selectable font-mono text-xs break-all">{saved.path}</span>
                  {saved.warning && <span className="text-warning text-xs">{saved.warning}</span>}
                </AlertDescription>
              </Alert>
            )}
            {error && (
              <Alert variant="destructive">
                <TriangleAlert />
                <AlertTitle>备份失败</AlertTitle>
                <AlertDescription className="selectable break-all">{error}</AlertDescription>
              </Alert>
            )}
          </div>
        )}

        <DialogFooter>
          {saved ? (
            <>
              <Button variant="outline" onClick={() => Apps.reveal(saved.path)}>
                <FolderOpen />
                在文件夹中显示
              </Button>
              <Button onClick={() => onOpenChange(false)}>完成</Button>
            </>
          ) : (
            <>
              <Button variant="outline" disabled={running} onClick={() => onOpenChange(false)}>
                取消
              </Button>
              <Button disabled={running || !e} onClick={start}>
                {running && <Loader2 className="animate-spin" />}
                {error ? "重试" : "选择位置并备份"}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
