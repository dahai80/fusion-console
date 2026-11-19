import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  Activity, ArrowRight, Bot, ChartGantt, ChevronDown,
  CircleAlert, ClipboardList, Clock3, Coins, FlaskConical, Layers3, Radio,
  RotateCcw, Timer, UsersRound,
} from "lucide-react";
import { PeriodPicker, tokenNumber, useTokenUsage, type PeriodProps } from "./TokenUsage";
import { api } from "../api";
import { clock, duration, estimateEnd, formatTokens, matchesFilter, runtimeHistory, timestamp, workerState } from "./overview";
import type { Run, Snapshot, TaskFilter, Worker } from "./overview";
import { sampleSnapshot } from "./sample";
import "./overview.css";

const filters: TaskFilter[] = ["All", "Running", "Queued", "Attention", "Done"];
const tones: Record<string, string> = { running: "indigo", queued: "amber", done: "green", blocked: "amber", error: "red" };
const workerName = (r: Run) => r.worker || r.agent;
type History = ReturnType<typeof runtimeHistory>;

type UsageControls = PeriodProps & { onUsage: () => void };

export default function TaskOverview(props: UsageControls) {
  const [sample, setSample] = useState(() => new URLSearchParams(location.search).get("dashboard") === "sample");
  const [advanced, setAdvanced] = useState(false);
  const [live, setLive] = useState<Snapshot>();
  const [error, setError] = useState("");
  useEffect(() => {
    if (sample) return;
    let alive = true;
    let loading = false;
    const refresh = async () => {
      if (loading || document.hidden) return;
      loading = true;
      try {
        const [dispatch, pool] = await Promise.all([api.dispatches(), api.workersStatus()]);
        if (alive) {
          setLive({ runs: dispatch.dispatches ?? [], workers: pool.workers ?? [], now: Date.now() });
          setError("");
        }
      } catch (e) {
        if (alive) setError((e as Error).message || "Could not refresh dashboard data.");
      } finally {
        loading = false;
      }
    };
    void refresh();
    const interval = window.setInterval(refresh, 10000);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      alive = false;
      clearInterval(interval);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [sample]);
  const demo = useMemo(() => sampleSnapshot(advanced), [advanced]);
  const data = sample ? demo : live;
  return (
    <div className="ops">
      <header className="ops-heading">
        <div><h1>Task overview</h1><p>Background dispatches and worker availability.</p></div>
        <div className="ops-controls">
          <div className="ops-switch" aria-label="Data source" role="group">
            <button aria-pressed={!sample} onClick={() => setSample(false)}><Radio />Live data</button>
            <button aria-pressed={sample} onClick={() => setSample(true)}><FlaskConical />Sample</button>
          </div>
          {sample && <button className="ops-action" onClick={() => setAdvanced(v => !v)}>
            {advanced ? <RotateCcw /> : <ArrowRight />}{advanced ? "Replay sample" : "Next event"}
          </button>}
        </div>
      </header>
      {!sample && error && <p className="ops-notice" role="status"><CircleAlert size={15} />Refresh failed · {error}{live && " · Showing the last snapshot."}</p>}
      {data ? <OverviewBody key={sample ? "sample" : "live"} data={data} sample={sample} {...props} /> :
        <p className="ops-empty" role="status">{error ? "Waiting for a successful refresh." : "Loading dispatches, workers and usage…"}</p>}
    </div>
  );
}

function OverviewBody({ data, sample, ...controls }: { data: Snapshot; sample: boolean } & UsageControls) {
  const { runs, workers, now } = data;
  const history = useMemo(() => runtimeHistory(runs), [runs]);
  const queued = runs.filter(r => r.status === "queued");
  const running = runs.filter(r => r.status === "running");
  const assigned = new Set(running.map(workerName)).size;
  const ready = workers.filter(w => workerState(w) === "Available").length;
  const attention = runs.filter(r => matchesFilter(r.status, "Attention")).length;
  const estimates = running.map(r => ({ end: estimateEnd(r, history), name: workerName(r) }))
    .filter((x): x is { end: number; name: string } => x.end !== undefined && x.end > now).sort((a, b) => a.end - b.end);
  const next = estimates[0];
  const overdue = running.some(r => (estimateEnd(r, history) ?? Infinity) <= now);
  return <>
    <div className="ops-metrics">
      <Metric icon={<Layers3 />} title="Queued" value={queued.length} tone="amber" note="Waiting to start" />
      <Metric icon={<Activity />} title="In progress" value={running.length} tone="indigo" note={`${assigned} assigned worker${assigned === 1 ? "" : "s"}`} />
      <Metric icon={<UsersRound />} title="Available workers" value={<>{ready}<small> / {workers.length}</small></>} tone="green" note="Enabled and ready for work" />
      <Metric icon={<Timer />} title="Next estimated finish" value={next ? <>{clock(next.end)}<small> UTC</small></> : "—"} tone="zinc"
        note={next ? `${next.name} · in ~${duration((next.end - now) / 1000)}` : overdue ? "Past historical estimate" : running.length ? "More run history needed" : "No running dispatches"} />
    </div>
    <div className="ops-grid">
      <Timeline runs={runs} now={now} history={history} />
      <TokenPanel data={data} {...controls} />
      <TaskList runs={runs} history={history} now={now} attention={attention} />
      <WorkerPool workers={workers} />
    </div>
    <footer className="ops-footer"><span><Clock3 />{sample ? "Sample snapshot" : "Last updated"} · {new Date(now).toISOString().slice(0, 10)} {clock(now)} UTC</span>
      {sample ? "Illustrative data" : "Refreshes every 10s while visible"}</footer>
  </>;
}

function Metric({ icon, title, value, tone, note }: { icon: ReactNode; title: string; value: ReactNode; tone: string; note: string }) {
  return <section className="ops-metric"><div className="ops-metric-title"><span className={`ops-icon ${tone}`}>{icon}</span>{title}</div>
    <p className="ops-number">{value}</p><p className="ops-muted">{note}</p></section>;
}

function PanelTitle({ icon, title, aside }: { icon: ReactNode; title: string; aside?: ReactNode }) {
  return <div className="ops-panel-head"><h2>{icon}{title}</h2>{aside}</div>;
}

function Timeline({ runs, now, history }: { runs: Run[]; now: number; history: History }) {
  const quarter = 15 * 60000;
  const end = Math.floor(now / quarter) * quarter + 2 * quarter;
  const start = end - 4 * quarter;
  const pct = (t: number) => Math.min(100, Math.max(0, (t - start) / (end - start) * 100));
  const rows = runs.flatMap(run => {
    const from = timestamp(run.started_at);
    const to = run.status === "running" ? now : timestamp(run.finished_at);
    if (from === undefined || to === undefined || to < start || from > now || to < from) return [];
    return [{ run, from, to }];
  }).sort((a, b) => Number(b.run.status === "running") - Number(a.run.status === "running") || b.from - a.from);
  const visible = rows.slice(0, 5);
  return <section className="ops-panel">
    <PanelTitle icon={<ChartGantt />} title="Execution timeline" aside={<span className="ops-meta">1 hour · UTC</span>} />
    {visible.length ? <div className="ops-chart" role="img" aria-label={`Execution timeline at ${clock(now)} UTC. ` + visible.map(({ run, from, to }) =>
      `${workerName(run)}, dispatch ${run.id}, ${run.status}, ${clock(from)} to ${clock(to)}`).join(". ")}>
      <div className="ops-ylabels" aria-hidden="true">{visible.map(({ run }) => <div key={run.id}><b>{workerName(run)}</b><small>#{run.id}</small></div>)}</div>
      <div className="ops-plot">
        <div className="ops-axis">{[0, 1, 2, 3, 4].map(i => <span key={i} style={{ left: i * 25 + "%" }}>{clock(start + i * quarter)}</span>)}</div>
        <div className="ops-now" style={{ transform: `translateX(${pct(now)}%)` }}><span>Now</span></div>
        {visible.map(({ run, from, to }) => {
          const estimate = estimateEnd(run, history);
          return <div className="ops-lane" key={run.id} title={`#${run.id} · ${run.task} · ${clock(from)}–${clock(to)} UTC`}>
            <div className={`ops-bar ${run.status === "running" ? "actual" : matchesFilter(run.status, "Attention") ? "issue" : "finished"}`}
              style={{ left: pct(from) + "%", width: (pct(to) - pct(from)) + "%" }} />
            {estimate !== undefined && estimate > now && <div className="ops-bar forecast" style={{ left: pct(now) + "%", width: (pct(estimate) - pct(now)) + "%" }} />}
          </div>;
        })}
      </div>
    </div> : <p className="ops-empty">No dispatch activity in this time window.</p>}
    <div className="ops-legend"><span><i className="actual" />Running</span><span><i className="forecast" />Estimated</span>
      <span><i className="finished" />Done</span><span><i className="issue" />Attention</span>
      {rows.length > 5 && <small>5 of {rows.length} runs</small>}</div>
  </section>;
}

function TokenPanel({ data, range, onRange, onUsage }: {data: Snapshot} & UsageControls) {
  const sample = data.sampleTokens;
  const { data: usage, error } = useTokenUsage(range, !sample);
  const brain = sample?.brain_tokens ?? usage?.totals.brain_tokens ?? 0;
  const workers = sample?.worker_tokens ?? usage?.totals.worker_tokens ?? 0;
  const total = brain + workers;
  const measured = !!sample || !!usage?.totals.measured;
  const people = sample ? data.workers.map(w => {
    const runs = data.runs.filter(r => workerName(r) === w.name && (r.tokens_in || r.tokens_out));
    return { name: w.name, measured: runs.length, value: runs.reduce((n,r) => n+r.tokens_in+r.tokens_out,0) };
  }) : (usage?.participants ?? []).filter(r => r.role === "worker").map(r => ({ name:r.name, measured:r.measured, value:r.tokens_in+r.tokens_out }));
  return <section className="ops-panel ops-token" aria-label="Token usage">
    <PanelTitle icon={<Coins />} title="Token usage" aside={<span className="ops-meta">{sample ? "Sample · UTC" : "UTC"}</span>} />
    <div className="ops-token-body"><PeriodPicker range={range} onRange={onRange} />
      {error && <p role="status" className="ops-muted">{error}</p>}
      <div className="ops-ring" style={{background: total ? `conic-gradient(var(--brain) ${100*brain/total}%, var(--worker) 0)` : "var(--line)"}}>
        <div><strong title={measured ? tokenNumber(total) : undefined}>{measured ? formatTokens(total) : "—"}</strong><small>Total tokens</small></div>
      </div>
      <div className="ops-token-key">{[
        {label:"Brain · 大脑", value:brain, reported:!!sample || !!usage?.participants[0]?.measured, tone:"brain"},
        {label:"Workers 合计", value:workers, reported:people.some(w=>w.measured), tone:"worker"}
      ].map(r => <div key={r.tone}><span><i className={`ops-key ${r.tone}`}/>{r.label}</span><b>{r.reported ? tokenNumber(r.value) : "—"}</b>
        <small>{r.reported && total ? (100*r.value/total).toFixed(1)+"%" : "—"}</small></div>)}</div>
      {people.length > 0 && <ul className="token-overview-list" aria-label="各 Worker 用量">{people.map(w =>
        <li key={w.name}><span>{w.name}</span><span title={w.measured ? undefined : "无已报告用量"}>{w.measured ? tokenNumber(w.value) : "—"}</span></li>)}</ul>}
      <div className="token-overview-foot"><span>{sample ? "示例数据仅覆盖一天" : usage?.start ? usage.start+" — "+usage.end : usage ? "无用量记录" : "读取中…"}</span>
        {!sample && <button onClick={onUsage}>查看明细 →</button>}</div>
    </div>
  </section>;
}

function TaskList({ runs, history, now, attention }: { runs: Run[]; history: History; now: number; attention: number }) {
  const [filter, setFilter] = useState<TaskFilter>("All");
  const [expanded, setExpanded] = useState<number>();
  const [limit, setLimit] = useState(5);
  const ordered = runs.filter(r => matchesFilter(r.status, filter)).sort((a, b) => {
    const rank = (s: string) => s === "running" ? 0 : s === "queued" ? 1 : matchesFilter(s, "Attention") ? 2 : 3;
    return rank(a.status) - rank(b.status) || (a.status === "queued" ? a.id - b.id : b.id - a.id);
  });
  function choose(value: TaskFilter) {
    setFilter(value);
    setExpanded(undefined);
    setLimit(5);
  }
  return <section className="ops-panel ops-tasks">
    <PanelTitle title="Task queue" icon={<ClipboardList />} aside={attention > 0 && <button className="ops-attention" onClick={() => choose("Attention")}><CircleAlert />{attention} {attention === 1 ? "needs" : "need"} attention</button>} />
    <div className="ops-tabs" role="group" aria-label="Filter tasks">{filters.map(f =>
      <button key={f} aria-pressed={filter === f} onClick={() => choose(f)}>{f}</button>)}</div>
    <div className="ops-col-head ops-task-grid"><span>Task / Worker</span><span>Status</span><span>Time</span><span /></div>
    {ordered.slice(0, limit).map(r => {
      const start = timestamp(r.started_at);
      const estimate = estimateEnd(r, history);
      const typical = history.get(workerName(r));
      let time = r.elapsed_s > 0 ? duration(r.elapsed_s) : "—";
      if (r.status === "queued") time = typical ? `~${duration(typical.seconds)} run` : "Pending";
      if (r.status === "running") {
        if (estimate !== undefined) time = estimate > now ? `~${duration((estimate - now) / 1000)} left` : "Past est.";
        else time = start === undefined ? "In progress" : `${duration(Math.max(0, (now - start) / 1000))} elapsed`;
      }
      return <div className="ops-task" key={r.id}>
        <button className="ops-task-grid ops-row" aria-expanded={expanded === r.id} onClick={() => setExpanded(v => v === r.id ? undefined : r.id)}>
          <span className="ops-truncate"><b>{r.task}</b><small>#{r.id} <span>·</span> {workerName(r)}</small></span>
          <span key={r.status} className={`ops-status ${tones[r.status] ?? "zinc"}`}><i />{r.status}</span>
          <span className="ops-time">{time}</span><ChevronDown className={expanded === r.id ? "open" : ""} />
        </button>
        {expanded === r.id && <div className="ops-detail"><p>{r.task}</p>
          {(r.error || r.detail) && <p>{r.error || r.detail}</p>}
          <dl><div><dt>Started (UTC)</dt><dd>{start === undefined ? "—" : new Date(start).toISOString().slice(0, 16).replace("T", " ")}</dd></div>
            <div><dt>Reported tokens</dt><dd>{(r.tokens_in + r.tokens_out).toLocaleString()}</dd></div>
            <div><dt>Typical runtime</dt><dd>{typical ? `${duration(typical.seconds)} · median of ${typical.count} runs` : "Needs 3 completed runs"}</dd></div></dl>
        </div>}
      </div>;
    })}
    {ordered.length === 0 && <p className="ops-empty">{filter === "All" ? "No background dispatches yet." : `No tasks in this view.`}</p>}
    {ordered.length > limit && <button className="ops-more" onClick={() => setLimit(l => l + 10)}>Show more · {ordered.length - limit} remaining <ChevronDown size={13} /></button>}
  </section>;
}

function WorkerPool({ workers }: { workers: Worker[] }) {
  const busy = workers.filter(w => w.busy).length;
  const list = [...workers].sort((a, b) => Number(!!b.busy) - Number(!!a.busy) || a.name.localeCompare(b.name));
  return <section className="ops-panel ops-pool">
    <PanelTitle icon={<Bot />} title="Worker pool" aside={<span className="ops-meta">{busy} busy / {workers.length}</span>} />
    {list.length ? <div className="ops-workers">{list.map(w => {
      const state = workerState(w);
      return <div className="ops-worker" key={w.name}><span className="ops-avatar"><Bot size={17} /></span>
        <div><b>{w.name}</b><small title={w.task || w.agent}>{w.busy ? (w.task || "Task in progress") : w.agent}</small></div>
        <span className={`ops-state ${state === "Busy" ? "indigo" : state === "Available" ? "green" : "zinc"}`}><i />{state}</span></div>;
    })}</div> : <p className="ops-empty">No workers configured.</p>}
  </section>;
}
