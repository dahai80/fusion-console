import { useCallback, useEffect, useState } from "react";
import { Cloud } from "lucide-react";
import { api } from "./api";
import { Badge, Button, Input, Select } from "./components/ui";

// Cloud backends live in the gateway (one place, one token ledger). This
// panel adds them right where it is rendered — the console relays them into
// the gateway config via its admin API. Used by the wizard's Gateway models
// step and by the Gateway page.
export const BACKEND_TYPES = [
  ["openai-compatible", "OpenAI-compatible (any /v1 endpoint)"],
  ["anthropic", "Anthropic (Claude)"],
  ["deepseek", "DeepSeek"],
  ["zhipu", "Zhipu (GLM)"],
  ["moonshot", "Moonshot (Kimi)"],
  ["dashscope", "DashScope (Qwen)"],
  ["minimax", "MiniMax"],
  ["openrouter", "OpenRouter"],
] as const;

export function GatewayBackendsPanel({ compact = false }: { compact?: boolean }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [backends, setBackends] = useState<Awaited<ReturnType<typeof api.gatewayBackends>>["backends"]>([]);
  const [bType, setBType] = useState<string>("openai-compatible");
  const [bName, setBName] = useState("");
  const [bURL, setBURL] = useState("");
  const [bKey, setBKey] = useState("");
  const [bModels, setBModels] = useState("");

  const load = useCallback(() => {
    api.gatewayBackends().then((r) => setBackends(r.backends ?? [])).catch(() => setBackends([]));
  }, []);
  useEffect(() => { load(); }, [load]);

  const deleteBackend = async (name: string) => {
    setErr(""); setMsg("");
    try {
      await api.deleteGatewayBackend(name);
      setMsg(`backend "${name}" removed from the gateway`);
      load();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const addBackend = async () => {
    setBusy(true); setErr(""); setMsg("");
    try {
      await api.addGatewayBackend({
        name: bName.trim() || bType.replace(/[^a-z0-9-]/gi, ""),
        type: bType,
        base_url: bURL.trim(),
        api_key: bKey.trim(),
        models: bModels.split(",").map((s) => s.trim()).filter(Boolean),
      });
      setMsg(`backend "${bName.trim() || bType}" added to the gateway`);
      setBName(""); setBURL(""); setBKey(""); setBModels("");
      load();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-3">
      {!compact && (
        <div className="flex items-center gap-2">
          <Cloud size={16} className="text-indigo-500" />
          <p className="text-sm font-medium">Cloud providers in the gateway</p>
        </div>
      )}
      <p className="text-xs text-zinc-500">
        Add a cloud provider so models route through the local gateway (one token ledger).
        Repeat for several; keys are stored in the gateway config, never echoed back.
      </p>
      <div className="flex flex-wrap gap-2">
        <Select className="w-64" value={bType} onChange={(e) => setBType(e.target.value)}>
          {BACKEND_TYPES.map(([v, label]) => <option key={v} value={v}>{label}</option>)}
        </Select>
        <Input className="w-40" placeholder="name (optional)" value={bName}
          onChange={(e) => setBName(e.target.value)} />
      </div>
      <div className="flex flex-wrap gap-2">
        <Input className="w-72" placeholder="base URL, e.g. https://api.deepseek.com/v1" value={bURL}
          onChange={(e) => setBURL(e.target.value)} />
        <Input className="w-56" type="password" placeholder="API key" value={bKey}
          onChange={(e) => setBKey(e.target.value)} />
      </div>
      <Input placeholder="models, comma separated (e.g. deepseek-chat, deepseek-reasoner)" value={bModels}
        onChange={(e) => setBModels(e.target.value)} />
      <Button onClick={addBackend} disabled={busy || (!bURL && !bKey)}>Add provider</Button>
      {backends.length > 0 && (
        <div className="space-y-1">
          <p className="text-xs font-medium text-zinc-500">Backends currently in the gateway</p>
          {backends.map((b) => (
            <div key={b.name} className="flex flex-wrap items-center gap-2 text-xs">
              <span className="font-medium">{b.name}</span>
              <Badge tone={b.enabled ? "ok" : "warn"}>{b.type}</Badge>
              <span className="text-zinc-400">{b.base_url}</span>
              {b.models?.length > 0 && <span className="text-zinc-400">models: {b.models.join(", ")}</span>}
              <button onClick={() => deleteBackend(b.name)} title={`Remove backend ${b.name}`}
                className="ml-auto rounded-lg px-2 py-0.5 text-zinc-400 hover:bg-zinc-100 hover:text-red-500 dark:hover:bg-zinc-800">
                remove
              </button>
            </div>
          ))}
        </div>
      )}
      {msg && <p className="text-sm text-indigo-500">{msg}</p>}
      {err && <p className="text-sm text-red-500">{err}</p>}
    </div>
  );
}
