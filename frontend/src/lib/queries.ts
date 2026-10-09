import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Apps, Servers, events } from "@/mygo";

export const keys = {
  servers: ["servers"] as const,
  connect: (id: string) => ["connect", id] as const,
  overview: (id: string) => ["overview", id] as const,
  ports: (id: string) => ["ports", id] as const,
  apps: (id: string) => ["apps", id] as const,
  app: (id: string, name: string) => ["app", id, name] as const,
  releases: (id: string, name: string) => ["releases", id, name] as const,
  presets: ["presets"] as const,
};

export function useServers() {
  return useQuery({ queryKey: keys.servers, queryFn: () => Servers.list() });
}

/** useServerEvents refreshes the server list when a connection changes. */
export function useServerEvents() {
  const qc = useQueryClient();
  useEffect(
    () =>
      events.serverStatus.on((ch) => {
        qc.invalidateQueries({ queryKey: keys.servers });
        if (ch.status === "disconnected") qc.invalidateQueries({ queryKey: keys.connect(ch.id) });
      }),
    [qc],
  );
}

/**
 * useConnection connects to a server; other queries wait for it. A server
 * the user disconnected is left alone until useServerLink's connect.
 */
export function useConnection(id: string) {
  const servers = useServers();
  const offline = !!servers.data?.find((s) => s.server.id === id)?.offline;
  return useQuery({
    queryKey: keys.connect(id),
    queryFn: () => Servers.connect(id),
    enabled: !!servers.data && !offline,
    retry: false,
    staleTime: Infinity,
  });
}

/** useServerLink is a server's connection with the user's controls. */
export function useServerLink(id: string) {
  const qc = useQueryClient();
  const servers = useServers();
  const view = servers.data?.find((s) => s.server.id === id);
  const offline = !!view?.offline;
  const conn = useConnection(id);
  const [busy, setBusy] = useState(false);

  const forget = () => {
    for (const k of [keys.connect(id), keys.overview(id), keys.apps(id), keys.ports(id)]) qc.removeQueries({ queryKey: k });
  };
  /** connect connects again, after an error or the user's disconnect. */
  const connect = async () => {
    setBusy(true);
    try {
      forget();
      await Servers.reconnect(id).catch(() => {}); // errors show through useConnection
      await qc.invalidateQueries({ queryKey: keys.servers });
      await qc.invalidateQueries({ queryKey: keys.connect(id) });
    } finally {
      setBusy(false);
    }
  };
  const disconnect = async () => {
    await Servers.disconnect(id); // refused while a deploy runs
    forget();
    await qc.invalidateQueries({ queryKey: keys.servers });
  };
  return {
    view,
    offline,
    conn,
    connecting: busy || conn.isFetching,
    ready: !offline && conn.isSuccess,
    env: offline ? null : (conn.data?.env ?? null),
    connect,
    disconnect,
  };
}

// Refreshed every 5 seconds while the page is visible: React Query pauses
// intervals in the background.
export function useOverview(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.overview(id),
    queryFn: () => Servers.overview(id),
    enabled,
    refetchInterval: 5000,
  });
}

export function usePorts(id: string, enabled: boolean) {
  return useQuery({ queryKey: keys.ports(id), queryFn: () => Servers.ports(id), enabled, refetchInterval: 10000 });
}

export function useApps(id: string, enabled: boolean) {
  return useQuery({ queryKey: keys.apps(id), queryFn: () => Apps.list(id), enabled, refetchInterval: 5000 });
}

export function useApp(id: string, name: string, enabled = true) {
  return useQuery({ queryKey: keys.app(id, name), queryFn: () => Apps.get(id, name), enabled, refetchInterval: 3000 });
}

export function useReleases(id: string, name: string) {
  return useQuery({ queryKey: keys.releases(id, name), queryFn: () => Apps.releases(id, name) });
}

export function usePresets() {
  return useQuery({ queryKey: keys.presets, queryFn: () => Apps.presets(), staleTime: Infinity });
}
