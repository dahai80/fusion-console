import { describe, expect, test } from "bun:test";
import { REASON_CODES, reasonCopy } from "./reasons";

// Parity contract (issue #45 F1 acceptance): every enum value has EN + CN
// copy; unknown codes have none (callers fall back to the raw message).
describe("reasonCopy parity", () => {
  test("every enum value has non-empty EN and CN copy", () => {
    for (const code of REASON_CODES) {
      const c = reasonCopy(code);
      expect(c).not.toBeNull();
      expect(c!.en.length).toBeGreaterThan(0);
      expect(c!.cn.length).toBeGreaterThan(0);
    }
  });

  test("unknown code returns null", () => {
    expect(reasonCopy("something_new_from_a_newer_daemon")).toBeNull();
    expect(reasonCopy("")).toBeNull();
  });

  test("at most one primary action per code", () => {
    for (const code of REASON_CODES) {
      const c = reasonCopy(code)!;
      if (c.action) {
        expect(["start_lead", "raise_budget", "wait_or_inbox", "rearm_wakeup"]).toContain(c.action);
      }
    }
  });
});
