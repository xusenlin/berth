import { useState, type CSSProperties } from "react";
import { AgentActivity } from "@/components/agent-activity";
import { AppSidebar } from "@/components/app-sidebar";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { AgentProvider } from "@/lib/agent";
import { NavContext, type Route } from "@/lib/nav";
import { useServerEvents, useServers } from "@/lib/queries";
import { AppPage } from "@/pages/app-page";
import { ServerPage } from "@/pages/server-page";
import { Welcome } from "@/pages/welcome";

export default function App() {
  const [route, setRoute] = useState<Route>({});
  const servers = useServers();
  useServerEvents();

  // Open the first server once the list arrives.
  const current = route.server ?? servers.data?.[0]?.server.id;
  const exists = servers.data?.some((s) => s.server.id === current);

  let page;
  if (!servers.data) page = null;
  else if (!current || !exists) page = <Welcome />;
  else if (route.app) page = <AppPage key={`${current}/${route.app}`} serverId={current} name={route.app} />;
  else page = <ServerPage key={current} serverId={current} />;

  return (
    <NavContext.Provider value={{ route: { ...route, server: current }, go: setRoute }}>
      <AgentProvider>
        <SidebarProvider style={{ "--sidebar-width": "15rem" } as CSSProperties} className="h-full">
          <AppSidebar />
          <SidebarInset className="min-w-0 overflow-hidden">
            {page}
            <AgentActivity />
          </SidebarInset>
        </SidebarProvider>
      </AgentProvider>
    </NavContext.Provider>
  );
}
