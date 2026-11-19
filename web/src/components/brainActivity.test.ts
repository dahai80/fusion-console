import test from "node:test";
import assert from "node:assert/strict";
import { beginBrainTurn, brainEvent, endBrainTurn } from "./brainActivity.ts";

test("approval and progress do not claim that a dispatch succeeded", () => {
  let state = beginBrainTurn(7, "Check the workspace");
  state = brainEvent(state, { type: "approval", tool: "run_agent", detail: "codex-1: inspect the checkout" });
  assert.equal(state.phase, "Awaiting approval");
  assert.equal(state.lastHandoff, "");
  state = brainEvent(state, { type: "tool", tool: "run_agent", status: "running", detail: "codex-1: inspect the checkout" });
  state = brainEvent(state, { type: "tool", tool: "run_agent", status: "progress", detail: "Checking workspace" });
  assert.equal(state.lastHandoff, "");
  const denied = brainEvent(state, { type: "tool", tool: "run_agent", status: "denied" });
  assert.equal(endBrainTurn(denied).lastHandoff, "");
  state = brainEvent(state, { type: "tool", tool: "run_agent", status: "queued", detail: "" });
  assert.equal(endBrainTurn(state).lastHandoff, "codex-1: inspect the checkout");
});

test("shows public activity, retains a transport failure, and resets for a new turn", () => {
  const initial = beginBrainTurn(8, "Review tests");
  assert.equal(brainEvent(initial, { type: "reasoning", text: "internal" }), initial);
  const writing = brainEvent(initial, { type: "delta", text: "public reply" });
  assert.equal(writing.phase, "Writing reply");
  assert.equal(writing.detail, "");
  const failed = endBrainTurn(brainEvent(writing, { type: "error", text: "Provider unavailable" }));
  assert.ok(failed.failed && failed.finished);
  assert.equal(failed.detail, "Provider unavailable");
  assert.equal(beginBrainTurn(9, "New task").lastHandoff, "");
});
