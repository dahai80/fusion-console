import { useCallback, useEffect, useRef, useState } from "react";
import { Database, Download, ExternalLink, Power, RefreshCw } from "lucide-react";
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

      {st && !st.installed && !installing && (
        <div className="space-y-3 rounded-2xl border border-zinc-200/80 p-4 text-sm dark:border-zinc-800">
          <p className="text-zinc-600 dark:text-zinc-300">
            fusion-rag is the local offline vector knowledge base (Apple Silicon native).
            Once installed, every chat gains <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">kb_search</code> /{" "}
            <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">kb_ask</code> tools over MCP, and this menu
            opens the knowledge base management console.
          </p>
          <p className="text-xs text-zinc-400">
            Not downloaded by default. Install fetches the latest GitHub release, builds a local venv and pip-installs
            it (aliyun mirror). Requires python3 on this machine.
          </p>
          <Button onClick={() => action(() => api.ragInstall(), "install")} disabled={busy !== ""}>
            <Download size={14} /> Download &amp; install
          </Button>
        </div>
      )}

      {installing && (
        <div className="space-y-2 rounded-2xl border border-zinc-200/80 p-4 dark:border-zinc-800">
          <p className="flex items-center gap-2 text-sm text-zinc-600 dark:text-zinc-300">
            <RefreshCw size={14} className="animate-spin" /> installing fusion-rag — phase: {st?.phase}
          </p>
          <pre ref={logRef} className="max-h-64 overflow-auto rounded-xl bg-zinc-950 p-3 text-xs leading-5 text-zinc-300">
            {(st?.logs ?? []).join("\n")}
          </pre>
          {st?.error && <p className="text-sm text-red-500">{st.error}</p>}
        </div>
      )}

      {st?.phase === "error" && !installing && (
        <div className="space-y-2 rounded-2xl border border-red-500/30 bg-red-500/5 p-4">
          <p className="text-sm text-red-500">install failed: {st.error}</p>
          <Button variant="outline" onClick={() => action(() => api.ragInstall(), "retry")} disabled={busy !== ""}>
            <Download size={14} /> Retry install
          </Button>
        </div>
      )}

      {st?.installed && !installing && (
        <div className="space-y-3 rounded-2xl border border-zinc-200/80 p-4 dark:border-zinc-800">
          <div className="flex flex-wrap items-center gap-2">
            {st.running ? (
              <>
                <Button variant="outline" onClick={() => action(() => api.ragStop(), "stop")} disabled={busy !== ""}>
                  <Power size={14} /> Stop
                </Button>
                <a
                  href="/shell/fusion-rag/"
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-2 rounded-xl bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
                >
                  <ExternalLink size={14} /> Open management console
                </a>
                <label className="ml-2 flex items-center gap-2 text-xs text-zinc-500" title="Expose kb_search / kb_ask MCP tools in chat">
                  <input
                    type="checkbox"
                    checked={st.enabled}
                    onChange={(e) => action(() => api.ragSetEnabled(e.target.checked), "enabled")}
                  />
                  enable in chat
                </label>
              </>
            ) : (
              <Button onClick={() => action(() => api.ragStart(), "start")} disabled={busy !== ""}>
                <Power size={14} /> Start fusion-rag
              </Button>
            )}
          </div>
          {st.running && (
            <iframe
              src="/shell/fusion-rag/"
              title="fusion-rag console"
              className="h-[70vh] w-full rounded-xl border border-zinc-200/80 dark:border-zinc-800"
            />
          )}
        </div>
      )}
    </div>
  );
}
