import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { UsageSeriesData } from "../api";
import { axisTokens, calendarStart, cnTokens, heatLevel, lanes, monthColumns, mondayIndex, niceScale, participantDays, recentTotal, slotFor, windowDays } from "./usageSeries.ts";

test("token units follow 万 / 亿 conventions", () => {
  assert.equal(cnTokens(138_000_000), "1.38 亿");
  assert.equal(cnTokens(8_950_000_000), "89.5 亿");
  assert.equal(cnTokens(26_470_000), "2,647 万");
  assert.equal(cnTokens(45_000), "4.5 万");
  assert.equal(cnTokens(812), "812");
  assert.equal(axisTokens(800_000_000), "8亿");
  assert.equal(axisTokens(60_000_000), "6000万");
  assert.equal(axisTokens(0), "0");
});

test("nice scale covers the max with round steps", () => {
  assert.deepEqual(niceScale(760_000_000), { top: 800_000_000, ticks: [0, 2e8, 4e8, 6e8, 8e8] });
  assert.deepEqual(niceScale(42_000_000).ticks, [0, 1e7, 2e7, 3e7, 4e7, 5e7]);
  assert.deepEqual(niceScale(0), { top: 3, ticks: [0, 1, 2, 3] });
  assert.equal(niceScale(9_100).top, 10_000);
});

const data: UsageSeriesData = {
  tz_offset_min: 480, today: "2026-11-20", start: "2025-11-15", first_day: "2026-08-03",
  series: [
    { name: "Brain", role: "brain", total: 10 },
    ...["a", "b", "c", "d", "e", "f", "g", "h"].map((name, i) => ({ name, role: "worker" as const, total: 80 - i * 10 })),
    { name: "idle", role: "worker", total: 0 },
  ],
  days: [
    { day: "2026-11-19", total: 30, records: 3, measured: 3, values: [10, 0, 0, 0, 0, 0, 0, 5, 15, 0] },
    { day: "2026-11-20", total: 7, records: 1, measured: 1, values: [0, 7, 0, 0, 0, 0, 0, 0, 0, 0] },
    { day: "2025-01-01", total: 99, records: 1, measured: 1, values: [99, 0, 0, 0, 0, 0, 0, 0, 0, 0] },
  ],
  hours: { today: Array(24).fill(0), yesterday: Array(24).fill(0) },
};

test("lanes keep brain first, six worker colors, fold the rest, skip idle", () => {
  const ls = lanes(data);
  assert.deepEqual(ls.map(l => l.label), ["Brain · 大脑", "a", "b", "c", "d", "e", "f", "其他"]);
  assert.deepEqual(ls.at(-1)!.indexes, [7, 8]);
  assert.equal(slotFor(ls, "worker", "b"), 2);
  assert.equal(slotFor(ls, "worker", "h"), 7);
  assert.equal(slotFor(ls, "brain", "Brain"), 0);
});

test("windows fill missing days and sum folded lanes", () => {
  const rows = windowDays(data, lanes(data), 3);
  assert.deepEqual(rows.map(r => r.day), ["2026-11-18", "2026-11-19", "2026-11-20"]);
  assert.deepEqual(rows[0].values, [0, 0, 0, 0, 0, 0, 0, 0]);
  assert.deepEqual(rows[1].values, [10, 0, 0, 0, 0, 0, 0, 20]);
  assert.deepEqual(participantDays(data, "worker", "a", 2), [0, 7]);
  assert.deepEqual(participantDays(data, "brain", "", 2), [10, 0]);
  assert.equal(recentTotal(data, 365), 37);
});

test("calendar spans 53 Monday-first weeks ending this week", () => {
  const start = calendarStart("2026-11-20");
  assert.equal(mondayIndex(start), 0);
  assert.equal(start, "2025-11-17");
  const months = monthColumns(start);
  assert.equal(months[0].label, "12月");
  assert.equal(months.at(-1)!.label, "11月");
  assert.equal(heatLevel(0, 10), 0);
  assert.equal(heatLevel(10, 10), 4);
  assert.equal(heatLevel(0.1, 10), 1);
});
