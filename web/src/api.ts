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
  agents: () => req<{ agents: AgentInfo[] }>("/api/agents"),
  providers: () => req<{ providers: Provider[] }>("/api/settings/providers"),
  saveProvider: (p: Provider) =>
    req<{ ok: boolean }>("/api/settings/providers", { method: "POST", body: JSON.stringify(p) }),
  storeApiKey: (keyId: string, secret: string) =>
    req<{ ok: boolean }>("/api/settings/apikey", { method: "POST", body: JSON.stringify({ key_id: keyId, secret }) }),
  models: (provider: string) =>
    req<{ models: string[] }>(`/api/settings/models?provider=${encodeURIComponent(provider)}`),
  costs: () => req<{ report: Record<string, unknown>[] }>("/api/costs"),
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
