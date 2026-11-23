import { useEffect, useMemo, useRef, useState } from "react";
import { Check, CircleAlert, LoaderCircle } from "lucide-react";
import { api, type AgentInfo, type Orchestrator, type Provider } from "../api";
import { Button, Input, Select, Textarea } from "../components/ui";
import { Advanced, Choice, Field, Section, Tag, selectCls, updateOrchestrator, useFlash } from "./parts";

// BrainSection holds every brain setting that used to sit on top of the
// workers form, plus the dispatch approval mode from the wizard. Each choice
// says what it means in plain words; rarely needed fields are folded away.

type Source = "none" | "github" | "local";
type BrainForm = {
  mode: "chat" | "agent";
  brainAgent: "claude" | "fusion-code";
  provider: string;
  model: string;
  dispatch: Orchestrator["mode"];
  source: Source;
  repo: string;
  branch: string;
  workDir: string;
  context: string;
};

const isRemote = (s: string) => /^(https?:\/\/|git@|ssh:\/\/)/.test(s);

function formFrom(o: Orchestrator): BrainForm {
  const repo = o.brain_repo ?? "";
  return {
    mode: o.brain_mode === "agent" ? "agent" : "chat",
    brainAgent: o.brain_agent === "fusion-code" ? "fusion-code" : "claude",
    provider: o.brain_provider_id ?? "",
    model: o.brain_model ?? "",
    dispatch: o.mode ?? "confirm",
    source: !repo ? "none" : isRemote(repo) ? "github" : "local",
    repo,
    branch: o.brain_repo_branch ?? "",
    workDir: o.brain_work_dir ?? "",
    context: (o.brain_context_files ?? []).join("\n"),
  };
}

