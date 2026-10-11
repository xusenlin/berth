import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, NotebookPen, Pencil } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { toastError } from "@/lib/errors";
import { keys } from "@/lib/queries";
import { Apps } from "@/mygo";

const MAX_NOTE = 2000;

/** NoteCard shows an app's note and edits it in place. */
export function NoteCard({ serverId, name, note }: { serverId: string; name: string; note: string }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(note);
  const [saving, setSaving] = useState(false);

  // Follow the server's note while not editing it.
  useEffect(() => {
    if (!editing) setText(note);
  }, [note, editing]);

  const save = async () => {
    setSaving(true);
    try {
      const saved = await Apps.setNote(serverId, name, text);
      setText(saved);
      setEditing(false);
      qc.invalidateQueries({ queryKey: keys.app(serverId, name) });
      qc.invalidateQueries({ queryKey: keys.apps(serverId) });
      toast.success(saved ? "备注已保存" : "备注已清空");
    } catch (e) {
      toastError(e, "保存备注失败");
    } finally {
      setSaving(false);
    }
  };

  if (editing) {
    return (
      <div className="surface space-y-2 p-5">
        <div className="text-muted-foreground flex items-center gap-1.5 text-xs">
          <NotebookPen className="size-3.5" />
          备注
        </div>
        <Textarea
          autoFocus
          value={text}
          maxLength={MAX_NOTE}
          placeholder="用途、负责人、访问方式、注意事项……"
          className="min-h-28"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === "Enter") save();
            if (e.key === "Escape") {
              setText(note);
              setEditing(false);
            }
          }}
        />
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground text-xs">⌘↩ 保存，Esc 取消 · {text.length}/{MAX_NOTE}</span>
          <div className="ml-auto flex gap-2">
            <Button
              size="sm"
              variant="ghost"
              disabled={saving}
              onClick={() => {
                setText(note);
                setEditing(false);
              }}
            >
              取消
            </Button>
            <Button size="sm" disabled={saving || text.trim() === note} onClick={save}>
              {saving && <Loader2 className="animate-spin" />}
              保存
            </Button>
          </div>
        </div>
      </div>
    );
  }

  if (!note) {
    return (
      <button
        className="text-muted-foreground hover:text-foreground hover:bg-accent/50 hover:border-primary/40 flex w-full items-center gap-1.5 rounded-xl border border-dashed px-4 py-3 text-sm transition-colors"
        onClick={() => setEditing(true)}
      >
        <NotebookPen className="size-4" />
        添加备注
      </button>
    );
  }

  return (
    <div className="surface group relative p-5">
      <div className="text-muted-foreground mb-1.5 flex items-center gap-1.5 text-xs">
        <NotebookPen className="size-3.5" />
        备注
      </div>
      <p className="selectable text-sm break-words whitespace-pre-wrap">{note}</p>
      <Button size="sm" variant="ghost" className="absolute top-2 right-2" onClick={() => setEditing(true)}>
        <Pencil />
        编辑
      </Button>
    </div>
  );
}
