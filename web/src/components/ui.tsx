import React, { createContext, useContext, useEffect, useLayoutEffect, useState, useSyncExternalStore } from "react";

// Shared UI primitives styled after shadcn/ui design tokens
// (zinc neutrals + indigo accent, rounded-xl, subtle borders/shadows).

export function Button({
  variant = "default",
  className = "",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "default" | "ghost" | "outline" | "destructive";
}) {
  const styles: Record<string, string> = {
    default:
      "bg-indigo-600 text-white hover:bg-indigo-500 shadow-sm shadow-indigo-950/20",
    ghost: "hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60",
    outline:
      "border border-zinc-200/80 dark:border-zinc-700/80 hover:bg-zinc-100 dark:hover:bg-zinc-800/60",
    destructive: "bg-red-900/90 text-white hover:bg-red-800",
  };
  return (
    <button
      className={`inline-flex items-center justify-center gap-2 rounded-xl px-4 py-2 text-sm font-medium transition-colors disabled:opacity-50 disabled:pointer-events-none ${styles[variant]} ${className}`}
      {...props}
    />
  );
}

export function Input({ className = "", ...props }: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={`w-full rounded-xl border border-zinc-200/80 dark:border-zinc-700/80 bg-white dark:bg-zinc-900 px-3 py-2 text-sm pointer-coarse:text-base placeholder:text-zinc-400 focus:outline-none focus:ring-2 focus:ring-indigo-500/50 ${className}`}
      {...props}
    />
  );
}

export function Textarea({ className = "", ...props }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={`w-full rounded-xl border border-zinc-200/80 dark:border-zinc-700/80 bg-white dark:bg-zinc-900 px-3 py-2 text-sm pointer-coarse:text-base placeholder:text-zinc-400 focus:outline-none focus:ring-2 focus:ring-indigo-500/50 resize-none ${className}`}
      {...props}
    />
  );
}

export function Select({ className = "", ...props }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={`w-full rounded-xl border border-zinc-200/80 dark:border-zinc-700/80 bg-white dark:bg-zinc-900 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500/50 ${className}`}
      {...props}
    />
  );
}

export function Card({ className = "", ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={`rounded-2xl border border-zinc-200/80 dark:border-zinc-800 bg-white dark:bg-zinc-900 shadow-sm shadow-zinc-900/5 ${className}`}
      {...props}
    />
  );
}

export function Badge({
  tone = "neutral",
  children,
}: {
  tone?: "neutral" | "ok" | "warn" | "err";
  children: React.ReactNode;
}) {
  const tones: Record<string, string> = {
    neutral: "bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300",
    ok: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
    warn: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
    err: "bg-red-500/10 text-red-600 dark:text-red-400",
  };
  return (
    <span className={`inline-flex items-center rounded-lg px-2 py-0.5 text-xs font-medium ${tones[tone]}`}>
      {children}
    </span>
  );
}

export type ThemeMode = "system" | "light" | "dark";

function themeMode(value?: string | null): ThemeMode {
  return value === "light" || value === "dark" ? value : "system";
}

// The top-bar button cycles light → dark → system → light on each click.
const THEME_CYCLE: ThemeMode[] = ["light", "dark", "system"];
export const nextThemeMode = (mode: ThemeMode): ThemeMode => THEME_CYCLE[(THEME_CYCLE.indexOf(mode) + 1) % THEME_CYCLE.length];
export const THEME_LABELS: Record<ThemeMode, string> = { system: "跟随系统", light: "浅色", dark: "深色" };

const colorScheme = window.matchMedia("(prefers-color-scheme: dark)");
function subscribeToScheme(notify: () => void) {
  colorScheme.addEventListener("change", notify);
  return () => colorScheme.removeEventListener("change", notify);
}
const ThemeCtx = createContext<{ dark: boolean; mode: ThemeMode; setMode: (mode: ThemeMode) => void }>({
  dark: false, mode: "system", setMode: () => {},
});

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [mode, selectMode] = useState(() => themeMode(document.documentElement.dataset.theme));
  const systemDark = useSyncExternalStore(subscribeToScheme, () => colorScheme.matches);
  const dark = mode === "dark" || (mode === "system" && systemDark);
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = mode;
    document.documentElement.classList.toggle("dark", dark);
  }, [dark, mode]);
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === "fc-theme" || event.key === null) selectMode(themeMode(event.newValue));
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);
  function setMode(next: ThemeMode) {
    selectMode(next);
    try { localStorage.setItem("fc-theme", next); } catch { /* Keep the in-tab preference. */ }
  }
  return <ThemeCtx.Provider value={{ dark, mode, setMode }}>{children}</ThemeCtx.Provider>;
}

export const useTheme = () => useContext(ThemeCtx);
