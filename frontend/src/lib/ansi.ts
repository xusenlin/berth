/** A run of text with the SGR attributes of an ANSI-colored line. */
export interface Segment {
  text: string;
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

const basic = ["#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5"];
const bright = ["#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff"];

function color256(n: number): string {
  if (n < 8) return basic[n]!;
  if (n < 16) return bright[n - 8]!;
  if (n < 232) {
    const i = n - 16;
    const c = (v: number) => (v === 0 ? 0 : 55 + v * 40);
    return `rgb(${c(Math.floor(i / 36))},${c(Math.floor(i / 6) % 6)},${c(i % 6)})`;
  }
  const g = 8 + (n - 232) * 10;
  return `rgb(${g},${g},${g})`;
}

// CSI sequences and OSC sequences; only SGR ("m") changes the style.
const escape = /\x1b\[([0-9;:?]*)([A-Za-z])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>]/g;

/** parseAnsi splits a line into styled segments, dropping other escapes. */
export function parseAnsi(line: string): Segment[] {
  if (!line.includes("\x1b")) return [{ text: line }];
  const out: Segment[] = [];
  let style: Omit<Segment, "text"> = {};
  let last = 0;
  for (const m of line.matchAll(escape)) {
    if (m.index! > last) out.push({ ...style, text: line.slice(last, m.index) });
    last = m.index! + m[0].length;
    if (m[2] !== "m") continue;
    const codes = (m[1] || "0").split(/[;:]/).map((c) => Number(c || 0));
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i]!;
      if (c === 0) style = {};
      else if (c === 1) style.bold = true;
      else if (c === 2) style.dim = true;
      else if (c === 3) style.italic = true;
      else if (c === 4) style.underline = true;
      else if (c === 22) style.bold = style.dim = false;
      else if (c === 23) style.italic = false;
      else if (c === 24) style.underline = false;
      else if (c >= 30 && c <= 37) style.fg = basic[c - 30];
      else if (c >= 90 && c <= 97) style.fg = bright[c - 90];
      else if (c >= 40 && c <= 47) style.bg = basic[c - 40];
      else if (c >= 100 && c <= 107) style.bg = bright[c - 100];
      else if (c === 39) style.fg = undefined;
      else if (c === 49) style.bg = undefined;
      else if (c === 38 || c === 48) {
        let col: string | undefined;
        if (codes[i + 1] === 5) {
          col = color256(codes[i + 2] ?? 0);
          i += 2;
        } else if (codes[i + 1] === 2) {
          col = `rgb(${codes[i + 2] ?? 0},${codes[i + 3] ?? 0},${codes[i + 4] ?? 0})`;
          i += 4;
        }
        if (c === 38) style.fg = col;
        else style.bg = col;
      }
    }
  }
  if (last < line.length) out.push({ ...style, text: line.slice(last) });
  return out;
}

/** stripAnsi returns the text of a line without escapes. */
export function stripAnsi(line: string): string {
  return line.includes("\x1b") ? line.replace(escape, "") : line;
}
