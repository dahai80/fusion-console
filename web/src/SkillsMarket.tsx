import { useEffect, useMemo, useState } from "react";
import { BookOpen, Check, Download, ExternalLink, Loader2, RefreshCw, Store } from "lucide-react";
import { api, ApiError, type MarketEntry, type MarketRegistry } from "./api";

// Skills Market tab: browse the fusion-skills catalog (plugin / connector /
// skill / app). Skills install in one click: the server downloads the pinned
// artifact (sha256 + size checked) or the in-repo source_path at a pinned
// commit, and unpacks it into ~/.claude/skills. Plugins, connectors and apps
// keep the link to their Releases page (host-specific install targets).

const KINDS = [
    { id: "all", label: "All" },
    { id: "plugin", label: "Plugin" },
    { id: "connector", label: "Connector" },
    { id: "skill", label: "Skill" },
    { id: "app", label: "App" },
];

const KIND_BADGE: Record<string, string> = {
    plugin: "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400",
    connector: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
    skill: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
    app: "bg-zinc-500/10 text-zinc-500",
};

export function SkillsMarket({ onInstalled }: { onInstalled?: () => void }) {
    const [reg, setReg] = useState<MarketRegistry | null>(null);
    const [err, setErr] = useState("");
    const [loading, setLoading] = useState(true);
    const [refreshing, setRefreshing] = useState(false);
    const [kind, setKind] = useState("all");
    const [query, setQuery] = useState("");

    const load = (force: boolean) => {
        if (force) setRefreshing(true);
        api.marketRegistry()
            .then((r) => { setReg(r); setErr(""); })
            .catch((ex) => setErr((ex as Error).message))
            .finally(() => { setLoading(false); setRefreshing(false); });
    };
    useEffect(() => { load(false); }, []);

    const entries = useMemo(() => {
        if (!reg) return [];
        const q = query.trim().toLowerCase();
        return reg.entries.filter((e) =>
            (kind === "all" || e.kind === kind) &&
            (!q || `${e.name} ${e.id} ${e.description?.zh ?? ""} ${e.description?.en ?? ""}`.toLowerCase().includes(q)));
    }, [reg, kind, query]);

    if (loading) return <p className="p-4 text-sm text-zinc-500">loading market…</p>;
    return (
        <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2">
                <div className="relative flex-1 min-w-52">
                    <input
                        className="w-full rounded-lg border border-zinc-200/80 bg-white px-3 py-2 text-sm outline-none focus:border-indigo-500 dark:border-zinc-800 dark:bg-zinc-900"
                        placeholder="Search name or description…"
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                    />
                </div>
                <div className="flex gap-1">
                    {KINDS.map((k) => (
                        <button key={k.id} type="button" onClick={() => setKind(k.id)}
                            className={`rounded-lg border px-3 py-1.5 text-xs transition-colors ${kind === k.id
                                ? "border-indigo-500 bg-indigo-500/10 text-indigo-600 dark:text-indigo-400"
                                : "border-zinc-200/80 text-zinc-500 hover:border-zinc-400 dark:border-zinc-800"}`}>
                            {k.label}
                        </button>
                    ))}
                </div>
                <button type="button" onClick={() => load(true)} disabled={refreshing}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-zinc-200/80 px-3 py-1.5 text-xs text-zinc-500 hover:border-zinc-400 disabled:opacity-50 dark:border-zinc-800">
                    <RefreshCw size={13} className={refreshing ? "animate-spin" : ""} /> Refresh
                </button>
                {reg && (
                    <span className="ml-auto text-[11px] text-zinc-400">
                        registry v{reg.version} · {reg.entries.length} entries ·{" "}
                        {reg.cached
                            ? `cache ${new Date(reg.fetched_at).toLocaleTimeString()}`
                            : "fresh"}
                    </span>
                )}
            </div>

            {err && <p className="rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-500">{err}</p>}
            {reg?.stale && !err && (
                <p className="rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-600 dark:text-amber-400">
                    Serving a cached copy (upstream unreachable). Hit Refresh to retry.
                </p>
            )}

            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {entries.map((e) => <MarketCard key={e.id} e={e} onInstalled={() => { load(false); onInstalled?.(); }} />)}
            </div>
            {!loading && entries.length === 0 && !err && (
                <p className="text-sm text-zinc-500">No matching entries.</p>
            )}
        </div>
    );
}

