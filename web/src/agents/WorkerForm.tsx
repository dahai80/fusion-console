import { useEffect, useState } from "react";
import { CircleAlert } from "lucide-react";
import { api, type AgentInfo, type Provider, type WorkerSettings } from "../api";
import { Input, Select } from "../components/ui";
import { Advanced, Choice, Field, selectCls } from "./parts";
import { nextName, toolChoices, toolLabel, type Place, type WorkerDraft } from "./workerModel";

// WorkerForm is shared by "new worker" and the settings part of an opened
// worker. The first screen asks only three things (tool, where it works,
// name); everything else sits under 高级选项 with working defaults.
export function WorkerForm({ draft, onChange, agents, providers, workers, editing, onGoTools }: {
  draft: WorkerDraft;
  onChange: (patch: Partial<WorkerDraft>) => void;
  agents: AgentInfo[];
  providers: Provider[];
  workers: WorkerSettings[];
  editing?: string; // key of the worker being edited
  onGoTools?: () => void;
}) {
  const tools = toolChoices(agents);
  const tool = agents.find((a) => a.name === draft.agent);
  const others = workers.filter((w) => (w.name || w.agent) !== editing);
  const autoName = editing || nextName(draft.agent || "worker", others);
  const [more, setMore] = useState(() => !!editing && !!(draft.workDir || draft.provider || draft.model || draft.branch || draft.memory !== "notes" || draft.budget !== 5));
  const place = (id: Place) => ({ name: "fc-worker-place", checked: draft.place === id, onSelect: () => onChange({ place: id }) });

  return (
    <div className="space-y-6">
      <div>
        <p className="mb-2 text-sm font-medium">用哪个工具</p>
        {editing ? (
          <p className="text-sm text-zinc-600 dark:text-zinc-300">{toolLabel(draft.agent)} <span className="text-zinc-400">· {tool?.command ?? draft.agent}</span></p>
        ) : tools.length === 0 ? (
          <p className="text-sm text-zinc-500">读取本机工具…</p>
        ) : (
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            {tools.map((a) => {
              const on = draft.agent === a.name;
              return (
                <button key={a.name} type="button" aria-pressed={on} onClick={() => onChange({ agent: a.name })}
                  className={`flex min-h-16 flex-col items-start justify-center rounded-xl border px-3 py-2.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500/50 ${
                    on ? "border-indigo-500 bg-indigo-500/[0.04] dark:bg-indigo-500/[0.07]" : "border-zinc-200/80 hover:border-zinc-300 dark:border-zinc-800 dark:hover:border-zinc-700"}`}>
                  <span className={`text-sm font-medium ${on ? "text-indigo-700 dark:text-indigo-300" : ""}`}>{toolLabel(a.name)}</span>
                  <span className={`mt-0.5 text-xs ${a.detected ? "text-emerald-600 dark:text-emerald-400" : "text-zinc-400"}`}>{a.detected ? "已安装" : "未安装"}</span>
                </button>
              );
            })}
          </div>
        )}
        {tool && !tool.detected && (
          <p className="mt-2 flex items-start gap-1.5 text-xs leading-5 text-amber-700 dark:text-amber-400">
            <CircleAlert size={14} className="mt-0.5 shrink-0" />
            <span>这台电脑还没装 {toolLabel(tool.name)}，装好之前它接不了活。
              {onGoTools && <button type="button" onClick={onGoTools} className="ml-1 font-medium underline underline-offset-2">去安装</button>}
            </span>
          </p>
        )}
      </div>

      <div>
        <p className="mb-2 text-sm font-medium">在哪干活</p>
        <div className="space-y-2">
          <Choice {...place("standalone")} title="独立文件夹" desc="不关联代码仓库，适合整理资料、写文档、跑脚本。" />
          <Choice {...place("github")} title="GitHub 仓库" desc="每次派活前先拉到最新；仓库里有没提交的改动时不会覆盖。">
            <Input value={draft.repoUrl} onChange={(e) => onChange({ repoUrl: e.target.value })} inputMode="url" autoCapitalize="off" autoCorrect="off" spellCheck={false}
              placeholder="https://github.com/用户名/仓库.git" aria-label="GitHub 仓库地址" />
          </Choice>
          <Choice {...place("worktree")} title="本机仓库" desc="每个任务在仓库里单独开一个工作区，几件事同时做也不打架。">
            <Input value={draft.repoPath} onChange={(e) => onChange({ repoPath: e.target.value })} autoCapitalize="off" autoCorrect="off" spellCheck={false}
              placeholder="/Users/你的用户名/代码/项目" aria-label="本机仓库路径" />
          </Choice>
        </div>
      </div>

      <Field label="名字" hint={<>在对话里输入 <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">@{draft.name.trim() || autoName}</code> 就能直接找它</>}>
        <Input value={draft.name} onChange={(e) => onChange({ name: e.target.value })} placeholder={autoName} autoCapitalize="off" autoCorrect="off" spellCheck={false} />
      </Field>

      <Advanced open={more} onToggle={() => setMore((v) => !v)}>
        {draft.place === "github" && (
          <Field label="分支">
            <Input value={draft.branch} onChange={(e) => onChange({ branch: e.target.value })} placeholder="仓库默认分支" autoCapitalize="off" spellCheck={false} />
          </Field>
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="模型服务">
            <Select className={selectCls} value={draft.provider} onChange={(e) => onChange({ provider: e.target.value, model: "" })}>
              <option value="">沿用工具自己的设置</option>
              {providers.map((p) => <option key={p.id} value={p.id}>{p.name || p.id}{p.type === "gateway" ? "（网关）" : ""}</option>)}
            </Select>
          </Field>
          <ModelField key={draft.provider} provider={draft.provider} value={draft.model}
            onChange={(model) => onChange({ model })} />
          <Field label="每次最多花费（美元）">
            <Input type="number" inputMode="decimal" min={0.5} step={0.5} value={Number.isFinite(draft.budget) ? draft.budget : ""}
              onChange={(e) => onChange({ budget: e.target.value === "" ? NaN : Number(e.target.value) })} />
          </Field>
          <Field label="记忆">
            <Select className={selectCls} value={draft.memory} onChange={(e) => onChange({ memory: e.target.value })}>
              <option value="notes">笔记（推荐）</option>
              <option value="fusion-memory">共享记忆库 fusion-memory</option>
              <option value="off">不记</option>
            </Select>
          </Field>
        </div>
        <Field label="工作目录">
          <Input value={draft.workDir} onChange={(e) => onChange({ workDir: e.target.value })}
            placeholder={`~/.fusion/fusion-console/agents/${draft.name.trim() || autoName}`} autoCapitalize="off" spellCheck={false} />
        </Field>
      </Advanced>
    </div>
  );
}

function ModelField({ provider, value, onChange }: {
  provider: string; value: string; onChange: (model: string) => void;
}) {
  const [models, setModels] = useState<string[]>([]);
  const [manual, setManual] = useState(false);
  useEffect(() => {
    if (!provider) return;
    let active = true;
    api.models(provider).then((r) => {
      if (active) setModels([...new Set((r.models ?? []).filter(Boolean))]);
    }).catch(() => { /* Manual entry remains available. */ });
    return () => { active = false; };
  }, [provider]);

  const defaultLabel = provider ? "服务的默认模型" : "工具的默认模型";
  const custom = manual || (!!value && !models.includes(value));
  return (
    <div className="space-y-2">
      {models.length > 0 && (
        <Field label="模型">
          <Select className={selectCls} value={custom ? "custom" : value ? "model:" + value : ""}
            onChange={(e) => {
              const choice = e.target.value;
              setManual(choice === "custom");
              if (choice !== "custom") onChange(choice ? choice.slice("model:".length) : "");
            }}>
            <option value="">{defaultLabel}</option>
            {models.map((model) => <option key={model} value={"model:" + model}>{model}</option>)}
            <option value="custom">手动输入</option>
          </Select>
        </Field>
      )}
      {(models.length === 0 || custom) && (
        <Field label={models.length ? "自定义模型" : "模型"}>
          <Input value={value} onFocus={() => setManual(true)} onChange={(e) => onChange(e.target.value)}
            placeholder={defaultLabel} autoCapitalize="off" spellCheck={false} />
        </Field>
      )}
    </div>
  );
}
