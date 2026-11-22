import test from "node:test";
import assert from "node:assert/strict";
import { entryKey, mergeEntries, supersedeWith, type TimelineEntry } from "./chatTimeline.ts";

const row = (role: string, content: string, meta?: string): TimelineEntry => ({ role, content, meta });

test("entryKey prefers stable meta ids over content", () => {
  assert.equal(entryKey(row("assistant", "a", '{"team_msg_id":"m1"}')), "assistant|team_msg_id:m1");
  assert.equal(entryKey(row("assistant", "same text", '{"background_run":7}')), "assistant|background_run:7");
  assert.equal(entryKey(row("user", "hello")), "user|c:hello");
  assert.equal(entryKey(row("user", "x", "not json")), "user|meta:not json");
});

test("mergeEntries dedupes live appends against the view", () => {
  const cur = [row("user", "hi"), row("assistant", "team a: working", '{"team_msg_id":"m1"}')];
  const inc = [
    row("assistant", "team a: working", '{"team_msg_id":"m1"}'), // already shown
    row("assistant", "team a: done", '{"team_msg_id":"m2"}'),    // new
  ];
  const merged = mergeEntries(cur, inc);
  assert.equal(merged.length, 3);
  assert.equal(merged.at(-1)?.meta, '{"team_msg_id":"m2"}');
  assert.equal(mergeEntries(cur, []).length, 2, "empty incoming is a no-op");
  assert.equal(mergeEntries(cur, inc).length, mergeEntries(cur, inc).length, "merge is stable");
});

test("supersedeWith keeps the server list but preserves a live turn bubble", () => {
  const server = [row("user", "hi"), row("assistant", "done")];
  assert.deepEqual(supersedeWith([row("user", "hi")], server), server);
  const live = [...server, row("assistant", "")];
  const kept = supersedeWith(live, server);
  assert.equal(kept.length, 3);
  assert.equal(kept.at(-1)?.content, "", "empty assistant bubble survives the refresh");
  assert.deepEqual(supersedeWith([row("user", "hi"), row("assistant", "partial draft")], server), server, "no live bubble -> plain replace");
});
