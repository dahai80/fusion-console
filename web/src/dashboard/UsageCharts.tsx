import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { CalendarDays, ChartColumn, Clock3, UsersRound } from "lucide-react";
import { api, type UsageParticipant, type UsageRange, type UsageSeriesData } from "../api";
import { PeriodPicker, tokenNumber, useTokenUsage, type PeriodProps } from "./TokenUsage";
import type { Worker } from "./overview";
import {
  addDays, axisTokens, calendarStart, cnTokens, dayLabel, heatLevel, lanes as laneList, monthColumns, niceScale,
  participantDays, recentTotal, shortDay, slotFor, windowDays, type Lane,
} from "./usageSeries";
import "./usage-charts.css";

const color = (slot: number) => `var(--s${slot})`;
const GAP = 2; // surface gap between stacked segments
const PAD = { t: 10, r: 6, b: 24, l: 46 };

export function useUsageSeries(enabled: boolean) {
  const [data, setData] = useState<UsageSeriesData>();
  const [error, setError] = useState("");
  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    let loading = false;
    const read = async () => {
      if (loading || document.hidden) return;
      loading = true;
      try {
        const reply = await api.dashboardSeries();
        if (alive) { setData(reply); setError(""); }
      } catch (e) {
        if (alive) setError((e as Error).message);
      } finally { loading = false; }
    };
    void read();
    const timer = window.setInterval(read, 60000);
    document.addEventListener("visibilitychange", read);
    return () => { alive = false; clearInterval(timer); document.removeEventListener("visibilitychange", read); };
  }, [enabled]);
  return { data: enabled ? data : undefined, error: enabled ? error : "" };
}

// Charts lay out in real pixels so labels keep their size on a phone.
function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(Math.floor(el.getBoundingClientRect().width));
    const observer = new ResizeObserver(([entry]) => setWidth(Math.floor(entry.contentRect.width)));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [ref, width] as const;
}

function roundTop(x: number, y: number, w: number, h: number, radius: number) {
  const r = Math.max(0, Math.min(radius, h, w / 2));
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}

function Tip({ x, width, top = 4, children }: { x: number; width: number; top?: number; children: ReactNode }) {
  const left = Math.min(Math.max(x, 100), Math.max(100, width - 100));
  return <div className="uc-tip" style={{ left, top }} role="status">{children}</div>;
}

function TipLine({ slot, label, value, dashed }: { slot?: number; label: string; value: string; dashed?: boolean }) {
  return <p><i className={dashed ? "dashed" : undefined} style={slot === undefined ? undefined : { background: color(slot) }} />{label}<b>{value}</b></p>;
}

function Legend({ ls }: { ls: Lane[] }) {
  return <div className="uc-legend">{ls.map(l => <span key={l.key}><i style={{ background: color(l.slot) }} />{l.label}</span>)}</div>;
}

function laneValues(ls: Lane[], values: number[] | undefined) {
  return ls.map(l => ({ lane: l, value: l.indexes.reduce((sum, k) => sum + (values?.[k] ?? 0), 0) }));
}

function YGrid({ ticks, y, width }: { ticks: number[]; y: (v: number) => number; width: number }) {
  return <>{ticks.map(t => <g key={t}>
    <line className="uc-grid" x1={PAD.l} x2={width - PAD.r} y1={y(t)} y2={y(t)} />
    <text className="uc-axis" x={PAD.l - 8} y={y(t) + 4} textAnchor="end">{axisTokens(t)}</text>
  </g>)}</>;
}

/** Usage charts at the top of the overview: 30 days by participant, today's hours, and the year. */
export function UsageCharts({ data, error }: { data?: UsageSeriesData; error: string }) {
  const ls = useMemo(() => data ? laneList(data) : [], [data]);
  if (!data) {
    return <div className="uc-row">
      <section className="ops-panel uc-panel"><p className="ops-empty">{error ? `用量读取失败 · ${error}` : "读取用量…"}</p></section>
    </div>;
  }
  return <>
    {error && <p className="ops-notice" role="status">用量刷新失败 · {error} · 显示上一次结果</p>}
    <div className="uc-row">
      <ThirtyDays data={data} ls={ls} />
      <TodayHourly data={data} />
    </div>
    <DailyActivity data={data} ls={ls} />
  </>;
}

