import { useEffect, useMemo, useState } from "react";
import { Brain, Bot, Sigma, RefreshCw, Table2, BarChart3, ShieldAlert, Download } from "lucide-react";
import { api, type DashboardBucket, type DashboardData, type FailureRow, type UsageRange, type WorkerStatus } from "./api";
import { Button, Card, useTheme } from "./components/ui";

// Palette validated with the dataviz validator (light + dark surfaces).
// Brain = indigo (chat:* sources), Workers = amber (agent:* sources).
const COLORS = {
  light: { brain: "#4f46e5", worker: "#d97706" },
  dark: { brain: "#6366f1", worker: "#d97706" },
};

const RANGES: { id: UsageRange; label: string }[] = [
  { id: "today", label: "Today" },
  { id: "week", label: "This week" },
  { id: "month", label: "This month" },
  { id: "all", label: "All time" },
];

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(n >= 10_000 ? 0 : 1) + "k";
  return String(n);
}

// Bucket strings come from SQL substr: hour "13", day "2026-11-13",
// month "2026-11". Turn each into a short display label.
function bucketLabel(bucket: string, unit: string): string {
  if (unit === "hour") return `${bucket}:00`;
  if (unit === "month") return bucket;
  const parts = bucket.split("-");
  return `${parts[1]}-${parts[2]}`;
}

export default function Dashboard() {
  const [tab, setTab] = useState<"usage" | "workers" | "failures">("usage");
  return (
    <div>
      <div className="flex items-center gap-1 border-b border-zinc-200/80 px-6 pt-4 dark:border-zinc-800">
        {([["usage", "Usage", BarChart3], ["workers", "Workers", Bot], ["failures", "Failures", ShieldAlert]] as const).map(([id, label, Icon]) => (
          <button
            key={id}
            onClick={() => setTab(id)}
            className={`inline-flex items-center gap-2 rounded-t-xl px-4 py-2 text-sm font-medium transition-colors ${
              tab === id
                ? "border border-b-0 border-zinc-200/80 bg-white text-indigo-600 dark:border-zinc-800 dark:bg-zinc-900 dark:text-indigo-400"
                : "text-zinc-500 hover:text-zinc-700 dark:text-zinc-400 dark:hover:text-zinc-200"
            }`}
          >
            <Icon size={15} /> {label}
          </button>
        ))}
      </div>
      {tab === "usage" ? <UsageTab /> : tab === "workers" ? <WorkersTab /> : <FailuresTab />}
    </div>
  );
}

