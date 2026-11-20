import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { AgentInfo, WorkerSettings, WorkerStatus } from "../api";
import {
  agoText, budgetChoices, draftFrom, elapsedText, emptyDraft, mergeRows, modelText, modelValue, nextName, parseModelValue, placeText, repoLabel, summaryText, toSettings, toolChoices, validate,
} from "./workerModel.ts";

const tool = (name: string, detected: boolean, extra: Partial<AgentInfo> = {}): AgentInfo =>
  ({ name, command: name, detected, permission_mode: "acceptEdits", max_budget_usd: 5, ...extra });

const worker = (name: string, extra: Partial<WorkerSettings> = {}): WorkerSettings =>
  ({ name, agent: "claude", enabled: true, isolation: "standalone", permission_mode: "acceptEdits", max_budget_usd: 5, provider_id: "", work_dir: "", ...extra });

test("tool choices drop worker entries and put detected tools first", () => {
  const list = toolChoices([tool("codex", false), tool("claude", true), tool("claude-1", true, { is_worker: true })]);
  assert.deepEqual(list.map((a) => a.name), ["claude", "codex"]);
});

test("empty draft preselects the first detected tool only", () => {
  assert.equal(emptyDraft([tool("codex", false), tool("claude", true)]).agent, "claude");
  assert.equal(emptyDraft([tool("codex", false)]).agent, "");
});

test("next name skips names already taken", () => {
  assert.equal(nextName("claude", []), "claude-1");
  assert.equal(nextName("claude", [worker("claude-2")]), "claude-3");
  assert.equal(nextName("codex", [worker("claude-1"), { name: "", agent: "codex" }, worker("codex-2", { agent: "codex" })]), "codex-3");
});

test("validate explains what is missing", () => {
  const d = { ...emptyDraft([tool("claude", true)]) };
  assert.equal(validate(d, []), "");
  assert.match(validate({ ...d, agent: "" }, []), /工具/);
  assert.match(validate({ ...d, place: "github" }, []), /仓库地址/);
  assert.match(validate({ ...d, place: "github", repoUrl: "github.com/a/b" }, []), /https/);
  assert.equal(validate({ ...d, place: "github", repoUrl: "https://github.com/a/b.git" }, []), "");
  assert.match(validate({ ...d, place: "worktree", repoPath: "code/app" }, []), /完整路径/);
  assert.match(validate({ ...d, name: "my worker" }, []), /字母/);
  assert.match(validate({ ...d, name: "claude-1" }, [worker("claude-1")]), /已经有/);
  assert.equal(validate({ ...d, name: "claude-1" }, [worker("claude-1")], "claude-1"), "");
  assert.match(validate({ ...d, budget: 0 }, []), /大于 0/);
});

test("drafts round-trip through settings and keep fields the form does not edit", () => {
  const base = worker("rev", { isolation: "github", repo: "https://github.com/o/r.git", repo_branch: "dev", permission_mode: "plan", enabled: false, model: "m1", memory: "off" });
  const d = draftFrom(base);
  assert.equal(d.place, "github");
  assert.equal(d.repoUrl, "https://github.com/o/r.git");
  const back = toSettings(d, [base], base);
  assert.equal(back.permission_mode, "plan");
  assert.equal(back.enabled, false);
  assert.equal(back.repo_branch, "dev");
  assert.equal(back.memory, "off");
  const local = toSettings({ ...d, place: "worktree", repoPath: "/src/app" }, [base], base);
  assert.equal(local.repo, "/src/app");
  assert.equal(local.repo_branch, "");
  const fresh = toSettings({ ...emptyDraft([tool("codex", true)]) }, [base]);
  assert.equal(fresh.name, "codex-1");
  assert.equal(fresh.enabled, true);
  assert.equal(fresh.permission_mode, "acceptEdits");
  assert.equal(fresh.repo, "");
});

test("legacy empty isolation is treated as a local worktree", () => {
  assert.equal(draftFrom(worker("x", { isolation: "" as WorkerSettings["isolation"], repo: "/a/b" })).place, "worktree");
});

test("repo labels are short", () => {
  assert.equal(repoLabel("https://github.com/dahai80/fusion-console.git"), "dahai80/fusion-console");
  assert.equal(repoLabel("git@github.com:dahai80/fusion-console.git"), "dahai80/fusion-console");
  assert.equal(repoLabel("/Users/me/code/app/"), "app");
  assert.equal(placeText({ isolation: "standalone", repo: "" }), "独立文件夹");
  assert.equal(placeText({ isolation: "worktree", repo: "/Users/me/app" }), "本机仓库 · app");
});

test("rows put busy workers first, then enabled, then disabled", () => {
  const st = (name: string, busy: boolean, elapsed = 0): WorkerStatus =>
    ({ name, agent: "claude", enabled: true, detected: true, isolation: "", work_dir: "", repo: "", provider_id: "", max_budget_usd: 5, calls: 0, cost_usd: 0, tokens_in: 0, tokens_out: 0, last_run: "", busy, elapsed_s: elapsed });
  const rows = mergeRows(
    [worker("off", { enabled: false }), worker("idle"), worker("short"), worker("long")],
    [st("short", true, 10), st("long", true, 300), st("idle", false)],
  );
  assert.deepEqual(rows.map((r) => r.key), ["long", "short", "idle", "off"]);
  assert.equal(rows[3].st, undefined);
});

test("time and summary strings read naturally", () => {
  assert.equal(elapsedText(42), "42 秒");
  assert.equal(elapsedText(192), "3 分 12 秒");
  assert.equal(elapsedText(3720), "1 小时 2 分");
  const now = Date.parse("2026-11-20T08:00:00Z");
  assert.equal(agoText("2026-11-20 07:55:00", now), "5 分钟前");
  assert.equal(agoText("2026-11-20 05:00:00", now), "3 小时前");
  assert.equal(agoText("", now), "");
  const d = { ...emptyDraft([tool("claude", true)]), place: "github" as const, repoUrl: "https://github.com/o/r" };
  assert.equal(summaryText(d, []), "claude-1 用 Claude Code，在 GitHub 仓库 o/r 里干活，每次最多花 $5。");
});

test("budget choices keep the steps and slot in a custom current cap", () => {
  assert.deepEqual(budgetChoices(5), [1, 2, 5, 10, 20]);
  assert.deepEqual(budgetChoices(7.5), [1, 2, 5, 7.5, 10, 20]);
  assert.deepEqual(budgetChoices(Number.NaN), [1, 2, 5, 10, 20]);
});

test("model values round-trip provider and model, including empty parts", () => {
  for (const [provider, model] of [["", ""], ["gw-glm", ""], ["gw-gpt", "gpt-6.2-astra"], ["gw", "org/model|v2"]]) {
    assert.deepEqual(parseModelValue(modelValue(provider, model)), { provider, model });
  }
});

test("model text names the provider and model in plain words", () => {
  const providers = [{ id: "gw-glm", name: "GLM 网关" }];
  assert.equal(modelText({ provider_id: "", model: "" }, providers), "工具自己的设置");
  assert.equal(modelText({ provider_id: "", model: "opus" }, providers), "opus（工具自己的设置）");
  assert.equal(modelText({ provider_id: "gw-glm", model: "" }, providers), "GLM 网关 · 默认模型");
  assert.equal(modelText({ provider_id: "gw-glm", model: "glm-5" }, providers), "GLM 网关 · glm-5");
  assert.equal(modelText({ provider_id: "gone", model: "x" }, providers), "gone · x");
});
