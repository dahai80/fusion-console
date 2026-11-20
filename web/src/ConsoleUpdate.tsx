import { useEffect, useRef, useState } from "react";
import { ArrowDownToLine, Check, RefreshCw } from "lucide-react";

type Status = {
  enabled: boolean; current?: string; latest?: string; title?: string; checked?: number;
  phase?: string; error?: string; auto?: boolean; available?: boolean;
};
const phases: Record<string, string> = {
  checking: "正在检查上游…", building: "正在准备新版本…",
  waiting: "等待当前任务结束后安装", restarting: "正在重启 Console…",
};

export default function ConsoleUpdate() {
  const [open, setOpen] = useState(false);
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const [pendingAuto, setPendingAuto] = useState<boolean | null>(null);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const request = useRef(0);
  const firstRevision = useRef("");
  async function read(action?: Record<string, unknown>) {
    const id = ++request.current;
    try {
      const r = await fetch("/api/console-update", action
        ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(action) }
        : { cache: "no-store" });
      if (r.status === 401) { window.dispatchEvent(new Event("fc:logout")); return; }
      const data = await r.json();
      if (!r.ok) throw new Error(data.error || "检查失败，请重试");
      if (id !== request.current) return;
      if (data.current && firstRevision.current && data.current !== firstRevision.current) {
        window.location.reload(); return;
      }
      if (data.current) firstRevision.current = data.current;
      setStatus(data); setError("");
    } catch (e) {
      if (id === request.current) setError(e instanceof TypeError ? "连接中断，正在等待 Console 恢复" : (e as Error).message);
    }
  }
  useEffect(() => {
    void read();
    const timer = window.setInterval(() => { if (!document.hidden) void read(); }, open ? 2500 : 60000);
    const focus = () => void read();
    window.addEventListener("focus", focus);
    return () => { window.clearInterval(timer); window.removeEventListener("focus", focus); };
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const outside = (e: PointerEvent) => { if (!root.current?.contains(e.target as Node)) setOpen(false); };
    const escape = (e: KeyboardEvent) => { if (e.key === "Escape") { setOpen(false); trigger.current?.focus(); } };
    document.addEventListener("pointerdown", outside); document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("pointerdown", outside); document.removeEventListener("keydown", escape); };
  }, [open]);
  async function act(name: string, fields = {}) {
    setSending(true);
    try { await read({ action: name, ...fields }); } finally { setSending(false); }
  }
  if ((!status && !error) || status?.enabled === false) return null;
  const busy = Boolean(phases[status?.phase || ""]);
  const message = phases[status?.phase || ""] || (status?.available ? "有可用更新" : status?.checked ? "已是最新可用版本" : "等待检查上游");
  return <div ref={root} className="shrink-0 sm:relative">
    <button ref={trigger} onClick={() => setOpen(v => !v)} aria-expanded={open} aria-controls="console-update-panel"
      aria-label={status?.available ? "更新 Console，有可用更新" : "更新 Console"} title="更新 Console"
      className="relative flex items-center gap-1 rounded-xl p-2 hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60">
      <ArrowDownToLine size={16} /><span className="hidden md:inline">更新</span>
      {status?.available && <span className="absolute right-0.5 top-1 h-2 w-2 rounded-full bg-indigo-500" />}
    </button>
    {open && <section id="console-update-panel" aria-label="Console 更新"
      className="absolute right-3 top-full z-50 mt-1 max-h-[calc(100dvh-4rem)] w-64 max-w-[calc(100vw-1.5rem)] space-y-3 overflow-y-auto sm:right-0 sm:top-auto sm:mt-2 rounded-xl border border-zinc-200 bg-white p-4 text-sm text-zinc-800 shadow-lg dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100">
      <div className="flex items-center justify-between"><strong>Console 更新</strong><span className="text-xs text-zinc-500">上游 main</span></div>
      <p aria-live="polite" className="flex items-center gap-2">{!busy && !status?.available && Boolean(status?.checked) && !status?.error && !error && <Check size={15} />}{error || status?.error || message}</p>
      <div className="space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
        <div>当前版本 <code>{status?.current?.slice(0, 8) || "读取中"}</code></div>
        {status?.latest && <div>上游版本 <a className="underline" target="_blank" rel="noreferrer" href={'https://github.com/dahai80/fusion-console/commit/' + status.latest}>{status.latest.slice(0, 8)}</a></div>}
        {status?.title && status.available && <p className="break-words">{status.title}</p>}
      </div>
      <div className="flex flex-wrap gap-2">
        <button disabled={sending || busy} onClick={() => void act("check")} className="inline-flex items-center gap-1.5 rounded-lg border px-2 py-1.5 disabled:opacity-50 dark:border-zinc-700"><RefreshCw size={13} />检查更新</button>
        {status?.available && <button disabled={sending || busy} onClick={() => void act("install", { target: status.latest })}
          className="rounded-lg bg-indigo-600 px-3 py-1.5 text-white disabled:opacity-50">立即更新</button>}
      </div>
      <label className="flex items-center gap-2 border-t border-zinc-200 pt-3 text-xs dark:border-zinc-700">
        <input type="checkbox" className="accent-indigo-600" checked={pendingAuto ?? Boolean(status?.auto)} disabled={!status || sending}
          onChange={e => {
            const enabled = e.target.checked;
            setPendingAuto(enabled);
            void act("auto", { enabled }).finally(() => setPendingAuto(null));
          }} />空闲时自动更新
      </label>
    </section>}
  </div>;
}
