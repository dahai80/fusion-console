import { useCallback, useEffect, useRef, useState } from "react";
import { Database, Download, ExternalLink, Power } from "lucide-react";
import { api, type RagStatus } from "./api";
import { Button } from "./components/ui";

// Knowledge Base page for the optional fusion-rag integration. Three
// lifecycle states driven by /api/rag/status:
//   - not installed: intro + one-click install (live install log while the
//     background job downloads, extracts and pip-installs the release)
//   - installed, stopped: start button (install success enables it by default)
//   - running: the RAG console UI, proxied same-origin at /shell/fusion-rag/
// When running, chat conversations automatically gain the kb_search / kb_ask
// MCP tools.
export function RagPage() {
  const [st, setSt] = useState<RagStatus | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const logRef = useRef<HTMLPreElement>(null);

  const refresh = useCallback(async () => {
    try {
      const s = await api.ragStatus();
      setSt(s);
    } catch (ex) {
      setErr((ex as Error).message);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Poll while an install job is active so the log streams.
  useEffect(() => {
    if (!st || (st.phase !== "downloading" && st.phase !== "extracting" && st.phase !== "installing")) return;
    const t = setInterval(refresh, 1500);
    return () => clearInterval(t);
  }, [st, refresh]);

  // Keep running-state honest with a slower poll while visible.
  useEffect(() => {
    if (!st?.installed || st.phase === "downloading" || st.phase === "extracting" || st.phase === "installing") return;
    const t = setInterval(refresh, 10000);
    return () => clearInterval(t);
  }, [st, refresh]);

  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [st?.logs?.length]);

  const action = async (fn: () => Promise<unknown>, label: string) => {
    setBusy(label); setErr("");
    try {
      await fn();
      await refresh();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy("");
    }
  };

  const installing = st?.phase === "downloading" || st?.phase === "extracting" || st?.phase === "installing";

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div className="flex items-center gap-2">
        <Database size={20} className="text-indigo-500" />
        <h1 className="text-lg font-semibold">Knowledge Base</h1>
        {st?.installed && (
          <span className={`rounded-lg px-2 py-0.5 text-xs ${
            st.running
              ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
              : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800"
          }`}>
            {st.running ? `running on :${st.port}` : "installed · stopped"}
          </span>
        )}
      </div>

      {err && <p className="rounded-xl bg-red-500/10 px-3 py-2 text-sm text-red-500">{err}</p>}

      {!st && <p className="text-sm text-zinc-400">loading…</p>}

      {/* Lifecycle status mirrors the Team page's agent-studio card: one row,
          status text on the left, action on the right — always visible. */}
      {st && (
        <div className={`rounded-xl border p-3 text-xs ${
          st.installed && st.running
            ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400"
            : "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400"
        }`}>
          <div className="flex flex-wrap items-center gap-2">
            <p className="min-w-0 flex-1 font-medium">
              {!st.installed && "Knowledge Base 需要 fusion-rag — 本机未安装，一键安装（PyPI）后每个对话自动获得 kb_search / kb_ask 工具"}
              {st.installed && st.running && `fusion-rag 运行中 :${st.port}`}
              {st.installed && !st.running && "fusion-rag 已安装，服务未运行 — 启动后对话才能使用 kb_search / kb_ask"}
            </p>
            {!st.installed && !installing && st.phase !== "error" && (
              <Button onClick={() => action(() => api.ragInstall(), "install")} disabled={busy !== ""}>
                <Download size={14} /> 一键安装（PyPI）
              </Button>
            )}
            {st.installed && st.running && (
              <div className="flex shrink-0 flex-wrap gap-1.5">
                <a
                  href="/shell/fusion-rag/"
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-2 rounded-xl bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
                >
                  <ExternalLink size={14} /> 打开管理控制台
                </a>
                <label className="flex items-center gap-1.5 text-xs opacity-80" title="Expose kb_search / kb_ask MCP tools in chat">
                  <input
                    type="checkbox"
                    checked={st.enabled}
                    onChange={(e) => action(() => api.ragSetEnabled(e.target.checked), "enabled")}
                  />
                  对话中启用
                </label>
                <Button variant="outline" onClick={() => action(() => api.ragStop(), "stop")} disabled={busy !== ""}>
                  <Power size={14} /> 停止
                </Button>
              </div>
            )}
            {st.installed && !st.running && !installing && (
              <Button onClick={() => action(() => api.ragStart(), "start")} disabled={busy !== ""} className="shrink-0">
                <Power size={14} /> 启动 fusion-rag
              </Button>
            )}
          </div>
          {st.phase === "error" && !installing && (
            <div className="mt-2 flex items-center gap-2">
              <p className="font-mono text-[10px] opacity-80">install failed: {st.error}</p>
              <Button variant="outline" onClick={() => action(() => api.ragInstall(), "retry")} disabled={busy !== ""}>
                <Download size={14} /> 重试安装
              </Button>
            </div>
          )}
          {installing && (
            <pre ref={logRef} className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-xl bg-zinc-950 p-3 font-mono text-[10px] leading-5 text-zinc-300">
              {(st.logs ?? []).join("\n")}
            </pre>
          )}
        </div>
      )}

      {st?.installed && st.running && (
        <iframe
          src="/shell/fusion-rag/"
          title="fusion-rag console"
          className="h-[70vh] w-full rounded-xl border border-zinc-200/80 dark:border-zinc-800"
        />
      )}
    </div>
  );
}
