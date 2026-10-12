import type { Build } from "@/mygo";

export function bytes(n: number | undefined | null, digits = 1): string {
  if (n == null || n < 0) return "-";
  const units = ["B", "K", "M", "G", "T"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(v >= 100 ? 0 : digits)}${units[i]}`;
}

export function duration(seconds: number): string {
  if (!seconds || seconds < 0) return "-";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d} 天${h ? ` ${h} 小时` : ""}`;
  if (h > 0) return `${h} 小时${m ? ` ${m} 分` : ""}`;
  if (m > 0) return `${m} 分钟`;
  return `${Math.floor(seconds)} 秒`;
}

export function percent(used: number, total: number): number {
  return total > 0 ? Math.min(100, (used / total) * 100) : 0;
}

/** release turns 20261009-151800 into 10-09 15:18. */
export function release(id: string | undefined): string {
  if (!id) return "-";
  const m = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})(-\d+)?$/.exec(id);
  if (!m) return id;
  return `${m[2]}-${m[3]} ${m[4]}:${m[5]}${m[7] ?? ""}`;
}

export function releaseDate(id: string): string {
  const m = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})/.exec(id);
  if (!m) return id;
  return `${m[1]}-${m[2]}-${m[3]} ${m[4]}:${m[5]}:${m[6]}`;
}

export function clock(ms: number): string {
  const d = new Date(ms);
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}

export function cpuTime(ns: number): string {
  if (ns < 0) return "-";
  const s = ns / 1e9;
  if (s < 60) return `${s.toFixed(1)} 秒`;
  return duration(s);
}

/** buildLabel describes a Go build, as v1.0.1 (01b4f77); "" without one. */
export function buildLabel(b: Build | undefined): string {
  if (!b) return "";
  const rev = b.revision?.slice(0, 7) ?? "";
  const s = b.version && rev ? `${b.version} (${rev})` : b.version || rev;
  return s + (b.modified ? "*" : "");
}

/** buildTitle is the details of a Go build, for a tooltip. */
export function buildTitle(b: Build | undefined): string {
  if (!b) return "";
  return [
    b.version && `版本 ${b.version}`,
    b.revision && `提交 ${b.revision}`,
    b.time && `提交时间 ${new Date(b.time).toLocaleString()}`,
    b.modified && "* 构建时有未提交的改动",
  ]
    .filter(Boolean)
    .join("\n");
}
