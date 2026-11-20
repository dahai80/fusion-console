import { useEffect, useState } from "react";
import { Activity, RefreshCw, ShieldAlert, Download } from "lucide-react";
import { api, type FailureRow, type UsageRange } from "./api";
import TaskOverview from "./dashboard/TaskOverview";
import { Button, Card } from "./components/ui";

export default function Dashboard() {
  const [tab, setTab] = useState<"overview" | "failures">("overview");
  const [range, setRange] = useState<UsageRange>("week");
  return (
    <div className="h-full overflow-y-auto">
      <div className="flex overflow-x-auto items-center gap-1 border-b border-zinc-200/80 px-6 pt-4 dark:border-zinc-800">
        {([["overview", "Overview", Activity], ["failures", "Failures", ShieldAlert]] as const).map(([id, label, Icon]) => (
          <button
            key={id}
            aria-pressed={tab === id}
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
      {tab === "overview" ? <TaskOverview range={range} onRange={setRange} /> : <FailuresTab />}
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
