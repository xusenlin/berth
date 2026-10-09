import { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Channel } from "mygo-runtime";
import { ArchiveRestore, CircleCheck, CircleX, FileArchive, Loader2, TriangleAlert } from "lucide-react";
import { backupLabels } from "@/components/backup-dialog";
import { applyEvent, Steps, type StepView } from "@/components/steps";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { stripAnsi } from "@/lib/ansi";
import { message } from "@/lib/errors";
import { bytes, releaseDate } from "@/lib/format";
import { useNav } from "@/lib/nav";
import { keys, useApps } from "@/lib/queries";
import { cn } from "@/lib/utils";
import { Apps, type Preview, type Progress, type RestoreResult } from "@/mygo";

/** RestoreDialog restores a backup onto a server, as a new app or over one. */
export function RestoreDialog({ serverId, open, onOpenChange }: { serverId: string; open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const { go } = useNav();
  const apps = useApps(serverId, open);
  const [file, setFile] = useState("");
  const [preview, setPreview] = useState<Preview | null>(null);
  const [inspecting, setInspecting] = useState(false);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [port, setPort] = useState(0);
  const [overwrite, setOverwrite] = useState(false);
  const [startAfter, setStartAfter] = useState(true);
  const [running, setRunning] = useState(false);
  const [steps, setSteps] = useState<StepView[]>([]);
  const [result, setResult] = useState<RestoreResult | null>(null);

  useEffect(() => {
    if (!open) return;
    setFile("");
    setPreview(null);
    setError("");
    setSteps([]);
    setResult(null);
    setOverwrite(false);
  }, [open]);

  const choose = async () => {
    const path = await Apps.pickBackupFile();
    if (!path) return;
    setFile(path);
    setPreview(null);
    setError("");
    setInspecting(true);
    try {
      const pv = await Apps.inspectBackup(serverId, path);
      setPreview(pv);
      if (pv) {
        setName(pv.app.name);
        setPort(pv.port);
        setStartAfter(!!pv.info?.manifest.release);
      }
    } catch (e) {
      setError(message(e));
    } finally {
      setInspecting(false);
    }
  };

  const exists = useMemo(() => !!apps.data?.some((a) => a.app.name === name), [apps.data, name]);
  useEffect(() => setOverwrite(false), [name]);
  const man = preview?.info?.manifest;

  const run = async () => {
    setRunning(true);
    setSteps([]);
    setResult(null);
    const ch = new Channel<Progress>((ev) => setSteps((s) => applyEvent(s, ev)));
    try {
      const res = await Apps.restore(serverId, file, { name, port, overwrite, start: startAfter }, ch);
      setResult(res);
    } catch (e) {
      setResult({ ok: false, name, release: "", started: false, error: message(e) });
    } finally {
      setRunning(false);
      qc.invalidateQueries({ queryKey: keys.apps(serverId) });
      qc.invalidateQueries({ queryKey: keys.ports(serverId) });
    }
  };

  const canRun = !!preview && preview.archOK && /^[a-z0-9][a-z0-9_-]{0,39}$/.test(name) && (!exists || overwrite) && !running;

  return (
    <Dialog open={open} onOpenChange={(o) => !running && onOpenChange(o)}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>从备份恢复</DialogTitle>
          <DialogDescription>把 Berth 备份恢复到这台服务器，可以来自任意服务器。</DialogDescription>
        </DialogHeader>

        {!steps.length && (
          <div className="space-y-4">
            <div className="flex items-center gap-2">
              <Button variant="outline" onClick={choose} disabled={inspecting}>
                {inspecting ? <Loader2 className="animate-spin" /> : <FileArchive />}
                {file ? "换一个备份" : "选择备份文件"}
              </Button>
              {file && <span className="text-muted-foreground selectable truncate font-mono text-xs">{file}</span>}
            </div>
            {error && <p className="text-destructive selectable text-sm">{error}</p>}

            {preview && man && (
              <>
                <div className="bg-muted/40 grid grid-cols-2 gap-x-4 gap-y-2 rounded-lg border p-3 text-sm">
                  <div>
                    <div className="text-muted-foreground text-xs">应用</div>
                    {man.app}
                  </div>
                  <div>
                    <div className="text-muted-foreground text-xs">来源服务器</div>
                    <div className="truncate">
                      {man.server.name} <span className="text-muted-foreground text-xs">{man.server.user}@{man.server.host}</span>
                    </div>
                  </div>
                  <div>
                    <div className="text-muted-foreground text-xs">备份时间</div>
                    {new Date(man.createdAt).toLocaleString()}
                  </div>
                  <div>
                    <div className="text-muted-foreground text-xs">程序版本</div>
                    {man.release ? releaseDate(man.release) : "无"}
                    {man.releases.length > 1 && <span className="text-muted-foreground text-xs">（共 {man.releases.length} 个版本）</span>}
                  </div>
                  <div>
                    <div className="text-muted-foreground text-xs">数据</div>
                    {preview.info!.files} 个文件，{bytes(preview.info!.dataSize)}
                  </div>
                  <div>
                    <div className="text-muted-foreground text-xs">一致性</div>
                    {man.paused ? "备份时已暂停应用" : "备份时应用在运行"}
                  </div>
                </div>
                {!preview.archOK && (
                  <Alert variant="destructive">
                    <TriangleAlert />
                    <AlertTitle>架构不匹配</AlertTitle>
                    <AlertDescription>
                      备份中的程序是 {man.server.arch}，这台服务器是 {preview.serverArch}，无法运行。
                    </AlertDescription>
                  </Alert>
                )}
                {preview.warnings.map((w) => (
                  <p key={w} className="text-warning flex items-start gap-1.5 text-xs">
                    <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                    {w}
                  </p>
                ))}

                <div className="grid grid-cols-6 gap-3">
                  <div className="col-span-4">
                    <Label className="mb-1.5">恢复为应用</Label>
                    <Input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} className={cn(exists && !overwrite && "border-destructive")} />
                  </div>
                  <div className="col-span-2">
                    <Label className="mb-1.5">端口</Label>
                    <Input type="number" value={port || ""} onChange={(e) => setPort(Number(e.target.value) || 0)} />
                  </div>
                  {preview.portTaken && preview.app.port ? (
                    <p className="text-muted-foreground col-span-6 -mt-1 text-xs">
                      原端口 {preview.app.port} 在这台服务器上已被{preview.portTaken}占用，已改为 {preview.port || "空"}。启动参数中的 {"{{port}}"} 会随之变化。
                    </p>
                  ) : null}
                </div>

                {exists && (
                  <label className="border-destructive/50 bg-destructive/5 flex items-start justify-between gap-4 rounded-lg border p-3">
                    <div>
                      <div className="text-destructive text-sm font-medium">覆盖已有应用 {name}</div>
                      <div className="text-muted-foreground mt-0.5 text-xs">
                        会停止它，用备份替换配置、数据和程序。原 shared/ 不会删除，而是移到 backups/ 下保留。
                      </div>
                    </div>
                    <Switch checked={overwrite} onCheckedChange={setOverwrite} />
                  </label>
                )}
                <label className="flex items-center justify-between gap-4 rounded-lg border p-3">
                  <div className="text-sm font-medium">恢复后启动并做健康检查</div>
                  <Switch checked={startAfter} disabled={!man.release} onCheckedChange={setStartAfter} />
                </label>
              </>
            )}
          </div>
        )}

        {steps.length > 0 && (
          <div className="space-y-3">
            <div className="rounded-lg border p-4">
              <Steps steps={steps} labels={backupLabels} />
            </div>
            {result &&
              (result.ok ? (
                <Alert>
                  <CircleCheck className="text-success!" />
                  <AlertTitle>已恢复为 {result.name}</AlertTitle>
                  <AlertDescription>
                    {result.started ? "已启动并通过健康检查。" : "未启动。"}
                    {result.kept && <span className="selectable text-xs">原 shared/ 保留在 {result.kept}</span>}
                  </AlertDescription>
                </Alert>
              ) : (
                <Alert variant="destructive">
                  <CircleX />
                  <AlertTitle>恢复失败</AlertTitle>
                  <AlertDescription>
                    <p className="selectable break-all">{result.error}</p>
                    {result.kept && <p className="selectable text-xs">原 shared/ 保留在 {result.kept}</p>}
                    {result.logs && (
                      <pre className="selectable bg-muted text-foreground mt-2 max-h-48 w-full overflow-auto rounded p-2 font-mono text-xs whitespace-pre-wrap">
                        {stripAnsi(result.logs)}
                      </pre>
                    )}
                  </AlertDescription>
                </Alert>
              ))}
          </div>
        )}

        <DialogFooter>
          {result ? (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                关闭
              </Button>
              <Button
                onClick={() => {
                  onOpenChange(false);
                  go({ server: serverId, app: result.name });
                }}
              >
                打开应用
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" disabled={running} onClick={() => onOpenChange(false)}>
                取消
              </Button>
              <Button variant={overwrite ? "destructive" : "default"} disabled={!canRun} onClick={run}>
                {running ? <Loader2 className="animate-spin" /> : <ArchiveRestore />}
                {overwrite ? "覆盖并恢复" : "恢复"}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
