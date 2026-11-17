import { useEffect, useMemo, useState } from "react";
import { ArrowLeft, ArrowRight, Check, Plus, Rocket, Trash2, Users, Wand2 } from "lucide-react";
import { api, type TeamCreateRes, type TeamCreateTaskResult, type WorkerSettings } from "./api";
import { Badge, Button, Card, Input, Select } from "./components/ui";

// Team creation wizard (issue #16): 4 steps + done, reusing the v0.1.x
// Onboarding shell pattern (step rail + one component per step). Client
// checks mirror the kernel rules in fusion_harness/team.py — the error text
// matches the kernel's TeamDefError messages so the form shows the same
// words the daemon would. Server validation stays authoritative: whatever
// the kernel rejects renders verbatim, the console never weakens it.

type StepId = "basics" | "members" | "done";

const STEPS: { id: StepId; label: string; icon: typeof Users }[] = [
  { id: "basics", label: "Team basics", icon: Wand2 },
  { id: "members", label: "Members", icon: Users },
];

// Kernel _VALID_MODES (fusion_harness/team.py) — the dropdown can only
// produce valid values, but the check text stays for the review step.
const PERMISSION_MODES = ["bypass", "approved", "default", "plan"];
const MEMBER_NAME_RE = /^[A-Za-z0-9_-]+$/;

type MemberDraft = {
  name: string;
  role: string;
  skills: string;
  tool_allowlist: string;
  permission_mode: string;
  budget_tokens: number;
  max_concurrency: number;
};

// Mirror of internal/server/briefcheck.go checkBrief — the same five
// contract items, flagged while typing so bad briefs die in the wizard
// instead of at dispatch. Warnings only, never a blocker (issue #4
// semantics).
export function checkBriefClient(task: string): { item: string; fix: string }[] {
  const t = task.toLowerCase();
  const out: { item: string; fix: string }[] = [];
  const has = (needles: string[]) => needles.some((n) => t.includes(n));
  if (task.trim().split(/\s+/).filter(Boolean).length < 8)
    out.push({ item: "objective", fix: "brief under 8 words — state the goal and the success criteria explicitly" });
  if (!has(["output", "format", "return", "deliver", "produce", "report", "file ", "json", "markdown", "table"]))
    out.push({ item: "output format", fix: "no expected output format — say what artifact the worker should produce" });
  if (!has(["use ", "with ", "tool", "source", "docs", "reference", "path", "repo"]))
    out.push({ item: "tool guidance", fix: "no tool or source guidance — point at the files/commands/sources to use" });
  if (!has(["only", "do not", "don't", "avoid", "within", "scope", "limit", "must not", "stop"]))
    out.push({ item: "boundaries", fix: "no task boundaries — say what is out of scope and when to stop" });
  if (!has(["budget", "token", "step", "time", "minute", "iteration"]))
    out.push({ item: "budget", fix: "no budget hint — cap the expected tokens/steps so the worker plans within them" });
  return out;
}

const csv = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

