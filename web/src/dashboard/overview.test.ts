import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { Run } from "./overview";
import { estimateEnd, matchesFilter, runtimeHistory, timestamp, workerState } from "./overview.ts";

function done(id: number, seconds: number): Run {
  return {
    id, worker: "codex-1", agent: "codex", status: "done", task: "Verify changes",
    elapsed_s: seconds, started_at: "", finished_at: new Date(id * 86400000).toISOString(),
    tokens_in: 100, tokens_out: 20, detail: "",
  };
}
test("ETAs use up to ten recent measured completions from the same worker", () => {
  const samples = Array.from({ length: 11 }, (_, i) => done(i + 1, i === 0 ? 999 : i * 10));
  samples.reverse();
  const history = runtimeHistory([...samples, { ...done(12, 999), status: "error" }, { ...done(13, 999), worker: "codex-2" }]);
  assert.deepEqual(history.get("codex-1"), { seconds: 55, count: 10 });
  assert.equal(history.has("codex-2"), false);
  const running = { ...samples[0], status: "running", started_at: "2026-11-19T12:00:00Z" };
  assert.equal(estimateEnd(running, history), Date.parse(running.started_at) + 55000);
  assert.equal(estimateEnd({ ...running, status: "queued" }, history), undefined);
  assert.equal(estimateEnd({ ...running, started_at: "0001-01-01T00:00:00Z" }, history), undefined);
});
test("invalid or missing measurements cannot produce an ETA", () => {
  assert.equal(timestamp("unknown"), undefined);
  const history = runtimeHistory([done(1, 20), done(2, 30), done(3, 0), { ...done(4, 45), finished_at: "" }]);
  assert.equal(history.size, 0);
});
test("attention and available worker counts reflect dispatch and CLI state", () => {
  assert.equal(matchesFilter("blocked", "Attention"), true);
  assert.equal(matchesFilter("error", "Attention"), true);
  assert.equal(matchesFilter("done", "Attention"), false);
  const worker = { name: "codex-1", agent: "codex", enabled: true, detected: true };
  assert.equal(workerState(worker), "Available");
  assert.equal(workerState({ ...worker, busy: true }), "Busy");
  assert.equal(workerState({ ...worker, detected: false }), "CLI offline");
  assert.equal(workerState({ ...worker, enabled: false }), "Disabled");
});
