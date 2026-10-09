import { useState } from "react";
import { Monitor, Moon, Plus, Server as ServerIcon, Sun } from "lucide-react";
import { Logo } from "@/components/logo";
import { ServerDialog } from "@/components/server-dialog";
import { ServerDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
} from "@/components/ui/sidebar";
import { useNav } from "@/lib/nav";
import { useServers } from "@/lib/queries";
import { useTheme, type Theme } from "@/lib/theme";
import { cn } from "@/lib/utils";

export function AppSidebar() {
  const { route, go } = useNav();
  const servers = useServers();
  const [adding, setAdding] = useState(false);
  const { theme, setTheme } = useTheme();
  const ThemeIcon = theme === "dark" ? Moon : theme === "light" ? Sun : Monitor;

  return (
    <Sidebar>
      {/* Room for the traffic lights; the strip moves the window. */}
      <SidebarHeader className="drag h-11 shrink-0 flex-row items-center gap-2 pl-[max(calc(var(--mygo-titlebar-inset-left,0px)+10px),0.75rem)]">
        <Logo className="size-5" />
        <span className="text-sm font-semibold">Berth</span>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>
            服务器
            {!!servers.data?.length && <span className="ml-1.5 tabular-nums">{servers.data.length}</span>}
          </SidebarGroupLabel>
          <SidebarGroupAction title="添加服务器" onClick={() => setAdding(true)}>
            <Plus />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu className="gap-1">
              {servers.isLoading &&
                Array.from({ length: 3 }, (_, i) => (
                  <SidebarMenuItem key={i}>
                    <SidebarMenuSkeleton showIcon className="h-12" />
                  </SidebarMenuItem>
                ))}
              {servers.data?.map((s) => {
                const active = route.server === s.server.id;
                return (
                  <SidebarMenuItem key={s.server.id}>
                    <SidebarMenuButton
                      size="lg"
                      isActive={active}
                      onClick={() => go({ server: s.server.id })}
                      className="gap-3"
                      title={`${s.server.user}@${s.server.host}:${s.server.port}`}
                    >
                      <div
                        className={cn(
                          "relative flex size-8 shrink-0 items-center justify-center rounded-lg border shadow-xs transition-colors",
                          active ? "bg-brand border-brand text-white" : "bg-background text-muted-foreground",
                        )}
                      >
                        <ServerIcon className="size-4" />
                        <ServerDot
                          status={s.offline ? "disconnected" : s.status}
                          className={cn("absolute -right-0.5 -bottom-0.5 size-2.5 ring-2", active ? "ring-sidebar-accent" : "ring-sidebar")}
                        />
                      </div>
                      <div className="min-w-0 flex-1 leading-tight">
                        <div className="truncate text-sm font-medium">{s.server.name}</div>
                        <div className="text-muted-foreground truncate text-xs">
                          {s.server.user}@{s.server.host}
                        </div>
                      </div>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                );
              })}
              {servers.data?.length === 0 && (
                <SidebarMenuItem>
                  <button
                    onClick={() => setAdding(true)}
                    className="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent flex w-full flex-col items-center gap-2 rounded-lg border border-dashed px-3 py-5 text-xs"
                  >
                    <ServerIcon className="size-5" />
                    还没有服务器，点击添加
                  </button>
                </SidebarMenuItem>
              )}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter className="flex-row items-center justify-between">
        <Button variant="ghost" size="sm" onClick={() => setAdding(true)}>
          <Plus />
          添加服务器
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-8" title="外观">
              <ThemeIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
              <DropdownMenuRadioItem value="system">跟随系统</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="light">浅色</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="dark">深色</DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarFooter>
      <ServerDialog open={adding} onOpenChange={setAdding} />
    </Sidebar>
  );
}
