import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { Settings } from "@/mygo";

export type Theme = "system" | "light" | "dark";

interface ThemeState {
  theme: Theme;
  resolved: "light" | "dark";
  setTheme: (t: Theme) => void;
}

const ThemeContext = createContext<ThemeState>({ theme: "system", resolved: "light", setTheme: () => {} });

function systemDark() {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/** ThemeProvider applies the theme saved in config.json to the page. */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>("system");
  const [dark, setDark] = useState(systemDark);

  useEffect(() => {
    Settings.get()
      .then((s) => setThemeState((s.theme as Theme) || "system"))
      .catch(() => {});
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const on = () => setDark(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);

  const resolved = theme === "system" ? (dark ? "dark" : "light") : theme;
  useEffect(() => {
    document.documentElement.classList.toggle("dark", resolved === "dark");
  }, [resolved]);

  const setTheme = (t: Theme) => {
    setThemeState(t);
    Settings.set({ theme: t }).catch(() => {});
  };
  return <ThemeContext.Provider value={{ theme, resolved, setTheme }}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  return useContext(ThemeContext);
}

export function useResolvedTheme() {
  return useContext(ThemeContext).resolved;
}
