import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Settings } from "@/mygo";

export type Theme = "system" | "light" | "dark";
export type Palette = "harbor" | "dusk" | "ember";

export const palettes: { id: Palette; name: string; description: string }[] = [
  { id: "harbor", name: "港湾", description: "图标的青绿，清爽冷静" },
  { id: "dusk", name: "暮色", description: "靛蓝与紫，沉稳专注" },
  { id: "ember", name: "余晖", description: "琥珀暖橙，温和柔软" },
];

interface ThemeState {
  theme: Theme;
  palette: Palette;
  resolved: "light" | "dark";
  setTheme: (t: Theme) => void;
  setPalette: (p: Palette) => void;
}

const ThemeContext = createContext<ThemeState>({
  theme: "system",
  palette: "harbor",
  resolved: "light",
  setTheme: () => {},
  setPalette: () => {},
});

// config.json is read asynchronously; the last choice is kept here too so
// the first paint already has the right colors.
const cacheKey = "berth.appearance";

function cached(): { theme: Theme; palette: Palette } {
  try {
    const v = JSON.parse(localStorage.getItem(cacheKey) ?? "{}");
    return { theme: v.theme ?? "system", palette: v.palette ?? "harbor" };
  } catch {
    return { theme: "system", palette: "harbor" };
  }
}

function systemDark() {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function apply(dark: boolean, palette: Palette, animate: boolean) {
  const root = document.documentElement;
  if (animate) {
    root.classList.add("theme-transition");
    window.setTimeout(() => root.classList.remove("theme-transition"), 300);
  }
  root.classList.toggle("dark", dark);
  root.dataset.palette = palette;
}

const initial = cached();
apply(initial.theme === "system" ? systemDark() : initial.theme === "dark", initial.palette, false);

/** ThemeProvider applies the theme and palette saved in config.json to the page. */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(initial.theme);
  const [palette, setPaletteState] = useState<Palette>(initial.palette);
  const [dark, setDark] = useState(systemDark);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Settings.get()
      .then((s) => {
        setThemeState((s.theme as Theme) || "system");
        setPaletteState((s.palette as Palette) || "harbor");
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const on = () => setDark(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);

  const resolved = theme === "system" ? (dark ? "dark" : "light") : theme;
  useEffect(() => {
    apply(resolved === "dark", palette, loaded);
    localStorage.setItem(cacheKey, JSON.stringify({ theme, palette }));
  }, [theme, resolved, palette]);

  // Settings.set replaces both fields: read the other one from here, not
  // from a render that may predate a change made in the same tick.
  const current = useRef({ theme, palette });
  current.current = { theme, palette };
  const save = (next: Partial<{ theme: Theme; palette: Palette }>) => {
    const v = { ...current.current, ...next };
    current.current = v;
    setThemeState(v.theme);
    setPaletteState(v.palette);
    Settings.set(v).catch(() => {});
  };
  return (
    <ThemeContext.Provider
      value={{ theme, palette, resolved, setTheme: (t) => save({ theme: t }), setPalette: (p) => save({ palette: p }) }}
    >
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  return useContext(ThemeContext);
}

export function useResolvedTheme() {
  return useContext(ThemeContext).resolved;
}
