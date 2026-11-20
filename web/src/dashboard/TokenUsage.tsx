import { useEffect, useState } from "react";
import { api, type DashboardData, type UsageRange } from "../api";
import "./token-usage.css";

export const usageRanges = [["today", "今日"], ["week", "近 7 天"], ["month", "近 30 天"], ["all", "全部历史"]] as const;
const integer = new Intl.NumberFormat("zh-CN");
export const tokenNumber = (value: number) => integer.format(value);
export type PeriodProps = { range: UsageRange; onRange: (r: UsageRange) => void };

export function PeriodPicker({ range, onRange }: PeriodProps) {
  return <div className="token-period" role="group" aria-label="Token 统计周期">{usageRanges.map(([id,label]) =>
    <button type="button" key={id} aria-pressed={id === range} onClick={() => onRange(id)}>{label}</button>)}</div>;
}

export function useTokenUsage(range: UsageRange, enabled = true) {
  const [reply, setReply] = useState<DashboardData>();
  const [failure, setFailure] = useState<{range: UsageRange; message: string}>();
  const [version, setVersion] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    let active = true, loading = false;
    const read = async () => {
      if (loading || document.hidden) return;
      loading = true;
      try {
        const result = await api.dashboard(range);
        if (active) { setReply(result); setFailure(undefined); }
      } catch (e) {
        if (active) setFailure({ range, message: (e as Error).message });
      } finally { loading = false; }
    };
    void read();
    const timer = window.setInterval(read, 10000);
    document.addEventListener("visibilitychange", read);
    return () => { active = false; clearInterval(timer); document.removeEventListener("visibilitychange", read); };
  }, [range, enabled, version]);
  return { data: enabled && reply?.range === range ? reply : undefined, error: enabled && failure?.range === range ? failure.message : "", refresh: () => setVersion(v => v + 1) };
}
