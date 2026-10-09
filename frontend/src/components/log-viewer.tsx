import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Channel } from "mygo-runtime";
import { ArrowDownToLine, Eraser, Loader2, Pause, Play, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { parseAnsi, stripAnsi, type Segment } from "@/lib/ansi";
import { message } from "@/lib/errors";
import { clock } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Apps, type Entry } from "@/mygo";

const MAX_LINES = 20000;
const ROW = 20;

function Highlight({ text, term }: { text: string; term: string }) {
  if (!term) return <>{text}</>;
  const lower = text.toLowerCase();
  const parts = [];
  let i = 0;
  for (;;) {
    const j = lower.indexOf(term, i);
    if (j < 0) break;
    if (j > i) parts.push(text.slice(i, j));
    parts.push(
      <mark key={j} className="rounded-sm bg-yellow-300 text-black">
        {text.slice(j, j + term.length)}
      </mark>,
    );
    i = j + term.length;
  }
  parts.push(text.slice(i));
  return <>{parts}</>;
}

function segStyle(s: Segment): React.CSSProperties {
  return {
    color: s.fg,
    backgroundColor: s.bg,
    fontWeight: s.bold ? 600 : undefined,
    opacity: s.dim ? 0.7 : undefined,
    fontStyle: s.italic ? "italic" : undefined,
    textDecoration: s.underline ? "underline" : undefined,
  };
}

const Line = memo(function Line({ e, term }: { e: Entry; term: string }) {
  const segs = useMemo(() => parseAnsi(e.message), [e.message]);
  const level = e.priority <= 3 ? "text-destructive" : e.priority === 4 ? "text-warning" : "";
  return (
    <div className={cn("flex gap-3 px-3 leading-5 whitespace-pre", e.source === "systemd" && "text-muted-foreground italic", level)}>
      <span className="text-muted-foreground shrink-0 tabular-nums not-italic">{clock(e.time)}</span>
      <span>
        {segs.map((s, i) => (
          <span key={i} style={segStyle(s)}>
            <Highlight text={s.text} term={term} />
          </span>
        ))}
      </span>
    </div>
  );
});

function toLocalInput(ms: number) {
  const d = new Date(ms - new Date().getTimezoneOffset() * 60000);
  return d.toISOString().slice(0, 16);
}

