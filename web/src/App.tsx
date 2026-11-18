import { useCallback, useEffect, useRef, useState } from "react";
import {
  Bot,
  CheckCircle,
  Clock,
  Copy,
  Menu,
  PanelRight,
  Cpu,
  Brain,
  ChevronDown,
  XCircle,
  LayoutDashboard,
  LogOut,
  MessageSquare,
  Moon,

  Send,
  Sparkles,
  Sun,
  Terminal,
  Plus,
  X,
  RefreshCw,
  KeyRound,
  Loader2,
  Maximize2,
  Mic,
  Network,
  Rocket,
  Paperclip,
  Pencil,
  Trash2,
  Download,
  ExternalLink,
  Database,
Code2,
  Users,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, streamPOST, type AgentInfo, type ChatDefault, type Conversation, type DispatchRun, type Message, type GatewayKey, type GatewayModelItem, type Provider, type WorkerSettings, type WorkerStatus } from "./api";
import Onboarding from "./Onboarding";
import { GatewayBackendsPanel } from "./GatewayBackends";
import { WorkersPanel } from "./WorkersPanel";
import Dashboard from "./Dashboard";
import { SkillsPanel } from "./SkillsPanel";
import { RagPage } from "./RagPage";
import { CodePage } from "./CodePage";
import { ModelsPage } from "./ModelsPage";
import TeamPage from "./TeamPage";
import { Badge, Button, Card, Input, Select, Textarea, ThemeProvider, useTheme } from "./components/ui";

type Page = "dashboard" | "chat" | "agents" | "gateway" | "rag" | "code" | "team" | "onboard";

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
  const [page, setPage] = useState<Page>("chat");

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
    const onLogout = () => setUser(null);
    window.addEventListener("fc:logout", onLogout);
    return () => window.removeEventListener("fc:logout", onLogout);
  }, []);

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
  }, [user]);

  if (user === undefined) return <Splash />;
  if (!user) return <Login onDone={setUser} />;

  return (
    <div className="flex h-screen flex-col bg-zinc-50 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100">
      <TopBar user={user} page={page} setPage={setPage} onLogout={async () => { await api.logout(); setUser(null); }} onWizard={() => setPage("onboard")} />
      <main className="flex-1 min-h-0">
        {page === "onboard" && (
          <Onboarding onDone={() => setPage("gateway")} />
        )}
        {page === "dashboard" && <Dashboard />}
        {page === "chat" && <ChatPage onPage={setPage} />}
        {page === "agents" && <AgentsPage />}
        {page === "gateway" && <GatewayPage />}
        {page === "rag" && <RagPage />}
        {page === "code" && <CodePage />}
        {page === "team" && <TeamPage />}
      </main>
    </div>
  );
}

function Splash() {
  return <div className="flex h-screen items-center justify-center text-zinc-400">loading…</div>;
}

function TopBar({
  user, page, setPage, onLogout, onWizard,
}: {
  user: string; page: Page; setPage: (p: Page) => void; onLogout: () => void; onWizard: () => void;
}) {
  const { dark, toggle } = useTheme();
  const tab = (id: Page, label: string, Icon: typeof Bot) => (
    <button
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
    <header className="flex items-center justify-between border-b border-zinc-200/80 dark:border-zinc-800 bg-white/60 dark:bg-zinc-900/60 px-4 py-2 backdrop-blur">
      <div className="flex min-w-0 items-center gap-4">
        <span className="hidden shrink-0 font-semibold tracking-tight sm:inline">fusion-console</span>
        <nav className="flex gap-1 overflow-x-auto">
          {tab("chat", "Chat", MessageSquare)}
          {tab("agents", "Agents", Bot)}
          {tab("dashboard", "Dashboard", LayoutDashboard)}
          {tab("gateway", "Gateway", Network)}
          {tab("rag", "Knowledge Base", Database)}
          {tab("code", "Coding Agent", Code2)}
          {tab("team", "Team", Users)}
        </nav>
      </div>
      <div className="flex items-center gap-2 text-sm text-zinc-500">
        <button onClick={toggle} className="rounded-xl p-2 hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60">
          {dark ? <Sun size={16} /> : <Moon size={16} />}
        </button>
        <UserMenu user={user} onLogout={onLogout} onWizard={onWizard} />
      </div>
    </header>
  );
}