function ThirtyDays({ data, ls }: { data: UsageSeriesData; ls: Lane[] }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const rows = useMemo(() => windowDays(data, ls, 30), [data, ls]);
  const H = 240;
  const plotW = Math.max(0, width - PAD.l - PAD.r);
  const plotH = H - PAD.t - PAD.b;
  const { top, ticks } = niceScale(Math.max(...rows.map(r => r.total)));
  const band = plotW / rows.length;
  const barW = Math.max(3, Math.min(24, band * 0.64));
  const y = (v: number) => PAD.t + plotH - (v / top) * plotH;
  const cx = (i: number) => PAD.l + band * (i + 0.5);
  const sum = rows.reduce((a, r) => a + r.total, 0);
  const hovered = hover === null ? undefined : rows[hover];
  return <section className="ops-panel uc-panel">
    <div className="ops-panel-head"><h2><ChartColumn />近 30 天</h2><Legend ls={ls} /></div>
    <div className="uc-plot" ref={ref} onPointerLeave={() => setHover(null)}>
      {width > 0 && <svg width={width} height={H} role="img" aria-label={`近 30 天 Token 用量，合计 ${cnTokens(sum)}`}>
        <YGrid ticks={ticks} y={y} width={width} />
        {hover !== null && <rect className="uc-band" x={PAD.l + band * hover} y={PAD.t} width={band} height={plotH} rx={6} />}
        {rows.map((r, i) => {
          const segs = r.values.map((v, k) => ({ v, k })).filter(s => s.v > 0);
          let acc = 0;
          const x = cx(i) - barW / 2;
          return <g key={r.day}>{segs.map((s, j) => {
            const y0 = y(acc + s.v);
            const bottom = y(acc) - (j === 0 ? 0 : GAP);
            acc += s.v;
            const h = bottom - y0;
            if (h <= 0.5) return null;
            const fill = color(ls[s.k].slot);
            return j === segs.length - 1
              ? <path key={s.k} d={roundTop(x, y0, barW, h, 4)} fill={fill} />
              : <rect key={s.k} x={x} y={y0} width={barW} height={h} fill={fill} />;
          })}</g>;
        })}
        {rows.map((r, i) => (rows.length - 1 - i) % 7 === 0 &&
          <text key={"x" + r.day} className="uc-axis" x={Math.min(cx(i), width - PAD.r - 13)} y={H - 6} textAnchor="middle">
            {i === rows.length - 1 ? "今天" : shortDay(r.day)}
          </text>)}
        {rows.map((r, i) => <rect key={"hit" + r.day} x={PAD.l + band * i} y={PAD.t} width={band} height={plotH} fill="transparent"
          onPointerEnter={() => setHover(i)} onPointerDown={() => setHover(i)} />)}
      </svg>}
      {hovered && hover !== null && <Tip x={cx(hover)} width={width}>
        <p className="uc-tip-head">{dayLabel(hovered.day)} · 合计 {cnTokens(hovered.total)}</p>
        {ls.map((l, k) => hovered.values[k] > 0 &&
          <TipLine key={l.key} slot={l.slot} label={l.label} value={cnTokens(hovered.values[k])} />)}
      </Tip>}
    </div>
  </section>;
}

