import { Check, Monitor, Moon, Sun } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { palettes, useTheme, type Palette, type Theme } from "@/lib/theme";
import { cn } from "@/lib/utils";

const modes: { id: Theme; name: string; icon: typeof Sun }[] = [
  { id: "system", name: "跟随系统", icon: Monitor },
  { id: "light", name: "浅色", icon: Sun },
  { id: "dark", name: "深色", icon: Moon },
];

/** Preview is a tiny window in a palette, drawn with that palette's colors. */
function Preview({ palette, dark }: { palette: Palette; dark: boolean }) {
  return (
    <div
      data-palette={palette}
      className={cn("bg-background text-foreground flex h-28 overflow-hidden rounded-lg border", dark && "dark")}
    >
      <div className="bg-sidebar flex w-[36%] flex-col gap-1.5 border-r p-2">
        <div className="mb-0.5 flex items-center gap-1">
          <span className="bg-brand size-2.5 rounded-[3px]" />
          <span className="bg-sidebar-foreground/35 h-1 w-7 rounded-full" />
        </div>
        <div className="bg-sidebar-accent flex items-center gap-1 rounded p-1">
          <span className="bg-primary size-2.5 rounded-[3px]" />
          <span className="bg-sidebar-foreground/45 h-1 flex-1 rounded-full" />
        </div>
        {[0, 1].map((i) => (
          <div key={i} className="flex items-center gap-1 p-1">
            <span className="bg-background size-2.5 rounded-[3px] border" />
            <span className="bg-sidebar-foreground/25 h-1 flex-1 rounded-full" />
          </div>
        ))}
      </div>
      <div className="flex flex-1 flex-col gap-1.5 p-2">
        <span className="bg-foreground/60 h-1.5 w-10 rounded-full" />
        <div className="grid grid-cols-2 gap-1.5">
          {[70, 40].map((w) => (
            <div key={w} className="bg-card shadow-surface space-y-1 rounded-md border p-1.5">
              <span className="bg-muted-foreground/40 block h-1 w-6 rounded-full" />
              <div className="bg-primary/20 h-1 rounded-full">
                <div className="bg-primary h-full rounded-full" style={{ width: `${w}%` }} />
              </div>
            </div>
          ))}
        </div>
        <div className="mt-auto flex justify-end gap-1">
          <span className="bg-background h-3.5 w-7 rounded-[3px] border" />
          <span className="bg-primary h-3.5 w-9 rounded-[3px]" />
        </div>
      </div>
    </div>
  );
}

export function AppearanceDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { theme, palette, resolved, setTheme, setPalette } = useTheme();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>外观</DialogTitle>
          <DialogDescription>选择配色和明暗，立即生效。</DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <div className="text-sm font-medium">配色</div>
          <div className="grid grid-cols-3 gap-3">
            {palettes.map((p) => {
              const active = p.id === palette;
              return (
                <button
                  key={p.id}
                  onClick={() => setPalette(p.id)}
                  className={cn(
                    "group rounded-xl border p-1.5 text-left transition-all outline-none",
                    "focus-visible:ring-ring/50 focus-visible:ring-[3px]",
                    active ? "border-primary ring-primary/25 ring-[3px]" : "hover:border-foreground/20 hover:bg-accent/50",
                  )}
                >
                  <Preview palette={p.id} dark={resolved === "dark"} />
                  <div className="flex items-start gap-2 px-1 pt-2 pb-0.5">
                    <div className="min-w-0 flex-1">
                      <div className="text-sm font-medium">{p.name}</div>
                      <div className="text-muted-foreground truncate text-xs">{p.description}</div>
                    </div>
                    <span
                      className={cn(
                        "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border transition-colors",
                        active ? "bg-primary border-primary text-primary-foreground" : "bg-background",
                      )}
                    >
                      {active && <Check className="size-3" strokeWidth={3} />}
                    </span>
                  </div>
                </button>
              );
            })}
          </div>
        </div>

        <div className="space-y-2">
          <div className="text-sm font-medium">明暗</div>
          <div className="bg-muted grid grid-cols-3 gap-1 rounded-lg p-1">
            {modes.map((m) => (
              <button
                key={m.id}
                onClick={() => setTheme(m.id)}
                className={cn(
                  "flex h-8 items-center justify-center gap-1.5 rounded-md text-sm transition-all",
                  m.id === theme ? "bg-background text-foreground shadow-sm dark:bg-input/40" : "text-muted-foreground hover:text-foreground",
                )}
              >
                <m.icon className="size-4" />
                {m.name}
              </button>
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
