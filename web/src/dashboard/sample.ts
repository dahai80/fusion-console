import type { Run, Snapshot, Worker } from "./overview";

const day = "2026-11-19T";
const at = (hhmm: string) => day + hhmm + ":00Z";

function run(id: number, worker: string, task: string, status: string, start = "", finish = "", tokens = 0): Run {
  return {
    id, worker, task, status, agent: worker.split("-")[0],
    started_at: start && at(start), finished_at: finish && at(finish),
    elapsed_s: start && finish ? (Date.parse(at(finish)) - Date.parse(at(start))) / 1000 : 0,
    tokens_in: Math.round(tokens * 0.8), tokens_out: tokens - Math.round(tokens * 0.8), detail: "",
  };
}

export function sampleSnapshot(advanced: boolean): Snapshot {
  const runs = [
    run(131, "codex-1", "Verify worker alias resolution", "queued"),
    run(130, "claude-1", "Draft the release notes", "queued"),
    run(129, "claude-1", "Review the dashboard changes", advanced ? "running" : "queued", advanced ? "14:41" : ""),
    run(128, "codex-1", "Run gateway integration tests", "running", "14:25"),
    run(127, "claude-1", "Review dispatch handling", advanced ? "done" : "running", "14:18", advanced ? "14:41" : "", advanced ? 17800 : 0),
    { ...run(126, "codex-2", "Validate repository binding", "error", "14:08", "14:13", 900), error: "The configured checkout is unavailable." },
    run(125, "claude-2", "Update worker documentation", "done", "14:02", "14:16", 7200),
    run(124, "claude-1", "Review provider routing", "done", "13:52", "14:14", 12100),
    run(123, "codex-1", "Check model compatibility", "done", "13:38", "13:55", 9300),
    run(122, "claude-1", "Review session recovery", "done", "13:15", "13:40", 10100),
    run(121, "codex-1", "Verify streaming responses", "done", "13:06", "13:26", 6000),
    run(120, "claude-1", "Review the orchestration plan", "done", "12:38", "13:01", 11100),
    run(119, "codex-1", "Test queue persistence", "done", "12:28", "12:46", 7600),
  ];
  const workers: Worker[] = ["codex-1", "claude-1", "claude-2", "codex-2"].map(name => {
    const current = runs.find(r => r.worker === name && r.status === "running");
    return { name, agent: name.split("-")[0], enabled: true, detected: true, busy: !!current, task: current?.task };
  });
  return {
    now: Date.parse(at(advanced ? "14:41" : "14:36")), runs, workers,
    tokens: { brain_tokens: 38600, worker_tokens: advanced ? 134000 : 116200, brain_cost_usd: 0, worker_cost_usd: 0 },
  };
}
