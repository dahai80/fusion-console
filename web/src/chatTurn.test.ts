import test from "node:test";
import assert from "node:assert/strict";
import { applyAgentEvent, applyChatEvent, emptyTurn, followDelay, FOLLOW_MAX_FAILURES, isNetworkError, newTurnId, replayTurn, type TurnEvent } from "./chatTurn.ts";

const chatEvents: TurnEvent[] = [
  { type: "budget", spent: 10, limit: 100 },
  { type: "delta", text: "Looking at the issue. " },
  { type: "approval", id: "appr-1", tool: "run_agent", detail: "codex-1: fix #179" },
  { type: "tool", tool: "run_agent", status: "running", detail: "codex-1: fix #179" },
  { type: "tool", tool: "run_agent", status: "queued" },
  { type: "notice", text: "worker busy" },
  { type: "usage", tokens_in: 5, tokens_out: 7 },
  { type: "budget", spent: 22, limit: 100 },
  { type: "delta", text: "Dispatched." },
  { type: "done" },
];

test("replaying recorded chat events rebuilds the streamed bubble", () => {
  let live = emptyTurn();
  for (const ev of chatEvents) live = applyChatEvent(live, ev);
  const r = replayTurn("chat", chatEvents);
  assert.deepEqual(r.view, live);
  assert.equal(r.view.content, "Looking at the issue. \n\n> ⏸ awaiting your approval: codex-1: fix #179\n\n\n\n> ⚙️ dispatch `run_agent` — codex-1: fix #179\n\n> ↳ queued\n\n\n\n> ℹ️ worker busy\nDispatched.");
  assert.equal(r.view.meta, JSON.stringify({ type: "usage", tokens_in: 5, tokens_out: 7 }));
  assert.deepEqual(r.budget, { spent: 22, limit: 100 });
  assert.equal(r.approval, null, "a tool event after the approval closes the gate");
});

test("an approval still waiting is restored after a reconnect", () => {
  const r = replayTurn("chat", chatEvents.slice(0, 3));
  assert.deepEqual(r.approval, { id: "appr-1", detail: "codex-1: fix #179" });
});

test("agent replay skips a result that repeats the streamed text", () => {
  const events: TurnEvent[] = [
    { type: "text", text: "Fixed the bug." },
    { type: "tool", tool: "Edit", detail: "main.go" },
    { type: "result", text: " Fixed the bug. " },
    { type: "usage", cost_usd: 0.1 },
  ];
  const r = replayTurn("agent", events);
  assert.equal(r.view.content, "Fixed the bug.\n\n> 🔧 `Edit` main.go\n");
  assert.equal(replayTurn("agent", [...events, { type: "result", text: "Summary" }]).view.content.endsWith("Summary"), true);
  assert.equal(applyAgentEvent(r.view, { type: "blocked", text: "**blocked:** needs repo" }).content, "**blocked:** needs repo");
});

test("only transport failures count as a broken stream", () => {
  assert.equal(isNetworkError(new TypeError("network error")), true);
  assert.equal(isNetworkError(new TypeError("Failed to fetch")), true);
  assert.equal(isNetworkError(new Error("unauthorized")), false);
  assert.equal(isNetworkError(new Error("这个对话上一条消息还在处理中，等它结束再发")), false);
});

test("turn ids are url-safe and distinct; reconnect backs off and gives up", () => {
  const a = newTurnId(), b = newTurnId();
  assert.match(a, /^[a-z0-9-]{6,64}$/);
  assert.notEqual(a, b);
  assert.equal(followDelay(0), 2000);
  assert.equal(followDelay(3), 6000);
  assert.equal(followDelay(30), 10000);
  let total = 0;
  for (let i = 1; i < FOLLOW_MAX_FAILURES; i++) total += followDelay(i);
  assert.ok(total > 5 * 60_000 && total < 8 * 60_000, `gives up after ${total} ms`);
});
