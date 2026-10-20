import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Plus, Wand2, X } from "lucide-react";
import { toast } from "sonner";
import { ownerOf } from "@/components/ports-panel";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { message } from "@/lib/errors";
import { useNav } from "@/lib/nav";
import { keys, useConnection, usePorts, usePresets } from "@/lib/queries";
import { cn } from "@/lib/utils";
import { Apps, type App, type HealthType, type Restart } from "@/mygo";

function Field({ label, hint, children, className }: { label: ReactNode; hint?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={className}>
      <Label className="mb-1.5">{label}</Label>
      {children}
      {hint && <div className="text-muted-foreground mt-1 text-xs">{hint}</div>}
    </div>
  );
}

interface EnvRow {
  k: string;
  v: string;
}

const toRows = (env: Record<string, string>): EnvRow[] => Object.entries(env).map(([k, v]) => ({ k, v }));

/** AppForm edits the settings of an app. */
export function AppForm({
  serverId,
  initial,
  creating,
  onSubmit,
  submitLabel,
  footer,
}: {
  serverId: string;
  initial: App;
  creating?: boolean;
  onSubmit: (a: App) => Promise<void>;
  submitLabel: string;
  footer?: (submit: ReactNode) => ReactNode;
}) {
  const [a, setA] = useState<App>(initial);
  const [env, setEnv] = useState<EnvRow[]>(toRows(initial.env));
  const [shared, setShared] = useState(initial.shared.join("\n"));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const conn = useConnection(serverId);
  const ports = usePorts(serverId, conn.isSuccess);

  useEffect(() => {
    setA(initial);
    setEnv(toRows(initial.env));
    setShared(initial.shared.join("\n"));
  }, [initial]);

  const set = <K extends keyof App>(k: K, v: App[K]) => setA((x) => ({ ...x, [k]: v }));

  // Who else holds the chosen port.
  const conflict = useMemo(() => {
    const p = a.port;
    if (!p || !ports.data) return "";
    const l = ports.data.listeners.find((l) => l.port === p && l.proto === "tcp" && l.app !== initial.name);
    if (l) return `端口 ${p} 已被 ${ownerOf(l, ports.data.allProcesses)} 占用`;
    const r = ports.data.reserved.find((r) => r.port === p && r.app !== initial.name);
    if (r) return `端口 ${p} 已分配给应用 ${r.app}`;
    return "";
  }, [a.port, ports.data, initial]);

  const home = conn.data?.env?.home ?? "~";
  const sharedDir = `${home}/senlinApps/${a.name || "<名称>"}/shared`;
  const command = `${home}/senlinApps/${a.name || "<名称>"}/current/${a.entry || "<入口>"} ${a.args
    .replaceAll("{{port}}", a.port ? String(a.port) : "")
    .replaceAll("{{shared}}", sharedDir)}`.trim();

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      const out: App = {
        ...a,
        port: a.port || 0,
        env: Object.fromEntries(env.filter((r) => r.k.trim()).map((r) => [r.k.trim(), r.v])),
        shared: shared
          .split(/[\n,]/)
          .map((s) => s.trim())
          .filter(Boolean),
      };
      await onSubmit(out);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  const submitButton = (
    <Button onClick={submit} disabled={busy || !a.name || !a.entry}>
      {busy && <Loader2 className="animate-spin" />}
      {submitLabel}
    </Button>
  );

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-6 gap-3">
        <Field label="名称" className="col-span-3" hint={creating ? "小写字母、数字、- 和 _；创建后不能修改" : undefined}>
          <Input value={a.name} disabled={!creating} onChange={(e) => set("name", e.target.value.toLowerCase())} placeholder="myapp" />
        </Field>
        <Field label="入口文件" className="col-span-3" hint="发布包中可执行文件的相对路径">
          <Input value={a.entry} onChange={(e) => set("entry", e.target.value)} placeholder="myapp" />
        </Field>
        <Field
          label="启动参数"
          className="col-span-6"
          hint={
            <>
              可用 <code className="font-mono">{"{{port}}"}</code>、<code className="font-mono">{"{{shared}}"}</code>（shared 目录的绝对路径）。实际命令：
              <code className="selectable bg-muted mt-1 block rounded px-1.5 py-1 font-mono break-all">{command}</code>
            </>
          }
        >
          <Input className="font-mono" value={a.args} onChange={(e) => set("args", e.target.value)} placeholder="serve --http=0.0.0.0:{{port}}" />
        </Field>
        <Field
          label="端口"
          className="col-span-2"
          hint={conflict ? <span className="text-destructive">{conflict}</span> : "可留空；程序也能从环境变量 PORT 读取"}
        >
          <div className="flex gap-1.5">
            <Input
              type="number"
              className={cn(conflict && "border-destructive")}
              value={a.port || ""}
              onChange={(e) => set("port", Number(e.target.value) || 0)}
            />
            <Button
              type="button"
              variant="outline"
              size="icon"
              title="自动分配"
              onClick={async () => {
                try {
                  const p = await Apps.suggestPort(serverId);
                  if (p) set("port", p);
                  else toast.error("端口区间内已没有空闲端口");
                } catch (e) {
                  toast.error(message(e));
                }
              }}
            >
              <Wand2 />
            </Button>
          </div>
        </Field>
        <Field label="重启策略" className="col-span-2" hint="进程退出后 systemd 的处理方式">
          <Select value={a.restart} onValueChange={(v) => set("restart", v as Restart)}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="on-failure">异常退出时重启</SelectItem>
              <SelectItem value="always">总是重启</SelectItem>
              <SelectItem value="no">不重启</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label="保留版本数" className="col-span-2" hint="更早的版本发版成功后清理">
          <Input type="number" min={1} value={a.keepReleases} onChange={(e) => set("keepReleases", Number(e.target.value))} />
        </Field>

        <Field label="健康检查" className="col-span-2">
          <Select value={a.health.type} onValueChange={(v) => set("health", { ...a.health, type: v as HealthType })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="process">进程保持运行</SelectItem>
              <SelectItem value="tcp">TCP 端口可连接</SelectItem>
              <SelectItem value="http">HTTP 返回 2xx</SelectItem>
              <SelectItem value="none">不检查</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        {a.health.type === "http" && (
          <Field label="检查路径" className="col-span-2">
            <Input className="font-mono" value={a.health.path ?? ""} onChange={(e) => set("health", { ...a.health, path: e.target.value })} placeholder="/health" />
          </Field>
        )}
        {a.health.type !== "none" && (
          <Field label={a.health.type === "process" ? "保持运行秒数" : "超时秒数"} className="col-span-2">
            <Input type="number" min={1} value={a.health.timeout ?? 10} onChange={(e) => set("health", { ...a.health, timeout: Number(e.target.value) })} />
          </Field>
        )}
        <p className="text-muted-foreground col-span-6 -mt-1 text-xs">
          检查在服务器本地回环进行：通过但外网访问不了时，多半是云厂商安全组或防火墙没放开端口。
        </p>

        <Field
          label="持久路径"
          className="col-span-3"
          hint="每行一个相对路径，保存在 shared/ 中并软链进每个版本，跨版本保留"
        >
          <Textarea className="min-h-20 font-mono" value={shared} onChange={(e) => setShared(e.target.value)} placeholder={"data\nconfig.yaml"} />
        </Field>
        <Field label="环境变量" className="col-span-3">
          <div className="space-y-1.5">
            {env.map((r, i) => (
              <div key={i} className="flex gap-1.5">
                <Input
                  className="font-mono"
                  placeholder="KEY"
                  value={r.k}
                  onChange={(e) => setEnv(env.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)))}
                />
                <Input
                  className="font-mono"
                  placeholder="value"
                  value={r.v}
                  onChange={(e) => setEnv(env.map((x, j) => (j === i ? { ...x, v: e.target.value } : x)))}
                />
                <Button variant="ghost" size="icon" onClick={() => setEnv(env.filter((_, j) => j !== i))}>
                  <X />
                </Button>
              </div>
            ))}
            <Button variant="outline" size="sm" onClick={() => setEnv([...env, { k: "", v: "" }])}>
              <Plus />
              添加变量
            </Button>
          </div>
        </Field>

        {creating && (
          <Field label="备注" className="col-span-6" hint="可选，创建后可在应用概览中随时修改">
            <Textarea className="min-h-16" maxLength={2000} value={a.note ?? ""} onChange={(e) => set("note", e.target.value)} placeholder="用途、负责人、访问方式……" />
          </Field>
        )}
        <label className="col-span-3 flex items-center justify-between gap-3 rounded-lg border p-3">
          <div>
            <div className="text-sm font-medium">开机自启</div>
            <div className="text-muted-foreground text-xs">systemctl enable</div>
          </div>
          <Switch checked={a.autostart} onCheckedChange={(v) => set("autostart", v)} />
        </label>
        <label className="col-span-3 flex items-center justify-between gap-3 rounded-lg border p-3">
          <div>
            <div className="text-sm font-medium">发版前备份 shared/</div>
            <div className="text-muted-foreground text-xs">失败回滚时恢复，保留最近 20 份</div>
          </div>
          <Switch checked={a.backup} onCheckedChange={(v) => set("backup", v)} />
        </label>
      </div>
      {error && <p className="text-destructive selectable text-sm">{error}</p>}
      {footer ? footer(submitButton) : <div className="flex justify-end">{submitButton}</div>}
    </div>
  );
}

