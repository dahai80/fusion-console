import type { DashboardData, DispatchRun, WorkerStatus } from "../api";

export type Run = Pick<DispatchRun, "id" | "worker" | "agent" | "task" | "started_at" | "finished_at" | "elapsed_s" | "detail" | "error" | "tokens_in" | "tokens_out"> & {
  status: string;
};
export type Worker = Pick<WorkerStatus, "name" | "agent" | "enabled" | "detected" | "busy" | "task" | "detail">;
export type Snapshot = {
  runs: Run[];
  workers: Worker[];
  tokens: DashboardData["totals"];
  now: number;
};
export type TaskFilter = "All" | "Running" | "Queued" | "Attention" | "Done";

export function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(n >= 10_000 ? 0 : 1) + "k";
  return String(n);
}

export function timestamp(iso: string) {
  if (!iso || iso.startsWith("0001-")) return undefined;
  const time = Date.parse(iso);
  return Number.isFinite(time) ? time : undefined;
}

export const clock = (time: number) => new Date(time).toISOString().slice(11, 16);

export function duration(seconds: number) {
  if (seconds < 60) return Math.max(0, Math.round(seconds)) + "s";
  if (seconds < 3600) return Math.round(seconds / 60) + "m";
  return (seconds / 3600).toFixed(1).replace(/\.0$/, "") + "h";
}

export function matchesFilter(status: string, filter: TaskFilter) {
  if (filter === "All") return true;
  if (filter === "Attention") return status === "error" || status === "blocked";
  return status === filter.toLowerCase();
}

export function workerState(w: Worker) {
  if (w.busy) return "Busy";
  if (!w.enabled) return "Disabled";
  if (!w.detected) return "CLI offline";
  return "Available";
}

// Estimate from the median of the latest measured completions for this worker.
export function runtimeHistory(runs: Run[]) {
  const samples = new Map<string, number[]>();
  const done = runs.filter(r => r.status === "done" && r.elapsed_s > 0 && timestamp(r.finished_at) !== undefined);
  done.sort((a, b) => Date.parse(b.finished_at) - Date.parse(a.finished_at));
  for (const r of done) {
    const key = r.worker || r.agent;
    const values = samples.get(key) ?? [];
    if (values.length < 10) values.push(r.elapsed_s);
    samples.set(key, values);
  }
  const result = new Map<string, { seconds: number; count: number }>();
  for (const [worker, values] of samples) {
    if (values.length < 3) continue;
    values.sort((a, b) => a - b);
    const mid = Math.floor(values.length / 2);
    const median = values.length % 2 ? values[mid] : (values[mid - 1] + values[mid]) / 2;
    result.set(worker, { seconds: median, count: values.length });
  }
  return result;
}

export function estimateEnd(run: Run, history: ReturnType<typeof runtimeHistory>) {
  const start = timestamp(run.started_at);
  const sample = history.get(run.worker || run.agent);
  if (run.status === "running" && start !== undefined && sample) return start + sample.seconds * 1000;
}
