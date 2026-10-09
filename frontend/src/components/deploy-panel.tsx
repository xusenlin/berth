import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Channel, onFileDrop } from "mygo-runtime";
import { CircleCheck, CircleX, FileArchive, FolderOpen, Loader2, RotateCcw, Upload } from "lucide-react";
import { toast } from "sonner";
import { Confirm } from "@/components/confirm";
import { applyEvent, Steps, type StepView } from "@/components/steps";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { stripAnsi } from "@/lib/ansi";
import { message } from "@/lib/errors";
import { bytes, releaseDate } from "@/lib/format";
import { keys, useReleases } from "@/lib/queries";
import { cn } from "@/lib/utils";
import { Apps, type Event, type Info, type Preflight, type Result, type Step } from "@/mygo";

const deployLabels: Record<Step, string> = {
  check: "检查文件",
  upload: "上传",
  link: "链接持久路径",
  stop: "停止服务",
  backup: "备份 shared/",
  switch: "切换版本",
  start: "启动",
  health: "健康检查",
  rollback: "回滚",
  cleanup: "清理旧版本",
};

function ResultBox({ result, kind }: { result: Result; kind: "deploy" | "rollback" }) {
  const verb = kind === "deploy" ? "发布" : "回滚";
  if (result.ok) {
    return (
      <Alert>
        <CircleCheck className="text-success!" />
        <AlertTitle>{verb}成功</AlertTitle>
        <AlertDescription>当前版本 {releaseDate(result.release)}</AlertDescription>
      </Alert>
    );
  }
  return (
    <Alert variant="destructive">
      <CircleX />
      <AlertTitle>{result.rolledBack ? `${verb}失败，已切回原版本` : `${verb}失败`}</AlertTitle>
      <AlertDescription>
        <p className="selectable break-all">{result.error}</p>
        {result.logs && (
          <pre className="selectable bg-muted text-foreground mt-2 max-h-56 w-full overflow-auto rounded p-2 font-mono text-xs whitespace-pre-wrap">
            {stripAnsi(result.logs)}
          </pre>
        )}
      </AlertDescription>
    </Alert>
  );
}

