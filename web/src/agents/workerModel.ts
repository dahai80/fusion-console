import type { AgentInfo, Orchestrator, WorkerSettings, WorkerStatus } from "../api";

// Pure helpers behind the Agents page: worker drafts, validation, the
// config/status merge and display strings. Kept free of React so they can be
// unit-tested with node --test.

export type Place = "github" | "worktree" | "standalone";

export type WorkerDraft = {
  agent: string;
  place: Place;
  repoUrl: string;
  branch: string;
  repoPath: string;
  name: string;
  workDir: string;
  provider: string;
  model: string;
  budget: number;
  memory: string;
};

export type WorkerRow = {
  key: string;
  cfg: WorkerSettings;
  st?: WorkerStatus;
};

const TOOL_LABELS: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex",
  opencode: "OpenCode",
  "fusion-code": "Fusion Code",
  atlascode: "AtlasCode",
};

export const toolLabel = (agent: string) => TOOL_LABELS[agent.toLowerCase()] ?? agent;

export const workerKey = (w: Pick<WorkerSettings, "name" | "agent">) => (w.name || "").trim() || w.agent;

export function emptyOrchestrator(): Orchestrator {
  return { mode: "confirm", brain_agent: "claude", repos: [], workers: [], configured: false, worker_memory: "notes" };
}

// Tools a worker can run on: the configured CLI adapters, not the worker
// entries the agents endpoint also lists. Detected tools come first.
export function toolChoices(agents: AgentInfo[]): AgentInfo[] {
  return agents.filter((a) => !a.is_worker).sort((a, b) => Number(b.detected) - Number(a.detected));
}

export function nextName(agent: string, workers: Pick<WorkerSettings, "name" | "agent">[]): string {
  const taken = new Set(workers.map(workerKey));
  let n = workers.filter((w) => w.agent === agent).length + 1;
  while (taken.has(`${agent}-${n}`)) n++;
  return `${agent}-${n}`;
}

export function emptyDraft(agents: AgentInfo[]): WorkerDraft {
  const first = toolChoices(agents)[0];
  return {
    agent: first?.detected ? first.name : "",
    place: "standalone",
    repoUrl: "",
    branch: "",
    repoPath: "",
    name: "",
    workDir: "",
    provider: "",
    model: "",
    budget: 5,
    memory: "notes",
  };
}

export function draftFrom(w: WorkerSettings): WorkerDraft {
  const place: Place = w.isolation === "github" || w.isolation === "standalone" ? w.isolation : "worktree";
  return {
    agent: w.agent,
    place,
    repoUrl: place === "github" ? w.repo ?? "" : "",
    branch: place === "github" ? w.repo_branch ?? "" : "",
    repoPath: place === "worktree" ? w.repo ?? "" : "",
    name: w.name ?? "",
    workDir: w.work_dir ?? "",
    provider: w.provider_id ?? "",
    model: w.model ?? "",
    budget: w.max_budget_usd ?? 5,
    memory: w.memory || "notes",
  };
}

const isRepoUrl = (s: string) => /^(https?:\/\/|git@|ssh:\/\/)/.test(s);

// validate returns a user-facing message, or "" when the draft can be saved.
// `self` is the key of the worker being edited (its own name is not a clash).
export function validate(d: WorkerDraft, workers: Pick<WorkerSettings, "name" | "agent">[], self?: string): string {
  if (!d.agent) return "先选一个工具";
  if (d.place === "github") {
    if (!d.repoUrl.trim()) return "填一下 GitHub 仓库地址";
    if (!isRepoUrl(d.repoUrl.trim())) return "仓库地址要以 https:// 或 git@ 开头";
  }
  if (d.place === "worktree" && !d.repoPath.trim().startsWith("/")) return "本机仓库要填完整路径，以 / 开头";
  const name = d.name.trim();
  if (name && !/^[\w.-]+$/.test(name)) return "名字只能用字母、数字、- _ .，方便在对话里 @ 它";
  const finalName = name || nextName(d.agent, workers);
  if (workers.some((w) => workerKey(w) === finalName && workerKey(w) !== self)) return `已经有叫 ${finalName} 的 Worker 了`;
  if (!(d.budget > 0)) return "每次花费上限要大于 0";
  return "";
}

