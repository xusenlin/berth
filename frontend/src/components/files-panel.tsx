import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { File, FilePlus, Folder, Loader2, RefreshCw, RotateCw, Save } from "lucide-react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { message, toastError } from "@/lib/errors";
import { bytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Apps, type FileContent, type Info } from "@/mygo";

const MAX_EDIT = 1 << 20;

/** FilesPanel edits the text files of an app's shared/ directory. */
export function FilesPanel({ serverId, info }: { serverId: string; info: Info }) {
  const name = info.app.name;
  const list = useQuery({
    queryKey: ["shared", serverId, name],
    queryFn: () => Apps.listShared(serverId, name),
  });
  const [file, setFile] = useState<FileContent | null>(null);
  const [text, setText] = useState("");
  const [loading, setLoading] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState<"" | "save" | "restart">("");
  const [pending, setPending] = useState<string | null>(null); // path to open after discarding
  const [creating, setCreating] = useState(false);
  const dirty = !!file && text !== file.content;
  const running = info.status.active === "active";

  const load = useCallback(
    async (path: string, isNew = false) => {
      setLoading(path);
      setError("");
      try {
        const fc = await Apps.readShared(serverId, name, path);
        if (isNew && fc.exists) toast.info(`${path} 已存在，已打开`);
        setFile(fc);
        setText(fc.content);
      } catch (e) {
        setFile(null);
        setError(message(e));
      } finally {
        setLoading("");
      }
    },
    [serverId, name],
  );

  const open = (path: string) => {
    if (dirty && path !== file?.path) setPending(path);
    else load(path);
  };

  const save = useCallback(
    async (restart: boolean) => {
      if (!file) return;
      setSaving(restart ? "restart" : "save");
      try {
        const fc = await Apps.writeShared(serverId, name, file.path, text, file.exists ? file.modTime : 0);
        setFile(fc);
        setText(fc.content);
        list.refetch();
        if (restart) {
          await Apps.do(serverId, name, "restart");
          toast.success(`已保存 ${fc.path} 并重启 ${name}`);
        } else {
          toast.success(`已保存 ${fc.path}`, running ? { description: "程序重新读取配置后生效，通常需要重启" } : undefined);
        }
      } catch (e) {
        toastError(e, "保存失败");
      } finally {
        setSaving("");
      }
    },
    [file, text, serverId, name, list, running],
  );

  // ⌘S / Ctrl+S saves.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        if (dirty && !saving) save(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dirty, saving, save]);

  const files = list.data?.files ?? [];
  const sorted = useMemo(() => [...files].sort((a, b) => a.path.localeCompare(b.path)), [files]);

  return (
    <div className="grid h-full min-h-0 grid-cols-[16rem_1fr] gap-4">
      <div className="flex min-h-0 flex-col rounded-lg border">
        <div className="flex items-center gap-1 border-b px-3 py-2">
          <span className="flex-1 font-mono text-xs">shared/</span>
          <Button variant="ghost" size="icon" className="size-7" title="新建文件" onClick={() => setCreating(true)}>
            <FilePlus />
          </Button>
          <Button variant="ghost" size="icon" className="size-7" title="刷新" onClick={() => list.refetch()}>
            <RefreshCw className={cn(list.isFetching && "animate-spin")} />
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto py-1 text-sm">
          {list.isError && <p className="text-destructive selectable p-3 text-xs">{message(list.error)}</p>}
          {list.isLoading && <Loader2 className="text-muted-foreground m-3 size-4 animate-spin" />}
          {sorted.map((f) => {
            const depth = f.path.split("/").length - 1;
            const base = f.path.split("/").pop();
            const tooBig = !f.dir && f.size > MAX_EDIT;
            return (
              <button
                key={f.path}
                disabled={f.dir || tooBig}
                title={tooBig ? "超过 1 MiB，不能在界面中编辑" : f.path}
                onClick={() => open(f.path)}
                style={{ paddingLeft: 12 + depth * 14 }}
                className={cn(
                  "flex w-full items-center gap-1.5 py-1 pr-3 text-left disabled:cursor-default",
                  !f.dir && !tooBig && "hover:bg-accent",
                  file?.path === f.path && "bg-accent font-medium",
                  tooBig && "text-muted-foreground",
                )}
              >
                {f.dir ? <Folder className="text-muted-foreground size-3.5 shrink-0" /> : <File className="text-muted-foreground size-3.5 shrink-0" />}
                <span className="truncate">{base}</span>
                {!f.dir && <span className="text-muted-foreground ml-auto shrink-0 text-xs">{bytes(f.size)}</span>}
                {loading === f.path && <Loader2 className="size-3 shrink-0 animate-spin" />}
              </button>
            );
          })}
          {list.data && sorted.length === 0 && (
            <p className="text-muted-foreground p-3 text-xs">shared/ 是空的。持久路径会在发版时创建，也可以直接新建配置文件。</p>
          )}
          {list.data?.truncated && <p className="text-muted-foreground p-3 text-xs">文件太多，只列出前 2000 个。</p>}
        </div>
        <p className="text-muted-foreground border-t px-3 py-2 text-xs">环境变量（.env）请在"设置"中编辑。</p>
      </div>

      <div className="flex min-h-0 flex-col gap-2">
        {file ? (
          <>
            <div className="flex items-center gap-2">
              <span className="selectable truncate font-mono text-sm">{file.path}</span>
              {dirty && <span className="bg-warning size-2 shrink-0 rounded-full" title="未保存" />}
              <span className="text-muted-foreground shrink-0 text-xs">
                {file.exists ? `${bytes(file.size)} · ${new Date(file.modTime).toLocaleString()}` : "新文件"}
              </span>
              <div className="ml-auto flex shrink-0 gap-1">
                <Button size="sm" variant="ghost" disabled={!dirty || !!saving} onClick={() => setText(file.content)}>
                  还原
                </Button>
                {file.exists && (
                  <Button size="sm" variant="ghost" disabled={!!saving} onClick={() => load(file.path)} title="从服务器重新读取">
                    <RefreshCw />
                  </Button>
                )}
                {running && (
                  <Button size="sm" variant="outline" disabled={(!dirty && file.exists) || !!saving} onClick={() => save(true)}>
                    {saving === "restart" ? <Loader2 className="animate-spin" /> : <RotateCw />}
                    保存并重启
                  </Button>
                )}
                <Button size="sm" disabled={(!dirty && file.exists) || !!saving} onClick={() => save(false)}>
                  {saving === "save" ? <Loader2 className="animate-spin" /> : <Save />}
                  保存
                </Button>
              </div>
            </div>
            <textarea
              value={text}
              spellCheck={false}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Tab") {
                  e.preventDefault();
                  const el = e.currentTarget;
                  const { selectionStart: s, selectionEnd: end } = el;
                  const next = text.slice(0, s) + "  " + text.slice(end);
                  setText(next);
                  requestAnimationFrame(() => el.setSelectionRange(s + 2, s + 2));
                }
              }}
              className="bg-background focus-visible:ring-ring/50 min-h-0 flex-1 resize-none rounded-lg border p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-[3px]"
            />
            <p className="text-muted-foreground text-xs">⌘S 保存。保存时若文件已在服务器上被修改，会拒绝覆盖。</p>
          </>
        ) : (
          <div className="text-muted-foreground flex flex-1 items-center justify-center rounded-lg border border-dashed p-6 text-center text-sm">
            {error ? <span className="text-destructive selectable">{error}</span> : "选择左侧的文件进行编辑"}
          </div>
        )}
      </div>

      <AlertDialog open={pending !== null} onOpenChange={(o) => !o && setPending(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>放弃未保存的修改？</AlertDialogTitle>
            <AlertDialogDescription>{file?.path} 有未保存的修改。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>继续编辑</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (pending) load(pending);
                setPending(null);
              }}
            >
              放弃并打开
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <NewFileDialog
        open={creating}
        onOpenChange={setCreating}
        onCreate={(path) => {
          setCreating(false);
          if (dirty) setPending(path);
          else load(path, true);
        }}
      />
    </div>
  );
}

function NewFileDialog({ open, onOpenChange, onCreate }: { open: boolean; onOpenChange: (o: boolean) => void; onCreate: (path: string) => void }) {
  const [path, setPath] = useState("");
  useEffect(() => {
    if (open) setPath("");
  }, [open]);
  const valid = path.trim() !== "" && !path.trim().startsWith("/") && !path.split("/").includes("..");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>新建文件</DialogTitle>
          <DialogDescription>相对 shared/ 的路径，目录会自动创建。保存后才会写入服务器。</DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (valid) onCreate(path.trim());
          }}
        >
          <Input autoFocus className="font-mono" placeholder="config.yaml" value={path} onChange={(e) => setPath(e.target.value)} />
          <DialogFooter className="mt-4">
            <Button type="submit" disabled={!valid}>
              创建
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