/** DeployPanel uploads releases and lists them for rollback. */
export function DeployPanel({ serverId, info }: { serverId: string; info: Info }) {
  const qc = useQueryClient();
  const name = info.app.name;
  const releases = useReleases(serverId, name);
  const [file, setFile] = useState("");
  const [preflight, setPreflight] = useState<Preflight | null>(null);
  const [checkError, setCheckError] = useState("");
  const [checking, setChecking] = useState(false);
  const [running, setRunning] = useState<"" | "deploy" | "rollback">("");
  const [steps, setSteps] = useState<StepView[]>([]);
  const [result, setResult] = useState<Result | null>(null);
  const [resultKind, setResultKind] = useState<"deploy" | "rollback">("deploy");
  const runningRef = useRef(running);
  runningRef.current = running;

  const choose = async (path: string) => {
    if (!path || runningRef.current) return;
    setFile(path);
    setPreflight(null);
    setCheckError("");
    setResult(null);
    setSteps([]);
    setChecking(true);
    try {
      setPreflight(await Apps.check(serverId, name, path));
    } catch (e) {
      setCheckError(message(e));
    } finally {
      setChecking(false);
    }
  };

  // Files dropped on the window become the release.
  useEffect(() => onFileDrop(({ paths }) => paths[0] && choose(paths[0])), []); // choose reads the latest state through refs

  const refresh = () => {
    qc.invalidateQueries({ queryKey: keys.app(serverId, name) });
    qc.invalidateQueries({ queryKey: keys.releases(serverId, name) });
    qc.invalidateQueries({ queryKey: keys.apps(serverId) });
  };

  const run = async (kind: "deploy" | "rollback", release?: string) => {
    setRunning(kind);
    setResultKind(kind);
    setSteps([]);
    setResult(null);
    const ch = new Channel<Event>((e) => setSteps((s) => applyEvent(s, e)));
    try {
      const res = kind === "deploy" ? await Apps.deploy(serverId, name, file, ch) : await Apps.rollback(serverId, name, release!, ch);
      setResult(res);
      if (res.ok) {
        toast.success(kind === "deploy" ? `${name} 发布成功` : `${name} 已回滚到 ${releaseDate(release!)}`);
        if (kind === "deploy") {
          setFile("");
          setPreflight(null);
        }
      }
    } catch (e) {
      setResult({ ok: false, release: "", previous: "", rolledBack: false, error: message(e) });
    } finally {
      setRunning("");
      refresh();
    }
  };

  const pkg = preflight?.package;
  return (
    <div className="space-y-6">
      <section className="space-y-3">
        <div
          className={cn(
            "flex flex-col items-center gap-3 rounded-lg border border-dashed p-6 text-center",
            running && "pointer-events-none opacity-60",
          )}
        >
          <Upload className="text-muted-foreground size-6" />
          <div className="text-sm">
            拖入文件，或
            <Button variant="link" className="h-auto px-1" onClick={async () => choose(await Apps.pickRelease())}>
              选择文件
            </Button>
          </div>
          <div className="text-muted-foreground text-xs">
            单个可执行文件，或包含入口 <code className="font-mono">{info.app.entry}</code> 的 zip / tar.gz
          </div>
        </div>

        {file && (
          <div className="bg-muted/40 space-y-2 rounded-lg border p-3 text-sm">
            <div className="flex items-center gap-2">
              <FileArchive className="text-muted-foreground size-4 shrink-0" />
              <span className="selectable truncate font-mono text-xs">{file}</span>
              {checking && <Loader2 className="size-4 animate-spin" />}
            </div>
            {checkError && <p className="text-destructive selectable text-xs">{checkError}</p>}
            {pkg && (
              <div className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
                <span>{pkg.kind === "binary" ? "可执行文件" : pkg.kind}</span>
                <span>{pkg.files} 个文件</span>
                <span>{bytes(pkg.size)}</span>
                <span>
                  {pkg.arch ? `架构 ${pkg.arch}` : "脚本"}
                  {pkg.arch && <span className="text-success"> ✓ 与服务器 {preflight!.serverArch} 一致</span>}
                </span>
                {pkg.stripped && <span>已去掉顶层目录 {pkg.stripped}/</span>}
                <span>服务器剩余 {bytes(preflight!.diskFree)}</span>
              </div>
            )}
            {preflight?.warnings.map((w) => (
              <p key={w} className="text-warning text-xs">
                {w}
              </p>
            ))}
            {preflight && (
              <div className="flex gap-2 pt-1">
                <Button size="sm" disabled={!!running} onClick={() => run("deploy")}>
                  {running === "deploy" ? <Loader2 className="animate-spin" /> : <Upload />}
                  {info.current ? "发布新版本" : "首次发布"}
                </Button>
                <Button size="sm" variant="ghost" disabled={!!running} onClick={() => choose(file)}>
                  重新检查
                </Button>
              </div>
            )}
          </div>
        )}

        {steps.length > 0 && (
          <div className="rounded-lg border p-4">
            <Steps steps={steps} labels={deployLabels} />
          </div>
        )}
        {result && <ResultBox result={result} kind={resultKind} />}
      </section>

      <section>
        <h3 className="mb-2 text-sm font-medium">历史版本</h3>
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>版本</TableHead>
                <TableHead>发布时间</TableHead>
                <TableHead>来源文件</TableHead>
                <TableHead className="text-right">大小</TableHead>
                <TableHead className="w-32" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {releases.data?.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="font-mono text-xs">
                    {r.id}
                    {r.current && (
                      <Badge variant="secondary" className="ml-2">
                        当前
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>{releaseDate(r.id)}</TableCell>
                  <TableCell className="text-muted-foreground max-w-56 truncate" title={r.source}>
                    {r.source || "-"}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{bytes(r.size)}</TableCell>
                  <TableCell className="text-right">
                    {!r.current && (
                      <Confirm
                        trigger={
                          <Button size="sm" variant="ghost" disabled={!!running}>
                            <RotateCcw />
                            回滚到此版本
                          </Button>
                        }
                        title={`回滚 ${name} 到 ${releaseDate(r.id)}？`}
                        description="会停止服务、切换版本并启动，未通过健康检查则自动切回。shared/ 中的数据保持不变，若新版本改过数据格式，旧版本可能无法读取。"
                        action="回滚"
                        onConfirm={() => run("rollback", r.id)}
                      />
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {releases.data?.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground py-6 text-center">
                    还没有发布过版本
                  </TableCell>
                </TableRow>
              )}
              {releases.isLoading && (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground py-6 text-center">
                    <Loader2 className="mx-auto size-4 animate-spin" />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <p className="text-muted-foreground mt-2 flex items-center gap-1 text-xs">
          <FolderOpen className="size-3.5" />
          保留最近 {info.app.keepReleases} 个版本，位于 ~/senlinApps/{name}/releases
        </p>
      </section>
    </div>
  );
}
