import assert from "node:assert/strict";
import { after, before, test } from "node:test";
import { chromium } from "playwright-core";
import { createServer } from "vite";

const errorText = "Worker exited before returning a result";
const draft = {
  title: "Worker exits during a run",
  steps: "1. Run a worker.",
  expect: "Worker returns a result.",
  actual: errorText,
  extra: "",
  component: "console",
};
const provider = { id: "test", name: "Test", type: "gateway", model: "test-model", enabled: true };

let vite;
let browser;
before(async () => {
  vite = await createServer({ server: { host: "127.0.0.1", port: 0, hmr: false } });
  await vite.listen();
  browser = await chromium.launch();
});
after(async () => {
  await browser?.close();
  await vite?.close();
});

async function openConsole(t, { chatEvent, respondToDraft }) {
  const page = await browser.newPage();
  t.after(() => page.close());
  page.setDefaultTimeout(8000);
  const drafts = [];
  const submissions = [];
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    switch (path) {
      case "/api/me":
        return route.fulfill({ json: { user: "test" } });
      case "/api/settings/providers":
        return route.fulfill({ json: { providers: [provider] } });
      case "/api/conversations":
        return route.fulfill({ json: request.method() === "POST" ? { id: 1 } : { conversations: [] } });
      case "/api/chat":
        return route.fulfill({
          contentType: "text/event-stream",
          body: [chatEvent, { type: "done" }].map((event) => `data: ${JSON.stringify(event)}\n\n`).join(""),
        });
      case "/api/feedback/draft":
        drafts.push(request.postDataJSON());
        return route.fulfill(await respondToDraft(drafts.length));
      case "/api/feedback/draft_latest":
        return route.fulfill({ json: draft });
      case "/api/feedback/submit":
        submissions.push(request.postDataJSON());
        return route.fulfill({ json: { repo: "dahai80/fusion-console", url: "https://example.test/issues/1" } });
      default:
        return route.fulfill({ json: {} });
    }
  });
  await page.goto(vite.resolvedUrls.local[0]);
  await page.getByTitle(/^Chat brain:/).waitFor();
  await page.getByPlaceholder(/^Message…/).fill("Run the task");
  await page.getByTitle("Send as chat").click();
  return { page, drafts, submissions };
}

function issueTitle(page) {
  return page.getByPlaceholder("一句话说清问题或需求");
}

function waitForDraft(page) {
  return issueTitle(page).and(page.locator(`input[value="${draft.title}"]`)).waitFor();
}

test("clicking the error toast starts one editable draft and returning to Feedback does not replay it", async (t) => {
  const allowDraft = Promise.withResolvers();
  const { page, drafts, submissions } = await openConsole(t, {
    chatEvent: { type: "error", text: errorText },
    respondToDraft: async () => {
      await allowDraft.promise;
      return { json: draft };
    },
  });
  const summarize = page.getByRole("button", { name: "总结此问题" });
  await summarize.waitFor();
  assert.deepEqual(drafts, []);

  const title = issueTitle(page);
  const feedback = page.getByRole("button", { name: "Feedback", exact: true });
  try {
    await summarize.evaluate((button) => { button.click(); button.click(); });
    const loading = page.getByRole("button", { name: "大脑正在整理…" });
    await loading.waitFor();
    assert.equal(await loading.isDisabled(), true);
    assert.equal(await page.getByPlaceholder(/^例：派发任务/).inputValue(), errorText);
  } finally {
    allowDraft.resolve();
  }

  await waitForDraft(page);
  await title.fill("Edited worker issue");
  await feedback.click();
  assert.equal(await title.inputValue(), "Edited worker issue");
  await page.getByRole("button", { name: "Chat", exact: true }).click();
  await feedback.click();
  assert.equal(await title.inputValue(), "");
  assert.deepEqual(drafts, [{ rough: errorText, component: "console" }]);
  assert.deepEqual(submissions, []);
});

test("an automatic draft error keeps the context for retry and submitting still requires a click", async (t) => {
  const failure = "Draft service temporarily unavailable";
  const { page, drafts, submissions } = await openConsole(t, {
    chatEvent: { type: "error", text: errorText },
    respondToDraft: (attempt) => attempt === 1 ? { status: 502, json: { error: failure } } : { json: draft },
  });
  await page.getByRole("button", { name: "总结此问题" }).click();
  await page.getByText(failure, { exact: true }).waitFor();
  assert.equal(await page.getByPlaceholder(/^例：派发任务/).inputValue(), errorText);
  assert.equal(drafts.length, 1);

  await page.getByRole("button", { name: "让大脑帮我填写" }).click();
  const submit = page.getByRole("button", { name: "提交到 dahai80/fusion-console" });
  await waitForDraft(page);
  assert.equal(await page.getByText(failure, { exact: true }).count(), 0);
  assert.equal(drafts.length, 2);
  assert.deepEqual(submissions, []);
  await issueTitle(page).fill("Reviewed worker issue");
  await submit.click();
  await page.getByText("已提交：https://example.test/issues/1", { exact: true }).waitFor();
  assert.equal(submissions.length, 1);
  assert.equal(submissions[0].title, "Reviewed worker issue");
  assert.ok(submissions[0].body.includes(errorText));
});

test("opening a brain suggestion shows the draft without generating it again", async (t) => {
  const { page, drafts, submissions } = await openConsole(t, {
    chatEvent: { type: "tool", tool: "suggest_feedback", status: "progress" },
    respondToDraft: () => ({ json: draft }),
  });
  await page.getByRole("button", { name: "查看并提交" }).click();
  assert.equal(await issueTitle(page).inputValue(), draft.title);
  assert.deepEqual(drafts, []);
  assert.deepEqual(submissions, []);
});
