export type Provider = {
  id: string;
  name: string;
  type: "openai_compat" | "anthropic_compat" | "gateway";
  base_url: string;
  model: string;
  models?: string[];
  api_key_id: string;
  enabled: boolean;
  lifecycle?: "fusion-gateway" | "macfabric" | "";
};

export type ModelCatalogItem = {
  provider_id: string;
  provider_name: string;
  kind: "gateway" | "direct";
  model: string;
  reachable: boolean;
  lifecycle?: string;
};

export type ChatDefault = { default_provider_id?: string; default_model?: string };

export type AgentInfo = {
  name: string;
  command: string;
  detected: boolean;
  permission_mode: string;
  max_budget_usd: number;
  model?: string;
  protocol?: string;
  max_budget_tokens?: number;
  provider_id?: string;
  is_worker?: boolean;
  agent_type?: string;
};

export type Conversation = {
  ID: number;
  User: string;
  Title: string;
  Kind: "chat" | "agent" | "team";
  AgentName: string;
  Workdir: string;
  ProviderID?: string;
  Model?: string;
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

// ApiError carries the daemon's structured reason_code (issue #45 F1) when
// the backend classified the failure; plain Error otherwise.
export class ApiError extends Error {
  status?: number;
  reason_code?: string;
  hint?: string;
}

function toApiError(status: number, body: { error?: string; reason_code?: string; hint?: string } | null): ApiError {
  const err = new ApiError(body?.error || `HTTP ${status}`);
  err.status = status;
  err.reason_code = body?.reason_code;
  err.hint = body?.hint;
  return err;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  // FormData bodies must keep the browser-generated multipart Content-Type.
  const isForm = typeof FormData !== "undefined" && init?.body instanceof FormData;
  const res = await fetch(path, {
    headers: isForm ? {} : { "Content-Type": "application/json" },
    credentials: "same-origin",
    ...init,
  });
  if (res.status === 401) {
    window.dispatchEvent(new Event("fc:logout"));
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let body: { error?: string; reason_code?: string; hint?: string } | null = null;
    try {
      body = await res.json();
    } catch {
      /* keep default */
    }
    throw toApiError(res.status, body);
  }
  return res.json() as Promise<T>;
}

export const api = {
  login: (user: string, password: string) =>
    req<{ user: string }>("/api/login", { method: "POST", body: JSON.stringify({ user, password }) }),
  memoryStatus: () =>
    req<{ mode: string; healthy: boolean; outbox_pending: number }>("/api/memory/status"),
  autoLogin: (token: string) =>
    req<{ user: string }>("/api/autologin", { method: "POST", body: JSON.stringify({ token }) }),
  logout: () => req<{ ok: boolean }>("/api/logout", { method: "POST" }),
  authHint: () => req<{ user: string; factory_default: boolean }>("/api/auth/hint"),
  provisionGatewayKey: () =>
    req<{ ok: boolean; key_id: string; key_prefix: string; raw_key: string }>("/api/gateway/key", { method: "POST" }),
  me: () => req<{ user: string }>("/api/me"),
  conversations: () => req<{ conversations: Conversation[] }>("/api/conversations"),
  createConversation: (title: string, kind: "chat" | "agent" | "team", agentName = "", workdir = "") =>
    req<{ id: number }>("/api/conversations", {
      method: "POST",
      body: JSON.stringify({ title, kind, agent_name: agentName, workdir }),
    }),
  messages: (id: number) =>
    req<{ messages: Message[] }>(`/api/conversations/${id}/messages`),
  conversationTurn: (id: number) =>
    req<{ turn: ChatTurn | null }>(`/api/conversations/${id}/turn`, { cache: "no-store" }),
  deleteConversation: (id: number) =>
    req<{ ok: boolean }>(`/api/conversations/${id}`, { method: "DELETE" }),
  renameConversation: (id: number, title: string) =>
    req<{ ok: boolean }>(`/api/conversations/${id}`, { method: "PATCH", body: JSON.stringify({ title }) }),
  setConversationModel: (id: number, providerId: string, model: string) =>
    req<{ ok: boolean }>(`/api/conversations/${id}/model`, {
      method: "PUT",
      body: JSON.stringify({ provider_id: providerId, model }),
    }),
  agents: () => req<{ agents: AgentInfo[] }>("/api/agents"),
  agentRun: (p: { conversation_id: number; agent: string; task: string; workdir?: string; background?: boolean; requires_repo?: boolean; repo?: string }) =>
    req<{ ok: boolean; note?: string }>("/api/agent/run", { method: "POST", body: JSON.stringify(p) }),
  providers: () => req<{ providers: Provider[] }>("/api/settings/providers"),
  saveProvider: (p: Provider) =>
    req<{ ok: boolean; warning?: string; base_url?: string }>("/api/settings/providers", {
      method: "POST",
      body: JSON.stringify(p),
    }),
  deleteProvider: (id: string) =>
    req<{ ok: boolean }>(`/api/settings/providers/${encodeURIComponent(id)}`, { method: "DELETE" }),
  storeApiKey: (keyId: string, secret: string) =>
    req<{ ok: boolean }>("/api/settings/apikey", { method: "POST", body: JSON.stringify({ key_id: keyId, secret }) }),
  models: (provider: string) =>
    req<{ models: string[] }>(`/api/settings/models?provider=${encodeURIComponent(provider)}`),
  modelCatalog: () => req<{ items: ModelCatalogItem[] }>("/api/model-catalog"),
  mlxStatus: () =>
    req<{ supported: boolean; installed: boolean; enabled: boolean; healthy: boolean; port: number; model: string; install_dir: string; ram_gb: number; chat_default: boolean; recommendations: { model: string; params: string; gb: number; tier: string }[] }>("/api/mlx/status"),
  mlxOp: (p: { op: string; model?: string }) =>
    req<{ ok: boolean; note?: string }>("/api/mlx/op", { method: "POST", body: JSON.stringify(p) }),
  setChatDefault: (providerId: string, model: string) =>
    req<{ ok: boolean }>("/api/settings/chat-default", {
      method: "POST",
      body: JSON.stringify({ provider_id: providerId, model }),
    }),
  sourceAction: (id: string, action: "start" | "stop" | "install") =>
    req<{ ok: boolean; note?: string }>(`/api/sources/${encodeURIComponent(id)}/action`, {
      method: "POST",
      body: JSON.stringify({ action }),
    }),
  sourceInstallJob: () =>
    req<{ phase: string; logs: string[]; error: string }>("/api/sources/install-job"),
  gatewayStart: () => req<{ ok: boolean; note?: string }>("/api/gateway/start", { method: "POST" }),
  gatewayStop: () => req<{ ok: boolean; note?: string }>("/api/gateway/stop", { method: "POST" }),
  testCall: (provider: string) =>
    req<{ ok: boolean; sample?: string; error?: string; latency_ms?: number }>("/api/settings/testcall", {
      method: "POST",
      body: JSON.stringify({ provider_id: provider }),
    }),
  costs: () => req<{ report: Record<string, unknown>[] }>("/api/costs"),
  dashboard: (range: UsageRange) =>
    req<DashboardData>(`/api/dashboard?range=${encodeURIComponent(range)}`),
  // Year of per-day, per-participant tokens bucketed in the viewer's time zone.
  dashboardSeries: () =>
    req<UsageSeriesData>(`/api/dashboard/series?tz=${-new Date().getTimezoneOffset()}`),
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
    req<{ content: string; doc: SkillDoc }>(`/api/skills/read?path=${encodeURIComponent(path)}${repo ? `&repo=${encodeURIComponent(repo)}` : ""}`),
  saveSkill: (s: { name: string; description: string; extra: string; body: string; scope: string; repo?: string }) =>
    req<{ ok: boolean; path: string }>("/api/skills", { method: "POST", body: JSON.stringify(s) }),
  deleteSkill: (name: string, scope: string, repo?: string) =>
    req<{ ok: boolean }>(`/api/skills/${encodeURIComponent(name)}?scope=${scope}${repo ? `&repo=${encodeURIComponent(repo)}` : ""}`, { method: "DELETE" }),
  draftSkill: (d: { hint: string; text: string }) =>
    req<{ content: string; doc: SkillDoc; warnings: string[] | null }>("/api/skills/draft", { method: "POST", body: JSON.stringify(d) }),
  importSkill: (source: string) =>
    req<SkillImport>("/api/skills/import", { method: "POST", body: JSON.stringify({ source }) }),
  localImportSkill: (path: string) =>
    req<SkillImport>("/api/skills/local-import", { method: "POST", body: JSON.stringify({ path }) }),
  orchestration: () =>
    req<{ orchestrator: Orchestrator }>("/api/settings/orchestration"),
  cavemanStatus: () =>
    req<CavemanStatus>("/api/caveman"),
  saveOrchestration: (o: Orchestrator) =>
    req<{ ok: boolean }>("/api/settings/orchestration", { method: "POST", body: JSON.stringify(o) }),
  ragStatus: () => req<RagStatus>("/api/rag/status"),
  studioStatus: () => req<StudioStatus>("/api/studio/status"),
  studioInstall: (method?: "auto" | "workspace" | "pypi" | "brew") =>
    req<{ status: string }>("/api/studio/install", { method: "POST", body: JSON.stringify({ method }) }),
  ragInstall: () => req<{ status: string }>("/api/rag/install", { method: "POST" }),
  ragStart: () => req<{ running: boolean }>("/api/rag/start", { method: "POST" }),
  ragStop: () => req<{ running: boolean }>("/api/rag/stop", { method: "POST" }),
  ragSetEnabled: (enabled: boolean) =>
    req<{ enabled: boolean }>("/api/rag/enabled", { method: "POST", body: JSON.stringify({ enabled }) }),
  workersStatus: () => req<{ workers: WorkerStatus[] }>("/api/workers/status"),
  uploadAttachment: (file: File) =>
    req<{ name: string; size: number; stored: string; path: string }>("/api/attachments", {
      method: "POST",
      body: (() => { const f = new FormData(); f.append("file", file); return f; })(),
    }),
  readAttachment: (stored: string, offset: number, maxBytes: number) =>
    req<{
      stored: string; total: number; offset: number; bytes_read: number;
      next_offset: number; encoding: string; content: string; eof: boolean;
    }>(`/api/attachments/read?stored=${encodeURIComponent(stored)}&offset=${offset}&max_bytes=${maxBytes}`),
  dispatches: () => req<{ dispatches: DispatchRun[]; unread_by_conv: Record<string, number> }>("/api/dispatches"),
  ackDispatches: (conversationId: number) =>
    req<{ ok: boolean }>("/api/dispatches/ack", { method: "POST", body: JSON.stringify({ conversation_id: conversationId }) }),
  dismissDispatch: (id: number) =>
    req<{ ok: boolean }>(`/api/dispatches/${id}/dismiss`, { method: "POST" }),
  clearDispatches: () => req<{ ok: boolean }>("/api/dispatches/clear", { method: "POST" }),
  harnessReport: (convId: number) =>
    req<HarnessReport>(`/api/harness/report?conv_id=${convId}`),
  failuresList: () => req<{ failures: FailureRow[] }>("/api/failures"),
  failuresAdd: (body: { agent?: string; category: string; note: string }) =>
    req<{ ok: boolean }>("/api/failures", { method: "POST", body: JSON.stringify(body) }),
  teamView: (team?: string) =>
    req<TeamSnapshot>(`/api/team/view${team ? `?team=${encodeURIComponent(team)}` : ""}`),
    teamChatPoll: (id: number) =>
    req<{ messages: { role: string; content: string; meta?: string }[] }>(`/api/team/chat_poll?conversation_id=${id}`),
  teamTeams: () => req<{ teams: Record<string, any>[] }>("/api/team/teams"),
  // Run replay (issue #45 F2): read-only proxies over the daemon RPCs.
  teamRuns: (team: string | undefined, taskId: string) =>
    req<Record<string, any>>(`/api/team/runs?task_id=${encodeURIComponent(taskId)}${team ? `&team=${encodeURIComponent(team)}` : ""}`),
  teamRunMessages: (team: string | undefined, taskId: string, attempt: number) =>
    req<Record<string, any>>(`/api/team/run_messages?task_id=${encodeURIComponent(taskId)}&attempt=${attempt}${team ? `&team=${encodeURIComponent(team)}` : ""}`),
  teamRunUsage: (team: string | undefined, taskId: string) =>
    req<Record<string, any>>(`/api/team/run_usage?task_id=${encodeURIComponent(taskId)}${team ? `&team=${encodeURIComponent(team)}` : ""}`),
  teamCreate: (body: TeamCreateReq) =>
    req<TeamCreateRes>("/api/team/create", { method: "POST", body: JSON.stringify(body) }),
  teamOp: (body: { op: string; task_id?: string; entry_id?: string; max_concurrent?: number; max_depth?: number; agent_id?: string; name?: string; team?: string; status?: string; capabilities?: string[]; goal?: string; max_rounds?: number; team_budget?: unknown; cleanup_inboxes?: boolean; text?: string; wakeup?: Record<string, unknown>; message_id?: string; decision?: string; answered?: boolean; }) =>
    req<Record<string, any>>("/api/team/op", { method: "POST", body: JSON.stringify(body) }),
  codeStatus: () => req<CodeStatus>("/api/code/status"),
  memoryHubStatus: () => req<MemoryHubStatus>("/api/memoryhub/status"),
  feedbackEnv: () => req<Record<string, string>>("/api/feedback/env"),
  feedbackDraft: (rough: string, component: string) =>
    req<FeedbackDraft>("/api/feedback/draft", { method: "POST", body: JSON.stringify({ rough, component }) }),
  feedbackDraftLatest: (conversationId: number) =>
    req<FeedbackDraft>(`/api/feedback/draft_latest?conversation_id=${conversationId}`),
  feedbackSubmit: (component: string, title: string, body: string) =>
    req<{ url: string; repo: string }>("/api/feedback/submit", { method: "POST", body: JSON.stringify({ component, title, body }) }),
  marketRegistry: () => req<MarketRegistry>("/api/market/registry"),
  marketInstall: (id: string, overwrite = false) =>
    req<MarketInstallResult>("/api/market/install", { method: "POST", body: JSON.stringify({ id, overwrite }) }),
  memoryHubInstall: () =>
    req<{ status: string }>("/api/memoryhub/install", { method: "POST", body: "{}" }),
  memoryHubStart: () => req<{ running: boolean }>("/api/memoryhub/start", { method: "POST", body: "{}" }),
  memoryHubStop: () => req<{ running: boolean }>("/api/memoryhub/stop", { method: "POST", body: "{}" }),
  codeInstall: (agent: string) =>
    req<{ status: string }>("/api/code/install", { method: "POST", body: JSON.stringify({ agent }) }),
  codeStart: () => req<{ running: boolean }>("/api/code/start", { method: "POST" }),
  codeStop: () => req<{ running: boolean }>("/api/code/stop", { method: "POST" }),
  codeSetEnabled: (enabled: boolean) =>
    req<{ enabled: boolean }>("/api/code/enabled", { method: "POST", body: JSON.stringify({ enabled }) }),
  codeSetKey: (providerId: string) =>
    req<{ ok: boolean; env_vars: string[] }>("/api/code/key", { method: "POST", body: JSON.stringify({ provider_id: providerId }) }),
};

export type TeamSnapshot = {
  fetched_at: string;
  connected: boolean;
  error?: string;
  tasks: Record<string, any>[];
  roster: Record<string, any>[];
  health?: Record<string, any>;
  limits?: Record<string, any>;
  brief_warnings: Record<string, { item: string; fix: string }[]>;
  alerts: string[];
  lifecycle?: string;
};

// teams.lead_status result (lead loop on the daemon, issue #17).
export type LeadStatus = {
  running: boolean;
  lead_id?: string;
  round?: number;
  n_stalls?: number;
  replan_count?: number;
  last_verdict?: string;
};

// Team creation wizard payload (issue #16). Field names mirror the kernel
// TeamDefinition/TeamMember exactly — the review step shows this verbatim.
export type TeamCreateMember = {
  name: string;
  role: string;
  skills: string[];
  tool_allowlist: string[];
  permission_mode: string;
  budget_tokens: number;
  max_concurrency: number;
  // Resident member context (issue #41): role description + key worker
  // conventions, injected into every member turn by the harness.
  context?: string;
};

export type TeamCreateReq = {
  team: {
    name: string;
    namespace_key: string;
    members: TeamCreateMember[];
    lead_budget_tokens: number;
    playbook: boolean;
    max_depth: number;
    metadata: Record<string, unknown>;
  };
  tasks: { title: string; detail: string }[];
  lead_start: boolean;
  lead_goal: string;
  // Optional initial SPEC.md body — seeded by the daemon at register time.
  spec?: string;
};

// One queued task's outcome — a rejected task is an entry with an error,
// never a dropped row (partial success stays visible).
export type TeamCreateTaskResult = {
  index: number;
  title: string;
  task_id?: string;
  error?: string;
};

export type TeamCreateRes = {
  status: string;
  team: string;
  namespace_key: string;
  tasks: TeamCreateTaskResult[];
  lead_started: boolean;
  lead_error?: string;
};

export type FailureRow = {
  id: number;
  conv_id: number;
  agent: string;
  category: string;
  note: string;
  created_at: string;
};

export type WorkerStatus = {
  name: string;
  agent: string;
  enabled: boolean;
  detected: boolean;
  isolation: string;
  work_dir: string;
  repo: string;
  provider_id: string;
  max_budget_usd: number;
  calls: number;
  cost_usd: number;
  tokens_in: number;
  tokens_out: number;
  last_run: string;
  // Live dispatch state (from the in-memory run registry).
  busy?: boolean;
  task?: string;
  detail?: string;
  elapsed_s?: number;
  last_task?: string;
  last_elapsed_s?: number;
};

// One background dispatch (run_agent with background=true), newest first.
export type DispatchRun = {
  id: number;
  worker: string;
  agent: string;
  task: string;
  conversation_id: number;
  user: string;
  requires_repo?: boolean;
  repo?: string;
  status: "queued" | "running" | "done" | "blocked" | "error";
  started_at: string;
  finished_at: string;
  elapsed_s: number;
  cost_usd: number;
  tokens_in: number;
  tokens_out: number;
  detail: string;
  result: string;
  error?: string;
  read: boolean;
};

// Harness sidecar report (issue #78): funnel snapshot + failure attribution
// for one conversation, fetched on demand from the run view.
export type HarnessReportEvent = {
  seq: number; ts: number; domain: string; event: string;
  payload?: Record<string, unknown>; session_id?: string;
};
export type HarnessReportRun = {
  verdicts?: { mode?: string; cluster?: string; agent?: string }[];
  responsible_agent?: string;
  decisive_moment?: Record<string, unknown>;
  handoff_candidates?: Record<string, unknown>[];
};
export type HarnessReport = {
  snapshot: {
    llm_meter?: Record<string, unknown>;
    budget: { spent: number; limit: number; exceeded: boolean };
    events: HarnessReportEvent[];
  };
  attribution: {
    runs: Record<string, HarnessReportRun>;
    mode_counts: Record<string, number>;
    cluster_counts: Record<string, number>;
  };
};

export type StudioStatus = {
  installed: boolean; daemon_running: boolean; enabled: boolean;
  dir: string; socket: string; phase: string; logs?: string[]; error?: string;
};

export type RagStatus = {
  installed: boolean;
  enabled: boolean;
  running: boolean;
  phase: "idle" | "downloading" | "extracting" | "installing" | "done" | "error" | string;
  logs?: string[];
  error?: string;
  port: number;
};

export type CodeAgentRow = {
  id: string;
  name: string;
  installed: boolean;
  detail?: string;
  running: boolean;
};

export type CodeStatus = {
  agents: CodeAgentRow[];
  fusion: {
    phase: "idle" | "downloading" | "extracting" | "installing" | "done" | "error" | string;
    logs?: string[];
    error?: string;
    port: number;
    web_port: number;
    key_configured: boolean;
  };
};

export type MarketEntry = {
  id: string;
  name: string;
  kind: "plugin" | "connector" | "skill" | "app" | string;
  category?: string;
  source?: string;
  description?: { zh?: string; en?: string };
  releases_url?: string;
  doc_url?: string;
  version?: string;
  installable?: boolean;
  installed?: MarketInstalled;
};

export type MarketInstalled = {
  id?: string;
  version?: string;
  anchor: "sha256" | "commit" | "local" | string;
  digest?: string;
  installed_at?: string;
  link?: boolean;
};

export type MarketInstallResult = {
  name: string;
  path: string;
  installed: MarketInstalled;
  warnings: string[];
};

export type MarketRegistry = {
  version: number;
  entries: MarketEntry[];
  fetched_at: string;
  cached: boolean;
  stale?: boolean;
};

export type FeedbackDraft = {
  title: string;
  steps: string;
  expect: string;
  actual: string;
  extra: string;
  component: string;
};

export type MemoryHubStatus = {
  installed: boolean;
  enabled: boolean;
  running: boolean;
  detail?: string;
  phase: "idle" | "downloading" | "installing" | "done" | "error" | string;
  logs?: string[];
  error?: string;
  port: number;
};

export type WorkerSettings = {
  name?: string;
  agent: string;
  enabled: boolean;
  isolation: "github" | "worktree" | "standalone";
  permission_mode: string;
  max_budget_usd: number;
  provider_id: string;
  model?: string;
  work_dir: string;
  repo?: string;
  repo_branch?: string;
  memory?: string;
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

// SkillDoc is a SKILL.md parsed server-side: name/description are edited in
// the form, extra holds every other frontmatter line verbatim.
export type SkillDoc = {
  name: string;
  description: string;
  extra: string;
  body: string;
};

export type SkillImport = {
  content: string;
  doc: SkillDoc;
  url: string;
  via_mirror: boolean;
  warnings: string[] | null;
  missing_files: string[] | null;
};

export type DashboardBucket = {
  brain_measured: number;
  worker_measured: number;
  bucket: string;
  brain_tokens_in: number;
  brain_tokens_out: number;
  worker_tokens_in: number;
  worker_tokens_out: number;
  brain_cost_usd: number;
  worker_cost_usd: number;
};

export type UsageParticipant = {
  role: "brain" | "worker"; name: string; records: number; measured: number; tokens_in: number; tokens_out: number;
};
export type UsageSeriesData = {
  tz_offset_min: number;
  today: string;
  start: string;
  first_day: string;
  series: { name: string; role: "brain" | "worker"; total: number }[];
  days: { day: string; total: number; records: number; measured: number; values: number[] }[];
  hours: { today: number[]; yesterday: number[] };
};
export type DashboardData = {
  start: string;
  end: string;
  daily: { day: string; tokens: number; measured: number }[];
  participants: UsageParticipant[];
  range: UsageRange;
  bucket_unit: "hour" | "day" | "month";
  buckets: DashboardBucket[];
  totals: {
    records: number;
    measured: number;
    brain_tokens: number;
    worker_tokens: number;
    brain_cost_usd: number;
    worker_cost_usd: number;
  };
};

export type CavemanStatus = {
  enabled: boolean;
  level: string;
  source: string;
  ruleset_bytes: number;
  targets: string[];
};

export type Orchestrator = {
  mode: "off" | "confirm" | "auto";
  brain_agent: string;
  repos: string[];
  workers: WorkerSettings[];
  configured: boolean;
  worker_memory?: "notes" | "fusion-memory" | "off";
  brain_work_dir?: string;
  brain_repo?: string;
  brain_repo_branch?: string;
  brain_mode?: "chat" | "agent";
  brain_context_files?: string[];
  brain_provider_id?: string;
  brain_model?: string;
};

// ChatTurn is the server's record of the latest streamed chat / agent turn
// of a conversation: it keeps running when the stream drops, and the browser
// replays its events to pick the reply back up.
export type ChatTurn = {
  id: string;
  kind: "chat" | "agent";
  request: string;
  running: boolean;
  started_at: string;
  ended_at?: string;
  events: Record<string, unknown>[];
  truncated?: boolean;
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
    let body: { error?: string; reason_code?: string; hint?: string } | null = null;
    try {
      body = await res.json();
    } catch {
      /* keep default */
    }
    throw toApiError(res.status, body);
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
