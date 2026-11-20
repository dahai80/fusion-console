import { useCallback, useEffect, useState } from "react";
import { Cloud, ExternalLink } from "lucide-react";
import { api } from "./api";
import { Badge, Button, Input, Select } from "./components/ui";

// Cloud backends live in the gateway (one place, one token ledger). This
// panel adds them right where it is rendered — the console relays them into
// the gateway config via its admin API. Used by the wizard's model step and
// by the Gateway page.
export const BACKEND_TYPES = [
  ["openai-compatible", "OpenAI 兼容（任意 /v1 接口）"],
  ["anthropic", "Anthropic (Claude)"],
  ["deepseek", "DeepSeek"],
  ["zhipu", "智谱 (GLM)"],
  ["moonshot", "Moonshot (Kimi)"],
  ["dashscope", "阿里云百炼 (Qwen)"],
  ["minimax", "MiniMax"],
  ["openrouter", "OpenRouter"],
] as const;

// Starter values for common vendors. The gateway appends /v1/... to the base
// URL itself, so presets stop before /v1. Model ids are only shown as
// placeholders: vendors rename models, so the user types the current ids.
const PRESETS: Record<string, { url: string; models: string; keyPage: string }> = {
  deepseek: { url: "https://api.deepseek.com", models: "deepseek-chat, deepseek-reasoner", keyPage: "https://platform.deepseek.com/api_keys" },
  moonshot: { url: "https://api.moonshot.cn", models: "kimi-k2-turbo-preview", keyPage: "https://platform.moonshot.cn/console/api-keys" },
  dashscope: { url: "https://dashscope.aliyuncs.com/compatible-mode", models: "qwen-plus, qwen-max", keyPage: "https://bailian.console.aliyun.com/?apiKey=1" },
  openrouter: { url: "https://openrouter.ai/api", models: "openai/gpt-4o-mini", keyPage: "https://openrouter.ai/keys" },
  anthropic: { url: "https://api.anthropic.com", models: "claude-sonnet-4-5", keyPage: "https://console.anthropic.com/settings/keys" },
};

// The gateway adds /v1 on every call; a pasted ".../v1" would become /v1/v1.
function normalizeBaseURL(u: string) {
  return u.trim().replace(/\/+$/, "").replace(/\/v1$/, "");
}

export function GatewayBackendsPanel({ compact = false, onAdded }: { compact?: boolean; onAdded?: () => void }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [backends, setBackends] = useState<Awaited<ReturnType<typeof api.gatewayBackends>>["backends"]>([]);
  const [bType, setBType] = useState<string>("deepseek");
  const [bName, setBName] = useState("");
  const [bURL, setBURL] = useState(PRESETS.deepseek.url);
  const [bKey, setBKey] = useState("");
  const [bModels, setBModels] = useState("");
  const preset = PRESETS[bType];

  const load = useCallback(() => {
    api.gatewayBackends().then((r) => setBackends(r.backends ?? [])).catch(() => setBackends([]));
  }, []);
  useEffect(() => { load(); }, [load]);

  const pickType = (t: string) => {
    setBType(t);
    // Swap in the new vendor's starter values unless the user typed their own.
    const prev = PRESETS[bType];
    const next = PRESETS[t];
    if (!bURL || bURL === prev?.url) setBURL(next?.url ?? "");
  };

  const deleteBackend = async (b: { name: string; models?: string[] }) => {
    setErr(""); setMsg("");
    try {
      await api.deleteGatewayBackend(b.name, b.models ?? []);
      setMsg(`已从网关删除「${b.name}」`);
      load();
      onAdded?.();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  };

  const addBackend = async () => {
    setBusy(true); setErr(""); setMsg("");
    const name = bName.trim() || bType.replace(/[^a-z0-9-]/gi, "");
    try {
      const r = await api.addGatewayBackend({
        name,
        type: bType,
        base_url: normalizeBaseURL(bURL),
        api_key: bKey.trim(),
        models: bModels.split(/[,，\s]+/).map((s) => s.trim()).filter(Boolean),
      });
      setMsg(r.note || `「${name}」已加入网关`);
      setBName(""); setBKey(""); setBModels("");
      load();
      onAdded?.();
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
          <p className="text-sm font-medium">网关里的云端模型</p>
        </div>
      )}
      <div className="grid gap-2 sm:grid-cols-2">
        <label className="block text-xs text-zinc-500">厂商
          <Select className="mt-1" value={bType} onChange={(e) => pickType(e.target.value)}>
            {BACKEND_TYPES.map(([v, label]) => <option key={v} value={v}>{label}</option>)}
          </Select>
        </label>
        <label className="block text-xs text-zinc-500">名称（可不填）
          <Input className="mt-1" placeholder={bType} value={bName} onChange={(e) => setBName(e.target.value)} />
        </label>
        <label className="block text-xs text-zinc-500 sm:col-span-2">
          <span className="flex items-center gap-2">API key
            {preset && (
              <a href={preset.keyPage} target="_blank" rel="noreferrer"
                className="inline-flex items-center gap-0.5 text-indigo-600 underline-offset-2 hover:underline dark:text-indigo-400">
                去哪里申请 <ExternalLink size={11} />
              </a>
            )}
          </span>
          <Input className="mt-1" type="password" autoComplete="off" placeholder="sk-..." value={bKey} onChange={(e) => setBKey(e.target.value)} />
        </label>
        <label className="block text-xs text-zinc-500 sm:col-span-2">Base URL（填到 /v1 之前，网关会自己补）
          <Input className="mt-1 font-mono" placeholder="https://api.example.com" value={bURL} onChange={(e) => setBURL(e.target.value)} />
        </label>
        <label className="block text-xs text-zinc-500 sm:col-span-2">模型名（逗号分隔，必须和厂商文档完全一致）
          <Input className="mt-1 font-mono" placeholder={preset ? `例如 ${preset.models}` : "model-a, model-b"} value={bModels} onChange={(e) => setBModels(e.target.value)} />
        </label>
      </div>
      <Button onClick={addBackend} disabled={busy || (!bURL.trim() && !bKey.trim()) || !bModels.trim()}>
        {busy ? "添加中…" : "加入网关"}
      </Button>
      {backends.length > 0 && (
        <div className="space-y-1.5 border-t border-zinc-100 pt-3 dark:border-zinc-800">
          <p className="text-xs font-medium text-zinc-500">网关里已有的后端</p>
          {backends.map((b) => (
            <div key={b.name} className="flex flex-wrap items-center gap-2 text-xs">
              <span className="font-medium">{b.name}</span>
              <Badge tone={b.enabled ? "ok" : "warn"}>{b.type}</Badge>
              <span className="min-w-0 truncate text-zinc-400">{b.base_url}</span>
              {b.models?.length > 0 && <span className="text-zinc-400">{b.models.join(", ")}</span>}
              <button onClick={() => deleteBackend(b)} title={`删除后端 ${b.name}`}
                className="ml-auto rounded-lg px-2 py-0.5 text-zinc-400 hover:bg-zinc-100 hover:text-red-500 dark:hover:bg-zinc-800">
                删除
              </button>
            </div>
          ))}
        </div>
      )}
      {msg && <p className="text-sm text-emerald-600 dark:text-emerald-400">{msg}</p>}
      {err && <p className="break-words text-sm text-red-500">{err}</p>}
    </div>
  );
}
