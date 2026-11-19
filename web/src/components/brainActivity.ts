// Only public chat stream events describe the Brain's actions in this tab.
export type BrainActivity = {
  conversation: number;
  request: string;
  phase: string;
  detail: string;
  pendingDispatch: string;
  lastHandoff: string;
  failed: boolean;
  finished: boolean;
};

export function beginBrainTurn(conversation: number, request: string): BrainActivity {
  return { conversation, request, phase: "Processing request", detail: "", pendingDispatch: "", lastHandoff: "", failed: false, finished: false };
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
};

export function brainEvent(activity: BrainActivity, ev: Record<string, unknown>): BrainActivity {
  const type = ev.type;
  const detail = typeof ev.detail === "string" ? ev.detail : "";
  const tool = typeof ev.tool === "string" ? ev.tool : "";
  if (type === "error") return { ...activity, phase: "Request failed", failed: true, detail: String(ev.text || ""), pendingDispatch: "" };
  if (type === "approval") return { ...activity, phase: "Awaiting approval", detail };
  if (type === "delta") {
    if (activity.phase === "Writing reply" || activity.failed || !ev.text) return activity;
    return { ...activity, phase: "Writing reply", detail: "" };
  }
  if (type !== "tool") return activity;
  const action = toolLabels[tool] || (tool === "brain" ? "Using a tool" : tool);
  switch (ev.status) {
    case "running":
      return { ...activity, phase: action, detail: detail === tool ? "" : detail, pendingDispatch: tool === "run_agent" ? detail : "" };
    case "progress":
      return { ...activity, phase: tool === "run_agent" ? "Worker update" : action, detail };
    case "queued":
      return { ...activity, phase: "Dispatch queued", detail: "", lastHandoff: activity.pendingDispatch || detail, pendingDispatch: "" };
    case "done":
      return { ...activity, phase: tool === "run_agent" ? "Worker returned" : "Processing tool result", detail: activity.pendingDispatch || detail || action, pendingDispatch: "" };
    case "blocked":
    case "denied":
    case "error":
      return { ...activity, phase: action + " · " + ev.status, detail, pendingDispatch: "" };
    default:
      return activity;
  }
}

export function endBrainTurn(activity: BrainActivity): BrainActivity {
  return { ...activity, finished: true, phase: activity.failed ? activity.phase : "Turn finished", detail: activity.failed ? activity.detail : "", pendingDispatch: "" };
}
