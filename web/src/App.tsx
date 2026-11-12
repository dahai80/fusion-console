import { useCallback, useEffect, useRef, useState } from "react";
import {
  Bot,
  LogOut,
  MessageSquare,
  Moon,
  Settings as SettingsIcon,
  Send,
  Sun,
  Terminal,
  Plus,
  RefreshCw,
  KeyRound,
  Network,
  Download,
  ExternalLink,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, streamPOST, type AgentInfo, type Conversation, type Message, type Provider } from "./api";
import { Badge, Button, Card, Input, Select, Textarea, ThemeProvider, useTheme } from "./components/ui";

type Page = "chat" | "agents" | "gateway" | "settings";

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

  if (user === undefined) return <Splash />;
  if (!user) return <Login onDone={setUser} />;

  return (
    <div className="flex h-screen flex-col bg-zinc-50 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100">
      <TopBar user={user} page={page} setPage={setPage} onLogout={async () => { await api.logout(); setUser(null); }} />
      <main className="flex-1 min-h-0">
        {page === "chat" && <ChatPage />}
        {page === "agents" && <AgentsPage />}
        {page === "gateway" && <GatewayPage />}
        {page === "settings" && <SettingsPage />}
      </main>
    </div>
  );
}

function Splash() {
  return <div className="flex h-screen items-center justify-center text-zinc-400">loading…</div>;
}

function TopBar({
  user, page, setPage, onLogout,
}: {
  user: string; page: Page; setPage: (p: Page) => void; onLogout: () => void;
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
          {tab("gateway", "Gateway", Network)}
          {tab("settings", "Settings", SettingsIcon)}
        </nav>
      </div>
      <div className="flex items-center gap-2 text-sm text-zinc-500">
        <button onClick={toggle} className="rounded-xl p-2 hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60">
          {dark ? <Sun size={16} /> : <Moon size={16} />}
        </button>
        <span className="hidden sm:inline">{user}</span>
        <button onClick={onLogout} className="rounded-xl p-2 hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60">
          <LogOut size={16} />
        </button>
      </div>
    </header>
  );
}

