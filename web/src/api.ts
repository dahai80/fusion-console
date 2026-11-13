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
  orchestration: () =>
    req<{ orchestrator: Orchestrator }>("/api/settings/orchestration"),
  saveOrchestration: (o: Orchestrator) =>
    req<{ ok: boolean }>("/api/settings/orchestration", { method: "POST", body: JSON.stringify(o) }),
};

export type WorkerSettings = {
  agent: string;
  enabled: boolean;
  isolation: "worktree" | "standalone";
  permission_mode: string;
  max_budget_usd: number;
};

export type UsageRange = "today" | "week" | "month" | "all";

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