function TodayHourly({ data }: { data: UsageSeriesData }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const { today, yesterday } = data.hours;
  const nowHour = new Date().getHours();
  const H = 240;
  const plotW = Math.max(0, width - PAD.l - PAD.r);
  const plotH = H - PAD.t - PAD.b;
  const { top, ticks } = niceScale(Math.max(...today, ...yesterday));
  const band = plotW / 24;
  const barW = Math.max(3, Math.min(16, band * 0.72));
  const y = (v: number) => PAD.t + plotH - (v / top) * plotH;
  const x0 = (h: number) => PAD.l + band * h;
  const step = yesterday.map((v, h) => (h ? `H${x0(h).toFixed(1)}V` : `M${x0(0).toFixed(1)},`) + y(v).toFixed(1)).join("") + `H${x0(24).toFixed(1)}`;
  const sum = today.reduce((a, b) => a + b, 0);
  return <section className="ops-panel uc-panel">
    <div className="ops-panel-head"><h2><Clock3 />今天每小时</h2>
      <div className="uc-legend"><span><i style={{ background: color(0) }} />今天</span><span><i className="dashed" />昨天</span></div></div>
    <div className="uc-plot" ref={ref} onPointerLeave={() => setHover(null)}>
      {width > 0 && <svg width={width} height={H} role="img" aria-label={`今天每小时 Token 用量，合计 ${cnTokens(sum)}，虚线为昨天`}>
        <YGrid ticks={ticks} y={y} width={width} />
        {hover !== null && <rect className="uc-band" x={x0(hover)} y={PAD.t} width={band} height={plotH} rx={5} />}
        {today.map((v, h) => v > 0 && <path key={h} d={roundTop(x0(h) + (band - barW) / 2, y(v), barW, PAD.t + plotH - y(v), 3)} fill={color(0)} />)}
        <path className="uc-yesterday" d={step} />
        {[0, 6, 12, 18].map(h => <text key={h} className="uc-axis" x={x0(h) + band / 2} y={H - 6} textAnchor="middle">{String(h).padStart(2, "0")}</text>)}
        {today.map((_, h) => <rect key={"hit" + h} x={x0(h)} y={PAD.t} width={band} height={plotH} fill="transparent"
          onPointerEnter={() => setHover(h)} onPointerDown={() => setHover(h)} />)}
      </svg>}
      {hover !== null && <Tip x={x0(hover) + band / 2} width={width}>
        <p className="uc-tip-head">{String(hover).padStart(2, "0")}:00 – {String(hover + 1).padStart(2, "0")}:00</p>
        <TipLine slot={0} label="今天" value={hover > nowHour ? "—" : cnTokens(today[hover])} />
        <TipLine dashed label="昨天" value={cnTokens(yesterday[hover])} />
      </Tip>}
    </div>
  </section>;
}

function DailyActivity({ data, ls }: { data: UsageSeriesData; ls: Lane[] }) {
  const [mode, setMode] = useState<"bars" | "calendar">("calendar");
  const first = data.first_day;
  const since = !first ? "" : first.slice(0, 4) === data.today.slice(0, 4) ? shortDay(first) : first.replaceAll("-", "/");
  return <section className="ops-panel uc-panel uc-activity">
    <div className="ops-panel-head">
      <h2><CalendarDays />每日活动<span className="uc-sub">近一年 {cnTokens(recentTotal(data, 365))}{since && ` · ${since} 起有记录`}</span></h2>
      <div className="ops-switch" role="group" aria-label="每日活动视图">
        {([["bars", "柱状"], ["calendar", "日历"]] as const).map(([id, label]) =>
          <button key={id} aria-pressed={mode === id} onClick={() => setMode(id)}>{label}</button>)}
      </div>
    </div>
    {mode === "calendar" ? <Calendar data={data} ls={ls} /> : <YearBars data={data} ls={ls} />}
  </section>;
}

function DayTip({ day, ls, values, total }: { day: string; ls: Lane[]; values?: number[]; total: number }) {
  const parts = laneValues(ls, values).filter(p => p.value > 0).sort((a, b) => b.value - a.value).slice(0, 4);
  return <>
    <p className="uc-tip-head">{dayLabel(day)} · {total > 0 ? cnTokens(total) : "无记录"}</p>
    {parts.map(p => <TipLine key={p.lane.key} slot={p.lane.slot} label={p.lane.label} value={cnTokens(p.value)} />)}
  </>;
}