// UserMenu is the top-right account dropdown. Besides logout it offers the
// wizard's one-click gateway API key creation so users who skipped the
// wizard can mint (and copy) a key from any page, plus a re-run of the
// setup wizard itself.
function UserMenu({ user, onLogout, onWizard }: { user: string; onLogout: () => void; onWizard: () => void }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [rawKey, setRawKey] = useState("");
  const [copied, setCopied] = useState(false);
  const [err, setErr] = useState("");

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
    <div className="relative">
      <button
        onClick={() => { setOpen(!open); if (!open) { setRawKey(""); setErr(""); } }}
        className="flex items-center gap-1 rounded-xl px-2 py-1.5 hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60"
      >
        <span className="hidden sm:inline">{user}</span>
        <ChevronDown size={14} />
      </button>
      {open && (
        <div className="absolute right-0 z-50 mt-1 w-80 rounded-xl border border-zinc-200/80 bg-white p-3 shadow-md shadow-zinc-900/5 dark:border-zinc-800 dark:bg-zinc-900">
          {rawKey ? (
            <div className="space-y-2">
              <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
                Copy and save this key now — it is shown only once (the console keeps its copy in the OS keychain).
              </p>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded-lg bg-zinc-100 px-2 py-1.5 font-mono text-xs dark:bg-zinc-800">{rawKey}</code>
                <Button variant="outline" onClick={copy} disabled={copied}>
                  {copied ? "copied" : "Copy"}
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-2">
              <Button onClick={provision} disabled={busy} className="w-full">
                {busy ? "Creating..." : "Create API key"}
              </Button>
              <p className="text-xs text-zinc-400">
                Issues a gateway API key via one click (stored in the OS keychain). Also available on the Gateway page.
              </p>
              <Button variant="outline" onClick={() => { setOpen(false); onWizard(); }} className="w-full">
                <Sparkles size={14} /> Run setup wizard
              </Button>
            </div>
          )}
          {err && <p className="mt-2 text-xs text-red-500">{err}</p>}
          <button
            onClick={onLogout}
            className="mt-2 flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800"
          >
            <LogOut size={14} /> Sign out
          </button>
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
  const recogRef = useRef<any>(null);
  const [orchestrate, setOrchestrate] = useState(true);
  const [busy, setBusy] = useState(false);
  const [approval, setApproval] = useState<{ id: string; detail: string } | null>(null);
  const [err, setErr] = useState("");
  const [unread, setUnread] = useState<Record<string, number>>({});
  // H5 (mobile) drawers: the conversation rail and the dispatch rail are
  // hidden below md/lg — on phones they open as overlay drawers instead.
  const [convDrawer, setConvDrawer] = useState(false);
  const [dispatchDrawer, setDispatchDrawer] = useState(false);
  const [dispatchActive, setDispatchActive] = useState(0);
const [workingWorkers, setWorkingWorkers] = useState<string[]>([]);
  const bottomRef = useRef<HTMLDivElement>(null);

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
    refreshConvs().catch(() => {});
    api.providers().then((r) => {
      const list = (r.providers ?? []).filter((p) => p.enabled);
      setProviders(list);
      const gw = list.find((p) => p.type === "gateway") ?? null;
      setGwProvider(gw);
    }).catch(() => {});
    api.agents().then((r) => {
      setAgents(r.agents ?? []);
    }).catch(() => {});
  }, [refreshConvs]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [entries]);

  // Background dispatches: poll the run list for sidebar badges and refresh
  // the open conversation so finished runs appear without a manual reload.
  useEffect(() => {
    let stop = false;
    const tick = async () => {
      try {
        const r = await api.dispatches();
        if (stop) return;
        setUnread(r.unread_by_conv ?? {});
        const hasActiveBg = (r.dispatches ?? []).some((d) => d.status === "running" || d.status === "queued");
        if (!hasActiveBg) {
          try {
            const ws = await api.workersStatus();
            if (stop) return;
            setWorkingWorkers((ws.workers ?? []).filter((w) => w.busy).map((w) => w.name));
          } catch { /* ignore */ }
        } else {
          setWorkingWorkers([]);
        }
        if (active && !busy) {
          const mr = await api.messages(active.ID);
          const fresh = (mr.messages ?? []).map((m) => ({ role: m.Role, content: m.Content, meta: m.Meta }));
          setEntries((cur) => (fresh.length !== cur.length ? fresh : cur));
        }
      } catch { /* transient — next tick retries */ }
    };
    const t = setInterval(tick, 6000);
    return () => { stop = true; clearInterval(t); };
  }, [active, busy]);

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

  const openConv = async (c: Conversation) => {
    setActive(c);
    setErr("");
    setConvDrawer(false);
    const r = await api.messages(c.ID);
    setEntries((r.messages ?? []).map((m) => ({ role: m.Role, content: m.Content, meta: m.Meta })));
    if (unread[c.ID] > 0) {
      api.ackDispatches(c.ID).then(() => setUnread((u) => ({ ...u, [c.ID]: 0 }))).catch(() => {});
    }
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
          setEntries((e) => [...e, ...r.messages.map((m) => ({ role: m.role, content: m.content, meta: m.meta }))]);
        }
      } catch { /* daemon offline — next tick retries */ }
    };
    pull();
    const t = setInterval(pull, 5000);
    return () => clearInterval(t);
  }, [active?.ID, active?.Kind]); // eslint-disable-line react-hooks/exhaustive-deps

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
  useEffect(() => {
    if (!teamModalOpen) return;
    api.orchestration().then((r) => setTeamWorkers(r.orchestrator?.workers ?? [])).catch(() => setTeamWorkers([]));
  }, [teamModalOpen]);
  const createTeam = async () => {
    const nm = teamName.trim();
    if (!nm) { setTeamErr("team name required"); return; }
    const members = teamPicked.map((wn) => {
      const w = teamWorkers.find((x) => (x.name || x.agent) === wn);
      return {
        name: (w?.name || w?.agent || wn).replace(/[^A-Za-z0-9_-]/g, "-"),
        role: w?.agent || "",
        skills: [] as string[],
        tool_allowlist: [] as string[],
        permission_mode: teamMode,
        budget_tokens: 50000,
        max_concurrency: 1,
      };
    });
    if (members.length === 0) { setTeamErr("pick at least one worker"); return; }
    setTeamCreating(true);
    setTeamErr("");
    try {
      await api.teamCreate({
        team: { name: nm, namespace_key: nm, members, lead_budget_tokens: 200000, playbook: false, max_depth: 2, metadata: {} },
        tasks: [], lead_start: false, lead_goal: "",
      });
      setTeamModalOpen(false);
      setTeamName("");
      setTeamPicked([]);
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
    const mm = text.match(/@([a-zA-Z0-9_-]+)/);
    const mentioned = mm ? detectedAgents.find((a) => a.name === mm[1]) : null;
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
    try {
      let conv = active;
      if (!conv) {
        const r = await api.createConversation(text.slice(0, 40), "chat");
        conv = { ID: r.id, Title: text.slice(0, 40), Kind: "chat", User: "", AgentName: "", Workdir: "", CreatedAt: "" };
        setActive(conv);
        await refreshConvs();
      }
      setEntries((e) => [...e, { role: "user", content: full }, { role: "assistant", content: "" }]);
      const idx = conv.ID;
      await streamPOST("/api/chat", { conversation_id: idx, content: full, orchestrate }, (ev) => {
        const type = ev.type as string;
        setEntries((e) => {
          const copy = e.slice();
          const last = copy[copy.length - 1];
          if (type === "delta") last.content += (ev.text as string) ?? "";
          else if (type === "notice") last.content += `\n\n> ℹ️ ${ev.text}\n`;
          else if (type === "approval") {
            setApproval({ id: (ev.id as string) ?? "", detail: (ev.detail as string) ?? "" });
            last.content += `\n\n> ⏸ awaiting your approval: ${ev.detail ?? ""}\n\n`;
          }
          else if (type === "tool") {
            const status = (ev.status as string) ?? "";
            setApproval(null);
            const detail = (ev.detail as string) ?? "";
            if (status === "running") last.content += `\n\n> ⚙️ dispatch \`${ev.tool}\`${detail ? ` — ${detail}` : ""}\n\n`;
            else if (status === "progress") last.content += `> ⏳ ${detail}\n`;
            else if (status === "done") last.content += "> ↳ done\n\n";
            else if (status === "denied") last.content += `> ↳ denied: ${detail}\n\n`;
            else if (status === "error") last.content += "> ↳ error\n\n";
          }
          else if (type === "error") last.content += `\n\n**error:** ${ev.text}`;
          else if (type === "usage") last.meta = JSON.stringify(ev);
          return copy;
        });
      });
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
    try {
      let conv = active;
      if (!conv || conv.Kind !== "agent" || conv.AgentName !== agent) {
        const r = await api.createConversation(text.slice(0, 40), "agent", agent, "");
        conv = { ID: r.id, Title: text.slice(0, 40), Kind: "agent", User: "", AgentName: agent, Workdir: "", CreatedAt: "" };
        setActive(conv);
        await refreshConvs();
      }
      setEntries((e) => [...e, { role: "user", content: text }, { role: "assistant", content: "" }]);
      await streamPOST("/api/agent/run", { conversation_id: conv.ID, agent, task: text, workdir: "" }, (ev) => {
        const type = ev.type as string;
        setEntries((e) => {
          const copy = e.slice();
          const last = copy[copy.length - 1];
          if (type === "text") last.content += (ev.text as string) ?? "";
          else if (type === "tool") last.content += `\n\n> 🔧 \`${ev.tool}\` ${ev.detail ?? ""}\n`;
          else if (type === "result") last.content += (ev.text as string) ?? "";
          else if (type === "usage") last.meta = JSON.stringify(ev);
          else if (type === "notice") last.content += `\n\n> ℹ️ ${ev.text}\n`;
          else if (type === "error") last.content += `\n\n**error:** ${ev.text}`;
          return copy;
        });
      });
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
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
    <div className="flex h-full">
      {convDrawer && (
        <div className="fixed inset-0 z-30 bg-black/40 lg:hidden" onClick={() => setConvDrawer(false)} />
      )}
      <aside
        className={`${convDrawer ? "fixed inset-y-0 left-0 z-40 flex w-72 shadow-xl" : "hidden"} flex-col border-r border-zinc-200/80 dark:border-zinc-800 bg-white lg:static lg:flex lg:w-64 lg:shadow-none lg:bg-white/40 dark:bg-zinc-900 dark:lg:bg-zinc-900/40`}
      >
        <div className="p-3 space-y-2">
          <div className="grid grid-cols-2 gap-1 rounded-xl bg-zinc-200/60 p-1 text-xs dark:bg-zinc-800/60">
            <button
              onClick={() => setSideTab("chat")}
              className={`flex items-center justify-center gap-1.5 rounded-lg px-2 py-1.5 ${sideTab === "chat" ? "bg-white shadow-sm text-indigo-600 dark:bg-zinc-900 dark:text-indigo-400" : "text-zinc-500 hover:text-zinc-700 dark:hover:text-zinc-300"}`}
            >
              <MessageSquare size={13} /> Chats
            </button>
            <button
              onClick={() => setSideTab("team")}
              className={`flex items-center justify-center gap-1.5 rounded-lg px-2 py-1.5 ${sideTab === "team" ? "bg-white shadow-sm text-indigo-600 dark:bg-zinc-900 dark:text-indigo-400" : "text-zinc-500 hover:text-zinc-700 dark:hover:text-zinc-300"}`}
            >
              <Users size={13} /> Teams
            </button>
          </div>
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
        {sideTab === "team" && studio && !studioReady && (
          <div className="mx-3 mb-2 rounded-xl border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
            <p className="font-medium">Team needs fusion-agent-studio</p>
            <p className="mt-1 opacity-80">The daemon is not running and the package is not installed yet. Install it into the workspace venv via pip (editable), then the daemon starts automatically.</p>
            {studioInstalling ? (
              <p className="mt-2 flex items-center gap-1.5"><Loader2 size={12} className="animate-spin" /> {studio.phase}: installing…</p>
            ) : (
              <button
                onClick={() => api.studioInstall().then(() => api.studioStatus().then(setStudio)).catch(() => {})}
                className="mt-2 rounded-lg bg-amber-500/20 px-2.5 py-1.5 font-medium hover:bg-amber-500/30"
              >
                Install fusion-agent-studio (pip)
              </button>
            )}
            {studio.error && <p className="mt-2 font-mono text-[10px] opacity-80">{studio.error}</p>}
            {(studio.logs ?? []).length > 0 && (
              <pre className="mt-2 max-h-24 overflow-y-auto whitespace-pre-wrap font-mono text-[10px] opacity-70">{(studio.logs ?? []).slice(-8).join("\n")}</pre>
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
        <div className="flex flex-wrap items-center gap-2 border-b border-zinc-200/80 dark:border-zinc-800 px-4 py-2 text-sm">
          <button
            onClick={() => setConvDrawer(true)}
            title="Conversations"
            className="lg:hidden flex items-center rounded-xl border border-zinc-200/80 px-2.5 py-1.5 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400"
          >
            <Menu size={16} />
          </button>
          {active?.Kind === "team" && (
            <span className="flex items-center gap-1.5 rounded-xl border border-indigo-500/40 bg-indigo-500/10 px-2.5 py-1.5 text-xs text-indigo-500" title="Team conversation — each message assigns a task to the team; replies appear here via inbox polling">
              <Users size={14} /> team: {active.AgentName}
            </span>
          )}
          <button
            onClick={() => setOrchestrate((v) => !v)}
            title={orchestrate ? "Orchestration ON — the brain can dispatch workers (list_agents / run_agent / read_artifact). Click to turn off." : "Orchestration OFF — plain chat with the brain model. Click to let the brain dispatch workers."}
            className={`flex items-center gap-1.5 rounded-xl border px-2.5 py-1.5 text-xs transition-colors ${
              orchestrate
                ? "border-indigo-500/40 bg-indigo-500/10 text-indigo-500"
                : "border-zinc-200/80 text-zinc-400 hover:text-zinc-600 dark:border-zinc-800 dark:hover:text-zinc-300"
            }`}
          >
            <Network size={14} /> orchestrate: {orchestrate ? "on" : "off"}
          </button>
          <button
            onClick={() => setDispatchDrawer(true)}
            title="Dispatches"
            className="md:hidden ml-auto relative flex items-center rounded-xl border border-zinc-200/80 px-2.5 py-1.5 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400"
          >
            <PanelRight size={16} />
            {dispatchActive > 0 && (
              <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-amber-500 px-1 text-[10px] font-semibold text-white">
                {dispatchActive}
              </span>
            )}
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-4 space-y-3">
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
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>{m.content || "…"}</ReactMarkdown>
                  </div>
                )}
                {m.meta && (() => {
                  try {
                    const j = JSON.parse(m.meta);
                    return (
                      <p className="mt-1 text-xs text-zinc-400">
                        {j.cost_usd ? `$${Number(j.cost_usd).toFixed(4)} · ` : ""}
                        {j.tokens_in ? `${j.tokens_in}→${j.tokens_out} tok` : ""}
                        {j.model ? ` · ${j.model}` : ""}
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
          <div ref={bottomRef} />
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
                if (e.nativeEvent.isComposing) return;
                if (mentionOptions.length > 0 && (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey))) {
                  e.preventDefault();
                  insertMention(mentionOptions[0].name);
                  return;
                }
                if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); }
              }}
              placeholder={listening ? "listening…" : "Message…  (@ to hand off to an agent)"}
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
        </div>
      </div>
      {teamModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={() => !teamCreating && setTeamModalOpen(false)}>
          <div className="w-full max-w-md rounded-2xl border border-zinc-200/80 bg-white p-5 shadow-lg dark:border-zinc-800 dark:bg-zinc-900" onClick={(e) => e.stopPropagation()}>
            <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold"><Users size={16} /> New team</h3>
            <Input className="mb-3" placeholder="team name" value={teamName} onChange={(e) => setTeamName(e.target.value)} autoFocus />
            <label className="mb-3 flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
              permission mode
              <Select className="w-40" value={teamMode} onChange={(e) => setTeamMode(e.target.value)}
                title="Kernel permission mode applied to every member: default (ask), approved (auto-accept edits), bypass (no prompts), plan (read-only until approved)">
                <option value="default">default — ask on each tool</option>
                <option value="approved">approved — auto-accept edits</option>
                <option value="bypass">bypass — no prompts</option>
                <option value="plan">plan — read-only</option>
              </Select>
            </label>
            <p className="mb-2 text-xs font-medium text-zinc-500">Members — pick from configured workers</p>
            {teamWorkers.filter((w) => w.enabled).length === 0 && (
              <p className="mb-2 text-xs text-amber-600 dark:text-amber-400">no workers configured — add one on the Agents page first</p>
            )}
            <div className="mb-3 max-h-56 space-y-1 overflow-y-auto">
              {teamWorkers.filter((w) => w.enabled).map((w) => {
                const wn = w.name || w.agent;
                const on = teamPicked.includes(wn);
                return (
                  <label key={wn} className="flex cursor-pointer items-center gap-2 rounded-xl px-2 py-1.5 text-sm hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60">
                    <input type="checkbox" checked={on} className="h-4 w-4 rounded border-zinc-300 text-indigo-600"
                      onChange={() => setTeamPicked(on ? teamPicked.filter((x) => x !== wn) : [...teamPicked, wn])} />
                    <span className="font-medium">{wn}</span>
                    <span className="text-xs text-zinc-500">{w.agent}{w.model ? ` · ${w.model}` : ""}</span>
                  </label>
                );
              })}
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
          onRunningCount={setDispatchActive}
        onOpen={(c) => { setDispatchDrawer(false); openConv(c); }}
        drawerOpen={dispatchDrawer}
        onDrawerClose={() => setDispatchDrawer(false)}
      />
    </div>
  );
}

// DispatchPanel is the right rail of the chat page: every background run
// (run_agent background=true) grouped into running / queued / finished.
// Clicking a finished row opens its conversation.
function DispatchPanel({ onOpen, drawerOpen, onDrawerClose, onRunningCount }: { onOpen: (c: Conversation) => void; drawerOpen?: boolean; onDrawerClose?: () => void; onRunningCount?: (n: number) => void }) {
  const [runs, setRuns] = useState<DispatchRun[]>([]);
  const [convs, setConvs] = useState<Conversation[]>([]);
  const [busyWorkers, setBusyWorkers] = useState<WorkerStatus[]>([]);
  useEffect(() => {
    const load = async () => {
      try {
        const r = await api.dispatches();
        setRuns(r.dispatches ?? []);
      } catch { /* server unreachable — keep last snapshot */ }
      try {
        const r = await api.conversations();
        setConvs(r.conversations ?? []);
      } catch { /* ignore */ }
      try {
        const r = await api.workersStatus();
        setBusyWorkers((r.workers ?? []).filter((w) => w.busy));
      } catch { /* ignore */ }
    };
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, []);
  const running = runs.filter((r) => r.status === "running");
  const queued = runs.filter((r) => r.status === "queued");
  const finished = runs.filter((r) => r.status === "done" || r.status === "error").slice(0, 8);
  // Report active-run count so the mobile toggle button can badge it — the
  // panel itself is a drawer below md and its cards are not visible there.
  useEffect(() => {
    onRunningCount?.(running.length + queued.length);
  }, [running.length, queued.length, onRunningCount]);
  const openRun = (r: DispatchRun) => {
    const c = convs.find((x) => x.ID === r.conversation_id);
    if (c) onOpen(c);
  };
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
      setRuns((r) => r.filter((x) => x.status === "running" || x.status === "queued"));
    } catch { /* keep current view */ }
  };
  const dismissRun = async (id: number) => {
    setRuns((r) => r.filter((x) => x.id !== id));
    try {
      await api.dismissDispatch(id);
    } catch { /* next poll restores if delete failed */ }
  };
  // Expanded card view: a fixed overlay showing the full task / detail /
  // error / result of one run — cards in the rail stay a fixed small size.
  const [expanded, setExpanded] = useState<DispatchRun | null>(null);
  const row = (r: DispatchRun) => (
    <div key={r.id} onClick={() => openRun(r)}
      className={`h-[72px] w-full overflow-hidden rounded-xl border p-2.5 text-left transition-colors select-text ${
        r.status === "running"
          ? "border-amber-500/30 bg-amber-500/5 hover:bg-amber-500/10"
          : r.status === "done"
            ? "border-emerald-500/30 bg-emerald-500/5 hover:bg-emerald-500/10"
            : r.status === "error"
              ? "border-red-500/30 bg-red-500/5 hover:bg-red-500/10"
              : "border-zinc-200/80 dark:border-zinc-800 hover:bg-zinc-100 dark:hover:bg-zinc-800"
      } ${r.conversation_id === 0 ? "cursor-default" : "cursor-pointer"}`}>
      <div className="flex items-center gap-2">
        {r.status === "running" ? (
          <RefreshCw size={12} className="shrink-0 animate-spin text-amber-500" />
        ) : r.status === "done" ? (
          <CheckCircle size={12} className="shrink-0 text-emerald-500" />
        ) : r.status === "error" ? (
          <XCircle size={12} className="shrink-0 text-red-500" />
        ) : (
          <Clock size={12} className="shrink-0 text-zinc-400" />
        )}
        <span className="truncate text-xs font-medium">{r.worker}</span>
        <span className={`ml-auto text-[11px] ${r.status === "running" ? "text-amber-600 dark:text-amber-400" : r.status === "done" ? "text-emerald-600 dark:text-emerald-400" : r.status === "error" ? "text-red-500" : "text-zinc-400"}`}>
          {r.status === "running" ? `${r.elapsed_s}s`
            : r.status === "queued" ? "queued"
            : r.status === "done" ? `done · $${r.cost_usd.toFixed(2)}`
            : "error"}
        </span>
        {copyBtn(`run:${r.id}`, () => copyRun(r, "run"))}
        <button onClick={(e) => { e.stopPropagation(); setExpanded(r); }}
          className="shrink-0 rounded-md p-0.5 text-zinc-400 hover:bg-zinc-200/60 hover:text-zinc-600 dark:hover:bg-zinc-700 dark:hover:text-zinc-300"
          title="Maximize — full details">
          <Maximize2 size={12} />
        </button>
        {(r.status === "done" || r.status === "error") && (
          <button onClick={(e) => { e.stopPropagation(); dismissRun(r.id); }}
            className="shrink-0 rounded-md p-0.5 text-zinc-400 hover:bg-zinc-200/60 hover:text-red-500 dark:hover:bg-zinc-700"
            title="Dismiss this card">
            <X size={12} />
          </button>
        )}
      </div>
      <p className="mt-1 truncate text-[11px] text-zinc-500 dark:text-zinc-400" title={r.task}>▸ {r.task}</p>
      {(r.status === "running" || r.status === "queued") && r.detail && (
        <p className="mt-0.5 truncate text-[11px] text-zinc-400" title={r.detail}>{r.detail}</p>
      )}
    </div>
  );
  return (
    <>
      {drawerOpen && (
        <div className="fixed inset-0 z-30 bg-black/40 md:hidden" onClick={onDrawerClose} />
      )}
      <aside
        className={`${drawerOpen ? "fixed inset-y-0 right-0 z-40 flex w-80 max-w-[85vw] shadow-xl" : "hidden"} shrink-0 flex-col border-l border-zinc-200/80 dark:border-zinc-800 bg-white md:static md:flex md:w-64 lg:w-72 md:shadow-none md:bg-white/40 dark:bg-zinc-900 dark:md:bg-zinc-900/40 p-3`}
      >
      <div className="flex items-center justify-between px-1 pb-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-zinc-400">Dispatches</span>
        <div className="flex items-center gap-1.5">
          <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-[11px] text-zinc-500 dark:bg-zinc-800">
            {running.length} running · {queued.length} queued
          </span>
          {(runs.some((r) => r.status === "done" || r.status === "error")) && (
            <Button variant="ghost" className="px-1.5 py-0.5 text-[11px]" onClick={clearFinished} title="Remove all finished cards">
              Clear
            </Button>
          )}
        </div>
      </div>
      <div className="flex-1 space-y-2 overflow-y-auto">
        {/* Agent status strip: every worker with a live agent process, even
            one recovered after a console restart (registry lost, process not). */}
        {busyWorkers.length > 0 && (
          <div className="space-y-1.5">
            {busyWorkers.map((w) => (
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
        {runs.length === 0 && busyWorkers.length === 0 && (
          <p className="px-1 pt-4 text-center text-xs text-zinc-400">
            No background runs yet. The brain dispatches with background=true for long tasks.
          </p>
        )}
      </div>
      <p className="border-t border-zinc-200/80 px-1 pt-2 text-[11px] leading-4 text-zinc-400 dark:border-zinc-800">
        Finished runs post their outcome into the conversation automatically.
      </p>
      {expanded && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-zinc-900/50 p-6" onClick={() => setExpanded(null)}>
          <div className="flex max-h-[80vh] w-[560px] max-w-full flex-col rounded-2xl border border-zinc-200/80 bg-white shadow-lg shadow-zinc-900/10 dark:border-zinc-700 dark:bg-zinc-900"
            onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center gap-2 border-b border-zinc-200/80 px-4 py-2.5 dark:border-zinc-800">
              {expanded.status === "running" ? <RefreshCw size={14} className="animate-spin text-amber-500" />
                : expanded.status === "done" ? <CheckCircle size={14} className="text-emerald-500" />
                : expanded.status === "error" ? <XCircle size={14} className="text-red-500" />
                : <Clock size={14} className="text-zinc-400" />}
              <span className="text-sm font-medium">run #{expanded.id} · {expanded.worker}</span>
              <span className="text-xs text-zinc-400">
                {expanded.status} · {expanded.elapsed_s}s · ${expanded.cost_usd.toFixed(4)}
              </span>
              <button className="ml-auto rounded-md p-1 text-zinc-400 hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800" onClick={() => setExpanded(null)} title="Close">
                <X size={14} />
              </button>
            </div>
            <div className="flex-1 space-y-3 overflow-y-auto p-4 text-sm">
              <section>
                <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-zinc-400">Task</p>
                <p className="select-text whitespace-pre-wrap break-words text-zinc-700 dark:text-zinc-200">{expanded.task}</p>
              </section>
              {expanded.detail && (
                <section>
                  <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-zinc-400">Detail</p>
                  <p className="select-text whitespace-pre-wrap break-words text-zinc-500 dark:text-zinc-400">{expanded.detail}</p>
                </section>
              )}
              {expanded.error && (
                <section>
                  <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-red-400">Error</p>
                  <pre className="select-text whitespace-pre-wrap break-words rounded-xl bg-red-500/5 p-2.5 text-[12px] leading-5 text-red-600 dark:text-red-400">{expanded.error}</pre>
                </section>
              )}
              {expanded.result && (
                <section>
                  <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-emerald-400">Result</p>
                  <pre className="select-text whitespace-pre-wrap break-words rounded-xl bg-emerald-500/5 p-2.5 text-[12px] leading-5 text-zinc-700 dark:text-zinc-200">{expanded.result}</pre>
                </section>
              )}
            </div>
          </div>
        </div>
      )}
    </aside>
    </>
  );
}

function AgentsPage() {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [tab, setTab] = useState<"tools" | "workers" | "skills">("tools");
  const load = () => api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
  useEffect(() => { load(); }, []);
  return (
    <div className="mx-auto max-w-3xl space-y-4 p-6">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Coding agents</h2>
        {tab === "tools" && <Button variant="ghost" onClick={load}><RefreshCw size={14} /> Refresh</Button>}
      </div>
      <div className="flex gap-2">
        {(
          [
            ["tools", "Agents (tools)"],
            ["workers", "Workers"],
            ["skills", "Skills"],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            onClick={() => setTab(id)}
            className={`rounded-xl border px-3 py-1.5 text-sm transition-colors ${
              tab === id ? "border-indigo-500 bg-indigo-500/5 text-indigo-600 dark:text-indigo-400" : "border-zinc-200/80 text-zinc-500 dark:border-zinc-800"
            }`}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === "tools" && (
        <>
          <p className="text-sm text-zinc-500">
            Headless adapters only — each run is one task in the chosen workdir. Agents missing from PATH are grayed out.
          </p>
          {agents.filter((a) => !a.is_worker).map((a) => (
            <Card key={a.name} className={`flex items-center justify-between p-4 ${!a.detected ? "opacity-50" : ""}`}>
              <div>
                <p className="font-medium flex items-center gap-2">
                  <Terminal size={16} /> {a.name}
                  {a.detected ? <Badge tone="ok">detected</Badge> : <Badge>not on PATH</Badge>}
                </p>
                <p className="text-xs text-zinc-500 mt-1">
                  command: {a.command} · permission mode: {a.permission_mode} · budget cap ${a.max_budget_usd.toFixed(2)}
                </p>
              </div>
            </Card>
          ))}
        </>
      )}
      {tab === "workers" && (
        <Card className="space-y-3 p-4">
          <WorkersPanel />
        </Card>
      )}
      {tab === "skills" && <SkillsPanel />}
    </div>
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

