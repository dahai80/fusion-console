import { useCallback, useEffect, useState } from "react";
import { Download, LoaderCircle, RefreshCw, Terminal } from "lucide-react";
import { api, type AgentInfo, type CodeStatus } from "../api";
import { Button } from "../components/ui";
import { Tag } from "./parts";
import { toolLabel } from "./workerModel";

// ToolsSection lists the command-line coding tools workers run on (the old
// "Agents (tools)" tab): which are installed, and a one-step install for the
// ones the console ships an installer for.
const INSTALLERS = ["fusion-code", "claude", "codex", "opencode", "atlascode"];
const installerId = (name: string) => (INSTALLERS.includes(name.toLowerCase()) ? name.toLowerCase() : "");

export default function ToolsSection() {
  const [agents, setAgents] = useState<AgentInfo[] | null>(null);
  const [code, setCode] = useState<CodeStatus | null>(null);
  const [installing, setInstalling] = useState("");
  const [err, setErr] = useState("");
  const load = useCallback(() => api.agents().then((r) => setAgents(r.agents ?? [])).catch((e) => setErr((e as Error).message)), []);
  const loadCode = useCallback(() => api.codeStatus().then(setCode).catch(() => setCode(null)), []);
  useEffect(() => { void load(); void loadCode(); }, [load, loadCode]);
  // While an install job runs, poll so the log moves and the row flips to
  // installed when it lands.
  useEffect(() => {
    if (!installing) return;
    const t = setInterval(() => api.codeStatus().then((s) => {
      setCode(s);
      if (!["downloading", "extracting", "installing"].includes(s.fusion.phase)) {
        setInstalling("");
        void load();
      }
    }).catch(() => {}), 1500);
    return () => clearInterval(t);
  }, [installing, load]);

  const tools = (agents ?? []).filter((a) => !a.is_worker).sort((a, b) => Number(b.detected) - Number(a.detected));
  const row = (name: string) => code?.agents.find((x) => x.id === installerId(name));

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-zinc-500 dark:text-zinc-400">
          {agents === null ? "读取中…" : `装好 ${tools.filter((a) => a.detected).length} 个，共 ${tools.length} 个`}
        </p>
        <Button variant="ghost" onClick={() => { setErr(""); void load(); void loadCode(); }}><RefreshCw size={14} /> 重新检测</Button>
      </div>
      {err && <p className="rounded-xl bg-red-500/10 px-3 py-2 text-sm text-red-600 dark:text-red-400">{err}</p>}
      <div className="divide-y divide-zinc-200/80 overflow-hidden rounded-2xl border border-zinc-200/80 bg-white shadow-sm shadow-zinc-900/5 dark:divide-zinc-800 dark:border-zinc-800 dark:bg-zinc-900">
        {tools.map((a) => {
          const r = row(a.name);
          const canInstall = !!installerId(a.name) && !!r && !r.installed && !a.detected;
          return (
            <div key={a.name} className="flex items-start gap-3 px-4 py-3">
              <Terminal size={16} className={`mt-0.5 shrink-0 ${a.detected ? "text-zinc-500" : "text-zinc-300 dark:text-zinc-600"}`} />
              <div className="min-w-0 flex-1">
                <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                  {toolLabel(a.name)}
                  {a.detected ? <Tag tone="ok">已安装</Tag> : <Tag>未安装</Tag>}
                </p>
                <p className="mt-0.5 truncate text-xs text-zinc-500 dark:text-zinc-400" title={a.command}>
                  命令 {a.command} · 权限 {a.permission_mode} · 每次上限 ${a.max_budget_usd.toFixed(2)}
                </p>
                {installing === a.name && (code?.fusion.logs ?? []).length > 0 && (
                  <pre className="mt-2 max-h-32 overflow-auto rounded-lg bg-zinc-950 p-2 text-[10px] leading-4 text-zinc-300">{(code?.fusion.logs ?? []).slice(-6).join("\n")}</pre>
                )}
              </div>
              {canInstall && (
                <Button variant="outline" className="shrink-0" disabled={installing !== ""}
                  onClick={async () => {
                    setErr("");
                    try {
                      setInstalling(a.name);
                      await api.codeInstall(installerId(a.name));
                      await loadCode();
                      await load();
                    } catch (ex) { setErr((ex as Error).message); setInstalling(""); }
                  }}>
                  {installing === a.name ? <LoaderCircle size={14} className="animate-spin" /> : <Download size={14} />} 安装
                </Button>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
