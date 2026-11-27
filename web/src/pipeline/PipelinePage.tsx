import { useEffect, useMemo, useState } from "react";
import {
    CheckCircle2, CircleDashed, Clock, Eye, FileText, FlaskConical, GitBranch,
    Hourglass, Loader2, ListChecks, PauseCircle, Play, Rocket, UserRound, XCircle,
} from "lucide-react";
import { Card } from "../components/ui";
import {
    loadPipelineConfig, type DocState, type PipelineConfig, type PipelineTask, type PipelineTaskState,
    type SetupItem,
} from "./config";

const stateMeta: Record<PipelineTaskState, { label: string; icon: typeof Clock; cls: string }> = {
    running: { label: "运行中", icon: Loader2, cls: "text-indigo-500" },
    gated: { label: "闸门等待", icon: Hourglass, cls: "text-amber-500" },
    approval: { label: "审批", icon: PauseCircle, cls: "text-amber-500" },
    done: { label: "完成", icon: CheckCircle2, cls: "text-emerald-500" },
    rejected: { label: "已筛掉", icon: XCircle, cls: "text-zinc-400" },
};

const docMeta: Record<DocState, { icon: typeof Clock; cls: string; label: string }> = {
    pending: { icon: CircleDashed, cls: "text-zinc-400", label: "未产出" },
    review: { icon: Eye, cls: "text-indigo-500", label: "审中" },
    pass: { icon: CheckCircle2, cls: "text-emerald-500", label: "pass" },
    reject: { icon: XCircle, cls: "text-red-500", label: "打回" },
};

