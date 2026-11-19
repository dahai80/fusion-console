import { useEffect, useRef, useState, type ReactNode } from "react";
import { Loader2, PanelRightClose, PanelRightOpen, X } from "lucide-react";
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

export function DispatchSummary({ runs, stale, open, onToggle }: {
  runs: DispatchRun[] | null; stale: boolean; open: boolean; onToggle: () => void;
}) {
  const counts = { running: 0, queued: 0, issues: 0, done: 0 };
  for (const run of runs ?? []) {
    const group = run.status === "blocked" || run.status === "error" ? "issues" : run.status;
    counts[group]++;
  }
  const action = open ? "收起任务状态" : "展开任务状态";
  const ToggleIcon = open ? PanelRightClose : PanelRightOpen;
  const summary = runs ? `${counts.running} 进行中，${counts.queued} 排队，${counts.issues} 有问题，${counts.done} 已完成` : "";
  return <div className="dispatch-summary">
    <button type="button" id="dispatch-toggle" aria-controls="dispatch-rail" aria-expanded={open}
      aria-label={action} aria-describedby="dispatch-counts" title={action} onClick={onToggle}>
      <ToggleIcon size={16} aria-hidden="true" /><span>任务状态</span>
    </button>
    <div className="dispatch-counts" id="dispatch-counts" role="status" aria-atomic="true" title="所有聊天中保留的后台派工，问题包括受阻和失败">
      {runs && <span className="sr-only">{summary}</span>}
      {runs && <div className="dispatch-totals" aria-hidden="true">
        <span className="dispatch-active-count" data-active={counts.running > 0}>
          {counts.running > 0 && <Loader2 size={12} />}<b>{counts.running}</b> 进行中
        </span>
        <span><b>{counts.queued}</b> 排队</span>
        <span data-issue={counts.issues > 0}><b>{counts.issues}</b> 有问题</span>
        <span><b>{counts.done}</b> 已完成</span>
      </div>}
      {stale ? <span className="dispatch-feed-note">{runs ? "等待刷新" : "暂时无法获取"}</span>
        : !runs && <span className="dispatch-feed-note">正在读取…</span>}
    </div>
  </div>;
}

export function DispatchRail({ desktop, open, onClose, headerActions, children }: {
  desktop: boolean; open: boolean; onClose: () => void; headerActions: ReactNode; children: ReactNode;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const node = dialog.current;
    if (!node || !open) return;
    node.showModal();
    closeButton.current?.focus();
    return () => node.close();
  }, [desktop, open]);
  const CloseIcon = desktop ? PanelRightClose : X;
  const content = <>
    <header className="flex shrink-0 items-center justify-between gap-1 pb-2 pl-1">
      <h2 className="text-xs font-semibold uppercase tracking-wide text-zinc-500 dark:text-zinc-400">Dispatches</h2>
      <div className="flex items-center gap-1">{headerActions}
        <button ref={closeButton} type="button" onClick={onClose} className="dispatch-rail-close" aria-label="收起任务状态" title="收起任务状态">
          <CloseIcon size={16} aria-hidden="true" />
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
