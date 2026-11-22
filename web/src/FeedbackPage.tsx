import { useEffect, useState } from "react";
import { Bug, Lightbulb, HelpCircle, Brain, Send, Save, CheckCircle2, Loader2 } from "lucide-react";
import { api, type FeedbackDraft } from "./api";

// FeedbackPage: file GitHub issues from the console. One form, three entry
// points (manual / error toast / brain suggestion card — prefill arrives via
// the fc:feedback-prefill window event). Submission is always an explicit
// user action; drafts live in localStorage until filed.

const COMPONENTS = [
    { id: "console", label: "fusion-console（本控制台）" },
    { id: "memory", label: "fusion-memory（记忆 hub）" },
    { id: "rag", label: "fusion-rag（知识库）" },
    { id: "gateway", label: "fusion-gateway（网关）" },
    { id: "code", label: "fusion-code（编码 agent）" },
];
const REPO_LABELS: Record<string, string> = {
    console: "dahai80/fusion-console",
    memory: "dahai80/fusion-memory",
    rag: "dahai80/fusion-rag",
    gateway: "dahai80/fusion-gateway",
    code: "dahai80/fusion-code",
};

type FbKind = "bug" | "feature" | "question";

const KINDS: { id: FbKind; label: string; desc: string; icon: typeof Bug }[] = [
    { id: "bug", label: "问题（Bug）", desc: "功能坏了、报错、行为不符合预期", icon: Bug },
    { id: "feature", label: "需求（Feature）", desc: "新功能、改进建议", icon: Lightbulb },
    { id: "question", label: "使用咨询", desc: "怎么用、为什么这样设计", icon: HelpCircle },
];

const EMPTY: FeedbackDraft = { title: "", steps: "", expect: "", actual: "", extra: "", component: "console" };
const HISTORY_KEY = "fc-feedback-history";

type HistoryRow = { title: string; repo: string; url?: string; when: number; state: "submitted" | "draft" };

function loadHistory(): HistoryRow[] {
    try {
        return JSON.parse(localStorage.getItem(HISTORY_KEY) ?? "[]") as HistoryRow[];
    } catch {
        return [];
    }
}