export default function PipelinePage() {
    const [config, setConfig] = useState<PipelineConfig | null>(null);
    const [source, setSource] = useState<"file" | "demo">("demo");
    const [mode, setMode] = useState<"prepare" | "run">("prepare");
    const [setupId, setSetupId] = useState<string | null>(null);
    const [selectedId, setSelectedId] = useState<string | null>(null);
    const [stageId, setStageId] = useState<string | null>(null);
    const [quoteInput, setQuoteInput] = useState("");
    const [notice, setNotice] = useState<string | null>(null);

    useEffect(() => {
        void loadPipelineConfig().then(({ config: cfg, source: src }) => {
            setConfig(cfg);
            setSource(src);
            setSelectedId(cfg.tasks.find((t) => t.state === "approval")?.id ?? cfg.tasks[0]?.id ?? null);
        });
    }, []);

    const stageMap = useMemo(() => new Map((config?.stages ?? []).map((s) => [s.id, s])), [config]);
    const openGates = useMemo(
        () => (config?.tasks ?? []).filter((t) => t.state === "gated" || t.state === "approval").length,
        [config],
    );
    const selected = config?.tasks.find((t) => t.id === selectedId) ?? null;
    const selectedStage = config?.stages.find((s) => s.id === stageId) ?? null;
    const selectedSetup = config?.setup.find((p) => p.id === setupId) ?? null;
    const setupDone = (config?.setup ?? []).filter((p) => p.status === "done").length;
    if (!config) {
        return <div className="flex h-full items-center justify-center text-sm text-zinc-500"><Loader2 className="mr-2 h-4 w-4 animate-spin" />加载 pipeline 配置…</div>;
    }

    const act = (task: PipelineTask, action: "approve" | "reject" | "reprice") => {
        setNotice(`[原型] ${task.id} ${action} — 后端接线前为本地 mock`);
        if (action === "approve") {
            task.state = "done";
            if (task.docs) task.docs["quote-ai-XXXX.md"] = "pass";
        } else if (action === "reject") {
            task.state = "gated";
            task.detail = "报价被打回，回退上一阶段";
        } else if (action === "reprice" && quoteInput) {
            if (task.quote) task.quote.amount = quoteInput;
        }
        setConfig({ ...config, tasks: [...config.tasks] });
    };

    const openTask = (id: string) => {
        setSelectedId(id);
        setStageId(null);
    };

    const openSetup = (id: string) => {
        setSetupId(id);
        setStageId(null);
        setSelectedId(null);
    };

    const toggleSetup = (item: SetupItem) => {
        if (!config) return;
        item.status = item.status === "done" ? "pending" : "done";
        setNotice(`[原型] ${item.id} ${item.name} 标记为 ${item.status === "done" ? "已就绪" : "未就绪"} — 后端接线前为本地 mock`);
        setConfig({ ...config, setup: [...config.setup] });
    };

    return (
        <div className="mx-auto flex h-full max-w-[1600px] flex-col gap-4 p-4">
            <div className="flex flex-wrap items-center gap-3">
                <h1 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">Pipeline · {config.name}</h1>
                <span className="rounded-full border border-zinc-200/80 bg-white px-2 py-0.5 text-xs text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
                    原型 · 配置驱动 · {source === "file" ? "pipeline.config.json" : "内置 demo 配置"}
                </span>
                <div className="flex overflow-hidden rounded-xl border border-zinc-200/80 text-xs dark:border-zinc-800">
                    <button type="button" onClick={() => { setMode("prepare"); setStageId(null); }}
                        className={`flex items-center gap-1.5 px-3 py-1.5 transition ${mode === "prepare" ? "bg-indigo-600 text-white" : "bg-white text-zinc-600 hover:bg-zinc-100 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:bg-zinc-800"}`}>
                        <ListChecks size={13} />准备工作
                        <span className={mode === "prepare" ? "text-indigo-200" : "text-zinc-400"}>{setupDone}/{config.setup.length}</span>
                    </button>
                    <button type="button" onClick={() => { setMode("run"); setSetupId(null); }}
                        className={`flex items-center gap-1.5 border-l border-zinc-200/80 px-3 py-1.5 transition dark:border-zinc-800 ${mode === "run" ? "bg-indigo-600 text-white" : "bg-white text-zinc-600 hover:bg-zinc-100 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:bg-zinc-800"}`}>
                        <Rocket size={13} />运行中
                    </button>
                </div>
                {mode === "prepare" && setupDone < config.setup.length && (
                    <span className="rounded-xl border border-amber-300 bg-amber-50 px-2.5 py-1 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-300">
                        准备未完成：需求源 / 脚手架 / 团队 / 外发通道就绪后，需求才会流入流水线
                    </span>
                )}
                <span className="ml-auto text-xs text-zinc-500">闸门 open: <b className="text-amber-500">{openGates}</b></span>
            </div>
            {notice && (
                <div className="rounded-xl border border-indigo-200/60 bg-indigo-50 px-3 py-2 text-xs text-indigo-600 dark:border-indigo-900 dark:bg-indigo-950/40 dark:text-indigo-300">{notice}</div>
            )}
            <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-[240px_1fr_360px]">
                {/* column 1: setup flow (prepare) / stage machine (run) */}
                <Card className="min-h-0 overflow-y-auto p-3">
                    {mode === "prepare" ? (
                        <>
                            <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-400">准备流程 · 先于一切需求</div>
                            <ol className="space-y-1.5">
                                {config.setup.map((p, i) => {
                                    const Done = p.status === "done" ? CheckCircle2 : CircleDashed;
                                    return (
                                        <li key={p.id}>
                                            <button type="button" onClick={() => openSetup(p.id)}
                                                className={`w-full rounded-xl px-2.5 py-2 text-left text-sm transition ${setupId === p.id
                                                    ? "bg-indigo-100 dark:bg-indigo-900/50"
                                                    : "hover:bg-zinc-100 dark:hover:bg-zinc-800/60"}`}>
                                                <div className="flex items-center gap-2">
                                                    <span className="w-7 text-xs font-mono text-zinc-400">{p.id}</span>
                                                    <span className="flex-1 text-zinc-800 dark:text-zinc-200">{p.name}</span>
                                                    <Done size={14} className={p.status === "done" ? "text-emerald-500" : "text-zinc-300 dark:text-zinc-600"} />
                                                </div>
                                            </button>
                                            {i < config.setup.length - 1 && <div className="ml-3.5 h-1 w-px bg-zinc-200 dark:bg-zinc-800" />}
                                        </li>
                                    );
                                })}
                            </ol>
                            <div className="mt-3 rounded-xl border border-zinc-200/80 px-2.5 py-2 text-xs leading-relaxed text-zinc-500 dark:border-zinc-800">
                                全部就绪后切到「运行中」：需求才从已配置源流入 S0 筛选。
                            </div>
                        </>
                    ) : (
                        <>
                            <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-400">阶段流水线</div>
                    <ol className="space-y-1.5">
                        {config.stages.map((s, i) => (
                            <li key={s.id}>
                                <button type="button" onClick={() => setStageId(s.id)}
                                    className={`w-full rounded-xl px-2.5 py-2 text-left text-sm transition ${stageId === s.id
                                        ? "bg-indigo-100 dark:bg-indigo-900/50"
                                        : selected?.stageId === s.id ? "bg-indigo-50 dark:bg-indigo-950/40" : "hover:bg-zinc-100 dark:hover:bg-zinc-800/60"}`}>
                                    <div className="flex items-center gap-2">
                                        <span className="w-7 text-xs font-mono text-zinc-400">{s.id}</span>
                                        <span className="flex-1 text-zinc-800 dark:text-zinc-200">{s.name}</span>
                                        <span className="inline-flex items-center gap-1 rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"><UserRound size={11} />{s.role}</span>
                                    </div>
                                </button>
                                {s.gate && (
                                    <div className="ml-9 mt-0.5 flex items-center gap-1.5 text-xs text-zinc-400">
                                        <GitBranch size={11} className={s.gate.human ? "text-amber-500" : "text-zinc-400"} />
                                        闸门 {s.gate.artifact}{s.gate.human ? " · 人工确认" : ""}
                                    </div>
                                )}
                                {i < config.stages.length - 1 && <div className="ml-3.5 h-1 w-px bg-zinc-200 dark:bg-zinc-800" />}
                            </li>
                        ))}
                    </ol>
                        </>
                    )}
                </Card>

                {/* column 2: task list (run) / lead source preview (prepare) */}
                <Card className="min-h-0 overflow-y-auto p-3">
                    {mode === "prepare" ? (
                        <>
                            <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-400">需求入口 · 尚未开通</div>
                            <div className="rounded-xl border border-dashed border-zinc-300 px-3 py-6 text-center text-xs leading-relaxed text-zinc-500 dark:border-zinc-700">
                                <ListChecks size={20} className="mx-auto mb-2 text-zinc-300 dark:text-zinc-600" />
                                需求不会凭空出现：先在左侧逐项完成准备（源配置 → 脚手架 → 团队绑定 → 外发通道），<br />这里才会出现统一需求队列（带 source 标记）。
                            </div>
                            <div className="mt-3 space-y-2">
                                {config.setup.map((p) => (
                                    <button key={p.id} type="button" onClick={() => openSetup(p.id)}
                                        className={`w-full rounded-xl border px-3 py-2.5 text-left transition ${setupId === p.id
                                            ? "border-indigo-300 bg-indigo-50/60 dark:border-indigo-800 dark:bg-indigo-950/30"
                                            : "border-zinc-200/80 bg-white hover:border-zinc-300 dark:border-zinc-800 dark:bg-zinc-900 dark:hover:border-zinc-700"}`}>
                                        <div className="flex items-center gap-2">
                                            {p.status === "done"
                                                ? <CheckCircle2 size={15} className="text-emerald-500" />
                                                : <CircleDashed size={15} className="text-zinc-300 dark:text-zinc-600" />}
                                            <span className="font-mono text-xs text-zinc-400">{p.id}</span>
                                            <span className="flex-1 text-sm font-medium text-zinc-900 dark:text-zinc-100">{p.name}</span>
                                            <span className={`rounded-lg px-1.5 py-0.5 text-xs ${p.status === "done" ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300" : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"}`}>{p.status === "done" ? "就绪" : "未配置"}</span>
                                        </div>
                                        {p.desc && <div className="mt-1 line-clamp-2 pl-[23px] text-xs text-zinc-500">{p.desc}</div>}
                                    </button>
                                ))}
                            </div>
                        </>
                    ) : (
                        <>
                            <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-400">任务列表</div>
                    <ul className="space-y-2">
                        {config.tasks.map((t) => {
                            const meta = stateMeta[t.state];
                            const Icon = meta.icon;
                            const st = stageMap.get(t.stageId);
                            return (
                                <li key={t.id}>
                                    <button type="button" onClick={() => openTask(t.id)}
                                        className={`w-full rounded-xl border px-3 py-2.5 text-left transition ${t.id === selectedId
                                            ? "border-indigo-300 bg-indigo-50/60 dark:border-indigo-800 dark:bg-indigo-950/30"
                                            : "border-zinc-200/80 bg-white hover:border-zinc-300 dark:border-zinc-800 dark:bg-zinc-900 dark:hover:border-zinc-700"}`}>
                                        <div className="flex items-center gap-2">
                                            <Icon size={15} className={`${meta.cls} ${t.state === "running" ? "animate-spin" : ""}`} />
                                            <span className="font-mono text-xs text-zinc-400">#{t.id}</span>
                                            <span className="flex-1 truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">{t.title}</span>
                                            <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">[{st?.id ?? t.stageId}]</span>
                                        </div>
                                        <div className="mt-1 flex items-center gap-2 pl-[23px] text-xs text-zinc-500">
                                            <span>{meta.label}</span>
                                            <span>· {t.worker}</span>
                                            {t.elapsed && <span>· {t.elapsed}</span>}
                                            {t.state === "approval" && <span className="ml-auto rounded-lg bg-amber-100 px-1.5 py-0.5 font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">⏸ APPROVAL</span>}
                                        </div>
                                        {t.detail && <div className="mt-0.5 pl-[23px] text-xs text-zinc-400">{t.detail}</div>}
                                    </button>
                                </li>
                            );
                        })}
                    </ul>
                        </>
                    )}
                </Card>

                {/* column 3: requirement detail */}
                <Card className="min-h-0 overflow-y-auto p-3">
                    <div className="mb-2 flex items-center justify-between">
                        <span className="text-xs font-medium uppercase tracking-wide text-zinc-400">{selectedSetup ? `准备详情 · ${selectedSetup.id}` : selectedStage ? `阶段详情 · ${selectedStage.id}` : mode === "prepare" ? "准备详情" : "需求详情"}</span>
                        {selectedSetup && (
                            <button type="button" onClick={() => setSetupId(null)} className="text-xs text-indigo-500 hover:text-indigo-400">返回准备总览</button>
                        )}
                        {selectedStage && (
                            <button type="button" onClick={() => setStageId(null)} className="text-xs text-indigo-500 hover:text-indigo-400">返回任务详情</button>
                        )}
                    </div>
                    {selectedSetup ? (
                        <div className="space-y-3">
                            <div className="flex items-center gap-2">
                                <div className="flex-1 text-sm font-semibold text-zinc-900 dark:text-zinc-100">{selectedSetup.id} · {selectedSetup.name}</div>
                                <span className={`rounded-lg px-1.5 py-0.5 text-xs ${selectedSetup.status === "done" ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300" : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"}`}>{selectedSetup.status === "done" ? "就绪" : "未配置"}</span>
                            </div>
                            {selectedSetup.desc && <p className="text-xs leading-relaxed text-zinc-600 dark:text-zinc-300">{selectedSetup.desc}</p>}
                            {selectedSetup.how && selectedSetup.how.length > 0 && (
                                <div>
                                    <div className="mb-1 text-xs font-medium text-zinc-500">怎么做</div>
                                    <ol className="space-y-1">
                                        {selectedSetup.how.map((step, i) => (
                                            <li key={i} className="flex gap-2 rounded-lg px-2 py-1 text-xs leading-relaxed text-zinc-600 hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800/60">
                                                <span className="font-mono text-zinc-400">{i + 1}.</span>
                                                <span>{step}</span>
                                            </li>
                                        ))}
                                    </ol>
                                </div>
                            )}
                            {selectedSetup.refs && selectedSetup.refs.length > 0 && (
                                <div>
                                    <div className="mb-1 text-xs font-medium text-zinc-500">关联物</div>
                                    <div className="flex flex-wrap gap-1.5">
                                        {selectedSetup.refs.map((ref) => (
                                            <span key={ref} className="inline-flex items-center gap-1 rounded-lg bg-zinc-100 px-1.5 py-0.5 font-mono text-xs text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"><FileText size={11} className="text-zinc-400" />{ref}</span>
                                        ))}
                                    </div>
                                </div>
                            )}
                            <button type="button" onClick={() => toggleSetup(selectedSetup)}
                                className="w-full rounded-xl bg-indigo-600 px-3 py-2 text-xs font-medium text-white hover:bg-indigo-500">
                                {selectedSetup.status === "done" ? "撤销 · 标记为未就绪" : "标记为已就绪"}
                            </button>
                        </div>
                    ) : mode === "prepare" ? (
                        <div className="text-sm text-zinc-500">从左侧选择准备项，查看怎么做</div>
                    ) : selectedStage ? (
                        <div className="space-y-3">
                            <div>
                                <div className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{selectedStage.id} · {selectedStage.name}</div>
                                <div className="mt-0.5 inline-flex items-center gap-1 rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"><UserRound size={11} />{selectedStage.role}</div>
                            </div>
                            {selectedStage.desc && <p className="text-xs leading-relaxed text-zinc-600 dark:text-zinc-300">{selectedStage.desc}</p>}
                            {selectedStage.entry && (
                                <div className="rounded-xl border border-zinc-200/80 px-3 py-2 dark:border-zinc-800">
                                    <div className="text-xs font-medium text-zinc-500">进入条件</div>
                                    <div className="mt-0.5 text-xs text-zinc-600 dark:text-zinc-300">{selectedStage.entry}</div>
                                </div>
                            )}
                            {selectedStage.exit && (
                                <div className="rounded-xl border border-zinc-200/80 px-3 py-2 dark:border-zinc-800">
                                    <div className="text-xs font-medium text-zinc-500">退出条件</div>
                                    <div className="mt-0.5 text-xs text-zinc-600 dark:text-zinc-300">{selectedStage.exit}</div>
                                </div>
                            )}
                            <div>
                                <div className="mb-1 text-xs font-medium text-zinc-500">文档契约</div>
                                <div className="space-y-1">
                                    {selectedStage.docs.map((doc) => (
                                        <div key={doc} className="flex items-center gap-2 rounded-lg px-2 py-1 text-sm hover:bg-zinc-100 dark:hover:bg-zinc-800/60">
                                            <FileText size={13} className="text-zinc-400" />
                                            <span className="flex-1 truncate font-mono text-xs text-zinc-700 dark:text-zinc-300">{doc}</span>
                                        </div>
                                    ))}
                                </div>
                            </div>
                            {selectedStage.gate && (
                                <div className={`rounded-xl border px-3 py-2 ${selectedStage.gate.human ? "border-amber-300 bg-amber-50 dark:border-amber-800 dark:bg-amber-950/30" : "border-zinc-200/80 dark:border-zinc-800"}`}>
                                    <div className="flex items-center gap-2 text-sm font-medium text-zinc-900 dark:text-zinc-100">
                                        <GitBranch size={13} className={selectedStage.gate.human ? "text-amber-500" : "text-indigo-500"} />
                                        闸门 {selectedStage.gate.artifact}
                                    </div>
                                    <div className="mt-0.5 text-xs text-zinc-500">
                                        verdict={selectedStage.gate.verdict} 才放行下一阶段{selectedStage.gate.human ? " · 人工确认（fail-closed）" : " · valid review"}
                                    </div>
                                </div>
                            )}
                            <div>
                                <div className="mb-1 text-xs font-medium text-zinc-500">当前处于此阶段的任务</div>
                                {config.tasks.filter((t) => t.stageId === selectedStage.id).length === 0 ? (
                                    <div className="text-xs text-zinc-400">无</div>
                                ) : (
                                    <div className="space-y-1">
                                        {config.tasks.filter((t) => t.stageId === selectedStage.id).map((t) => {
                                            const meta = stateMeta[t.state];
                                            return (
                                                <button key={t.id} type="button" onClick={() => openTask(t.id)}
                                                    className="flex w-full items-center gap-2 rounded-lg border border-zinc-200/80 px-2 py-1.5 text-left text-sm hover:border-zinc-300 dark:border-zinc-800 dark:hover:border-zinc-700">
                                                    <meta.icon size={13} className={meta.cls} />
                                                    <span className="font-mono text-xs text-zinc-400">#{t.id}</span>
                                                    <span className="flex-1 truncate text-xs text-zinc-800 dark:text-zinc-200">{t.title}</span>
                                                    <span className="text-xs text-zinc-400">{meta.label}</span>
                                                </button>
                                            );
                                        })}
                                    </div>
                                )}
                            </div>
                        </div>
                    ) : !selected ? (
                        <div className="text-sm text-zinc-500">选择一个任务</div>
                    ) : (
                        <div className="space-y-3">
                            <div>
                                <div className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{selected.id} · {selected.title}</div>
                                <div className="mt-0.5 text-xs text-zinc-500">stage: {stageMap.get(selected.stageId)?.name ?? selected.stageId} · worker: {selected.worker}</div>
                            </div>
                            <div className="space-y-1">
                                {Object.entries(selected.docs).map(([doc, st]) => {
                                    const dm = docMeta[st];
                                    const DIcon = dm.icon;
                                    return (
                                        <div key={doc} className="flex items-center gap-2 rounded-lg px-2 py-1 text-sm hover:bg-zinc-100 dark:hover:bg-zinc-800/60">
                                            <FileText size={13} className="text-zinc-400" />
                                            <span className="flex-1 truncate font-mono text-xs text-zinc-700 dark:text-zinc-300">{doc}</span>
                                            <DIcon size={13} className={dm.cls} />
                                            <span className={`text-xs ${dm.cls}`}>{dm.label}</span>
                                        </div>
                                    );
                                })}
                            </div>
                            {selected.quote && (
                                <div className={`rounded-xl border p-3 ${selected.state === "approval" ? "border-amber-300 bg-amber-50 dark:border-amber-800 dark:bg-amber-950/30" : "border-zinc-200/80 dark:border-zinc-800"}`}>
                                    <div className="flex items-center gap-2 text-sm font-medium text-zinc-900 dark:text-zinc-100">
                                        <FlaskConical size={14} className="text-amber-500" />报价 {selected.quote.amount}
                                        {selected.quote.margin && <span className="text-xs text-zinc-500">毛利 {selected.quote.margin}</span>}
                                    </div>
                                    <div className="mt-1 text-xs text-zinc-500">fail-closed：批准后才经对应源回程外发</div>
                                    {selected.state === "approval" && (
                                        <div className="mt-2 flex items-center gap-2">
                                            <input value={quoteInput} onChange={(e) => setQuoteInput(e.target.value)} placeholder="改价"
                                                className="w-24 rounded-lg border border-zinc-200/80 bg-white px-2 py-1 text-xs dark:border-zinc-700 dark:bg-zinc-900" />
                                            <button type="button" onClick={() => act(selected, "approve")}
                                                className="rounded-lg bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-indigo-500">批准报价</button>
                                            <button type="button" onClick={() => act(selected, "reprice")}
                                                className="rounded-lg border border-zinc-200/80 px-2.5 py-1 text-xs text-zinc-600 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-800">改价</button>
                                            <button type="button" onClick={() => act(selected, "reject")}
                                                className="rounded-lg border border-zinc-200/80 px-2.5 py-1 text-xs text-zinc-600 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-800">打回</button>
                                        </div>
                                    )}
                                </div>
                            )}
                            <div className="rounded-xl border border-zinc-200/80 px-3 py-2 text-xs text-zinc-500 dark:border-zinc-800">
                                <Play size={11} className="mr-1 inline text-indigo-500" />原型说明：数据来自 pipeline 配置；审批动作在 mock 层改本地状态，后端接线走 approvals + task 闸门。
                            </div>
                        </div>
                    )}
                </Card>
            </div>
        </div>
    );
}
