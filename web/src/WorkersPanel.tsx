import { useEffect, useState } from "react";
import { Terminal } from "lucide-react";
import { api, type AgentInfo, type Provider, type WorkerSettings } from "./api";
import { Button, Input, Select } from "./components/ui";

// Worker management shared by the wizard's Workers step and the Agents page:
// workers are added one at a time from a dropdown of agent CLIs, each with
// isolation mode, budget cap, provider binding and a standalone work
// directory. Saved into the orchestrator config.
export function WorkersPanel({ onSaved }: { onSaved?: () => void }) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [orch, setOrch] = useState<Awaited<ReturnType<typeof api.orchestration>>["orchestrator"] | null>(null);
  const [newAgent, setNewAgent] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
    api.providers().then((r) => setProviders((r.providers ?? []).filter((p) => p.enabled))).catch(() => {});
    api.orchestration().then((r) => setOrch(r.orchestrator)).catch(() => {});
  }, []);

  const detected = (name: string) => agents.find((a) => a.name === name)?.detected ?? false;
  const setWorker = (name: string, patch: { enabled?: boolean; isolation?: WorkerSettings["isolation"]; max_budget_usd?: number; provider_id?: string; work_dir?: string }) => {
    const base = orch ?? { mode: "confirm", brain_agent: "claude", repos: [], workers: [], configured: false };
    const workers = [...(base.workers ?? [])];
    const i = workers.findIndex((w) => w.agent === name);
    const cur = i >= 0 ? workers[i] : { agent: name, enabled: true, isolation: "worktree" as const, permission_mode: "acceptEdits", max_budget_usd: 5, provider_id: "", work_dir: "" };
    workers[i >= 0 ? i : workers.length] = { ...cur, ...patch };
    setOrch({ ...base, workers });
  };
  const addWorker = () => {
    if (!newAgent) return;
    setWorker(newAgent, { enabled: true });
    setNewAgent("");
  };
  const removeWorker = (name: string) => {
    if (!orch) return;
    setOrch({ ...orch, workers: orch.workers.filter((w) => w.agent !== name) });
  };

  const save = async () => {
    setMsg(""); setErr("");
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
        Workers are ephemeral coding-agent runs: each task gets an isolated workspace
        (worktree in the target repo, or a standalone repo clone), a budget cap, and dies
        on completion. Add them one at a time.
      </p>
      <div className="space-y-2">
        {(orch?.workers ?? []).map((w) => (
          <div key={w.agent} className="rounded-xl border border-zinc-200/80 p-3 text-sm dark:border-zinc-800">
            <div className="flex flex-wrap items-center gap-3">
              <span className="flex items-center gap-2 font-medium">
                <Terminal size={14} /> {w.agent}
                {!detected(w.agent) && <span className="text-xs text-amber-500">not on PATH</span>}
              </span>
              <Select className="w-44" value={w.isolation ?? "worktree"}
                onChange={(e) => setWorker(w.agent, { isolation: e.target.value as WorkerSettings["isolation"] })}>
                <option value="worktree">isolation: worktree</option>
                <option value="standalone">isolation: standalone repo</option>
              </Select>
              <label className="flex items-center gap-1 text-xs text-zinc-500">
                budget $
                <Input className="w-20" type="number" min={0.5} step={0.5} value={w.max_budget_usd ?? 5}
                  onChange={(e) => setWorker(w.agent, { max_budget_usd: Number(e.target.value) })} />
              </label>
              <Select className="w-48" value={w.provider_id ?? ""}
                onChange={(e) => setWorker(w.agent, { provider_id: e.target.value })}>
                <option value="">provider: CLI default</option>
                {providers.map((p) => (
                  <option key={p.id} value={p.id}>provider: {p.name || p.id}</option>
                ))}
              </Select>
              {(w.isolation ?? "worktree") === "standalone" && (
                <label className="flex items-center gap-1 text-xs text-zinc-500" title="Standalone workspace directory; empty = default per-agent dir">
                  dir
                  <Input className="w-44" placeholder="~/.fusion-console/agents/{name}" value={w.work_dir ?? ""}
                    onChange={(e) => setWorker(w.agent, { work_dir: e.target.value })} />
                </label>
              )}
              <button onClick={() => removeWorker(w.agent)} title="Remove worker"
                className="ml-auto rounded-lg px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-100 hover:text-red-500 dark:hover:bg-zinc-800">
                remove
              </button>
            </div>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2 rounded-xl border border-dashed border-zinc-300 p-3 dark:border-zinc-700">
        <Select className="w-56" value={newAgent} onChange={(e) => setNewAgent(e.target.value)}>
          <option value="">choose agent type...</option>
          {agents.map((a) => (
            <option key={a.name} value={a.name}>{a.name} ({a.command}){a.detected ? "" : " — not on PATH"}</option>
          ))}
        </Select>
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
