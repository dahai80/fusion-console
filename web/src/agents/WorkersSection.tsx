import { useCallback, useEffect, useMemo, useState } from "react";
import { AtSign, Bot, Check, ChevronRight, CircleAlert, Copy, LoaderCircle, Plus, Trash2 } from "lucide-react";
import { api, type AgentInfo, type Orchestrator, type Provider, type WorkerSettings, type WorkerStatus } from "../api";
import { Button } from "../components/ui";
import { WorkerForm } from "./WorkerForm";
import { Sheet, Switch, Tag, copyText, updateOrchestrator, useFlash } from "./parts";
import {
  agoText, draftFrom, elapsedText, emptyDraft, mergeRows, placeText, summaryText, toSettings, tokensText, toolLabel, validate, workerKey,
  type WorkerDraft, type WorkerRow,
} from "./workerModel";

// WorkersSection is the single place for workers: live status on each card
// (formerly the Dashboard's Workers tab), open a card to manage that worker,
// and "新建 Worker" uses the same sheet with a short form.
// `embedded` (the onboarding wizard) drops the summary row while the list is
// empty, since the wizard already says there are no workers.
export default function WorkersSection({ onGoTools, onChanged, embedded }: { onGoTools?: () => void; onChanged?: () => void; embedded?: boolean }) {
  const [orch, setOrch] = useState<Orchestrator | null>(null);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [status, setStatus] = useState<WorkerStatus[]>([]);
  const [loadErr, setLoadErr] = useState("");
  const [opened, setOpened] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [flash, setFlash] = useFlash();

  const loadStatus = useCallback(() => {
    if (document.hidden) return;
    api.workersStatus().then((r) => setStatus(r.workers ?? [])).catch(() => {});
  }, []);
  useEffect(() => {
    api.orchestration().then((r) => setOrch(r.orchestrator)).catch((e) => setLoadErr((e as Error).message));
    api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
    api.providers().then((r) => setProviders((r.providers ?? []).filter((p) => p.enabled))).catch(() => {});
    loadStatus();
    const t = setInterval(loadStatus, 5000);
    document.addEventListener("visibilitychange", loadStatus);
    return () => { clearInterval(t); document.removeEventListener("visibilitychange", loadStatus); };
  }, [loadStatus]);

  const workers = orch?.workers ?? [];
  const rows = useMemo(() => mergeRows(workers, status), [workers, status]);
  const busy = rows.filter((r) => r.st?.busy).length;
  const detected = (agent: string) => agents.find((a) => a.name === agent)?.detected ?? true;
  const current = rows.find((r) => r.key === opened) ?? null;

  const saved = (next: Orchestrator, msg: string) => {
    setOrch(next);
    setFlash(msg);
    onChanged?.();
    loadStatus();
  };

  return (
    <div className="space-y-3">
      {!(embedded && orch !== null && workers.length === 0) && <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-zinc-500 dark:text-zinc-400">
          {orch === null ? (loadErr ? "" : "读取中…") : workers.length === 0 ? "还没有 Worker" : (
            <>{workers.length} 个 Worker{busy > 0 && <span className="text-amber-600 dark:text-amber-400"> · {busy} 个在干活</span>}</>
          )}
        </p>
        <Button onClick={() => setCreating(true)} disabled={orch === null} className="shrink-0"><Plus size={16} /> 新建 Worker</Button>
      </div>}

      {loadErr && <p className="rounded-xl bg-red-500/10 px-3 py-2 text-sm text-red-600 dark:text-red-400">读取失败：{loadErr}</p>}
      {flash && <p role="status" className="rounded-xl bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-400">{flash}</p>}

      {orch !== null && workers.length === 0 && (
        <div className="rounded-2xl border border-dashed border-zinc-300 px-5 py-10 text-center dark:border-zinc-700">
          <Bot size={22} className="mx-auto text-zinc-400" />
          <p className="mt-3 text-sm font-medium">还没有 Worker</p>
          <p className="mx-auto mt-1 max-w-sm text-sm text-zinc-500 dark:text-zinc-400">Worker 是装在这台电脑上的编程工具，比如 Claude Code。建好以后，大脑会把具体的活交给它。</p>
          <Button className="mt-4" onClick={() => setCreating(true)}><Plus size={16} /> 新建第一个</Button>
        </div>
      )}

      <div className="grid gap-3 md:grid-cols-2">
        {rows.map((r) => <WorkerCard key={r.key} row={r} detected={detected(r.cfg.agent)} onOpen={() => setOpened(r.key)} />)}
      </div>

      <NewWorkerSheet open={creating} onClose={() => setCreating(false)} agents={agents} providers={providers} workers={workers} onGoTools={onGoTools}
        onCreated={(next, name) => { setCreating(false); saved(next, `已添加 ${name}`); }} />
      <WorkerSheet row={current} onClose={() => setOpened(null)} agents={agents} providers={providers} workers={workers}
        detected={current ? detected(current.cfg.agent) : true} onGoTools={onGoTools}
        onSaved={(next, key, msg) => { saved(next, msg); setOpened(key); }}
        onRemoved={(next, name) => { setOpened(null); saved(next, `已删除 ${name}`); }} />
    </div>
  );
}

