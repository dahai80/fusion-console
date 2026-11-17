import { useCallback, useEffect, useState } from "react";
import { Cpu, PlayCircle, RefreshCw, Trash2 } from "lucide-react";
import { api, type Provider } from "./api";
import { Badge, Button, Card, Select } from "./components/ui";

// ModelsPage is the unified model-source view: every console provider
// (fusion-gateway plus direct OpenAI/Anthropic-compatible entries) with its
// models and default. This page is the single owner of provider.Model —
// chat and the gateway brain card only read it.
export default function ModelsPage() {
    const [providers, setProviders] = useState<Provider[]>([]);
    const [models, setModels] = useState<Record<string, string[]>>({});
    const [draft, setDraft] = useState<Record<string, string>>({});
    const [busy, setBusy] = useState("");
    const [msg, setMsg] = useState("");
    const [warn, setWarn] = useState("");
    const [err, setErr] = useState("");

    const load = useCallback(async () => {
        try {
            const r = await api.providers();
            const list = r.providers ?? [];
            setProviders(list);
            setDraft((cur) => {
                const next = { ...cur };
                for (const p of list) if (next[p.id] === undefined) next[p.id] = p.model || "";
                return next;
            });
            for (const p of list) {
                api.models(p.id).then((m) => {
                    setModels((cur) => ({ ...cur, [p.id]: m.models ?? [] }));
                }).catch(() => {
                    setModels((cur) => ({ ...cur, [p.id]: p.model ? [p.model] : [] }));
                });
            }
        } catch (ex) {
            setErr((ex as Error).message);
        }
    }, []);
    useEffect(() => { load(); }, [load]);

    const saveDefaultModel = async (p: Provider) => {
        const model = draft[p.id] ?? p.model;
        setBusy(p.id); setMsg(""); setWarn(""); setErr("");
        try {
            const r = await api.saveProvider({ ...p, model });
            setWarn(r.warning || "");
            setMsg(`provider ${p.name || p.id} default model set to ${model || "(none)"}`);
            await load();
        } catch (ex) {
            // Inline error: bad URL, or the default model is not served by
            // the provider's /v1/models endpoint.
            setErr((ex as Error).message);
        } finally {
            setBusy("");
        }
    };

    const testCall = async (p: Provider) => {
        setBusy("test:" + p.id); setMsg(""); setWarn(""); setErr("");
        try {
            const r = await api.testCall(p.id);
            if (r.ok) setMsg(`provider ${p.name || p.id} answered in ${r.latency_ms ?? "?"} ms: ${(r.sample ?? "").slice(0, 80)}`);
            else setErr(r.error ?? "test call failed");
        } catch (ex) {
            setErr((ex as Error).message);
        } finally {
            setBusy("");
        }
    };

    const deleteProvider = async (p: Provider) => {
        if (!window.confirm(`Delete provider "${p.id}" from console config?`)) return;
        setMsg(""); setWarn(""); setErr("");
        try {
            await api.deleteProvider(p.id);
            setMsg(`provider "${p.id}" deleted`);
            await load();
        } catch (ex) {
            setErr((ex as Error).message);
        }
    };

    return (
        <div className="mx-auto max-w-3xl space-y-4 p-6">
            <h2 className="text-lg font-semibold">Models</h2>
            <p className="text-sm text-zinc-500 dark:text-zinc-400">
                Unified model sources: every provider the console can talk to. Each provider's
                default model is edited here only — chat picks a provider/model per conversation
                and workers bind to a provider by id.
            </p>
            {providers.length === 0 && (
                <Card className="p-4 text-sm text-zinc-500">
                    No providers yet — run the setup wizard or create a gateway API key on the Gateway page.
                </Card>
            )}
            {providers.map((p) => {
                const opts = models[p.id] ?? [];
                return (
                    <Card key={p.id} className="space-y-3 p-4">
                        <div className="flex flex-wrap items-center gap-2">
                            <Cpu size={16} className="text-indigo-500" />
                            <span className="font-medium">{p.name || p.id}</span>
                            <Badge>{p.type}</Badge>
                            {!p.enabled && <Badge>disabled</Badge>}
                            <span className="min-w-0 truncate text-xs text-zinc-400">{p.base_url}</span>
                            <div className="ml-auto flex gap-1">
                                <Button variant="ghost" onClick={load} title="Reload providers and model lists">
                                    <RefreshCw size={14} />
                                </Button>
                                {p.type !== "gateway" && (
                                    <Button variant="ghost" onClick={() => deleteProvider(p)} title={`Delete provider ${p.id}`}>
                                        <Trash2 size={14} />
                                    </Button>
                                )}
                            </div>
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                            <Select
                                className="w-72"
                                value={draft[p.id] ?? p.model ?? ""}
                                onChange={(e) => setDraft((cur) => ({ ...cur, [p.id]: e.target.value }))}
                                title="Provider default model — verified against /v1/models on save"
                            >
                                {!((draft[p.id] ?? p.model ?? "") === "") && <option value={draft[p.id] ?? p.model}>{draft[p.id] || p.model}</option>}
                                {opts.map((m) => (
                                    <option key={m} value={m}>{m}</option>
                                ))}
                                {opts.length === 0 && (draft[p.id] ?? p.model) === "" && <option value="">no default</option>}
                            </Select>
                            <Button variant="outline" onClick={() => saveDefaultModel(p)} disabled={busy === p.id}>
                                {busy === p.id ? "Saving…" : "Set default"}
                            </Button>
                            <Button variant="ghost" onClick={() => testCall(p)} disabled={busy === "test:" + p.id}>
                                <PlayCircle size={14} /> {busy === "test:" + p.id ? "Calling…" : "Test call"}
                            </Button>
                        </div>
                        {p.models && p.models.length > 0 && (
                            <p className="text-xs text-zinc-400">pool: {p.models.join(", ")}</p>
                        )}
                    </Card>
                );
            })}
            {msg && <p className="text-sm text-indigo-500">{msg}</p>}
            {warn && <p className="text-sm text-amber-600 dark:text-amber-400">{warn}</p>}
            {err && <p className="text-sm text-red-500">{err}</p>}
        </div>
    );
}
