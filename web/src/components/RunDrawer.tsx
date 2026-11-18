import { useEffect, useState } from "react";
import { AlertTriangle, Check, Copy, MessageSquare, Play, Square, Wrench, X } from "lucide-react";
import { api, ApiError } from "../api";
import { Button, Card } from "./ui";

// RunDrawer replays one task's runs over the daemon's read-only run RPCs
// (issue #45 F2): attempt list, timestamped event timeline, usage footer,
// copy-jsonl. Degraded path: an RPC that fails with spec_unavailable /
// task_not_found renders an empty state — never a crash, and the entry
// point hides at the call site once the daemon reports the feature absent.
type RunRow = { attempt: number; member?: string; status?: string; events?: number; usage?: Record<string, unknown> };
type RunEvent = { seq: number; ts: number; type: string; payload: Record<string, any> };

const TYPE_ICON: Record<string, typeof Play> = {
  run_started: Play,
  prompt: MessageSquare,
  tool_call: Wrench,
  tool_result: Check,
  tool_error: AlertTriangle,
  final: MessageSquare,
  run_finished: Square,
};

function eventText(ev: RunEvent): string {
  const p = ev.payload || {};
  const t = p.detail || p.content || p.summary || p.text || p.tool || p.status || "";
  if (typeof t === "string" && t) return t;
  return JSON.stringify(p);
}

function fmtTokens(n: unknown): string {
  const v = Number(n);
  if (!Number.isFinite(v)) return "—";
  return v >= 1000 ? `${(v / 1000).toFixed(1)}k` : String(v);
}

function usageLine(u: Record<string, unknown> | null | undefined): string {
  if (!u) return "";
  const parts: string[] = [];
  const keys = Object.keys(u);
  const pick = (...names: string[]): unknown => {
    for (const n of names) {
      if (keys.some((k) => k.toLowerCase() === n)) return u[keys.find((k) => k.toLowerCase() === n)!];
    }
    return undefined;
  };
  const inTok = pick("in", "input", "tokens_in", "input_tokens");
  const outTok = pick("out", "output", "tokens_out", "output_tokens");
  const cache = pick("cache_hit", "cache-hit", "cache_read");
  const cost = pick("cost", "cost_usd");
  if (inTok !== undefined) parts.push(`in ${fmtTokens(inTok)}`);
  if (outTok !== undefined) parts.push(`out ${fmtTokens(outTok)}`);
  if (cache !== undefined && inTok !== undefined) {
    const pct = (Number(cache) / Math.max(Number(inTok), 1)) * 100;
    if (Number.isFinite(pct)) parts.push(`cache-hit ${pct.toFixed(0)}%`);
  }
  if (cost !== undefined && Number(cost) > 0) parts.push(`≈ $${Number(cost).toFixed(2)}`);
  return parts.join(" · ");
}

export default function RunDrawer({
  team,
  taskId,
  onClose,
  onUnavailable,
}: {
  team: string | undefined;
  taskId: string;
  onClose: () => void;
  onUnavailable?: () => void;
}) {
  const [runs, setRuns] = useState<RunRow[] | null>(null);
  const [attempt, setAttempt] = useState<number | null>(null);
  const [events, setEvents] = useState<RunEvent[] | null>(null);
  const [usage, setUsage] = useState<Record<string, unknown> | null>(null);
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    setRuns(null);
    setAttempt(null);
    setEvents(null);
    setUsage(null);
    setErr("");
    api
      .teamRuns(team, taskId)
      .then((r) => {
        const list = (r.runs as RunRow[]) ?? [];
        setRuns(list);
        const last = list.length ? list[list.length - 1].attempt : null;
        if (last != null) setAttempt(last);
        else setErr("no recorded runs for this task yet");
      })
      .catch((e: ApiError) => {
        // Feature not landed / task gone: entry point hides at the call site.
        if (e.reason_code === "spec_unavailable" || e.reason_code === "task_not_found") onUnavailable?.();
        setErr(e.message);
      });
  }, [team, taskId]);

  useEffect(() => {
    if (attempt == null) return;
    api
      .teamRunMessages(team, taskId, attempt)
      .then((r) => setEvents((r.messages as RunEvent[]) ?? []))
      .catch((e: ApiError) => setErr(e.message));
    api
      .teamRunUsage(team, taskId)
      .then((r) => {
        const rows = (r.usage as { attempt: number; usage: Record<string, unknown> }[]) ?? [];
        const row = rows.find((x) => x.attempt === attempt);
        setUsage(row?.usage ?? null);
      })
      .catch(() => setUsage(null));
  }, [team, taskId, attempt]);

  const copyJsonl = () => {
    if (!events) return;
    navigator.clipboard
      .writeText(events.map((e) => JSON.stringify(e)).join("\n"))
      .then(() => setCopied(true))
      .catch(() => setCopied(false));
  };

  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-zinc-950/40" onClick={onClose}>
      <Card
        className="flex h-full w-full max-w-xl flex-col rounded-none border-y-0 border-r-0 p-0"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-zinc-200/60 p-4 dark:border-zinc-800">
          <h3 className="flex-1 truncate text-sm font-medium">Run: {taskId}</h3>
          {runs && runs.length > 1 && (
            <select
              value={attempt ?? ""}
              onChange={(e) => setAttempt(Number(e.target.value))}
              className="rounded-lg border border-zinc-200/80 bg-transparent px-2 py-1 text-xs dark:border-zinc-700"
            >
              {runs.map((r) => (
                <option key={r.attempt} value={r.attempt}>
                  attempt {r.attempt}/{runs.length} {r.status || ""}
                </option>
              ))}
            </select>
          )}
          <Button variant="ghost" onClick={onClose} aria-label="close">
            <X size={14} />
          </Button>
        </div>
        <div className="flex-1 overflow-auto p-4">
          {err && <p className="text-sm text-zinc-500 dark:text-zinc-400">{err}</p>}
          {events && events.length === 0 && <p className="text-sm text-zinc-400">empty run log</p>}
          <div className="space-y-1.5">
            {(events ?? []).map((ev) => {
              const Icon = TYPE_ICON[ev.type] ?? Play;
              const isErr = ev.type === "tool_error" || (ev.type === "run_finished" && ev.payload?.status === "error");
              return (
                <div key={ev.seq} className="flex items-start gap-2 text-sm">
                  <span className="w-16 shrink-0 pt-0.5 font-mono text-[11px] text-zinc-400">
                    {ev.ts ? new Date(ev.ts * 1000).toLocaleTimeString() : ""}
                  </span>
                  <Icon size={14} className={`mt-1 shrink-0 ${isErr ? "text-red-500" : "text-indigo-500"}`} />
                  <span className="min-w-0 flex-1 break-words">
                    <span className="mr-1.5 font-mono text-[11px] text-zinc-400">{ev.type}</span>
                    {eventText(ev)}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
        <div className="flex items-center gap-2 border-t border-zinc-200/60 p-3 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
          <span className="flex-1 truncate tabular-nums">{usageLine(usage) || (events ? "no usage recorded" : "")}</span>
          <Button variant="ghost" onClick={copyJsonl} disabled={!events?.length}>
            <Copy size={12} /> {copied ? "copied" : "copy jsonl"}
          </Button>
        </div>
      </Card>
    </div>
  );
}
