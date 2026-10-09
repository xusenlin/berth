import { useEffect } from "react";
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

/** useConnection connects to a server; other queries wait for it. */
export function useConnection(id: string) {
  return useQuery({
    queryKey: keys.connect(id),
    queryFn: () => Servers.connect(id),
    retry: false,
    staleTime: Infinity,
  });
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

export function useApp(id: string, name: string) {
  return useQuery({ queryKey: keys.app(id, name), queryFn: () => Apps.get(id, name), refetchInterval: 3000 });
}

export function useReleases(id: string, name: string) {
  return useQuery({ queryKey: keys.releases(id, name), queryFn: () => Apps.releases(id, name) });
}

export function usePresets() {
  return useQuery({ queryKey: keys.presets, queryFn: () => Apps.presets(), staleTime: Infinity });
}