function Calendar({ data, ls }: { data: UsageSeriesData; ls: Lane[] }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const scroller = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<{ day: string; c: number; r: number } | null>(null);
  const [scroll, setScroll] = useState(0);
  const start = calendarStart(data.today);
  const lookup = useMemo(() => new Map(data.days.map(d => [d.day, d])), [data]);
  const max = useMemo(() => Math.max(1, ...data.days.filter(d => d.day >= start).map(d => d.total)), [data, start]);
  const LW = 24, TOP = 20;
  const col = Math.max(13, Math.min(26, Math.floor((width - LW) / 53)));
  const cell = col - 3;
  // Newest weeks first in view when the year does not fit (phones).
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el) { el.scrollLeft = el.scrollWidth; setScroll(el.scrollLeft); }
  }, [width]);
  const cells: ReactNode[] = [];
  for (let c = 0; c < 53; c++) {
    for (let r = 0; r < 7; r++) {
      const day = addDays(start, c * 7 + r);
      if (day > data.today) break;
      const level = heatLevel(lookup.get(day)?.total ?? 0, max);
      cells.push(<rect key={day} x={LW + c * col} y={TOP + r * col} width={cell} height={cell} rx={Math.min(4, cell / 4)}
        className={`uc-heat l${level}${day === data.today ? " today" : ""}`}
        onPointerEnter={() => setHover({ day, c, r })} onPointerDown={() => setHover({ day, c, r })} />);
    }
  }
  const picked = hover ? lookup.get(hover.day) : undefined;
  return <div className="uc-cal" ref={ref} onPointerLeave={() => setHover(null)}>
    {width > 0 && <div className="uc-cal-scroll" ref={scroller} onScroll={e => setScroll(e.currentTarget.scrollLeft)}>
      <svg width={LW + 53 * col} height={TOP + 7 * col} role="img" aria-label={`近一年每日 Token 用量日历，截至 ${data.today}`}>
        {monthColumns(start).map(m => <text key={m.col} className="uc-axis" x={LW + m.col * col} y={12}>{m.label}</text>)}
        {([[0, "一"], [2, "三"], [4, "五"]] as const).map(([r, label]) =>
          <text key={r} className="uc-axis" x={2} y={TOP + r * col + cell / 2 + 4}>{label}</text>)}
        {cells}
      </svg>
    </div>}
    {hover && <Tip x={LW + hover.c * col + cell / 2 - scroll} width={width} top={TOP + (hover.r + 1) * col + 4}>
      <DayTip day={hover.day} ls={ls} values={picked?.values} total={picked?.total ?? 0} />
    </Tip>}
    <div className="uc-key" aria-hidden="true"><span>少</span>{[0, 1, 2, 3, 4].map(i => <i key={i} className={`uc-heat l${i}`} />)}<span>多</span></div>
  </div>;
}

function YearBars({ data, ls }: { data: UsageSeriesData; ls: Lane[] }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const rows = useMemo(() => windowDays(data, ls, 365), [data, ls]);
  const H = 220;
  const plotW = Math.max(0, width - PAD.l - PAD.r);
  const plotH = H - PAD.t - PAD.b;
  const { top, ticks } = niceScale(Math.max(...rows.map(r => r.total)));
  const band = plotW / rows.length;
  const barW = Math.max(1, band > 3 ? band - 1 : band);
  const y = (v: number) => PAD.t + plotH - (v / top) * plotH;
  const hovered = hover === null ? undefined : rows[hover];
  return <div className="uc-plot uc-year" ref={ref} onPointerLeave={() => setHover(null)}>
    {width > 0 && <svg width={width} height={H} role="img" aria-label={`近一年每日 Token 用量柱状图，截至 ${data.today}`}>
      <YGrid ticks={ticks} y={y} width={width} />
      {rows.map((r, i) => r.total > 0 && <rect key={r.day} x={PAD.l + band * i} y={y(r.total)} width={barW} height={PAD.t + plotH - y(r.total)}
        className={hover === i ? "uc-yearbar on" : "uc-yearbar"} />)}
      {rows.map((r, i) => r.day.endsWith("-01") && i < rows.length - 8 &&
        <text key={"m" + r.day} className="uc-axis" x={PAD.l + band * i} y={H - 6}>{+r.day.slice(5, 7)}月</text>)}
      {rows.map((r, i) => <rect key={"hit" + r.day} x={PAD.l + band * i} y={PAD.t} width={band} height={plotH} fill="transparent"
        onPointerEnter={() => setHover(i)} onPointerDown={() => setHover(i)} />)}
    </svg>}
    {hovered && hover !== null && <Tip x={PAD.l + band * (hover + 0.5)} width={width}>
      <DayTip day={hovered.day} ls={ls} values={data.days.find(d => d.day === hovered.day)?.values} total={hovered.total} />
    </Tip>}
  </div>;
}

function Spark({ values, slot }: { values: number[]; slot: number }) {
  const max = Math.max(1, ...values);
  const w = 3, gap = 1, h = 22;
  return <svg className="uc-spark" width={values.length * (w + gap)} height={h} aria-hidden="true">
    {values.map((v, i) => <rect key={i} x={i * (w + gap)} y={v > 0 ? h - Math.max(2, (v / max) * h) : h - 1} width={w}
      height={v > 0 ? Math.max(2, (v / max) * h) : 1} rx={1} fill={v > 0 ? color(slot) : "var(--line)"} />)}
  </svg>;
}

function parseUTC(s?: string) {
  if (!s) return undefined;
  const t = Date.parse(/[zZ]|[+-]\d\d:?\d\d$/.test(s) ? s : s.replace(" ", "T") + "Z");
  return Number.isFinite(t) ? t : undefined;
}