function StatusTag({ row, detected }: { row: WorkerRow; detected: boolean }) {
  if (!row.cfg.enabled) return <Tag>已停用</Tag>;
  if (row.st?.busy) return <Tag tone="busy"><LoaderCircle size={11} className="animate-spin" />干活中 · {elapsedText(row.st.elapsed_s ?? 0)}</Tag>;
  if (!detected) return <Tag tone="warn">工具未安装</Tag>;
  return <Tag tone="ok">空闲</Tag>;
}

function WorkerCard({ row, detected, onOpen }: { row: WorkerRow; detected: boolean; onOpen: () => void }) {
  const st = row.st;
  const tokens = (st?.tokens_in ?? 0) + (st?.tokens_out ?? 0);
  return (
    <button type="button" onClick={onOpen}
      className={`group flex w-full items-start gap-3 rounded-2xl border bg-white p-4 text-left shadow-sm shadow-zinc-900/5 transition-colors hover:border-zinc-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500/50 dark:bg-zinc-900 dark:hover:border-zinc-700 ${
        st?.busy ? "border-amber-300/70 dark:border-amber-500/40" : "border-zinc-200/80 dark:border-zinc-800"} ${row.cfg.enabled ? "" : "opacity-70"}`}>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate font-medium">{row.key}</span>
          <StatusTag row={row} detected={detected} />
        </div>
        <p className="mt-1 truncate text-xs text-zinc-500 dark:text-zinc-400">{toolLabel(row.cfg.agent)} · {placeText(row.cfg)}</p>
        {st?.busy && st.task && <p className="mt-2 line-clamp-2 text-sm text-zinc-700 dark:text-zinc-200">{st.task}</p>}
        <p className="mt-2 text-xs text-zinc-400">
          {st && st.calls > 0 ? `跑过 ${st.calls} 次 · ${tokensText(tokens)} tokens · $${(st.cost_usd ?? 0).toFixed(2)}${st.last_run ? ` · ${agoText(st.last_run)}` : ""}` : "还没跑过"}
        </p>
      </div>
      <ChevronRight size={18} className="mt-0.5 shrink-0 text-zinc-300 transition-colors group-hover:text-zinc-500 dark:text-zinc-600" />
    </button>
  );
}

