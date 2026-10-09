import { useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Cpu, HardDrive, Loader2, MemoryStick, MoreHorizontal, Pencil, Plus, Plug, RefreshCw, Server, Trash2, TriangleAlert, Unplug } from "lucide-react";
import { toast } from "sonner";
import { AppDialog } from "@/components/app-form";
import { Confirm } from "@/components/confirm";
import { Code } from "@/components/copy";
import { PageHeader } from "@/components/page-header";
import { PortsPanel } from "@/components/ports-panel";
import { ServerDialog } from "@/components/server-dialog";
import { AppState, ServerDot, serverLabels } from "@/components/status";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { message, toastError } from "@/lib/errors";
import { bytes, duration, percent, release } from "@/lib/format";
import { useNav } from "@/lib/nav";
import { keys, useApps, useOverview, usePorts, useServerLink } from "@/lib/queries";
import { cn } from "@/lib/utils";
import { Servers, type Env, type Overview } from "@/mygo";

export function ServerPage({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const { go } = useNav();
  const { view, offline, conn, connecting, ready, env, connect, disconnect } = useServerLink(serverId);
  const overview = useOverview(serverId, ready);
  const apps = useApps(serverId, ready && !!env?.systemd);
  const ports = usePorts(serverId, ready);
  const [editing, setEditing] = useState(false);
  const [creating, setCreating] = useState(false);
  const [tab, setTab] = useState("apps");

  const del = useMutation({
    mutationFn: () => Servers.delete(serverId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.servers });
      go({});
      toast.success("已删除服务器，服务器上的应用不受影响");
    },
    onError: (e) => toastError(e),
  });

  if (!view) return null;
  const s = view.server;
  const status = connecting ? "connecting" : offline ? "disconnected" : conn.isError ? "error" : view.status;

  return (
    <>
      <PageHeader
        actions={
          <>
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              title="刷新"
              onClick={() => {
                if (!ready) return connect();
                qc.invalidateQueries({ queryKey: keys.overview(serverId) });
                qc.invalidateQueries({ queryKey: keys.apps(serverId) });
                qc.invalidateQueries({ queryKey: keys.ports(serverId) });
              }}
            >
              <RefreshCw className={cn(connecting && "animate-spin")} />
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="size-8">
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => setEditing(true)}>
                  <Pencil />
                  编辑连接
                </DropdownMenuItem>
                {!offline && view.status === "connected" ? (
                  <DropdownMenuItem onClick={() => disconnect().then(() => toast.success(`已断开 ${s.name}`), toastError)}>
                    <Unplug />
                    断开连接
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem onClick={connect}>
                    <Plug />
                    连接
                  </DropdownMenuItem>
                )}
                <DropdownMenuSeparator />
                <Confirm
                  trigger={
                    <DropdownMenuItem variant="destructive" onSelect={(e) => e.preventDefault()}>
                      <Trash2 />
                      删除服务器
                    </DropdownMenuItem>
                  }
                  title={`删除 ${s.name}？`}
                  description="只从 Berth 中移除这台服务器和保存的密码。服务器上的应用会继续运行，重新添加后即可恢复管理。"
                  action="删除"
                  destructive
                  onConfirm={() => del.mutate()}
                />
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      >
        <ServerDot status={status} />
        <span className="truncate font-medium">{s.name}</span>
        <span className="text-muted-foreground truncate text-sm">
          {s.user}@{s.host}
          {s.port !== 22 && `:${s.port}`}
        </span>
        <span className="text-muted-foreground text-xs">{offline && !connecting ? "已断开" : serverLabels[status]}</span>
      </PageHeader>

      <div className="flex-1 space-y-5 overflow-y-auto p-5">
        {offline && !connecting && (
          <div className="text-muted-foreground flex flex-col items-center gap-3 rounded-lg border border-dashed py-12 text-sm">
            <Unplug className="size-6" />
            已断开与 {s.name} 的连接，不会自动重连
            <Button size="sm" onClick={connect}>
              <Plug />
              连接
            </Button>
          </div>
        )}
        {!offline && conn.isError && (
          <Alert variant="destructive">
            <TriangleAlert />
            <AlertTitle>无法连接</AlertTitle>
            <AlertDescription>
              <p className="selectable break-all">{message(conn.error)}</p>
              <div className="mt-2 flex gap-2">
                <Button size="sm" variant="outline" onClick={connect}>
                  重试
                </Button>
                <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
                  编辑连接
                </Button>
              </div>
            </AlertDescription>
          </Alert>
        )}
        {!offline && conn.isLoading && (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            正在连接 {s.host}…
          </div>
        )}
        {env && <EnvBanners serverId={serverId} env={env} />}
        {ready && <OverviewCards serverId={serverId} data={overview.data} />}

        {ready && env?.systemd && (
          <Tabs value={tab} onValueChange={setTab}>
            <div className="flex items-center justify-between">
              <TabsList>
                <TabsTrigger value="apps">应用 {apps.data && <span className="text-muted-foreground">{apps.data.length}</span>}</TabsTrigger>
                <TabsTrigger value="ports">
                  端口 {ports.data && <span className="text-muted-foreground">{ports.data.listeners.filter((l) => l.proto === "tcp").length}</span>}
                </TabsTrigger>
              </TabsList>
              {tab === "apps" && (
                <Button size="sm" onClick={() => setCreating(true)}>
                  <Plus />
                  新建应用
                </Button>
              )}
            </div>
            <TabsContent value="apps" className="mt-3">
              <AppsTable serverId={serverId} apps={apps} onCreate={() => setCreating(true)} />
            </TabsContent>
            <TabsContent value="ports" className="mt-3">
              <PortsPanel serverId={serverId} ports={ports.data} />
            </TabsContent>
          </Tabs>
        )}
      </div>
      <ServerDialog open={editing} onOpenChange={setEditing} edit={view} />
      <AppDialog serverId={serverId} open={creating} onOpenChange={setCreating} />
    </>
  );
}