// toSettings turns a draft into the stored shape. Fields the form does not
// edit (permission mode, enabled) carry over from `base`.
export function toSettings(d: WorkerDraft, workers: Pick<WorkerSettings, "name" | "agent">[], base?: WorkerSettings): WorkerSettings {
  return {
    ...base,
    name: d.name.trim() || base?.name || nextName(d.agent, workers),
    agent: d.agent,
    enabled: base?.enabled ?? true,
    isolation: d.place,
    permission_mode: base?.permission_mode || "acceptEdits",
    max_budget_usd: d.budget,
    provider_id: d.provider,
    model: d.model.trim(),
    work_dir: d.workDir.trim(),
    repo: d.place === "github" ? d.repoUrl.trim() : d.place === "worktree" ? d.repoPath.trim() : "",
    repo_branch: d.place === "github" ? d.branch.trim() : "",
    memory: d.memory,
  };
}

// repoLabel shortens a repo reference for display: owner/name for remote
// URLs, the folder name for local paths.
export function repoLabel(repo: string): string {
  const s = repo.trim().replace(/\/+$/, "").replace(/\.git$/, "");
  if (!s) return "";
  const remote = s.match(/[:/]([^/:]+\/[^/:]+)$/);
  if (isRepoUrl(s) && remote) return remote[1];
  return s.split("/").pop() || s;
}

export function placeText(w: Pick<WorkerSettings, "isolation" | "repo">): string {
  const repo = repoLabel(w.repo ?? "");
  if (w.isolation === "github") return repo ? `GitHub · ${repo}` : "GitHub 仓库";
  if (w.isolation === "standalone") return "独立文件夹";
  return repo ? `本机仓库 · ${repo}` : "本机仓库";
}

// mergeRows pairs each configured worker with its live status (matched by
// name) and orders the list: working now (longest first), then idle, then
// disabled.
export function mergeRows(workers: WorkerSettings[], status: WorkerStatus[]): WorkerRow[] {
  const byName = new Map(status.map((s) => [s.name, s]));
  const rank = (r: WorkerRow) => (r.st?.busy ? 0 : r.cfg.enabled ? 1 : 2);
  return workers
    .map((cfg) => ({ key: workerKey(cfg), cfg, st: byName.get(workerKey(cfg)) }))
    .sort((a, b) => rank(a) - rank(b) || (b.st?.elapsed_s ?? 0) - (a.st?.elapsed_s ?? 0));
}

export function elapsedText(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s} 秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} 分 ${s % 60} 秒`;
  return `${Math.floor(m / 60)} 小时 ${m % 60} 分`;
}

// lastRun is stored as "YYYY-MM-DD HH:MM:SS" in UTC.
export function agoText(lastRun: string, now = Date.now()): string {
  if (!lastRun) return "";
  const t = Date.parse(lastRun.replace(" ", "T") + (/[zZ]|[+-]\d\d:?\d\d$/.test(lastRun) ? "" : "Z"));
  if (Number.isNaN(t)) return lastRun;
  const m = Math.floor(Math.max(0, now - t) / 60000);
  if (m < 1) return "刚刚";
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  return d < 30 ? `${d} 天前` : lastRun.slice(0, 10);
}

export function tokensText(n: number): string {
  if (n >= 1e8) return `${(n / 1e8).toFixed(1)} 亿`;
  if (n >= 1e4) return `${(n / 1e4).toFixed(n >= 1e6 ? 0 : 1)} 万`;
  return String(n);
}

export function summaryText(d: WorkerDraft, workers: Pick<WorkerSettings, "name" | "agent">[]): string {
  if (!d.agent) return "";
  const name = d.name.trim() || nextName(d.agent, workers);
  const github = repoLabel(d.repoUrl), local = repoLabel(d.repoPath);
  const where =
    d.place === "github" ? (github ? `在 GitHub 仓库 ${github} 里` : "在一个 GitHub 仓库里")
    : d.place === "worktree" ? (local ? `在本机仓库 ${local} 里` : "在一个本机仓库里")
    : "在自己的文件夹里";
  return `${name} 用 ${toolLabel(d.agent)}，${where}干活，每次最多花 $${d.budget}。`;
}
