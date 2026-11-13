import { useCallback, useEffect, useState } from "react";
import {
  ArrowRight,
  Bot,
  Brain,
  Check,
  Cloud,
  Cpu,
  KeyRound,
  Network,
  Sparkles,
  Terminal,
  Workflow,
} from "lucide-react";
import { api, type AgentInfo, type Provider, type WorkerSettings } from "./api";
import { Badge, Button, Card, Input, Select } from "./components/ui";

type StepId = "welcome" | "llm" | "gateway" | "brain" | "workers" | "done";

const STEPS: { id: StepId; label: string; icon: typeof Brain }[] = [
  { id: "welcome", label: "Welcome", icon: Sparkles },
  { id: "gateway", label: "Gateway", icon: Network },
  { id: "llm", label: "LLM", icon: Cloud },
  { id: "brain", label: "Brain", icon: Brain },
  { id: "workers", label: "Workers", icon: Bot },
  { id: "done", label: "Done", icon: Check },
];

export default function Onboarding({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState<StepId>("welcome");
  const idx = STEPS.findIndex((s) => s.id === step);
  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col p-6">
      <header className="mb-6">
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          <Terminal size={20} className="text-indigo-500" /> fusion-console setup
        </h1>
        <p className="mt-1 text-sm text-zinc-500">
          Guided setup — configure the LLM, the local gateway, then the brain and its workers.
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
      <div className="flex-1 overflow-y-auto">
        {step === "welcome" && <WelcomeStep onNext={() => setStep("gateway")} />}
        {step === "gateway" && <GatewayStep onNext={() => setStep("llm")} onSkip={() => setStep("llm")} />}
        {step === "llm" && <LlmStep onNext={() => setStep("brain")} onSkip={() => setStep("brain")} />}
        {step === "brain" && <BrainStep onNext={() => setStep("workers")} onSkip={() => setStep("workers")} />}
        {step === "workers" && <WorkersStep onNext={() => setStep("done")} onSkip={() => setStep("done")} />}
        {step === "done" && <DoneStep onDone={onDone} />}
      </div>
    </div>
  );
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <Card className="space-y-4 p-6">
      <p className="text-sm leading-6 text-zinc-600 dark:text-zinc-300">
        This wizard walks through four decisions. Nothing here is hidden in config files — every value
        lands in <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">config.json</code> or the OS
        keychain, and can be changed later under Settings.
      </p>
      <ul className="space-y-2 text-sm">
        {[
          { icon: Cloud, t: "LLM provider", d: "cloud OpenAI/Anthropic-compatible endpoint, or the local fusion-gateway" },
          { icon: Network, t: "fusion-gateway", d: "bundled at packaging time; started together with the console by start.sh" },
          { icon: Brain, t: "Brain", d: "one coding-agent CLI acting as the architect session" },
          { icon: Bot, t: "Workers", d: "per-agent isolation (worktree or standalone repo), permission mode, budget cap" },
        ].map((r) => (
          <li key={r.t} className="flex items-start gap-2">
            <r.icon size={16} className="mt-0.5 shrink-0 text-indigo-500" />
            <span><strong>{r.t}</strong> — {r.d}</span>
          </li>
        ))}
      </ul>
      <Button onClick={onNext}>Start <ArrowRight size={14} /></Button>
    </Card>
  );
}

const PRESETS = {
  gateway: { id: "gateway", name: "fusion-gateway (local)", type: "gateway", base_url: "http://127.0.0.1:11432/v1", model: "glm-5" },
  openai: { id: "cloud", name: "Cloud (OpenAI-compatible)", type: "openai_compat", base_url: "https://api.openai.com/v1", model: "" },
  anthropic: { id: "cloud", name: "Cloud (Anthropic-compatible)", type: "anthropic_compat", base_url: "https://api.anthropic.com/v1", model: "" },
} as const;

function LlmStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  const [type, setType] = useState<"gateway" | "openai" | "anthropic">("gateway");
  const [draft, setDraft] = useState<Provider>({
    id: PRESETS.gateway.id, name: PRESETS.gateway.name, type: "gateway",
    base_url: PRESETS.gateway.base_url, model: PRESETS.gateway.model, api_key_id: "", enabled: true,
  });
  const [secret, setSecret] = useState("");
  const [models, setModels] = useState<string[]>([]);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const pick = (t: "gateway" | "openai" | "anthropic") => {
    setType(t);
    setModels([]);
    setErr("");
    const p = PRESETS[t];
    setDraft({ id: p.id, name: p.name, type: p.type, base_url: p.base_url, model: p.model, api_key_id: "", enabled: true });
  };

  const saveAll = async (): Promise<boolean> => {
    setMsg(""); setErr(""); setBusy(true);
    try {
      const p = { ...draft, api_key_id: draft.api_key_id || draft.id + "-key" };
      if (secret) await api.storeApiKey(p.api_key_id, secret);
      await api.saveProvider(p);
      setDraft(p);
      setMsg(`provider ${p.id} saved${secret ? " + api key in OS keychain" : ""}`);
      return true;
    } catch (ex) {
      setErr((ex as Error).message);
      return false;
    } finally {
      setBusy(false);
    }
  };

  const fetchModels = async () => {
    setMsg(""); setErr(""); setModels([]); setBusy(true);
    try {
      if (!(await saveAll())) return;
      const r = await api.models(draft.id);
      setModels(r.models ?? []);
      setMsg(`${(r.models ?? []).length} models fetched — pick one, then continue`);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // Smoke call: proves endpoint + key + model work before leaving the step.
  const testCall = async () => {
    setMsg(""); setErr(""); setBusy(true);
    try {
      if (!(await saveAll())) return;
      const r = await api.testCall(draft.id);
      if (r.ok) setMsg(`connection verified (${r.latency_ms} ms) — model replied: "${(r.sample ?? "").trim().slice(0, 40)}"`);
      else setErr(`test call failed: ${r.error}`);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="space-y-4 p-6">
      <div className="grid gap-3 sm:grid-cols-3">
        {(
          [
            ["gateway", "fusion-gateway", "local, bundled", Network],
            ["openai", "OpenAI-compatible", "cloud, first choice", Cloud],
            ["anthropic", "Anthropic-compatible", "cloud, first choice", Cloud],
          ] as const
        ).map(([t, title, sub, Icon]) => (
          <button
            key={t}
            onClick={() => pick(t)}
            className={`rounded-xl border p-3 text-left transition-colors ${
              type === t
                ? "border-indigo-500 bg-indigo-500/5"
                : "border-zinc-200/80 hover:border-zinc-300 dark:border-zinc-800 dark:hover:border-zinc-700"
            }`}
          >
            <Icon size={16} className="text-indigo-500" />
            <p className="mt-1 text-sm font-medium">{title}</p>
            <p className="text-xs text-zinc-500">{sub}</p>
          </button>
        ))}
      </div>

      <div className="grid grid-cols-2 gap-3">
        <label className="text-xs text-zinc-500">base URL
          <Input value={draft.base_url} onChange={(e) => setDraft({ ...draft, base_url: e.target.value })} />
        </label>
        <label className="text-xs text-zinc-500">model
          <Input value={draft.model} onChange={(e) => setDraft({ ...draft, model: e.target.value })} placeholder="e.g. glm-5 / gpt-… / claude-…" />
        </label>
      </div>
      {type === "gateway" ? (
        <div className="space-y-2 rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
          <p className="text-sm font-medium">Gateway API key</p>
          <p className="text-xs text-zinc-500">
            Provisioned automatically in the previous step and stored in the OS keychain as{" "}
            <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">gateway-key</code>. Missing it?
            Go back to the Gateway step (or open the Gateway admin GUI and paste a key below).
          </p>
          <label className="flex items-center gap-2 text-xs text-zinc-500">
            paste key instead (optional)
            <Input className="flex-1" type="password" placeholder="sk-…" value={secret}
              onChange={(e) => setSecret(e.target.value)} />
          </label>
        </div>
      ) : (
        <>
          <label className="flex items-center gap-2 text-sm">
            <Cpu size={14} className="text-zinc-400" />
            <Input className="flex-1" placeholder="api key id (keychain ref)" value={draft.api_key_id}
              onChange={(e) => setDraft({ ...draft, api_key_id: e.target.value })} />
            <Input className="flex-1" type="password" placeholder="secret → OS keychain" value={secret}
              onChange={(e) => setSecret(e.target.value)} />
          </label>
          <p className="text-xs text-zinc-400">
            Headless host without a keychain? Skip the secret here and export{" "}
            <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">FUSION_CONSOLE_APIKEY_{draft.api_key_id ? draft.api_key_id.toUpperCase().replace(/[^A-Z0-9]/g, "_") : "&lt;KEY_ID&gt;"}</code>{" "}
            before starting the console.
          </p>
        </>
      )}

      {models.length > 0 && (
        <Select value={draft.model} onChange={(e) => setDraft({ ...draft, model: e.target.value })}>
          {models.map((m) => <option key={m} value={m}>{m}</option>)}
        </Select>
      )}

      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={fetchModels} disabled={busy}>Save & fetch models</Button>
        <Button variant="outline" onClick={testCall} disabled={busy}>Test call</Button>
        <Button onClick={async () => { if (await saveAll()) onNext(); }} disabled={busy}>Save & continue <ArrowRight size={14} /></Button>
        <Button variant="ghost" onClick={onSkip}>Skip for now</Button>
      </div>
      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
    </Card>
  );
}

function GatewayStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  const [status, setStatus] = useState<Awaited<ReturnType<typeof api.gatewayStatus>> | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [adminUser, setAdminUser] = useState("");
  const [adminPass, setAdminPass] = useState("");

  const load = useCallback(() => {
    api.gatewayStatus().then(setStatus).catch(() => setStatus(null));
  }, []);
  useEffect(() => { load(); }, [load]);

  const install = async () => {
    setBusy(true); setMsg(""); setErr("");
    try {
      const r = await api.gatewayInstall();
      setMsg(`${r.note} (v${r.version})`);
      load();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const saveAdminCreds = async () => {
    setMsg(""); setErr("");
    try {
      await api.storeApiKey("gateway-admin-user", adminUser);
      await api.storeApiKey("gateway-admin-pass", adminPass);
      setMsg("gateway admin credentials saved (OS keychain)");
      setAdminPass("");
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  // One click: gateway admin login + key creation, raw key lands in the
  // keychain under "gateway-key" — the LLM step then needs nothing typed.
  const provisionKey = async () => {
    setMsg(""); setErr(""); setBusy(true);
    try {
      const r = await api.provisionGatewayKey();
      setMsg(`gateway API key ready (${r.key_prefix}…) — the LLM step will use it automatically`);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="space-y-4 p-6">
      <p className="text-sm text-zinc-600 dark:text-zinc-300">
        The matching <strong>fusion-gateway</strong> binary ships inside the console package and is
        started together with the console (by <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">start.sh</code>{" "}
        or automatically on launch).
      </p>
      <div className="flex items-center justify-between rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
        <div className="text-sm">
          <p className="font-medium flex items-center gap-2">
            <Network size={16} /> {status?.base_url || "…"}
            {status?.reachable ? <Badge tone="ok">running</Badge> : <Badge tone="warn">not reachable</Badge>}
          </p>
          <p className="mt-1 text-xs text-zinc-500">
            {status?.installed ? `bundled binary installed: ${status.binary}` : "binary not installed yet"}
          </p>
        </div>
        {!status?.reachable && (
          <Button variant="outline" onClick={install} disabled={busy}>
            {busy ? "Installing…" : "Install bundled gateway"}
          </Button>
        )}
      </div>

      {status?.reachable && (
        <div className="space-y-3 rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
          <p className="text-sm font-medium">Gateway admin & API key</p>
          <p className="text-xs text-zinc-500">
            Save the gateway admin account once, then provision the API key the console will use for
            chat — no copy-paste from the admin GUI needed.
          </p>
          <div className="flex flex-wrap gap-2">
            <Input className="w-44" placeholder="admin username" value={adminUser}
              onChange={(e) => setAdminUser(e.target.value)} />
            <Input className="w-44" type="password" placeholder="admin password" value={adminPass}
              onChange={(e) => setAdminPass(e.target.value)} />
            <Button variant="outline" onClick={saveAdminCreds} disabled={!adminUser || !adminPass}>
              Save credentials
            </Button>
            <Button onClick={provisionKey} disabled={busy}>Provision API key</Button>
          </div>
        </div>
      )}

      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
      <div className="flex gap-2">
        <Button onClick={onNext}>Continue <ArrowRight size={14} /></Button>
        <Button variant="ghost" onClick={onSkip}>Skip</Button>
      </div>
      <p className="text-xs text-zinc-400">
        Local gateway is optional — cloud providers work without it. It is required only for local models.
      </p>
    </Card>
  );
}

function BrainStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [orch, setOrch] = useState<Awaited<ReturnType<typeof api.orchestration>>["orchestrator"] | null>(null);
  useEffect(() => {
    api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
    api.orchestration().then((r) => setOrch(r.orchestrator)).catch(() => {});
  }, []);
  const mode = orch?.mode ?? "confirm";
  const brain = orch?.brain_agent || agents.find((a) => a.detected)?.name || "claude";
  const set = (patch: Partial<NonNullable<typeof orch>>) =>
    setOrch({ mode: "confirm", brain_agent: "claude", repos: [], workers: [], configured: false, ...orch, ...patch });

  return (
    <Card className="space-y-4 p-6">
      <p className="text-sm text-zinc-600 dark:text-zinc-300">
        The <strong>brain</strong> is one coding-agent CLI that acts as the architect: it receives your
        requirements, splits work into issues for the workers, reviews their diffs, and reports back.
      </p>
      <label className="block text-xs text-zinc-500">brain agent (detected CLIs only)
        <Select value={brain} onChange={(e) => set({ brain_agent: e.target.value })}>
          {agents.filter((a) => a.detected).map((a) => (
            <option key={a.name} value={a.name}>{a.name} ({a.command})</option>
          ))}
        </Select>
      </label>
      <div>
        <p className="mb-1 text-xs text-zinc-500">dispatch approval</p>
        <div className="flex gap-2">
          {(
            [
              ["confirm", "Confirm each dispatch (recommended)"],
              ["auto", "Auto-dispatch within budget caps"],
              ["off", "Off — workers only, no brain loop"],
            ] as const
          ).map(([m, label]) => (
            <button
              key={m}
              onClick={() => set({ mode: m })}
              className={`rounded-xl border px-3 py-2 text-xs transition-colors ${
                mode === m ? "border-indigo-500 bg-indigo-500/5 text-indigo-600 dark:text-indigo-400" : "border-zinc-200/80 dark:border-zinc-800"
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      <label className="block text-xs text-zinc-500">worker repo whitelist (comma separated, empty = all fusion-* repos)
        <Input
          placeholder="e.g. /Users/dahai/fusion/fusion-doc, /Users/dahai/fusion/fusion-insight"
          value={(orch?.repos ?? []).join(", ")}
          onChange={(e) => set({ repos: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })}
        />
      </label>
      <div className="flex gap-2">
        <Button onClick={onNext}>Continue <ArrowRight size={14} /></Button>
        <Button variant="ghost" onClick={onSkip}>Skip</Button>
      </div>
    </Card>
  );
}

function WorkersStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [orch, setOrch] = useState<Awaited<ReturnType<typeof api.orchestration>>["orchestrator"] | null>(null);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
    api.orchestration().then((r) => setOrch(r.orchestrator)).catch(() => {});
  }, []);

  const worker = (name: string) => orch?.workers.find((w) => w.agent === name);
  const setWorker = (name: string, patch: { enabled?: boolean; isolation?: WorkerSettings["isolation"]; max_budget_usd?: number }) => {
    const base = orch ?? { mode: "confirm", brain_agent: "claude", repos: [], workers: [], configured: false };
    const workers = [...(base.workers ?? [])];
    const i = workers.findIndex((w) => w.agent === name);
    const cur = i >= 0 ? workers[i] : { agent: name, enabled: false, isolation: "worktree" as const, permission_mode: "acceptEdits", max_budget_usd: 5 };
    workers[i >= 0 ? i : workers.length] = { ...cur, ...patch };
    setOrch({ ...base, workers });
  };

  const save = async (after: () => void) => {
    setMsg(""); setErr("");
    try {
      if (orch) await api.saveOrchestration(orch);
      setMsg("brain & workers saved");
      after();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  return (
    <Card className="space-y-4 p-6">
      <p className="text-sm text-zinc-600 dark:text-zinc-300">
        Workers are ephemeral coding-agent runs: each task gets an isolated workspace
        (<strong>worktree</strong> in the target repo, or a <strong>standalone</strong> repo clone),
        a budget cap, and dies on completion.
      </p>
      <div className="space-y-2">
        {agents.map((a) => {
          const w = worker(a.name);
          return (
            <div key={a.name} className={`rounded-xl border p-3 text-sm ${!a.detected ? "opacity-50" : "border-zinc-200/80 dark:border-zinc-800"}`}>
              <div className="flex flex-wrap items-center gap-3">
                <label className="flex items-center gap-2 font-medium">
                  <input
                    type="checkbox"
                    checked={w?.enabled ?? a.name === "claude"}
                    disabled={!a.detected}
                    onChange={(e) => setWorker(a.name, { enabled: e.target.checked })}
                  />
                  <Terminal size={14} /> {a.name}
                </label>
                <Select className="w-44" value={w?.isolation ?? "worktree"}
                  onChange={(e) => setWorker(a.name, { isolation: e.target.value as WorkerSettings["isolation"] })}>
                  <option value="worktree">isolation: worktree</option>
                  <option value="standalone">isolation: standalone repo</option>
                </Select>
                <label className="flex items-center gap-1 text-xs text-zinc-500">
                  budget $
                  <Input className="w-20" type="number" min={0.5} step={0.5} value={w?.max_budget_usd ?? 5}
                    onChange={(e) => setWorker(a.name, { max_budget_usd: Number(e.target.value) })} />
                </label>
                <span className="ml-auto text-xs text-zinc-400">
                  {a.detected ? `mode ${a.permission_mode}` : "not on PATH"}
                </span>
              </div>
            </div>
          );
        })}
      </div>
      <div className="flex gap-2">
        <Button onClick={() => save(onNext)}>Save & finish <ArrowRight size={14} /></Button>
        <Button variant="ghost" onClick={onSkip}>Skip</Button>
      </div>
      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
    </Card>
  );
}

function DoneStep({ onDone }: { onDone: () => void }) {
  return (
    <Card className="space-y-4 p-6">
      <div className="flex items-center gap-2">
        <Workflow size={18} className="text-emerald-500" />
        <h3 className="font-semibold">Setup complete</h3>
      </div>
      <ul className="space-y-1 text-sm text-zinc-600 dark:text-zinc-300">
        <li className="flex items-center gap-2"><Check size={14} className="text-emerald-500" /> LLM provider saved (key in OS keychain)</li>
        <li className="flex items-center gap-2"><Check size={14} className="text-emerald-500" /> Gateway status checked — start.sh keeps both processes up</li>
        <li className="flex items-center gap-2"><Check size={14} className="text-emerald-500" /> Brain & workers stored — every value editable under Settings later</li>
      </ul>
      <p className="text-xs text-zinc-400">
        Tip: flip the `orchestrate` toggle in chat to let the brain dispatch the workers you just
        configured (confirm mode asks you per dispatch).
      </p>
      <Button onClick={onDone}>Open chat <ArrowRight size={14} /></Button>
    </Card>
  );
}