function EnvBanners({ serverId, env }: { serverId: string; env: Env }) {
  const qc = useQueryClient();
  const [lingerError, setLingerError] = useState("");
  const linger = useMutation({
    mutationFn: () => Servers.enableLinger(serverId),
    onSuccess: () => {
      toast.success("已开启 linger，应用将随服务器开机启动");
      qc.invalidateQueries({ queryKey: keys.connect(serverId) });
    },
    onError: (e) => setLingerError(message(e)),
  });
  if (!env.systemd) {
    return (
      <Alert variant="destructive">
        <TriangleAlert />
        <AlertTitle>没有检测到 systemd</AlertTitle>
        <AlertDescription>Berth 依靠服务器自带的 systemd 运行应用，这台服务器上无法管理应用。</AlertDescription>
      </Alert>
    );
  }
  if (env.mode === "user" && !env.linger) {
    return (
      <Alert>
        <TriangleAlert className="text-warning!" />
        <AlertTitle>服务器重启后应用不会自动启动</AlertTitle>
        <AlertDescription>
          <p>
            以普通用户 {env.user} 登录时，应用运行在用户级 systemd 中，需要开启 linger 才能开机启动。
            {env.sudo === "" && " 该用户没有 sudo，需要管理员执行下面的命令。"}
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {env.sudo !== "" && (
              <Button size="sm" variant="outline" disabled={linger.isPending} onClick={() => linger.mutate()}>
                {linger.isPending && <Loader2 className="animate-spin" />}
                开启 linger
              </Button>
            )}
            <Code>{`sudo loginctl enable-linger ${env.user}`}</Code>
          </div>
          {lingerError && <p className="text-destructive selectable mt-2 text-xs">{lingerError}</p>}
        </AlertDescription>
      </Alert>
    );
  }
  return null;
}

function Meter({ value, warnAt = 80 }: { value: number; warnAt?: number }) {
  return (
    <Progress
      value={value}
      className={cn("h-1.5", value >= 90 ? "[&>*]:bg-destructive" : value >= warnAt ? "[&>*]:bg-warning" : "")}
    />
  );
}

function Card({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className="bg-card min-w-0 rounded-lg border p-3.5">
      <div className="text-muted-foreground mb-2 flex items-center gap-1.5 text-xs [&>svg]:size-3.5">
        {icon}
        {title}
      </div>
      {children}
    </div>
  );
}

