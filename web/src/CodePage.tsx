import { useCallback, useEffect, useRef, useState } from "react";
import { Code2, Download, ExternalLink, KeyRound, Power, RefreshCw, Terminal } from "lucide-react";
import { api, type CodeStatus, type Provider } from "./api";
import { Button } from "./components/ui";

// Coding Agent page: optional one-click installers for the three headless
// coding agent CLIs — fusion-code, Claude Code and Codex. State comes from
// /api/code/status:
//   - agents[]: per-CLI installed flag (fusion-code = console-installed;
//     claude/codex = found on PATH, however installed)
//   - fusion: install-job log + service state + key binding. fusion-code is
//     the only one with a service to start/stop and a proxied web console;
//     claude/codex are plain CLIs the Agents page dispatches.
// First-open flow for fusion-code: install → bind API key (step 1) → start.
type AgentRow = { id: string; name: string; installed: boolean; detail?: string; running: boolean };

export function CodePage() {
  const [st, setSt] = useState<CodeStatus | null>(null);
  const [sel, setSel] = useState("fusion-code");
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const [providers, setProviders] = useState<Provider[]>([]);
  const [keyProvider, setKeyProvider] = useState("");
  const logRef = useRef<HTMLPreElement>(null);

  const refresh = useCallback(async () => {
    try {
      const s = await api.codeStatus();
      setSt(s);
    } catch (ex) {
      setErr((ex as Error).message);
    }
  }, []);

  useEffect(() => {
    refresh();
    api.providers().then((r) => setProviders(r.providers.filter((p) => p.enabled))).catch(() => {});
  }, [refresh]);

  const installing = st?.fusion.phase === "downloading" || st?.fusion.phase === "extracting" || st?.fusion.phase === "installing";

  // Poll while an install job is active so the log streams.
  useEffect(() => {
    if (!installing) return;
    const t = setInterval(refresh, 1500);
    return () => clearInterval(t);
  }, [installing, refresh]);

  // Keep running-state honest with a slower poll while visible.
  useEffect(() => {
    if (installing) return;
    const t = setInterval(refresh, 10000);
    return () => clearInterval(t);
  }, [installing, refresh]);

  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [st?.fusion.logs?.length]);

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

  const fusion = st?.agents.find((a) => a.id === "fusion-code");
  const isFusion = sel === "fusion-code";
  const selRow = st?.agents.find((a) => a.id === sel);

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div className="flex flex-wrap items-center gap-2">
        <Code2 size={20} className="text-indigo-500" />
        <h1 className="text-lg font-semibold">Coding Agent</h1>
        {fusion?.running && (
          <span className="rounded-lg bg-emerald-500/10 px-2 py-0.5 text-xs text-emerald-600 dark:text-emerald-400">
            fusion-code running on :{st?.fusion.web_port}
          </span>
        )}
        <select
          value={sel}
          onChange={(e) => setSel(e.target.value)}
          className="ml-auto rounded-xl border border-zinc-200/80 bg-white px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-zinc-900"
        >
          {(st?.agents ?? [{ id: "fusion-code", name: "fusion-code", installed: false, running: false }]).map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}{a.installed ? " ✓" : ""}
            </option>
          ))}
        </select>
      </div>

      {err && <p className="rounded-xl bg-red-500/10 px-3 py-2 text-sm text-red-500">{err}</p>}

      {!st && <p className="text-sm text-zinc-400">loading…</p>}

      {installing && (
        <div className="space-y-2 rounded-2xl border border-zinc-200/80 p-4 dark:border-zinc-800">
          <p className="flex items-center gap-2 text-sm text-zinc-600 dark:text-zinc-300">
            <RefreshCw size={14} className="animate-spin" /> installing — phase: {st?.fusion.phase}
          </p>
          <pre ref={logRef} className="max-h-64 overflow-auto rounded-xl bg-zinc-950 p-3 text-xs leading-5 text-zinc-300">
            {(st?.fusion.logs ?? []).join("\n")}
          </pre>
          {st?.fusion.error && <p className="text-sm text-red-500">{st.fusion.error}</p>}
        </div>
      )}

      {st?.fusion.phase === "error" && !installing && (
        <div className="space-y-2 rounded-2xl border border-red-500/30 bg-red-500/5 p-4">
          <p className="text-sm text-red-500">install failed: {st.fusion.error}</p>
          <Button variant="outline" onClick={() => action(() => api.codeInstall(sel), "retry")} disabled={busy !== ""}>
            <Download size={14} /> Retry install
          </Button>
        </div>
      )}

      {st && selRow && !installing && st.fusion.phase !== "error" && (
        <div className="space-y-3 rounded-2xl border border-zinc-200/80 p-4 dark:border-zinc-800">
          {!selRow.installed ? (
            <>
              <p className="text-sm text-zinc-600 dark:text-zinc-300">
                {isFusion
                  ? "fusion-code is the local headless coding agent (TypeScript/Bun). Install builds the CLI and its web console; the menu then manages service start/stop and the same-origin proxied web GUI."
                  : `${selRow.name} is a headless coding agent CLI. Install puts it on PATH via npm; dispatch it from the Agents page (@-mention or run form).`}
              </p>
              <p className="text-xs text-zinc-400">
                Not downloaded by default.{" "}
                {isFusion
                  ? "Install fetches the source from GitHub and builds with bun (bun required)."
                  : "Install runs npm install -g (npm required)."}
              </p>
              <Button onClick={() => action(() => api.codeInstall(sel), "install")} disabled={busy !== ""}>
                <Download size={14} /> Download &amp; install
              </Button>
            </>
          ) : isFusion ? (
            <>
              {!st.fusion.key_configured && (
                <div className="space-y-2 rounded-xl bg-amber-500/5 px-3 py-2 text-sm dark:bg-amber-500/10">
                  <p className="flex items-center gap-2 font-medium text-amber-600 dark:text-amber-400">
                    <KeyRound size={14} /> Step 1 · bind an API key
                  </p>
                  <p className="text-xs text-zinc-500 dark:text-zinc-400">
                    fusion-code needs an LLM API key before first use. Pick one of the console's configured
                    providers — the key is copied from the OS keychain into{" "}
                    <code className="rounded bg-zinc-100 px-1 dark:bg-zinc-800">~/.fusion-code/settings.json</code> (env
                    block) and never displayed.
                  </p>
                  <div className="flex flex-wrap items-center gap-2">
                    <select
                      value={keyProvider}
                      onChange={(e) => setKeyProvider(e.target.value)}
                      className="rounded-xl border border-zinc-200/80 bg-white px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-zinc-900"
                    >
                      <option value="">select provider…</option>
                      {providers.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name} ({p.type})
                        </option>
                      ))}
                    </select>
                    <Button
                      variant="outline"
                      disabled={!keyProvider || busy !== ""}
                      onClick={() => action(() => api.codeSetKey(keyProvider), "key")}
                    >
                      <KeyRound size={14} /> Bind key
                    </Button>
                  </div>
                </div>
              )}
              <div className="flex flex-wrap items-center gap-2">
                {fusion?.running ? (
                  <>
                    <Button variant="outline" onClick={() => action(() => api.codeStop(), "stop")} disabled={busy !== ""}>
                      <Power size={14} /> Stop
                    </Button>
                    <a
                      href="/shell/fusion-code/"
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-2 rounded-xl bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
                    >
                      <ExternalLink size={14} /> Open web console
                    </a>
                  </>
                ) : (
                  <Button onClick={() => action(() => api.codeStart(), "start")} disabled={busy !== ""}>
                    <Power size={14} /> Start fusion-code
                  </Button>
                )}
              </div>
              {fusion?.running && (
                <iframe
                  src="/shell/fusion-code/"
                  title="fusion-code console"
                  className="h-[70vh] w-full rounded-xl border border-zinc-200/80 dark:border-zinc-800"
                />
              )}
            </>
          ) : (
            <div className="flex flex-wrap items-center gap-2 text-sm text-zinc-600 dark:text-zinc-300">
              <Terminal size={15} className="text-emerald-500" />
              <span className="font-medium">{selRow.name} on PATH</span>
              {selRow.detail && <span className="text-xs text-zinc-400">{selRow.detail}</span>}
              <span className="text-xs text-zinc-400">— dispatch from the Agents page</span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