function lastRun(s?: string) {
  const t = parseUTC(s);
  if (t === undefined) return "—";
  const d = new Date(t);
  const hm = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  const today = new Date();
  return d.toDateString() === today.toDateString() ? `今天 ${hm}` : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
}

const windowSize: Record<UsageRange, number> = { today: 1, week: 7, month: 30, all: 371 };

// Sample mode has no server ledger: derive the period rows from the sample year.
function sampleParticipants(data: UsageSeriesData, range: UsageRange): UsageParticipant[] {
  return data.series.map(s => {
    const values = participantDays(data, s.role, s.name, windowSize[range]);
    const value = values.reduce((a, b) => a + b, 0);
    const days = values.filter(v => v > 0).length;
    return { role: s.role, name: s.name, records: days, measured: days, tokens_in: Math.round(value * 0.82), tokens_out: value - Math.round(value * 0.82) };
  });
}

/** Per-participant table: period ledger, 30-day trend, dispatch count and last run. */
export function ExecutorUsage({ range, onRange, data, workers, sample }: PeriodProps & { data: UsageSeriesData; workers: Worker[]; sample: boolean }) {
  const { data: usage, error } = useTokenUsage(range, !sample);
  const [mem, setMem] = useState<{ mode: string; healthy: boolean; outbox_pending: number } | null>(null);
  useEffect(() => {
    if (!sample) api.memoryStatus().then(setMem).catch(() => setMem(null));
  }, [sample]);
  const ls = useMemo(() => laneList(data), [data]);
  const rows = sample ? sampleParticipants(data, range) : usage?.participants ?? [];
  const total = rows.reduce((sum, r) => sum + (r.measured ? r.tokens_in + r.tokens_out : 0), 0);
  const byName = new Map(workers.map(w => [w.name, w]));
  return <section className="ops-panel uc-panel" id="usage-executors">
    <div className="ops-panel-head"><h2><UsersRound />执行者用量</h2>
      <div className="uc-tools">
        {mem && <span className={`uc-chip ${mem.healthy ? "green" : "zinc"}`}>memory {mem.healthy ? "healthy" : "not connected"}{mem.outbox_pending > 0 ? ` · ${mem.outbox_pending} buffered` : ""}</span>}
        <PeriodPicker range={range} onRange={onRange} />
      </div>
    </div>
    {error && <p className="ops-notice uc-inset" role="status">{error}</p>}
    {!sample && !usage && !error ? <p className="ops-empty">读取中…</p> :
      <div className="token-ledger-wrap uc-table"><table className="token-ledger">
        <caption className="sr-only">Brain 与各 Worker 的 Token 用量、近 30 天趋势和派工记录</caption>
        <thead><tr><th>执行者</th><th>输入</th><th>输出</th><th>合计</th><th>占比</th><th>近 30 天</th><th>派工</th><th>最近运行</th></tr></thead>
        <tbody>{rows.map(r => {
          const value = r.tokens_in + r.tokens_out;
          const pct = total && r.measured ? 100 * value / total : null;
          const days = participantDays(data, r.role, r.name, 30);
          const month = days.reduce((a, b) => a + b, 0);
          const slot = slotFor(ls, r.role, r.name);
          const w = r.role === "worker" ? byName.get(r.name) : undefined;
          return <tr key={r.role + ":" + r.name}>
            <th scope="row"><span className="uc-swatch" style={{ background: color(slot) }} />{r.role === "brain" ? "Brain · 大脑" : r.name}
              <small>{!r.records ? "本周期无记录" : !r.measured ? "无已报告用量" : r.measured !== r.records ? `已计量 ${r.measured} / ${r.records} 条` : r.role === "worker" ? "Worker" : "对话与规划"}</small></th>
            <td>{r.measured ? tokenNumber(r.tokens_in) : "—"}</td>
            <td>{r.measured ? tokenNumber(r.tokens_out) : "—"}</td>
            <td><b>{r.measured ? tokenNumber(value) : "—"}</b></td>
            <td>{pct === null ? "—" : (pct < 0.1 && pct > 0 ? "<0.1" : pct.toFixed(1)) + "%"}</td>
            <td><span className="uc-trend"><Spark values={days} slot={slot} /><span>{month ? cnTokens(month) : "—"}</span></span></td>
            <td>{w?.calls ?? "—"}</td>
            <td>{r.role === "worker" ? lastRun(w?.last_run) : "—"}</td>
          </tr>;
        })}</tbody>
      </table></div>}
  </section>;
}
