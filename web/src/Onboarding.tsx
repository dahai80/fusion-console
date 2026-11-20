import { useEffect, useState, type ReactNode } from "react";
import {
  ArrowLeft,
  ArrowRight,
  Bot,
  Check,
  ChevronDown,
  Circle,
  Cloud,
  ExternalLink,
  KeyRound,
  Loader2,
  MessageSquare,
  Network,
  PlayCircle,
  RefreshCw,
  ShieldAlert,
  Sparkles,
  X,
} from "lucide-react";
import { api, type Orchestrator } from "./api";
import { GatewayBackendsPanel } from "./GatewayBackends";
import { WorkersPanel } from "./WorkersPanel";
import { Badge, Button, Card, Input, Select } from "./components/ui";
import {
  directProviders,
  keyReady,
  markVerified,
  progress,
  stepDone,
  useSetupStatus,
  type SetupStatus,
  type SetupStepId,
} from "./setupStatus";

// Guided setup. Opens automatically on first run (no enabled provider) and
// from the account menu. The order follows what a newcomer needs to reach a
// first answer: gateway + key -> a model -> one test call. Brain and workers
// come last and are optional. Every step reads live status, so a re-run
// shows what is already done and offers to jump to the first open step.

export type OnboardingExit = "chat" | "gateway" | "agents";

type StepId = "start" | SetupStepId | "done";

const STEPS: { id: StepId; label: string; optional?: boolean }[] = [
  { id: "start", label: "开始" },
  { id: "gateway", label: "启动网关" },
  { id: "models", label: "接入模型" },
  { id: "verify", label: "试一次" },
  { id: "team", label: "Brain 与 Worker", optional: true },
  { id: "done", label: "完成" },
];

const REMOTE_GUIDE = "https://github.com/dahai80/fusion-console/blob/main/deploy/TAILSCALE_CN.md";

function firstOpenStep(s: SetupStatus): StepId {
  for (const id of ["gateway", "models", "verify"] as const) {
    if (!stepDone(s, id)) return id;
  }
  return "team";
}

