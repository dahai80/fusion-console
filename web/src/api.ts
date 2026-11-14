export type Provider = {
  id: string;
  name: string;
  type: "openai_compat" | "anthropic_compat" | "gateway";
  base_url: string;
  model: string;
  api_key_id: string;
  enabled: boolean;
};

export type AgentInfo = {
  name: string;
  command: string;
  detected: boolean;
  permission_mode: string;
  max_budget_usd: number;
  is_worker?: boolean;
  agent_type?: string;
};

export type Conversation = {
  ID: number;
  User: string;
  Title: string;
  Kind: "chat" | "agent";
  AgentName: string;
  Workdir: string;
  CreatedAt: string;
};

export type Message = {
  ID: number;
  ConvID: number;
  Role: string;
  Content: string;
  Meta: string;
  CreatedAt: string;
};

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    ...init,
  });
  if (res.status === 401) {
    window.dispatchEvent(new Event("fc:logout"));
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const body = await res.json();
      if (body.error) msg = body.error;
    } catch {
      /* keep default */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

export const api = {
  login: (user: string, password: string) =>
    req<{ user: string }>("/api/login", { method: "POST", body: JSON.stringify({ user, password }) }),
  autoLogin: (token: string) =>
    req<{ user: string }>("/api/autologin", { method: "POST", body: JSON.stringify({ token }) }),
  logout: () => req<{ ok: boolean }>("/api/logout", { method: "POST" }),
  authHint: () => req<{ user: string; factory_default: boolean }>("/api/auth/hint"),
  provisionGatewayKey: () =>
    req<{ ok: boolean; key_id: string; key_prefix: string; raw_key: string }>("/api/gateway/key", { method: "POST" }),
  me: () => req<{ user: string }>("/api/me"),
  conversations: () => req<{ conversations: Conversation[] }>("/api/conversations"),
  createConversation: (title: string, kind: "chat" | "agent", agentName = "", workdir = "") =>
    req<{ id: number }>("/api/conversations", {
      method: "POST",
      body: JSON.stringify({ title, kind, agent_name: agentName, workdir }),
    }),
  messages: (id: number) =>
    req<{ messages: Message[] }>(`/api/conversations/${id}/messages`),
  deleteConversation: (id: number) =>
    req<{ ok: boolean }>(`/api/conversations/${id}`, { method: "DELETE" }),
  renameConversation: (id: number, title: string) =>
    req<{ ok: boolean }>(`/api/conversations/${id}`, { method: "PATCH", body: JSON.stringify({ title }) }),
  agents: () => req<{ agents: AgentInfo[] }>("/api/agents"),
  providers: () => req<{ providers: Provider[] }>("/api/settings/providers"),
  saveProvider: (p: Provider) =>
    req<{ ok: boolean }>("/api/settings/providers", { method: "POST", body: JSON.stringify(p) }),
  deleteProvider: (id: string) =>
    req<{ ok: boolean }>(`/api/settings/providers/${encodeURIComponent(id)}`, { method: "DELETE" }),
  storeApiKey: (keyId: string, secret: string) =>
    req<{ ok: boolean }>("/api/settings/apikey", { method: "POST", body: JSON.stringify({ key_id: keyId, secret }) }),
  models: (provider: string) =>
    req<{ models: string[] }>(`/api/settings/models?provider=${encodeURIComponent(provider)}`),
  testCall: (provider: string) =>
    req<{ ok: boolean; sample?: string; error?: string; latency_ms?: number }>("/api/settings/testcall", {
      method: "POST",
      body: JSON.stringify({ provider_id: provider }),
    }),
  costs: () => req<{ report: Record<string, unknown>[] }>("/api/costs"),
  dashboard: (range: UsageRange) =>
    req<DashboardData>(`/api/dashboard?range=${encodeURIComponent(range)}`),
  approve: (id: string, approve: boolean) =>
    req<{ ok: boolean }>(`/api/approvals/${encodeURIComponent(id)}`, {
      method: "POST",
      body: JSON.stringify({ approve }),
    }),
  gatewayStatus: () =>
    req<{
      base_url: string;
      admin_url: string;
      reachable: boolean;
      installed: boolean;
      binary: string;
    }>("/api/gateway/status"),
  gatewayInstall: () =>
    req<{ ok: boolean; binary: string; version: string; note: string }>(
      "/api/gateway/install",
      { method: "POST" }
    ),
  gatewaySSO: () => req<{ ok: boolean }>("/api/gateway/sso", { method: "POST" }),
  gatewayBackends: () =>
    req<{ backends: GatewayBackend[] }>("/api/gateway/backends"),
  addGatewayBackend: (b: { name: string; type: string; base_url: string; api_key: string; models: string[] }) =>
    req<{ ok: boolean; note?: string }>("/api/gateway/backend", { method: "POST", body: JSON.stringify(b) }),
  deleteGatewayBackend: (name: string, models?: string[]) =>
    req<{ ok: boolean }>(`/api/gateway/backend/${encodeURIComponent(name)}${models?.length ? `?models=${encodeURIComponent(models.join(","))}` : ""}`, { method: "DELETE" }),
  gatewayKeys: () =>
    req<{ keys: GatewayKey[] }>("/api/gateway/keys"),
  deleteGatewayKey: (name: string) =>
    req<{ ok: boolean }>(`/api/gateway/keys/${encodeURIComponent(name)}`, { method: "DELETE" }),
  gatewayModels: () =>
    req<{ models: string[]; items: GatewayModelItem[] }>("/api/gateway/models"),
  skills: (repo?: string) =>
    req<{ skills: SkillEntry[] }>(`/api/skills${repo ? `?repo=${encodeURIComponent(repo)}` : ""}`),
  readSkill: (path: string, repo?: string) =>
    req<{ content: string }>(`/api/skills/read?path=${encodeURIComponent(path)}${repo ? `&repo=${encodeURIComponent(repo)}` : ""}`),
  saveSkill: (s: { name: string; description: string; body: string; scope: string; repo?: string }) =>
    req<{ ok: boolean; path: string }>("/api/skills", { method: "POST", body: JSON.stringify(s) }),
  deleteSkill: (name: string, scope: string, repo?: string) =>
    req<{ ok: boolean }>(`/api/skills/${encodeURIComponent(name)}?scope=${scope}${repo ? `&repo=${encodeURIComponent(repo)}` : ""}`, { method: "DELETE" }),
  draftSkill: (d: { hint: string; text: string }) =>
    req<{ content: string }>("/api/skills/draft", { method: "POST", body: JSON.stringify(d) }),
  importSkill: (source: string) =>
    req<{ content: string }>("/api/skills/import", { method: "POST", body: JSON.stringify({ source }) }),
  orchestration: () =>
    req<{ orchestrator: Orchestrator }>("/api/settings/orchestration"),
  saveOrchestration: (o: Orchestrator) =>
    req<{ ok: boolean }>("/api/settings/orchestration", { method: "POST", body: JSON.stringify(o) }),
};

export type WorkerSettings = {
  name?: string;
  agent: string;
  enabled: boolean;
  isolation: "worktree" | "standalone";
  permission_mode: string;
  max_budget_usd: number;
  provider_id: string;
  work_dir: string;
  repo?: string;
};

export type UsageRange = "today" | "week" | "month" | "all";

export type GatewayBackend = {
  name: string;
  type: string;
  base_url: string;
  api_key: string;
  enabled: boolean;
  models: string[];
};

export type GatewayKey = {
  name: string;
  key_prefix?: string;
  created_at?: string;
  in_use?: boolean;
};

// One selectable gateway model. value is what clients must send — bare id
// for local backends, qualified "backend/id" for cloud backends (the gateway
// pins qualified ids to their backend).
export type GatewayModelItem = {
  id: string;
  backend: string;
  value: string;
  local: boolean;
};

export type SkillEntry = {
  name: string;
  description: string;
  scope: "global" | "project";
  path: string;
};

export type DashboardBucket = {
  bucket: string;
  brain_tokens_in: number;
  brain_tokens_out: number;
  worker_tokens_in: number;
  worker_tokens_out: number;
  brain_cost_usd: number;
  worker_cost_usd: number;
};

export type DashboardData = {
  range: UsageRange;
  bucket_unit: "hour" | "day" | "month";
  buckets: DashboardBucket[];
  totals: {
    brain_tokens: number;
    worker_tokens: number;
    brain_cost_usd: number;
    worker_cost_usd: number;
  };
};

export type Orchestrator = {
  mode: "off" | "confirm" | "auto";
  brain_agent: string;
  repos: string[];
  workers: WorkerSettings[];
  configured: boolean;
};

// streamPOST performs a POST that yields SSE "data:" JSON events.
export async function streamPOST(
  path: string,
  body: unknown,
  onEvent: (ev: Record<string, unknown>) => void,
  signal?: AbortSignal
): Promise<void> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(body),
    signal,
  });
  if (res.status === 401) {
    window.dispatchEvent(new Event("fc:logout"));
    throw new Error("unauthorized");
  }
  if (!res.ok || !res.body) {
    let msg = `HTTP ${res.status}`;
    try {
      const j = await res.json();
      if (j.error) msg = j.error;
    } catch {
      /* keep default */
    }
    throw new Error(msg);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const parts = buf.split("\n\n");
    buf = parts.pop() ?? "";
    for (const part of parts) {
      const line = part.split("\n").find((l) => l.startsWith("data: "));
      if (!line) continue;
      try {
        onEvent(JSON.parse(line.slice(6)));
      } catch (err) {
        console.error("bad SSE payload", err);
      }
    }
  }
}