// WorkersTab lists every configured worker with its runtime agent
// availability and lifetime spend (from the cost log, source agent:<name>).
function WorkersTab() {
  const [workers, setWorkers] = useState<WorkerStatus[] | null>(null);
  const [err, setErr] = useState("");
  const load = () => {
    setErr("");
    api.workersStatus().then((r) => setWorkers(r.workers ?? [])).catch((e) => setErr((e as Error).message));
  };
  useEffect(load, []);
  // Poll while mounted: busy/idle and current activity change live.
  useEffect(() => {
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, []);
  return (
    <div className="mx-auto max-w-5xl space-y-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">Worker status</h2>
        <Button variant="ghost" onClick={load}>
          <RefreshCw size={14} /> Refresh
        </Button>
      </div>
      {err && <p className="text-sm text-red-500">{err}</p>}
      {workers === null ? (
        <p className="text-sm text-zinc-400">loading…</p>
      ) : workers.length === 0 ? (
        <Card className="p-6 text-center text-sm text-zinc-400">
          No workers configured yet — set them up on the Agents page.
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          {workers.map((w) => (
            <Card key={w.name} className="p-4">
              <div className="flex flex-wrap items-center gap-2">
                <Bot size={15} className={w.busy ? "text-amber-500" : "text-indigo-500"} />
                <span className="font-medium">{w.name}</span>
                <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800">type: {w.agent}</span>
                {w.busy ? (
                  <span className="inline-flex items-center gap-1.5 rounded-lg bg-amber-500/10 px-1.5 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400">
                    <RefreshCw size={11} className="animate-spin" /> busy · {w.elapsed_s ?? 0}s
                  </span>
                ) : (
                  <span className="rounded-lg bg-emerald-500/10 px-1.5 py-0.5 text-xs text-emerald-600 dark:text-emerald-400">idle</span>
                )}
                {!w.enabled && <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800">disabled</span>}
                {!w.detected && <span className="text-xs text-amber-500">CLI not on PATH</span>}
              </div>
              {w.busy && (
                <div className="mt-2 rounded-xl bg-amber-500/5 px-3 py-2 text-xs dark:bg-amber-500/10">
                  <p className="truncate font-medium text-amber-600 dark:text-amber-400" title={w.task}>▸ {w.task}</p>
                  {w.detail && <p className="mt-0.5 truncate text-zinc-500 dark:text-zinc-400" title={w.detail}>{w.detail}</p>}
                </div>
              )}
              <dl className="mt-3 space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
                <div className="flex gap-2"><dt className="w-20 shrink-0">isolation</dt><dd className="truncate">{w.isolation || "worktree"}{w.repo ? ` · ${w.repo}` : ""}</dd></div>
                <div className="flex gap-2"><dt className="w-20 shrink-0">work dir</dt><dd className="truncate" title={w.work_dir}>{w.work_dir}</dd></div>
                <div className="flex gap-2"><dt className="w-20 shrink-0">budget</dt><dd>
                  cap ${(w.max_budget_usd ?? 5).toFixed(2)} / run · spent ${(w.cost_usd ?? 0).toFixed(2)} lifetime
                  {w.provider_id ? ` · provider ${w.provider_id}` : " · CLI default provider"}
                </dd></div>
              </dl>
              <div className="mt-3 grid grid-cols-2 gap-2 border-t border-zinc-200/80 pt-3 text-center dark:border-zinc-800">
                <div><p className="text-sm font-semibold">{w.calls}</p><p className="text-xs text-zinc-500">runs</p></div>
                <div><p className="text-sm font-semibold">{fmtTokens((w.tokens_in ?? 0) + (w.tokens_out ?? 0))}</p><p className="text-xs text-zinc-500">tokens</p></div>
              </div>
              <p className="mt-2 text-xs text-zinc-400">{w.last_run ? `last run ${w.last_run} UTC` : "no runs yet"}</p>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

function UsageTab() {
  const { dark } = useTheme();
  const [range, setRange] = useState<UsageRange>("week");
  const [data, setData] = useState<DashboardData | null>(null);
  const [view, setView] = useState<"chart" | "table">("chart");
  const [err, setErr] = useState("");
  const [hover, setHover] = useState<number | null>(null);

  const load = useMemo(
    () => (r: UsageRange) => {
      setErr("");
      api
        .dashboard(r)
        .then(setData)
        .catch((e) => setErr((e as Error).message));
    },
    []
  );

  useEffect(() => {
    load(range);
  }, [range, load]);

  const colors = dark ? COLORS.dark : COLORS.light;
  const buckets = data?.buckets ?? [];
  const totals = data?.totals;

  const economics = useMemo(() => {
    const inT = buckets.reduce((s, b) => s + b.brain_tokens_in + b.worker_tokens_in, 0);
    const outT = buckets.reduce((s, b) => s + b.brain_tokens_out + b.worker_tokens_out, 0);
    const brain = totals?.brain_tokens ?? 0;
    const worker = totals?.worker_tokens ?? 0;
    const all = brain + worker;
    return {
      in: inT,
      out: outT,
      inOutRatio: inT === 0 && outT === 0 ? "—" : outT === 0 ? "1 : 0" : (inT / outT).toFixed(1) + " : 1",
      brainPct: all === 0 ? 0 : Math.round((brain / all) * 100),
      worker,
    };
  }, [buckets, totals]);

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">Usage dashboard</h2>
        <div className="flex items-center gap-1 rounded-xl border border-zinc-200/80 dark:border-zinc-800 p-1">
          {RANGES.map((r) => (
            <button
              key={r.id}
              onClick={() => setRange(r.id)}
              className={`rounded-lg px-3 py-1 text-sm transition-colors ${
                range === r.id
                  ? "bg-indigo-600 text-white shadow-sm"
                  : "text-zinc-500 hover:bg-zinc-200/60 dark:text-zinc-400 dark:hover:bg-zinc-800/60"
              }`}
            >
              {r.label}
            </button>
          ))}
        </div>
      </div>

      {err && <p className="text-sm text-red-500">{err}</p>}

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <StatTile icon={<Brain size={16} />} label="Brain tokens" value={totals ? fmtTokens(totals.brain_tokens) : "—"} sub="chat turns" swatch={colors.brain} />
        <StatTile icon={<Bot size={16} />} label="Worker tokens" value={totals ? fmtTokens(totals.worker_tokens) : "—"} sub="agent runs" swatch={colors.worker} />
        <StatTile icon={<Sigma size={16} />} label="Total tokens" value={totals ? fmtTokens(totals.brain_tokens + totals.worker_tokens) : "—"} sub={`${buckets.length} ${data?.bucket_unit ?? ""} buckets`} />
      </div>

      {/* Economics (issue #4, C9): input:output mix and brain-vs-worker share
          — orchestration overhead is visible as a bloated brain share. */}
      <Card className="p-4">
        <h3 className="mb-3 text-sm font-medium">Token economics</h3>
        {buckets.length === 0 ? (
          <p className="text-sm text-zinc-400">No data yet.</p>
        ) : (
          <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-3">
            <div>
              <dt className="text-xs text-zinc-500 dark:text-zinc-400">Input : output</dt>
              <dd className="mt-0.5 font-semibold tabular-nums">{economics.inOutRatio}</dd>
              <p className="text-xs text-zinc-400">{fmtTokens(economics.in)} in · {fmtTokens(economics.out)} out</p>
            </div>
            <div>
              <dt className="text-xs text-zinc-500 dark:text-zinc-400">Brain share</dt>
              <dd className="mt-0.5 font-semibold tabular-nums">{economics.brainPct}%</dd>
              <p className="text-xs text-zinc-400">{economics.brainPct > 50 ? "planner overhead dominates" : "healthy planner ratio"}</p>
            </div>
            <div>
              <dt className="text-xs text-zinc-500 dark:text-zinc-400">Worker share</dt>
              <dd className="mt-0.5 font-semibold tabular-nums">{100 - economics.brainPct}%</dd>
              <p className="text-xs text-zinc-400">{fmtTokens(economics.worker)} dispatched</p>
            </div>
          </dl>
        )}
      </Card>

      <Card className="p-4">
        <div className="mb-3 flex items-center justify-between">
          <div className="flex items-center gap-4 text-xs text-zinc-600 dark:text-zinc-400">
            <span className="inline-flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-sm" style={{ background: colors.brain }} /> Brain (chat)
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-sm" style={{ background: colors.worker }} /> Workers (agents)
            </span>
          </div>
          <Button variant="ghost" onClick={() => setView(view === "chart" ? "table" : "chart")}>
            {view === "chart" ? <Table2 size={14} /> : <BarChart3 size={14} />}
            {view === "chart" ? "Table" : "Chart"}
          </Button>
        </div>

        {buckets.length === 0 ? (
          <p className="flex h-40 items-center justify-center text-sm text-zinc-400">
            No usage recorded in this window yet.
          </p>
        ) : view === "chart" ? (
          <UsageChart buckets={buckets} unit={data?.bucket_unit ?? "day"} colors={colors} hover={hover} setHover={setHover} />
        ) : (
          <UsageTable buckets={buckets} unit={data?.bucket_unit ?? "day"} />
        )}
      </Card>

      <div className="flex justify-end">
        <Button variant="ghost" onClick={() => load(range)}>
          <RefreshCw size={14} /> Refresh
        </Button>
      </div>
    </div>
  );
}

// FailuresTab shows the MAST failure-tag log (issue #4): auto-recorded
// dispatch errors and brief warnings, plus manual notes, with JSONL export.
const FAILURE_CATEGORIES = ["fc1_brief", "fc2_handoff", "fc3_tool", "other"] as const;
const CATEGORY_HINT: Record<string, string> = {
  fc1_brief: "defective task brief",
  fc2_handoff: "brain/worker misalignment",
  fc3_tool: "tool misuse or tool error",
  other: "other",
};

function FailuresTab() {
  const [rows, setRows] = useState<FailureRow[] | null>(null);
  const [err, setErr] = useState("");
  const [agent, setAgent] = useState("");
  const [category, setCategory] = useState<string>("other");
  const [note, setNote] = useState("");

  const load = () => {
    setErr("");
    api.failuresList().then((r) => setRows(r.failures ?? [])).catch((e) => setErr((e as Error).message));
  };
  useEffect(load, []);

  const submit = async () => {
    if (!note.trim()) return;
    try {
      await api.failuresAdd({ agent: agent.trim(), category, note: note.trim() });
      setNote("");
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">Failure log</h2>
        <div className="flex items-center gap-2">
          <a href="/api/failures/export" download>
            <Button variant="ghost"><Download size={14} /> Export JSONL</Button>
          </a>
          <Button variant="ghost" onClick={load}><RefreshCw size={14} /> Refresh</Button>
        </div>
      </div>
      <p className="text-xs text-zinc-500 dark:text-zinc-400">
        Auto-tagged per the MAST taxonomy: fc1_brief on weak dispatch briefs, fc3_tool on agent errors. Add manual notes below.
      </p>

      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-2">
          <input value={agent} onChange={(e) => setAgent(e.target.value)} placeholder="agent (optional)"
            className="w-40 rounded-xl border border-zinc-200/80 bg-transparent px-3 py-1.5 text-sm dark:border-zinc-700" />
          <select value={category} onChange={(e) => setCategory(e.target.value)}
            className="rounded-xl border border-zinc-200/80 bg-transparent px-2 py-1.5 text-sm dark:border-zinc-700">
            {FAILURE_CATEGORIES.map((c) => <option key={c} value={c}>{c} — {CATEGORY_HINT[c]}</option>)}
          </select>
          <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="what failed and why"
            className="min-w-48 flex-1 rounded-xl border border-zinc-200/80 bg-transparent px-3 py-1.5 text-sm dark:border-zinc-700" />
          <Button onClick={submit}>Add</Button>
        </div>
      </Card>

      {err && <p className="text-sm text-red-500">{err}</p>}
      {rows === null ? (
        <p className="text-sm text-zinc-400">loading…</p>
      ) : rows.length === 0 ? (
        <Card className="p-6 text-center text-sm text-zinc-400">No failure tags recorded yet.</Card>
      ) : (
        <div className="space-y-2">
          {rows.map((f) => (
            <Card key={f.id} className="p-3">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className={`rounded-lg px-1.5 py-0.5 text-xs font-medium ${
                  f.category === "fc1_brief" ? "bg-amber-500/10 text-amber-600 dark:text-amber-400"
                  : f.category === "fc3_tool" ? "bg-red-500/10 text-red-600 dark:text-red-400"
                  : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"}`}>
                  {f.category}
                </span>
                {f.agent && <span className="font-medium">{f.agent}</span>}
                <span className="text-xs text-zinc-400">{f.created_at} UTC</span>
              </div>
              <p className="mt-1 break-words text-sm text-zinc-600 dark:text-zinc-300">{f.note}</p>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

function StatTile({ icon, label, value, sub, swatch }: { icon: React.ReactNode; label: string; value: string; sub: string; swatch?: string }) {
  return (
    <Card className="p-4">
      <p className="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
        {swatch && <span className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: swatch }} />}
        {icon} {label}
      </p>
      <p className="mt-1 text-2xl font-semibold tabular-nums tracking-tight">{value}</p>
      {sub && <p className="text-xs text-zinc-400">{sub}</p>}
    </Card>
  );
}

function UsageChart({
  buckets, unit, colors, hover, setHover,
}: {
  buckets: DashboardBucket[];
  unit: string;
  colors: { brain: string; worker: string };
  hover: number | null;
  setHover: (i: number | null) => void;
}) {
  const W = 720;
  const H = 240;
  const PAD = { top: 16, right: 8, bottom: 24, left: 44 };
  const plotW = W - PAD.left - PAD.right;
  const plotH = H - PAD.top - PAD.bottom;
  const GAP = 2; // surface gap between stacked segments
  const barW = Math.min(36, Math.max(8, (plotW / buckets.length) * 0.6));

  const maxTotal = Math.max(
    ...buckets.map((b) => b.brain_tokens_in + b.brain_tokens_out + b.worker_tokens_in + b.worker_tokens_out),
    1
  );
  const yTicks = 4;
  const labelEvery = Math.ceil(buckets.length / 12);
  const maxIdx = buckets.reduce((mi, b, i) =>
    b.brain_tokens_in + b.brain_tokens_out + b.worker_tokens_in + b.worker_tokens_out >
    buckets[mi].brain_tokens_in + buckets[mi].brain_tokens_out + buckets[mi].worker_tokens_in + buckets[mi].worker_tokens_out
      ? i : mi, 0);

  const y = (v: number) => PAD.top + plotH - (v / maxTotal) * plotH;
  const x = (i: number) => PAD.left + (plotW / buckets.length) * (i + 0.5);

  const hovered = hover != null ? buckets[hover] : null;

  return (
    <div className="relative">
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label="Tokens per bucket, brain vs workers">
        {/* recessive y grid */}
        {Array.from({ length: yTicks + 1 }, (_, t) => {
          const v = (maxTotal / yTicks) * t;
          return (
            <g key={t}>
              <line x1={PAD.left} x2={W - PAD.right} y1={y(v)} y2={y(v)} className="stroke-zinc-200/70 dark:stroke-zinc-800" strokeWidth={1} />
              <text x={PAD.left - 6} y={y(v) + 4} textAnchor="end" className="fill-zinc-400 text-[10px] tabular-nums">
                {fmtTokens(v)}
              </text>
            </g>
          );
        })}

        {buckets.map((b, i) => {
          const brainH = ((b.brain_tokens_in + b.brain_tokens_out) / maxTotal) * plotH;
          const workerH = ((b.worker_tokens_in + b.worker_tokens_out) / maxTotal) * plotH;
          const bx = x(i) - barW / 2;
          const brainY = PAD.top + plotH - brainH;
          const workerY = brainY - GAP - workerH;
          // 4px rounded top only on the topmost visible segment
          const topSeg = workerH > 0 ? { yy: workerY, hh: workerH, fill: colors.worker } : { yy: brainY, hh: brainH, fill: colors.brain };
          return (
            <g key={b.bucket}>
              {brainH > 0 && (
                <rect
                  x={bx} y={brainY} width={barW} height={brainH} fill={colors.brain}
                  rx={topSeg.fill === colors.brain ? 4 : 0}
                  style={topSeg.fill === colors.brain ? { clipPath: "inset(0 0 round 4px 4px 0 0)" } : undefined}
                />
              )}
              {workerH > 0 && (
                <rect
                  x={bx} y={workerY} width={barW} height={workerH} fill={colors.worker}
                  rx={4}
                  style={{ clipPath: "inset(0 0 round 4px 4px 0 0)" }}
                />
              )}
              {/* selective direct label: peak bucket only */}
              {i === maxIdx && (
                <text x={x(i)} y={Math.min(workerY, brainY) - 5} textAnchor="middle" className="fill-zinc-500 dark:fill-zinc-400 text-[10px] font-medium tabular-nums">
                  {fmtTokens(b.brain_tokens_in + b.brain_tokens_out + b.worker_tokens_in + b.worker_tokens_out)}
                </text>
              )}
              {/* x labels, thinned */}
              {i % labelEvery === 0 && (
                <text x={x(i)} y={H - 8} textAnchor="middle" className="fill-zinc-400 text-[10px] tabular-nums">
                  {bucketLabel(b.bucket, unit)}
                </text>
              )}
              {/* hover hit target: full column, wider than the mark */}
              <rect
                x={x(i) - (plotW / buckets.length) / 2} y={PAD.top}
                width={plotW / buckets.length} height={plotH}
                fill="transparent"
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover(null)}
              />
            </g>
          );
        })}
      </svg>

      {hovered && hover != null && (
        <div
          className="pointer-events-none absolute z-10 w-48 rounded-xl border border-zinc-200/80 bg-white p-2.5 text-xs shadow-md dark:border-zinc-700 dark:bg-zinc-900"
          style={{
            left: `${((x(hover) - PAD.left) / W) * 100}%`,
            top: 0,
            transform: `translateX(${hover > buckets.length / 2 ? "-110%" : "10%"})`,
          }}
        >
          <p className="mb-1 font-medium text-zinc-700 dark:text-zinc-300">{bucketLabel(hovered.bucket, unit)}</p>
          <p className="flex items-center gap-1.5 text-zinc-600 dark:text-zinc-400">
            <span className="h-2 w-2 rounded-sm" style={{ background: colors.brain }} />
            brain {fmtTokens(hovered.brain_tokens_in + hovered.brain_tokens_out)}
          </p>
          <p className="flex items-center gap-1.5 text-zinc-600 dark:text-zinc-400">
            <span className="h-2 w-2 rounded-sm" style={{ background: colors.worker }} />
            workers {fmtTokens(hovered.worker_tokens_in + hovered.worker_tokens_out)}
          </p>
        </div>
      )}
    </div>
  );
}

function UsageTable({ buckets, unit }: { buckets: DashboardBucket[]; unit: string }) {
  return (
    <div className="max-h-80 overflow-y-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-zinc-200/80 dark:border-zinc-800 text-left text-xs text-zinc-500">
            <th className="py-1.5 pr-3 font-medium">{unit}</th>
            <th className="py-1.5 pr-3 text-right font-medium">Brain tokens</th>
            <th className="py-1.5 text-right font-medium">Worker tokens</th>
          </tr>
        </thead>
        <tbody className="tabular-nums">
          {buckets.map((b) => (
            <tr key={b.bucket} className="border-b border-zinc-100 dark:border-zinc-800/50">
              <td className="py-1.5 pr-3 text-zinc-600 dark:text-zinc-400">{bucketLabel(b.bucket, unit)}</td>
              <td className="py-1.5 pr-3 text-right">{fmtTokens(b.brain_tokens_in + b.brain_tokens_out)}</td>
              <td className="py-1.5 text-right">{fmtTokens(b.worker_tokens_in + b.worker_tokens_out)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
