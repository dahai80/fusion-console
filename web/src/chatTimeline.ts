// Timeline merge discipline for the conversation view (issue #100 N1, from
// Paseo's timeline-sync triangle): the authoritative source is the server's
// message list; live appends (team inbox poll, background-run receipts) are
// merged in only when they are not already present, so a poll racing the
// authoritative refresh can never duplicate or reorder a row.

export type TimelineEntry = { role: string; content: string; meta?: string };

// entryKey identifies a row. Server messages carry no ids to the client, so
// the meta payload (team_msg_id, background_run, task_id, ...) is the stable
// part when present; role+content is the fallback for plain rows.
export const entryKey = (m: TimelineEntry) => {
  const meta = m.meta ?? "";
  if (meta) {
    try {
      const parsed = JSON.parse(meta) as Record<string, unknown>;
      for (const k of ["team_msg_id", "background_run", "task_id"]) {
        if (parsed[k] !== undefined) return `${m.role}|${k}:${String(parsed[k])}`;
      }
    } catch { /* non-JSON meta — fall through to content key */ }
    return `${m.role}|meta:${meta}`;
  }
  return `${m.role}|c:${m.content}`;
};

// mergeEntries appends incoming rows that the current view does not already
// show. A row already present by key is skipped; everything else keeps its
// arrival order (live appends are chronological by construction).
export function mergeEntries(current: TimelineEntry[], incoming: TimelineEntry[]): TimelineEntry[] {
  if (!incoming.length) return current;
  const seen = new Set(current.map(entryKey));
  const out = current.slice();
  for (const m of incoming) {
    const k = entryKey(m);
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(m);
  }
  return out;
}

// supersedeWith is the authoritative-refresh path: when the server list
// disagrees with the view, the server wins wholesale — except a trailing
// in-progress assistant bubble (empty content placeholder for a live turn),
// which is not on the server yet and must survive the refresh.
export function supersedeWith(current: TimelineEntry[], authoritative: TimelineEntry[]): TimelineEntry[] {
  const live = current.at(-1);
  if (live && live.role === "assistant" && live.content === "" && !live.meta) {
    return [...authoritative, live];
  }
  return authoritative;
}
