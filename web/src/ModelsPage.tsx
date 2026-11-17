import { useCallback, useEffect, useRef, useState } from "react";
import { Box, Check, Cpu, Download, Play, PlugZap, RefreshCw, Square } from "lucide-react";
import { api, type ChatDefault, type ModelCatalogItem, type Provider } from "./api";
import { Button, Input, Select } from "./components/ui";

// Models page (design v2 §4/§Phase-0): the single registration surface for
// model sources. Unified logic — every source is {type, base_url, key,
// model pool}; presets differ only in prefills and lifecycle management.
// First-run starter panel: two preset cards + custom, per plan Phase 0.

type Preset = "macfabric" | "fusion-gateway" | "custom";

const PRESETS: { id: Preset; label: string; hint: string; type: Provider["type"]; baseURL: string; lifecycle: string; darwinOnly?: boolean }[] = [
  {
    id: "macfabric", label: "MacFabric", hint: "local Apple-Silicon model pool (:8999), OpenAI + Anthropic dual protocol",
    type: "openai_compat", baseURL: "http://127.0.0.1:8999/v1", lifecycle: "macfabric", darwinOnly: true,
  },
  {
    id: "fusion-gateway", label: "fusion-gateway", hint: "multi-backend gateway (:11432); the console installs and starts it",
    type: "gateway", baseURL: "http://127.0.0.1:11432/v1", lifecycle: "fusion-gateway",
  },
  {
    id: "custom", label: "Custom", hint: "any OpenAI- or Anthropic-compatible endpoint",
    type: "openai_compat", baseURL: "", lifecycle: "",
  },
];

const darwin = navigator.platform.toLowerCase().includes("mac") || navigator.userAgent.includes("Macintosh");

type Draft = {
  id: string;
  name: string;
  type: Provider["type"];
  base_url: string;
  models: string[];
  model: string;
  lifecycle: string;
  enabled: boolean;
};

function draftFromPreset(p: Preset): Draft {
  const t = PRESETS.find((x) => x.id === p)!;
  const name = t.id === "custom" ? "" : t.label.toLowerCase();
  return {
    id: t.id === "custom" ? "" : name,
    name: t.id === "custom" ? "" : t.label,
    type: t.type,
    base_url: t.baseURL,
    models: [],
    model: "",
    lifecycle: t.lifecycle,
    enabled: true,
  };
}

