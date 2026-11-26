// Only public chat stream events describe the Brain's actions in this tab.
export type BrainDispatch = {
  task: string;
  status: "dispatching" | "queued" | "done" | "failed";
};

export type BrainActivity = {
  conversation: number;
  request: string;
  phase: string;
  detail: string;
  pendingDispatch: string;
  lastHandoff: string;
  dispatches: BrainDispatch[];
  failed: boolean;
  finished: boolean;
};

export function beginBrainTurn(conversation: number, request: string): BrainActivity {
  return { conversation, request, phase: "Processing request", detail: "", pendingDispatch: "", lastHandoff: "", dispatches: [], failed: false, finished: false };
}

const toolLabels: Record<string, string> = {
  list_agents: "Checking available workers",
  run_agent: "Dispatching work",
  read_artifact: "Reading an artifact",
  dispatch_status: "Checking dispatches",
  create_team: "Setting up a team",
  team_send: "Sending a team message",
  team_inbox: "Checking team replies",
  kb_search: "Searching the knowledge base",
  kb_ask: "Querying the knowledge base",
  weekly_report: "Compiling the weekly report",
};

// Upsert one dispatch entry, matched by task brief. Fan-out turns emit
// several run_agent calls per turn — every one keeps its own row so the
// card shows the brain handing each issue to its worker step by step.
function upsertDispatch(list: BrainDispatch[], task: string, status: BrainDispatch["status"]): BrainDispatch[] {
  const idx = task ? list.findIndex((d) => d.task === task) : -1;
  if (idx >= 0) {
    const next = list.slice();
    next[idx] = { ...next[idx], status };
    return next;
  }
  if (!task) return list;
  return [...list, { task, status }];
}

// Mark the in-flight dispatch (no exact task match, e.g. the done event
// carries no brief) — with a single pending dispatch that is unambiguous.
function settlePending(list: BrainDispatch[], status: BrainDispatch["status"]): BrainDispatch[] {
  const idx = list.findIndex((d) => d.status === "dispatching");
  if (idx < 0) return list;
  const next = list.slice();
  next[idx] = { ...next[idx], status };
  return next;
}

export function brainEvent(activity: BrainActivity, ev: Record<string, unknown>): BrainActivity {
  const type = ev.type;
  const detail = typeof ev.detail === "string" ? ev.detail : "";
  const tool = typeof ev.tool === "string" ? ev.tool : "";
  if (type === "error") {
    return { ...activity, phase: "Request failed", failed: true, detail: String(ev.text || ""), pendingDispatch: "", dispatches: activity.dispatches.map((d) => d.status === "dispatching" ? { ...d, status: "failed" as const } : d) };
  }
  if (type === "approval") return { ...activity, phase: "Awaiting approval", detail };
  if (type === "delta") {
    if (activity.phase === "Writing reply" || activity.failed || !ev.text) return activity;
    return { ...activity, phase: "Writing reply", detail: "" };
  }
  if (type !== "tool") return activity;
  const action = toolLabels[tool] || (tool === "brain" ? "Using a tool" : tool);
  const brief = detail === tool ? "" : detail;
  switch (ev.status) {
    case "running":
      return {
        ...activity,
        phase: action,
        detail: brief,
        pendingDispatch: tool === "run_agent" ? brief : "",
        dispatches: tool === "run_agent" ? upsertDispatch(activity.dispatches, brief, "dispatching") : activity.dispatches,
      };
    case "progress":
      return { ...activity, phase: tool === "run_agent" ? "Worker update" : action, detail };
    case "queued": {
      const task = activity.pendingDispatch || detail;
      return {
        ...activity,
        phase: "Dispatch queued",
        detail: "",
        lastHandoff: task,
        pendingDispatch: "",
        dispatches: tool === "run_agent" ? upsertDispatch(activity.dispatches, task, "queued") : activity.dispatches,
      };
    }
    case "done": {
      const task = activity.pendingDispatch || detail;
      return {
        ...activity,
        phase: tool === "run_agent" ? "Worker returned" : "Processing tool result",
        detail: task || action,
        pendingDispatch: "",
        dispatches: tool === "run_agent" ? upsertDispatch(activity.dispatches, task, "done") : activity.dispatches,
      };
    }
    case "blocked":
    case "denied":
    case "error":
      return {
        ...activity,
        phase: action + " · " + ev.status,
        detail,
        pendingDispatch: "",
        dispatches: tool === "run_agent" ? upsertDispatch(activity.dispatches, activity.pendingDispatch || brief, "failed") : activity.dispatches,
      };
    default:
      return activity;
  }
}

export function endBrainTurn(activity: BrainActivity): BrainActivity {
  return {
    ...activity,
    finished: true,
    phase: activity.failed ? activity.phase : "Turn finished",
    detail: activity.failed ? activity.detail : "",
    pendingDispatch: "",
    // A dispatch brief sent but never confirmed (stream cut) stays honest.
    dispatches: activity.dispatches.map((d) => d.status === "dispatching" ? { ...d, status: "failed" as const } : d),
  };
}
