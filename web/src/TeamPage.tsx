import { useEffect, useState } from "react";
import { AlarmClock, AlertTriangle, Ban, BookOpen, Eye, Pause, Play, RefreshCw, RotateCcw, Square, Users, X } from "lucide-react";
import { api, ApiError, type LeadStatus, type TeamSnapshot } from "./api";
import { Button, Card, Select } from "./components/ui";
import ReasonCard from "./components/ReasonCard";
import RunDrawer from "./components/RunDrawer";

// Team view (T4b, issue #6): pure observer over the agent-studio daemon.
// Board columns by task status, roster, health/limits, alerts, brief warnings.
// Ops: cancel / rerun / set_limits plus lifecycle pause/resume/disband and the
// lead loop start/stop (upstream RPCs landed in fusion-agent-studio). The
// playbook panel (issue #14) renders from teams.playbook_render/trace and
// hides itself for playbook:false teams. Team creation and member management
// live in the chat sidebar team panel; this page is ops + observability only.
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

// WakeupCard (issue #45 F4): humans arm a wakeup on a blocked task (kind
// at/every, expiry, timeout action), fire it now, or cancel the task. The
// armed count renders only when the daemon exposes task metadata — it does
// not today, so the counter stays hidden rather than showing a fake 0/8.
function WakeupCard({
  team,
  taskId,
  title,
  onDone,
  onCancel,
}: {
  team: string | undefined;
  taskId: string;
  title: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [kind, setKind] = useState<"at" | "every">("every");
  const [minutes, setMinutes] = useState("15");
  const [expiryMin, setExpiryMin] = useState("120");
  const [onTimeout, setOnTimeout] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const arm = async () => {
    setBusy(true);
    setErr("");
    const rule: Record<string, unknown> = { kind };
    if (kind === "every") rule.every = Math.max(60, Number(minutes) * 60 || 900);
    else rule.at = Math.floor(Date.now() / 1000) + Math.max(1, Number(minutes) * 60 || 900);
    if (expiryMin) rule.expires_at = Math.floor(Date.now() / 1000) + Number(expiryMin) * 60;
    if (onTimeout) rule.on_timeout = onTimeout;
    try {
      await api.teamOp({ op: "wakeup_create", team, task_id: taskId, wakeup: rule });
      onDone();
    } catch (e) {
      setErr((e as ApiError).message);
    } finally {
      setBusy(false);
    }
  };

  const wakeNow = async () => {
    setBusy(true);
    setErr("");
    try {
      await api.teamOp({ op: "wakeup_now", team, task_id: taskId });
      onDone();
    } catch (e) {
      setErr((e as ApiError).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="border-amber-300/60 p-2.5 dark:border-amber-500/30">
      <p className="flex items-center gap-1.5 break-words text-sm font-medium">
        <AlarmClock size={14} className="shrink-0 text-amber-500" /> {title}
      </p>
      <p className="mt-1 text-xs text-zinc-400">blocked — waiting on an external condition</p>
      <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs">
        <Select className="w-auto px-2 py-1" value={kind} onChange={(e) => setKind(e.target.value as "at" | "every")}>
          <option value="every">every</option>
          <option value="at">at (once)</option>
        </Select>
        <input
          className="w-16 rounded-lg border border-zinc-200/80 bg-transparent px-2 py-1 dark:border-zinc-700"
          value={minutes}
          onChange={(e) => setMinutes(e.target.value)}
          title={kind === "every" ? "interval in minutes" : "minutes from now"}
        />
        <span className="text-zinc-400">min</span>
        <Select className="w-auto px-2 py-1" value={onTimeout} onChange={(e) => setOnTimeout(e.target.value)}>
          <option value="">on timeout: keep</option>
          <option value="end">on timeout: cancel</option>
        </Select>
        <input
          className="w-16 rounded-lg border border-zinc-200/80 bg-transparent px-2 py-1 dark:border-zinc-700"
          value={expiryMin}
          onChange={(e) => setExpiryMin(e.target.value)}
          title="wakeup expiry in minutes"
        />
        <span className="text-zinc-400">expiry</span>
      </div>
      {err && <p className="mt-1 text-xs text-red-500">{err}</p>}
      <div className="mt-2 flex gap-1">
        <Button variant="ghost" className="px-2 py-1 text-xs" onClick={arm} disabled={busy}>
          <AlarmClock size={12} /> Arm wakeup
        </Button>
        <Button variant="ghost" className="px-2 py-1 text-xs" onClick={wakeNow} disabled={busy}>
          <Play size={12} /> Wake now
        </Button>
        <Button variant="ghost" className="px-2 py-1 text-xs" onClick={onCancel} disabled={busy}>
          <Ban size={12} /> Cancel task
        </Button>
      </div>
    </Card>
  );
}

export default function TeamPage() {
  const [snap, setSnap] = useState<TeamSnapshot | null>(null);
  const [err, setErr] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState("");
  const [lead, setLead] = useState<LeadStatus | null>(null);
  const [goal, setGoal] = useState("");
  // Playbook panel (issue #14): rendered strictly from the
  // teams.playbook_render / playbook_trace payloads — no console-side team
  // state. Hidden entirely when the daemon reports playbook disabled on the
  // team (hidden, not disabled-looking).
  const [pb, setPb] = useState<{ rendered: string; entries: Record<string, any>[] } | null>(null);
  const [pbHidden, setPbHidden] = useState(false);
  const [pbError, setPbError] = useState("");
  const [trace, setTrace] = useState<{ entryId: string; episodes: Record<string, any>[] } | null>(null);
  const [traceErr, setTraceErr] = useState("");
  // Focus team: the wizard's success deep-link lands here filtered to the
  // new team (?team= in the URL keeps the view linkable). Empty = the
  // configured default team from the cached poller snapshot.
  const [focusTeam, setFocusTeam] = useState(() => new URLSearchParams(window.location.search).get("team") || "");
  const [liveTeams, setLiveTeams] = useState<string[]>([]);
  // Run drawer (issue #45 F2): open over one task's replay RPCs. Hidden for
  // good once the daemon reports the run RPCs unavailable (degraded path).
  const [runDrawer, setRunDrawer] = useState<string | null>(null);
  const [runsUnavailable, setRunsUnavailable] = useState(false);
  const load = () => {
    setErr(null);
    api.teamView(focusTeam || undefined).then(setSnap).catch((e) => setErr(e as ApiError));
    // Lead loop status rides the same poll; an unregistered team (or daemon
    // without the RPC) just means "no lead" — never a visible error here.
    api.teamOp({ op: "lead_status", team: focusTeam || undefined }).then((r) => setLead(r as LeadStatus)).catch(() => setLead(null));
    // Playbook rides the same refresh; its errors are panel-scoped, never
    // page-level (a daemon without the RPC just means no panel).
    api.teamOp({ op: "playbook_render", team: focusTeam || undefined })
      .then((r) => {
        setPb({ rendered: String(r.rendered || ""), entries: (r.entries as Record<string, any>[]) ?? [] });
        setPbHidden(false);
        setPbError("");
      })
      .catch((e) => {
        const msg = (e as Error).message;
        if (msg.includes("playbook disabled")) setPbHidden(true);
        else if (msg.includes("no playbook store")) { setPb({ rendered: "", entries: [] }); setPbHidden(false); setPbError(""); }
        else { setPb(null); setPbError(msg); }
      });
  };
  useEffect(() => { load(); }, [focusTeam]);
  useEffect(() => {
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [focusTeam]);
  // Team switcher options — the daemon's live teams (teams.list). Chat-created
  // teams land here with the next refresh, same as wizard-created ones.
  useEffect(() => {
    const read = () =>
      api.teamTeams()
        .then((r) => setLiveTeams((r.teams ?? []).map((x: Record<string, any>) => String(x.name || "")).filter(Boolean)))
        .catch(() => setLiveTeams([]));
    read();
    const t = setInterval(read, 15000);
    return () => clearInterval(t);
  }, []);
  const op = async (op: string, task_id?: string) => {
    setBusy(task_id ? `${op}:${task_id}` : op);
    try {
      await api.teamOp({ op, task_id });
      load();
    } catch (e) {
      setErr(e as ApiError);
    } finally {
      setBusy("");
    }
  };

  // Team-level lifecycle op (pause/resume/disband/lead_*) — no task id.
  // Ops target the focused team when the wizard deep-link set one, else the
  // daemon's configured default.
  const teamOp = async (op: string, extra?: Record<string, unknown>) => {
    setBusy(op);
    try {
      await api.teamOp({ op, team: focusTeam || undefined, ...extra });
      load();
    } catch (e) {
      setErr(e as ApiError);
    } finally {
      setBusy("");
    }
  };

  const lifecycle = (snap?.lifecycle || "").toUpperCase();

  // Deep-linkable focus: ?team=<name> in the URL mirrors the state so a
  // fresh load (or a shared link) observes the same non-default team.
  const applyFocus = (team: string) => {
    setFocusTeam(team);
    const url = new URL(window.location.href);
    if (team) url.searchParams.set("team", team);
    else url.searchParams.delete("team");
    window.history.replaceState(null, "", url.toString());
  };

  // Trace provenance for one playbook entry (teams.playbook_trace).
  const loadTrace = async (entryId: string) => {
    setTraceErr("");
    setTrace(null);
    try {
      const r = await api.teamOp({ op: "playbook_trace", team: focusTeam || undefined, entry_id: entryId });
      setTrace({ entryId, episodes: (r.episodes as Record<string, any>[]) ?? [] });
    } catch (e) {
      setTraceErr((e as Error).message);
    }
  };
  const disband = () => {
    const members = snap?.roster?.length ?? 0;
    const open = tasks.filter((t) => !["done", "completed", "finished", "success", "cancelled"].includes(String(t.status || "").toLowerCase())).length;
    if (!window.confirm(`Disband this team? ${members} member(s), ${open} open task(s). Members and the task board go away with it.`)) return;
    teamOp("disband");
  };

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

      {/* Team switcher — every live team on the daemon (wizard- or chat-created).
          Selection deep-links via ?team= so the view stays linkable. */}
      {liveTeams.length > 0 && (
        <div className="flex items-center gap-2 text-sm">
          <label className="text-zinc-500 dark:text-zinc-400">Team</label>
          <select
            value={focusTeam}
            onChange={(e) => applyFocus(e.target.value)}
            className="rounded-xl border border-zinc-200/80 bg-white px-3 py-1.5 text-sm shadow-sm dark:border-zinc-700 dark:bg-zinc-900"
          >
            <option value="">(default team)</option>
            {liveTeams.map((n) => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
        </div>
      )}

      {err && <ReasonCard error={err} />}

      {focusTeam && (
        <div className="flex items-center gap-2 rounded-xl bg-indigo-500/10 px-3 py-1.5 text-sm text-indigo-600 dark:text-indigo-400">
          <Eye size={14} /> observing <span className="font-mono">{focusTeam}</span>
          <button onClick={() => applyFocus("")} className="ml-1 rounded p-0.5 hover:bg-indigo-500/20" title="Back to the default team">
            <X size={12} />
          </button>
        </div>
      )}

      {/* Lifecycle + lead loop controls (issue #17). Disband is the only
          destructive one — it confirms with member/open-task counts first. */}
      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="mr-1 text-sm font-medium">Team</h3>
          {lifecycle ? (
            <span className={`rounded-lg px-2 py-0.5 text-xs ${
              lifecycle === "ACTIVE" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
              : lifecycle === "PAUSED" ? "bg-amber-500/10 text-amber-600 dark:text-amber-400"
              : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"}`}>
              {lifecycle.toLowerCase()}
            </span>
          ) : (
            <span className="text-xs text-zinc-400">not registered yet</span>
          )}
          <div className="ml-auto flex flex-wrap items-center gap-2">
            {lifecycle !== "PAUSED" && (
              <Button variant="ghost" onClick={() => teamOp("pause")} disabled={busy !== "" || !lifecycle}>
                <Pause size={12} /> Pause
              </Button>
            )}
            {lifecycle === "PAUSED" && (
              <Button variant="ghost" onClick={() => teamOp("resume")} disabled={busy !== ""}>
                <Play size={12} /> Resume
              </Button>
            )}
            <Button variant="ghost" className="text-red-500 dark:text-red-400" onClick={disband} disabled={busy !== "" || !lifecycle}>
              <Ban size={12} /> Disband
            </Button>
          </div>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-zinc-200/60 pt-3 dark:border-zinc-800">
          <h3 className="mr-1 text-sm font-medium">Lead loop</h3>
          {lead?.running ? (
            <>
              <span className="rounded-lg bg-indigo-500/10 px-2 py-0.5 text-xs text-indigo-600 dark:text-indigo-400">
                running · round {lead.round ?? 0}{lead.n_stalls ? ` · ${lead.n_stalls} stall(s)` : ""}
              </span>
              {lead.last_verdict && <span className="max-w-72 truncate text-xs text-zinc-400" title={lead.last_verdict}>{lead.last_verdict}</span>}
              <Button variant="ghost" className="ml-auto" onClick={() => teamOp("lead_stop")} disabled={busy !== ""}>
                <Square size={12} /> Stop
              </Button>
            </>
          ) : (
            <>
              <input value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="goal for the lead loop…"
                className="min-w-64 flex-1 rounded-xl border border-zinc-200/80 bg-transparent px-3 py-1.5 text-sm dark:border-zinc-700" />
              <Button onClick={() => teamOp("lead_start", { goal })} disabled={busy !== "" || !goal.trim()}>
                <Play size={12} /> Start
              </Button>
            </>
          )}
        </div>
      </Card>

      {/* Roster is read-only here: adding/removing members happens in the
          chat sidebar team panel (Manage members), the single entry point. */}
      <Card className="p-4">
        <div className="mb-2 flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">Members</h3>
          <span className="text-xs text-zinc-400">manage members from the chat sidebar team panel</span>
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

      {/* Playbook panel (issue #14) — entries + trace provenance from the
          daemon only; hidden entirely for playbook:false teams. */}
      {!pbHidden && (
        <Card className="p-4">
          <div className="mb-2 flex items-center gap-2">
            <BookOpen size={14} className="text-indigo-500" />
            <h3 className="text-sm font-medium">Playbook</h3>
            {pb && <span className="text-xs text-zinc-400">{pb.entries.length} entr{pb.entries.length === 1 ? "y" : "ies"}</span>}
          </div>
          {pbError && <p className="text-sm text-red-500">{pbError}</p>}
          {!pbError && !pb && <p className="text-sm text-zinc-400">loading…</p>}
          {pb && pb.entries.length === 0 && (
            <p className="text-sm text-zinc-400">no playbook entries yet — the lead loop writes lessons here</p>
          )}
          {pb && pb.entries.length > 0 && (
            <div className="space-y-2">
              {pb.rendered && (
                <pre className="max-h-64 overflow-auto rounded-xl bg-zinc-100 p-3 text-xs leading-5 dark:bg-zinc-800">{pb.rendered}</pre>
              )}
              {pb.entries.map((e, i) => {
                const id = String(e.id || e.entry_id || `entry-${i}`);
                const open = trace?.entryId === id;
                return (
                  <div key={id} className="rounded-xl border border-zinc-200/80 p-2.5 dark:border-zinc-800">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-xs text-zinc-500 dark:text-zinc-400">{id}</span>
                      <span className="flex-1 truncate text-sm" title={String(e.title || e.summary || "")}>
                        {String(e.title || e.summary || e.lesson || id)}
                      </span>
                      <Button variant="ghost" onClick={() => (open ? setTrace(null) : loadTrace(id))} disabled={busy !== ""}>
                        {open ? "Hide trace" : "Trace"}
                      </Button>
                    </div>
                    {open && (
                      <div className="mt-2 border-t border-zinc-200/60 pt-2 dark:border-zinc-800">
                        {traceErr && <p className="text-xs text-red-500">{traceErr}</p>}
                        {trace && trace.episodes.length === 0 && <p className="text-xs text-zinc-400">no provenance episodes</p>}
                        {trace && trace.episodes.length > 0 && (
                          <pre className="max-h-48 overflow-auto rounded-lg bg-zinc-100 p-2 text-[11px] leading-4 dark:bg-zinc-800">
                            {JSON.stringify(trace.episodes, null, 2)}
                          </pre>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      )}

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
                  String(t.status || "").toLowerCase() === "blocked" ? (
                    <WakeupCard
                      key={t.task_id || t.id}
                      team={focusTeam || undefined}
                      taskId={String(t.task_id || t.id)}
                      title={String(t.title || t.description || t.task_id || t.id)}
                      onDone={load}
                      onCancel={() => op("cancel", String(t.task_id || t.id))}
                    />
                  ) : (
                  <Card key={t.task_id || t.id} className="p-2.5">
                    <p className="break-words text-sm font-medium">{t.title || t.description || t.task_id || t.id}</p>
                    <p className="mt-1 truncate text-xs text-zinc-400" title={t.agent_id || ""}>{t.agent_id || ""}</p>
                    {!runsUnavailable && (
                      <Button variant="ghost" className="mt-1 px-2 py-1 text-xs" onClick={() => setRunDrawer(String(t.task_id || t.id))}>
                        <Eye size={12} /> View run
                      </Button>
                    )}
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
                  )
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

      {runDrawer && (
        <RunDrawer
          team={focusTeam || undefined}
          taskId={runDrawer}
          onClose={() => setRunDrawer(null)}
          onUnavailable={() => setRunsUnavailable(true)}
        />
      )}
    </div>
  );
}
