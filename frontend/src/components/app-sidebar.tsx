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
          <SidebarGroupLabel>服务器</SidebarGroupLabel>
          <SidebarGroupAction title="添加服务器" onClick={() => setAdding(true)}>
            <Plus />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {servers.isLoading &&
                Array.from({ length: 3 }, (_, i) => (
                  <SidebarMenuItem key={i}>
                    <SidebarMenuSkeleton showIcon />
                  </SidebarMenuItem>
                ))}
              {servers.data?.map((s) => (
                <SidebarMenuItem key={s.server.id}>
                  <SidebarMenuButton
                    isActive={route.server === s.server.id}
                    onClick={() => go({ server: s.server.id })}
                    className="h-auto py-1.5"
                    title={`${s.server.user}@${s.server.host}`}
                  >
                    <ServerDot status={s.status} />
                    <div className="min-w-0 flex-1">
                      <div className="truncate">{s.server.name}</div>
                      <div className="text-muted-foreground truncate text-xs">{s.server.host}</div>
                    </div>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
              {servers.data?.length === 0 && (
                <SidebarMenuItem>
                  <SidebarMenuButton onClick={() => setAdding(true)} className="text-muted-foreground">
                    <ServerIcon />
                    还没有服务器
                  </SidebarMenuButton>
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