export default function FeedbackPage() {
    const [kind, setKind] = useState<FbKind>("bug");
    const [rough, setRough] = useState("");
    const [draft, setDraft] = useState<FeedbackDraft>(EMPTY);
    const [env, setEnv] = useState<Record<string, string>>({});
    const [filling, setFilling] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [fillErr, setFillErr] = useState("");
    const [submitMsg, setSubmitMsg] = useState("");
    const [history, setHistory] = useState<HistoryRow[]>(loadHistory);

    useEffect(() => {
        api.feedbackEnv().then(setEnv).catch(() => {});
    }, []);

    // Prefill from the error toast / brain suggestion card paths.
    useEffect(() => {
        const onPrefill = (e: Event) => {
            const d = (e as CustomEvent).detail as Partial<FeedbackDraft> & { rough?: string };
            if (!d) return;
            if (d.rough) setRough(d.rough);
            const next = { ...EMPTY, ...d };
            delete (next as { rough?: string }).rough;
            if (next.component && COMPONENTS.some((c) => c.id === next.component)) setDraft(next);
            else if (d.rough) setDraft((cur) => ({ ...cur, title: d.title ?? cur.title }));
            if (d.title) setDraft((cur) => ({ ...cur, title: d.title! }));
        };
        window.addEventListener("fc:feedback-prefill", onPrefill);
        return () => window.removeEventListener("fc:feedback-prefill", onPrefill);
    }, []);

    const envLine = [
        env.revision ? `fusion-console ${env.revision}` : "fusion-console",
        env.os,
        env.brain ? `brain ${env.brain}` : "",
    ].filter(Boolean).join(" · ");

    const setField = (k: keyof FeedbackDraft, v: string) => setDraft((cur) => ({ ...cur, [k]: v }));

    const brainFill = async () => {
        if (!rough.trim()) return;
        setFilling(true);
        setFillErr("");
        try {
            const d = await api.feedbackDraft(rough.trim(), draft.component);
            setDraft({ ...d, component: COMPONENTS.some((c) => c.id === d.component) ? d.component : "console" });
        } catch (ex) {
            setFillErr((ex as Error).message);
        } finally {
            setFilling(false);
        }
    };

    const submit = async () => {
        if (!draft.title.trim() || submitting) return;
        setSubmitting(true);
        setSubmitMsg("");
        try {
            const body = issueBody(kind, draft, envLine);
            const r = await api.feedbackSubmit(draft.component, draft.title.trim(), body);
            pushHistory({ title: draft.title.trim(), repo: r.repo, url: r.url, when: Date.now(), state: "submitted" });
            setSubmitMsg(`已提交：${r.url}`);
            setDraft(EMPTY);
            setRough("");
        } catch (ex) {
            setSubmitMsg((ex as Error).message);
        } finally {
            setSubmitting(false);
        }
    };

    const saveDraft = () => {
        if (!draft.title.trim()) return;
        pushHistory({ title: draft.title.trim(), repo: REPO_LABELS[draft.component] ?? "", when: Date.now(), state: "draft" });
        setSubmitMsg("草稿已保存");
    };

    const pushHistory = (row: HistoryRow) => {
        const next = [row, ...loadHistory()].slice(0, 50);
        localStorage.setItem(HISTORY_KEY, JSON.stringify(next));
        setHistory(next);
    };

    const field = "w-full rounded-lg border border-zinc-300/80 bg-white px-3 py-2 text-sm outline-none focus:border-indigo-500 dark:border-zinc-700 dark:bg-zinc-900";

    return (
        <div className="mx-auto max-w-3xl space-y-4 overflow-y-auto p-4">
            <div className="flex items-center justify-between">
                <div>
                    <h2 className="text-lg font-semibold">Feedback</h2>
                    <p className="text-xs text-zinc-500">提交问题或需求到 GitHub Issues — 不会用 GitHub 也可以让大脑代填</p>
                </div>
                <span className="rounded-full bg-emerald-500/10 px-3 py-1 text-xs text-emerald-600 dark:text-emerald-400">
                    {REPO_LABELS[draft.component]}
                </span>
            </div>

            {/* ① kind + component */}
            <section className="rounded-2xl border border-zinc-200/80 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
                <h3 className="mb-3 text-xs font-semibold text-zinc-500">① 反馈类型与组件</h3>
                <div className="flex flex-wrap gap-2">
                    {KINDS.map((k) => (
                        <button key={k.id} type="button" onClick={() => setKind(k.id)}
                            className={`flex-1 rounded-xl border p-3 text-left transition-colors ${kind === k.id
                                ? "border-indigo-500 bg-indigo-500/10"
                                : "border-zinc-200/80 hover:border-zinc-400 dark:border-zinc-800 dark:hover:border-zinc-600"}`}>
                            <div className="flex items-center gap-2 text-sm font-medium"><k.icon size={15} /> {k.label}</div>
                            <div className="mt-1 text-xs text-zinc-500">{k.desc}</div>
                        </button>
                    ))}
                </div>
                <label className="mt-3 block text-xs text-zinc-500">相关组件（提交到对应仓库，大脑填写时自动识别）</label>
                <select className={`${field} mt-1 max-w-sm`} value={draft.component}
                    onChange={(e) => setField("component", e.target.value)}>
                    {COMPONENTS.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}
                </select>
            </section>

            {/* ② rough + brain fill */}
            <section className="rounded-2xl border border-zinc-200/80 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
                <h3 className="mb-3 text-xs font-semibold text-zinc-500">② 描述你的反馈</h3>
                <label className="block text-xs text-zinc-500">随手写（想到什么写什么，不用管格式）</label>
                <textarea className={`${field} mt-1 min-h-20`} value={rough}
                    onChange={(e) => setRough(e.target.value)}
                    placeholder="例：派发任务跑了两个小时网络断了，重连后一直转圈" />
                <div className="mt-2 flex items-center gap-3">
                    <button type="button" onClick={brainFill} disabled={filling || !rough.trim()}
                        className="inline-flex items-center gap-2 rounded-xl bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-500 disabled:opacity-45">
                        {filling ? <Loader2 size={15} className="animate-spin" /> : <Brain size={15} />}
                        {filling ? "大脑正在整理…" : "让大脑帮我填写"}
                    </button>
                    <span className="text-xs text-zinc-500">按 GitHub 模板整理成下面的结构化字段，填完你再检查、修改</span>
                </div>
                {fillErr && <p className="mt-2 text-xs text-red-500">{fillErr}</p>}

                <div className="mt-4 grid gap-3">
                    <div>
                        <label className="block text-xs text-zinc-500">标题 <span className="text-red-500">*</span></label>
                        <input className={`${field} mt-1`} value={draft.title} onChange={(e) => setField("title", e.target.value)}
                            placeholder="一句话说清问题或需求" />
                    </div>
                    <div className="grid gap-3 sm:grid-cols-2">
                        <div>
                            <label className="block text-xs text-zinc-500">复现步骤 <span className="text-red-500">*</span></label>
                            <textarea className={`${field} mt-1 min-h-20`} value={draft.steps} onChange={(e) => setField("steps", e.target.value)} />
                        </div>
                        <div>
                            <label className="block text-xs text-zinc-500">期望行为 <span className="text-red-500">*</span></label>
                            <textarea className={`${field} mt-1 min-h-20`} value={draft.expect} onChange={(e) => setField("expect", e.target.value)} />
                        </div>
                    </div>
                    <div>
                        <label className="block text-xs text-zinc-500">实际表现 <span className="text-red-500">*</span></label>
                        <textarea className={`${field} mt-1 min-h-20`} value={draft.actual} onChange={(e) => setField("actual", e.target.value)} />
                    </div>
                    <div>
                        <label className="block text-xs text-zinc-500">补充信息（日志 / 截图说明，可选）</label>
                        <textarea className={`${field} mt-1 min-h-16`} value={draft.extra} onChange={(e) => setField("extra", e.target.value)}
                            placeholder="粘贴关键日志或描述" />
                    </div>
                    <div className="rounded-lg border border-dashed border-zinc-300/80 px-3 py-2 text-xs text-zinc-500 dark:border-zinc-700">
                        运行环境（自动收集，随 issue 附上）：{envLine || "…"}
                    </div>
                </div>

                <div className="mt-4 flex items-center gap-3">
                    <button type="button" onClick={submit} disabled={submitting || !draft.title.trim()}
                        className="inline-flex items-center gap-2 rounded-xl bg-emerald-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors hover:bg-emerald-500 disabled:opacity-45">
                        {submitting ? <Loader2 size={15} className="animate-spin" /> : <Send size={15} />}
                        提交到 {REPO_LABELS[draft.component]}
                    </button>
                    <button type="button" onClick={saveDraft} disabled={!draft.title.trim()}
                        className="inline-flex items-center gap-2 rounded-xl border border-zinc-200/80 px-4 py-2 text-sm text-zinc-600 transition-colors hover:border-zinc-400 disabled:opacity-45 dark:border-zinc-800 dark:text-zinc-300">
                        <Save size={15} /> 存草稿
                    </button>
                </div>
                {submitMsg && (
                    <p className="mt-2 flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                        {submitMsg.startsWith("已提交") && <CheckCircle2 size={13} />} {submitMsg}
                    </p>
                )}
            </section>

            {/* ③ history */}
            <section className="rounded-2xl border border-zinc-200/80 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
                <h3 className="mb-2 text-xs font-semibold text-zinc-500">③ 我的反馈</h3>
                {history.length === 0 && <p className="text-xs text-zinc-500">还没有记录</p>}
                {history.map((h, i) => (
                    <div key={i} className="flex items-center gap-2 border-b border-zinc-200/60 py-2 text-sm last:border-0 dark:border-zinc-800/60">
                        <span className={`rounded-lg px-2 py-0.5 text-xs ${h.state === "submitted"
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                            : "bg-amber-500/10 text-amber-600 dark:text-amber-400"}`}>
                            {h.state === "submitted" ? `${h.repo} 已提交` : `${h.repo} 草稿`}
                        </span>
                        <span className="min-w-0 flex-1 truncate text-zinc-600 dark:text-zinc-400">
                            {h.url ? <a href={h.url} target="_blank" rel="noreferrer" className="text-indigo-500 hover:underline">{h.title}</a> : h.title}
                        </span>
                        <span className="shrink-0 text-xs text-zinc-400">{new Date(h.when).toLocaleString()}</span>
                    </div>
                ))}
            </section>
        </div>
    );
}

