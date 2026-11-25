import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  Bot,
  CheckCircle,
  Clock,
  Copy,
  Menu,
  Cpu,
  Brain,
  ChevronDown,
  ChevronRight,
  LayoutDashboard,
  LogOut,
  MessageSquare,
  Moon,
  Monitor,
  CirclePause,

  Send,
  Sparkles,
  Sun,
  Terminal,
  Plus,
  X,
  RotateCcw,
  KeyRound,
  Loader2,
  Mic,
  Network,
  Rocket,
  Paperclip,
  Pencil,
  Trash2,
  Download,
  ExternalLink,
  Database,
  FileText,
  Gauge,
  Users,
  Megaphone,
} from "lucide-react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, streamPOST, ApiError, type AgentInfo, type ChatDefault, type ChatTurn, type Conversation, type DispatchRun, type Message, type GatewayKey, type GatewayModelItem, type Provider, type WorkerSettings, type WorkerStatus } from "./api";
import { applyAgentEvent, applyChatEvent, approvalAfter, createPacer, emptyTurn, followDelay, FOLLOW_MAX_FAILURES, isNetworkError, newTurnId, replayTurn, type TurnKind, type TurnView } from "./chatTurn";
import { mergeEntries, supersedeWith } from "./chatTimeline";
import Onboarding from "./Onboarding";
import { progress as setupProgress, useSetupStatus } from "./setupStatus";
import ConsoleUpdate from "./ConsoleUpdate";
import { GatewayBackendsPanel } from "./GatewayBackends";
import AgentsPage from "./agents/AgentsPage";
import FeedbackPage, { type FeedbackEntry } from "./FeedbackPage";
import { notifyFeedbackError, notifyFeedbackSuggestion } from "./feedbackBus";
import type { FeedbackDraft } from "./api";
import Dashboard from "./Dashboard";
import { RagPage } from "./RagPage";
import { ModelsPage } from "./ModelsPage";
import TeamPage from "./TeamPage";
import { Badge, Button, Card, Input, Select, Textarea, THEME_LABELS, ThemeProvider, nextThemeMode, useTheme } from "./components/ui";
import RunDrawer from "./components/RunDrawer";
import { beginBrainTurn, brainEvent, endBrainTurn, type BrainActivity } from "./components/brainActivity";
import { BrainCard, DispatchCard, DispatchDetails } from "./components/DispatchCard";
import { DispatchRail, DispatchReopen, useDispatchRail } from "./components/DispatchStatus";

const chatMarkdownComponents: Components = {
  a({ children, href, title }) {
    // Workers often cite files on the console host. A browser would resolve
    // these absolute paths against the console origin, even on a phone.
    if (href && /^\/(?:Users|home|Volumes|private|tmp)\//.test(href)) {
      return <span>{children === href ? null : <>{children} </>}<code className="break-all">{href}</code></span>;
    }
    return <a href={href} title={title}>{children}</a>;
  },
};

type Page = "dashboard" | "chat" | "agents" | "gateway" | "rag" | "team" | "feedback" | "onboard";
type Navigation = { page: Page; feedbackEntry?: FeedbackEntry & { id: number } };

export default function App() {
  return (
    <ThemeProvider>
      <Shell />
    </ThemeProvider>
  );
}

function Shell() {
  // undefined = session check in flight, null = logged out, string = logged in
  const [user, setUser] = useState<string | null | undefined>(undefined);
  const [{ page, feedbackEntry }, setNavigation] = useState<Navigation>(() => ({
    page: new URLSearchParams(window.location.search).has("dashboard") ? "dashboard" : "chat",
  }));
  const setPage = useCallback((page: Page) => {
    setNavigation((current) => current.page === page ? current : { page });
  }, []);
  const openFeedback = (entry: FeedbackEntry) => {
    setNavigation((current) => ({
      page: "feedback",
      feedbackEntry: { ...entry, id: (current.feedbackEntry?.id ?? 0) + 1 },
    }));
  };
  const clearSession = useCallback(() => {
    setUser(null);
    setNavigation(({ page }) => ({ page }));
  }, []);
  const shellRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const shell = shellRef.current;
    const viewport = window.visualViewport;
    if (!shell || page !== "chat" || !viewport || !matchMedia("(pointer: coarse)").matches) return;

    // iOS shrinks and can pan the visual viewport while its keyboard is open.
    const fitChat = () => {
      if (Math.abs(viewport.scale - 1) > 0.01) return; // Preserve manual pinch zoom.
      shell.style.height = `${viewport.height}px`;
      shell.style.top = `${viewport.offsetTop}px`;
    };
    fitChat();
    viewport.addEventListener("resize", fitChat);
    viewport.addEventListener("scroll", fitChat);
    return () => {
      viewport.removeEventListener("resize", fitChat);
      viewport.removeEventListener("scroll", fitChat);
      shell.style.removeProperty("height");
      shell.style.removeProperty("top");
    };
  }, [page, user]);

  useEffect(() => {
    // One-time auto-login token (console launched with --open puts it in the
    // URL); redeem it, then strip it from the address bar.
    const params = new URLSearchParams(window.location.search);
    const token = params.get("fc_token");
    const stripToken = () => {
      params.delete("fc_token");
      const qs = params.toString();
      window.history.replaceState({}, "", window.location.pathname + (qs ? `?${qs}` : ""));
    };
    const boot = token
      ? api
          .autoLogin(token)
          .then((r) => r.user)
          .catch(() => null)
          .finally(stripToken)
      : api.me().then((r) => r.user).catch(() => null);
    boot.then((u) => setUser(u));
    window.addEventListener("fc:logout", clearSession);
    return () => window.removeEventListener("fc:logout", clearSession);
  }, [clearSession]);

  // Guided setup: explicit ?onboard=1 (first run opens it server-side) or a
  // logged-in session with no enabled provider at all. Keyed on user so a
  // manual login (no auto-login token) also triggers the wizard.
  useEffect(() => {
    if (!user) return;
    if (new URLSearchParams(window.location.search).get("onboard") === "1") {
      setPage("onboard");
      return;
    }
    api.providers().then((r) => {
      if (!(r.providers ?? []).some((p) => p.enabled)) {
        setPage("onboard");
      }
    }).catch(() => {});
  }, [user, setPage]);

  if (user === undefined) return <Splash />;
  if (!user) return <Login onDone={setUser} />;

  return (
    <div ref={shellRef} className={`flex h-dvh flex-col bg-zinc-50 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 ${page === "chat" ? "pointer-coarse:fixed pointer-coarse:inset-x-0 pointer-coarse:top-0" : ""}`}>
      <TopBar user={user} page={page} setPage={setPage} onLogout={async () => { await api.logout(); clearSession(); }} onWizard={() => setPage("onboard")} />
      <main className="flex-1 min-h-0">
        {page === "onboard" && (
          <Onboarding onExit={(to) => setPage(to)} />
        )}
        {page === "dashboard" && <Dashboard />}
        {page === "chat" && <ChatPage onPage={setPage} />}
        {page === "agents" && <AgentsPage />}
        {page === "gateway" && <GatewayPage />}
        {page === "rag" && <RagPage />}
        {page === "team" && <TeamPage />}
        {page === "feedback" && <FeedbackPage key={feedbackEntry?.id} entry={feedbackEntry} />}
        <FeedbackToastHost openFeedback={openFeedback} />
      </main>
    </div>
  );
}

function Splash() {
  return <div className="flex h-screen items-center justify-center text-zinc-400">loading…</div>;
}

// Stream errors offer to generate a draft. Brain suggestions already have one.
function FeedbackToastHost({ openFeedback }: { openFeedback: (entry: FeedbackEntry) => void }) {
  const [errText, setErrText] = useState("");
  const [suggest, setSuggest] = useState<FeedbackDraft | null>(null);

  useEffect(() => {
    const onError = (e: Event) => setErrText(((e as CustomEvent).detail as { text: string }).text);
    const onSuggest = (e: Event) => setSuggest(((e as CustomEvent).detail as { draft: FeedbackDraft }).draft);
    window.addEventListener("fc:feedback-error", onError);
    window.addEventListener("fc:feedback-suggest", onSuggest);
    return () => {
      window.removeEventListener("fc:feedback-error", onError);
      window.removeEventListener("fc:feedback-suggest", onSuggest);
    };
  }, []);

  const openPage = (entry: FeedbackEntry) => {
    setErrText("");
    setSuggest(null);
    openFeedback(entry);
  };

  if (!errText && !suggest) return null;
  const card = errText
    ? {
        title: "任务出错了",
        body: errText,
        action: "总结此问题",
        onAction: () => openPage({ kind: "error", text: errText }),
        dismiss: () => setErrText(""),
      }
    : {
        title: "大脑起草了一个 issue",
        body: suggest!.title,
        action: "查看并提交",
        onAction: () => openPage({ kind: "suggestion", draft: suggest! }),
        dismiss: () => setSuggest(null),
      };
  return (
    <div className="pointer-coarse:bottom-2 fixed bottom-4 right-4 z-40 max-w-sm rounded-2xl border border-zinc-200/80 bg-white p-4 shadow-lg dark:border-zinc-800 dark:bg-zinc-900">
      <div className="flex items-start gap-2">
        <Megaphone size={16} className="mt-0.5 shrink-0 text-indigo-500" />
        <div className="min-w-0">
          <p className="text-sm font-medium">{card.title}</p>
          <p className="mt-1 line-clamp-3 break-all text-xs text-zinc-500">{card.body}</p>
          <div className="mt-3 flex gap-2">
            <button type="button" onClick={card.onAction}
              className="rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-500">
              {card.action}
            </button>
            <button type="button" onClick={card.dismiss}
              className="rounded-lg border border-zinc-200/80 px-3 py-1.5 text-xs text-zinc-500 hover:border-zinc-400 dark:border-zinc-800">
              忽略
            </button>
          </div>
        </div>
        <button type="button" aria-label="close" onClick={card.dismiss} className="ml-1 text-zinc-400 hover:text-zinc-600">
          <X size={14} />
        </button>
      </div>
    </div>
  );
}

function TopBar({
  user, page, setPage, onLogout, onWizard,
}: {
  user: string; page: Page; setPage: (p: Page) => void; onLogout: () => void; onWizard: () => void;
}) {
  const { mode, setMode } = useTheme();
  const nextMode = nextThemeMode(mode);
  const themeHint = `外观：${THEME_LABELS[mode]}，点击切换为${THEME_LABELS[nextMode]}`;
  // Phones have no hover tooltip: name the new mode briefly after each tap.
  const [themeToast, setThemeToast] = useState(false);
  useEffect(() => {
    if (!themeToast) return;
    const t = setTimeout(() => setThemeToast(false), 1200);
    return () => clearTimeout(t);
  }, [themeToast, mode]);
  const tab = (id: Page, label: string, Icon: typeof Bot) => (
    <button
      aria-label={label}
      onClick={() => setPage(id)}
      className={`inline-flex shrink-0 items-center gap-2 rounded-xl px-3 py-1.5 text-sm font-medium transition-colors ${
        page === id
          ? "bg-indigo-600 text-white shadow-sm"
          : "text-zinc-500 hover:bg-zinc-200/60 dark:text-zinc-400 dark:hover:bg-zinc-800/60"
      }`}
    >
      <Icon size={16} />
      {/* H5: icons only below sm — seven labeled tabs overflow a phone width */}
      <span className="hidden sm:inline">{label}</span>
    </button>
  );
  return (
    <header className="relative z-20 flex items-center justify-between border-b border-zinc-200/80 dark:border-zinc-800 bg-white/60 dark:bg-zinc-900/60 px-4 py-2 backdrop-blur">
      <div className="flex min-w-0 items-center gap-4">
        <span className="hidden shrink-0 font-semibold tracking-tight sm:inline">fusion-console</span>
        <nav className="flex gap-1 overflow-x-auto">
          {tab("chat", "Chat", MessageSquare)}
          {tab("agents", "Agents", Bot)}
          {tab("dashboard", "Dashboard", LayoutDashboard)}
          {tab("gateway", "Gateway", Network)}
          {tab("rag", "Knowledge Base", Database)}
          {tab("team", "Team", Users)}
          {tab("feedback", "Feedback", Megaphone)}
        </nav>
      </div>
      <div className="flex items-center gap-2 text-sm text-zinc-500">
        <ConsoleUpdate />
        <button type="button" onClick={() => { setMode(nextMode); setThemeToast(true); }} title={themeHint} aria-label={themeHint}
          className="relative inline-flex shrink-0 items-center rounded-xl p-2 hover:bg-zinc-200/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 dark:hover:bg-zinc-800/60">
          {mode === "system" ? <Monitor size={16} /> : mode === "dark" ? <Moon size={16} /> : <Sun size={16} />}
          {themeToast && (
            <span role="status" className="pointer-events-none absolute right-0 top-full mt-1.5 whitespace-nowrap rounded-lg bg-zinc-900 px-2 py-1 text-xs text-white shadow-lg dark:bg-zinc-100 dark:text-zinc-900">
              {THEME_LABELS[mode]}
            </span>
          )}
        </button>
        <UserMenu user={user} onLogout={onLogout} onWizard={onWizard} />
      </div>
    </header>
  );
}

