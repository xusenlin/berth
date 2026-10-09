import { useMemo, useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ExternalLink, Loader2, Play, Plug, RotateCw, Square, TriangleAlert, Unplug } from "lucide-react";
import { toast } from "sonner";
import { AppForm } from "@/components/app-form";
import { Confirm } from "@/components/confirm";
import { Code } from "@/components/copy";
import { DeployPanel } from "@/components/deploy-panel";
import { FilesPanel } from "@/components/files-panel";
import { LogViewer } from "@/components/log-viewer";
import { NoteCard } from "@/components/note-card";
import { PageHeader } from "@/components/page-header";
import { AppState } from "@/components/status";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { message, toastError } from "@/lib/errors";
import { bytes, cpuTime, duration, releaseDate } from "@/lib/format";
import { useNav } from "@/lib/nav";
import { keys, useApp, useConnection, useServerLink, useServers } from "@/lib/queries";
import { Apps, type Action, type Info } from "@/mygo";

export function AppPage({ serverId, name }: { serverId: string; name: string }) {
  const qc = useQueryClient();
  const { route, go } = useNav();
  const [tab, setTab] = useState(route.tab ?? "overview");
  const link = useServerLink(serverId);
  const info = useApp(serverId, name, !link.offline);
  const servers = useServers();
  const server = servers.data?.find((s) => s.server.id === serverId)?.server;

  const act = useMutation({
    mutationFn: (a: Action) => Apps.do(serverId, name, a),
    onSuccess: (_, a) => {
      toast.success({ start: "已启动", stop: "已停止", restart: "已重启" }[a]);
      qc.invalidateQueries({ queryKey: keys.app(serverId, name) });
      qc.invalidateQueries({ queryKey: keys.apps(serverId) });
    },
    onError: (e) => toastError(e),
  });

  const d = info.data;
  const running = d && (d.status.active === "active" || d.status.active === "activating");
  const pending = act.isPending ? act.variables : undefined;

  return (
    <>
      <PageHeader
        actions={
          d &&
          !link.offline && (
            <>
              {d.app.port ? (
                <Button size="sm" variant="ghost" onClick={() => Apps.open(serverId, d.app.port!).catch(toastError)} title={`http://${server?.host}:${d.app.port}`}>
                  <ExternalLink />
                  打开
                </Button>
              ) : null}
              {running ? (
                <Button size="sm" variant="ghost" disabled={act.isPending} onClick={() => act.mutate("stop")}>
                  {pending === "stop" ? <Loader2 className="animate-spin" /> : <Square />}
                  停止
                </Button>
              ) : (
                <Button size="sm" variant="ghost" disabled={act.isPending || !d.current} onClick={() => act.mutate("start")}>
                  {pending === "start" ? <Loader2 className="animate-spin" /> : <Play />}
                  启动
                </Button>
              )}
              <Button size="sm" variant="ghost" disabled={act.isPending || !d.current} onClick={() => act.mutate("restart")}>
                {pending === "restart" ? <Loader2 className="animate-spin" /> : <RotateCw />}
                重启
              </Button>
            </>
          )
        }
      >
        <Button variant="ghost" size="icon" className="no-drag -ml-2 size-8" onClick={() => go({ server: serverId })} title="返回服务器">
          <ChevronLeft />
        </Button>
        <span className="text-muted-foreground truncate text-sm">{server?.name}</span>
        <span className="text-muted-foreground">/</span>
        <span className="truncate font-medium">{name}</span>
        {d && <AppState status={d.status} current={d.current} className="ml-1 text-xs" />}
      </PageHeader>

      {link.offline ? (
        <div className="p-5">
          <div className="text-muted-foreground flex flex-col items-center gap-3 rounded-lg border border-dashed py-12 text-sm">
            <Unplug className="size-6" />
            已断开与 {server?.name} 的连接，不会自动重连
            <Button size="sm" disabled={link.connecting} onClick={link.connect}>
              {link.connecting ? <Loader2 className="animate-spin" /> : <Plug />}
              连接
            </Button>
          </div>
        </div>
      ) : info.isError && !d ? (
        <div className="p-5">
          <Alert variant="destructive">
            <TriangleAlert />
            <AlertTitle>无法读取应用</AlertTitle>
            <AlertDescription className="selectable">{message(info.error)}</AlertDescription>
          </Alert>
        </div>
      ) : !d ? (
        <div className="text-muted-foreground flex items-center gap-2 p-5 text-sm">
          <Loader2 className="size-4 animate-spin" />
          加载中…
        </div>
      ) : (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-0">
          <div className="px-5 pt-4">
            <TabsList>
              <TabsTrigger value="overview">概览</TabsTrigger>
              <TabsTrigger value="deploy">发版</TabsTrigger>
              <TabsTrigger value="logs">日志</TabsTrigger>
              <TabsTrigger value="files">文件</TabsTrigger>
              <TabsTrigger value="settings">设置</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto p-5">
            <OverviewTab serverId={serverId} info={d} host={server?.host ?? ""} onDeploy={() => setTab("deploy")} />
          </TabsContent>
          <TabsContent value="deploy" className="min-h-0 flex-1 overflow-y-auto p-5">
            <DeployPanel serverId={serverId} info={d} />
          </TabsContent>
          <TabsContent value="logs" className="min-h-0 flex-1 p-5">
            <LogViewer serverId={serverId} name={name} />
          </TabsContent>
          <TabsContent value="files" className="min-h-0 flex-1 p-5">
            <FilesPanel serverId={serverId} info={d} />
          </TabsContent>
          <TabsContent value="settings" className="min-h-0 flex-1 overflow-y-auto p-5">
            <SettingsTab serverId={serverId} info={d} />
          </TabsContent>
        </Tabs>
      )}
    </>
  );
}

