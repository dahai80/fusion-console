import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { api, type StudioStatus } from "./api";

// Shared agent-studio bootstrap for every surface that needs the daemon
// (chat sidebar Team tab, TeamPage). The hook polls slow, faster while an
// install job is running; retry forces an immediate refetch. The card renders
// the four bootstrap states and stays silent once the daemon answers.

export function useStudio(): { studio: StudioStatus | null; retry: () => void } {
  const [studio, setStudio] = useState<StudioStatus | null>(null);
  const [nonce, setNonce] = useState(0);
  useEffect(() => {
    let stop = false;
    const read = () => api.studioStatus().then((s) => { if (!stop) setStudio(s); }).catch(() => { if (!stop) setStudio(null); });
    read();
    const busy = studio?.phase === "installing" || studio?.phase === "starting";
    const t = setInterval(read, busy ? 2000 : 30000);
    return () => { stop = true; clearInterval(t); };
  }, [studio?.phase, nonce]);
  return { studio, retry: () => setNonce((n) => n + 1) };
}

const btnCls = "rounded-lg bg-amber-500/20 px-2.5 py-1.5 font-medium hover:bg-amber-500/30";

const install = (method: "auto" | "workspace" | "pypi" | "brew") =>
  api.studioInstall(method).catch(() => {});

export function StudioStatusCard({ showRunningChip = false }: { showRunningChip?: boolean }) {
  const { studio, retry } = useStudio();
  if (studio === null) {
    return (
      <div className="rounded-xl border border-zinc-300/80 bg-zinc-100/60 p-3 text-xs text-zinc-600 dark:border-zinc-700 dark:bg-zinc-800/60 dark:text-zinc-300">
        <p>无法获取 agent-studio 状态</p>
        <button onClick={retry} className={`mt-2 ${btnCls}`}>重试</button>
      </div>
    );
  }
  if (studio.installed && studio.daemon_running) {
    if (!showRunningChip) return null;
    return (
      <p className="flex items-center gap-1.5 rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs font-medium text-emerald-700 dark:text-emerald-400">
        <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500" /> agent-studio 运行中
      </p>
    );
  }
  const installing = studio.phase === "installing" || studio.phase === "starting";
  return (
    <div className="rounded-xl border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
      <div className="flex flex-wrap items-center gap-2">
        <div className="min-w-0 flex-1">
          {studio.installed ? (
            <p className="font-medium">fusion-agent-studio 已安装，daemon 未运行 — 启动后才能使用 Team</p>
          ) : (
            <p className="font-medium">Team 需要 fusion-agent-studio — 当前这台机器上没有安装，选一种方式安装（完成后自动启动 daemon）</p>
          )}
        </div>
        {installing ? (
          <p className="flex shrink-0 items-center gap-1.5"><Loader2 size={12} className="animate-spin" /> {studio.phase === "starting" ? "启动 daemon…" : "安装中…"}</p>
        ) : (
          <div className="flex shrink-0 flex-wrap gap-1.5">
            {studio.installed ? (
              <button onClick={() => void install("auto")} className={btnCls}>启动 agent-studio</button>
            ) : (
              <>
                <button onClick={() => void install("workspace")} className={btnCls}>一键安装（工作区 pip）</button>
                <button onClick={() => void install("pypi")} className={btnCls}>pip（PyPI）</button>
                <button onClick={() => void install("brew")} className={btnCls}>Homebrew</button>
              </>
            )}
          </div>
        )}
      </div>
      {studio.error && <p className="mt-2 font-mono text-[10px] opacity-80">{studio.error}</p>}
      {(studio.logs ?? []).length > 0 && (
        <pre className="mt-2 max-h-24 overflow-y-auto whitespace-pre-wrap font-mono text-[10px] opacity-70">{(studio.logs ?? []).slice(-8).join("\n")}</pre>
      )}
    </div>
  );
}