// issueBody renders the GitHub issue markdown in the user's language with the
// auto-collected environment appended.
function issueBody(kind: FbKind, d: FeedbackDraft, envLine: string): string {
    const label = kind === "bug" ? "Bug" : kind === "feature" ? "Feature request" : "Question";
    const parts = [
        `### Type\n${label}`,
        `### Steps to reproduce\n${d.steps || "n/a"}`,
        `### Expected behavior\n${d.expect || "n/a"}`,
        `### Actual behavior\n${d.actual || "n/a"}`,
    ];
    if (kind === "feature") {
        parts[1] = `### Use case\n${d.steps || "n/a"}`;
        parts[2] = `### Proposal\n${d.expect || "n/a"}`;
        parts[3] = `### Alternatives considered\n${d.actual || "n/a"}`;
    } else if (kind === "question") {
        parts[1] = `### Question\n${d.steps || d.title}`;
        parts[2] = `### Context\n${d.expect || "n/a"}`;
        parts[3] = `### What I tried\n${d.actual || "n/a"}`;
    }
    if (d.extra.trim()) parts.push(`### Additional context\n${d.extra}`);
    if (envLine) parts.push(`### Environment\n${envLine}`);
    parts.push("_Filed from the fusion-console Feedback page._");
    return parts.join("\n\n");
}
