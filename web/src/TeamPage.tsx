import { useEffect, useState } from "react";
import { AlertTriangle, Ban, RefreshCw, RotateCcw, Users } from "lucide-react";
import { api, type TeamSnapshot } from "./api";
import { Button, Card } from "./components/ui";

// Team view (T4b, issue #6): pure observer over the agent-studio daemon.
// Board columns by task status, roster, health/limits, alerts, brief warnings.
// Ops limited to what the daemon exposes today: cancel / rerun / set_limits.
const COLUMNS: { id: string; label: string }[] = [
  { id: "pending", label: "Pending" },
  { id: "running", label: "Running" },
  { id: "failed", label: "Failed" },
  { id: "done", label: "Done" },
];

function columnFor(status: string): string {
  const s = (status || "").toLowerCase();
  if (COLUMNS.some((c) => c.id === s)) return s;
  if (["completed", "finished", "success"].includes(s)) return "done";
  return "pending";
}

export default function TeamPage() {
  const [snap, setSnap] = useState<TeamSnapshot | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [memberCaps, setMemberCaps] = useState("");
  const [agents, setAgents] = useState<Record<string, any>[]>([]);
  const [memberId, setMemberId] = useState("");
  const load = () => {
    setErr("");
    api.teamView().then(setSnap).catch((e) => setErr((e as Error).message));
  };
  useEffect(load, []);
  useEffect(() => {
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, []);
  // Pick-list source: the daemon's real agents — tasks dispatched to a roster
  // id that is not a real agent fail with "agent not found".
  useEffect(() => {
    api.teamAgents().then((r) => setAgents(r.agents ?? [])).catch(() => setAgents([]));
  }, []);

  const op = async (op: string, task_id?: string) => {
    setBusy(task_id ? `${op}:${task_id}` : op);
    try {
      await api.teamOp({ op, task_id });
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  // Add-member = the console-side team-creation entry: registers an existing
  // daemon agent on the team roster (team.roster_register pass-through).
  const addMember = async () => {
    if (!memberId) return;
    const agent = agents.find((a) => a.id === memberId);
    setBusy("register_member");
    try {
      await api.teamOp({
        op: "register_member",
        agent_id: memberId,
        name: (agent?.name as string) || memberId,
        capabilities: memberCaps.split(",").map((s) => s.trim()).filter(Boolean),
      });
      setMemberId("");
      setMemberCaps("");
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  // Only offer agents not already on the roster.
  const rosterIds = new Set((snap?.roster ?? []).map((m) => String(m.member_id || m.agent_id || m.name || "")));
  const candidates = agents.filter((a) => !rosterIds.has(String(a.id)));

  const tasks = snap?.tasks ?? [];
  const alerts = snap?.alerts ?? [];
  const brief = snap?.brief_warnings ?? {};

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold flex items-center gap-2"><Users size={18} /> Team</h2>
        <div className="flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
          {snap?.connected ? (
            <span className="rounded-lg bg-emerald-500/10 px-2 py-0.5 text-emerald-600 dark:text-emerald-400">live</span>
          ) : (
            <span className="rounded-lg bg-amber-500/10 px-2 py-0.5 text-amber-600 dark:text-amber-400">
              {snap?.error || "connecting…"}
            </span>
          )}
          {snap?.fetched_at && <span>{new Date(snap.fetched_at).toLocaleTimeString()}</span>}
          <Button variant="ghost" onClick={load}><RefreshCw size={14} /> Refresh</Button>
        </div>
      </div>

      {err && <p className="text-sm text-red-500">{err}</p>}

      {/* Members first — team creation is the primary entry on this page. */}
      <Card className="p-4">
        <h3 className="mb-2 text-sm font-medium">Members</h3>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <select value={memberId} onChange={(e) => {
            setMemberId(e.target.value);
            const a = agents.find((x) => x.id === e.target.value);
            const caps = (a?.capabilities as string[] | undefined)?.filter(Boolean) ?? [];
            setMemberCaps(caps.join(", "));
          }}
            className="w-56 rounded-xl border border-zinc-200/80 bg-transparent px-3 py-1.5 text-sm dark:border-zinc-700">
            <option value="">select an existing worker…</option>
            {candidates.map((a) => (
              <option key={String(a.id)} value={String(a.id)}>{String(a.name)} ({String(a.id)})</option>
            ))}
          </select>
          <input value={memberCaps} onChange={(e) => setMemberCaps(e.target.value)} placeholder="capabilities, comma-separated"
            className="min-w-48 flex-1 rounded-xl border border-zinc-200/80 bg-transparent px-3 py-1.5 text-sm dark:border-zinc-700" />
          <Button onClick={addMember} disabled={busy !== "" || !memberId}>Add member</Button>
        </div>
        {(snap?.roster ?? []).length === 0 ? (
          <p className="text-sm text-zinc-400">No roster reported by the daemon.</p>
        ) : (
          <div className="space-y-1.5">
            {snap!.roster.map((m, i) => (
              <div key={m.member_id || m.name || i} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-medium">{m.name || m.member_id}</span>
                {m.role && <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{m.role}</span>}
                {m.status && <span className="text-xs text-zinc-400">{m.status}</span>}
              </div>
            ))}
          </div>
        )}
      </Card>

      {alerts.length > 0 && (
        <Card className="border-amber-300/60 p-3 dark:border-amber-500/30">
          {alerts.slice(0, 5).map((a, i) => (
            <p key={i} className="flex items-start gap-1.5 text-sm text-amber-600 dark:text-amber-400">
              <AlertTriangle size={14} className="mt-0.5 shrink-0" /> {a}
            </p>
          ))}
          {alerts.length > 5 && (
            <p className="mt-1 text-xs text-zinc-400">+{alerts.length - 5} more alerts suppressed — export the failure log for the full list</p>
          )}
        </Card>
      )}

      {/* Board */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {COLUMNS.map((col) => {
          const colTasks = tasks.filter((t) => columnFor(t.status) === col.id);
          return (
            <div key={col.id} className="rounded-2xl border border-zinc-200/80 bg-zinc-50/50 p-2 dark:border-zinc-800 dark:bg-zinc-900/40">
              <p className="px-1 pb-2 text-xs font-medium text-zinc-500 dark:text-zinc-400">
                {col.label} <span className="tabular-nums">({colTasks.length})</span>
              </p>
              <div className="space-y-2">
                {colTasks.map((t) => (
                  <Card key={t.task_id || t.id} className="p-2.5">
                    <p className="break-words text-sm font-medium">{t.title || t.description || t.task_id || t.id}</p>
                    <p className="mt-1 truncate text-xs text-zinc-400" title={t.agent_id || ""}>{t.agent_id || ""}</p>
                    {brief[t.task_id || t.id] && (
                      <p className="mt-1 text-xs text-amber-500">
                        brief: {brief[t.task_id || t.id].length} contract item(s) missing
                      </p>
                    )}
                    {(col.id === "failed" || col.id === "running") && (
                      <div className="mt-2 flex gap-1">
                        {col.id === "failed" && (
                          <Button variant="ghost" onClick={() => op("rerun", t.task_id || t.id)} disabled={busy !== ""}>
                            <RotateCcw size={12} /> Rerun
                          </Button>
                        )}
                        <Button variant="ghost" onClick={() => op("cancel", t.task_id || t.id)} disabled={busy !== ""}>
                          <Ban size={12} /> Cancel
                        </Button>
                      </div>
                    )}
                  </Card>
                ))}
                {colTasks.length === 0 && <p className="px-1 py-3 text-xs text-zinc-300 dark:text-zinc-600">—</p>}
              </div>
            </div>
          );
        })}
      </div>

      {/* Health + limits */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Card className="p-4">
          <h3 className="mb-2 text-sm font-medium">Health</h3>
          {snap?.health ? (
            <dl className="space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
              {Object.entries(snap.health).filter(([, v]) => typeof v !== "object").slice(0, 8).map(([k, v]) => (
                <div key={k} className="flex gap-2"><dt className="w-32 shrink-0">{k}</dt><dd className="tabular-nums">{String(v)}</dd></div>
              ))}
            </dl>
          ) : <p className="text-sm text-zinc-400">—</p>}
        </Card>
        <Card className="p-4">
          <h3 className="mb-2 text-sm font-medium">Limits</h3>
          {snap?.limits ? (
            <dl className="space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
              {Object.entries(snap.limits).filter(([, v]) => typeof v !== "object").map(([k, v]) => (
                <div key={k} className="flex gap-2"><dt className="w-32 shrink-0">{k}</dt><dd className="tabular-nums">{String(v)}</dd></div>
              ))}
            </dl>
          ) : <p className="text-sm text-zinc-400">—</p>}
          <Button variant="ghost" className="mt-2" onClick={() => op("set_limits")} disabled={busy !== ""}>
            <RotateCcw size={12} /> Re-apply defaults
          </Button>
        </Card>
      </div>
    </div>
  );
}
