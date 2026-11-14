import { useEffect, useState } from "react";
import { Terminal } from "lucide-react";
import { api, type AgentInfo, type Provider, type WorkerSettings } from "./api";
import { Button, Input, Select } from "./components/ui";

// Worker management shared by the wizard's Workers step and the Agents page.
// Workers are ephemeral coding-agent runs: each has a unique name (used for
// @-mention dispatch), its own workspace directory, a default repo for
// worktree isolation, a budget cap and a provider binding. Several workers
// may share one agent CLI type. Saved into the orchestrator config.
export function WorkersPanel({ onSaved }: { onSaved?: () => void }) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [orch, setOrch] = useState<Awaited<ReturnType<typeof api.orchestration>>["orchestrator"] | null>(null);
  const [newAgent, setNewAgent] = useState("");
  const [newName, setNewName] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
    api.providers().then((r) => setProviders((r.providers ?? []).filter((p) => p.enabled))).catch(() => {});
    api.orchestration().then((r) => setOrch(r.orchestrator)).catch(() => {});
  }, []);

  const detected = (name: string) => agents.find((a) => a.name === name)?.detected ?? false;
  const setWorker = (idx: number, patch: Partial<WorkerSettings>) => {
    const base = orch ?? { mode: "confirm", brain_agent: "claude", repos: [], workers: [], configured: false };
    const workers = [...(base.workers ?? [])];
    workers[idx] = { ...workers[idx], ...patch };
    setOrch({ ...base, workers });
  };
  const addWorker = () => {
    if (!newAgent) return;
    const base = orch ?? { mode: "confirm", brain_agent: "claude", repos: [], workers: [], configured: false };
    const workers = [...(base.workers ?? [])];
    // Default name: <agent>-<n> — unique across the fleet.
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
    workers.push({ name, agent: newAgent, enabled: true, isolation: "worktree", permission_mode: "acceptEdits", max_budget_usd: 5, provider_id: "", work_dir: "", repo: "" });
    setOrch({ ...base, workers });
    setNewAgent("");
    setNewName("");
    setErr("");
  };
  const removeWorker = (idx: number) => {
    if (!orch) return;
    setOrch({ ...orch, workers: orch.workers.filter((_, i) => i !== idx) });
  };

  const save = async () => {
    setMsg(""); setErr("");
    const workers = orch?.workers ?? [];
    const seen = new Set<string>();
    for (const w of workers) {
      const id = w.name || w.agent;
      if (seen.has(id)) {
        setErr(`duplicate worker name "${id}" — names must be unique`);
        return;
      }
      seen.add(id);
    }
    try {
      if (orch) await api.saveOrchestration(orch);
      setMsg("workers saved");
      onSaved?.();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  return (
    <div className="space-y-3">
      <p className="text-xs text-zinc-500">
        Workers are ephemeral coding-agent runs. Each has a unique name (the brain and the
        chat <code>@</code> menu address workers by name), its own workspace directory, and —
        for worktree isolation — its own default repo. Multiple workers can share one agent CLI.
      </p>
      <div className="space-y-2">
        {(orch?.workers ?? []).map((w, idx) => (
          <div key={idx} className="rounded-xl border border-zinc-200/80 p-3 text-sm dark:border-zinc-800">
            <div className="flex flex-wrap items-center gap-3">
              <span className="flex items-center gap-2 font-medium">
                <Terminal size={14} /> {w.name || w.agent}
                <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800">type: {w.agent}</span>
                {!detected(w.agent) && <span className="text-xs text-amber-500">not on PATH</span>}
              </span>
              <label className="flex items-center gap-1 text-xs text-zinc-500" title="Unique name used for @-dispatch">
                name
                <Input className="w-36" placeholder="unique name" value={w.name ?? ""}
                  onChange={(e) => setWorker(idx, { name: e.target.value })} />
              </label>
              <Select className="w-44" value={w.isolation ?? "worktree"}
                onChange={(e) => setWorker(idx, { isolation: e.target.value as WorkerSettings["isolation"] })}>
                <option value="worktree">isolation: worktree</option>
                <option value="standalone">isolation: standalone repo</option>
              </Select>
              <label className="flex items-center gap-1 text-xs text-zinc-500">
                budget $
                <Input className="w-20" type="number" min={0.5} step={0.5} value={w.max_budget_usd ?? 5}
                  onChange={(e) => setWorker(idx, { max_budget_usd: Number(e.target.value) })} />
              </label>
              <Select className="w-48" value={w.provider_id ?? ""}
                onChange={(e) => setWorker(idx, { provider_id: e.target.value })}>
                <option value="">provider: CLI default</option>
                {providers.map((p) => (
                  <option key={p.id} value={p.id}>provider: {p.name || p.id}</option>
                ))}
              </Select>
              <button onClick={() => removeWorker(idx)} title="Remove worker"
                className="ml-auto rounded-lg px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-100 hover:text-red-500 dark:hover:bg-zinc-800">
                remove
              </button>
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-3">
              <label className="flex flex-1 items-center gap-1 text-xs text-zinc-500" title="Dedicated workspace; empty = ~/.fusion-console/agents/<name>">
                work dir
                <Input className="min-w-48 flex-1" placeholder="~/.fusion-console/agents/<name>" value={w.work_dir ?? ""}
                  onChange={(e) => setWorker(idx, { work_dir: e.target.value })} />
              </label>
              {(w.isolation ?? "worktree") === "worktree" && (
                <label className="flex flex-1 items-center gap-1 text-xs text-zinc-500" title="Default repo to branch worktrees from; the brain can still pass another repo per task">
                  repo (worktree)
                  <Input className="min-w-48 flex-1" placeholder="/absolute/path/to/repo" value={w.repo ?? ""}
                    onChange={(e) => setWorker(idx, { repo: e.target.value })} />
                </label>
              )}
            </div>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2 rounded-xl border border-dashed border-zinc-300 p-3 dark:border-zinc-700">
        <Select className="w-56" value={newAgent} onChange={(e) => setNewAgent(e.target.value)}>
          <option value="">choose agent type...</option>
          {agents.filter((a) => !a.is_worker).map((a) => (
            <option key={a.name} value={a.name}>{a.name} ({a.command}){a.detected ? "" : " — not on PATH"}</option>
          ))}
        </Select>
        <Input className="w-40" placeholder="name (optional)" value={newName} onChange={(e) => setNewName(e.target.value)} />
        <Button variant="outline" onClick={addWorker} disabled={!newAgent}>Add worker</Button>
      </div>
      <div className="flex items-center gap-3">
        <Button onClick={save}>Save workers</Button>
        {msg && <span className="text-sm text-indigo-500">{msg}</span>}
        {err && <span className="text-sm text-red-500">{err}</span>}
      </div>
    </div>
  );
}