export default function BrainSection({ onGoTools }: { onGoTools?: () => void }) {
  const [stored, setStored] = useState<BrainForm | null>(null);
  const [form, setForm] = useState<BrainForm | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [models, setModels] = useState<string[] | null>(null);
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [flash, setFlash] = useFlash();
  const loaded = useRef(false);

  useEffect(() => {
    api.orchestration().then((r) => {
      const f = formFrom(r.orchestrator ?? { mode: "confirm", brain_agent: "claude", repos: [], workers: [], configured: false });
      // Keep the first answer: a late duplicate load must not wipe choices
      // the user already started making.
      if (loaded.current) return;
      loaded.current = true;
      setStored(f);
      setForm(f);
      setMore(f.source !== "none" || !!f.workDir || !!f.context);
    }).catch((e) => setErr((e as Error).message));
    api.providers().then((r) => setProviders((r.providers ?? []).filter((p) => p.enabled))).catch(() => {});
    api.agents().then((r) => setAgents(r.agents ?? [])).catch(() => {});
  }, []);

  const provider = form?.provider ?? "";
  useEffect(() => {
    if (!provider) { setModels(null); return; }
    let stop = false;
    setModels(null);
    api.models(provider).then((r) => { if (!stop) setModels(r.models ?? []); }).catch(() => { if (!stop) setModels([]); });
    return () => { stop = true; };
  }, [provider]);

  const dirty = useMemo(() => !!form && !!stored && JSON.stringify(form) !== JSON.stringify(stored), [form, stored]);
  if (!form) {
    return err ? <p className="rounded-xl bg-red-500/10 px-3 py-2 text-sm text-red-600 dark:text-red-400">读取失败：{err}</p>
      : <p className="text-sm text-zinc-500">读取中…</p>;
  }
  const set = (p: Partial<BrainForm>) => { setForm({ ...form, ...p }); setErr(""); };
  const claude = agents.find((a) => a.name === "claude");
  const fusionCode = agents.find((a) => a.name === "fusion-code");
  const agentBrainName = form.brainAgent === "fusion-code" ? "fusion-code" : "claude";

  const save = async () => {
    if (form.source === "github" && !isRemote(form.repo.trim())) { setErr("GitHub 仓库地址要以 https:// 或 git@ 开头"); return; }
    if (form.source === "local" && !form.repo.trim().startsWith("/")) { setErr("本机仓库要填完整路径，以 / 开头"); return; }
    setBusy(true);
    setErr("");
    try {
      const next = await updateOrchestrator((o) => ({
        ...o,
        mode: form.dispatch,
        brain_mode: form.mode,
        brain_agent: form.brainAgent,
        brain_provider_id: form.provider,
        brain_model: form.model,
        brain_repo: form.source === "none" ? "" : form.repo.trim(),
        brain_repo_branch: form.source === "github" ? form.branch.trim() : "",
        brain_work_dir: form.workDir.trim(),
        brain_context_files: form.context.split("\n").map((s) => s.trim()).filter(Boolean),
      }));
      const f = formFrom(next);
      setStored(f);
      setForm(f);
      setFlash("大脑的设置已保存");
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const modeOpt = (id: BrainForm["mode"]) => ({ name: "fc-brain-mode", checked: form.mode === id, onSelect: () => set({ mode: id }) });
  // Agent-brain choices set both the mode and which CLI runs it, so picking
  // "Claude Code" / "fusion-code" is one radio move.
  const agentOpt = (id: BrainForm["brainAgent"]) => ({
    name: "fc-brain-mode", checked: form.mode === "agent" && form.brainAgent === id,
    onSelect: () => set({ mode: "agent", brainAgent: id }),
  });
  const dispatchOpt = (id: Orchestrator["mode"]) => ({ name: "fc-brain-dispatch", checked: form.dispatch === id, onSelect: () => set({ dispatch: id }) });
  // Switching between GitHub and a local path starts that field empty instead
  // of carrying a URL into the path box (or the other way round).
  const sourceOpt = (id: Source) => ({
    name: "fc-brain-source", checked: form.source === id,
    onSelect: () => set({ source: id, repo: id === "none" || (id === "github") === isRemote(form.repo) ? form.repo : "" }),
  });

  return (
    <div className="space-y-3 pb-20">
      {flash && <p role="status" className="rounded-xl bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-400">{flash}</p>}

      <Section title="大脑怎么工作">
        <div className="space-y-2">
          <Choice {...modeOpt("chat")} title="直接用模型" badge={<Tag tone="brand">推荐</Tag>}
            desc="大脑负责拆任务、派给 Worker、检查结果，自己不改文件。反应快，花费低，大多数情况选这个。" />
          <Choice {...agentOpt("claude")} title="用 Claude Code 当大脑"
            desc="大脑自己也能翻代码、跑命令，看清楚再派活。更慢，花费更高，适合先要摸清仓库的任务。">
            {claude && !claude.detected && (
              <p className="flex items-start gap-1.5 text-xs leading-5 text-amber-700 dark:text-amber-400">
                <CircleAlert size={14} className="mt-0.5 shrink-0" />
                <span>这台电脑还没装 Claude Code，选了也用不了。{onGoTools && <button type="button" onClick={onGoTools} className="ml-1 font-medium underline underline-offset-2">去安装</button>}</span>
              </p>
            )}
          </Choice>
          <Choice {...agentOpt("fusion-code")} title="用 fusion-code 当大脑"
            desc="和 Claude Code 一样能翻代码、跑命令，但用 fusion-code 自己的模型配置和会话。花费记在 brain:fusion-code 名下。">
            {fusionCode && !fusionCode.detected && (
              <p className="flex items-start gap-1.5 text-xs leading-5 text-amber-700 dark:text-amber-400">
                <CircleAlert size={14} className="mt-0.5 shrink-0" />
                <span>这台电脑还没装 fusion-code，选了也用不了。{onGoTools && <button type="button" onClick={onGoTools} className="ml-1 font-medium underline underline-offset-2">去安装</button>}</span>
              </p>
            )}
          </Choice>
        </div>
      </Section>

      <Section title="用哪个模型">
        {form.mode === "agent" ? (
          <p className="text-sm text-zinc-500 dark:text-zinc-400">{agentBrainName === "fusion-code" ? "fusion-code" : "Claude Code"}用它自己的模型设置，这里不用选。</p>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="模型服务" hint={form.provider ? undefined : "不确定就保持这个，用网关里设好的默认模型。"}>
              <Select className={selectCls} value={form.provider} onChange={(e) => set({ provider: e.target.value, model: "" })}>
                <option value="">自动（网关默认）</option>
                {providers.map((p) => <option key={p.id} value={p.id}>{p.name || p.id}{p.type === "gateway" ? "（网关）" : ""}</option>)}
              </Select>
            </Field>
            <Field label="模型">
              {form.provider && models === null ? (
                <p className="flex h-[38px] items-center gap-1.5 text-sm text-zinc-500"><LoaderCircle size={14} className="animate-spin" />读取模型列表…</p>
              ) : form.provider && models && models.length === 0 ? (
                <Input value={form.model} onChange={(e) => set({ model: e.target.value })} placeholder="读不到列表，手动填模型名" autoCapitalize="off" spellCheck={false} />
              ) : (
                <Select className={selectCls} value={form.model} onChange={(e) => set({ model: e.target.value })} disabled={!form.provider}>
                  <option value="">{form.provider ? "这个服务的默认模型" : "跟随网关"}</option>
                  {form.model && !(models ?? []).includes(form.model) && <option value={form.model}>{form.model}</option>}
                  {(models ?? []).map((m) => <option key={m} value={m}>{m}</option>)}
                </Select>
              )}
            </Field>
          </div>
        )}
      </Section>

      <Section title="派活之前">
        <div className="space-y-2">
          <Choice {...dispatchOpt("confirm")} title="每次先问我" badge={<Tag tone="brand">推荐</Tag>} desc="大脑要让 Worker 动手时，先弹出来让你点确认。" />
          <Choice {...dispatchOpt("auto")} title="直接派" desc="不再询问，只受每个 Worker 的花费上限约束。" />
          <Choice {...dispatchOpt("off")} title="不派活" desc="只聊天。想用哪个 Worker，就在对话里 @ 它。" />
        </div>
      </Section>

      <Section>
        <Advanced open={more} onToggle={() => setMore((v) => !v)}>
          <div>
            <p className="mb-2 text-xs font-medium text-zinc-600 dark:text-zinc-300">大脑看哪份代码</p>
            <div className="space-y-2">
              <Choice {...sourceOpt("none")} title="不看代码" desc="只根据对话和参考文件来安排。" />
              <Choice {...sourceOpt("github")} title="GitHub 仓库" desc="派活前同步到最新。">
                <div className="grid gap-2 sm:grid-cols-[1fr_9rem]">
                  <Input value={form.repo} onChange={(e) => set({ repo: e.target.value })} inputMode="url" autoCapitalize="off" spellCheck={false}
                    placeholder="https://github.com/用户名/仓库.git" aria-label="GitHub 仓库地址" />
                  <Input value={form.branch} onChange={(e) => set({ branch: e.target.value })} placeholder="分支（可不填）" autoCapitalize="off" spellCheck={false} aria-label="分支" />
                </div>
              </Choice>
              <Choice {...sourceOpt("local")} title="本机仓库" desc="直接用这台电脑上的仓库，大脑产出的文件也放在里面。">
                <Input value={form.repo} onChange={(e) => set({ repo: e.target.value })} autoCapitalize="off" spellCheck={false}
                  placeholder="/Users/你的用户名/代码/项目" aria-label="本机仓库路径" />
              </Choice>
            </div>
          </div>
          <Field label="工作目录">
            <Input value={form.workDir} onChange={(e) => set({ workDir: e.target.value })} placeholder="~/.fusion/fusion-console/agents" autoCapitalize="off" spellCheck={false} />
          </Field>
          <Field label="参考文件" hint={form.mode === "agent"
            ? `每行一个路径。${agentBrainName === "fusion-code" ? "fusion-code" : "Claude Code"}还会读工作目录里的 CLAUDE.md，团队约定可以写在那里。合计最多 12 KB。`
            : "每行一个路径，比如团队约定、仓库清单。内容会一起交给大脑，合计最多 12 KB。"}>
            <Textarea rows={3} value={form.context} onChange={(e) => set({ context: e.target.value })} placeholder="~/fusion/CLAUDE.md"
              className="font-mono text-xs" autoCapitalize="off" spellCheck={false} />
          </Field>
        </Advanced>
      </Section>

      {(dirty || err) && (
        <div className="sticky bottom-3 z-10">
          <div className="flex flex-wrap items-center gap-2 rounded-2xl border border-zinc-200/80 bg-white/95 p-2 pl-4 shadow-lg backdrop-blur dark:border-zinc-700 dark:bg-zinc-900/95">
            <p className={`min-w-0 flex-1 text-sm ${err ? "text-red-600 dark:text-red-400" : "text-zinc-600 dark:text-zinc-300"}`}>{err || "有改动还没保存"}</p>
            {dirty && <>
              <Button variant="ghost" onClick={() => { setForm(stored); setErr(""); }} disabled={busy}>还原</Button>
              <Button onClick={save} disabled={busy}>{busy ? <LoaderCircle size={16} className="animate-spin" /> : <Check size={16} />} 保存</Button>
            </>}
          </div>
        </div>
      )}
    </div>
  );
}