export function ModelsPage({ onSaved }: { onSaved?: () => void }) {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [chatDefault, setChatDefault] = useState<ChatDefault>({});
  const [catalog, setCatalog] = useState<ModelCatalogItem[]>([]);
  const [editing, setEditing] = useState<Draft | null>(null);
  const [editMode, setEditMode] = useState<"add" | "edit">("add");
  const [keyInput, setKeyInput] = useState("");
  const [keyStored, setKeyStored] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [job, setJob] = useState<{ phase: string; logs: string[]; error: string } | null>(null);
  const logRef = useRef<HTMLPreElement>(null);

  const refresh = useCallback(async () => {
    try {
      const [pr, cat] = await Promise.all([api.providers(), api.modelCatalog()]);
      setProviders(pr.providers ?? []);
      setChatDefault((pr as { chat_default?: ChatDefault }).chat_default ?? {});
      setCatalog(cat.items ?? []);
    } catch (ex) {
      setErr((ex as Error).message);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);
  // Poll the install job while it runs (macfabric stage-1 installer).
  useEffect(() => {
    if (job?.phase !== "downloading" && job?.phase !== "extracting" && job?.phase !== "installing") return;
    const t = setInterval(async () => {
      try { setJob(await api.sourceInstallJob()); } catch { /* keep last */ }
    }, 1500);
    return () => clearInterval(t);
  }, [job?.phase]);
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [job?.logs?.length]);

  const enabled = providers.filter((p) => p.enabled);
  const detectedMacfabric = catalog.some((i) => i.lifecycle === "macfabric" && i.reachable);
  const detectedGateway = catalog.some((i) => i.kind === "gateway" && i.reachable);

  const startAdd = (p: Preset) => {
    setErr(""); setMsg(""); setKeyInput(""); setKeyStored(false);
    setEditMode("add");
    setEditing(draftFromPreset(p));
  };
  const startEdit = (p: Provider) => {
    setErr(""); setMsg(""); setKeyInput("");
    setEditMode("edit");
    setEditing({
      id: p.id, name: p.name, type: p.type, base_url: p.base_url,
      models: p.models ?? [], model: p.model, lifecycle: (p.lifecycle ?? "") as Draft["lifecycle"], enabled: p.enabled,
    });
    // keychain status is inferred: non-empty api_key_id means a reference exists
    setKeyStored(!!p.api_key_id);
  };

  const save = async () => {
    if (!editing) return;
    setErr(""); setMsg("");
    const p: Provider = {
      id: editing.id, name: editing.name || editing.id, type: editing.type,
      base_url: editing.base_url, model: editing.model || editing.models[0] || "",
      models: editing.models, api_key_id: editing.id,
      enabled: editing.enabled, lifecycle: (editing.lifecycle || undefined) as Provider["lifecycle"],
    };
    try {
      const r = await api.saveProvider(p);
      if (keyInput.trim()) {
        await api.storeApiKey(p.id, keyInput.trim());
      }
      if (editMode === "add" && enabled.length === 0) {
        // First source: suggest (never silently set) as the default chat model.
        const firstModel = p.models?.[0] || p.model;
        if (firstModel) {
          await api.setChatDefault(p.id, firstModel);
          setMsg(`saved — set as default chat model (${p.id} / ${firstModel})`);
        } else {
          setMsg("saved — pick a default model in the pool editor");
        }
      } else {
        setMsg("saved" + ((r as { note?: string }).note ? ` — ${(r as { note?: string }).note}` : ""));
      }
      setEditing(null);
      await refresh();
      onSaved?.();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const testCall = async () => {
    if (!editing?.id) return;
    setBusy("test"); setErr("");
    try {
      const r = await api.testCall(editing.id);
      if (r.ok) setMsg(`test call ok in ${r.latency_ms ?? "?"} ms`);
      else setErr(r.error || "test call failed");
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy("");
    }
  };

  const fetchModels = async () => {
    if (!editing?.id) { setErr("save the source first, then fetch its live model list"); return; }
    setBusy("models"); setErr("");
    try {
      const r = await api.models(editing.id);
      const models = r.models ?? [];
      setEditing({ ...editing, models, model: editing.model || models[0] || "" });
      setMsg(`fetched ${models.length} models from the endpoint`);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy("");
    }
  };

  const action = async (p: Provider, act: "start" | "stop" | "install") => {
    setBusy(p.id + act); setErr("");
    try {
      if (act === "install" && p.lifecycle === "macfabric") {
        await api.sourceAction(p.id, "install");
        setJob({ phase: "downloading", logs: [], error: "" });
        setMsg("macfabric installer started");
      } else {
        const r = await api.sourceAction(p.id, act);
        setMsg(`${act}: ${r.note || "ok"}`);
      }
      await refresh();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy("");
    }
  };

  const del = async (p: Provider) => {
    if (!window.confirm(`Delete source "${p.name || p.id}"? Its stored API key is removed too.`)) return;
    setErr("");
    try {
      await api.deleteProvider(p.id);
      setMsg("deleted");
      await refresh();
    } catch (ex) {
      const m = (ex as Error).message;
      if (m.includes("referenced") && window.confirm(m + "\n\nDelete anyway (references keep pointing at nothing)?")) {
        try {
          await fetch(`/api/settings/providers/${encodeURIComponent(p.id)}?force=1`, {
            method: "DELETE", credentials: "same-origin",
          });
          setMsg("deleted (forced)");
          await refresh();
        } catch (ex2) { setErr((ex2 as Error).message); }
      } else {
        setErr(m);
      }
    }
  };

  const setDefault = async (p: Provider) => {
    const model = p.models?.[0] || p.model;
    if (!model) { setErr(`source ${p.id} has no model in its pool`); return; }
    try {
      await api.setChatDefault(p.id, model);
      setMsg(`default chat model: ${p.id} / ${model}`);
      await refresh();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const lifecycleBadge = (p: Provider) => {
    const items = catalog.filter((i) => i.provider_id === p.id);
    const reachable = items.length > 0 && items.every((i) => i.reachable);
    if (p.lifecycle === "fusion-gateway") {
      return reachable
        ? <span className="rounded-lg bg-emerald-500/10 px-1.5 py-0.5 text-xs text-emerald-600 dark:text-emerald-400">running</span>
        : <span className="rounded-lg bg-amber-500/10 px-1.5 py-0.5 text-xs text-amber-600 dark:text-amber-400">installed, not running</span>;
    }
    if (p.lifecycle === "macfabric") {
      return reachable
        ? <span className="rounded-lg bg-emerald-500/10 px-1.5 py-0.5 text-xs text-emerald-600 dark:text-emerald-400">running</span>
        : <span className="rounded-lg bg-amber-500/10 px-1.5 py-0.5 text-xs text-amber-600 dark:text-amber-400">not running</span>;
    }
    return <span className="rounded-lg bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-500 dark:bg-zinc-800">external</span>;
  };

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div className="flex flex-wrap items-center gap-2">
        <Cpu size={20} className="text-indigo-500" />
        <h1 className="text-lg font-semibold">Models</h1>
        {enabled.length === 0 && (
          <span className="rounded-lg bg-amber-500/10 px-2 py-0.5 text-xs text-amber-600 dark:text-amber-400">
            no model source yet — add one to start chatting
          </span>
        )}
        {chatDefault.default_provider_id && (
          <span className="ml-auto text-xs text-zinc-500">
            default chat: {chatDefault.default_provider_id} / {chatDefault.default_model}
          </span>
        )}
      </div>

      {err && <p className="rounded-xl bg-red-500/10 px-3 py-2 text-sm text-red-500">{err}</p>}
      {msg && <p className="rounded-xl bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600 dark:text-emerald-400">{msg}</p>}

      {/* Starter panel: first-run empty state (plan Phase 0 #1) */}
      {enabled.length === 0 && !editing && (
        <div className="space-y-3 rounded-2xl border border-dashed border-indigo-300 p-4 dark:border-indigo-500/40">
          <p className="text-sm text-zinc-600 dark:text-zinc-300">Add your first model source — presets pre-fill everything but the key.</p>
          <div className="grid gap-3 sm:grid-cols-3">
            {PRESETS.filter((p) => !p.darwinOnly || darwin).map((p) => (
              <button key={p.id} onClick={() => startAdd(p.id)}
                className="rounded-xl border border-zinc-200/80 p-3 text-left text-sm hover:border-indigo-400 dark:border-zinc-800">
                <p className="font-medium flex items-center gap-1.5">
                  <Box size={14} className="text-indigo-500" /> {p.label}
                  {p.id === "macfabric" && <span className="text-xs text-zinc-400">macOS only</span>}
                </p>
                <p className="mt-1 text-xs text-zinc-500">{p.hint}</p>
                {p.id === "macfabric" && detectedMacfabric && (
                  <p className="mt-1 text-xs text-emerald-600">detected running on :8999</p>
                )}
                {p.id === "fusion-gateway" && detectedGateway && (
                  <p className="mt-1 text-xs text-emerald-600">detected running on :11432</p>
                )}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Source list */}
      {providers.length > 0 && (
        <div className="space-y-2">
          {providers.map((p) => (
            <div key={p.id} className="rounded-xl border border-zinc-200/80 p-3 text-sm dark:border-zinc-800">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{p.name || p.id}</span>
                {!p.enabled && <span className="text-xs text-zinc-400">disabled</span>}
                {lifecycleBadge(p)}
                <span className="text-xs text-zinc-500">{p.type} · {p.base_url}</span>
                <span className="text-xs text-zinc-400">{(p.models?.length || (p.model ? 1 : 0))} model(s)</span>
                {chatDefault.default_provider_id === p.id && (
                  <span className="rounded-lg bg-indigo-500/10 px-1.5 py-0.5 text-xs text-indigo-500">default chat</span>
                )}
                <span className="ml-auto flex gap-1.5">
                  {p.lifecycle === "fusion-gateway" && (
                    catalog.find((i) => i.provider_id === p.id)?.reachable
                      ? <Button variant="outline" onClick={() => action(p, "stop")} disabled={busy !== ""}><Square size={12} /> Stop</Button>
                      : <Button variant="outline" onClick={() => action(p, "start")} disabled={busy !== ""}><Play size={12} /> Start</Button>
                  )}
                  {p.lifecycle === "macfabric" && (
                    catalog.find((i) => i.provider_id === p.id)?.reachable
                      ? <Button variant="outline" onClick={() => action(p, "stop")} disabled={busy !== ""}><Square size={12} /> Stop</Button>
                      : <Button variant="outline" onClick={() => action(p, "start")} disabled={busy !== ""}><Play size={12} /> Start</Button>
                  )}
                  {chatDefault.default_provider_id !== p.id &&
                    <Button variant="outline" onClick={() => setDefault(p)} disabled={busy !== ""}>Set default</Button>}
                  <Button variant="outline" onClick={() => startEdit(p)}>Edit</Button>
                  <Button variant="outline" onClick={() => del(p)} disabled={busy !== ""}>Delete</Button>
                </span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Add / edit form (design v2 §4.1A: stepped, type-aware, testcall-gated) */}
      {editing && (
        <div className="space-y-3 rounded-2xl border border-zinc-200/80 p-4 dark:border-zinc-800">
          <p className="text-sm font-medium">{editMode === "add" ? "Add model source" : `Edit ${editing.name || editing.id}`}</p>
          {editMode === "add" && (
            <p className="text-xs text-zinc-500">
              {editing.type === "anthropic_compat"
                ? "Anthropic-compatible base URL must NOT end with /v1 — the protocol appends the path itself."
                : "OpenAI-compatible base URLs usually end with /v1 (the test call verifies)."}
            </p>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="text-xs text-zinc-500">id (lowercase, unique)
              <Input className="mt-1 w-full" placeholder="my-source" value={editing.id} disabled={editMode === "edit"}
                onChange={(e) => setEditing({ ...editing, id: e.target.value.trim().toLowerCase() })} />
            </label>
            <label className="text-xs text-zinc-500">display name
              <Input className="mt-1 w-full" placeholder="My gateway" value={editing.name}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })} />
            </label>
            <label className="text-xs text-zinc-500">type
              <Select className="mt-1 w-full" value={editing.type}
                onChange={(e) => setEditing({ ...editing, type: e.target.value as Provider["type"] })}>
                <option value="openai_compat">OpenAI-compatible (most gateways)</option>
                <option value="anthropic_compat">Anthropic-compatible</option>
                <option value="gateway">fusion-gateway</option>
              </Select>
            </label>
            <label className="text-xs text-zinc-500">base URL
              <Input className="mt-1 w-full" placeholder={editing.type === "anthropic_compat" ? "https://api.anthropic.com" : "https://api.example.com/v1"}
                value={editing.base_url}
                onChange={(e) => setEditing({ ...editing, base_url: e.target.value.trim() })} />
            </label>
          </div>
          <label className="block text-xs text-zinc-500">
            API key {keyStored ? "(stored in the OS keychain — enter only to replace)" : "(stored in the OS keychain, never shown again)"}
            <Input className="mt-1 w-full" type="password" placeholder={keyStored ? "••••••••" : "sk-..."} value={keyInput}
              onChange={(e) => setKeyInput(e.target.value)} />
          </label>
          <div className="flex flex-wrap items-end gap-2">
            <label className="min-w-48 flex-1 text-xs text-zinc-500">
              model pool (default = first)
              <Input className="mt-1 w-full" placeholder="model-a, model-b, ..."
                value={editing.models.join(", ")}
                onChange={(e) => {
                  const models = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
                  setEditing({ ...editing, models, model: editing.model && models.includes(editing.model) ? editing.model : models[0] || "" });
                }} />
            </label>
            <Button variant="outline" onClick={fetchModels} disabled={busy !== "" || !editing.id}>
              <RefreshCw size={13} className={busy === "models" ? "animate-spin" : ""} /> Fetch from endpoint
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={save} disabled={busy !== "" || !editing.id || !editing.base_url}><Check size={14} /> Save</Button>
            <Button variant="outline" onClick={testCall} disabled={busy !== "" || !editing.id}>
              <PlugZap size={13} /> {busy === "test" ? "testing…" : "Test call"}
            </Button>
            <Button variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
          </div>
        </div>
      )}

      {/* macfabric stage-1 installer log (design v2 §6.1) */}
      {job && (
        <div className="space-y-2 rounded-2xl border border-zinc-200/80 p-4 dark:border-zinc-800">
          <p className="flex items-center gap-2 text-sm text-zinc-600 dark:text-zinc-300">
            <Download size={14} className={job.phase === "installing" || job.phase === "downloading" ? "animate-pulse" : ""} />
            macfabric installer — phase: {job.phase || "idle"}
          </p>
          <pre ref={logRef} className="max-h-64 overflow-auto rounded-xl bg-zinc-950 p-3 text-xs leading-5 text-zinc-300">
            {(job.logs ?? []).join("\n")}
          </pre>
          {job.error && <p className="text-sm text-red-500">{job.error}</p>}
        </div>
      )}
    </div>
  );
}