function MarketCard({ e, onInstalled }: { e: MarketEntry; onInstalled: () => void }) {
    const [busy, setBusy] = useState(false);
    const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);
    const inst = e.installed;
    const fromMarket = inst && inst.anchor !== "local";

    const install = async (overwrite: boolean) => {
        setBusy(true); setNote(null);
        try {
            const r = await api.marketInstall(e.id, overwrite);
            const pin = r.installed.anchor === "sha256" ? "sha256 verified" : `commit ${r.installed.digest?.slice(0, 7)}`;
            setNote({ ok: true, text: `已安装到 ${r.path}（${pin}）` + (r.warnings.length ? ` · 注意：${r.warnings.join("；")}` : "") });
            onInstalled();
        } catch (ex) {
            const err = ex as ApiError;
            if (err.status === 409 && !overwrite && !inst?.link &&
                window.confirm(`~/.claude/skills 里已有同名技能「${e.name}」。用市场版本覆盖吗？原目录会被整体替换。`)) {
                setBusy(false);
                return install(true);
            }
            setNote({ ok: false, text: err.message });
        } finally {
            setBusy(false);
        }
    };

    return (
        <div className="flex flex-col gap-2 rounded-2xl border border-zinc-200/80 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
            <div className="flex items-center gap-2">
                <Store size={14} className="shrink-0 text-zinc-400" />
                <span className="min-w-0 truncate text-sm font-medium">{e.name}</span>
                <span className={`shrink-0 rounded-md px-2 py-0.5 text-[11px] ${KIND_BADGE[e.kind] ?? KIND_BADGE.app}`}>{e.kind}</span>
                {e.category && (
                    <span className="shrink-0 rounded-md bg-zinc-500/10 px-2 py-0.5 text-[11px] text-zinc-500">{e.category}</span>
                )}
            </div>
            <p className="flex-1 text-xs text-zinc-600 dark:text-zinc-400">
                {e.description?.zh}
                {e.description?.en && <span className="mt-0.5 block text-zinc-400">{e.description.en}</span>}
            </p>
            {note && (
                <p className={`break-all rounded-lg px-2 py-1 text-[11px] ${note.ok ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : "bg-red-500/10 text-red-500"}`}>{note.text}</p>
            )}
            <div className="flex items-center gap-2 text-[11px] text-zinc-400">
                <span className="min-w-0 truncate">⬢ {e.source}{e.version ? ` · ${e.version}` : ""}</span>
                {e.doc_url && (
                    <a href={e.doc_url} target="_blank" rel="noreferrer" className="inline-flex shrink-0 items-center gap-1 text-indigo-500 hover:underline">
                        <BookOpen size={11} /> doc
                    </a>
                )}
                {inst && (
                    <span className="ml-auto inline-flex shrink-0 items-center gap-1 text-emerald-600 dark:text-emerald-400"
                        title={inst.link ? "symlinked skill — the market never replaces it" : fromMarket ? `${inst.anchor} ${inst.digest ?? ""}` : "a local skill with the same name"}>
                        <Check size={11} /> {inst.link ? "已链接" : fromMarket ? "已安装" : "本地同名"}
                    </span>
                )}
                {e.installable ? (
                    <button type="button" onClick={() => install(false)} disabled={busy || inst?.link}
                        title={inst?.link ? "symlinked skill — manage it at its source" : undefined}
                        className={`${inst ? "" : "ml-auto "}inline-flex shrink-0 items-center gap-1 rounded-lg bg-indigo-600 px-3 py-1.5 text-[11px] font-medium text-white hover:bg-indigo-500 disabled:opacity-50`}>
                        {busy ? <Loader2 size={11} className="animate-spin" /> : <Download size={11} />}
                        {busy ? "安装中…" : inst ? "重新安装" : "安装"}
                    </button>
                ) : e.releases_url && (
                    <a href={e.releases_url} target="_blank" rel="noreferrer"
                        className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-lg bg-indigo-600 px-3 py-1.5 text-[11px] font-medium text-white hover:bg-indigo-500">
                        <ExternalLink size={11} /> Releases 安装
                    </a>
                )}
            </div>
        </div>
    );
}