/** AppDialog creates an app from a preset. */
export function AppDialog({ serverId, open, onOpenChange }: { serverId: string; open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const { go } = useNav();
  const presets = usePresets();
  const [preset, setPreset] = useState("generic");
  const [initial, setInitial] = useState<App | null>(null);

  useEffect(() => {
    if (!open || !presets.data) return;
    const p = presets.data.find((x) => x.id === preset) ?? presets.data[0]!;
    let cancelled = false;
    Apps.suggestPort(serverId)
      .catch(() => 0)
      .then((port) => {
        if (!cancelled) setInitial((prev) => ({ ...p.app, name: prev?.name ?? "", entry: p.app.entry || prev?.entry || "", port }));
      });
    return () => {
      cancelled = true;
    };
  }, [open, preset, presets.data, serverId]);

  useEffect(() => {
    if (!open) {
      setInitial(null);
      setPreset("generic");
    }
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>新建应用</DialogTitle>
          <DialogDescription>创建目录、app.json 和 systemd unit。创建后在"发版"中上传程序。</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-2">
          {presets.data?.map((p) => (
            <button
              key={p.id}
              onClick={() => setPreset(p.id)}
              className={cn(
                "rounded-lg border p-3 text-left transition-colors",
                preset === p.id ? "border-primary bg-accent" : "hover:bg-accent/50",
              )}
            >
              <div className="text-sm font-medium">{p.name}</div>
              <div className="text-muted-foreground text-xs">{p.description}</div>
            </button>
          ))}
        </div>
        {initial ? (
          <AppForm
            serverId={serverId}
            initial={initial}
            creating
            submitLabel="创建"
            footer={(btn) => (
              <DialogFooter>
                <Button variant="outline" onClick={() => onOpenChange(false)}>
                  取消
                </Button>
                {btn}
              </DialogFooter>
            )}
            onSubmit={async (a) => {
              const created = await Apps.create(serverId, a);
              qc.invalidateQueries({ queryKey: keys.apps(serverId) });
              qc.invalidateQueries({ queryKey: keys.ports(serverId) });
              toast.success(`已创建 ${created.name}`);
              onOpenChange(false);
              go({ server: serverId, app: created.name, tab: "deploy" });
            }}
          />
        ) : (
          <div className="text-muted-foreground flex items-center gap-2 py-8 text-sm">
            <Loader2 className="size-4 animate-spin" />
            加载中…
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
