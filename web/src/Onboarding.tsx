import { useCallback, useEffect, useState } from "react";
import {
  ArrowRight,
  Bot,
  Brain,
  Check,
  Cloud,
  KeyRound,
  Network,
  PlayCircle,
  Sparkles,
  Terminal,
  Workflow,
} from "lucide-react";
import { api, type AgentInfo } from "./api";
import { GatewayBackendsPanel } from "./GatewayBackends";
import { WorkersPanel } from "./WorkersPanel";
import { Badge, Button, Card, Input, Select } from "./components/ui";

type StepId = "welcome" | "key" | "backends" | "brain" | "workers" | "verify" | "done";

const STEPS: { id: StepId; label: string; icon: typeof Brain }[] = [
  { id: "welcome", label: "Welcome", icon: Sparkles },
  { id: "key", label: "API key", icon: KeyRound },
  { id: "backends", label: "Gateway models", icon: Cloud },
  { id: "brain", label: "Brain", icon: Brain },
  { id: "workers", label: "Workers", icon: Bot },
  { id: "verify", label: "Verify", icon: PlayCircle },
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
        {step === "welcome" && <WelcomeStep onNext={() => setStep("key")} />}
        {step === "key" && <KeyStep onNext={() => setStep("backends")} onSkip={() => setStep("backends")} />}
        {step === "backends" && <BackendsStep onNext={() => setStep("brain")} onSkip={() => setStep("brain")} />}
        {step === "brain" && <BrainStep onNext={() => setStep("workers")} onSkip={() => setStep("workers")} />}
        {step === "workers" && <WorkersStep onNext={() => setStep("verify")} onSkip={() => setStep("verify")} />}
        {step === "verify" && <VerifyStep onNext={() => setStep("done")} onSkip={() => setStep("done")} />}
        {step === "done" && <DoneStep onDone={onDone} />}
      </div>
    </div>
  );
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <Card className="space-y-4 p-6">
      <p className="text-sm leading-6 text-zinc-600 dark:text-zinc-300">
        This wizard walks through four decisions. Nothing here is hidden in config files —          lands in <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">config.json</code> or the OS
        keychain, and can be changed later on the Gateway and Agents pages.
      </p>
      <ul className="space-y-2 text-sm">
        {[
          { icon: Cloud, t: "LLM provider", d: "cloud OpenAI/Anthropic-compatible endpoint, or the local fusion-gateway" },
          { icon: Network, t: "fusion-gateway", d: "bundled at packaging time; started together with the console by startup.sh" },
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

function BackendsStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const openAdmin = async () => {
    setBusy(true); setErr(""); setMsg("");
    try {
      await api.gatewaySSO();
      const st = await api.gatewayStatus();
      window.open(st.admin_url, "_blank");
      setMsg("gateway admin opened — add cloud backends under Backends");
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="space-y-4 p-6">
      <div className="flex items-center gap-2">
        <Cloud size={18} className="text-indigo-500" />
        <h3 className="font-semibold">Gateway models</h3>
      </div>
      <p className="text-sm text-zinc-600 dark:text-zinc-300">
        The brain and every worker call the local <strong>fusion-gateway</strong> with the API key
        from the previous step; the gateway routes each request to the model you configured —
        local (fusion-mlx) or cloud. One path, one place, so all token usage lands in one ledger.
      </p>
      <div className="rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
        <GatewayBackendsPanel />
        <button onClick={openAdmin} disabled={busy} className="mt-3 text-xs text-indigo-500 underline-offset-2 hover:underline">
          advanced: open gateway admin GUI
        </button>
      </div>
      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
      <div className="flex gap-2">
        <Button onClick={onNext}>Continue <ArrowRight size={14} /></Button>
        <Button variant="ghost" onClick={onSkip}>Local models only — skip</Button>
      </div>
    </Card>
  );
}

function KeyStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  const [status, setStatus] = useState<Awaited<ReturnType<typeof api.gatewayStatus>> | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [adminUser, setAdminUser] = useState("");
  const [adminPass, setAdminPass] = useState("");
  const [rawKey, setRawKey] = useState("");
  const [copied, setCopied] = useState(false);

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
  // keychain under "gateway-key" — later steps need nothing typed.
  const provisionKey = async () => {
    setMsg(""); setErr(""); setBusy(true); setRawKey(""); setCopied(false);
    try {
      const r = await api.provisionGatewayKey();
      setRawKey(r.raw_key || "");
      setMsg(`gateway API key ready (${r.key_prefix}...) — stored in the OS keychain, the gateway provider is active`);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const copyKey = async () => {
    try {
      await navigator.clipboard.writeText(rawKey);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <Card className="space-y-4 p-6">
      <p className="text-sm text-zinc-600 dark:text-zinc-300">
        The matching <strong>fusion-gateway</strong> binary ships inside the console package and is
        started together with the console (by <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">startup.sh</code>{" "}
        or automatically on launch).
      </p>
      <div className="flex items-center justify-between rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
        <div className="text-sm">
          <p className="font-medium flex items-center gap-2">
            <Network size={16} /> {status?.base_url || "…"}
            {status?.reachable ? <Badge tone="ok">running</Badge> : <Badge tone="warn">not reachable</Badge>}
          </p>
          <p className="mt-1 text-xs text-zinc-500">
            {status?.reachable
              ? status.installed
                ? `binary: ${status.binary}`
                : "gateway detected on :11432 — no install needed"
              : status?.installed
                ? "binary present but not responding — start it or check its config"
                : "binary not installed yet"}
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
          <p className="text-sm font-medium">Create the console's API key</p>
          <p className="text-xs text-zinc-500">
            One click — the console logs into the gateway with its factory admin account
            (admin / fusion-admin-2026), issues an API key, and stores it in the OS keychain
            (<code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">gateway-key</code>).
            Chat, brain and workers all use this key against the gateway.
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={provisionKey} disabled={busy}>{busy ? "Creating..." : "Create API key"}</Button>
            <span className="text-xs text-zinc-400">changed the gateway admin password? save it below first</span>
          </div>
          <div className="flex flex-wrap gap-2 border-t border-zinc-100 pt-3 dark:border-zinc-800">
            <Input className="w-44" placeholder="admin username" value={adminUser}
              onChange={(e) => setAdminUser(e.target.value)} />
            <Input className="w-44" type="password" placeholder="admin password" value={adminPass}
              onChange={(e) => setAdminPass(e.target.value)} />
            <Button variant="outline" onClick={saveAdminCreds} disabled={!adminUser || !adminPass}>
              Save credentials
            </Button>
          </div>
        </div>
      )}

      {rawKey && (
        <div className="space-y-2 rounded-xl border border-amber-300 bg-amber-50 p-3 dark:border-amber-500/30 dark:bg-amber-500/5">
          <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
            Copy and save this key now — it is shown only once (the console keeps its copy in the OS keychain).
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-lg bg-white px-2 py-1.5 font-mono text-xs dark:bg-zinc-900">{rawKey}</code>
            <Button variant="outline" onClick={copyKey} disabled={copied}>
              {copied ? <><Check size={14} /> copied</> : "Copy"}
            </Button>
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
        Recommended path: everything (brain + workers) goes through the gateway with this key,
        so token usage is tracked in one place. Direct cloud providers can still be added on the Gateway page as an escape hatch.
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
  return (
    <Card className="space-y-4 p-6">
      <div className="flex items-center gap-2">
        <Bot size={18} className="text-indigo-500" />
        <h3 className="font-semibold">Workers</h3>
      </div>
      <WorkersPanel onSaved={onNext} />
      <div className="flex gap-2">
        <Button onClick={onNext}>Continue <ArrowRight size={14} /></Button>
        <Button variant="ghost" onClick={onSkip}>Skip</Button>
      </div>
    </Card>
  );
}

function VerifyStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  const [gw, setGw] = useState<Awaited<ReturnType<typeof api.gatewayStatus>> | null>(null);
  const [sample, setSample] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    api.gatewayStatus().then(setGw).catch(() => {});
  }, []);
  const test = async () => {
    setBusy(true); setMsg(""); setErr(""); setSample("");
    try {
      const pr = await api.providers();
      const p = (pr.providers ?? []).find((x) => x.enabled);
      if (!p) {
        setErr("no enabled provider — finish the Providers step first");
        return;
      }
      const r = await api.testCall(p.id);
      if (r.ok) {
        setSample(r.sample ?? "");
        setMsg(`provider ${p.name || p.id} answered in ${r.latency_ms ?? "?"} ms`);
      } else {
        setErr(r.error ?? "test call failed");
      }
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card className="space-y-4 p-6">
      <div className="flex items-center gap-2">
        <PlayCircle size={18} className="text-indigo-500" />
        <h3 className="font-semibold">Verify</h3>
      </div>
      <ul className="space-y-1 text-sm text-zinc-600 dark:text-zinc-300">
        <li className="flex items-center gap-2">
          {gw?.reachable ? <Check size={14} className="text-emerald-500" /> : <span className="text-red-500">!</span>}
          Gateway {gw ? `on ${gw.base_url}` : "status unknown"}
        </li>
        <li className="flex items-center gap-2">
          <span className="text-zinc-400">o</span> Brain provider configured (chat header select labeled "brain")
        </li>
        <li className="flex items-center gap-2">
          <span className="text-zinc-400">o</span> Workers with budget caps and provider bindings
        </li>
      </ul>
      <div className="flex gap-2">
        <Button onClick={test} disabled={busy}>{busy ? "Calling..." : "Test LLM call"}</Button>
        <Button variant="ghost" onClick={onNext}>Skip to done <ArrowRight size={14} /></Button>
      </div>
      {sample && <pre className="max-h-32 overflow-auto rounded-lg bg-zinc-50 p-2 text-xs dark:bg-zinc-900">{sample}</pre>}
      {msg && <p className="text-sm text-emerald-600 dark:text-emerald-400">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
      <p className="text-xs text-zinc-400">The test call runs against the brain provider saved on the Gateway page.</p>
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
        <li className="flex items-center gap-2"><Check size={14} className="text-emerald-500" /> Gateway status checked — startup.sh keeps both processes up</li>
        <li className="flex items-center gap-2"><Check size={14} className="text-emerald-500" /> Brain & workers stored — every value editable on the Gateway and Agents pages later</li>
      </ul>
      <p className="text-xs text-zinc-400">
        Tip: flip the `orchestrate` toggle in chat to let the brain dispatch the workers you just
        configured (confirm mode asks you per dispatch).
      </p>
      <Button onClick={onDone}>Open chat <ArrowRight size={14} /></Button>
    </Card>
  );
}
