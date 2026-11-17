import { useEffect, useState } from "react";
import { Bot, Brain, CheckCircle, ChevronDown, ChevronRight, Folder, GitBranch, Github, Info, Save, Terminal } from "lucide-react";
import { api, type AgentInfo, type Orchestrator, type Provider, type WorkerSettings } from "./api";
import { Button, Input, Select } from "./components/ui";

// Worker management shared by the wizard's Workers step and the Agents page.
// Add-worker follows a 4-step flow (agent type -> workspace -> LLM -> name &
// limits); existing workers stay editable as compact cards. Saved into the
// orchestrator config.
type IsoMode = "github" | "worktree" | "standalone";
type BrainSource = "none" | "github" | "local";

const ISO_LABEL: Record<IsoMode, string> = {
  github: "GitHub repo (clone/pull sync)",
  worktree: "local worktree",
  standalone: "standalone",
};

export function WorkersPanel({ onSaved }: { onSaved?: () => void }) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [orch, setOrch] = useState<Orchestrator | null>(null);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  // Add-form state, one field group per step.
  const [newAgent, setNewAgent] = useState("");
  const [newWorkDir, setNewWorkDir] = useState("");
  const [newIso, setNewIso] = useState<IsoMode>("github");
  const [newRepoUrl, setNewRepoUrl] = useState("");
  const [newBranch, setNewBranch] = useState("");
  const [newRepoPath, setNewRepoPath] = useState("");
  const [newProvider, setNewProvider] = useState("");
  const [newModel, setNewModel] = useState("");
  const [newName, setNewName] = useState("");
  const [newBudget, setNewBudget] = useState(5);
  const [newMemory, setNewMemory] = useState("notes");
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<number | null>(null);
  const [brainDir, setBrainDir] = useState("");
  const [brainSource, setBrainSource] = useState<BrainSource>("none");
  const [brainRepo, setBrainRepo] = useState("");
  const [brainBranch, setBrainBranch] = useState("");
  const [brainMode, setBrainMode] = useState<"chat" | "agent">("chat");
  const [brainContext, setBrainContext] = useState("");
  useEffect(() => {
    api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
    api.providers().then((r) => setProviders((r.providers ?? []).filter((p) => p.enabled))).catch(() => {});
    api.orchestration().then((r) => {
      setOrch(r.orchestrator);
      setBrainDir(r.orchestrator?.brain_work_dir ?? "");
      const repo = r.orchestrator?.brain_repo ?? "";
      setBrainRepo(repo);
      setBrainBranch(r.orchestrator?.brain_repo_branch ?? "");
      setBrainSource(!repo ? "none" : repo.startsWith("http") || repo.startsWith("git@") || repo.startsWith("ssh://") ? "github" : "local");
      setBrainMode(r.orchestrator?.brain_mode === "agent" ? "agent" : "chat");
      setBrainContext((r.orchestrator?.brain_context_files ?? []).join("\n"));
    }).catch(() => {});
  }, []);

  const detected = (name: string) => agents.find((a) => a.name === name)?.detected ?? false;
  const setWorker = (idx: number, patch: Partial<WorkerSettings>) => {
    if (!orch) return;
    const workers = [...orch.workers];
    workers[idx] = { ...workers[idx], ...patch };
    setOrch({ ...orch, workers });
  };
  const resetAddForm = () => {
    setNewAgent("");
    setNewWorkDir("");
    setNewIso("github");
    setNewRepoUrl("");
    setNewBranch("");
    setNewRepoPath("");
    setNewProvider("");
    setNewModel("");
    setNewName("");
    setNewBudget(5);
    setNewMemory("notes");
  };

  // Save worker: validate the add form, append to the fleet, persist.
  const addWorker = async () => {
    setMsg("");
    setErr("");
    if (!newAgent) { setErr("choose an agent type first"); return; }
    if (newIso === "github" && !newRepoUrl.trim()) { setErr("GitHub repo URL is required for github source"); return; }
    const base = orch ?? { mode: "confirm" as const, brain_agent: "claude", repos: [], workers: [] as WorkerSettings[], configured: false };
    const workers = [...base.workers];
    let name = newName.trim();
    if (!name) {
      let n = workers.filter((w) => w.agent === newAgent).length + 1;
      name = `${newAgent}-${n}`;
      while (workers.some((w) => (w.name || w.agent) === name)) name = `${newAgent}-${++n}`;
    }
    if (workers.some((w) => (w.name || w.agent) === name)) {
      setErr(`worker name "${name}" already taken`);
      return;
    }
    workers.push({
      name, agent: newAgent, enabled: true,
      isolation: newIso,
      permission_mode: "acceptEdits",
      max_budget_usd: newBudget,
      provider_id: newProvider,
      work_dir: newWorkDir.trim(),
      repo: newIso === "github" ? newRepoUrl.trim() : newRepoPath.trim(),
      repo_branch: newIso === "github" ? newBranch.trim() : "",
      model: newModel.trim(),
      memory: newMemory,
    });
    setBusy(true);
    try {
      await api.saveOrchestration({ ...base, workers, worker_memory: orch?.worker_memory ?? "notes" });
      setOrch({ ...base, workers, worker_memory: orch?.worker_memory ?? "notes" });
      resetAddForm();
      setMsg(`worker "${name}" saved`);
      onSaved?.();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const removeWorker = async (idx: number) => {
    if (!orch) return;
    const workers = orch.workers.filter((_, i) => i !== idx);
    setBusy(true);
    setMsg("");
    setErr("");
    try {
      await api.saveOrchestration({ ...orch, workers });
      setOrch({ ...orch, workers });
      setMsg("worker removed");
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // Persist inline edits on an existing worker card.
  const saveOrch = async (next: Orchestrator) => {
    setBusy(true);
    setMsg("");
    setErr("");
    try {
      await api.saveOrchestration(next);
      setOrch(next);
      setMsg("workers saved");
      onSaved?.();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const summary = newAgent
    ? `${newAgent} · ${ISO_LABEL[newIso]}${newModel ? ` · ${newModel}` : ""}${newName ? ` · ${newName}` : ""}`
    : "";

  return (
    <div className="space-y-4">
      {/* Brain — mode + work dir + code source + context files */}
      <div className="rounded-2xl border border-zinc-200/80 p-5 dark:border-zinc-800">
        <h3 className="mb-2 flex items-center gap-2 text-sm font-semibold"><Brain size={16} /> Brain</h3>
        <div className="flex flex-wrap items-center gap-2">
          <Select className="w-56" value={brainMode} onChange={(e) => setBrainMode(e.target.value as "chat" | "agent")}>
            <option value="chat">brain: chat model (planner)</option>
            <option value="agent">brain: claude code (agent)</option>
          </Select>
          <Select className="w-48" value={brainSource} onChange={(e) => setBrainSource(e.target.value as BrainSource)}>
            <option value="none">code source: none</option>
            <option value="github">code source: github</option>
            <option value="local">code source: local repo</option>
          </Select>
          {brainSource === "github" && (
            <>
              <Input className="min-w-56 flex-1" placeholder="repository URL *required — https://github.com/owner/repo.git" value={brainRepo}
                onChange={(e) => setBrainRepo(e.target.value)} />
              <Input className="w-36" placeholder="branch (optional)" value={brainBranch} onChange={(e) => setBrainBranch(e.target.value)} />
            </>
          )}
          {brainSource === "local" && (
            <Input className="min-w-56 flex-1" placeholder="local repo path (absolute) — brain artifacts land inside it" value={brainRepo}
              onChange={(e) => setBrainRepo(e.target.value)} />
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Input className="min-w-64 flex-1" placeholder="brain work dir — empty = ~/.fusion/fusion-console/agents" value={brainDir}
            onChange={(e) => setBrainDir(e.target.value)} />
          <Button variant="outline" disabled={busy || !orch}
            onClick={() => orch && saveOrch({
              ...orch,
              brain_mode: brainMode,
              brain_work_dir: brainDir.trim(),
              brain_repo: brainSource === "none" ? "" : brainRepo.trim(),
              brain_repo_branch: brainSource === "github" ? brainBranch.trim() : "",
              brain_context_files: brainContext.split("\n").map((s) => s.trim()).filter(Boolean),
            })}>
            <Save size={14} /> Save
          </Button>
        </div>
        <textarea value={brainContext} onChange={(e) => setBrainContext(e.target.value)} rows={2}
          placeholder={"context files, one path per line — working conventions, repo inventory, team rules\n~/fusion/CLAUDE.md"}
          className="mt-2 w-full rounded-xl border border-zinc-200/80 bg-transparent px-3 py-2 font-mono text-xs placeholder:text-zinc-400 focus:outline-none focus:ring-2 focus:ring-indigo-500/50 dark:border-zinc-700" />
        <p className="mt-2 flex items-start gap-1 text-[11px] text-zinc-500">
          <Info size={12} className="mt-0.5 shrink-0" />
          {brainMode === "agent"
            ? "Claude code brain: each orchestrated turn runs a claude session in the brain work dir with the console dispatch tools over MCP. It reads CLAUDE.md from the work dir — put working conventions there for fresh installs."
            : "Chat-model brain: the planner prompt includes the workspace config and the context files below."}
          {" "}Context files are injected (capped at 12 KB total).
        </p>
      </div>

      {/* Add worker — 4-step flow */}
      <div className="space-y-4 rounded-2xl border border-zinc-200/80 p-5 dark:border-zinc-800">
        <h3 className="flex items-center gap-2 text-sm font-semibold"><Bot size={16} /> Add worker</h3>

        {/* Step 1: agent type */}
        <section>
          <p className="mb-2 text-xs font-medium text-zinc-500">1 · Agent type</p>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {agents.filter((a) => !a.is_worker).map((a) => (
              <label key={a.name} className="cursor-pointer">
                <input type="radio" name="fc-worker-agent" className="peer sr-only" checked={newAgent === a.name} onChange={() => setNewAgent(a.name)} />
                <div className="flex flex-col items-center gap-1 rounded-xl border border-zinc-200/80 p-3 transition-colors hover:border-zinc-300 peer-checked:border-indigo-500 peer-checked:bg-indigo-500/5 dark:border-zinc-800 dark:hover:border-zinc-700 dark:peer-checked:border-indigo-500">
                  <Terminal size={16} className="text-zinc-400" />
                  <span className="text-sm font-medium">{a.name}</span>
                  {a.detected
                    ? <span className="rounded-lg bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-600 dark:text-emerald-400">on PATH</span>
                    : <span className="rounded-lg bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-600 dark:text-amber-400">not on PATH</span>}
                </div>
              </label>
            ))}
          </div>
        </section>

        {/* Step 2: workspace */}
        <section>
          <p className="mb-2 text-xs font-medium text-zinc-500">2 · Workspace</p>
          <Input className="mb-2" placeholder="work dir — empty = ~/.fusion/fusion-console/agents/<name>" value={newWorkDir}
            onChange={(e) => setNewWorkDir(e.target.value)} />
          <div className="mb-2 grid grid-cols-3 gap-2">
            {([
              { id: "github", icon: Github, label: "GitHub repo", hint: "clone / pull sync" },
              { id: "worktree", icon: GitBranch, label: "Local worktree", hint: "branch per task" },
              { id: "standalone", icon: Folder, label: "Standalone", hint: "empty workspace" },
            ] as const).map((o) => (
              <label key={o.id} className="cursor-pointer">
                <input type="radio" name="fc-worker-iso" className="peer sr-only" checked={newIso === o.id} onChange={() => setNewIso(o.id)} />
                <div className="flex flex-col items-center gap-1 rounded-xl border border-zinc-200/80 p-2.5 transition-colors hover:border-zinc-300 peer-checked:border-indigo-500 peer-checked:bg-indigo-500/5 dark:border-zinc-800 dark:hover:border-zinc-700 dark:peer-checked:border-indigo-500">
                  <o.icon size={14} className="text-zinc-400" />
                  <span className="text-xs font-medium">{o.label}</span>
                  <span className="text-[10px] text-zinc-500">{o.hint}</span>
                </div>
              </label>
            ))}
          </div>
          {newIso === "github" && (
            <div className="space-y-2 rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
              <Input placeholder="repository URL *required — https://github.com/owner/repo.git" value={newRepoUrl}
                onChange={(e) => setNewRepoUrl(e.target.value)} />
              <Input placeholder="branch — optional, default HEAD" value={newBranch} onChange={(e) => setNewBranch(e.target.value)} />
              <p className="flex items-start gap-1 text-[11px] text-zinc-500">
                <Info size={12} className="mt-0.5 shrink-0" />
                Sync before every dispatch: no repo in the work dir → clone; clean and behind → fast-forward pull to latest; uncommitted changes or local commits → warning, never overwritten.
              </p>
            </div>
          )}
          {newIso === "worktree" && (
            <div className="rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
              <Input placeholder="local repository path (absolute)" value={newRepoPath} onChange={(e) => setNewRepoPath(e.target.value)} />
              <p className="mt-2 flex items-start gap-1 text-[11px] text-zinc-500">
                <Info size={12} className="mt-0.5 shrink-0" />
                Each task branches a detached git worktree; results stay inspectable after the run.
              </p>
            </div>
          )}
          {newIso === "standalone" && (
            <p className="flex items-start gap-1 rounded-xl border border-zinc-200/80 p-3 text-[11px] text-zinc-500 dark:border-zinc-800">
              <Info size={12} className="mt-0.5 shrink-0" />
              No repo binding — the worker starts in its work dir with an empty workspace.
            </p>
          )}
        </section>

        {/* Step 3: LLM */}
        <section>
          <p className="mb-2 text-xs font-medium text-zinc-500">3 · LLM</p>
          <div className="flex flex-wrap gap-2">
            <Select className="min-w-52 flex-1" value={newProvider} onChange={(e) => setNewProvider(e.target.value)}>
              <option value="">provider: CLI default</option>
              {providers.map((p) => (
                <option key={p.id} value={p.id}>provider: {p.name || p.id}</option>
              ))}
            </Select>
            <Input className="min-w-44 flex-1" placeholder="model — overrides provider default" value={newModel}
              onChange={(e) => setNewModel(e.target.value)} />
          </div>
        </section>

        {/* Step 4: name & limits */}
        <section>
          <p className="mb-2 text-xs font-medium text-zinc-500">4 · Name &amp; limits</p>
          <div className="flex flex-wrap gap-2">
            <Input className="min-w-40 flex-1" placeholder="name — used for @-dispatch" value={newName} onChange={(e) => setNewName(e.target.value)} />
            <label className="flex items-center gap-1 text-xs text-zinc-500">
              budget $
              <Input className="w-20" type="number" min={0.5} step={0.5} value={newBudget} onChange={(e) => setNewBudget(Number(e.target.value))} />
            </label>
            <Select className="w-48" value={newMemory} onChange={(e) => setNewMemory(e.target.value)}>
              <option value="notes">memory: notes (recommended)</option>
              <option value="fusion-memory">memory: fusion-memory hub</option>
              <option value="off">memory: off</option>
            </Select>
          </div>
        </section>

        <div className="flex items-center justify-between border-t border-zinc-200/80 pt-3 dark:border-zinc-800">
          <p className="text-xs text-zinc-500">{summary}</p>
          <Button onClick={addWorker} disabled={busy || !newAgent}><Save size={14} /> Save worker</Button>
        </div>
        {msg && <p className="text-sm text-indigo-500">{msg}</p>}
        {err && <p className="text-sm text-red-500">{err}</p>}
      </div>

      {/* Existing workers — compact list, click a row to edit its details */}
      <div className="space-y-2">
        <h3 className="text-sm font-semibold">Existing workers</h3>
        {(orch?.workers ?? []).length === 0 && (
          <p className="text-sm text-zinc-400">No workers yet — add the first one above.</p>
        )}
        {(orch?.workers ?? []).map((w, idx) => (
          <div key={idx} className="rounded-xl border border-zinc-200/80 dark:border-zinc-800">
            <button onClick={() => setEditing(editing === idx ? null : idx)}
              className="flex w-full flex-wrap items-center gap-2 p-3 text-left text-sm hover:bg-zinc-50 dark:hover:bg-zinc-900/60">
              {editing === idx ? <ChevronDown size={14} className="text-zinc-400" /> : <ChevronRight size={14} className="text-zinc-400" />}
              <span className="font-medium">{w.name || w.agent}</span>
              <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">{w.agent}</span>
              <span className="text-xs text-zinc-500">{ISO_LABEL[(w.isolation ?? "worktree") as IsoMode] ?? w.isolation}</span>
              {w.repo && <span className="truncate text-xs text-zinc-400" title={w.repo}>{w.repo}</span>}
              {!detected(w.agent) && <span className="text-xs text-amber-500">not on PATH</span>}
              <span className={`ml-auto flex items-center gap-1 text-xs ${w.enabled ? "text-emerald-500" : "text-zinc-400"}`}>
                {w.enabled ? <><CheckCircle size={12} /> enabled</> : "disabled"}
              </span>
            </button>
            {editing === idx && (
              <div className="space-y-2 border-t border-zinc-200/80 p-3 text-sm dark:border-zinc-800">
                <div className="flex flex-wrap items-center gap-2">
                  <label className="flex items-center gap-1 text-xs text-zinc-500" title="Unique name used for @-dispatch">
                    name
                    <Input className="w-36" placeholder="unique name" value={w.name ?? ""} onChange={(e) => setWorker(idx, { name: e.target.value })} />
                  </label>
                  <Select className="w-44" value={w.isolation ?? "worktree"}
                    onChange={(e) => setWorker(idx, { isolation: e.target.value as WorkerSettings["isolation"] })}>
                    <option value="github">source: github</option>
                    <option value="worktree">source: worktree</option>
                    <option value="standalone">source: standalone</option>
                  </Select>
                  <label className="flex items-center gap-1 text-xs text-zinc-500">
                    budget $
                    <Input className="w-20" type="number" min={0.5} step={0.5} value={w.max_budget_usd ?? 5}
                      onChange={(e) => setWorker(idx, { max_budget_usd: Number(e.target.value) })} />
                  </label>
                  <label className="flex items-center gap-1 text-xs text-zinc-500">
                    <input type="checkbox" checked={w.enabled} onChange={(e) => setWorker(idx, { enabled: e.target.checked })} /> enabled
                  </label>
                </div>
                <Input className="w-full" placeholder="work dir — empty = default" value={w.work_dir ?? ""}
                  onChange={(e) => setWorker(idx, { work_dir: e.target.value })} />
                {w.isolation === "github" && (
                  <div className="flex flex-wrap items-center gap-2">
                    <Input className="min-w-48 flex-1" placeholder="repository URL" value={w.repo ?? ""}
                      onChange={(e) => setWorker(idx, { repo: e.target.value })} />
                    <Input className="w-36" placeholder="branch (optional)" value={w.repo_branch ?? ""}
                      onChange={(e) => setWorker(idx, { repo_branch: e.target.value })} />
                  </div>
                )}
                {w.isolation === "worktree" && (
                  <Input className="w-full" placeholder="local repo path (absolute)" value={w.repo ?? ""}
                    onChange={(e) => setWorker(idx, { repo: e.target.value })} />
                )}
                <div className="flex flex-wrap items-center gap-2">
                  <Select className="w-48" value={w.provider_id ?? ""} onChange={(e) => setWorker(idx, { provider_id: e.target.value })}>
                    <option value="">provider: CLI default</option>
                    {providers.map((p) => (
                      <option key={p.id} value={p.id}>provider: {p.name || p.id}</option>
                    ))}
                  </Select>
                  <Input className="w-44" placeholder="model (optional)" value={w.model ?? ""}
                    onChange={(e) => setWorker(idx, { model: e.target.value })} />
                  <Select className="w-48" value={w.memory ?? "notes"} onChange={(e) => setWorker(idx, { memory: e.target.value })}>
                    <option value="notes">memory: notes (recommended)</option>
                    <option value="fusion-memory">memory: fusion-memory hub</option>
                    <option value="off">memory: off</option>
                  </Select>
                </div>
                <div className="flex items-center gap-2 pt-1">
                  <Button onClick={() => orch && saveOrch(orch)} disabled={busy}><Save size={14} /> Save</Button>
                  <Button variant="outline" onClick={() => setEditing(null)} disabled={busy}>Close</Button>
                  <Button variant="ghost" className="ml-auto" onClick={() => removeWorker(idx)} disabled={busy}>Remove</Button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
