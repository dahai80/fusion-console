import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ChevronRight, X } from "lucide-react";
import { api, type Orchestrator } from "../api";
import { emptyOrchestrator } from "./workerModel";
import "./agents.css";

// Small building blocks shared by the Workers, Brain and Tools sections.

// updateOrchestrator re-reads the stored config before every save so the
// Workers view, the Brain view and the onboarding wizard never overwrite each
// other's fields with a stale copy. Unknown keys from the server
// (caveman_*, dispatch_timeout_minutes) are carried through untouched.
export async function updateOrchestrator(patch: (o: Orchestrator) => Orchestrator): Promise<Orchestrator> {
  const cur = (await api.orchestration()).orchestrator;
  const base = { ...emptyOrchestrator(), ...cur, workers: cur?.workers ?? [] };
  const next = patch(base);
  await api.saveOrchestration(next);
  return next;
}

export function Section({ title, aside, children, className = "" }: { title?: ReactNode; aside?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-2xl border border-zinc-200/80 bg-white p-4 shadow-sm shadow-zinc-900/5 dark:border-zinc-800 dark:bg-zinc-900 sm:p-5 ${className}`}>
      {(title || aside) && (
        <div className="mb-3 flex items-center justify-between gap-3">
          {title && <h3 className="text-sm font-semibold">{title}</h3>}
          {aside}
        </div>
      )}
      {children}
    </section>
  );
}

export function Choice({ name, checked, onSelect, title, desc, badge, children, disabled }: {
  name: string; checked: boolean; onSelect: () => void; title: ReactNode; desc?: ReactNode; badge?: ReactNode; children?: ReactNode; disabled?: boolean;
}) {
  return (
    <div className={`rounded-xl border transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-indigo-500/50 ${
      checked ? "border-indigo-500 bg-indigo-500/[0.04] dark:bg-indigo-500/[0.07]" : "border-zinc-200/80 hover:border-zinc-300 dark:border-zinc-800 dark:hover:border-zinc-700"
    } ${disabled ? "opacity-60" : ""}`}>
      <label className="flex cursor-pointer items-start gap-3 p-3">
        <input type="radio" name={name} className="sr-only" checked={checked} disabled={disabled} onChange={onSelect} />
        <span aria-hidden className={`mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border ${
          checked ? "border-indigo-600 bg-indigo-600" : "border-zinc-300 dark:border-zinc-600"}`}>
          {checked && <span className="size-1.5 rounded-full bg-white" />}
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-2 text-sm font-medium">{title}{badge}</span>
          {desc && <span className="mt-0.5 block text-xs leading-5 text-zinc-500 dark:text-zinc-400">{desc}</span>}
        </span>
      </label>
      {checked && children && <div className="px-3 pb-3 pl-10">{children}</div>}
    </div>
  );
}

export function Tag({ children, tone = "neutral" }: { children: ReactNode; tone?: "neutral" | "ok" | "warn" | "busy" | "brand" }) {
  const tones = {
    neutral: "bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300",
    ok: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
    warn: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
    busy: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
    brand: "bg-indigo-500/10 text-indigo-700 dark:text-indigo-300",
  };
  return <span className={`inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium leading-4 ${tones[tone]}`}>{children}</span>;
}

export function Field({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="block min-w-0">
      <span className="mb-1 block text-xs font-medium text-zinc-600 dark:text-zinc-300">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs leading-5 text-zinc-500 dark:text-zinc-400">{hint}</span>}
    </label>
  );
}

export function Advanced({ open, onToggle, children, label = "高级选项" }: { open: boolean; onToggle: () => void; children: ReactNode; label?: string }) {
  return (
    <div>
      <button type="button" onClick={onToggle} aria-expanded={open}
        className="-mx-1 inline-flex items-center gap-1 rounded-lg px-1 py-1 text-sm font-medium text-zinc-600 hover:text-zinc-900 dark:text-zinc-300 dark:hover:text-white">
        <ChevronRight size={15} className={`transition-transform ${open ? "rotate-90" : ""}`} /> {label}
      </button>
      {open && <div className="mt-3 space-y-4">{children}</div>}
    </div>
  );
}

export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}
      className={`relative inline-flex h-6 w-10 shrink-0 items-center rounded-full transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500/50 disabled:opacity-50 ${
        checked ? "bg-indigo-600" : "bg-zinc-300 dark:bg-zinc-700"}`}>
      <span className={`inline-block size-5 rounded-full bg-white shadow transition-transform ${checked ? "translate-x-[18px]" : "translate-x-0.5"}`} />
    </button>
  );
}

// Sheet is the one overlay used for opening a worker or creating one: a
// bottom sheet on phones, a right-hand drawer from the sm breakpoint up.
export function Sheet({ open, title, subtitle, onClose, footer, children }: {
  open: boolean; title: ReactNode; subtitle?: ReactNode; onClose: () => void; footer?: ReactNode; children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    panel.current?.focus();
    return () => {
      document.body.style.overflow = overflow;
    };
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-end sm:items-stretch sm:justify-end">
      <div className="fc-scrim absolute inset-0 bg-zinc-950/40 dark:bg-black/60" onClick={onClose} />
      <div ref={panel} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="fc-sheet-title"
        className="fc-sheet relative flex max-h-[92dvh] w-full flex-col rounded-t-2xl bg-zinc-50 shadow-2xl outline-none dark:bg-zinc-950 sm:h-full sm:max-h-none sm:w-[32rem] sm:rounded-none sm:border-l sm:border-zinc-200 sm:dark:border-zinc-800">
        <div aria-hidden className="mx-auto mt-2 h-1 w-10 shrink-0 rounded-full bg-zinc-300 dark:bg-zinc-700 sm:hidden" />
        <header className="flex shrink-0 items-start gap-3 px-4 pb-3 pt-2 sm:px-5 sm:pt-4">
          <div className="min-w-0 flex-1">
            <h2 id="fc-sheet-title" className="truncate text-base font-semibold">{title}</h2>
            {subtitle && <div className="mt-0.5 truncate text-xs text-zinc-500 dark:text-zinc-400">{subtitle}</div>}
          </div>
          <button type="button" onClick={onClose} aria-label="关闭"
            className="-mr-1 rounded-lg p-2 text-zinc-500 hover:bg-zinc-200/70 hover:text-zinc-900 dark:hover:bg-zinc-800 dark:hover:text-white">
            <X size={18} />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pb-4 sm:px-5">{children}</div>
        {footer && (
          <footer className="shrink-0 border-t border-zinc-200/80 bg-white px-4 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] dark:border-zinc-800 dark:bg-zinc-900 sm:px-5">
            {footer}
          </footer>
        )}
      </div>
    </div>,
    document.body,
  );
}

// useFlash shows a short status line that clears itself.
export function useFlash(ms = 3500): [string, (msg: string) => void] {
  const [msg, setMsg] = useState("");
  useEffect(() => {
    if (!msg) return;
    const t = setTimeout(() => setMsg(""), ms);
    return () => clearTimeout(t);
  }, [msg, ms]);
  return [msg, setMsg];
}

// copyText falls back to a hidden textarea: phones reach the console over
// plain http on the tailnet, where navigator.clipboard is unavailable.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* fall through */ }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand("copy"); } catch { ok = false; }
  ta.remove();
  return ok;
}

export const selectCls = "pointer-coarse:text-base";
