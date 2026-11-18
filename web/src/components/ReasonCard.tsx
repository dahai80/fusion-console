import { AlertTriangle, ArrowRight } from "lucide-react";
import type { ApiError } from "../api";
import { reasonCopy } from "../reasons";
import { Button } from "./ui";

// ReasonCard renders a classified failure (issue #45 F1): the reason_code
// line, bilingual operator copy, and at most one primary action. Unknown or
// missing codes fall back to the raw message — no crash, no invented copy.
const ACTION_LABELS: Record<string, string> = {
  start_lead: "Start lead loop",
  raise_budget: "Raise budget",
  wait_or_inbox: "Wait / send inbox message",
  rearm_wakeup: "Re-arm wakeup",
};

export default function ReasonCard({
  error,
  onAction,
}: {
  error: ApiError;
  onAction?: (action: string) => void;
}) {
  const copy = error.reason_code ? reasonCopy(error.reason_code) : null;
  const action = copy?.action ? ACTION_LABELS[copy.action] : undefined;
  return (
    <div className="rounded-xl border border-amber-300/60 bg-amber-500/5 p-3 text-sm dark:border-amber-500/30">
      <p className="flex items-center gap-1.5 font-medium text-amber-600 dark:text-amber-400">
        <AlertTriangle size={14} className="shrink-0" />
        {error.reason_code ? <span className="font-mono text-xs">{error.reason_code}</span> : null}
      </p>
      {copy ? (
        <>
          <p className="mt-1 text-zinc-700 dark:text-zinc-300">{copy.en}</p>
          <p className="mt-0.5 text-zinc-500 dark:text-zinc-400">{copy.cn}</p>
          {action && onAction && (
            <Button variant="ghost" className="mt-2" onClick={() => onAction(copy.action!)}>
              {action} <ArrowRight size={12} />
            </Button>
          )}
        </>
      ) : (
        <p className="mt-1 text-zinc-700 dark:text-zinc-300">{error.message}</p>
      )}
    </div>
  );
}