// UserMenu is the top-right account dropdown: avatar trigger, live setup
// progress with a one-click way back into the onboarding wizard, the
// one-click gateway API key (for users who skipped the wizard), and logout.
function UserMenu({ user, onLogout, onWizard }: { user: string; onLogout: () => void; onWizard: () => void }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [rawKey, setRawKey] = useState("");
  const [copied, setCopied] = useState(false);
  const [err, setErr] = useState("");
  const { status, refresh } = useSetupStatus();
  const rootRef = useRef<HTMLDivElement>(null);
  const p = setupProgress(status);
  const setupDone = status.loaded && p.done === p.total;
  const initial = (user || "?").slice(0, 1).toUpperCase();

  useEffect(() => {
    if (!open) return;
    void refresh();
    const close = (e: MouseEvent | KeyboardEvent) => {
      if (e instanceof KeyboardEvent ? e.key === "Escape" : !rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", close);
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", close);
    };
  }, [open, refresh]);

  const provision = async () => {
    setBusy(true); setErr(""); setRawKey(""); setCopied(false);
    try {
      const r = await api.provisionGatewayKey();
      setRawKey(r.raw_key || "");
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(rawKey);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div ref={rootRef} className="relative">
      <button
        onClick={() => { setOpen(!open); if (!open) { setRawKey(""); setErr(""); } }}
        aria-label={`账户菜单 · ${user}`}
        aria-expanded={open}
        className="flex items-center gap-1.5 rounded-xl py-1 pl-1 pr-1.5 hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60"
      >
        <span className="relative flex size-7 items-center justify-center rounded-full bg-indigo-600 text-xs font-semibold text-white">
          {initial}
          {status.loaded && !setupDone && (
            <span className="absolute -right-0.5 -top-0.5 size-2.5 rounded-full border-2 border-white bg-amber-500 dark:border-zinc-900" />
          )}
        </span>
        <span className="hidden sm:inline">{user}</span>
        <ChevronDown size={14} />
      </button>
      {open && (
        <div className="absolute right-0 z-50 mt-1 w-[min(20rem,calc(100vw-2rem))] rounded-xl border border-zinc-200/80 bg-white p-3 shadow-md shadow-zinc-900/5 dark:border-zinc-800 dark:bg-zinc-900">
          <button
            onClick={() => { setOpen(false); onWizard(); }}
            className="w-full rounded-xl border border-indigo-200 bg-indigo-500/5 p-3 text-left transition-colors hover:bg-indigo-500/10 dark:border-indigo-500/30"
          >
            <span className="flex items-center gap-2 text-sm font-medium text-indigo-700 dark:text-indigo-300">
              <Sparkles size={14} /> {setupDone ? "新手引导" : "继续新手引导"}
              <span className="ml-auto text-xs font-normal text-zinc-500">{status.loaded ? `${p.done}/${p.total}` : "…"}</span>
            </span>
            <span className="mt-2 block h-1 overflow-hidden rounded-full bg-indigo-500/15">
              <span className="block h-full rounded-full bg-indigo-500 transition-[width] duration-500"
                style={{ width: `${(p.done / p.total) * 100}%` }} />
            </span>
            <span className="mt-1.5 block text-xs text-zinc-500">
              {!status.loaded ? "检查中…" : setupDone ? "网关、模型、测试调用都已就绪，可以重新检查或配置 Worker" : "网关 → 模型 → 试一次，几分钟就能开始对话"}
            </span>
          </button>
          <div className="mt-3 space-y-2 border-t border-zinc-100 pt-3 dark:border-zinc-800">
            {rawKey ? (
              <div className="space-y-2">
                <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
                  这把 key 只显示这一次（console 已在系统钥匙串里存了一份）。
                </p>
                <div className="flex items-center gap-2">
                  <code className="min-w-0 flex-1 truncate rounded-lg bg-zinc-100 px-2 py-1.5 font-mono text-xs dark:bg-zinc-800">{rawKey}</code>
                  <Button variant="outline" onClick={copy} disabled={copied}>
                    {copied ? "已复制" : "复制"}
                  </Button>
                </div>
              </div>
            ) : (
              <button onClick={provision} disabled={busy}
                title="给网关发一把新的 API key（存进系统钥匙串），给 Claude Code、Codex 等外部工具用"
                className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-zinc-600 hover:bg-zinc-100 disabled:opacity-50 dark:text-zinc-300 dark:hover:bg-zinc-800">
                {busy ? <Loader2 size={14} className="animate-spin" /> : <KeyRound size={14} />}
                {busy ? "创建中…" : "创建网关 API key"}
              </button>
            )}
            {err && <p className="text-xs text-red-500">{err}</p>}
            <button
              onClick={onLogout}
              className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800"
            >
              <LogOut size={14} /> 退出登录
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function Login({ onDone }: { onDone: (u: string) => void }) {
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [hint, setHint] = useState("");

  // Local convenience: when the admin still runs the factory password and the
  // request is local, prefill both fields (server refuses to answer for
  // non-loopback sessions).
  useEffect(() => {
    api.authHint().then((h) => {
      setUser(h.user || "admin");
      if (h.factory_default) {
        setPassword("fusion-admin-2026");
        setHint("factory password prefilled");
      }
    }).catch(() => setUser("admin"));
  }, []);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const r = await api.login(user, password);
      onDone(r.user);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="flex h-screen items-center justify-center bg-zinc-50 dark:bg-zinc-950 p-4">
      <Card className="w-full max-w-sm p-6">
        <div className="mb-4 flex items-center gap-2">
          <Terminal size={20} className="text-indigo-500" />
          <h1 className="text-lg font-semibold">fusion-console</h1>
        </div>
        <form onSubmit={submit} className="space-y-3">
          <Input value={user} onChange={(e) => setUser(e.target.value)} placeholder="user" autoFocus />
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="password" />
          {err && <p className="text-sm text-red-500">{err}</p>}
          {hint && <p className="text-xs text-zinc-400">{hint}</p>}
          <Button type="submit" disabled={busy || !password} className="w-full">Sign in</Button>
        </form>
      </Card>
    </div>
  );
}

type ChatEntry = { role: string; content: string; meta?: string };

const RESUME_RECONNECTING = "连接断了，正在重连… 这一轮在后台继续，不用重发";
const RESUME_FOLLOWING = "已重新连上，回复还在后台生成";
const NETWORK_DOWN = "连不上 console，请检查网络后重试";
const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

function ChatPage({ onPage }: { onPage: (p: Page) => void }) {
  const [convs, setConvs] = useState<Conversation[]>([]);
  // Sidebar history tabs: chats vs teams. The Teams tab bootstraps the
  // agent-studio integration (status + consented install) and lists live
  // teams as conversation-like entry points.
  const [sideTab, setSideTab] = useState<"chat" | "team">("chat");
  const [teamsLive, setTeamsLive] = useState<{ name: string }[]>([]);
  const [studio, setStudio] = useState<{ installed: boolean; daemon_running: boolean; dir: string; phase: string; logs?: string[]; error?: string } | null>(null);
  const [active, setActive] = useState<Conversation | null>(null);
  const [entries, setEntries] = useState<ChatEntry[]>([]);
  const [gwProvider, setGwProvider] = useState<Provider | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [input, setInput] = useState("");
  const [attachments, setAttachments] = useState<{ name: string; size: number; stored: string; path: string }[]>([]);
  const [uploading, setUploading] = useState(false);
  const [listening, setListening] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const requiresRepo = useRef<HTMLInputElement>(null);
  const recogRef = useRef<any>(null);
  const [orchestrate, setOrchestrate] = useState(true);
  const [busy, setBusy] = useState(false);
  const [approval, setApproval] = useState<{ id: string; detail: string } | null>(null);
  // Budget meter (issue #66): live conversation token spend from the harness
  // pre_llm/post_llm gate events.
  const [budget, setBudget] = useState<{ spent: number; limit: number } | null>(null);
  const [err, setErr] = useState("");
  // Shown while a turn is followed after its stream broke (phone locked,
  // network switched): the server keeps running it, the bubble keeps filling.
  const [resumeNote, setResumeNote] = useState("");
  const activeRef = useRef(active);
  activeRef.current = active;
  const followTok = useRef(0);
  const followingConv = useRef<number | null>(null);
  const [unread, setUnread] = useState<Record<string, number>>({});
  // H5 (mobile) drawers: the conversation rail and the dispatch rail are
  // hidden below md/lg — on phones they open as overlay drawers instead.
  const [convDrawer, setConvDrawer] = useState(false);
  const rail = useDispatchRail();
  const [runs, setRuns] = useState<DispatchRun[] | null>(null);
  const [dispatchStale, setDispatchStale] = useState(false);
  const [busyWorkers, setBusyWorkers] = useState<WorkerStatus[]>([]);
  const [brainActivity, setBrainActivity] = useState<BrainActivity | null>(null);
  const workingWorkers = runs?.some(r => r.status === "running" || r.status === "queued")
    ? [] : busyWorkers.map(w => w.name);
  const dispatchVersion = useRef(0);
  const messageListRef = useRef<HTMLDivElement>(null);
  // Run drawer (issue #45 F2): "View run" on team-chat bubbles whose meta
  // carries a task_id. Hidden for good once the daemon lacks the run RPCs.
  const [runDrawerTask, setRunDrawerTask] = useState<string | null>(null);
  const [teamRunsHidden, setTeamRunsHidden] = useState(false);

  const resolveApproval = async (ok: boolean) => {
    if (!approval) return;
    try {
      await api.approve(approval.id, ok);
    } catch {
      /* gate also resolves via timeout */
    }
    setApproval(null);
  };

  const refreshConvs = useCallback(async () => {
    const r = await api.conversations();
    setConvs(r.conversations ?? []);
  }, []);

  useEffect(() => {
    api.providers().then((r) => {
      const list = (r.providers ?? []).filter((p) => p.enabled);
      setProviders(list);
      const gw = list.find((p) => p.type === "gateway") ?? null;
      setGwProvider(gw);
    }).catch(() => {});
    api.agents().then((r) => {
      setAgents(r.agents ?? []);
    }).catch(() => {});
  }, []);

  useEffect(() => {
    const list = messageListRef.current;
    if (list) list.scrollTo({ top: list.scrollHeight, behavior: "smooth" });
  }, [entries]);

  // The toolbar, badges and rail share a single feed, including while the rail is closed.
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const version = dispatchVersion.current;
      await Promise.allSettled([
        api.dispatches().then(data => {
          if (stopped || version !== dispatchVersion.current) return;
          setRuns(data.dispatches ?? []);
          setUnread(data.unread_by_conv ?? {});
          setDispatchStale(false);
        }).catch(() => { if (!stopped) setDispatchStale(true); }),
        api.workersStatus().then(data => { if (!stopped) setBusyWorkers((data.workers ?? []).filter(w => w.busy)); }),
        api.conversations().then(data => { if (!stopped) setConvs(data.conversations ?? []); }),
      ]);
      if (!stopped) timer = setTimeout(poll, 4000);
    };
    void poll();
    return () => { stopped = true; clearTimeout(timer); };
  }, []);

  // Completed runs post a receipt into their originating conversation.
  useEffect(() => {
    let stop = false;
    const tick = async () => {
      try {
        if (active && !busy) {
          const mr = await api.messages(active.ID);
          if (stop) return;
          const fresh = (mr.messages ?? []).map((m) => ({ role: m.Role, content: m.Content, meta: m.Meta }));
          const latest = fresh.at(-1);
          const receipt = latest?.meta && JSON.parse(latest.meta).background_run;
          // issue #100 N1: the server list is authoritative; a refresh that
          // changes nothing keeps the current array (and any live turn
          // bubble) untouched.
          setEntries((cur) => {
            if (fresh.length === cur.length && latest?.meta === cur.at(-1)?.meta) return cur;
            return supersedeWith(cur, fresh);
          });
        }
      } catch { /* transient — next tick retries */ }
    };
    const t = setInterval(tick, 6000);
    return () => { stop = true; clearInterval(t); };
  }, [active, busy]);

  function forgetRuns(id?: number) {
    // A poll started before the dismissal must not restore the cleared cards.
    dispatchVersion.current++;
    setRuns(current => current?.filter(run => id !== undefined ? run.id !== id
      : run.status === "running" || run.status === "queued") ?? null);
  }

  const deleteConv = async (c: Conversation) => {
    if (!window.confirm(`Delete "${c.Title || "#" + c.ID}" and its messages?`)) return;
    try {
      await api.deleteConversation(c.ID);
      if (active?.ID === c.ID) { setActive(null); setEntries([]); }
      await refreshConvs();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const renameConv = async (c: Conversation) => {
    const title = window.prompt("New title", c.Title);
    if (!title || !title.trim()) return;
    try {
      await api.renameConversation(c.ID, title.trim());
      await refreshConvs();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  // Leaving a conversation stops following its turn there (the server keeps
  // running it; coming back picks it up again).
  useEffect(() => {
    if (followingConv.current !== null && followingConv.current !== active?.ID) {
      followTok.current++;
      followingConv.current = null;
      setResumeNote("");
      setBusy(false);
    }
  }, [active?.ID]);

  // showTurn renders the in-progress turn into the trailing assistant bubble.
  const showTurn = (view: TurnView) => setEntries((e) => {
    const i = e.length - 1;
    if (i < 0 || e[i].role !== "assistant") return e;
    const copy = e.slice();
    copy[i] = { ...copy[i], content: view.content, meta: view.meta };
    return copy;
  });

  // followTurn keeps showing a turn the server runs on after its stream broke,
  // or one that was already running when the conversation was opened. It
  // returns false when a conversation switch took over (busy is not ours to
  // clear then). sent/restore: the message this browser just sent — when it
  // never reached the server it goes back into the input box.
  const followTurn = async (f: { conv: number; kind: TurnKind; turnId: string | null; sent: string | null; restore: string | null }) => {
    const tok = ++followTok.current;
    followingConv.current = f.conv;
    const mine = () => followTok.current === tok && activeRef.current?.ID === f.conv;
    let failures = 0;
    setErr("");
    setResumeNote(RESUME_RECONNECTING);
    try {
      while (mine()) {
        let turn: ChatTurn | null;
        try {
          turn = (await api.conversationTurn(f.conv)).turn;
        } catch (ex) {
          if (!isNetworkError(ex) || ++failures >= FOLLOW_MAX_FAILURES) {
            if (mine()) setErr(isNetworkError(ex) ? "一直连不上 console。这一轮可能还在后台跑，网络恢复后刷新页面查看结果" : (ex as Error).message);
            return mine();
          }
          if (mine()) setResumeNote(RESUME_RECONNECTING);
          await sleep(followDelay(failures));
          continue;
        }
        failures = 0;
        if (!mine()) break;
        if (!turn || (f.turnId !== null && turn.id !== f.turnId)) {
          // No turn of ours on the server: show what it kept.
          const r = await api.messages(f.conv).catch(() => null);
          if (r && mine()) {
            const fresh = (r.messages ?? []).map((m) => ({ role: m.Role, content: m.Content, meta: m.Meta }));
            setEntries((cur) => supersedeWith(cur, fresh));
            if (f.sent !== null && fresh.filter((m) => m.role === "user").at(-1)?.content !== f.sent) {
              if (f.restore) setInput((cur) => cur || f.restore!);
              setErr("消息没发出去，已放回输入框，网络恢复后再发");
            }
          }
          return mine();
        }
        const replay = replayTurn(f.kind, turn.events);
        showTurn(replay.view);
        if (f.kind === "chat") {
          setApproval(replay.approval);
          if (replay.budget) setBudget(replay.budget);
          const act = turn.events.reduce<BrainActivity>((a, ev) => brainEvent(a, ev), beginBrainTurn(f.conv, turn.request));
          setBrainActivity(turn.running ? act : endBrainTurn(act));
        }
        if (!turn.running) return mine();
        setResumeNote(RESUME_FOLLOWING);
        await sleep(2000);
      }
      return false;
    } finally {
      if (followTok.current === tok) {
        followingConv.current = null;
        setResumeNote("");
      }
    }
  };

  // A turn still running on the server (another tab, or this page reloaded
  // after the phone dropped it) is picked up when its conversation opens.
  const resumeRunningTurn = async (c: Conversation) => {
    const r = await api.conversationTurn(c.ID).catch(() => null);
    const turn = r?.turn;
    if (!turn?.running || activeRef.current?.ID !== c.ID || followingConv.current !== null) return;
    setBusy(true);
    setEntries((e) => [...e, { role: "assistant", content: "" }]);
    if (await followTurn({ conv: c.ID, kind: turn.kind, turnId: turn.id, sent: null, restore: null })) setBusy(false);
  };

  const openConv = async (c: Conversation) => {
    setActive(c);
    setErr("");
    setConvDrawer(false);
    const r = await api.messages(c.ID);
    setEntries((r.messages ?? []).map((m) => ({ role: m.Role, content: m.Content, meta: m.Meta })));
    setTeamTyped([]);
    if (unread[c.ID] > 0) {
      api.ackDispatches(c.ID).then(() => setUnread((u) => ({ ...u, [c.ID]: 0 }))).catch(() => {});
    }
    if (c.Kind !== "team" && !busy) void resumeRunningTurn(c);
  };

  // Studio bootstrap + live teams polling for the Teams sidebar tab.
  useEffect(() => {
    const read = () => api.studioStatus().then(setStudio).catch(() => setStudio(null));
    read();
    const busy = studio?.phase === "installing" || studio?.phase === "starting";
    const t = setInterval(read, busy ? 2000 : 30000);
    return () => clearInterval(t);
  }, [studio?.phase]);
  useEffect(() => {
    if (sideTab !== "team") return;
    const read = () => api.teamTeams().then((r) => setTeamsLive((r.teams ?? []).map((x: Record<string, any>) => ({ name: String(x.name || "") })))).catch(() => setTeamsLive([]));
    read();
    const t = setInterval(read, 10000);
    return () => clearInterval(t);
  }, [sideTab]);
  // Team group chat: clicking a team row opens (or creates) a conversation
  // bound to that team — no page navigation. Each message there assigns a
  // task to the team; replies come back via inbox polling.
  // Team replies: poll the operator inbox while a team conversation is open
  // and append new messages (server dedupes by msg_id).
  useEffect(() => {
    if (active?.Kind !== "team") return;
    const pull = async () => {
      try {
        const r = await api.teamChatPoll(active.ID);
        if ((r.messages ?? []).length) {
          // issue #100 N1: live appends dedupe against the view — a poll
          // racing the authoritative refresh cannot duplicate a row.
          setEntries((e) => mergeEntries(e, r.messages.map((m: any) => ({ role: m.role, content: m.content, meta: m.meta }))));
        }
      } catch { /* daemon offline — next tick retries */ }
      // console#79 R4: kernel-typed inbox cards (plan_approval /
      // shutdown_request) ride their own projection, not chat text.
      try {
        const t = await api.teamOp({ op: "inbox_typed", team: active.AgentName });
        setTeamTyped(((t.messages ?? []) as Record<string, any>[]).filter((c) => c && c.msg_id));
      } catch { setTeamTyped([]); }
    };
    pull();
    const t = setInterval(pull, 5000);
    return () => clearInterval(t);
  }, [active?.ID, active?.Kind]); // eslint-disable-line react-hooks/exhaustive-deps

  // Team banner: roster of the open team conversation + the daemon's
  // registered agents for the add-member picker. Reloads on switch/edit.
  const [teamRoster, setTeamRoster] = useState<string[]>([]);
// console#79 R4: pending kernel-typed inbox cards for the operator member
// (plan_approval / shutdown_request) — actionable approve/reject, not chat text.
const [teamTyped, setTeamTyped] = useState<Record<string, any>[]>([]);
const respondTyped = async (messageId: string, decision: "approve" | "reject") => {
  if (!active || active.Kind !== "team") return;
  setTeamTyped((cs) => cs.map((c) => (c.msg_id === messageId ? { ...c, pending: decision } : c)));
  try {
    await api.teamOp({ op: "inbox_respond", team: active.AgentName, message_id: messageId, decision });
    setTeamTyped((cs) => cs.filter((c) => c.msg_id !== messageId));
  } catch (ex) {
    setErr((ex as Error).message);
    setTeamTyped((cs) => cs.map((c) => (c.msg_id === messageId ? { ...c, pending: undefined } : c)));
  }
};
  const [teamPanelOpen, setTeamPanelOpen] = useState(false);
  const [teamAgentList, setTeamAgentList] = useState<Record<string, any>[]>([]);
  const [teamPickAdd, setTeamPickAdd] = useState<string[]>([]);
  const [teamPanelBusy, setTeamPanelBusy] = useState(false);
  // Steer context (issue #45 F3): the running task of the open team, if any —
  // while one is live the composer becomes a [STEER] composer and sends go
  // through teams.supplement instead of creating another task.
  const [teamRunning, setTeamRunning] = useState<{ task_id: string; title: string } | null>(null);
  const loadTeamRoster = useCallback(async (name: string) => {
    try {
      const r = await api.teamView(name);
      setTeamRoster((r.roster ?? []).map((m) => String(m.name || m.agent_id || "")).filter(Boolean));
      const running = (r.tasks ?? []).find((t) => String(t.status || "").toLowerCase() === "running");
      setTeamRunning(running ? { task_id: String(running.task_id || running.id || ""), title: String(running.title || running.description || "") } : null);
    } catch { setTeamRoster([]); setTeamRunning(null); }
  }, []);
  useEffect(() => {
    setTeamPanelOpen(false);
    if (active?.Kind !== "team" || !active.AgentName) { setTeamRoster([]); setTeamRunning(null); return; }
    loadTeamRoster(active.AgentName);
    // Keep the steer context live: a task finishing flips the composer back
    // to task-assignment mode on the next tick.
    const t = setInterval(() => loadTeamRoster(active.AgentName), 5000);
    return () => clearInterval(t);
  }, [active?.ID, active?.Kind, active?.AgentName, loadTeamRoster]); // eslint-disable-line react-hooks/exhaustive-deps
  // Team SPEC panel (issue #41): the group-chat entry point for the shared
  // SPEC.md. Read via teams.spec_read; edits persist through teams.spec_write
  // (human-direct write path, v1). A failed read (no SPEC yet) still allows
  // writing the first version.
  const [specPanelOpen, setSpecPanelOpen] = useState(false);
  const [specText, setSpecText] = useState("");
  const [specBusy, setSpecBusy] = useState(false);
  const [specErr, setSpecErr] = useState("");
  const openSpecPanel = async () => {
    if (!active?.AgentName) return;
    setSpecPanelOpen(true);
    setSpecBusy(true);
    setSpecErr("");
    try {
      const r = await api.teamOp({ op: "spec_read", team: active.AgentName });
      setSpecText(String(r.spec ?? ""));
    } catch (e) {
      setSpecText("");
      setSpecErr((e as Error).message);
    } finally {
      setSpecBusy(false);
    }
  };
  const saveSpec = async () => {
    if (!active?.AgentName || !specText.trim()) return;
    setSpecBusy(true);
    setSpecErr("");
    try {
      await api.teamOp({ op: "spec_write", team: active.AgentName, text: specText });
      setSpecPanelOpen(false);
    } catch (e) {
      setSpecErr((e as Error).message);
    } finally {
      setSpecBusy(false);
    }
  };
  // Brain indicator chip: read-only view of the chat brain configured under
  // Agents -> Workers -> Brain. Editing lives there (single owner).
  const [brainLabel, setBrainLabel] = useState("");
  useEffect(() => {
    api.orchestration().then((r) => {
      const o = r.orchestrator ?? {};
      const prov = String(o.brain_provider_id || "gateway default");
      const mdl = String(o.brain_model || "");
      setBrainLabel(mdl ? `${prov} · ${mdl}` : prov);
    }).catch(() => setBrainLabel(""));
  }, []);
  const openTeamPanel = async () => {
    setTeamPicked([]);
    setTeamPanelOpen(true);
    // Member picks come from the console fleet (orchestrator workers) — the
    // same source the team-creation modal uses, so create and modify always
    // offer the same roster candidates (never the daemon's raw agents).
    api.orchestration()
      .then((r) => setTeamAgentList((r.orchestrator?.workers ?? []).map((w: Record<string, any>) => ({
        agent_id: String(w.name || w.agent),
        name: String(w.name || w.agent),
        enabled: w.enabled,
      }))))
      .catch(() => setTeamAgentList([]));
  };
  const addTeamMembers = async () => {
    if (teamPickAdd.length === 0 || !active?.AgentName) return;
    setTeamPanelBusy(true);
    try {
      // One roster_register call per picked worker — partial failures stay
      // visible, the roster reloads from the daemon afterwards.
      for (const id of teamPickAdd) {
        const a = teamAgentList.find((x) => String(x.agent_id || x.name || "") === id);
        const nm = String(a?.name || id);
        await api.teamOp({
          op: "register_member",
          agent_id: id,
          name: nm,
          team: active.AgentName,
          status: "idle",
          capabilities: Array.isArray(a?.capabilities) ? a.capabilities.map(String) : [],
        });
      }
      setTeamPicked([]);
      setTeamPanelOpen(false);
      await loadTeamRoster(active.AgentName);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setTeamPanelBusy(false);
    }
  };

  const openTeamChat = async (name: string) => {
    setConvDrawer(false);
    const existing = convs.find((c) => c.Kind === "team" && c.AgentName === name);
    if (existing) { await openConv(existing); return; }
    try {
      const r = await api.createConversation(`team: ${name}`, "team", name);
      await refreshConvs();
      setActive({ ID: r.id, Title: `team: ${name}`, Kind: "team", User: "", AgentName: name, Workdir: "", CreatedAt: "" });
      setEntries([]);
      setErr("");
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  // New team modal: name + worker selection, created in place.
  const [teamModalOpen, setTeamModalOpen] = useState(false);
  const [teamName, setTeamName] = useState("");
  const [teamPicked, setTeamPicked] = useState<string[]>([]);
  const [teamWorkers, setTeamWorkers] = useState<WorkerSettings[]>([]);
  const [teamErr, setTeamErr] = useState("");
  const [teamCreating, setTeamCreating] = useState(false);
  const [teamMode, setTeamMode] = useState("default");
  // Initial SPEC (issue #41): optional textarea, collapsed by default —
  // passed through to teams.register, the daemon seeds SPEC.md with it.
  const [teamSpec, setTeamSpec] = useState("");
  const [teamSpecOpen, setTeamSpecOpen] = useState(false);
  // console#79 R1/R2/R3: per-member economics (budget/permission/allowlist),
  // playbook toggle, and the kernel #124 fan-out ladder. All opt-in —
  // defaults match the previous hardcoded payload byte-for-byte.
  const [teamMemberCfg, setTeamMemberCfg] = useState<Record<string, { budget: number; mode: string; tools: string[]; skills: string[] }>>({});
  const [teamLeadBudget, setTeamLeadBudget] = useState(200000);
  const [teamPlaybook, setTeamPlaybook] = useState(false);
  // console#79 R6: topology preset rides team metadata; max_depth is the
  // advanced kernel field (default 2 preserved); skills chips list the
  // installed SKILL.md surface (member.skills is also the persona gate).
  const [teamSkills, setTeamSkills] = useState<{ name: string }[]>([]);
  const [teamTopology, setTeamTopology] = useState("hub");
  const [teamMaxDepth, setTeamMaxDepth] = useState(2);
  const TEAM_FANOUT = { simpleMax: 5, complexMax: 15, hardMax: 20 };
  const TEAM_TOOLS = ["file_read", "file_write", "shell_exec", "web_search", "kb_search"];
  const memberCfg = (wn: string) => teamMemberCfg[wn] ?? { budget: 50000, mode: teamMode, tools: ["file_read"], skills: [] as string[] };
  const setMemberCfg = (wn: string, patch: Partial<{ budget: number; mode: string; tools: string[]; skills: string[] }>) =>
    setTeamMemberCfg((m) => ({ ...m, [wn]: { ...memberCfg(wn), ...patch } }));
  useEffect(() => {
    if (!teamModalOpen) return;
    api.orchestration().then((r) => setTeamWorkers(r.orchestrator?.workers ?? [])).catch(() => setTeamWorkers([]));
    api.skills().then((r) => setTeamSkills(r.skills ?? [])).catch(() => setTeamSkills([]));
  }, [teamModalOpen]);
  // Member context mapping (issue #41): the register payload carries a
  // resident per-member context derived from the worker config (role
  // description + key conventions). Console never reads filesystems for
  // this — workers have no per-worker AGENTS.md semantics.
  const workerContext = (w: WorkerSettings | undefined, wn: string): string =>
    [
      w?.agent ? `Role: ${w.agent} worker (${wn})` : `Role: team member (${wn})`,
      w?.model ? `Model: ${w.model}` : "",
      w?.repo ? `Repo: ${w.repo}${w.repo_branch ? ` @ ${w.repo_branch}` : ""}` : "",
      w?.isolation ? `Isolation: ${w.isolation}` : "",
      w?.memory ? `Memory: ${w.memory}` : "",
      w?.work_dir ? `Workdir: ${w.work_dir}` : "",
    ].filter(Boolean).join("\n");
  const createTeam = async () => {
    const nm = teamName.trim();
    if (!nm) { setTeamErr("team name required"); return; }
    // console#79 R3: client-side pre-validation of the kernel #124 ladder —
    // validate_brief hard-errors above FANOUT_HARD_MAX server-side.
    if (teamPicked.length > TEAM_FANOUT.hardMax) {
      setTeamErr(`fan-out ${teamPicked.length} exceeds the kernel hard max ${TEAM_FANOUT.hardMax} — split into phases or subteams`);
      return;
    }
    const members = teamPicked.map((wn) => {
      const w = teamWorkers.find((x) => (x.name || x.agent) === wn);
      const cfg = memberCfg(wn);
      return {
        name: (w?.name || w?.agent || wn).replace(/[^A-Za-z0-9_-]/g, "-"),
        role: w?.agent || "",
        skills: cfg.skills,
        // file_read by default (issue #41): members without it cannot read
        // the full SPEC.md and degrade to the injected section only.
        tool_allowlist: cfg.tools.length > 0 ? cfg.tools : ["file_read"],
        permission_mode: cfg.mode,
        budget_tokens: cfg.budget,
        max_concurrency: 1,
        context: workerContext(w, wn),
      };
    });
    if (members.length === 0) { setTeamErr("pick at least one worker"); return; }
    // console#79 R1: mirror the kernel TeamDefError rule (member budget must
    // stay <= lead budget, Deadline.child semantics) before the server does.
    const over = members.filter((m) => m.budget_tokens > teamLeadBudget);
    if (over.length > 0) {
      setTeamErr(`member budget exceeds lead budget ${teamLeadBudget}: ${over.map((m) => m.name).join(", ")}`);
      return;
    }
    setTeamCreating(true);
    setTeamErr("");
    try {
      await api.teamCreate({
        team: { name: nm, namespace_key: nm, members, lead_budget_tokens: teamLeadBudget, playbook: teamPlaybook, max_depth: teamMaxDepth, metadata: { topology: teamTopology } },
        tasks: [], lead_start: false, lead_goal: "",
        spec: teamSpec.trim(),
      });
      setTeamModalOpen(false);
      setTeamName("");
      setTeamPicked([]);
      setTeamMemberCfg({});
      setTeamSpec("");
      setTeamSpecOpen(false);
      await openTeamChat(nm);
    } catch (ex) {
      setTeamErr((ex as Error).message);
    } finally {
      setTeamCreating(false);
    }
  };
  const studioInstalling = studio?.phase === "installing" || studio?.phase === "starting";
  const studioReady = studio?.daemon_running || studio?.installed;

  const newChat = async () => {
    const r = await api.createConversation("chat " + new Date().toLocaleString(), "chat");
    await refreshConvs();
    setActive(null);
    setEntries([]);
    await api.messages(r.id).catch(() => {});
  };

  const pickFiles = () => fileInputRef.current?.click();
  const onFiles = async (files: FileList | null) => {
    if (!files?.length) return;
    setUploading(true);
    try {
      for (const f of Array.from(files)) {
        const r = await api.uploadAttachment(f);
        setAttachments((a) => [...a, r]);
      }
    } catch (e) {
      console.error("attachment upload failed", e);
      setErr((e as Error).message);
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  };

  // Voice dictation via the browser Web Speech API (Chrome/Edge, on-device).
  // A local fusion-speech daemon is the fallback path later — the button
  // disables itself with a hint when the browser has no recognizer.
  const voiceSupported = typeof window !== "undefined" &&
    !!((window as any).webkitSpeechRecognition || (window as any).SpeechRecognition);
  const toggleVoice = () => {
    const SR = (window as any).webkitSpeechRecognition || (window as any).SpeechRecognition;
    if (!SR) return;
    if (listening) {
      recogRef.current?.stop();
      return;
    }
    const rec = new SR();
    rec.lang = navigator.language || "zh-CN";
    rec.interimResults = true;
    rec.continuous = false;
    rec.onresult = (ev: any) => {
      let t = "";
      for (let i = ev.resultIndex; i < ev.results.length; i++) t += ev.results[i][0].transcript;
      setInput((cur) => (cur ? cur + t : t));
    };
    rec.onend = () => setListening(false);
    rec.onerror = (ev: any) => {
      console.error("speech recognition error", ev?.error);
      setListening(false);
    };
    recogRef.current = rec;
    setListening(true);
    rec.start();
  };

  const send = async () => {
    const text = input.trim();
    if (!text || busy) return;
    if (matchMedia("(pointer: coarse)").matches && document.activeElement instanceof HTMLTextAreaElement) {
      document.activeElement.blur();
    }
    // Mid-run steering (issue #45 F3): while the open team has a running
    // task, a plain send becomes a supplement into that run instead of a new
    // task — receipt states render on the steer bubble.
    if (active?.Kind === "team" && teamRunning && !text.startsWith("@")) {
      await sendSteer(text);
      return;
    }
    // Attachments pass the stored file path, never the content: the model
    // pulls segments through the read API (byte-range with continuation
    // metadata), so a single call can never push a whole large document into
    // the context and the chat bubble stays small.
    let full = text;
    if (attachments.length > 0) {
      const parts = attachments.map((a) =>
        `\n\n[attachment: ${a.name} (${Math.max(1, Math.round(a.size / 1024))} KB) — GET /api/attachments/read?stored=${a.stored}&offset=0&max_bytes=65536; follow next_offset until eof, do not read the whole file at once]`);
      full = text + parts.join("");
    }
    // @agent mention hands the message off explicitly; anything else is a
    // plain brain chat turn (orchestration lets the brain dispatch workers).
    // Named workers dispatch in the background — same pipeline as a brain
    // run_agent with no brain turn in front, so the chat stays usable —
    // while bare CLI agents keep the live-streaming foreground run.
    const mm = text.match(/@([a-zA-Z0-9_-]+)/);
    const mentioned = mm ? detectedAgents.find((a) => a.name === mm[1]) : null;
    if (mentioned?.is_worker) {
      await dispatchWorkerTask(text, mentioned.name, requiresRepo.current?.checked ?? false);
      return;
    }
    if (mentioned) {
      await runAgentTask(text, mentioned.name);
      return;
    }
    if (active?.Kind !== "team" && !gwProvider && providers.length === 0) {
      setErr("no enabled provider — set one up on the Models page first");
      return;
    }
    setBusy(true);
    setErr("");
    setInput("");
    setAttachments([]);
    let brainConversation: number | undefined;
    let ownBusy = true;
    const turnId = newTurnId();
    try {
      let conv = active;
      if (!conv) {
        const r = await api.createConversation(text.slice(0, 40), "chat");
        conv = { ID: r.id, Title: text.slice(0, 40), Kind: "chat", User: "", AgentName: "", Workdir: "", CreatedAt: "" };
        setActive(conv);
        await refreshConvs();
      }
      const idx = conv.ID;
      // The user may switch conversations while this turn streams (switching
      // is never blocked). Every view write below must confirm this
      // conversation is still the one on screen, or conv A's stream paints
      // into conv B's timeline until the turn ends.
      const onConv = () => activeRef.current?.ID === idx;
      if (onConv()) setEntries((e) => [...e, { role: "user", content: full }, { role: "assistant", content: "" }]);
      if (conv.Kind !== "team") {
        brainConversation = idx;
        setBrainActivity(beginBrainTurn(idx, text));
      }
      let view = emptyTurn();
      // issue #100 N2: render at most once per reveal window — arrival
      // lumps smooth out; the final flushNow keeps the last text exact.
      const reveal = createPacer(() => { if (onConv()) showTurn(view); });
      await streamPOST("/api/chat", { conversation_id: idx, content: full, orchestrate, turn_id: turnId }, (ev) => {
        if (brainConversation !== undefined) {
          setBrainActivity(prev => prev && prev.conversation === brainConversation ? brainEvent(prev, ev) : prev);
        }
        view = applyChatEvent(view, ev);
        reveal.tick();
        if (ev.type === "approval" || ev.type === "tool") setApproval((cur) => approvalAfter(cur, ev));
        if (ev.type === "budget") setBudget({ spent: (ev.spent as number) ?? 0, limit: (ev.limit as number) ?? 0 });
        // Feedback trigger paths: stream errors offer to draft an issue; the
        // suggest_feedback tool fetches the stashed brain draft for the card.
        if (ev.type === "error") notifyFeedbackError(String(ev.text ?? ""));
        if (ev.type === "tool" && ev.tool === "suggest_feedback") {
          api.feedbackDraftLatest(idx).then((d) => notifyFeedbackSuggestion(d)).catch(() => {});
        }
      });
      reveal.flushNow();
    } catch (ex) {
      if (isNetworkError(ex) && brainConversation !== undefined) {
        // The stream broke, not the turn: the server keeps running it.
        ownBusy = await followTurn({ conv: brainConversation, kind: "chat", turnId, sent: full, restore: text });
      } else {
        const message = isNetworkError(ex) ? NETWORK_DOWN : (ex as Error).message;
        if (isNetworkError(ex)) setInput((cur) => cur || text);
        setErr(message);
        notifyFeedbackError(message);
        setBrainActivity(prev => prev && prev.conversation === brainConversation ? brainEvent(prev, { type: "error", text: message }) : prev);
      }
    } finally {
      setBrainActivity(prev => prev && prev.conversation === brainConversation ? endBrainTurn(prev) : prev);
      if (ownBusy) setBusy(false);
    }
  };

  // sendSteer pushes one supplement into the running task and renders the
  // receipt transition on the steer bubble (pending -> delivered n/3 /
  // delivered_as_inbox). Cap errors surface the reason copy.
  const sendSteer = async (text: string) => {
    if (!active || active.Kind !== "team" || !teamRunning) return;
    const task = teamRunning;
    setBusy(true);
    setErr("");
    setInput("");
    setAttachments([]);
    const meta = JSON.stringify({ steer: true, task_id: task.task_id, receipt: "pending" });
    setEntries((e) => [...e, { role: "user", content: text, meta }]);
    try {
      const r = await api.teamOp({ op: "supplement", team: active.AgentName, task_id: task.task_id, text });
      const receipt = String(r.receipt || "delivered");
      const used = Number(r.steers_used || 0);
      setEntries((e) =>
        e.map((m) =>
          m.meta === meta
            ? { ...m, meta: JSON.stringify({ steer: true, task_id: task.task_id, receipt, steers_used: used }) }
            : m
        )
      );
    } catch (ex) {
      const ae = ex as ApiError;
      setEntries((e) =>
        e.map((m) =>
          m.meta === meta
            ? { ...m, meta: JSON.stringify({ steer: true, task_id: task.task_id, receipt: "failed", error: ae.message }) }
            : m
        )
      );
      setErr(ae.reason_code ? `${ae.message} [${ae.reason_code}]` : ae.message);
    } finally {
      setBusy(false);
    }
  };

  // @worker mention fires a direct background dispatch (POST /api/agent/run
  // with background) — the run lands in the dispatch panel and its result is
  // appended to this conversation when finished, no brain turn in front.
  const dispatchWorkerTask = async (text: string, worker: string, requiresRepo: boolean) => {
    if (!text || busy || !worker) return;
    setBusy(true);
    setErr("");
    setInput("");
    setAttachments([]);
    try {
      let conv = active;
      if (!conv) {
        const r = await api.createConversation(text.slice(0, 40), "agent", worker, "");
        conv = { ID: r.id, Title: text.slice(0, 40), Kind: "agent", User: "", AgentName: worker, Workdir: "", CreatedAt: "" };
        setActive(conv);
        await refreshConvs();
      }
      const r = await api.agentRun({ conversation_id: conv.ID, agent: worker, task: text, workdir: "", background: true, requires_repo: requiresRepo });
      setEntries((e) => [...e, { role: "user", content: text }, { role: "assistant", content: r.note || "dispatched as background run — progress in the dispatch panel" }]);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const runAgentTask = async (text: string, agent: string) => {
    if (!text || busy || !agent) return;
    setBusy(true);
    setErr("");
    setInput("");
    let convId: number | undefined;
    let ownBusy = true;
    const turnId = newTurnId();
    try {
      let conv = active;
      if (!conv || conv.Kind !== "agent" || conv.AgentName !== agent) {
        const r = await api.createConversation(text.slice(0, 40), "agent", agent, "");
        conv = { ID: r.id, Title: text.slice(0, 40), Kind: "agent", User: "", AgentName: agent, Workdir: "", CreatedAt: "" };
        setActive(conv);
        setEntries([]);
        await refreshConvs();
      }
      convId = conv.ID;
      // Same conversation guard as the chat send: the stream must not paint
      // into another conversation's timeline if the user switches away.
      const onConv = () => activeRef.current?.ID === convId;
      if (onConv()) setEntries((e) => [...e, { role: "user", content: text }, { role: "assistant", content: "" }]);
      let view = emptyTurn();
      const reveal = createPacer(() => { if (onConv()) showTurn(view); });
      await streamPOST("/api/agent/run", { conversation_id: conv.ID, agent, task: text, workdir: "", turn_id: turnId }, (ev) => {
        view = applyAgentEvent(view, ev);
        reveal.tick();
        if (ev.type === "error") notifyFeedbackError(String(ev.text ?? ""));
      });
      reveal.flushNow();
    } catch (ex) {
      if (isNetworkError(ex) && convId !== undefined) {
        ownBusy = await followTurn({ conv: convId, kind: "agent", turnId, sent: text, restore: text });
      } else {
        if (isNetworkError(ex)) setInput((cur) => cur || text);
        const agentErr = isNetworkError(ex) ? NETWORK_DOWN : (ex as Error).message;
        setErr(agentErr);
        notifyFeedbackError(agentErr);
      }
    } finally {
      if (ownBusy) setBusy(false);
    }
  };

  const detectedAgents = agents.filter((a) => a.detected);

  // @mention: while typing an "@name" token at the end of the input, offer
  // completing agents; sending a message that @mentions one hands it off.
  const mm = /@([a-zA-Z0-9_-]*)$/.exec(input);
  const mentionQuery = mm ? mm[1] : null;
  const mentionOptions = mentionQuery === null
    ? []
    : detectedAgents.filter((a) => a.name.toLowerCase().startsWith(mentionQuery.toLowerCase()));
  const insertMention = (name: string) => {
    setInput(input.replace(/@([a-zA-Z0-9_-]*)$/, `@${name} `));
  };
  const mentionedAgent = (() => {
    const m = input.match(/@([a-zA-Z0-9_-]+)/);
    return m ? detectedAgents.find((a) => a.name === m[1]) : null;
  })();

  return (
    <div className="chat-layout flex h-full">
      {convDrawer && (
        <div className="fixed inset-0 z-30 bg-black/40 lg:hidden" onClick={() => setConvDrawer(false)} />
      )}
      {/* Leftmost vertical rail: Chats / Teams switcher. Clicking an icon
          swaps the history column beside it — no top tabs. */}
      <div className="flex w-12 shrink-0 flex-col items-center gap-1.5 border-r border-zinc-200/80 bg-zinc-100/60 py-3 dark:border-zinc-800 dark:bg-zinc-900/60">
        <button
          onClick={() => setSideTab("chat")}
          title="Chats"
          className={`flex h-9 w-9 items-center justify-center rounded-xl transition-colors ${
            sideTab === "chat"
              ? "bg-indigo-600 text-white shadow-sm"
              : "text-zinc-400 hover:bg-zinc-200/60 hover:text-zinc-600 dark:hover:bg-zinc-800/60 dark:hover:text-zinc-300"
          }`}
        >
          <MessageSquare size={17} />
        </button>
        <button
          onClick={() => setSideTab("team")}
          title="Teams"
          className={`flex h-9 w-9 items-center justify-center rounded-xl transition-colors ${
            sideTab === "team"
              ? "bg-indigo-600 text-white shadow-sm"
              : "text-zinc-400 hover:bg-zinc-200/60 hover:text-zinc-600 dark:hover:bg-zinc-800/60 dark:hover:text-zinc-300"
          }`}
        >
          <Users size={17} />
        </button>
      </div>
      <aside
        className={`${convDrawer ? "fixed inset-y-0 left-0 z-40 flex w-72 shadow-xl" : "hidden"} flex-col border-r border-zinc-200/80 dark:border-zinc-800 bg-white lg:static lg:flex lg:w-64 lg:shadow-none lg:bg-white/40 dark:bg-zinc-900 dark:lg:bg-zinc-900/40`}
      >
        <div className="p-3">
          {sideTab === "chat" ? (
            <Button variant="outline" className="w-full" onClick={newChat}>
              <Plus size={14} /> New chat
            </Button>
          ) : (
            <Button variant="outline" className="w-full" onClick={() => setTeamModalOpen(true)} disabled={!studioReady}>
              <Plus size={14} /> New team
            </Button>
          )}
        </div>
        {sideTab === "team" && (
          <div className="mx-3 mb-2">
            {studio === null && (
              <div className="rounded-xl border border-zinc-300/80 bg-zinc-100/60 p-3 text-xs text-zinc-600 dark:border-zinc-700 dark:bg-zinc-800/60 dark:text-zinc-300">
                <p>无法获取 agent-studio 状态</p>
                <button onClick={() => api.studioStatus().then(setStudio).catch(() => {})}
                  className="mt-2 rounded-lg bg-zinc-500/20 px-2.5 py-1.5 font-medium hover:bg-zinc-500/30">重试</button>
              </div>
            )}
            {studio !== null && studio.installed && studio.daemon_running && (
              <p className="flex items-center gap-1.5 rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs font-medium text-emerald-700 dark:text-emerald-400">
                <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500" /> agent-studio 运行中
              </p>
            )}
            {studio !== null && !studioReady && (
              <div className="rounded-xl border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
                {studio.installed ? (
                  <>
                    <p className="font-medium">fusion-agent-studio 已安装，daemon 未运行</p>
                    <p className="mt-1 opacity-80">启动后才能使用 Team（建群、派活、收件箱都走这个守护进程）。</p>
                  </>
                ) : (
                  <>
                    <p className="font-medium">Team 需要 fusion-agent-studio</p>
                    <p className="mt-1 opacity-80">Team 功能（建群、派活、收件箱）依赖 fusion-agent-studio 守护进程，当前这台机器上没有安装。选一种方式安装，完成后自动启动 daemon。</p>
                  </>
                )}
                {studioInstalling ? (
                  <p className="mt-2 flex items-center gap-1.5"><Loader2 size={12} className="animate-spin" /> {studio.phase === "starting" ? "启动 daemon…" : "安装中…"}（完成后自动刷新）</p>
                ) : (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {studio.installed ? (
                      <button
                        onClick={() => api.studioInstall("auto").then(() => api.studioStatus().then(setStudio)).catch(() => {})}
                        className="rounded-lg bg-amber-500/20 px-2.5 py-1.5 font-medium hover:bg-amber-500/30"
                      >
                        启动 agent-studio
                      </button>
                    ) : (
                      <>
                        <button
                          onClick={() => api.studioInstall("workspace").then(() => api.studioStatus().then(setStudio)).catch(() => {})}
                          className="rounded-lg bg-amber-500/20 px-2.5 py-1.5 font-medium hover:bg-amber-500/30"
                        >
                          一键安装（工作区 pip）
                        </button>
                        <button
                          onClick={() => api.studioInstall("pypi").then(() => api.studioStatus().then(setStudio)).catch(() => {})}
                          className="rounded-lg bg-amber-500/20 px-2.5 py-1.5 font-medium hover:bg-amber-500/30"
                        >
                          pip（PyPI）
                        </button>
                        <button
                          onClick={() => api.studioInstall("brew").then(() => api.studioStatus().then(setStudio)).catch(() => {})}
                          className="rounded-lg bg-amber-500/20 px-2.5 py-1.5 font-medium hover:bg-amber-500/30"
                        >
                          Homebrew
                        </button>
                      </>
                    )}
                  </div>
                )}
                {!studio.installed && !studioInstalling && (
                  <p className="mt-2 font-mono text-[10px] leading-4 opacity-70">命令行：pip install fusion-agent-studio 或 brew install dahai80/fusion-mlx/fusion-agent-studio</p>
                )}
                {studio.error && <p className="mt-2 font-mono text-[10px] opacity-80">{studio.error}</p>}
                {(studio.logs ?? []).length > 0 && (
                  <pre className="mt-2 max-h-24 overflow-y-auto whitespace-pre-wrap font-mono text-[10px] opacity-70">{(studio.logs ?? []).slice(-8).join("\n")}</pre>
                )}
              </div>
            )}
          </div>
        )}
        <div className="flex-1 overflow-y-auto px-2 pb-3 space-y-1">
          {sideTab === "team" && studioReady && teamsLive.map((t) => (
            <button
              key={String(t.name)}
              onClick={() => openTeamChat(String(t.name))}
              className="flex w-full items-center gap-2 rounded-xl px-3 py-2 text-left text-sm hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60"
            >
              <Users size={14} className="shrink-0 text-indigo-500" />
              <span className="truncate">{String(t.name)}</span>
            </button>
          ))}
          {sideTab === "team" && studioReady && teamsLive.length === 0 && (
            <p className="px-3 py-2 text-xs text-zinc-400">No live teams yet — create one from chat (orchestrate on) or with New team.</p>
          )}
          {sideTab === "chat" && convs.map((c) => (
            <div
              key={c.ID}
              className={`group flex w-full items-center gap-1 rounded-xl pr-1 text-sm ${
                active?.ID === c.ID
                  ? "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400"
                  : "hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60"
              }`}
            >
              <button
                onClick={() => openConv(c)}
                className="flex min-w-0 flex-1 items-center gap-2 rounded-xl px-3 py-2 text-left"
              >
                {c.Kind === "agent" ? <Bot size={14} className="shrink-0" /> : <MessageSquare size={14} className="shrink-0" />}
                <span className="truncate">{c.Title || `#${c.ID}`}</span>
                {unread[c.ID] > 0 && (
                  <span className="ml-auto flex h-4.5 min-w-4.5 items-center justify-center rounded-full bg-indigo-500 px-1.5 text-[10px] font-semibold text-white" title={`${unread[c.ID]} finished background run(s) not seen yet`}>
                    {unread[c.ID]}
                  </span>
                )}
              </button>
              <button
                onClick={() => renameConv(c)}
                title="Rename"
                className="hidden shrink-0 rounded-lg p-1 text-zinc-400 hover:text-indigo-500 group-hover:block"
              >
                <Pencil size={13} />
              </button>
              <button
                onClick={() => deleteConv(c)}
                title="Delete"
                className="hidden shrink-0 rounded-lg p-1 text-zinc-400 hover:text-red-500 group-hover:block"
              >
                <Trash2 size={13} />
              </button>
            </div>
          ))}
        </div>
      </aside>

      <div className="flex flex-1 flex-col min-w-0">
        <div className="chat-toolbar border-b border-zinc-200/80 dark:border-zinc-800 text-sm">
          <button
            onClick={() => setConvDrawer(true)}
            title="Conversations"
            className="lg:hidden flex items-center rounded-xl border border-zinc-200/80 px-2.5 py-1.5 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400"
          >
            <Menu size={16} />
          </button>
          <div className="chat-tool-settings">
            <button
              onClick={() => setOrchestrate((v) => !v)}
              aria-pressed={orchestrate}
              title={orchestrate ? "Orchestration ON — the brain can dispatch workers (list_agents / run_agent / read_artifact). Click to turn off." : "Orchestration OFF — plain chat with the brain model. Click to let the brain dispatch workers."}
              className={`flex items-center gap-1.5 rounded-xl border px-2.5 py-1.5 text-xs transition-colors ${
                orchestrate
                  ? "border-indigo-500/40 bg-indigo-500/10 text-indigo-500"
                  : "border-zinc-200/80 text-zinc-400 hover:text-zinc-600 dark:border-zinc-800 dark:hover:text-zinc-300"
              }`}
            >
              <Network size={14} aria-hidden="true" /><span className="chat-orchestration-label">orchestrate: {orchestrate ? "on" : "off"}</span>
            </button>
            {brainLabel && (
              <span
                title={`Chat brain: ${brainLabel} — change it under Agents > Workers > Brain`}
                className="flex items-center gap-1.5 rounded-xl border border-zinc-200/80 px-2.5 py-1.5 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400"
              >
                <Brain size={14} className="text-indigo-500" /><span className="chat-brain-label">Brain</span>
              </span>
            )}
            {budget && budget.limit > 0 && (() => {
              const pct = Math.min(100, Math.round((budget.spent / budget.limit) * 100));
              return (
                <span
                  data-chat-budget=""
                  title={`Conversation token budget: ${budget.spent.toLocaleString()} / ${budget.limit.toLocaleString()} tokens (${pct}%)`}
                  className="flex items-center gap-1.5 rounded-xl border border-zinc-200/80 px-2.5 py-1.5 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400"
                >
                  <Gauge size={14} className={pct >= 90 ? "text-red-500" : pct >= 70 ? "text-amber-500" : "text-emerald-500"} />
                  <span className="tabular-nums">{pct}%</span>
                  <span className="h-1 w-14 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-700">
                    <span
                      className={`block h-full rounded-full ${pct >= 90 ? "bg-red-500" : pct >= 70 ? "bg-amber-500" : "bg-emerald-500"}`}
                      style={{ width: `${pct}%` }}
                    />
                  </span>
                </span>
              );
            })()}
          </div>
          {!rail.open && <DispatchReopen runs={runs} stale={dispatchStale} compact={!rail.desktop} onOpen={() => rail.setOpen(true)} />}
        </div>
        {active?.Kind === "team" && (
          <div className="border-b border-zinc-200/80 dark:border-zinc-800 bg-indigo-500/5 px-4 py-2">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="flex items-center gap-1.5 text-sm font-medium text-indigo-600 dark:text-indigo-400" title="Team conversation — each message assigns a task to the team; replies appear here via inbox polling">
                <Users size={15} /> team: {active.AgentName}
              </span>
              <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                {teamRoster.map((m) => (
                  <span key={m} className="rounded-lg border border-zinc-200/80 bg-white px-2 py-0.5 text-xs text-zinc-600 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-300">{m}</span>
                ))}
                {teamRoster.length === 0 && <span className="text-xs text-zinc-400">no roster yet</span>}
              </div>
            </div>
            {teamTyped.length > 0 && (
              <div className="mt-2 flex flex-wrap gap-2">
                {teamTyped.map((card) => (
                  <div key={String(card.msg_id)} className="rounded-xl border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-xs">
                    <div className="flex items-center gap-1.5 font-medium text-amber-600 dark:text-amber-400">
                      {String(card.kind || card.type) === "shutdown_request" ? <CirclePause size={13} /> : <FileText size={13} />}
                      {String(card.kind || card.type)} · from {String(card.sender || card.from || "?")}
                    </div>
                    <p className="mt-1 max-w-md whitespace-pre-wrap text-zinc-600 dark:text-zinc-300">{String(card.body || "")}</p>
                    {card.answered ? (
                      <span className="mt-1 inline-block text-zinc-400">answered</span>
                    ) : (
                      <div className="mt-1.5 flex gap-1.5">
                        <button
                          onClick={() => respondTyped(String(card.msg_id), "approve")}
                          disabled={!!card.pending}
                          className="rounded-lg bg-emerald-500/10 px-2 py-0.5 font-medium text-emerald-600 hover:bg-emerald-500/20 disabled:opacity-50 dark:text-emerald-400"
                        >approve</button>
                        <button
                          onClick={() => respondTyped(String(card.msg_id), "reject")}
                          disabled={!!card.pending}
                          className="rounded-lg bg-red-500/10 px-2 py-0.5 font-medium text-red-600 hover:bg-red-500/20 disabled:opacity-50 dark:text-red-400"
                        >reject</button>
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <button
                onClick={openSpecPanel}
                title="View / edit the team SPEC.md (shared conventions injected into every member turn)"
                className="ml-auto flex items-center gap-1 rounded-xl border border-zinc-200/80 px-2.5 py-1.5 text-xs text-zinc-500 hover:text-indigo-600 dark:border-zinc-800 dark:text-zinc-400 dark:hover:text-indigo-400"
              >
                <FileText size={13} /> SPEC
              </button>
              <button
                onClick={openTeamPanel}
                title="Manage members"
                className="flex items-center gap-1 rounded-xl border border-zinc-200/80 px-2.5 py-1.5 text-xs text-zinc-500 hover:text-indigo-600 dark:border-zinc-800 dark:text-zinc-400 dark:hover:text-indigo-400"
              >
                <Pencil size={13} /> members
              </button>
            </div>
            {teamPanelOpen && (
              <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={() => !teamPanelBusy && setTeamPanelOpen(false)}>
                <div className="w-full max-w-md rounded-2xl border border-zinc-200/80 bg-white p-5 shadow-lg dark:border-zinc-800 dark:bg-zinc-900" onClick={(e) => e.stopPropagation()}>
                  <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold"><Users size={16} /> Manage members — team: {active.AgentName}</h3>
                  <p className="mb-2 text-xs font-medium text-zinc-500">Current members ({teamRoster.length})</p>
                  <div className="mb-3 flex flex-wrap gap-1.5">
                    {teamRoster.map((m) => (
                      <span key={m} className="rounded-lg bg-zinc-100 px-2 py-0.5 text-xs dark:bg-zinc-800">{m}</span>
                    ))}
                    {teamRoster.length === 0 && <span className="text-xs text-zinc-400">no roster yet</span>}
                  </div>
                  <p className="mb-2 text-xs font-medium text-zinc-500">Workers — check to add to the team</p>
                  {teamAgentList.length === 0 && (
                    <p className="mb-2 text-xs text-amber-600 dark:text-amber-400">no workers configured — add them on the Dashboard Workers tab</p>
                  )}
                  <div className="mb-3 max-h-56 space-y-1 overflow-y-auto">
                    {teamAgentList.filter((a) => a.enabled !== false && !teamRoster.includes(String(a.agent_id || a.name || ""))).map((a) => {
                      const id = String(a.agent_id || a.name || "");
                      const on = teamPickAdd.includes(id);
                      return (
                        <label key={id} className="flex cursor-pointer items-center gap-2 rounded-xl px-2 py-1.5 text-sm hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60">
                          <input type="checkbox" checked={on} className="h-4 w-4 rounded border-zinc-300 text-indigo-600"
                            onChange={() => setTeamPicked(on ? teamPickAdd.filter((x) => x !== id) : [...teamPickAdd, id])} />
                          <span className="font-medium">{String(a.name || id)}</span>
                          <span className="text-xs text-zinc-500">{id}</span>
                        </label>
                      );
                    })}
                  </div>
                  <div className="flex justify-end gap-2">
                    <Button variant="outline" onClick={() => setTeamPanelOpen(false)} disabled={teamPanelBusy}>Cancel</Button>
                    <Button onClick={addTeamMembers} disabled={teamPickAdd.length === 0 || teamPanelBusy}>
                      {teamPanelBusy ? <Loader2 size={14} className="animate-spin" /> : <Plus size={14} />}
                      Add {teamPickAdd.length > 0 ? `${teamPicked.length} member${teamPicked.length > 1 ? "s" : ""}` : "members"}
                    </Button>
                  </div>
                </div>
              </div>
            )}
            {specPanelOpen && (
              <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={() => !specBusy && setSpecPanelOpen(false)}>
                <div className="w-full max-w-2xl rounded-2xl border border-zinc-200/80 bg-white p-5 shadow-lg dark:border-zinc-800 dark:bg-zinc-900" onClick={(e) => e.stopPropagation()}>
                  <h3 className="mb-1 flex items-center gap-2 text-sm font-semibold"><FileText size={16} /> Team SPEC — {active.AgentName}</h3>
                  <p className="mb-3 text-xs text-zinc-400">Shared conventions injected into every member turn; members also read the full file with file_read. Saving writes SPEC.md on the daemon.</p>
                  {specErr && <p className="mb-2 text-sm text-red-500">{specErr}</p>}
                  {specBusy && !specText ? (
                    <p className="mb-3 flex items-center gap-1.5 text-sm text-zinc-400"><Loader2 size={14} className="animate-spin" /> loading…</p>
                  ) : (
                    <Textarea rows={14} value={specText} onChange={(e) => setSpecText(e.target.value)}
                      className="font-mono text-xs" placeholder="# SPEC" />
                  )}
                  <div className="mt-3 flex justify-end gap-2">
                    <Button variant="outline" onClick={() => setSpecPanelOpen(false)} disabled={specBusy}>Cancel</Button>
                    <Button onClick={saveSpec} disabled={specBusy || !specText.trim()}>
                      {specBusy ? <Loader2 size={14} className="animate-spin" /> : <FileText size={14} />} Save SPEC
                    </Button>
                  </div>
                </div>
              </div>
            )}
          </div>
        )}


        <div ref={messageListRef} className="min-h-0 flex-1 overflow-y-auto p-4 space-y-3">
          {entries.length === 0 && (
            <div className="flex h-full items-center justify-center text-sm text-zinc-400">
              Send a message to chat — type @ to hand the task to a coding agent.
            </div>
          )}
          {entries.map((m, i) => (
            <div key={i} className={`flex ${m.role === "user" ? "justify-end" : "justify-start"}`}>
              <div
                className={`max-w-[85%] rounded-2xl px-4 py-2.5 text-sm shadow-sm ${
                  m.role === "user"
                    ? "bg-indigo-600 text-white"
                    : "bg-white dark:bg-zinc-900 border border-zinc-200/80 dark:border-zinc-800"
                }`}
              >
                {m.role === "user" ? (
                  <p className="whitespace-pre-wrap">{m.content}</p>
                ) : (
                  <div className="md">
                    <ReactMarkdown remarkPlugins={[remarkGfm]} components={chatMarkdownComponents}>{m.content || "…"}</ReactMarkdown>
                  </div>
                )}
                {m.meta && (() => {
                  try {
                    const j = JSON.parse(m.meta);
                    // Steer bubble receipt line (issue #45 F3).
                    if (j.steer) {
                      const label =
                        j.receipt === "pending" ? "steer · pending…"
                        : j.receipt === "delivered" ? `steer · ✓ delivered (${j.steers_used ?? "?"}/3)`
                        : j.receipt === "delivered_as_inbox" ? "steer · ✓ delivered as inbox (run already finished)"
                        : j.receipt === "failed" ? "steer · failed"
                        : `steer · ${j.receipt}`;
                      return (
                        <p className={`mt-1 text-xs ${j.receipt === "failed" ? "text-red-500" : "text-zinc-400"}`}>
                          {label}
                          {j.error ? ` — ${j.error}` : ""}
                        </p>
                      );
                    }
                    return (
                      <p className="mt-1 text-xs text-zinc-400">
                        {j.cost_usd ? `$${Number(j.cost_usd).toFixed(4)} · ` : ""}
                        {j.tokens_in ? `${j.tokens_in}→${j.tokens_out} tok` : ""}
                        {j.model ? ` · ${j.model}` : ""}
                        {j.task_id && active?.Kind === "team" && !teamRunsHidden && (
                          <button
                            className="ml-1 text-indigo-500 hover:underline"
                            onClick={() => setRunDrawerTask(String(j.task_id))}
                          >
                            · View run
                          </button>
                        )}
                      </p>
                    );
                  } catch { return null; }
                })()}
              </div>
            </div>
          ))}
          {busy && (
            <div className="flex items-center gap-2 px-4 py-2 text-xs text-zinc-400">
              <Loader2 size={14} className="animate-spin text-indigo-500" /> working…
            </div>
          )}
          {!busy && workingWorkers.length > 0 && (
            <div className="flex items-center gap-2 px-4 py-2 text-xs text-zinc-400">
              <Loader2 size={14} className="animate-spin text-indigo-500" />
              {workingWorkers.length} agent{workingWorkers.length > 1 ? "s" : ""} working in background: {workingWorkers.join(", ")}
            </div>
          )}
        </div>

        {approval && (
          <div className="flex flex-wrap items-center gap-3 border-t border-amber-500/30 bg-amber-500/5 px-4 py-2 text-sm">
            <span className="flex-1 min-w-40 text-zinc-700 dark:text-zinc-300">
              Dispatch awaiting approval: <strong>{approval.detail}</strong>
            </span>
            <Button onClick={() => resolveApproval(true)}>Approve</Button>
            <Button variant="outline" onClick={() => resolveApproval(false)}>Deny</Button>
          </div>
        )}

        {resumeNote && (
          <p role="status" className="flex items-center gap-1.5 px-4 pb-1 text-sm text-amber-600 dark:text-amber-400">
            <Loader2 size={14} className="shrink-0 animate-spin" />
            {resumeNote}
          </p>
        )}
        {err && <p className="px-4 pb-1 text-sm text-red-500">{err}</p>}

        <div className="relative border-t border-zinc-200/80 dark:border-zinc-800 p-3">
          {mentionOptions.length > 0 && (
            <div className="absolute bottom-full left-3 right-3 mb-1 max-h-48 overflow-y-auto rounded-xl border border-zinc-200/80 bg-white p-1 shadow-md shadow-zinc-900/5 dark:border-zinc-800 dark:bg-zinc-900">
              {mentionOptions.map((a) => (
                <button key={a.name} onClick={() => insertMention(a.name)}
                  className="flex w-full items-center gap-2 rounded-lg px-3 py-1.5 text-sm hover:bg-zinc-100 dark:hover:bg-zinc-800">
                  <Bot size={14} className="text-indigo-500" /> @{a.name}
                  {a.is_worker && <span className="text-xs text-zinc-400">worker · {a.agent_type}</span>}
                </button>
              ))}
            </div>
          )}
          {attachments.length > 0 && (
            <div className="mb-2 flex flex-wrap gap-1.5">
              {attachments.map((a, i) => (
                <span key={a.stored + i} className="flex items-center gap-1 rounded-lg bg-zinc-100 px-2 py-0.5 text-xs text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                  <Paperclip size={10} /> {a.name}
                  <button className="ml-0.5 text-zinc-400 hover:text-red-500"
                    onClick={() => setAttachments((arr) => arr.filter((_, j) => j !== i))}>×</button>
                </span>
              ))}
            </div>
          )}
          <div className="flex gap-2">
            <input ref={fileInputRef} type="file" multiple className="hidden" onChange={(e) => onFiles(e.target.files)} />
            <Button variant="outline" className="self-end px-2.5" onClick={pickFiles} disabled={uploading}
              title="Attach files (text files are inlined into the message)">
              {uploading ? <Loader2 size={16} className="animate-spin" /> : <Paperclip size={16} />}
            </Button>
            <Textarea
              rows={2}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                // IME safety: while the input method is composing (e.g.
                // typing English inside a Chinese IME), Enter confirms the
                // composition — it must never send the message.
                if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return;
                if (mentionOptions.length > 0 && (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey))) {
                  e.preventDefault();
                  insertMention(mentionOptions[0].name);
                  return;
                }
                if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); }
              }}
              placeholder={
                listening ? "listening…"
                : active?.Kind === "team" && teamRunning ? `[STEER] into ${teamRunning.title.slice(0, 40) || teamRunning.task_id}…`
                : "Message…  (@ to hand off to an agent)"
              }
            />
            <div className="flex flex-col gap-2">
              <Button onClick={send} disabled={busy || !input.trim()}
                title={mentionedAgent ? `Run with ${mentionedAgent.name}` : "Send as chat"}>
                {mentionedAgent ? <Bot size={16} /> : <Send size={16} />}
              </Button>
              <Button variant="outline" className="px-2.5" onClick={toggleVoice}
                disabled={!voiceSupported}
                title={voiceSupported ? (listening ? "Stop dictation" : "Voice input (browser speech)") : "Voice input needs Chrome/Edge (fusion-speech integration pending)"}>
                <Mic size={16} className={listening ? "animate-pulse text-red-500" : ""} />
              </Button>
            </div>
          </div>
          {mentionedAgent?.is_worker && (
            <label className="mt-2 flex items-center gap-2 text-xs text-zinc-500">
              <input key={mentionedAgent.name} type="checkbox" ref={requiresRepo} /> Requires Git repository
            </label>
          )}
        </div>
      </div>
      {teamModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={() => !teamCreating && setTeamModalOpen(false)}>
          <div className="w-full max-w-md rounded-2xl border border-zinc-200/80 bg-white p-5 shadow-lg dark:border-zinc-800 dark:bg-zinc-900" onClick={(e) => e.stopPropagation()}>
            <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold"><Users size={16} /> New team</h3>
            <Input className="mb-3" placeholder="team name" value={teamName} onChange={(e) => setTeamName(e.target.value)} autoFocus />
            <div className="mb-3 grid grid-cols-2 gap-2">
              <label className="flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
                lead budget
                <input type="number" value={teamLeadBudget} min={1} onChange={(e) => setTeamLeadBudget(parseInt(e.target.value) || 0)}
                  className="w-full rounded-lg border border-zinc-200 bg-transparent px-2 py-1 text-xs dark:border-zinc-700"
                  title="Lead budget (tokens). Kernel rule: every member budget must stay <= this (Deadline.child semantics)" />
              </label>
              <label className="flex cursor-pointer items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400" title="Kernel Playbook ops — renders the playbook panel on the Team page">
                <input type="checkbox" checked={teamPlaybook} onChange={(e) => setTeamPlaybook(e.target.checked)}
                  className="h-4 w-4 rounded border-zinc-300 text-indigo-600" />
                playbook
              </label>
            </div>
            <label className="mb-3 flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
              default permission mode
              <Select className="w-40" value={teamMode} onChange={(e) => setTeamMode(e.target.value)}
                title="Applied to members without an explicit per-member override (console#79 R1: override per member below)">
                <option value="default">default — ask on each tool</option>
                <option value="approved">approved — auto-accept edits</option>
                <option value="bypass">bypass — no prompts</option>
                <option value="plan">plan — read-only</option>
              </Select>
            </label>
            <div className="mb-3">
              <button
                onClick={() => setTeamSpecOpen(!teamSpecOpen)}
                className="flex items-center gap-1 text-xs font-medium text-zinc-500 hover:text-indigo-600 dark:text-zinc-400 dark:hover:text-indigo-400"
              >
                {teamSpecOpen ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                Initial SPEC (optional)
              </button>
              {teamSpecOpen && (
                <>
                  <p className="mb-1 mt-1 text-xs text-zinc-400">Team conventions seeded into SPEC.md at creation — role boundaries, interfaces, delivery norms. Members read it every turn.</p>
                  <Textarea rows={4} value={teamSpec} onChange={(e) => setTeamSpec(e.target.value)}
                    placeholder="# SPEC&#10;## Roles&#10;## Conventions&#10;## Interfaces" />
                </>
              )}
            </div>
            <p className="mb-2 text-xs font-medium text-zinc-500">Members — pick from configured workers</p>
            {teamWorkers.filter((w) => w.enabled).length === 0 && (
              <p className="mb-2 text-xs text-amber-600 dark:text-amber-400">no workers configured — add one on the Agents page first</p>
            )}
            <div className="mb-3 max-h-56 space-y-1 overflow-y-auto">
              {teamWorkers.filter((w) => w.enabled).map((w) => {
                const wn = w.name || w.agent;
                const on = teamPicked.includes(wn);
                const cfg = memberCfg(wn);
                return (
                  <div key={wn}>
                    <label className="flex cursor-pointer items-center gap-2 rounded-xl px-2 py-1.5 text-sm hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60">
                      <input type="checkbox" checked={on} className="h-4 w-4 rounded border-zinc-300 text-indigo-600"
                        onChange={() => setTeamPicked(on ? teamPicked.filter((x) => x !== wn) : [...teamPicked, wn])} />
                      <span className="font-medium">{wn}</span>
                      <span className="text-xs text-zinc-500">{w.agent}{w.model ? ` · ${w.model}` : ""}</span>
                    </label>
                    {on && (
                      <div className="mb-1 ml-6 rounded-xl border border-zinc-200/70 bg-zinc-100/60 p-2 dark:border-zinc-800 dark:bg-zinc-800/40">
                        <div className="flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
                          <span>budget</span>
                          <input type="number" value={cfg.budget} min={1}
                            onChange={(e) => setMemberCfg(wn, { budget: parseInt(e.target.value) || 0 })}
                            className={`w-24 rounded-lg border bg-transparent px-2 py-0.5 text-xs ${cfg.budget > teamLeadBudget ? "border-red-400 text-red-500" : "border-zinc-200 dark:border-zinc-700"}`}
                            title={`Must stay <= lead budget ${teamLeadBudget} (kernel TeamDefError)`} />
                          <span>mode</span>
                          <select value={cfg.mode} onChange={(e) => setMemberCfg(wn, { mode: e.target.value })}
                            className="rounded-lg border border-zinc-200 bg-transparent px-1.5 py-0.5 text-xs dark:border-zinc-700">
                            <option value="default">default</option>
                            <option value="approved">approved</option>
                            <option value="bypass">bypass</option>
                            <option value="plan">plan</option>
                          </select>
                        </div>
                        <div className="mt-1.5 flex flex-wrap gap-1" title="Tool allowlist (kernel TeamMember.tool_allowlist). file_read kept by default (issue #41)">
                          {TEAM_TOOLS.map((t) => {
                            const on = cfg.tools.includes(t);
                            return (
                              <button key={t} type="button"
                                onClick={() => setMemberCfg(wn, { tools: on ? cfg.tools.filter((x) => x !== t) : [...cfg.tools, t] })}
                                className={`rounded-md border px-1.5 py-0.5 text-[10px] ${on ? "border-indigo-400/60 bg-indigo-500/15 text-indigo-600 dark:text-indigo-300" : "border-zinc-300 text-zinc-400 dark:border-zinc-700"}`}>
                                {t}
                              </button>
                            );
                          })}
                        </div>
                        {teamSkills.length > 0 && (
                          <div className="mt-1.5 flex flex-wrap gap-1" title="Skills preload — SKILL.md bodies are injected into the member system prompt (agent-studio #482); the list is also the persona allowlist (#707)">
                            {teamSkills.map((s) => {
                              const on = cfg.skills.includes(s.name);
                              return (
                                <button key={s.name} type="button"
                                  onClick={() => setMemberCfg(wn, { skills: on ? cfg.skills.filter((x) => x !== s.name) : [...cfg.skills, s.name] })}
                                  className={`rounded-md border px-1.5 py-0.5 text-[10px] ${on ? "border-violet-400/60 bg-violet-500/15 text-violet-600 dark:text-violet-300" : "border-zinc-300 text-zinc-400 dark:border-zinc-700"}`}>
                                  skill:{s.name}
                                </button>
                              );
                            })}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
            {/* console#79 R3: kernel #124 fan-out ladder — client-side meter,
                validate_brief hard-errors above the hard max server-side. */}
            {teamPicked.length > 0 && (
              <div className={`mb-3 rounded-xl border px-3 py-2 ${teamPicked.length > TEAM_FANOUT.hardMax ? "border-red-400/40 bg-red-500/10" : teamPicked.length > TEAM_FANOUT.simpleMax ? "border-amber-400/30 bg-amber-400/5" : "border-emerald-400/30 bg-emerald-400/5"}`}>
                <div className="mb-1 flex items-center justify-between text-[11px]">
                  <span className={teamPicked.length > TEAM_FANOUT.hardMax ? "text-red-500 dark:text-red-400" : teamPicked.length > TEAM_FANOUT.simpleMax ? "text-amber-600 dark:text-amber-400" : "text-emerald-600 dark:text-emerald-400"}>
                    fan-out {teamPicked.length} / hard max {TEAM_FANOUT.hardMax}
                  </span>
                  {teamPicked.length > TEAM_FANOUT.simpleMax && (
                    <span className="text-[10px] text-zinc-500">multi-agent costs ~15× single-chat tokens</span>
                  )}
                </div>
                <div className="flex h-1.5 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
                  <div className="h-full bg-emerald-500/70" style={{ width: `${Math.min(teamPicked.length, TEAM_FANOUT.simpleMax) * 5}%` }} />
                  <div className="h-full bg-amber-500/70" style={{ width: `${Math.max(0, Math.min(teamPicked.length, TEAM_FANOUT.complexMax) - TEAM_FANOUT.simpleMax) * 5}%` }} />
                  <div className="h-full bg-red-500/70" style={{ width: `${Math.max(0, teamPicked.length - TEAM_FANOUT.complexMax) * 5}%` }} />
                </div>
              </div>
            )}
            {/* console#79 R6: topology preset + advanced max_depth. The preset
                rides team metadata for the kernel topology scheduler (opt-in
                upstream, #101 phase 2); hub matches the broadcast default. */}
            <div className="mb-3 flex flex-wrap items-center gap-3 text-xs text-zinc-500 dark:text-zinc-400">
              <span className="flex items-center gap-1.5" title="Communication topology preset stored in team metadata — hub (default) broadcasts through the lead">
                topology
                <select value={teamTopology} onChange={(e) => setTeamTopology(e.target.value)}
                  className="rounded-lg border border-zinc-200 bg-transparent px-1.5 py-0.5 text-xs dark:border-zinc-700">
                  <option value="hub">hub</option>
                  <option value="chain">chain</option>
                  <option value="mesh">mesh</option>
                </select>
              </span>
              <span className="flex items-center gap-1.5" title="Kernel TeamDefinition.max_depth — how deep subteams may nest">
                max depth
                <input type="number" value={teamMaxDepth} min={1} max={10}
                  onChange={(e) => setTeamMaxDepth(Math.max(1, parseInt(e.target.value) || 1))}
                  className="w-16 rounded-lg border border-zinc-200 bg-transparent px-2 py-0.5 text-xs dark:border-zinc-700" />
              </span>
            </div>
            {teamErr && <p className="mb-2 text-sm text-red-500">{teamErr}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setTeamModalOpen(false)} disabled={teamCreating}>Cancel</Button>
              <Button onClick={createTeam} disabled={teamCreating}>
                <Rocket size={14} /> {teamCreating ? "Creating…" : "Create team"}
              </Button>
            </div>
          </div>
        </div>
      )}
      <DispatchPanel
        activity={brainActivity && brainActivity.conversation === active?.ID ? brainActivity : null}
        runs={runs ?? []} loaded={runs !== null} stale={dispatchStale} convs={convs} busyWorkers={busyWorkers}
        onForget={forgetRuns} onOpen={c => { if (!rail.desktop) rail.setOpen(false); openConv(c); }}
        desktop={rail.desktop} open={rail.open} onClose={() => rail.setOpen(false)}
      />
      {runDrawerTask && active?.Kind === "team" && (
        <RunDrawer
          team={active.AgentName}
          taskId={runDrawerTask}
          onClose={() => setRunDrawerTask(null)}
          onUnavailable={() => setTeamRunsHidden(true)}
        />
      )}
    </div>
  );
}

// DispatchPanel is the right rail of the chat page: every background run
// (run_agent background=true) grouped into running / queued / finished.
// Cards open live run details, with a link back to the originating conversation.
function DispatchPanel({ activity, runs, loaded, stale, convs, busyWorkers, onForget, onOpen, desktop, open, onClose }: {
  activity: BrainActivity | null; runs: DispatchRun[]; loaded: boolean; stale: boolean;
  convs: Conversation[]; busyWorkers: WorkerStatus[]; onForget: (id?: number) => void;
  onOpen: (c: Conversation) => void; desktop: boolean; open: boolean; onClose: () => void;
}) {
  const running = runs.filter((r) => r.status === "running");
  const queued = runs.filter((r) => r.status === "queued");
  // Worker status has no run ID. Only attach a live tool report to a unique
  // match; otherwise keep the original independent worker strip.
  const liveByRun = new Map<number, WorkerStatus>();
  const independentWorkers = busyWorkers.filter(worker => {
    const matches = running.filter(run => run.worker === worker.name && run.task === worker.task);
    if (matches.length !== 1) return true;
    liveByRun.set(matches[0].id, worker);
    return false;
  });
  const finished = runs.filter((r) => r.status === "done" || r.status === "blocked" || r.status === "error").slice(0, 8);
  // copyRun puts the full card content on the clipboard for bug reports —
  // one click, no manual text selection across truncated lines.
  const copyRun = async (r: DispatchRun | WorkerStatus, kind: "run" | "worker") => {
    const lines = kind === "run"
      ? [`run #${(r as DispatchRun).id} (${(r as DispatchRun).status})`, `worker: ${(r as DispatchRun).worker} (${(r as DispatchRun).agent})`,
         `task: ${(r as DispatchRun).task}`, `elapsed: ${(r as DispatchRun).elapsed_s}s · cost: $${(r as DispatchRun).cost_usd.toFixed(4)}`,
         (r as DispatchRun).detail ? `detail: ${(r as DispatchRun).detail}` : "",
         (r as DispatchRun).error ? `error: ${(r as DispatchRun).error}` : "",
         (r as DispatchRun).result ? `result: ${(r as DispatchRun).result}` : ""]
      : [`worker: ${(r as WorkerStatus).name}`, `task: ${(r as WorkerStatus).task || ""}`,
         `detail: ${(r as WorkerStatus).detail || ""}`, `elapsed: ${(r as WorkerStatus).elapsed_s ?? "?"}s`];
    try {
      await navigator.clipboard.writeText(lines.filter(Boolean).join("\n"));
      setCopied(kind === "run" ? `run:${(r as DispatchRun).id}` : `worker:${(r as WorkerStatus).name}`);
      setTimeout(() => setCopied(""), 1200);
    } catch (e) {
      console.error("copy failed", e);
    }
  };
  const [copied, setCopied] = useState("");
  const copyBtn = (key: string, onClick: () => void) => (
    <button onClick={(e) => { e.stopPropagation(); onClick(); }}
      className="shrink-0 rounded-md p-0.5 text-zinc-400 hover:bg-zinc-200/60 hover:text-zinc-600 dark:hover:bg-zinc-700 dark:hover:text-zinc-300"
      title="Copy card details">
      {copied === key ? <CheckCircle size={12} className="text-emerald-500" /> : <Copy size={12} />}
    </button>
  );
  const clearFinished = async () => {
    try {
      await api.clearDispatches();
      onForget();
    } catch { /* keep current view */ }
  };
  const dismissRun = async (id: number) => {
    try {
      await api.dismissDispatch(id);
      onForget(id);
    } catch { /* keep current view */ }
  };
  // Expanded card view: a fixed overlay showing the full task / detail /
  // error / result of one run — cards in the rail stay a fixed small size.
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const expanded = runs.find((r) => r.id === expandedId);
  const conversation = expanded && convs.find((c) => c.ID === expanded.conversation_id);
  const row = (r: DispatchRun) => (
    <DispatchCard key={r.id} run={r} liveDetail={liveByRun.get(r.id)?.detail} onExpand={() => setExpandedId(r.id)} actions={<>
        {copyBtn(`run:${r.id}`, () => copyRun(r, "run"))}
        {(r.status === "error" || r.status === "blocked") && (
          <button onClick={(e) => {
            e.stopPropagation();
            api.agentRun({ conversation_id: r.conversation_id, agent: r.worker || r.agent, task: r.task, workdir: "", background: true, requires_repo: r.requires_repo, repo: r.repo })
              .catch(() => {});
          }}
            className="shrink-0 rounded-md p-0.5 text-zinc-400 hover:bg-zinc-200/60 hover:text-indigo-500 dark:hover:bg-zinc-700"
            title="Re-dispatch the same task (one-click re-deliver)">
            <RotateCcw size={12} />
          </button>
        )}
        {(r.status === "done" || r.status === "blocked" || r.status === "error") && (
          <button onClick={(e) => { e.stopPropagation(); dismissRun(r.id); }}
            className="shrink-0 rounded-md p-0.5 text-zinc-400 hover:bg-zinc-200/60 hover:text-red-500 dark:hover:bg-zinc-700"
            title="Dismiss this card">
            <X size={12} />
          </button>
        )}
    </>} />
  );
  return (
    <>
      <DispatchRail desktop={desktop} open={open} onClose={onClose} headerActions={finished.length > 0 && (
        <Button variant="ghost" className="px-2 py-1 text-xs" onClick={clearFinished} title="Remove all finished cards">Clear</Button>
      )}>
        <BrainCard activity={activity} runningCount={running.length} />
        {(!loaded || stale) && <p className="px-1 py-2 text-xs text-zinc-500" role="status">
          {stale ? "任务状态暂时无法刷新" : "正在读取任务状态…"}
        </p>}
        <div className="flex-1 space-y-2 overflow-y-auto">
          {/* Agent status strip: every worker with a live agent process, even
              one recovered after a console restart (registry lost, process not). */}
          {independentWorkers.length > 0 && (
            <div className="space-y-1.5">
              {independentWorkers.map((w) => (
                <div key={w.name} className="select-text rounded-xl border border-indigo-500/30 bg-indigo-500/5 px-2.5 py-2">
                  <div className="flex items-center gap-2">
                    <Loader2 size={12} className="shrink-0 animate-spin text-indigo-500" />
                    <span className="truncate text-xs font-medium">{w.name}</span>
                    <span className="ml-auto shrink-0 text-[11px] text-indigo-600 dark:text-indigo-400">
                      {typeof w.elapsed_s === "number" ? `${w.elapsed_s}s` : "working"}
                    </span>
                    {copyBtn(`worker:${w.name}`, () => copyRun(w, "worker"))}
                  </div>
                  {w.task && <p className="mt-0.5 break-words text-[11px] text-zinc-500 dark:text-zinc-400" title={w.task}>{w.task}</p>}
                  {w.detail && <p className="break-words text-[11px] text-zinc-400" title={w.detail}>{w.detail}</p>}
                </div>
              ))}
            </div>
          )}
          {running.map(row)}
          {queued.map(row)}
          {finished.map(row)}
          {loaded && runs.length === 0 && busyWorkers.length === 0 && (
            <p className="px-1 pt-4 text-center text-xs text-zinc-400">
              No background runs yet. The brain dispatches with background=true for long tasks.
            </p>
          )}
        </div>
        <p className="border-t border-zinc-200/80 px-1 pt-2 text-[11px] leading-4 text-zinc-400 dark:border-zinc-800">
          Finished runs post their outcome into the conversation automatically.
        </p>
      </DispatchRail>
      {expanded && (
        <DispatchDetails run={expanded} liveDetail={liveByRun.get(expanded.id)?.detail} onClose={() => setExpandedId(null)}
          onConversation={conversation ? () => { setExpandedId(null); onOpen(conversation); } : undefined}
        />
      )}
    </>
  );
}

function GatewayPage() {
  const [tab, setTab] = useState<"gateway" | "models">("gateway");
  const [status, setStatus] = useState<Awaited<ReturnType<typeof api.gatewayStatus>> | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [adminUser, setAdminUser] = useState("");
  const [adminPass, setAdminPass] = useState("");
  const [keys, setKeys] = useState<GatewayKey[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [brainModel, setBrainModel] = useState("");
  const [gwModels, setGwModels] = useState<GatewayModelItem[]>([]);

  const load = useCallback(() => {
    api.gatewayStatus().then(setStatus).catch((e) => setErr((e as Error).message));
  }, []);
  useEffect(() => { load(); }, [load]);

  const loadKeys = useCallback(() => {
    api.gatewayKeys().then((r) => setKeys(r.keys ?? [])).catch(() => setKeys([]));
  }, []);
  const loadBrainModel = useCallback(() => {
    api.providers().then((r) => {
      setProviders(r.providers ?? []);
      const gw = (r.providers ?? []).find((p) => p.type === "gateway" && p.enabled);
      if (gw) setBrainModel(gw.model || "");
    }).catch(() => {});
    api.gatewayModels().then((r) => setGwModels(r.items ?? [])).catch(() => setGwModels([]));
  }, []);
  useEffect(() => {
    if (!status?.reachable) return;
    loadKeys();
    loadBrainModel();
  }, [status?.reachable, loadKeys, loadBrainModel]);

  const install = async () => {
    setBusy(true);
    setMsg("");
    setErr("");
    try {
      const r = await api.gatewayInstall();
      setMsg(`${r.note} (v${r.version}, ${r.binary})`);
      load();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const saveAdminCreds = async () => {
    setMsg("");
    setErr("");
    try {
      await api.storeApiKey("gateway-admin-user", adminUser);
      await api.storeApiKey("gateway-admin-pass", adminPass);
      setMsg("Gateway admin credentials saved (OS keychain).");
      setAdminPass("");
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const openAdmin = async () => {
    setMsg("");
    setErr("");
    try {
      await api.gatewaySSO();
    } catch (ex) {
      setErr(`Gateway admin auto-login failed: ${(ex as Error).message}`);
    } finally {
      window.open(status?.admin_url, "_blank");
    }
  };

  // One click: gateway admin login + key creation; raw key shown once.
  const [rawKey, setRawKey] = useState("");
  const [keyCopied, setKeyCopied] = useState(false);
  const provisionKey = async () => {
    setBusy(true); setMsg(""); setErr(""); setRawKey(""); setKeyCopied(false);
    try {
      const r = await api.provisionGatewayKey();
      setRawKey(r.raw_key || "");
      setMsg(`gateway API key ready (${r.key_prefix}...) — stored in the OS keychain`);
      loadKeys();
      loadBrainModel();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const copyKey = async () => {
    try {
      await navigator.clipboard.writeText(rawKey);
      setKeyCopied(true);
    } catch {
      setKeyCopied(false);
    }
  };
  const deleteKey = async (name: string) => {
    setMsg(""); setErr("");
    try {
      await api.deleteGatewayKey(name);
      setMsg(`API key "${name}" deleted`);
      loadKeys();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const deleteProvider = async (p: Provider) => {
    setMsg(""); setErr("");
    try {
      await api.deleteProvider(p.id);
      setMsg(`provider "${p.id}" deleted from console config`);
      loadBrainModel();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const gwStart = async () => {
    setBusy(true); setMsg(""); setErr("");
    try {
      const r = await api.gatewayStart();
      setMsg(`gateway start: ${r.note || "ok"}`);
      load();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally { setBusy(false); }
  };
  const gwStop = async () => {
    setBusy(true); setMsg(""); setErr("");
    try {
      const r = await api.gatewayStop();
      setMsg(`gateway stop: ${r.note || "ok"}`);
      load();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally { setBusy(false); }
  };

  const tabBar = (
    <div className="flex gap-2">
      {(
        [
          ["gateway", "Gateway"],
          ["models", "Models"],
        ] as const
      ).map(([id, label]) => (
        <button
          key={id}
          onClick={() => setTab(id)}
          className={`rounded-xl border px-3 py-1.5 text-sm transition-colors ${
            tab === id
              ? "border-indigo-300 bg-indigo-50 text-indigo-600 dark:border-indigo-500/40 dark:bg-indigo-500/10 dark:text-indigo-300"
              : "border-zinc-200/80 text-zinc-500 hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-400 dark:hover:bg-zinc-800/60"
          }`}
        >
          {label}
        </button>
      ))}
    </div>
  );

  // Models live under the Gateway tab — full-width page of its own.
  if (tab === "models") {
    return (
      <div>
        <div className="mx-auto max-w-4xl px-6 pt-6">{tabBar}</div>
        <ModelsPage />
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-3xl space-y-4 p-6">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Gateway</h2>
        {tabBar}
      </div>
      <Card className="space-y-3 p-4">
        <div className="flex items-center justify-between">
          <div>
            <p className="font-medium flex items-center gap-2">
              <Network size={16} /> {status?.base_url || "…"}
              {status?.reachable ? <Badge tone="ok">running</Badge> : <Badge tone="warn">not reachable</Badge>}
            </p>
            <p className="text-xs text-zinc-500 mt-1">
              {status?.installed ? `installed: ${status.binary}` : "binary not installed by console"}
            </p>
          </div>
          <span className="ml-auto flex gap-1.5">
            {status?.reachable ? (
              <>
                <Button onClick={openAdmin}>
                  <ExternalLink size={14} /> Open Admin GUI
                </Button>
                <Button variant="outline" onClick={gwStop} disabled={busy}>Stop</Button>
              </>
            ) : (
              status?.installed && (
                <Button onClick={gwStart} disabled={busy}>Start</Button>
              )
            )}
          </span>
        </div>
      </Card>

      {status?.reachable && (
        <Card className="space-y-3 p-4">
          <h3 className="font-medium">Gateway models</h3>
          <GatewayBackendsPanel compact />
        </Card>
      )}

      {status?.reachable && gwModels.length > 0 && (
        <Card className="space-y-3 p-4">
          <h3 className="font-medium flex items-center gap-2"><Cpu size={16} /> Brain model</h3>
          <p className="text-sm text-zinc-500">
            The gateway provider's default model: <span className="font-medium text-zinc-700 dark:text-zinc-200">{brainModel || "not set"}</span>.
            Chat reads it per conversation — change the default in the Gateway tab {'>'} Models (single owner
            of provider.Model), or set the brain provider/model under Agents {'>'} Workers {'>'} Brain.
            Cloud models are pinned to their backend via the qualified <code>backend/model</code> id.
          </p>
        </Card>
      )}

      {!status?.reachable && (
        <Card className="space-y-3 p-4">
          <h3 className="font-medium flex items-center gap-2"><Download size={16} /> Install gateway</h3>
          <p className="text-sm text-zinc-500">
            Downloads the latest <a className="underline" href="https://github.com/dahai80/fusion-gateway/releases" target="_blank" rel="noreferrer">fusion-gateway release</a> for this
            host (detected automatically) into <code>~/.fusion/fusion-console/bin/</code>. Then start it with your
            gateway <code>config.yaml</code> and refresh this page.
          </p>
          <Button onClick={install} disabled={busy}>
            {busy ? "Downloading…" : "Download & install"}
          </Button>
        </Card>
      )}

      {status?.reachable && (
        <Card className="space-y-3 p-4">
          <h3 className="font-medium flex items-center gap-2"><KeyRound size={16} /> Gateway admin login</h3>
          <p className="text-sm text-zinc-500">
            Save the gateway admin account here once — "Open Admin GUI" then signs in automatically,
            no second login. Credentials live in the OS keychain.
          </p>
          <div className="flex flex-wrap gap-2">
            <input
              className="w-40 rounded-lg border border-zinc-200/80 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900"
              placeholder="admin username"
              value={adminUser}
              onChange={(e) => setAdminUser(e.target.value)}
            />
            <input
              className="w-40 rounded-lg border border-zinc-200/80 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900"
              type="password"
              placeholder="admin password"
              value={adminPass}
              onChange={(e) => setAdminPass(e.target.value)}
            />
            <Button onClick={saveAdminCreds} disabled={!adminUser || !adminPass}>
              Save credentials
            </Button>
          </div>
        </Card>
      )}

      {status?.reachable && (
        <Card className="space-y-3 p-4">
          <h3 className="font-medium flex items-center gap-2"><KeyRound size={16} /> API key</h3>
          <p className="text-sm text-zinc-500">
            One click — the console logs into the gateway admin and issues an API key, stored in the
            OS keychain (<code>gateway-key</code>). Chat, brain and workers all use this key against
            the gateway.
          </p>
          <Button onClick={provisionKey} disabled={busy}>
            {busy ? "Creating..." : "Create API key"}
          </Button>
          {rawKey && (
            <div className="space-y-2 rounded-xl border border-amber-300 bg-amber-50 p-3 dark:border-amber-500/30 dark:bg-amber-500/5">
              <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
                Copy and save this key now — it is shown only once (the console keeps its copy in the OS keychain).
              </p>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded-lg bg-white px-2 py-1.5 font-mono text-xs dark:bg-zinc-900">{rawKey}</code>
                <Button variant="outline" onClick={copyKey} disabled={keyCopied}>
                  {keyCopied ? "copied" : "Copy"}
                </Button>
              </div>
            </div>
          )}
          {keys.length > 0 && (
            <div className="space-y-1">
              <p className="text-xs font-medium text-zinc-500">API keys in the gateway</p>
              {keys.map((k) => (
                <div key={k.name} className="flex items-center gap-2 text-xs">
                  <span className="font-medium">{k.name}</span>
                  {k.key_prefix && <span className="text-zinc-400">{k.key_prefix}…</span>}
                  {k.in_use ? (
                    <span className="ml-auto rounded-lg px-2 py-0.5 text-zinc-300 dark:text-zinc-600" title="This key is used by the console itself (chat, brain, workers). Deleting it would break the console.">
                      in use
                    </span>
                  ) : (
                    <button onClick={() => deleteKey(k.name)} title={`Delete API key ${k.name}`}
                      className="ml-auto rounded-lg px-2 py-0.5 text-zinc-400 hover:bg-zinc-100 hover:text-red-500 dark:hover:bg-zinc-800">
                      delete
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </Card>
      )}

      {providers.length > 0 && (
        <Card className="space-y-3 p-4">
          <h3 className="font-medium flex items-center gap-2"><Cpu size={16} /> Console providers</h3>
          <p className="text-sm text-zinc-500">
            Provider entries in the console <code>config.json</code>. The <code>gateway</code> entry
            is managed automatically. Old direct entries (openai/anthropic) whose API key is not in
            this machine's keychain can be deleted here.
          </p>
          <div className="space-y-1">
            {providers.map((p) => (
              <div key={p.id} className="flex items-center gap-2 text-xs">
                <span className="font-medium">{p.id}</span>
                <Badge>{p.type}</Badge>
                {!p.enabled && <Badge>disabled</Badge>}
                <span className="min-w-0 truncate text-zinc-400">{p.model || p.base_url}</span>
                {p.type === "gateway" ? (
                  <span className="ml-auto rounded-lg px-2 py-0.5 text-zinc-300 dark:text-zinc-600" title="Managed by the console — chat, brain and workers all use it.">
                    managed
                  </span>
                ) : (
                  <button onClick={() => deleteProvider(p)} title={`Delete provider ${p.id}`}
                    className="ml-auto rounded-lg px-2 py-0.5 text-zinc-400 hover:bg-zinc-100 hover:text-red-500 dark:hover:bg-zinc-800">
                    delete
                  </button>
                )}
              </div>
            ))}
          </div>
        </Card>
      )}

      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
    </div>
  );
}

