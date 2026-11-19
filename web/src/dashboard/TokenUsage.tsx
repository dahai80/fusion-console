import { useEffect, useState } from "react";
import { api, type DashboardData, type UsageParticipant, type UsageRange } from "../api";
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

export function UsageLedger({ rows, total }: { rows: UsageParticipant[]; total: number }) {
  return <div className="token-ledger-wrap"><table className="token-ledger">
    <caption className="sr-only">Brain 与各 Worker 的 Token 用量</caption>
    <thead><tr><th>执行者</th><th>输入</th><th>输出</th><th>Token 合计</th><th>占比</th></tr></thead>
    <tbody>{rows.map(r => {
      const value = r.tokens_in + r.tokens_out;
      const pct = total && r.measured ? 100 * value / total : null;
      return <tr key={r.role + ":" + r.name}>
        <th scope="row"><span className={'token-dot ' + r.role} />{r.role === "brain" ? "Brain · 大脑" : r.name}
          <small>{!r.records ? "无记录" : !r.measured ? "无已报告用量" : r.measured !== r.records ? `已计量 ${r.measured} / ${r.records} 条` : (r.role === "worker" ? "Worker" : "对话与规划")}</small></th>
        <td>{r.measured ? tokenNumber(r.tokens_in) : "—"}</td><td>{r.measured ? tokenNumber(r.tokens_out) : "—"}</td>
        <td><b>{r.measured ? tokenNumber(value) : "—"}</b></td><td>{pct === null ? "—" : (pct < 0.1 && pct > 0 ? "<0.1" : pct.toFixed(1)) + "%"}</td>
      </tr>;
    })}</tbody></table></div>;
}

export function ActivityCalendar({ data }: { data: DashboardData }) {
  const [pickedYear, setPickedYear] = useState("");
  if (!data.start) return <p className="token-muted">此范围没有用量记录。</p>;
  const years = [];
  for (let n = Number(data.end.slice(0,4)); n >= Number(data.start.slice(0,4)); n--) years.push(String(n));
  const year = years.includes(pickedYear) ? pickedYear : years[0];
  const splitYears = data.range === "all" && years.length > 1;
  const start = splitYears && data.start < year + "-01-01" ? year + "-01-01" : data.start;
  const end = splitYears && data.end > year + "-12-31" ? year + "-12-31" : data.end;
  const lookup = new Map(data.daily.map(d => [d.day, d]));
  const max = Math.max(1, ...data.daily.filter(d => d.day >= start && d.day <= end).map(d => d.tokens));
  const cells = [];
  const first = Date.parse(start + "T00:00:00Z"), last = Date.parse(end + "T00:00:00Z");
  const compact = (last - first) / 86400000 < 35;
  for (let t = first; t <= last; t += 86400000) {
    const date = new Date(t).toISOString().slice(0,10);
    const day = lookup.get(date);
    const level = day?.measured ? Math.max(1, Math.ceil(4 * Math.sqrt(day.tokens / max))) : 0;
    const label = date + " · " + (day?.measured ? tokenNumber(day.tokens) + " Tokens" : day ? "无已报告用量" : "无记录");
    cells.push(<div role="img" key={date} aria-label={label} title={label} data-level={level} className="token-day">{compact && <span>{date.slice(5)}</span>}</div>);
  }
  const offset = (new Date(first).getUTCDay() + 6) % 7;
  return <div>
    <div className="token-calendar-meta"><span>{start} — {end} · UTC</span>
      {splitYears && <select aria-label="活动年份" value={year} onChange={e => setPickedYear(e.target.value)}>{years.map(y => <option key={y}>{y}</option>)}</select>}
    </div>
    <div className="token-calendar-scroll"><div className={compact ? "token-dates" : "token-heatmap"}>
      {!compact && Array.from({length:offset},(_,i)=><i aria-hidden key={i} />)}{cells}
    </div></div>
    <div className="token-key"><span>无记录 / 未报告</span>{[0,1,2,3,4].map(i => <i key={i} className="token-day" data-level={i} />)}<span>高用量</span></div>
  </div>;
}
