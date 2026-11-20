import { useEffect, useState } from "react";
import { AlarmClock, AlertTriangle, Ban, BookOpen, ChevronDown, ChevronRight, Eye, Pause, Play, RefreshCw, RotateCcw, Square, Users, X } from "lucide-react";
import { api, ApiError, type HarnessReport, type LeadStatus, type TeamSnapshot } from "./api";
import { Button, Card, Select } from "./components/ui";
import ReasonCard from "./components/ReasonCard";
import RunDrawer from "./components/RunDrawer";

// Teams view (T4b, issue #6): pure observer over the agent-studio daemon.
// Master-detail layout (prototype docs/prototype-team-ops.html): grouped tree
// of live teams on the left (active/paused/other, collapsible), focused team
// ops on the right — health/limits first, then lifecycle+lead loop, roster,
// playbook, alerts, task board. Team creation and member management live in
// the chat sidebar team panel; this page is ops + observability only.
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
      <div className="mt-2 flex flex-wrap gap-1">
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
  // Tree nav entries: name + lifecycle for grouping. Empty name = the daemon's
  // configured default team.
  const [liveTeams, setLiveTeams] = useState<{ name: string; lifecycle: string }[]>([]);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
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
        else if (msg.includes("team not registered")) {
          // Unregistered team (e.g. the configured default that was never
          // created) — a neutral empty state, not an error.
          setPb({ rendered: "", entries: [] }); setPbHidden(false); setPbError("");
        }
        else if (msg.includes("no playbook store")) { setPb({ rendered: "", entries: [] }); setPbHidden(false); setPbError(""); }
        else { setPb(null); setPbError(msg); }
      });
  };
  useEffect(() => { load(); }, [focusTeam]);
  useEffect(() => {
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [focusTeam]);
  // console#79 R5: harness guard report for the focused team's conversation
  // (report RPC from #78). Conversation lookup happens once per focus; the
  // report refreshes on its own slower cadence. Errors hide the panel.
  const [rep, setRep] = useState<HarnessReport | null>(null);
  const [repConv, setRepConv] = useState<number | null>(null);
  useEffect(() => {
    setRep(null);
    setRepConv(null);
    api.conversations().then((r) => {
      const c = (r.conversations ?? []).find((x) => x.Kind === "team" && x.AgentName === focusTeam);
      setRepConv(c ? c.ID : null);
    }).catch(() => setRepConv(null));
  }, [focusTeam]);
  useEffect(() => {
    if (!repConv) return;
    const read = () => api.harnessReport(repConv).then(setRep).catch(() => setRep(null));
    read();
    const t = setInterval(read, 10000);
    return () => clearInterval(t);
  }, [repConv]);
  // Tree nav options — the daemon's live teams (teams.list) with lifecycle for
  // grouping. Chat-created teams land here with the next refresh.
  useEffect(() => {
    const read = () =>
      api.teamTeams()
        .then((r) => setLiveTeams((r.teams ?? [])
          .map((x: Record<string, any>) => ({ name: String(x.name || ""), lifecycle: String(x.lifecycle || x.status || "").toLowerCase() }))
          .filter((t: { name: string }) => t.name)))
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

  const lifecycle = (snap?.lifecycle || "").toUpperCase();

  // Workload axis (issue #45 F5): open tasks per assignee, split by running
  // vs queued, with the member's concurrency cap when the roster carries one.
  const workloadFor = (name: string, all: Record<string, any>[]) => {
    let running = 0;
    let queued = 0;
    for (const t of all) {
      if (String(t.assignee || "") !== name) continue;
      const s = String(t.status || "").toLowerCase();
      if (s === "running") running += 1;
      else if (s === "queued") queued += 1;
    }
    const rosterRow = (snap?.roster ?? []).find((m) => String(m.name || m.member_id || "") === name);
    const capacity = Number(rosterRow?.max_concurrency ?? rosterRow?.concurrency ?? 0);
    return { running, queued, capacity };
  };
  // Tree grouping: default team (focusTeam === "") rides the focused
  // snapshot's lifecycle; listed teams carry their own.
  const treeTeams = [{ name: "", lifecycle: (snap?.lifecycle || "").toLowerCase() }, ...liveTeams];
  const groupOf = (lc: string) => (lc === "active" ? "active" : lc === "paused" ? "paused" : "other");
  const GROUPS: { id: string; label: string; dot: string }[] = [
    { id: "active", label: "Active", dot: "bg-emerald-500" },
    { id: "paused", label: "Paused", dot: "bg-amber-500" },
    { id: "other", label: "Other", dot: "bg-zinc-400" },
  ];

  return (
    <div className="mx-auto max-w-6xl space-y-4 p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold flex items-center gap-2"><Users size={18} /> Teams</h2>
        <div className="flex flex-wrap items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
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

      {err && <ReasonCard error={err} />}

      <div className="flex flex-col gap-4 md:flex-row md:items-start">
        {/* Left: grouped tree of live teams. Selection deep-links via ?team=
            so the view stays linkable; groups collapse independently. */}
        <Card className="w-full p-3 md:w-72 md:shrink-0">
          <h3 className="mb-1 text-sm font-medium">Teams</h3>
          <p className="mb-1 text-xs text-zinc-400">{liveTeams.length} live</p>
          {GROUPS.map((g) => {
            const members = treeTeams.filter((t) => groupOf(t.lifecycle) === g.id);
            if (members.length === 0) return null;
            const open = !collapsed[g.id];
            return (
              <div key={g.id} className="mb-1">
                <button
                  onClick={() => setCollapsed((c) => ({ ...c, [g.id]: !!open }))}
                  className="flex w-full items-center gap-1.5 rounded-lg px-1 py-1 text-xs font-medium text-zinc-500 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
                >
                  {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                  <span className={`h-1.5 w-1.5 rounded-full ${g.dot}`} />
                  {g.label}
                  <span className="ml-auto tabular-nums text-zinc-400">{members.length}</span>
                </button>
                {open && (
                  <div className="ml-2.5 space-y-0.5 border-l border-zinc-200/60 pl-2 dark:border-zinc-800">
                    {members.map((t) => (
                      <div key={t.name}>
                        <button
                          onClick={() => applyFocus(t.name)}
                          className={`flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm hover:bg-zinc-100 dark:hover:bg-zinc-800 ${
                            focusTeam === t.name ? "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400" : ""
                          }`}
                        >
                          <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${g.dot}`} />
                          <span className="flex-1 truncate">{t.name || "(default team)"}</span>
                        </button>
                        {focusTeam === t.name && (
                          <div className="mb-1 space-y-2 rounded-lg bg-zinc-50 p-2 dark:bg-zinc-900/60">
                            <div className="flex flex-wrap items-center gap-1.5">
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
                              <div className="ml-auto flex items-center gap-1">
                                {lifecycle !== "PAUSED" ? (
                                  <Button variant="ghost" className="px-1.5 py-0.5" title="Pause" onClick={() => teamOp("pause")} disabled={busy !== "" || !lifecycle}>
                                    <Pause size={12} />
                                  </Button>
                                ) : (
                                  <Button variant="ghost" className="px-1.5 py-0.5" title="Resume" onClick={() => teamOp("resume")} disabled={busy !== ""}>
                                    <Play size={12} />
                                  </Button>
                                )}
                                <Button variant="ghost" className="px-1.5 py-0.5 text-red-500 dark:text-red-400" title="Disband" onClick={disband} disabled={busy !== "" || !lifecycle}>
                                  <Ban size={12} />
                                </Button>
                              </div>
                            </div>
                            <div className="border-t border-zinc-200/60 pt-2 dark:border-zinc-800">
                              <p className="mb-1 text-xs font-medium text-zinc-500 dark:text-zinc-400">Lead loop</p>
                              {lead?.running ? (
                                <div className="flex items-center gap-1.5">
                                  <span className="min-w-0 flex-1 truncate text-xs text-indigo-600 dark:text-indigo-400">
                                    round {lead.round ?? 0}{lead.n_stalls ? ` · ${lead.n_stalls} stall(s)` : ""}
                                  </span>
                                  <Button variant="ghost" className="px-1.5 py-0.5" title="Stop" onClick={() => teamOp("lead_stop")} disabled={busy !== ""}>
                                    <Square size={12} />
                                  </Button>
                                </div>
                              ) : (
                                <div className="flex items-center gap-1">
                                  <input value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="goal…"
                                    className="min-w-0 flex-1 rounded-lg border border-zinc-200/80 bg-transparent px-2 py-1 text-xs dark:border-zinc-700" />
                                  <Button className="px-2 py-1 text-xs" onClick={() => teamOp("lead_start", { goal })} disabled={busy !== "" || !goal.trim()}>
                                    <Play size={11} />
                                  </Button>
                                </div>
                              )}
                            </div>
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            );
          })}
        </Card>

        {/* Right: focused team detail (health/limits first, per prototype). */}
        <div className="min-w-0 flex-1 space-y-4">

      {/* Health + limits */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Card className="p-4">
          <h3 className="mb-2 text-sm font-medium">Health</h3>
          {snap?.health ? (
            <dl className="space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
              {Object.entries(snap.health).filter(([, v]) => typeof v !== "object").slice(0, 8).map(([k, v]) => (
                <div key={k} className="flex gap-2"><dt className="w-32 shrink-0 truncate" title={k}>{k}</dt><dd className="min-w-0 break-all tabular-nums">{String(v)}</dd></div>
              ))}
            </dl>
          ) : <p className="text-sm text-zinc-400">—</p>}
        </Card>
        <Card className="p-4">
          <h3 className="mb-2 text-sm font-medium">Limits</h3>
          {snap?.limits ? (
            <dl className="space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
              {Object.entries(snap.limits).filter(([, v]) => typeof v !== "object").map(([k, v]) => (
                <div key={k} className="flex gap-2"><dt className="w-32 shrink-0 truncate" title={k}>{k}</dt><dd className="min-w-0 break-all tabular-nums">{String(v)}</dd></div>
              ))}
            </dl>
          ) : <p className="text-sm text-zinc-400">—</p>}
          <Button variant="ghost" className="mt-2" onClick={() => op("set_limits")} disabled={busy !== ""}>
            <RotateCcw size={12} /> Re-apply defaults
          </Button>
        </Card>
      </div>

      {/* Roster is read-only here: adding/removing members happens in the
          chat sidebar team panel (Manage members), the single entry point.
          Badges are dual-axis (issue #45 F5): availability x workload, with
          "offline + queued" as a distinct red stuck state. */}
      <Card className="p-4">
        <div className="mb-2 flex flex-wrap items-center justify-between gap-x-2 gap-y-0.5">
          <h3 className="text-sm font-medium">Members</h3>
          <span className="text-xs text-zinc-400">manage members from the chat sidebar team panel</span>
        </div>
        {(snap?.roster ?? []).length === 0 ? (
          <p className="text-sm text-zinc-400">No roster reported by the daemon.</p>
        ) : (
          <div className="space-y-1.5">
            {snap!.roster.map((m, i) => {
              const name = String(m.name || m.member_id || "");
              const wl = workloadFor(name, tasks);
              const stuck = (m.presence || "").toLowerCase() !== "available" && (wl.queued > 0 || wl.running > 0);
              return (
                <div key={m.member_id || m.name || i} className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="font-medium">{name}</span>
                  {m.role && <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{m.role}</span>}
                  {!!m.budget_tokens && (
                    <span className="rounded-lg border border-zinc-200/80 px-1.5 py-0.5 text-xs tabular-nums text-zinc-500 dark:border-zinc-800 dark:text-zinc-400"
                      title="Configured member token budget (kernel TeamMember.budget_tokens); the kernel does not yet report per-member spend">
                      budget {Number(m.budget_tokens).toLocaleString()}
                    </span>
                  )}
                  {stuck ? (
                    <span className="rounded-lg bg-red-500/10 px-2 py-0.5 text-xs font-medium text-red-600 dark:text-red-400">
                      stuck · {(m.presence || "offline").toLowerCase()} · {wl.queued + wl.running} open
                    </span>
                  ) : (
                    <>
                      <span className={`rounded-lg px-2 py-0.5 text-xs ${
                        (m.presence || "").toLowerCase() === "available"
                          ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                          : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"}`}>
                        {(m.presence || m.status || "unknown").toLowerCase()}
                      </span>
                      <span className="text-xs text-zinc-400">
                        {wl.running > 0 ? `working ${wl.running}${wl.capacity > 0 ? `/${wl.capacity}` : ""}` : wl.queued > 0 ? `${wl.queued} queued` : "idle"}
                      </span>
                    </>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Card>

      {/* console#79 R5: guard report — the #78 harness sidecar report for the
          team's operator conversation (funnel + budget + attribution).
          Hidden when the guard is off or the sidecar is down. */}
      {rep && (() => {
        const meter = rep.snapshot.llm_meter ?? {};
        const num = (k: string) => (typeof meter[k] === "number" ? (meter[k] as number) : null);
        const stats = [
          ["LLM calls", num("calls")], ["Prompt tokens", num("prompt_tokens")],
          ["Failures", num("failures")], ["Loops", num("loops_detected")],
          ["Budget", rep.snapshot.budget.limit > 0
            ? `${rep.snapshot.budget.spent.toLocaleString()} / ${rep.snapshot.budget.limit.toLocaleString()}${rep.snapshot.budget.exceeded ? " (exceeded)" : ""}`
            : null],
        ].filter(([, v]) => v !== null && v !== undefined) as [string, string | number][];
        const runs = Object.entries(rep.attribution.runs ?? {});
        if (!stats.length && !runs.length) return null;
        return (
          <Card className="p-4">
            <div className="mb-2 flex items-center gap-2">
              <Eye size={14} className="text-indigo-500" />
              <h3 className="text-sm font-medium">Guard report</h3>
              <span className="text-xs text-zinc-400">conversation #{repConv} · sidecar funnel + failure attribution</span>
            </div>
            {stats.length > 0 && (
              <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
                {stats.map(([label, value]) => (
                  <span key={label} className="text-zinc-500 dark:text-zinc-400">
                    {label} <span className="font-medium tabular-nums text-zinc-800 dark:text-zinc-200">{value}</span>
                  </span>
                ))}
              </div>
            )}
            {runs.map(([runId, run]) => (
              <div key={runId} className="mt-1.5 rounded-xl border border-zinc-200/80 px-3 py-2 text-xs dark:border-zinc-800">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{runId}</span>
                  {run.responsible_agent && <span className="text-indigo-600 dark:text-indigo-300">{run.responsible_agent}</span>}
                  {(run.verdicts ?? []).map((v, i) => v.mode && (
                    <span key={i} className="rounded-full border border-zinc-200 px-1.5 py-0.5 text-[10px] text-zinc-500 dark:border-zinc-700 dark:text-zinc-400">{v.mode}</span>
                  ))}
                </div>
                {run.decisive_moment && Object.keys(run.decisive_moment).length > 0 && (
                  <p className="mt-1 break-words text-zinc-500 dark:text-zinc-400">Decisive: {JSON.stringify(run.decisive_moment)}</p>
                )}
              </div>
            ))}
          </Card>
        );
      })()}

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
                      <span className="max-w-[40%] shrink-0 truncate font-mono text-xs text-zinc-500 dark:text-zinc-400" title={id}>{id}</span>
                      <span className="min-w-0 flex-1 truncate text-sm" title={String(e.title || e.summary || "")}>
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
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
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
                      <div className="mt-2 flex flex-wrap gap-1">
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

      </div>
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