export default function TeamWizard({ onCreated, onCancel }: { onCreated: (team: string) => void; onCancel: () => void }) {
  const [step, setStep] = useState<StepId>("basics");
  const idx = STEPS.findIndex((s) => s.id === step);

  // Step 1 state.
  const [name, setName] = useState("");
  const [nsTouched, setNsTouched] = useState(false);
  const [nsKeyState, setNsKeyState] = useState("");
  const nsKey = nsTouched ? nsKeyState : `team:${name.trim()}`;
  const [maxDepth, setMaxDepth] = useState(1);
  const [playbook, setPlaybook] = useState(false);
  const [liveTeams, setLiveTeams] = useState<Record<string, any>[]>([]);

  // Step 2 state.
  const [members, setMembers] = useState<MemberDraft[]>([]);
  const [workers, setWorkers] = useState<WorkerSettings[]>([]);

  // Step 3 state.
  const [leadBudget] = useState(200000);

  // Submit state.
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<TeamCreateRes | null>(null);

  // teams.list drives the non-blocking namespace warning. The daemon's list
  // exposes team names + lifecycle (no namespace_key), so anchor collisions
  // surface as a live team already registered under the same name.
  useEffect(() => {
    api.teamTeams().then((r) => setLiveTeams(r.teams ?? [])).catch(() => setLiveTeams([]));
    // Members come from the console fleet (orchestrator workers), not the
    // daemon's registered agents — the team is staffed by configured workers.
    api.orchestration().then((r) => setWorkers(r.orchestrator?.workers ?? [])).catch(() => setWorkers([]));
  }, []);

  const anchorWarn = useMemo(
    () => liveTeams.some((t) => String(t.name) === nsKey || String(t.name) === name.trim()) && step === "basics",
    [liveTeams, nsKey, name, step]
  );

  // Kernel-mirror checks. Each returns the kernel's error text, empty = ok.
  const memberErrors = useMemo(
    () =>
      members.map((m) => {
        if (!MEMBER_NAME_RE.test(m.name)) return `member name must match [A-Za-z0-9_-]+: ${m.name || "''"}`;
        if (!m.role.trim()) return `member role required: ${m.name}`;
        if (!PERMISSION_MODES.includes(m.permission_mode)) return `unknown permission mode: ${m.permission_mode}`;
        if (m.budget_tokens <= 0 || m.max_concurrency <= 0)
          return `budget_tokens and max_concurrency must be positive: ${m.name}`;
        if (m.budget_tokens > leadBudget)
          return `member ${m.name} budget ${m.budget_tokens} exceeds lead budget ${leadBudget} (Deadline.child semantics)`;
        return "";
      }),
    [members, leadBudget]
  );

  const dupNames = useMemo(() => {
    const seen = new Set<string>();
    const dups: string[] = [];
    for (const m of members) {
      if (seen.has(m.name)) dups.push(m.name);
      seen.add(m.name);
    }
    return dups;
  }, [members]);

  const basicsError = !name.trim()
    ? "team name required"
    : !nsKey.trim()
      ? "namespace_key required (memory anchor)"
      : maxDepth < 1 || maxDepth > 3
        ? "max_depth must be in 1..3 (global ladder cap)"
        : "";
  const membersError =
    members.length === 0
      ? "team needs at least one member"
      : dupNames.length > 0
        ? `duplicate member names: ${dupNames.join(", ")}`
        : memberErrors.find((e) => e) || "";
  const payload = useMemo(
    () => ({
      team: {
        name: name.trim(),
        namespace_key: nsKey.trim(),
        members: members.map((m) => ({
          name: m.name.trim(),
          role: m.role.trim(),
          skills: csv(m.skills),
          tool_allowlist: csv(m.tool_allowlist),
          permission_mode: m.permission_mode,
          budget_tokens: m.budget_tokens,
          max_concurrency: m.max_concurrency,
        })),
        lead_budget_tokens: leadBudget,
        playbook,
        max_depth: maxDepth,
        metadata: {},
      },
      // No queued tasks or lead loop here — assign work in chat afterwards
      // (create_team tool / team_send), per the simplified wizard flow.
      tasks: [],
      lead_start: false,
      lead_goal: "",
    }),
    [name, nsKey, members, leadBudget, playbook, maxDepth]
  );

  const next = () => {
    setError("");
    if (step === "basics" && basicsError) return setError(basicsError);
    if (step === "members" && membersError) return setError(membersError);
    if (step === "members") { submit(); return; }
    setStep("members");
  };

  const submit = async () => {
    setSubmitting(true);
    setError("");
    try {
      const res = await api.teamCreate(payload);
      setResult(res);
      setStep("done");
    } catch (e) {
      // Kernel/daemon error rendered verbatim — no silent retry.
      setError((e as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  const addMember = (agentName = "") => {
    setMembers([
      ...members,
      {
        name: agentName.replace(/[^A-Za-z0-9_-]/g, "-"),
        role: "",
        skills: "",
        tool_allowlist: "",
        permission_mode: "default",
        budget_tokens: 50000,
        max_concurrency: 1,
      },
    ]);
  };
  // Seed a member from a configured worker: name from the worker, role from
  // its agent type, permission mode carried over.
  const addMemberFromWorker = (w: WorkerSettings) => {
    const wname = (w.name || w.agent).replace(/[^A-Za-z0-9_-]/g, "-");
    if (members.some((m) => m.name === wname)) return;
    setMembers([
      ...members,
      {
        name: wname,
        role: w.agent,
        skills: "",
        tool_allowlist: "",
        permission_mode: w.permission_mode || "default",
        budget_tokens: 50000,
        max_concurrency: 1,
      },
    ]);
  };

  const patchMember = (i: number, patch: Partial<MemberDraft>) =>
    setMembers(members.map((m, j) => (j === i ? { ...m, ...patch } : m)));


  if (step === "done" && result) {
    return (
      <div className="mx-auto max-w-3xl space-y-4 p-6">
        <Card className="p-6">
          <h2 className="flex items-center gap-2 text-lg font-semibold">
            <Check size={18} className="text-emerald-500" /> team <span className="font-mono">{result.team}</span> created
          </h2>
          <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
            memory anchor <span className="font-mono">{result.namespace_key}</span> — the daemon owns the team from here.
          </p>
          {result.tasks.length > 0 && (
            <div className="mt-4 space-y-1.5">
              <p className="text-sm font-medium">Initial tasks</p>
              {result.tasks.map((t: TeamCreateTaskResult) => (
                <div key={t.index} className="flex items-start gap-2 text-sm">
                  {t.error ? (
                    <Badge tone="err">rejected</Badge>
                  ) : (
                    <Badge tone="ok">{t.task_id}</Badge>
                  )}
                  <span className="flex-1">{t.title}</span>
                  {t.error && <span className="text-xs text-red-500">{t.error}</span>}
                </div>
              ))}
            </div>
          )}
          {result.lead_error && (
            <p className="mt-3 rounded-xl bg-amber-500/10 p-2.5 text-sm text-amber-600 dark:text-amber-400">
              lead loop not started: {result.lead_error}
            </p>
          )}
          {result.lead_started && <p className="mt-3 text-sm text-emerald-600 dark:text-emerald-400">lead loop started.</p>}
          <div className="mt-5 flex gap-2">
            <Button onClick={() => onCreated(result.team)}>
              <Users size={14} /> Open team page
            </Button>
            <Button variant="ghost" onClick={onCancel}>Close</Button>
          </div>
        </Card>
      </div>
    );
  }

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col p-6">
      <header className="mb-6">
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          <Users size={20} className="text-indigo-500" /> Create a team
        </h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
          Registers a TeamDefinition on the agent-studio daemon — the console stores nothing.
        </p>
        <ol className="mt-4 flex flex-wrap items-center gap-1 text-xs">
          {STEPS.map((s, i) => (
            <li key={s.id} className="flex items-center gap-1">
              <span
                className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 ${
                  i === idx
                    ? "bg-indigo-600 text-white"
                    : i < idx
                      ? "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400"
                      : "bg-zinc-100 text-zinc-400 dark:bg-zinc-800/60"
                }`}
              >
                {i < idx ? <Check size={12} /> : <s.icon size={12} />} {s.label}
              </span>
              {i < STEPS.length - 1 && <span className="text-zinc-300 dark:text-zinc-700">—</span>}
            </li>
          ))}
        </ol>
      </header>

      <div className="flex-1 space-y-4 overflow-y-auto">
        {step === "basics" && (
          <Card className="space-y-4 p-4">
            <div>
              <label className="mb-1 block text-sm font-medium">Team name</label>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. release-crew" />
              <p className="mt-1 text-xs text-zinc-400">letters, digits, _ and - only (same charset as member names)</p>
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium">Memory anchor (namespace_key)</label>
              <Input
                value={nsKey}
                onChange={(e) => { setNsKeyState(e.target.value); setNsTouched(true); }}
                placeholder="team:<name>"
              />
              {anchorWarn && (
                <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                  a live team is already registered under this name — duplicate anchors share one memory namespace
                </p>
              )}
            </div>
            <div className="flex items-center gap-3">
              <label className="text-sm font-medium">Max depth</label>
              <div className="flex items-center gap-2">
                <Button variant="outline" onClick={() => setMaxDepth(Math.max(1, maxDepth - 1))} disabled={maxDepth <= 1}>−</Button>
                <span className="w-6 text-center text-sm tabular-nums">{maxDepth}</span>
                <Button variant="outline" onClick={() => setMaxDepth(Math.min(3, maxDepth + 1))} disabled={maxDepth >= 3}>+</Button>
              </div>
              <span className="text-xs text-zinc-400">subagent ladder cap (1..3)</span>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={playbook} onChange={(e) => setPlaybook(e.target.checked)}
                className="h-4 w-4 rounded border-zinc-300 text-indigo-600" />
              Enable playbook — lead-loop lessons render in the team page playbook panel
            </label>
          </Card>
        )}

        {step === "members" && (
          <Card className="space-y-4 p-4">
            <div className="flex flex-wrap items-center gap-2">
              <Select
                className="w-full max-w-64"
                value=""
                onChange={(e) => {
                  const v = e.target.value;
                  if (!v) return;
                  const w = workers.find((x) => (x.name || x.agent) === v);
                  if (w) addMemberFromWorker(w); else addMember(v);
                }}
              >
                <option value="">add member from an existing worker…</option>
                {workers.filter((w) => w.enabled).map((w, i) => (
                  <option key={i} value={`${w.name || w.agent}`}>{w.name || w.agent} · {w.agent}{w.model ? ` · ${w.model}` : ""}</option>
                ))}
              </Select>
              {workers.length === 0 && (
                <span className="text-xs text-amber-600 dark:text-amber-400">no workers configured — add one on the Agents page first</span>
              )}
              <Button variant="outline" onClick={() => addMember()}><Plus size={14} /> Blank member</Button>
            </div>
            {members.map((m, i) => (
              <div key={i} className="space-y-2 rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-700/80">
                <div className="flex flex-wrap items-center gap-2">
                  <Input className="w-full sm:w-44" value={m.name} onChange={(e) => patchMember(i, { name: e.target.value })} placeholder="name" />
                  <Input className="w-full sm:w-44" value={m.role} onChange={(e) => patchMember(i, { role: e.target.value })} placeholder="role" />
                  <Select className="w-full sm:w-36" value={m.permission_mode} onChange={(e) => patchMember(i, { permission_mode: e.target.value })}>
                    {PERMISSION_MODES.map((mode) => <option key={mode} value={mode}>{mode}</option>)}
                  </Select>
                  <Button variant="ghost" className="ml-auto text-red-500 dark:text-red-400"
                    onClick={() => setMembers(members.filter((_, j) => j !== i))}>
                    <Trash2 size={14} />
                  </Button>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <label className="text-xs text-zinc-500 dark:text-zinc-400">budget_tokens</label>
                  <Input className="w-full sm:w-32" type="number" value={m.budget_tokens}
                    onChange={(e) => patchMember(i, { budget_tokens: Number(e.target.value) })} />
                  <label className="text-xs text-zinc-500 dark:text-zinc-400">max_concurrency</label>
                  <Input className="w-full sm:w-24" type="number" value={m.max_concurrency}
                    onChange={(e) => patchMember(i, { max_concurrency: Number(e.target.value) })} />
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Input className="min-w-0 flex-1" value={m.skills} onChange={(e) => patchMember(i, { skills: e.target.value })}
                    placeholder="skills, comma-separated" />
                  <Input className="min-w-0 flex-1" value={m.tool_allowlist} onChange={(e) => patchMember(i, { tool_allowlist: e.target.value })}
                    placeholder="tool allowlist, comma-separated" />
                </div>
                {memberErrors[i] && <p className="text-xs text-red-500">{memberErrors[i]}</p>}
              </div>
            ))}
            {members.length === 0 && <p className="text-sm text-zinc-400">No members yet — a team needs at least one.</p>}
          </Card>
        )}

        {error && (
          <Card className="border-red-300/60 p-3 dark:border-red-500/30">
            <p className="break-words text-sm text-red-600 dark:text-red-400">{error}</p>
          </Card>
        )}
      </div>

      <div className="mt-4 flex items-center gap-2 border-t border-zinc-200/60 pt-4 dark:border-zinc-800">
        <Button variant="ghost" onClick={onCancel}>Cancel</Button>
        <div className="ml-auto flex gap-2">
          {idx > 0 && (
            <Button variant="outline" onClick={() => { setError(""); setStep(STEPS[idx - 1].id); }}>
              <ArrowLeft size={14} /> Back
            </Button>
          )}
          {step === "basics" ? (
            <Button onClick={next}><ArrowRight size={14} /> Next</Button>
          ) : (
            <Button onClick={next} disabled={submitting}>
              <Rocket size={14} /> {submitting ? "Creating…" : "Create team"}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