/** LogViewer shows an app's journal: live, or a range of history. */
export function LogViewer({ serverId, name }: { serverId: string; name: string }) {
  const [mode, setMode] = useState<"live" | "history">("live");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState(false);
  const [follow, setFollow] = useState(true);
  const [paused, setPaused] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [since, setSince] = useState(() => toLocalInput(Date.now() - 3600_000));
  const [until, setUntil] = useState("");
  const [keyword, setKeyword] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;
  const [session, setSession] = useState(0);

  // Live: stream into a buffer flushed once per frame.
  useEffect(() => {
    if (mode !== "live") return;
    setEntries([]);
    setError("");
    setLoading(true);
    let buf: Entry[] = [];
    let raf = 0;
    const flush = () => {
      raf = 0;
      if (pausedRef.current || buf.length === 0) return;
      const add = buf;
      buf = [];
      setEntries((prev) => {
        const next = prev.concat(add);
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
      });
    };
    const ch = new Channel<Entry>((e) => {
      buf.push(e);
      if (buf.length > MAX_LINES) buf = buf.slice(-MAX_LINES);
      if (!raf) raf = requestAnimationFrame(flush);
    });
    Apps.followLogs(serverId, name, 300, ch)
      .catch((e) => setError(message(e)))
      .finally(() => setLoading(false));
    const t = setTimeout(() => setLoading(false), 1500);
    // Lines that arrived while paused show once resumed.
    const resume = setInterval(() => {
      if (!pausedRef.current && buf.length && !raf) raf = requestAnimationFrame(flush);
    }, 300);
    return () => {
      ch.close();
      cancelAnimationFrame(raf);
      clearTimeout(t);
      clearInterval(resume);
    };
  }, [mode, serverId, name, session]);

  const query = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const rows = await Apps.queryLogs(serverId, name, {
        since: since ? new Date(since).getTime() : 0,
        until: until ? new Date(until).getTime() : 0,
        keyword,
        limit: 5000,
      });
      setEntries(rows);
      setFollow(true);
    } catch (e) {
      setError(message(e));
    } finally {
      setLoading(false);
    }
  }, [serverId, name, since, until, keyword]);

  useEffect(() => {
    if (mode === "history") {
      setEntries([]);
      query();
    }
  }, [mode]); // query once on entering history; later queries are submitted

  const term = search.trim().toLowerCase();
  const rows = useMemo(
    () => (filter && term ? entries.filter((e) => stripAnsi(e.message).toLowerCase().includes(term)) : entries),
    [entries, filter, term],
  );
  const matches = useMemo(() => (term ? entries.filter((e) => stripAnsi(e.message).toLowerCase().includes(term)).length : 0), [entries, term]);

  const virt = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW,
    overscan: 20,
  });

  useEffect(() => {
    if (follow && rows.length) virt.scrollToIndex(rows.length - 1, { align: "end" });
  }, [rows.length, follow, virt]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < ROW * 2;
    if (atBottom !== follow) setFollow(atBottom);
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Tabs value={mode} onValueChange={(v) => setMode(v as "live" | "history")}>
          <TabsList>
            <TabsTrigger value="live">实时</TabsTrigger>
            <TabsTrigger value="history">历史</TabsTrigger>
          </TabsList>
        </Tabs>
        {mode === "live" ? (
          <>
            <Button size="sm" variant="outline" onClick={() => setPaused(!paused)}>
              {paused ? <Play /> : <Pause />}
              {paused ? "继续" : "暂停"}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setEntries([])} title="清屏">
              <Eraser />
            </Button>
          </>
        ) : (
          <form
            className="flex flex-wrap items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              query();
            }}
          >
            <Input type="datetime-local" className="h-8 w-48" value={since} onChange={(e) => setSince(e.target.value)} title="开始时间" />
            <span className="text-muted-foreground">至</span>
            <Input type="datetime-local" className="h-8 w-48" value={until} onChange={(e) => setUntil(e.target.value)} title="结束时间（留空为现在）" />
            <Input className="h-8 w-36" placeholder="关键字" value={keyword} onChange={(e) => setKeyword(e.target.value)} />
            <Button size="sm" type="submit" disabled={loading}>
              查询
            </Button>
          </form>
        )}
        <div className="ml-auto flex items-center gap-2">
          <div className="relative">
            <Search className="text-muted-foreground absolute top-2 left-2 size-4" />
            <Input className="h-8 w-44 pl-7" placeholder="搜索高亮" value={search} onChange={(e) => setSearch(e.target.value)} />
          </div>
          {term && <span className="text-muted-foreground text-xs tabular-nums">{matches} 处</span>}
          <label className="text-muted-foreground flex items-center gap-1.5 text-xs">
            <Switch checked={filter} onCheckedChange={setFilter} />
            只看匹配
          </label>
        </div>
      </div>

      <div className="relative min-h-0 flex-1 overflow-hidden rounded-lg border bg-neutral-50 dark:bg-neutral-950">
        <div ref={scrollRef} onScroll={onScroll} className="selectable h-full overflow-auto font-mono text-xs">
          <div style={{ height: virt.getTotalSize(), position: "relative", minWidth: "100%", width: "max-content" }}>
            {virt.getVirtualItems().map((v) => (
              <div key={v.key} style={{ position: "absolute", top: 0, left: 0, minWidth: "100%", height: ROW, transform: `translateY(${v.start}px)` }}>
                <Line e={rows[v.index]!} term={term} />
              </div>
            ))}
          </div>
        </div>
        {(loading || error || rows.length === 0) && (
          <div className="text-muted-foreground pointer-events-none absolute inset-0 flex items-center justify-center gap-2 text-sm">
            {loading ? (
              <>
                <Loader2 className="size-4 animate-spin" /> 加载日志…
              </>
            ) : error ? (
              <span className="text-destructive pointer-events-auto selectable max-w-lg text-center">
                {error}
                {mode === "live" && (
                  <Button size="sm" variant="link" onClick={() => setSession((s) => s + 1)}>
                    重新连接
                  </Button>
                )}
              </span>
            ) : (
              "暂无日志"
            )}
          </div>
        )}
        {!follow && rows.length > 0 && (
          <Button
            size="sm"
            variant="secondary"
            className="absolute right-4 bottom-3 shadow"
            onClick={() => {
              setFollow(true);
              virt.scrollToIndex(rows.length - 1, { align: "end" });
            }}
          >
            <ArrowDownToLine />
            跟随最新
          </Button>
        )}
        {paused && <div className="bg-warning/90 absolute top-2 right-4 rounded px-2 py-0.5 text-xs text-black">已暂停，新日志在后台缓存</div>}
      </div>
    </div>
  );
}
