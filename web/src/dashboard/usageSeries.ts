import type { UsageSeriesData } from "../api";

// Brain + up to six workers get their own color; the rest fold into 其他.
export const WORKER_SLOTS = 6;
export const OTHER_SLOT = 7;

const integer = new Intl.NumberFormat("zh-CN");
const trim = (s: string) => s.replace(/\.0+$/, "").replace(/(\.\d*?[1-9])0+$/, "$1");

/** Token count in 万 / 亿 units: 1.38 亿, 2,647 万, 4.5 万, 812. */
export function cnTokens(n: number): string {
  if (n >= 1e8) return trim((n / 1e8).toFixed(n >= 1e10 ? 0 : n >= 1e9 ? 1 : 2)) + " 亿";
  if (n >= 1e6) return integer.format(Math.round(n / 1e4)) + " 万";
  if (n >= 1e4) return trim((n / 1e4).toFixed(1)) + " 万";
  return integer.format(Math.round(n));
}

/** Compact axis tick: 8亿, 6000万, 500. */
export function axisTokens(v: number): string {
  if (v <= 0) return "0";
  if (v >= 1e8) return trim((v / 1e8).toFixed(2)) + "亿";
  if (v >= 1e4) return trim((v / 1e4).toFixed(1)) + "万";
  return integer.format(v);
}

/** Round axis with 3–5 intervals of 1/2/2.5/5 × 10^k, picking the tightest top ≥ max. */
export function niceScale(max: number): { top: number; ticks: number[] } {
  const m = Math.max(max, 1);
  const base = 10 ** Math.floor(Math.log10(m / 5));
  let best = { top: Infinity, step: 1 };
  for (const mag of [base / 10, base, base * 10]) {
    for (const f of [1, 2, 2.5, 5]) {
      const step = f * mag;
      if (step < 1 || !Number.isInteger(step)) continue;
      const n = Math.max(3, Math.ceil(m / step));
      if (n <= 5 && n * step < best.top) best = { top: n * step, step };
    }
  }
  if (!Number.isFinite(best.top)) best = { top: 3, step: 1 };
  return { top: best.top, ticks: Array.from({ length: Math.round(best.top / best.step) + 1 }, (_, i) => i * best.step) };
}

const DAY = 86400000;
export const addDays = (day: string, n: number) => new Date(Date.parse(day + "T00:00:00Z") + n * DAY).toISOString().slice(0, 10);
/** 0 = Monday … 6 = Sunday. */
export const mondayIndex = (day: string) => (new Date(day + "T00:00:00Z").getUTCDay() + 6) % 7;
const WEEKDAY = "一二三四五六日";
export const shortDay = (day: string) => `${+day.slice(5, 7)}/${+day.slice(8, 10)}`;
export const dayLabel = (day: string) => `${shortDay(day)} 周${WEEKDAY[mondayIndex(day)]}`;
/** Local calendar date of a timestamp in the viewer's zone. */
export const localDay = (t: number) => {
  const d = new Date(t);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};

export type Lane = { key: string; name: string; label: string; slot: number; indexes: number[]; total: number };

/** Chart lanes in stacking order: Brain, then workers by yearly total, then 其他. */
export function lanes(data: Pick<UsageSeriesData, "series">): Lane[] {
  const out: Lane[] = [];
  const other: Lane = { key: "other", name: "其他", label: "其他", slot: OTHER_SLOT, indexes: [], total: 0 };
  let next = 1;
  data.series.forEach((s, i) => {
    if (s.role === "brain") out.unshift({ key: "brain", name: s.name, label: "Brain · 大脑", slot: 0, indexes: [i], total: s.total });
    else if (s.total <= 0) return;
    else if (next <= WORKER_SLOTS) out.push({ key: "w:" + s.name, name: s.name, label: s.name, slot: next++, indexes: [i], total: s.total });
    else { other.indexes.push(i); other.total += s.total; }
  });
  if (other.indexes.length) out.push(other);
  return out;
}

/** Color slot for a participant row (brain → 0, folded/idle workers → 其他). */
export function slotFor(all: Lane[], role: string, name: string): number {
  if (role === "brain") return 0;
  return all.find(l => l.key === "w:" + name)?.slot ?? OTHER_SLOT;
}

export type DayRow = { day: string; total: number; values: number[] };

/** The last n local days ending today, one value per lane (zeros filled). */
export function windowDays(data: UsageSeriesData, ls: Lane[], n: number): DayRow[] {
  const lookup = new Map(data.days.map(d => [d.day, d]));
  return Array.from({ length: n }, (_, i) => {
    const day = addDays(data.today, i - n + 1);
    const row = lookup.get(day);
    const values = ls.map(l => l.indexes.reduce((sum, k) => sum + (row?.values[k] ?? 0), 0));
    return { day, total: row?.total ?? 0, values };
  });
}

/** One participant's last n local days (by series name; brain uses role). */
export function participantDays(data: UsageSeriesData, role: string, name: string, n: number): number[] {
  const index = data.series.findIndex(s => s.role === role && (role === "brain" || s.name === name));
  const lookup = new Map(data.days.map(d => [d.day, d]));
  return Array.from({ length: n }, (_, i) => index < 0 ? 0 : lookup.get(addDays(data.today, i - n + 1))?.values[index] ?? 0);
}

/** 53 Monday-first week columns ending with the week that contains today. */
export function calendarStart(today: string): string {
  return addDays(today, -mondayIndex(today) - 52 * 7);
}

/** Month label columns: a label wherever a week's Monday enters a new month. */
export function monthColumns(start: string, weeks = 53): { col: number; label: string }[] {
  const out: { col: number; label: string }[] = [];
  let prev = "";
  for (let c = 0; c < weeks; c++) {
    const month = addDays(start, c * 7).slice(0, 7);
    if (month !== prev) out.push({ col: c, label: `${+month.slice(5)}月` });
    prev = month;
  }
  // A partial first month would collide with the next label.
  if (out.length > 1 && out[1].col - out[0].col < 3) out.shift();
  return out;
}

export const heatLevel = (v: number, max: number) => v <= 0 ? 0 : Math.min(4, Math.max(1, Math.ceil(4 * Math.sqrt(v / Math.max(max, 1)))));

/** Sum of the last n days ending today. */
export function recentTotal(data: UsageSeriesData, n: number): number {
  const from = addDays(data.today, -n + 1);
  return data.days.reduce((sum, d) => d.day >= from && d.day <= data.today ? sum + d.total : sum, 0);
}