function Login({ onDone }: { onDone: (u: string) => void }) {
  const [user, setUser] = useState("admin");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
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
  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerId, setProviderId] = useState("");
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [agentName, setAgentName] = useState("");
  const [workdir, setWorkdir] = useState("");
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);

  const refreshConvs = useCallback(async () => {
    const r = await api.conversations();
    setConvs(r.conversations ?? []);
  }, []);

  useEffect(() => {
    refreshConvs().catch(() => {});
    api.providers().then((r) => {
      setProviders(r.providers ?? []);
      const first = (r.providers ?? []).find((p) => p.enabled);
      if (first) setProviderId(first.id);
    }).catch(() => {});
    api.agents().then((r) => {
      setAgents(r.agents ?? []);
      const det = (r.agents ?? []).find((a) => a.detected);
      if (det) setAgentName(det.name);
    }).catch(() => {});
  }, [refreshConvs]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [entries]);

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

  const send = async () => {
    const text = input.trim();
    if (!text || busy) return;
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
      await streamPOST("/api/chat", { conversation_id: idx, provider_id: providerId, content: text }, (ev) => {
        const type = ev.type as string;
        setEntries((e) => {
          const copy = e.slice();
          const last = copy[copy.length - 1];
          if (type === "delta") last.content += (ev.text as string) ?? "";
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

  const runAgent = async () => {
    const text = input.trim();
    if (!text || busy || !agentName) return;
    setBusy(true);
    setErr("");
    setInput("");
    try {
      let conv = active;
      if (!conv || conv.Kind !== "agent" || conv.AgentName !== agentName) {
        const r = await api.createConversation(text.slice(0, 40), "agent", agentName, workdir);
        conv = { ID: r.id, Title: text.slice(0, 40), Kind: "agent", User: "", AgentName: agentName, Workdir: workdir, CreatedAt: "" };
        setActive(conv);
        await refreshConvs();
      }
      setEntries((e) => [...e, { role: "user", content: text }, { role: "assistant", content: "" }]);
      await streamPOST("/api/agent/run", { conversation_id: conv.ID, agent: agentName, task: text, workdir }, (ev) => {
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
            <button
              key={c.ID}
              onClick={() => openConv(c)}
              className={`flex w-full items-center gap-2 rounded-xl px-3 py-2 text-left text-sm truncate ${
                active?.ID === c.ID
                  ? "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400"
                  : "hover:bg-zinc-200/60 dark:hover:bg-zinc-800/60"
              }`}
            >
              {c.Kind === "agent" ? <Bot size={14} className="shrink-0" /> : <MessageSquare size={14} className="shrink-0" />}
              <span className="truncate">{c.Title || `#${c.ID}`}</span>
            </button>
          ))}
        </div>
      </aside>

      <div className="flex flex-1 flex-col min-w-0">
        <div className="flex flex-wrap items-center gap-2 border-b border-zinc-200/80 dark:border-zinc-800 px-4 py-2 text-sm">
          <Select value={providerId} onChange={(e) => setProviderId(e.target.value)} className="w-48">
            {providers.filter((p) => p.enabled).map((p) => (
              <option key={p.id} value={p.id}>{p.name || p.id}</option>
            ))}
          </Select>
          <Select value={agentName} onChange={(e) => setAgentName(e.target.value)} className="w-44">
            <option value="">agent…</option>
            {detectedAgents.map((a) => (
              <option key={a.name} value={a.name}>{a.name}</option>
            ))}
          </Select>
          <Input
            value={workdir}
            onChange={(e) => setWorkdir(e.target.value)}
            placeholder="agent workdir (e.g. /Users/me/proj)"
            className="flex-1 min-w-40"
          />
        </div>

        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          {entries.length === 0 && (
            <div className="flex h-full items-center justify-center text-sm text-zinc-400">
              Pick a provider, send a message — or select an agent + workdir and hand it a task.
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

        {err && <p className="px-4 pb-1 text-sm text-red-500">{err}</p>}

        <div className="border-t border-zinc-200/80 dark:border-zinc-800 p-3">
          <div className="flex gap-2">
            <Textarea
              rows={2}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); }
              }}
              placeholder={agentName ? `Task for ${agentName}…` : "Message…"}
            />
            <div className="flex flex-col gap-2">
              <Button onClick={send} disabled={busy || !input.trim()} title="Send as chat">
                <Send size={16} />
              </Button>
              {agentName && (
                <Button variant="outline" onClick={runAgent} disabled={busy || !input.trim()} title={`Run with ${agentName}`}>
                  <Bot size={16} />
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function AgentsPage() {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const load = () => api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
  useEffect(() => { load(); }, []);
  return (
    <div className="mx-auto max-w-3xl p-6 space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Coding agents</h2>
        <Button variant="ghost" onClick={load}><RefreshCw size={14} /> Refresh</Button>
      </div>
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

  const load = useCallback(() => {
    api.gatewayStatus().then(setStatus).catch((e) => setErr((e as Error).message));
  }, []);
  useEffect(() => { load(); }, [load]);

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

  return (
    <div className="mx-auto max-w-3xl space-y-4 p-6">
      <h2 className="text-lg font-semibold">fusion-gateway</h2>
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
            no second login. Credentials live in the OS keychain. Also configure gateway as a chat
            provider under Settings (type <code>fusion-gateway</code>, base URL <code>{status.base_url}/v1</code>).
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

      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
    </div>
  );
}

function SettingsPage() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [draft, setDraft] = useState<Provider>({ id: "", name: "", type: "gateway", base_url: "http://127.0.0.1:11432/v1", model: "glm-5", api_key_id: "", enabled: true });
  const [secret, setSecret] = useState("");
  const [msg, setMsg] = useState("");
  const [models, setModels] = useState<string[]>([]);

  const load = () => api.providers().then((r) => setProviders(r.providers ?? [])).catch(() => {});
  useEffect(() => { load(); }, []);

  const save = async () => {
    setMsg("");
    try {
      await api.saveProvider(draft);
      await load();
      setMsg(`provider ${draft.id} saved`);
    } catch (ex) {
      setMsg((ex as Error).message);
    }
  };

  const storeKey = async () => {
    setMsg("");
    try {
      await api.storeApiKey(draft.api_key_id, secret);
      setSecret("");
      setMsg(`api key ${draft.api_key_id} stored in OS keychain`);
    } catch (ex) {
      setMsg((ex as Error).message);
    }
  };

  const fetchModels = async () => {
    setMsg("");
    setModels([]);
    try {
      const r = await api.models(draft.id);
      setModels(r.models ?? []);
    } catch (ex) {
      setMsg((ex as Error).message);
    }
  };

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <div>
        <h2 className="mb-2 text-lg font-semibold">Providers</h2>
        <div className="space-y-2">
          {providers.map((p) => (
            <Card key={p.id} className="flex items-center justify-between p-3 text-sm">
              <div>
                <p className="font-medium">{p.name || p.id} <Badge>{p.type}</Badge> {p.enabled ? "" : <Badge tone="warn">disabled</Badge>}</p>
                <p className="text-xs text-zinc-500">{p.base_url} · model {p.model} · key ref {p.api_key_id || "—"}</p>
              </div>
              <Button variant="ghost" onClick={() => setDraft(p)}><SettingsIcon size={14} /></Button>
            </Card>
          ))}
        </div>
      </div>

      <Card className="space-y-3 p-4">
        <h3 className="font-medium">Edit / add provider</h3>
        <div className="grid grid-cols-2 gap-3">
          <Input placeholder="id" value={draft.id} onChange={(e) => setDraft({ ...draft, id: e.target.value })} />
          <Input placeholder="name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
          <Select value={draft.type} onChange={(e) => setDraft({ ...draft, type: e.target.value as Provider["type"] })}>
            <option value="gateway">fusion-gateway</option>
            <option value="openai_compat">OpenAI-compatible (cloud)</option>
            <option value="anthropic_compat">Anthropic-compatible (cloud)</option>
          </Select>
          <Input placeholder="base URL" value={draft.base_url} onChange={(e) => setDraft({ ...draft, base_url: e.target.value })} />
          <Input placeholder="model" value={draft.model} onChange={(e) => setDraft({ ...draft, model: e.target.value })} />
          <Input placeholder="api key id (keychain ref)" value={draft.api_key_id} onChange={(e) => setDraft({ ...draft, api_key_id: e.target.value })} />
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={draft.enabled} onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })} />
          enabled
        </label>
        <div className="flex gap-2">
          <Button onClick={save} disabled={!draft.id}>Save provider</Button>
          <Button variant="outline" onClick={fetchModels} disabled={!draft.id}>Fetch models</Button>
        </div>
        {models.length > 0 && (
          <Select
            value={draft.model}
            onChange={(e) => setDraft({ ...draft, model: e.target.value })}
          >
            {models.map((m) => <option key={m} value={m}>{m}</option>)}
          </Select>
        )}
      </Card>

      <Card className="space-y-3 p-4">
        <h3 className="font-medium flex items-center gap-2"><KeyRound size={16} /> API key (stored in OS keychain)</h3>
        <div className="flex gap-2">
          <Input
            type="password"
            placeholder="secret — stored via Keychain/DPAPI/libsecret, never in config"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
          />
          <Button onClick={storeKey} disabled={!draft.api_key_id || !secret}>Store</Button>
        </div>
      </Card>

      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
    </div>
  );
}