function Item({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-muted-foreground mb-1 text-xs">{label}</div>
      <div className="truncate text-sm">{children}</div>
    </div>
  );
}

function OverviewTab({ serverId, info, host, onDeploy }: { serverId: string; info: Info; host: string; onDeploy: () => void }) {
  const qc = useQueryClient();
  const conn = useConnection(serverId);
  const env = conn.data?.env;
  const s = info.status;
  const a = info.app;
  const active = s.active === "active";
  const dir = `${env?.home ?? "~"}/senlinApps/${a.name}`;
  const unit = `senlin-${a.name}.service`;
  const ctl = env?.mode === "user" ? "systemctl --user" : "systemctl";
  const journal = env?.mode === "user" ? "journalctl --user" : "journalctl";

  const autostart = useMutation({
    mutationFn: (on: boolean) => Apps.setAutostart(serverId, a.name, on),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.app(serverId, a.name) }),
    onError: (e) => toastError(e),
  });

  return (
    <div className="space-y-5">
      {info.error && (
        <Alert variant="destructive">
          <TriangleAlert />
          <AlertTitle>app.json 有问题</AlertTitle>
          <AlertDescription className="selectable">{info.error}</AlertDescription>
        </Alert>
      )}
      {!info.current && (
        <Alert>
          <TriangleAlert />
          <AlertTitle>还没有发布版本</AlertTitle>
          <AlertDescription>
            <span>
              上传程序后才能启动。
              <Button variant="link" className="h-auto p-0" onClick={onDeploy}>
                去发版
              </Button>
            </span>
          </AlertDescription>
        </Alert>
      )}
      {s.active === "failed" && (
        <Alert variant="destructive">
          <TriangleAlert />
          <AlertTitle>进程已失败退出</AlertTitle>
          <AlertDescription>查看"日志"了解原因。</AlertDescription>
        </Alert>
      )}
      <NoteCard serverId={serverId} name={a.name} note={a.note ?? ""} />
      <div className="bg-card grid grid-cols-2 gap-x-6 gap-y-4 rounded-lg border p-4 md:grid-cols-4">
        <Item label="状态">
          <AppState status={s} current={info.current} />
          {s.sub && s.sub !== "running" && s.sub !== "dead" && <span className="text-muted-foreground ml-1 text-xs">({s.sub})</span>}
        </Item>
        <Item label="当前版本">
          <span className="font-mono text-xs">{info.current ? releaseDate(info.current) : "-"}</span>
        </Item>
        <Item label="运行时长">{active ? duration(s.uptime) : "-"}</Item>
        <Item label="PID">{s.pid || "-"}</Item>
        <Item label="内存">{active ? bytes(s.memory) : "-"}</Item>
        <Item label="CPU 时间">{active ? cpuTime(s.cpu) : "-"}</Item>
        <Item label="自动重启次数">
          <span className={s.restarts > 0 ? "text-warning" : ""}>{s.restarts}</span>
        </Item>
        <Item label="开机自启">
          <Switch checked={s.enabled} disabled={autostart.isPending} onCheckedChange={(v) => autostart.mutate(v)} />
          {env?.mode === "user" && !env.linger && s.enabled && <span className="text-warning ml-2 text-xs">需开启 linger</span>}
        </Item>
      </div>

      <div className="bg-card grid grid-cols-1 gap-y-3 rounded-lg border p-4 text-sm md:grid-cols-[8rem_1fr]">
        <span className="text-muted-foreground">访问地址</span>
        <span>
          {a.port ? (
            <>
              <Code>{`http://${host}:${a.port}`}</Code>
              <span className="text-muted-foreground ml-2 text-xs">外网打不开时，请检查云厂商安全组和防火墙是否放开 {a.port}</span>
            </>
          ) : (
            "-"
          )}
        </span>
        <span className="text-muted-foreground">systemd unit</span>
        <span>
          <Code>{unit}</Code>
        </span>
        <span className="text-muted-foreground">应用目录</span>
        <span>
          <Code>{dir}</Code>
        </span>
        <span className="text-muted-foreground">在服务器上查看</span>
        <span className="flex flex-wrap gap-2">
          <Code>{`${ctl} status ${unit}`}</Code>
          <Code>{`${journal} -u ${unit} -f`}</Code>
        </span>
      </div>
    </div>
  );
}