function NewWorkerSheet({ open, onClose, agents, providers, workers, onGoTools, onCreated }: {
  open: boolean; onClose: () => void; agents: AgentInfo[]; providers: Provider[]; workers: WorkerSettings[];
  onGoTools?: () => void; onCreated: (next: Orchestrator, name: string) => void;
}) {
  const [draft, setDraft] = useState<WorkerDraft>(() => emptyDraft(agents));
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  // Fresh form each time the sheet opens; preselect a tool once they load.
  useEffect(() => { if (open) { setDraft(emptyDraft(agents)); setErr(""); } }, [open]);
  useEffect(() => { if (open && !draft.agent && agents.length) setDraft((d) => ({ ...d, agent: emptyDraft(agents).agent })); }, [agents]);

  const create = async () => {
    const problem = validate(draft, workers);
    if (problem) { setErr(problem); return; }
    setBusy(true);
    setErr("");
    try {
      let name = "";
      const next = await updateOrchestrator((o) => {
        const w = toSettings(draft, o.workers);
        if (o.workers.some((x) => workerKey(x) === workerKey(w))) throw new Error(`已经有叫 ${workerKey(w)} 的 Worker 了`);
        name = workerKey(w);
        return { ...o, configured: true, workers: [...o.workers, w] };
      });
      onCreated(next, name);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet open={open} onClose={onClose} title="新建 Worker"
      footer={
        <div className="space-y-2">
          {err ? <p className="flex items-start gap-1.5 text-sm text-red-600 dark:text-red-400"><CircleAlert size={15} className="mt-0.5 shrink-0" />{err}</p>
            : <p className="text-xs leading-5 text-zinc-500 dark:text-zinc-400">{summaryText(draft, workers)}</p>}
          <div className="flex gap-2">
            <Button variant="outline" onClick={onClose} className="flex-1 sm:flex-none">取消</Button>
            <Button onClick={create} disabled={busy || !draft.agent} className="flex-1">{busy ? <LoaderCircle size={16} className="animate-spin" /> : <Plus size={16} />} 创建</Button>
          </div>
        </div>
      }>
      <WorkerForm draft={draft} onChange={(p) => { setDraft((d) => ({ ...d, ...p })); setErr(""); }} agents={agents} providers={providers} workers={workers}
        onGoTools={onGoTools && (() => { onClose(); onGoTools(); })} />
    </Sheet>
  );
}

function WorkerSheet({ row, onClose, agents, providers, workers, detected, onGoTools, onSaved, onRemoved }: {
  row: WorkerRow | null; onClose: () => void; agents: AgentInfo[]; providers: Provider[]; workers: WorkerSettings[]; detected: boolean;
  onGoTools?: () => void; onSaved: (next: Orchestrator, key: string, msg: string) => void; onRemoved: (next: Orchestrator, name: string) => void;
}) {
  const [draft, setDraft] = useState<WorkerDraft | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [copied, setCopied] = useState(false);
  const key = row?.key ?? null;
  // Reset the form whenever a different worker is opened (not on status polls).
  useEffect(() => {
    setDraft(row ? draftFrom(row.cfg) : null);
    setErr("");
    setConfirmDelete(false);
  }, [key]);
  useEffect(() => {
    if (!confirmDelete) return;
    const t = setTimeout(() => setConfirmDelete(false), 4000);
    return () => clearTimeout(t);
  }, [confirmDelete]);

  if (!row || !draft) return <Sheet open={false} title="" onClose={onClose}>{null}</Sheet>;
  const st = row.st;
  const dirty = JSON.stringify(draftFrom(row.cfg)) !== JSON.stringify(draft);
  const tokens = (st?.tokens_in ?? 0) + (st?.tokens_out ?? 0);

  const patch = async (fn: (w: WorkerSettings, all: WorkerSettings[]) => WorkerSettings | null, msg: (w: WorkerSettings) => string) => {
    setBusy(true);
    setErr("");
    try {
      const out: { w: WorkerSettings | null } = { w: null };
      const next = await updateOrchestrator((o) => {
        const idx = o.workers.findIndex((w) => workerKey(w) === row.key);
        if (idx < 0) throw new Error("这个 Worker 已经不在了，刷新一下页面");
        const others = o.workers.filter((_, i) => i !== idx);
        out.w = fn(o.workers[idx], others);
        const list = [...o.workers];
        if (out.w) list[idx] = out.w; else list.splice(idx, 1);
        return { ...o, workers: list };
      });
      if (out.w) onSaved(next, workerKey(out.w), msg(out.w));
      else onRemoved(next, row.key);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const save = () => {
    const problem = validate(draft, workers, row.key);
    if (problem) { setErr(problem); return; }
    void patch((w, others) => toSettings(draft, others, w), (w) => `${workerKey(w)} 的设置已保存`);
  };

  return (
    <Sheet open onClose={onClose} title={row.key} subtitle={`${toolLabel(row.cfg.agent)} · ${placeText(row.cfg)}`}
      footer={dirty || err ? (
        <div className="space-y-2">
          {err && <p className="flex items-start gap-1.5 text-sm text-red-600 dark:text-red-400"><CircleAlert size={15} className="mt-0.5 shrink-0" />{err}</p>}
          {dirty && (
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => { setDraft(draftFrom(row.cfg)); setErr(""); }} disabled={busy} className="flex-1 sm:flex-none">还原</Button>
              <Button onClick={save} disabled={busy} className="flex-1">{busy ? <LoaderCircle size={16} className="animate-spin" /> : <Check size={16} />} 保存修改</Button>
            </div>
          )}
        </div>
      ) : undefined}>
      <div className="space-y-5">
        <div className={`rounded-xl px-3 py-3 text-sm ${st?.busy ? "bg-amber-500/10" : "bg-white ring-1 ring-zinc-200/80 dark:bg-zinc-900 dark:ring-zinc-800"}`}>
          {!row.cfg.enabled ? (
            <p className="text-zinc-600 dark:text-zinc-300">已停用，大脑不会派活给它。</p>
          ) : st?.busy ? (
            <>
              <p className="flex items-center gap-1.5 font-medium text-amber-700 dark:text-amber-300"><LoaderCircle size={14} className="animate-spin" />正在干活 · {elapsedText(st.elapsed_s ?? 0)}</p>
              {st.task && <p className="mt-1.5 text-zinc-800 dark:text-zinc-100">{st.task}</p>}
              {st.detail && <p className="mt-1 line-clamp-3 text-xs text-zinc-500 dark:text-zinc-400">{st.detail}</p>}
            </>
          ) : !detected ? (
            <p className="flex items-start gap-1.5 text-amber-700 dark:text-amber-400"><CircleAlert size={15} className="mt-0.5 shrink-0" />
              <span>这台电脑上找不到 {toolLabel(row.cfg.agent)}，它现在接不了活。{onGoTools && <button type="button" onClick={() => { onClose(); onGoTools(); }} className="ml-1 font-medium underline underline-offset-2">去安装</button>}</span>
            </p>
          ) : (
            <>
              <p className="font-medium text-emerald-700 dark:text-emerald-400">空闲，可以接活</p>
              {st?.last_task && <p className="mt-1 line-clamp-2 text-xs text-zinc-500 dark:text-zinc-400">上一件：{st.last_task}{st.last_elapsed_s ? `（用了 ${elapsedText(st.last_elapsed_s)}）` : ""}</p>}
            </>
          )}
        </div>

        <dl className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          {[
            ["跑过", st?.calls ? `${st.calls} 次` : "0 次"],
            ["Token", tokensText(tokens)],
            ["花费", `$${(st?.cost_usd ?? 0).toFixed(2)}`],
            ["上次", st?.last_run ? agoText(st.last_run) : "—"],
          ].map(([k, v]) => (
            <div key={k} className="rounded-xl bg-white px-3 py-2 ring-1 ring-zinc-200/80 dark:bg-zinc-900 dark:ring-zinc-800">
              <dt className="text-[11px] text-zinc-500 dark:text-zinc-400">{k}</dt>
              <dd className="mt-0.5 truncate text-sm font-semibold tabular-nums">{v}</dd>
            </div>
          ))}
        </dl>

        <div className="divide-y divide-zinc-200/80 rounded-xl bg-white ring-1 ring-zinc-200/80 dark:divide-zinc-800 dark:bg-zinc-900 dark:ring-zinc-800">
          <div className="flex items-center justify-between gap-3 px-3 py-2.5">
            <span className="text-sm">接活</span>
            <Switch label="接活" checked={row.cfg.enabled} disabled={busy}
              onChange={(v) => void patch((w) => ({ ...w, enabled: v }), (w) => (v ? `${workerKey(w)} 已启用` : `${workerKey(w)} 已停用`))} />
          </div>
          <button type="button" onClick={async () => { if (await copyText(`@${row.key} `)) { setCopied(true); setTimeout(() => setCopied(false), 1500); } }}
            className="flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left text-sm hover:bg-zinc-50 dark:hover:bg-zinc-800/50">
            <span className="flex items-center gap-1.5"><AtSign size={14} className="text-zinc-400" />在对话里点名：<code className="text-indigo-700 dark:text-indigo-300">@{row.key}</code></span>
            <span className="flex items-center gap-1 text-xs text-zinc-500">{copied ? <><Check size={13} />已复制</> : <><Copy size={13} />复制</>}</span>
          </button>
        </div>

        <div className="rounded-xl bg-white p-3 ring-1 ring-zinc-200/80 dark:bg-zinc-900 dark:ring-zinc-800 sm:p-4">
          <p className="mb-4 text-sm font-semibold">设置</p>
          <WorkerForm draft={draft} onChange={(p) => { setDraft((d) => (d ? { ...d, ...p } : d)); setErr(""); }} agents={agents} providers={providers}
            workers={workers} editing={row.key} onGoTools={onGoTools && (() => { onClose(); onGoTools(); })} />
        </div>

        <div className="pb-2">
          <Button variant={confirmDelete ? "destructive" : "ghost"} disabled={busy}
            className={confirmDelete ? "w-full" : "w-full text-red-600 hover:bg-red-500/10 dark:text-red-400"}
            onClick={() => (confirmDelete ? void patch(() => null, () => "") : setConfirmDelete(true))}>
            <Trash2 size={15} /> {confirmDelete ? `再点一次，删除 ${row.key}` : "删除这个 Worker"}
          </Button>
        </div>
      </div>
    </Sheet>
  );
}
