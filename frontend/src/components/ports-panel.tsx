import { useMemo, useState } from "react";
import { Globe, Info, Lock } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { useNav } from "@/lib/nav";
import type { Listener, Ports } from "@/mygo";

export function addressLabel(l: Listener): { text: string; local: boolean } {
  if (l.local) return { text: `仅本机 ${l.address}`, local: true };
  if (l.address === "0.0.0.0" || l.address === "::" || l.address === "*") return { text: "所有地址", local: false };
  return { text: l.address, local: false };
}

/** owner describes who holds a port. */
export function ownerOf(l: Listener, allProcesses: boolean): string {
  if (l.app) return `Berth 应用 ${l.app}`;
  if (l.process) return `${l.process}${l.pid ? `（pid ${l.pid}）` : ""}`;
  return allProcesses ? "未知进程" : "其他用户的进程";
}

/** PortsPanel lists what listens on a server and which apps hold ports. */
export function PortsPanel({ serverId, ports }: { serverId: string; ports: Ports | undefined }) {
  const { go } = useNav();
  const [udp, setUdp] = useState(false);
  const [externalOnly, setExternalOnly] = useState(false);

  const rows = useMemo(
    () => (ports?.listeners ?? []).filter((l) => (udp || l.proto === "tcp") && (!externalOnly || !l.local)),
    [ports, udp, externalOnly],
  );
  if (!ports) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-8 w-full" />
        ))}
      </div>
    );
  }
  const [lo, hi] = ports.portRange;
  const inRange = (p: number) => p >= (lo ?? 0) && p <= (hi ?? 0);
  const others = ports.listeners.filter((l) => !l.app && l.proto === "tcp" && inRange(l.port));

  return (
    <div className="space-y-3">
      <div className="text-muted-foreground flex flex-wrap items-center gap-x-5 gap-y-2 text-xs">
        <label className="flex items-center gap-2">
          <Switch checked={externalOnly} onCheckedChange={setExternalOnly} />
          只看对外开放的
        </label>
        <label className="flex items-center gap-2">
          <Switch checked={udp} onCheckedChange={setUdp} />
          显示 UDP
        </label>
        <span className="ml-auto">
          应用端口区间 {lo}–{hi}
          {others.length > 0 && <span className="text-warning">，其中 {others.length} 个被其他程序占用</span>}
        </span>
      </div>
      {!ports.allProcesses && (
        <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
          <Info className="size-3.5" />
          当前以普通用户登录，只能看到自己进程的名称；其他用户的进程只显示端口。
        </p>
      )}
      <div className="surface overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-24">端口</TableHead>
              <TableHead className="w-16">协议</TableHead>
              <TableHead>监听地址</TableHead>
              <TableHead>占用者</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((l) => {
              const addr = addressLabel(l);
              return (
                <TableRow key={`${l.proto}/${l.address}/${l.port}/${l.pid}`}>
                  <TableCell className="font-mono">
                    {l.port}
                    {inRange(l.port) && !l.app && (
                      <span className="text-warning ml-1.5 text-xs" title="在应用端口区间内，新应用不会分配到它">
                        区间内
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground uppercase">{l.proto}</TableCell>
                  <TableCell>
                    <span className="inline-flex items-center gap-1.5">
                      {addr.local ? <Lock className="text-muted-foreground size-3.5" /> : <Globe className="text-muted-foreground size-3.5" />}
                      {addr.text}
                    </span>
                  </TableCell>
                  <TableCell>
                    {l.app ? (
                      <Badge variant="secondary" className="cursor-pointer" onClick={() => go({ server: serverId, app: l.app })}>
                        {l.app}
                      </Badge>
                    ) : (
                      <span className={l.process ? "" : "text-muted-foreground"}>{ownerOf(l, ports.allProcesses)}</span>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
            {rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={4} className="text-muted-foreground py-6 text-center">
                  没有监听中的端口
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      {ports.reserved.length > 0 && (
        <div className="text-sm">
          <div className="text-muted-foreground mb-1.5 text-xs">已分配给应用、但当前没有监听的端口：</div>
          <div className="flex flex-wrap gap-2">
            {ports.reserved.map((r) => (
              <Badge key={r.port} variant="outline" className="cursor-pointer font-normal" onClick={() => go({ server: serverId, app: r.app })}>
                <span className="font-mono">{r.port}</span> {r.app}
                <span className="text-muted-foreground">{r.running ? "运行中但未监听" : "未运行"}</span>
              </Badge>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