function SettingsTab({ serverId, info }: { serverId: string; info: Info }) {
  const qc = useQueryClient();
  const { go } = useNav();
  const name = info.app.name;
  // Re-render the form only when the saved settings change, not on every
  // status poll.
  const snapshot = JSON.stringify(info.app);
  const initial = useMemo(() => JSON.parse(snapshot), [snapshot]);
  const running = info.status.active === "active";

  const del = useMutation({
    mutationFn: () => Apps.delete(serverId, name),
    onSuccess: () => {
      toast.success(`已删除 ${name}`);
      qc.invalidateQueries({ queryKey: keys.apps(serverId) });
      go({ server: serverId });
    },
    onError: (e) => toastError(e),
  });

  return (
    <div className="max-w-3xl space-y-8">
      <AppForm
        serverId={serverId}
        initial={initial}
        submitLabel="保存"
        onSubmit={async (a) => {
          await Apps.update(serverId, a);
          await qc.invalidateQueries({ queryKey: keys.app(serverId, name) });
          qc.invalidateQueries({ queryKey: keys.ports(serverId) });
          if (running) {
            toast.success("已保存，重启后生效", {
              action: { label: "立即重启", onClick: () => Apps.do(serverId, name, "restart").then(() => toast.success("已重启"), toastError) },
            });
          } else {
            toast.success("已保存");
          }
        }}
      />
      <section className="border-destructive/40 rounded-lg border p-4">
        <h3 className="text-destructive text-sm font-medium">删除应用</h3>
        <p className="text-muted-foreground mt-1 text-sm">停止服务并删除 unit、所有版本、shared/ 中的数据和备份。无法恢复。</p>
        <Confirm
          trigger={
            <Button variant="destructive" size="sm" className="mt-3" disabled={del.isPending}>
              {del.isPending && <Loader2 className="animate-spin" />}
              删除 {name}
            </Button>
          }
          title={`删除 ${name}？`}
          description="服务器上的程序、数据和备份都会被删除。"
          action="永久删除"
          destructive
          typeToConfirm={name}
          onConfirm={() => del.mutate()}
        />
      </section>
    </div>
  );
}
