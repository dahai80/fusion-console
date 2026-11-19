import { useEffect, useRef, type ReactNode } from "react";
import { Bot, Brain, CheckCircle, CirclePause, Clock, ExternalLink, Loader2, Maximize2, X, XCircle } from "lucide-react";
import type { DispatchRun } from "../api";
import type { BrainActivity } from "./brainActivity";
import "./dispatch-card.css";

const states = {
  queued: { label: "Queued", Icon: Clock },
  running: { label: "Running", Icon: Loader2 },
  blocked: { label: "Blocked", Icon: CirclePause },
  done: { label: "Done", Icon: CheckCircle },
  error: { label: "Error", Icon: XCircle },
};

function Status({ run }: { run: DispatchRun }) {
  const { Icon, label } = states[run.status];
  return <span className="dispatch-status"><Icon size={13} aria-hidden="true" />{label}</span>;
}

function duration(seconds: number) {
  if (seconds < 60) return seconds + "s";
  if (seconds < 3600) return Math.floor(seconds / 60) + "m " + seconds % 60 + "s";
  return Math.floor(seconds / 3600) + "h " + Math.floor(seconds % 3600 / 60) + "m";
}

function time(value: string) {
  if (!value || value.startsWith("0001-")) return null;
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : null;
}

function DispatchStages({ run }: { run: DispatchRun }) {
  const current = run.status === "queued" ? 0 : run.status === "running" ? 1 : 2;
  const steps = ["Queued", "Running", current === 2 ? states[run.status].label : "Result"];
  return <ol className="dispatch-stages" aria-label={`Run #${run.id} stages`}>
    {steps.map((label, i) => {
      const reached = i <= current && (i !== 1 || current === 1 || !!time(run.started_at));
      return <li key={i} data-reached={reached} aria-current={i === current ? "step" : undefined}>
        <span aria-hidden="true" />{label}
      </li>;
    })}
  </ol>;
}

export function BrainCard({ activity }: { activity: BrainActivity | null }) {
  const live = !!activity && !activity.finished;
  return <section className="dispatch-brain" data-live={live} aria-label="Brain activity in this chat">
    <div className="brain-line"><Brain size={15} aria-hidden="true" /><strong>Brain</strong>
      <span className="brain-scope">This chat</span>
      <span className="brain-phase" role="status">{activity?.phase ?? "No live turn"}</span>
    </div>
    {activity && <>
      {(!activity.finished || activity.failed) &&
        <p className="brain-context" key={activity.phase} title={activity.detail || activity.request}>
          {activity.detail || activity.request}
        </p>}
      {activity.lastHandoff && <p className="brain-handoff" title={activity.lastHandoff}>
        <span>Last handoff</span> {activity.lastHandoff}
      </p>}
    </>}
  </section>;
}

export function DispatchCard({ run, liveDetail, actions, onExpand }: {
  run: DispatchRun; liveDetail?: string; actions: ReactNode; onExpand: () => void;
}) {
  const note = run.status === "error" ? run.error || run.detail :
    run.status === "running" ? liveDetail || run.detail : run.status === "blocked" ? run.detail : "";
  return <div className="dispatch-card" data-status={run.status}>
    <div className="dispatch-top">
      <span className="dispatch-id">#{run.id}</span>
      <span className="dispatch-byline"><Bot size={13} /><span>{run.worker}</span>
        {run.status !== "queued" && <><i>·</i>{duration(run.elapsed_s)}</>}
      </span>
      <div className="dispatch-actions">{actions}</div>
    </div>
    <button type="button" className="dispatch-expand" onClick={onExpand}
      aria-haspopup="dialog" aria-label={`Expand run #${run.id}: ${run.task}`} title="Maximize — full details">
      <span className="dispatch-title">{run.task}</span><Maximize2 size={12} aria-hidden="true" />
    </button>
    <DispatchStages run={run} />
    {note && <p className="dispatch-note" title={note}>{note}</p>}
  </div>;
}

export function DispatchDetails({ run, liveDetail, onClose, onConversation }: {
  run: DispatchRun; liveDetail?: string; onClose: () => void; onConversation?: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const node = dialog.current!;
    node.showModal();
    return () => node.close();
  }, []);
  const active = run.status === "queued" || run.status === "running";
  const started = time(run.started_at);
  const ended = time(run.finished_at);
  const reportedTokens = (run.tokens_in || 0) + (run.tokens_out || 0);
  const fields = [
    ["Task", run.task], ["Detail", liveDetail || run.detail], ["Error", run.error], ["Result", run.result],
  ];
  return <dialog ref={dialog} className="dispatch-dialog" data-status={run.status}
    aria-labelledby="dispatch-detail-title" onCancel={onClose}
    onClick={e => { if (e.target === e.currentTarget) onClose(); }}>
    <div className="dispatch-sheet">
      <header className="dispatch-dialog-head">
        <div><Status run={run} /><h2 id="dispatch-detail-title">Run #{run.id} · {run.worker}</h2></div>
        <button type="button" autoFocus onClick={onClose} className="dispatch-tool" aria-label="Close details"><X size={18} /></button>
      </header>
      <div className="dispatch-content">
        <DispatchStages run={run} />
        <dl className="dispatch-facts">
          {started && <div><dt>Started</dt><dd>{started}</dd></div>}
          {ended && <div><dt>Finished</dt><dd>{ended}</dd></div>}
          {run.status !== "queued" && <div><dt>Elapsed</dt><dd>{duration(run.elapsed_s)}</dd></div>}
          {reportedTokens > 0 && <div><dt>Reported tokens</dt><dd>{reportedTokens.toLocaleString()}</dd></div>}
          {(!active || run.cost_usd > 0) && <div><dt>Reported cost</dt><dd>${run.cost_usd.toFixed(4)}</dd></div>}
        </dl>
        {fields.map(([label,content]) => content && <section key={label}>
          <h3>{label}</h3><p className={label === "Error" ? "dispatch-error-text" : ""}>{content}</p>
        </section>)}
      </div>
      {onConversation && <footer><button type="button" onClick={onConversation}><ExternalLink size={13} />Open conversation</button></footer>}
    </div>
  </dialog>;
}