export default function Onboarding({ onExit }: { onExit: (to: OnboardingExit) => void }) {
  const { status, refresh } = useSetupStatus();
  const [step, setStep] = useState<StepId>("start");
  const idx = STEPS.findIndex((s) => s.id === step);
  const go = (to: StepId) => { setStep(to); void refresh(); };
  const next = () => go(STEPS[Math.min(idx + 1, STEPS.length - 1)].id);
  const back = () => go(STEPS[Math.max(idx - 1, 0)].id);
  const p = progress(status);

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col px-4 py-5 sm:px-6">
      <header className="mb-4">
        <div className="flex items-center gap-3">
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <Sparkles size={18} className="text-indigo-500" /> 新手引导
          </h1>
          {status.loaded && (
            <span className="text-xs text-zinc-500">已完成 {p.done}/{p.total}</span>
          )}
          <button onClick={() => onExit("chat")} aria-label="退出引导" title="退出引导（随时可从右上角头像重新打开）"
            className="ml-auto rounded-lg p-1.5 text-zinc-400 hover:bg-zinc-200/60 hover:text-zinc-600 dark:hover:bg-zinc-800/60">
            <X size={16} />
          </button>
        </div>
        <div className="mt-2 h-1 overflow-hidden rounded-full bg-zinc-200/70 dark:bg-zinc-800">
          <div className="h-full rounded-full bg-indigo-500 transition-[width] duration-500"
            style={{ width: `${(p.done / p.total) * 100}%` }} />
        </div>
        <ol className="mt-3 flex gap-1 overflow-x-auto pb-1 text-xs [scrollbar-width:none]">
          {STEPS.map((s, i) => {
            const done = s.id !== "start" && s.id !== "done" && stepDone(status, s.id);
            const active = s.id === step;
            return (
              <li key={s.id} className="shrink-0">
                <button
                  onClick={() => go(s.id)}
                  aria-current={active ? "step" : undefined}
                  className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 transition-colors ${
                    active
                      ? "bg-indigo-600 text-white"
                      : done
                        ? "bg-emerald-500/10 text-emerald-700 hover:bg-emerald-500/20 dark:text-emerald-400"
                        : "bg-zinc-100 text-zinc-500 hover:bg-zinc-200 dark:bg-zinc-800/60 dark:hover:bg-zinc-800"
                  }`}
                >
                  {done ? <Check size={12} /> : <span className="tabular-nums">{i}</span>}
                  {s.label}
                  {s.optional && <span className="opacity-70">· 可选</span>}
                </button>
              </li>
            );
          })}
        </ol>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto pb-4">
        {step === "start" && <StartStep status={status} onGo={go} />}
        {step === "gateway" && <GatewayStep status={status} refresh={refresh} />}
        {step === "models" && <ModelsStep status={status} refresh={refresh} />}
        {step === "verify" && <VerifyStep status={status} refresh={refresh} />}
        {step === "team" && <TeamStep status={status} refresh={refresh} />}
        {step === "done" && <DoneStep status={status} onExit={onExit} />}
      </div>

      {step !== "start" && step !== "done" && (
        <footer className="flex items-center gap-2 border-t border-zinc-200/80 pt-3 dark:border-zinc-800">
          <Button variant="ghost" onClick={back}><ArrowLeft size={14} /> 上一步</Button>
          <span className="ml-auto" />
          {!stepDone(status, step) && (
            <Button variant="ghost" onClick={next} className="text-zinc-500">
              {step === "team" ? "暂不配置" : "先跳过"}
            </Button>
          )}
          <Button onClick={next}>下一步 <ArrowRight size={14} /></Button>
        </footer>
      )}
    </div>
  );
}

// ---- shared bits ----------------------------------------------------------

function StepCard({ icon: Icon, title, lead, done, children }: {
  icon: typeof Cloud; title: string; lead: string; done?: boolean; children: ReactNode;
}) {
  return (
    <Card className="space-y-4 p-5">
      <div>
        <h2 className="flex items-center gap-2 font-semibold">
          <Icon size={18} className="text-indigo-500" /> {title}
          {done && <Badge tone="ok"><Check size={12} className="mr-0.5" /> 已完成</Badge>}
        </h2>
        <p className="mt-1 text-sm leading-6 text-zinc-600 dark:text-zinc-300">{lead}</p>
      </div>
      {children}
    </Card>
  );
}

function StatusRow({ ok, pending, children, action }: {
  ok: boolean; pending?: boolean; children: ReactNode; action?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-zinc-200/80 px-3 py-2.5 dark:border-zinc-800">
      <span className="shrink-0">
        {pending ? <Loader2 size={16} className="animate-spin text-zinc-400" />
          : ok ? <Check size={16} className="text-emerald-500" />
            : <Circle size={16} className="text-zinc-300 dark:text-zinc-600" />}
      </span>
      <div className="min-w-0 flex-1 text-sm">{children}</div>
      {action}
    </div>
  );
}

function Feedback({ msg, err }: { msg?: string; err?: string }) {
  return (
    <>
      {msg && <p className="text-sm text-emerald-600 dark:text-emerald-400">{msg}</p>}
      {err && <p className="break-words text-sm text-red-500">{err}</p>}
    </>
  );
}

function useAction() {
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const run = async (name: string, fn: () => Promise<string | void>) => {
    setBusy(name); setMsg(""); setErr("");
    try {
      const m = await fn();
      if (m) setMsg(m);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy("");
    }
  };
  return { busy, msg, err, run };
}

const code = "rounded bg-zinc-100 px-1 font-mono text-[0.85em] dark:bg-zinc-800";

// ---- steps ----------------------------------------------------------------

function StartStep({ status, onGo }: { status: SetupStatus; onGo: (s: StepId) => void }) {
  const items: { id: SetupStepId; title: string; desc: string }[] = [
    { id: "gateway", title: "启动网关并创建 API key", desc: "fusion-gateway 随 console 一起安装，所有模型调用都经过它，用量记在一本账上。" },
    { id: "models", title: "接入一个模型", desc: "填一家云厂商的 API key（DeepSeek、通义、Kimi、OpenRouter……），或使用网关里已有的模型。" },
    { id: "verify", title: "试一次调用", desc: "点一下，确认模型能回答。做到这一步就可以开始对话了。" },
    { id: "team", title: "Brain 与 Worker（可选）", desc: "让对话模型把任务派给本机的编程 Agent（claude、codex 等）去改代码。" },
  ];
  const first = firstOpenStep(status);
  const allDone = progress(status).done === progress(status).total;
  return (
    <Card className="space-y-4 p-5">
      <p className="text-sm leading-6 text-zinc-600 dark:text-zinc-300">
        约 5 分钟。每一步都会检查当前状态，已经配好的会直接打勾；中途离开也没关系，右上角头像菜单里随时可以回来继续。
      </p>
      <ul className="space-y-2">
        {items.map((it, i) => {
          const done = stepDone(status, it.id);
          return (
            <li key={it.id}>
              <button onClick={() => onGo(it.id)}
                className="flex w-full items-start gap-3 rounded-xl border border-zinc-200/80 p-3 text-left transition-colors hover:border-indigo-300 hover:bg-indigo-500/5 dark:border-zinc-800 dark:hover:border-indigo-500/40">
                <span className={`mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] ${
                  done ? "bg-emerald-500 text-white" : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800"}`}>
                  {!status.loaded ? <Loader2 size={12} className="animate-spin" /> : done ? <Check size={12} /> : i + 1}
                </span>
                <span className="min-w-0">
                  <span className="block text-sm font-medium">{it.title}</span>
                  <span className="block text-xs leading-5 text-zinc-500">{it.desc}</span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
      {status.factoryPassword && (
        <div className="flex items-start gap-2 rounded-xl border border-amber-300/70 bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/5 dark:text-amber-300">
          <ShieldAlert size={14} className="mt-0.5 shrink-0" />
          <span>
            admin 还在用出厂密码 <code className={code}>fusion-admin-2026</code>。只在本机用没关系；要从手机或别的电脑访问前，请先按
            <a className="mx-1 underline underline-offset-2" href={REMOTE_GUIDE} target="_blank" rel="noreferrer">远程访问指南</a>
            改掉。
          </span>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => onGo(allDone ? "done" : first)} disabled={!status.loaded}>
          {allDone ? "都配好了，看看下一步" : first === "gateway" ? "开始" : `从「${STEPS.find((s) => s.id === first)?.label}」继续`}
          <ArrowRight size={14} />
        </Button>
      </div>
    </Card>
  );
}

function GatewayStep({ status, refresh }: { status: SetupStatus; refresh: () => Promise<void> }) {
  const a = useAction();
  const [rawKey, setRawKey] = useState("");
  const [copied, setCopied] = useState(false);
  const [adminUser, setAdminUser] = useState("");
  const [adminPass, setAdminPass] = useState("");
  const gw = status.gateway;
  const hasKey = keyReady(status);
  const direct = directProviders(status);

  const start = () => a.run("start", async () => {
    const r = gw?.installed ? await api.gatewayStart() : await api.gatewayInstall();
    await refresh();
    return "note" in r && r.note ? r.note : "网关已启动";
  });
  const provision = () => a.run("key", async () => {
    setRawKey(""); setCopied(false);
    const r = await api.provisionGatewayKey();
    setRawKey(r.raw_key || "");
    await refresh();
    return `API key 已创建（${r.key_prefix}…），已存进系统钥匙串`;
  });
  const saveAdmin = () => a.run("admin", async () => {
    await api.storeApiKey("gateway-admin-user", adminUser);
    await api.storeApiKey("gateway-admin-pass", adminPass);
    setAdminPass("");
    return "网关管理员账号已保存（系统钥匙串），现在可以再点一次创建";
  });
  const copy = async () => {
    try { await navigator.clipboard.writeText(rawKey); setCopied(true); } catch { setCopied(false); }
  };

  return (
    <StepCard icon={Network} title="启动网关" done={stepDone(status, "gateway")}
      lead="console 不直接拿各家模型的 key：所有请求先到本机的 fusion-gateway，再由它转发。这一步把网关跑起来，并给 console 发一把调用网关用的 API key。">
      <StatusRow ok={!!gw?.reachable} pending={!status.loaded}
        action={status.loaded && !gw?.reachable && (
          <Button variant="outline" onClick={start} disabled={!!a.busy}>
            {a.busy === "start" ? <Loader2 size={14} className="animate-spin" /> : null}
            {gw?.installed ? "启动网关" : "安装并启动"}
          </Button>
        )}>
        <p className="font-medium">网关 {gw?.base_url ? <span className="font-mono text-xs text-zinc-500">{gw.base_url}</span> : null}</p>
        <p className="text-xs text-zinc-500">
          {!status.loaded ? "检查中…" : gw?.reachable ? "运行中" : gw?.installed ? "已安装但没有响应" : "还没安装，安装包里自带，一键即可"}
        </p>
      </StatusRow>

      <StatusRow ok={hasKey} pending={!status.loaded}
        action={gw?.reachable && (
          <Button variant={hasKey ? "outline" : "default"} onClick={provision} disabled={!!a.busy}>
            {a.busy === "key" ? <Loader2 size={14} className="animate-spin" /> : <KeyRound size={14} />}
            {hasKey ? "重新创建" : "一键创建"}
          </Button>
        )}>
        <p className="font-medium">console 的 API key</p>
        <p className="text-xs text-zinc-500">
          {hasKey ? "已就绪，对话、Brain 和 Worker 都用它调用网关" : gw?.reachable ? "用网关出厂管理员账号自动创建，存进系统钥匙串，不用手抄" : "网关启动后才能创建"}
        </p>
      </StatusRow>

      {rawKey && (
        <div className="space-y-2 rounded-xl border border-amber-300 bg-amber-50 p-3 dark:border-amber-500/30 dark:bg-amber-500/5">
          <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
            这把 key 只显示这一次。console 自己已经存好了；如果别的工具（比如 Claude Code、Codex）也想走这个网关，现在复制保存。
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-lg bg-white px-2 py-1.5 font-mono text-xs dark:bg-zinc-900">{rawKey}</code>
            <Button variant="outline" onClick={copy} disabled={copied}>{copied ? <><Check size={14} /> 已复制</> : "复制"}</Button>
          </div>
        </div>
      )}

      {direct.length > 0 && !hasKey && (
        <p className="text-xs text-zinc-500">
          已有直连模型来源（{direct.map((p) => p.name || p.id).join("、")}），不走网关也能用，这一步可以跳过。
        </p>
      )}

      <details className="group rounded-xl border border-zinc-200/80 px-3 py-2 text-sm dark:border-zinc-800">
        <summary className="flex cursor-pointer list-none items-center gap-1 text-xs text-zinc-500">
          <ChevronDown size={14} className="transition-transform group-open:rotate-180" />
          创建失败、提示管理员登录不上？
        </summary>
        <div className="mt-3 space-y-2 text-xs text-zinc-500">
          <p>改过网关管理员密码（出厂是 <code className={code}>admin / fusion-admin-2026</code>）时，先把新的账号密码存进来再点创建。</p>
          <div className="flex flex-wrap gap-2">
            <Input className="w-40" placeholder="管理员用户名" value={adminUser} onChange={(e) => setAdminUser(e.target.value)} />
            <Input className="w-40" type="password" placeholder="管理员密码" value={adminPass} onChange={(e) => setAdminPass(e.target.value)} />
            <Button variant="outline" onClick={saveAdmin} disabled={!adminUser || !adminPass || !!a.busy}>保存</Button>
          </div>
        </div>
      </details>
      <Feedback msg={a.msg} err={a.err} />
    </StepCard>
  );
}

function ModelsStep({ status, refresh }: { status: SetupStatus; refresh: () => Promise<void> }) {
  const models = status.gatewayModels;
  const direct = directProviders(status);
  return (
    <StepCard icon={Cloud} title="接入模型" done={stepDone(status, "models")}
      lead="告诉网关去哪里调模型：选厂商、粘贴 API key、填模型名。可以加多家，key 只存在网关配置里，页面上不会再显示。">
      <StatusRow ok={models.length > 0 || direct.length > 0} pending={!status.loaded}
        action={<Button variant="ghost" onClick={() => void refresh()} title="重新读取"><RefreshCw size={14} /></Button>}>
        {models.length > 0 ? (
          <>
            <p className="font-medium">网关里现在有 {models.length} 个模型</p>
            <p className="mt-1 flex flex-wrap gap-1">
              {models.slice(0, 12).map((m) => <Badge key={m}>{m}</Badge>)}
              {models.length > 12 && <span className="text-xs text-zinc-400">等 {models.length} 个</span>}
            </p>
          </>
        ) : direct.length > 0 ? (
          <p className="font-medium">已有直连模型来源：{direct.map((p) => p.name || p.id).join("、")}</p>
        ) : (
          <>
            <p className="font-medium">还没有可用的模型</p>
            <p className="text-xs text-zinc-500">{keyReady(status) ? "在下面添加一家" : "先完成上一步的 API key，网关模型列表才读得到"}</p>
          </>
        )}
      </StatusRow>
      <div className="rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
        <GatewayBackendsPanel compact onAdded={() => void refresh()} />
      </div>
      <p className="text-xs text-zinc-400">
        要直连某个 OpenAI / Anthropic 兼容地址（不经网关），到 Gateway 页的 Models 标签点 Add model source。
      </p>
    </StepCard>
  );
}

function VerifyStep({ status, refresh }: { status: SetupStatus; refresh: () => Promise<void> }) {
  const providers = status.providers;
  const [pid, setPid] = useState("");
  const [sample, setSample] = useState("");
  const a = useAction();
  useEffect(() => {
    if (!pid && providers.length) setPid(providers[0].id);
  }, [providers, pid]);
  const test = () => a.run("test", async () => {
    setSample("");
    const r = await api.testCall(pid);
    if (!r.ok) throw new Error(r.error || "调用失败");
    setSample(r.sample ?? "");
    markVerified(pid);
    await refresh();
    return `${providers.find((x) => x.id === pid)?.name || pid} 回答了，用时 ${r.latency_ms ?? "?"} ms`;
  });
  return (
    <StepCard icon={PlayCircle} title="试一次" done={stepDone(status, "verify")}
      lead="发一句很短的话给模型，看它能不能回答。通过就说明网关、key、模型这条链是通的。">
      {providers.length === 0 ? (
        <StatusRow ok={false} pending={!status.loaded}>
          <p className="font-medium">没有可用的模型来源</p>
          <p className="text-xs text-zinc-500">回到「启动网关」创建 API key，console 会自动添加网关这个来源。</p>
        </StatusRow>
      ) : (
        <div className="flex flex-wrap items-end gap-2">
          <label className="block min-w-56 flex-1 text-xs text-zinc-500">用哪个来源
            <Select value={pid} onChange={(e) => setPid(e.target.value)} className="mt-1">
              {providers.map((p) => (
                <option key={p.id} value={p.id}>{p.name || p.id}{p.model ? ` · ${p.model}` : ""}{p.type === "gateway" ? "（网关）" : ""}</option>
              ))}
            </Select>
          </label>
          <Button onClick={test} disabled={!!a.busy || !pid}>
            {a.busy ? <Loader2 size={14} className="animate-spin" /> : <PlayCircle size={14} />}
            {a.busy ? "调用中…" : "发一条测试"}
          </Button>
        </div>
      )}
      {sample && <pre className="max-h-32 overflow-auto whitespace-pre-wrap rounded-lg bg-zinc-50 p-2 text-xs dark:bg-zinc-950">{sample}</pre>}
      <Feedback msg={a.msg} err={a.err} />
      {a.err && (
        <ul className="list-disc space-y-1 pl-5 text-xs text-zinc-500">
          <li><code className={code}>401</code>：网关 key 不对，回「启动网关」重新创建。</li>
          <li><code className={code}>model not allowed</code> / 找不到模型：模型名要和厂商文档完全一致，在「接入模型」里删掉重加。</li>
          <li>超时或连不上：检查厂商的 Base URL，以及本机能不能访问它（公司网络、代理）。</li>
        </ul>
      )}
    </StepCard>
  );
}

const MODES: { id: Orchestrator["mode"]; title: string; desc: string }[] = [
  { id: "confirm", title: "每次派工先问我", desc: "推荐。Brain 想让 Worker 动手时弹出确认。" },
  { id: "auto", title: "预算内自动派工", desc: "不再询问，受每个 Worker 的预算上限约束。" },
  { id: "off", title: "不派工", desc: "只聊天；Worker 只能手动 @ 点名使用。" },
];

function TeamStep({ status, refresh }: { status: SetupStatus; refresh: () => Promise<void> }) {
  const a = useAction();
  const [open, setOpen] = useState(false);
  const orch = status.orch;
  const detected = status.agents.filter((x) => x.detected);
  const missing = status.agents.filter((x) => !x.detected);
  const workers = orch?.workers ?? [];

  // Save one field on top of the server's current settings so the Workers
  // panel's edits are never overwritten by a stale copy.
  const save = (patch: Partial<Orchestrator>, note: string) => a.run("save", async () => {
    const cur = (await api.orchestration()).orchestrator;
    await api.saveOrchestration({ ...cur, ...patch });
    await refresh();
    return note;
  });

  return (
    <StepCard icon={Bot} title="Brain 与 Worker" done={stepDone(status, "team")}
      lead="可选。Brain 是对话里的那个模型，负责拆任务、审结果；Worker 是本机装好的编程 Agent，真正去仓库里改代码。只想聊天可以直接跳过。">
      <div>
        <p className="mb-1.5 text-xs font-medium text-zinc-500">本机检测到的 Agent</p>
        <div className="flex flex-wrap gap-1.5">
          {detected.map((x) => <Badge key={x.name} tone="ok"><Check size={12} className="mr-0.5" />{x.name}</Badge>)}
          {missing.slice(0, 6).map((x) => <Badge key={x.name}>{x.name} 未安装</Badge>)}
          {!status.loaded && <Loader2 size={14} className="animate-spin text-zinc-400" />}
        </div>
        {status.loaded && detected.length === 0 && (
          <p className="mt-2 text-xs text-zinc-500">一个都没检测到：先装一个命令行 Agent（如 <code className={code}>claude</code> 或 <code className={code}>codex</code>），装好后刷新本页。</p>
        )}
      </div>

      <div>
        <p className="mb-1.5 text-xs font-medium text-zinc-500">派工审批</p>
        <div className="grid gap-2 sm:grid-cols-3">
          {MODES.map((m) => {
            const on = (orch?.mode ?? "confirm") === m.id;
            return (
              <button key={m.id} disabled={!orch || !!a.busy}
                onClick={() => !on && save({ mode: m.id }, `已切换为「${m.title}」`)}
                className={`rounded-xl border p-3 text-left transition-colors disabled:opacity-60 ${
                  on ? "border-indigo-500 bg-indigo-500/5" : "border-zinc-200/80 hover:border-indigo-300 dark:border-zinc-800"}`}>
                <span className={`block text-sm font-medium ${on ? "text-indigo-600 dark:text-indigo-400" : ""}`}>{m.title}</span>
                <span className="mt-0.5 block text-xs leading-5 text-zinc-500">{m.desc}</span>
              </button>
            );
          })}
        </div>
      </div>

      {detected.length > 1 && (
        <label className="block text-xs text-zinc-500">Brain 用哪个 Agent（agent 模式下生效）
          <Select className="mt-1 sm:w-72" value={orch?.brain_agent || detected[0].name} disabled={!orch || !!a.busy}
            onChange={(e) => save({ brain_agent: e.target.value }, `Brain Agent 已设为 ${e.target.value}`)}>
            {detected.map((x) => <option key={x.name} value={x.name}>{x.name}（{x.command}）</option>)}
          </Select>
        </label>
      )}

      <StatusRow ok={workers.length > 0} pending={!status.loaded}
        action={<Button variant={workers.length ? "outline" : "default"} onClick={() => setOpen(!open)}>
          {open ? "收起" : workers.length ? "管理 Worker" : "添加 Worker"}
        </Button>}>
        <p className="font-medium">{workers.length ? `已有 ${workers.length} 个 Worker` : "还没有 Worker"}</p>
        <p className="text-xs text-zinc-500">
          {workers.length ? workers.map((w) => w.name || w.agent).join("、") : "每个 Worker = 一个 Agent + 一个工作目录（或 GitHub 仓库）+ 预算上限"}
        </p>
      </StatusRow>
      {open && (
        <div className="rounded-xl border border-zinc-200/80 p-3 dark:border-zinc-800">
          <WorkersPanel onSaved={() => void refresh()} />
        </div>
      )}
      <Feedback msg={a.msg} err={a.err} />
    </StepCard>
  );
}

function DoneStep({ status, onExit }: { status: SetupStatus; onExit: (to: OnboardingExit) => void }) {
  const rows: { id: SetupStepId; label: string }[] = [
    { id: "gateway", label: "网关与 API key" },
    { id: "models", label: "模型" },
    { id: "verify", label: "测试调用" },
    { id: "team", label: "Brain 与 Worker（可选）" },
  ];
  const ready = progress(status).done === progress(status).total;
  return (
    <Card className="space-y-4 p-5">
      <h2 className="flex items-center gap-2 font-semibold">
        {ready ? <Check size={18} className="text-emerald-500" /> : <Circle size={18} className="text-amber-500" />}
        {ready ? "可以开始用了" : "还差几步"}
      </h2>
      <ul className="space-y-1.5 text-sm">
        {rows.map((r) => {
          const done = stepDone(status, r.id);
          return (
            <li key={r.id} className="flex items-center gap-2">
              {done ? <Check size={14} className="text-emerald-500" /> : <Circle size={14} className="text-zinc-300 dark:text-zinc-600" />}
              <span className={done ? "" : "text-zinc-500"}>{r.label}</span>
            </li>
          );
        })}
      </ul>
      <div className="space-y-1.5 rounded-xl bg-zinc-50 p-3 text-xs leading-5 text-zinc-600 dark:bg-zinc-950 dark:text-zinc-400">
        <p>· 对话页输入框旁的 <strong>orchestrate</strong> 开关（默认开）决定 Brain 能不能给 Worker 派工；也可以用 <code className={code}>@Worker名</code> 直接点名。</p>
        <p>· 所有设置以后都能改：网关和模型在 Gateway 页，Brain 和 Worker 在 Agents 页，用量在 Dashboard。</p>
        <p>· 想在手机上用：看
          <a className="mx-1 inline-flex items-center gap-0.5 text-indigo-600 underline-offset-2 hover:underline dark:text-indigo-400" href={REMOTE_GUIDE} target="_blank" rel="noreferrer">
            Tailscale 远程访问指南 <ExternalLink size={11} />
          </a>。
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => onExit("chat")}><MessageSquare size={14} /> 开始对话</Button>
        <Button variant="outline" onClick={() => onExit("gateway")}>Gateway 页</Button>
        <Button variant="outline" onClick={() => onExit("agents")}>Agents 页</Button>
      </div>
    </Card>
  );
}
