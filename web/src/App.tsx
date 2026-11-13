import { useCallback, useEffect, useRef, useState } from "react";
import {
  Bot,
  Cpu,
  Brain,
  ChevronDown,
  LayoutDashboard,
  LogOut,
  MessageSquare,
  Moon,

  Send,
  Sparkles,
  Sun,
  Terminal,
  Plus,
  RefreshCw,
  KeyRound,
  Network,
  Pencil,
  Trash2,
  Download,
  ExternalLink,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, streamPOST, type AgentInfo, type Conversation, type Message, type GatewayKey, type GatewayModelItem, type Provider } from "./api";
import Onboarding from "./Onboarding";
import { GatewayBackendsPanel } from "./GatewayBackends";
import { WorkersPanel } from "./WorkersPanel";
import Dashboard from "./Dashboard";
import { SkillsPanel } from "./SkillsPanel";
import { Badge, Button, Card, Input, Select, Textarea, ThemeProvider, useTheme } from "./components/ui";

type Page = "dashboard" | "chat" | "agents" | "gateway" | "onboard";

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
          <Onboarding onDone={() => setPage("chat")} />
        )}
        {page === "dashboard" && <Dashboard />}
        {page === "chat" && <ChatPage />}
        {page === "agents" && <AgentsPage />}
        {page === "gateway" && <GatewayPage />}
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
      className={`inline-flex items-center gap-2 rounded-xl px-3 py-1.5 text-sm font-medium transition-colors ${
        page === id
          ? "bg-indigo-600 text-white shadow-sm"
          : "text-zinc-500 hover:bg-zinc-200/60 dark:text-zinc-400 dark:hover:bg-zinc-800/60"
      }`}
    >
      <Icon size={16} />
      {label}
    </button>
  );
  return (
    <header className="flex items-center justify-between border-b border-zinc-200/80 dark:border-zinc-800 bg-white/60 dark:bg-zinc-900/60 px-4 py-2 backdrop-blur">
      <div className="flex items-center gap-4">
        <span className="font-semibold tracking-tight">fusion-console</span>
        <nav className="flex gap-1">
          {tab("chat", "Chat", MessageSquare)}
          {tab("agents", "Agents", Bot)}
          {tab("dashboard", "Dashboard", LayoutDashboard)}
          {tab("gateway", "Gateway", Network)}
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

function ChatPage() {
  const [convs, setConvs] = useState<Conversation[]>([]);
  const [active, setActive] = useState<Conversation | null>(null);
  const [entries, setEntries] = useState<ChatEntry[]>([]);
  const [gwProvider, setGwProvider] = useState<Provider | null>(null);
  const [gwModels, setGwModels] = useState<GatewayModelItem[]>([]);
  const [brainModel, setBrainModel] = useState("");
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [approval, setApproval] = useState<{ id: string; detail: string } | null>(null);
  const [err, setErr] = useState("");
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
      const gw = (r.providers ?? []).find((p) => p.type === "gateway" && p.enabled) ?? null;
      setGwProvider(gw);
      if (!gw) return;
      // Brain selector = model names from the gateway (/v1/models), matching
      // the Gateway page brain card. Gateway unreachable: fall back to the
      // provider's stored model so chat still works.
      api.gatewayModels().then((m) => {
        const items = m.items ?? [];
        setGwModels(items);
        setBrainModel((cur) => cur || (gw.model && items.some((i) => i.value === gw.model) ? gw.model : items[0]?.value || gw.model || ""));
      }).catch(() => {
        setGwModels(gw.model ? [{ id: gw.model, backend: "", value: gw.model, local: false }] : []);
        setBrainModel((cur) => cur || gw.model || "");
      });
    }).catch(() => {});
    api.agents().then((r) => {
      setAgents(r.agents ?? []);
    }).catch(() => {});
  }, [refreshConvs]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [entries]);

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
    const r = await api.messages(c.ID);
    setEntries((r.messages ?? []).map((m) => ({ role: m.Role, content: m.Content, meta: m.Meta })));
  };

  const newChat = async () => {
    const r = await api.createConversation("chat " + new Date().toLocaleString(), "chat");
    await refreshConvs();
    setActive(null);
    setEntries([]);
    await api.messages(r.id).catch(() => {});
  };

  const changeBrain = async (model: string) => {
    setBrainModel(model);
    if (!gwProvider || !model) return;
    try {
      const updated = { ...gwProvider, model };
      await api.saveProvider(updated);
      setGwProvider(updated);
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const send = async () => {
    const text = input.trim();
    if (!text || busy) return;
    // @agent mention hands the message off explicitly; anything else is a
    // plain brain chat turn (orchestration lets the brain dispatch workers).
    const mm = text.match(/@([a-zA-Z0-9_-]+)/);
    const mentioned = mm ? detectedAgents.find((a) => a.name === mm[1]) : null;
    if (mentioned) {
      await runAgentTask(text, mentioned.name);
      return;
    }
    if (!gwProvider) {
      setErr("no gateway provider — open the Gateway page and click Create API key first");
      return;
    }
    setBusy(true);
    setErr("");
    setInput("");
    try {
      let conv = active;
      if (!conv) {
        const r = await api.createConversation(text.slice(0, 40), "chat");
        conv = { ID: r.id, Title: text.slice(0, 40), Kind: "chat", User: "", AgentName: "", Workdir: "", CreatedAt: "" };
        setActive(conv);
        await refreshConvs();
      }
      setEntries((e) => [...e, { role: "user", content: text }, { role: "assistant", content: "" }]);
      const idx = conv.ID;
      await streamPOST("/api/chat", { conversation_id: idx, provider_id: gwProvider.id, content: text, orchestrate: true }, (ev) => {
        const type = ev.type as string;
        setEntries((e) => {
          const copy = e.slice();
          const last = copy[copy.length - 1];
          if (type === "delta") last.content += (ev.text as string) ?? "";
          else if (type === "approval") {
            setApproval({ id: (ev.id as string) ?? "", detail: (ev.detail as string) ?? "" });
            last.content += `\n\n> ⏸ awaiting your approval: ${ev.detail ?? ""}\n\n`;
          }
          else if (type === "tool") {
            const status = (ev.status as string) ?? "";
            setApproval(null);
            const detail = (ev.detail as string) ?? "";
            if (status === "running") last.content += `\n\n> ⚙️ dispatch \`${ev.tool}\`${detail ? ` — ${detail}` : ""}\n\n`;
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
      <aside className="hidden md:flex w-64 flex-col border-r border-zinc-200/80 dark:border-zinc-800 bg-white/40 dark:bg-zinc-900/40">
        <div className="p-3">
          <Button variant="outline" className="w-full" onClick={newChat}>
            <Plus size={14} /> New chat
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto px-2 pb-3 space-y-1">
          {convs.map((c) => (
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
          <label className="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400" title="The brain model: fusion-gateway routes chat to this model (fed by /v1/models)">
            <Brain size={14} className="text-indigo-500" /> brain
          </label>
          <Select value={brainModel} onChange={(e) => changeBrain(e.target.value)} className="w-48"
            title="Model served by fusion-gateway — cloud models are pinned to their backend via the qualified backend/model id">
            {gwModels.length === 0 && brainModel && <option value={brainModel}>{brainModel}</option>}
            {gwModels.map((m) => (
              <option key={m.value} value={m.value}>{m.id}{m.backend && !m.local ? ` · ${m.backend}` : ""}</option>
            ))}
          </Select>
          {!gwProvider && (
            <span className="text-xs text-amber-600 dark:text-amber-400">no gateway provider — create an API key on the Gateway page</span>
          )}
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
                </button>
              ))}
            </div>
          )}
          <div className="flex gap-2">
            <Textarea
              rows={2}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (mentionOptions.length > 0 && (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey))) {
                  e.preventDefault();
                  insertMention(mentionOptions[0].name);
                  return;
                }
                if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); }
              }}
              placeholder="Message…  (@ to hand off to an agent)"
            />
            <div className="flex flex-col gap-2">
              <Button onClick={send} disabled={busy || !input.trim()}
                title={mentionedAgent ? `Run with ${mentionedAgent.name}` : "Send as chat"}>
                {mentionedAgent ? <Bot size={16} /> : <Send size={16} />}
              </Button>
            </div>
          </div>
        </div>
      </div>
    </div>
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
          {agents.map((a) => (
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
  const [status, setStatus] = useState<Awaited<ReturnType<typeof api.gatewayStatus>> | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [adminUser, setAdminUser] = useState("");
  const [adminPass, setAdminPass] = useState("");
  const [keys, setKeys] = useState<GatewayKey[]>([]);
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
  const saveBrainModel = async () => {
    setMsg(""); setErr("");
    try {
      const r = await api.providers();
      const gw = (r.providers ?? []).find((p) => p.type === "gateway" && p.enabled);
      if (!gw) {
        setErr("no gateway provider configured — create an API key first");
        return;
      }
      await api.saveProvider({ ...gw, model: brainModel });
      setMsg(`brain model set to ${brainModel}`);
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  return (
    <div className="mx-auto max-w-3xl space-y-4 p-6">
      <h2 className="text-lg font-semibold">Gateway</h2>
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
          {status?.reachable && (
            <Button onClick={openAdmin}>
              <ExternalLink size={14} /> Open Admin GUI
            </Button>
          )}
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
            Model the brain (chat planner) uses. Requests still go through the gateway —
            cloud models are pinned to their backend via the qualified <code>backend/model</code> id.
          </p>
          <div className="flex flex-wrap gap-2">
            <Select className="w-72" value={brainModel} onChange={(e) => setBrainModel(e.target.value)}>
              {!brainModel && <option value="">choose model...</option>}
              {gwModels.map((m) => (
                <option key={m.value} value={m.value}>{m.id}{m.backend && !m.local ? ` · ${m.backend}` : ""}</option>
              ))}
            </Select>
            <Button onClick={saveBrainModel} disabled={!brainModel}>Save</Button>
          </div>
        </Card>
      )}

      {!status?.reachable && (
        <Card className="space-y-3 p-4">
          <h3 className="font-medium flex items-center gap-2"><Download size={16} /> Install gateway</h3>
          <p className="text-sm text-zinc-500">
            Downloads the latest <a className="underline" href="https://github.com/dahai80/fusion-gateway/releases" target="_blank" rel="noreferrer">fusion-gateway release</a> for this
            host (detected automatically) into <code>~/.fusion-console/bin/</code>. Then start it with your
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

      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
    </div>
  );
}

