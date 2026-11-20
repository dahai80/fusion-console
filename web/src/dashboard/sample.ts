import type { UsageSeriesData } from "../api";
import type { Run, Snapshot, Worker } from "./overview";
import { addDays, localDay, mondayIndex } from "./usageSeries";

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
    sampleTokens: { brain_tokens: 38600, worker_tokens: runs.reduce((sum,r) => sum+r.tokens_in+r.tokens_out,0) },
  };
}

// Deterministic year of usage for the Sample switch: weekday rhythm, a
// ramp-up since early August, and a daytime hourly curve.
export function sampleSeries(now: number): UsageSeriesData {
  let seed = 7;
  const rand = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const today = localDay(now);
  const names = ["codex-1", "claude-1", "claude-2", "codex-2"];
  const first = addDays(today, -109);
  const days: UsageSeriesData["days"] = [];
  const totals = [0, 0, 0, 0, 0];
  for (let d = first; d <= today; d = addDays(d, 1)) {
    const weekend = mondayIndex(d) >= 5;
    if (rand() < (weekend ? 0.45 : 0.08)) continue;
    const ramp = 0.35 + 0.65 * (Date.parse(d) - Date.parse(first)) / (Date.parse(today) - Date.parse(first) || 1);
    const scale = 1.6e8 * ramp * (weekend ? 0.35 : 1) * (0.4 + rand());
    const shares = [0.32 + rand() * 0.3, rand() * 0.5, rand() * 0.35, rand() * 0.2, rand() < 0.3 ? rand() * 0.15 : 0];
    const sum = shares.reduce((a, b) => a + b, 0);
    const values = shares.map(s => Math.round(scale * s / sum));
    values.forEach((v, i) => (totals[i] += v));
    days.push({ day: d, total: values.reduce((a, b) => a + b, 0), records: 40, measured: 40, values });
  }
  const hourNow = new Date(now).getHours();
  const curve = (h: number) => Math.max(0, Math.sin((h - 7) / 13 * Math.PI)) * (0.6 + rand() * 0.8) * 3.2e7;
  return {
    tz_offset_min: -new Date(now).getTimezoneOffset(), today, start: addDays(today, -370), first_day: first,
    series: [{ name: "Brain", role: "brain", total: totals[0] }, ...names.map((name, i) => ({ name, role: "worker" as const, total: totals[i + 1] }))],
    days,
    hours: { today: Array.from({ length: 24 }, (_, h) => h <= hourNow ? Math.round(curve(h)) : 0), yesterday: Array.from({ length: 24 }, (_, h) => Math.round(curve(h))) },
  };
}
