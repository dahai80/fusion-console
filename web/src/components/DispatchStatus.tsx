import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Check, CircleAlert, Clock, Loader2, PanelRightClose, PanelRightOpen } from "lucide-react";
import type { DispatchRun } from "../api";

const desktopQuery = window.matchMedia("(min-width: 48rem)");
const preferenceKey = "fc-dispatch-rail";

export function useDispatchRail() {
  const [desktop, setDesktop] = useState(desktopQuery.matches);
  const [docked, setDocked] = useState(() => {
    try { return localStorage.getItem(preferenceKey) !== "collapsed"; } catch { return true; }
  });
  const [drawer, setDrawer] = useState(false);
  useEffect(() => {
    const resize = () => { setDesktop(desktopQuery.matches); setDrawer(false); };
    desktopQuery.addEventListener("change", resize);
    return () => desktopQuery.removeEventListener("change", resize);
  }, []);
  function setOpen(open: boolean) {
    if (!desktop) { setDrawer(open); return; }
    setDocked(open);
    try { localStorage.setItem(preferenceKey, open ? "expanded" : "collapsed"); } catch { /* In-tab preference still works. */ }
  }
  return { desktop, open: desktop ? docked : drawer, setOpen };
}

export function DispatchReopen({ runs, stale, onOpen }: {
  runs: DispatchRun[] | null; stale: boolean; onOpen: () => void;
}) {
  const counts = { running: 0, queued: 0, issues: 0, done: 0 };
  for (const run of runs ?? []) {
    const group = run.status === "blocked" || run.status === "error" ? "issues" : run.status;
    counts[group]++;
  }
  const allDone = !!runs?.length && counts.done === runs.length;
  const note = stale ? (runs ? "等待刷新" : "暂时无法获取")
    : !runs ? "正在读取…" : !runs.length ? "暂无任务" : "";
  const summary = runs?.length
    ? [note, `${counts.running} 运行中，${counts.queued} 排队，${counts.issues} 异常，${counts.done} 已完成`].filter(Boolean).join(" · ")
    : note;
  return <div className="dispatch-reopen">
    <span id="dispatch-counts" role="status" aria-atomic="true" className="sr-only">{summary}</span>
    <button type="button" id="dispatch-toggle" aria-controls="dispatch-rail" aria-expanded={false}
      aria-label="展开任务状态" aria-describedby="dispatch-counts" title={`展开任务状态 · ${summary}`} onClick={onOpen}>
      <span className="dispatch-signals" aria-hidden="true">
        {note ? <span className="dispatch-idle">{note}</span> : <>
          {counts.running > 0 && <span data-signal="running"><Loader2 />{counts.running} 运行中</span>}
          {counts.running === 0 && counts.queued > 0 && <span data-signal="queued"><Clock />{counts.queued} 排队</span>}
          {counts.issues > 0 && <span data-signal="error"><CircleAlert />{counts.issues} 异常</span>}
          {allDone && <span data-signal="done"><Check />全部完成 · {counts.done}</span>}
        </>}
      </span>
      <span className="dispatch-side-icon"><PanelRightOpen size={17} aria-hidden="true" /></span>
    </button>
  </div>;
}

export function DispatchRail({ desktop, open, onClose, headerActions, children }: {
  desktop: boolean; open: boolean; onClose: () => void; headerActions: ReactNode; children: ReactNode;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const wasOpen = useRef(open);
  useLayoutEffect(() => {
    const changed = open !== wasOpen.current;
    wasOpen.current = open;
    const node = dialog.current;
    if (open && node) {
      // Align the phone drawer header with the Chat toolbar, below the app navigation.
      const chat = node.closest(".chat-layout");
      function alignDrawer() {
        if (chat && node) node.style.top = Math.max(0, chat.getBoundingClientRect().top) + "px";
      }
      alignDrawer();
      node.showModal();
      closeButton.current?.focus({ preventScroll: true });
      window.addEventListener("resize", alignDrawer);
      return () => { window.removeEventListener("resize", alignDrawer); node.close(); };
    }
    if (changed) {
      const target = open ? closeButton.current : document.getElementById("dispatch-toggle");
      target?.focus({ preventScroll: true });
    }
  }, [desktop, open]);
  const content = <>
    <header className="flex shrink-0 items-center justify-between gap-1 pb-2 pl-1">
      <h2 className="text-xs font-semibold uppercase tracking-wide text-zinc-500 dark:text-zinc-400">Dispatches</h2>
      <div className="flex items-center gap-1">{headerActions}
        <button ref={closeButton} type="button" onClick={onClose} className="dispatch-rail-close" aria-controls="dispatch-rail" aria-expanded={true} aria-label="收起任务状态" title="收起任务状态">
          <PanelRightClose size={17} aria-hidden="true" />
        </button>
      </div>
    </header>
    {children}
  </>;
  if (desktop) return <aside id="dispatch-rail" aria-label="Dispatches" className="dispatch-rail" hidden={!open}>
    {open && content}
  </aside>;
  return <dialog id="dispatch-rail" className="dispatch-drawer" ref={dialog} aria-label="Dispatches"
    onCancel={event => { event.preventDefault(); onClose(); }}
    onClick={event => { if (event.target === event.currentTarget) onClose(); }}>
    {open && <div className="dispatch-drawer-content">{content}</div>}
  </dialog>;
}
