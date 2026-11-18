// Bilingual copy map for the daemon's stable reason_code enum (issue #45 F1,
// contract in agent-studio #747). One entry per code: EN + CN operator copy
// plus at most one primary action. Unknown codes render the raw message.
export type ReasonCopy = {
  en: string;
  cn: string;
  action?: "start_lead" | "raise_budget" | "wait_or_inbox" | "rearm_wakeup";
};

export const REASON_CODES = [
  "queued",
  "coalesced",
  "team_not_registered",
  "member_unknown",
  "permission_mode_unknown",
  "invocation_not_allowed",
  "runtime_offline",
  "lead_not_running",
  "budget_exhausted",
  "depth_overflow",
  "task_not_found",
  "spec_unavailable",
  "supplement_cap_reached",
  "wakeup_timeout",
] as const;

export type ReasonCode = (typeof REASON_CODES)[number];

const COPY: Record<ReasonCode, ReasonCopy> = {
  queued: {
    en: "The request was accepted and is queued. Watch the task board for progress.",
    cn: "请求已接受并进入队列，请在任务面板关注进度。",
  },
  coalesced: {
    en: "A duplicate request was merged into the one already running.",
    cn: "重复请求已合并到正在执行的那一个。",
  },
  team_not_registered: {
    en: "This team is not registered on the daemon yet. Create it first.",
    cn: "该团队尚未在守护进程注册，请先创建。",
    action: "start_lead",
  },
  member_unknown: {
    en: "That member name is not on the team roster. Check teams.status.",
    cn: "成员名不在团队花名册中，请核对成员列表。",
  },
  permission_mode_unknown: {
    en: "The permission mode is not one of the documented values.",
    cn: "权限模式不是文档中的合法值。",
  },
  invocation_not_allowed: {
    en: "This operation is not allowed for your role in the current team state.",
    cn: "当前角色或团队状态下不允许执行此操作。",
  },
  runtime_offline: {
    en: "The agent runtime is offline. Start it and retry.",
    cn: "智能体运行时不可用，请启动后重试。",
  },
  lead_not_running: {
    en: "The lead loop for this team is not running. Start it to dispatch tasks.",
    cn: "该团队的 lead 循环未运行，请先启动以派发任务。",
    action: "start_lead",
  },
  budget_exhausted: {
    en: "The token budget for this run is exhausted. Raise the budget or resume later.",
    cn: "本轮 token 预算已用尽，请提高预算或稍后恢复。",
    action: "raise_budget",
  },
  depth_overflow: {
    en: "Orchestration nesting is too deep. Flatten the task chain.",
    cn: "编排嵌套过深，请简化任务链。",
  },
  task_not_found: {
    en: "That task id does not exist on this team.",
    cn: "该任务 ID 不存在。",
  },
  spec_unavailable: {
    en: "The team SPEC.md is not available. Seed it via teams.register or spec_write.",
    cn: "团队 SPEC.md 不可用，请先通过注册或 spec_write 生成。",
  },
  supplement_cap_reached: {
    en: "Steering is capped at 3 per run. Wait for the run to finish or send an inbox message.",
    cn: "每次运行最多纠偏 3 次，请等待运行结束或改为收件箱消息。",
    action: "wait_or_inbox",
  },
  wakeup_timeout: {
    en: "The task was cancelled because its wakeup expired. Re-arm a wakeup to retry.",
    cn: "任务因唤醒超时被取消，如需重试请重新设置唤醒。",
    action: "rearm_wakeup",
  },
};

export function reasonCopy(code: string): ReasonCopy | null {
  if (Object.prototype.hasOwnProperty.call(COPY, code)) {
    return COPY[code as ReasonCode];
  }
  return null;
}