function OverviewCards({ serverId, data }: { serverId: string; data: Overview | undefined }) {
  const [usage, setUsage] = useState<number | null>(null);
  const [measuring, setMeasuring] = useState(false);
  if (!data) {
    return (
      <div className="grid grid-cols-4 gap-3">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-[92px] rounded-lg" />
        ))}
      </div>
    );
  }
  const disk = data.appsDisk ?? data.disks[0];
  const cpu = data.cpuPercent;
  const mem = percent(data.memUsed, data.memTotal);
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <Card icon={<Server />} title="系统">
        <div className="truncate text-sm font-medium" title={data.os}>
          {data.os || "Linux"}
        </div>
        <div className="text-muted-foreground mt-1 truncate text-xs" title={data.kernel}>
          {data.arch} · {data.kernel.replace(/^Linux /, "")}
        </div>
        <div className="text-muted-foreground mt-0.5 text-xs">已运行 {duration(data.uptime)}</div>
      </Card>
      <Card icon={<Cpu />} title={`CPU · ${data.cpus} 核`}>
        <div className="mb-2 text-lg font-semibold tabular-nums">{cpu.toFixed(0)}%</div>
        <Meter value={cpu} />
        <div className="text-muted-foreground mt-1.5 text-xs tabular-nums">负载 {data.load.map((l) => l.toFixed(2)).join(" ")}</div>
      </Card>
      <Card icon={<MemoryStick />} title="内存">
        <div className="mb-2 text-lg font-semibold tabular-nums">
          {bytes(data.memUsed)} <span className="text-muted-foreground text-sm font-normal">/ {bytes(data.memTotal)}</span>
        </div>
        <Meter value={mem} />
        <div className="text-muted-foreground mt-1.5 text-xs tabular-nums">
          交换 {data.swapTotal > 0 ? `${bytes(data.swapUsed)} / ${bytes(data.swapTotal)}` : "未启用"}
        </div>
      </Card>
      <Card icon={<HardDrive />} title={`磁盘 · ${disk?.mount ?? "/"}`}>
        {disk ? (
          <>
            <div className="mb-2 text-lg font-semibold tabular-nums">
              {bytes(disk.used)} <span className="text-muted-foreground text-sm font-normal">/ {bytes(disk.total)}</span>
            </div>
            <Meter value={percent(disk.used, disk.total)} />
          </>
        ) : (
          <div className="text-muted-foreground text-sm">-</div>
        )}
        <div className="text-muted-foreground mt-1.5 flex items-center gap-1 text-xs tabular-nums">
          senlinApps 占用 {usage == null ? "-" : bytes(usage)}
          <button
            className="hover:text-foreground ml-auto"
            title="统计 senlinApps 占用（目录大时较慢）"
            disabled={measuring}
            onClick={async () => {
              setMeasuring(true);
              try {
                setUsage(await Servers.appsDiskUsage(serverId));
              } catch (e) {
                toastError(e);
              } finally {
                setMeasuring(false);
              }
            }}
          >
            <RefreshCw className={cn("size-3", measuring && "animate-spin")} />
          </button>
        </div>
      </Card>
    </div>
  );
}

function AppsTable({ serverId, apps, onCreate }: { serverId: string; apps: ReturnType<typeof useApps>; onCreate: () => void }) {
  const { go } = useNav();
  if (apps.isError) {
    return <p className="text-destructive selectable text-sm">{message(apps.error)}</p>;
  }
  if (!apps.data) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 3 }, (_, i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    );
  }
  if (apps.data.length === 0) {
    return (
      <div className="text-muted-foreground flex flex-col items-center gap-3 rounded-lg border border-dashed py-10 text-sm">
        这台服务器上还没有应用
        <Button size="sm" variant="outline" onClick={onCreate}>
          <Plus />
          新建应用
        </Button>
      </div>
    );
  }
  return (
    <div className="rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>应用</TableHead>
            <TableHead>端口</TableHead>
            <TableHead>版本</TableHead>
            <TableHead>状态</TableHead>
            <TableHead className="text-right">内存</TableHead>
            <TableHead className="text-right">运行时长</TableHead>
            <TableHead className="text-right">自启</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {apps.data.map((a) => (
            <TableRow key={a.app.name} className="cursor-pointer" onClick={() => go({ server: serverId, app: a.app.name })}>
              <TableCell className="max-w-72">
                <div className="font-medium">
                  {a.app.name}
                  {a.error && <span className="text-destructive ml-2 text-xs font-normal">{a.error}</span>}
                </div>
                {a.app.note && (
                  <div className="text-muted-foreground truncate text-xs" title={a.app.note}>
                    {a.app.note.split("\n")[0]}
                  </div>
                )}
              </TableCell>
              <TableCell className="font-mono">{a.app.port || "-"}</TableCell>
              <TableCell className="font-mono text-xs">{release(a.current)}</TableCell>
              <TableCell>
                <AppState status={a.status} current={a.current} />
                {a.status.restarts > 0 && <span className="text-warning ml-2 text-xs">重启 {a.status.restarts} 次</span>}
              </TableCell>
              <TableCell className="text-right tabular-nums">{a.status.active === "active" ? bytes(a.status.memory) : "-"}</TableCell>
              <TableCell className="text-right tabular-nums">{a.status.active === "active" ? duration(a.status.uptime) : "-"}</TableCell>
              <TableCell className="text-muted-foreground text-right">{a.status.enabled ? "开" : "关"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
