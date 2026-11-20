// One streamed chat / agent turn rendered into its assistant bubble. The live
// stream and a reconnect (replaying the events the server recorded) go
// through the same reducers, so a reply picked up after a dropped connection
// looks exactly like one that streamed without a break.

export type TurnEvent = Record<string, unknown>;
export type TurnKind = "chat" | "agent";

// streamed tracks agent text so a final "result" that repeats it is skipped.
export type TurnView = { content: string; meta?: string; streamed: string };

export const emptyTurn = (): TurnView => ({ content: "", streamed: "" });

const str = (v: unknown) => (typeof v === "string" ? v : "");

export function applyChatEvent(v: TurnView, ev: TurnEvent): TurnView {
  const text = str(ev.text);
  switch (ev.type) {
    case "delta":
      return { ...v, content: v.content + text };
    case "notice":
      return { ...v, content: v.content + `\n\n> ℹ️ ${ev.text}\n` };
    case "approval":
      return { ...v, content: v.content + `\n\n> ⏸ awaiting your approval: ${ev.detail ?? ""}\n\n` };
    case "tool": {
      const detail = str(ev.detail);
      const line = {
        running: `\n\n> ⚙️ dispatch \`${ev.tool}\`${detail ? ` — ${detail}` : ""}\n\n`,
        progress: `> ⏳ ${detail}\n`,
        queued: "> ↳ queued\n\n",
        done: "> ↳ done\n\n",
        blocked: `> ↳ blocked: ${detail}\n\n`,
        denied: `> ↳ denied: ${detail}\n\n`,
        error: "> ↳ error\n\n",
      }[str(ev.status)];
      return line ? { ...v, content: v.content + line } : v;
    }
    case "error":
      return { ...v, content: v.content + `\n\n**error:** ${ev.text}` };
    case "usage":
      return { ...v, meta: JSON.stringify(ev) };
    default:
      return v;
  }
}

export function applyAgentEvent(v: TurnView, ev: TurnEvent): TurnView {
  const text = str(ev.text);
  switch (ev.type) {
    case "text":
      return { ...v, content: v.content + text, streamed: v.streamed + text };
    case "result":
      return text.trim() === v.streamed.trim() ? v : { ...v, content: v.content + text };
    case "tool":
      return { ...v, content: v.content + `\n\n> 🔧 \`${ev.tool}\` ${ev.detail ?? ""}\n` };
    case "blocked":
      return { ...v, content: String(ev.text) };
    case "usage":
      return { ...v, meta: JSON.stringify(ev) };
    case "notice":
      return { ...v, content: v.content + `\n\n> ℹ️ ${ev.text}\n` };
    case "error":
      return { ...v, content: v.content + `\n\n**error:** ${ev.text}` };
    default:
      return v;
  }
}

export const applyTurnEvent = (kind: TurnKind, v: TurnView, ev: TurnEvent) =>
  kind === "chat" ? applyChatEvent(v, ev) : applyAgentEvent(v, ev);

export type Approval = { id: string; detail: string };

// approvalAfter tracks the confirm-mode gate: an approval event opens it,
// the next tool event (approved, denied or timed out) closes it.
export function approvalAfter(cur: Approval | null, ev: TurnEvent): Approval | null {
  if (ev.type === "approval") return { id: str(ev.id), detail: str(ev.detail) };
  if (ev.type === "tool") return null;
  return cur;
}

export type ReplayedTurn = { view: TurnView; approval: Approval | null; budget: { spent: number; limit: number } | null };

export function replayTurn(kind: TurnKind, events: TurnEvent[]): ReplayedTurn {
  let view = emptyTurn();
  let approval: Approval | null = null;
  let budget: ReplayedTurn["budget"] = null;
  for (const ev of events) {
    view = applyTurnEvent(kind, view, ev);
    if (kind === "chat") approval = approvalAfter(approval, ev);
    if (ev.type === "budget") budget = { spent: Number(ev.spent) || 0, limit: Number(ev.limit) || 0 };
  }
  return { view, approval, budget };
}

// fetch() reports a broken link as a TypeError ("network error" when the
// stream dies mid-reply, "Failed to fetch" / "Load failed" when the request
// never got through); server answers become ApiError or plain Error.
export const isNetworkError = (ex: unknown) => ex instanceof TypeError;

// newTurnId names a turn before it is sent, so after a dropped stream the
// browser can tell its own turn from an older one. crypto.randomUUID needs a
// secure context and the console is often opened over plain http on a
// tailnet address.
export const newTurnId = () => `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;

// Reconnect pacing: 2 s while the server answers, backing off to 10 s while
// it does not; give up after this many failures in a row (about 6 minutes).
export const followDelay = (failures: number) => Math.min(2000 * Math.max(1, failures), 10000);
export const FOLLOW_MAX_FAILURES = 40;
