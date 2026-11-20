import { useCallback, useEffect, useState } from "react";
import { Brain, LoaderCircle, Play, RefreshCw, Square } from "lucide-react";
import { api, type MemoryHubStatus } from "../api";
import { Button } from "../components/ui";
import { Tag } from "./parts";

// MemorySection is the Agents > Memory tab: the fusion-memory hub
// (fm-server) install + lifecycle. Install = shallow clone of
// dahai80/fusion-memory + cargo build of the fm-server crate; start/stop go
// through the repository's own start.sh, health via GET /healthz.
export default function MemorySection() {
  const [st, setSt] = useState<MemoryHubStatus | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const load = useCallback(() => api.memoryHubStatus().then(setSt).catch((e) => setErr((e as Error).message)), []);
  useEffect(() => { void load(); }, [load]);
  // While an install job runs, poll so the log moves and the state flips
  // when it lands.
  const installing = busy === "install" || (["downloading", "installing"].includes(st?.phase ?? ""));
  useEffect(() => {
    if (!installing) return;
    const t = setInterval(() => api.memoryHubStatus().then(setSt).catch(() => {}), 1500);
    return () => clearInterval(t);
  }, [installing]);

  const act = async (op: "install" | "start" | "stop") => {
    setErr("");
    setBusy(op);
    try {
      if (op === "install") await api.memoryHubInstall();
      else if (op === "start") await api.memoryHubStart();
      else await api.memoryHubStop();
      await load();
    } catch (ex) {
      setErr((ex as Error).message);
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-zinc-500 dark:text-zinc-400">
          系统级长期记忆中枢（fm-server，127.0.0.1:{st?.port ?? 11435}）。安装 = 克隆 dahai80/fusion-memory 并编译 fm-server，需要 git 与 Rust 工具链。
        </p>
        <Button variant="ghost" onClick={() => { setErr(""); void load(); }}><RefreshCw size={14} /> 刷新</Button>
      </div>
      {err && <p className="rounded-xl bg-red-500/10 px-3 py-2 text-sm text-red-600 dark:text-red-400">{err}</p>}
      <div className="overflow-hidden rounded-2xl border border-zinc-200/80 bg-white shadow-sm shadow-zinc-900/5 dark:border-zinc-800 dark:bg-zinc-900">
        <div className="flex items-start gap-3 px-4 py-3">
          <Brain size={16} className={`mt-0.5 shrink-0 ${st?.running ? "text-emerald-500" : "text-zinc-300 dark:text-zinc-600"}`} />
          <div className="min-w-0 flex-1">
            <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
              fusion-memory
              {st?.running ? <Tag tone="ok">运行中</Tag> : st?.installed ? <Tag>已安装</Tag> : <Tag>未安装</Tag>}
            </p>
            <p className="mt-0.5 truncate text-xs text-zinc-500 dark:text-zinc-400" title={st?.detail}>
              {st?.detail ? `fm-server ${st.detail}` : "fm-server 未构建 · 安装后 worker 共享记忆（fusion-memory 模式）可用"}
            </p>
            {st?.error && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{st.error}</p>}
            {installing && (st?.logs ?? []).length > 0 && (
              <pre className="mt-2 max-h-32 overflow-auto rounded-lg bg-zinc-950 p-2 text-[10px] leading-4 text-zinc-300">{(st?.logs ?? []).slice(-6).join("\n")}</pre>
            )}
          </div>
          <div className="flex shrink-0 gap-2">
            {!st?.installed && !st?.running && (
              <Button variant="outline" disabled={installing} onClick={() => act("install")}>
                {busy === "install" || installing ? <LoaderCircle size={14} className="animate-spin" /> : null} 安装
              </Button>
            )}
            {st?.installed && !st.running && (
              <Button variant="outline" disabled={busy !== ""} onClick={() => act("start")}>
                {busy === "start" ? <LoaderCircle size={14} className="animate-spin" /> : <Play size={14} />} 启动
              </Button>
            )}
            {st?.installed && st.running && (
              <Button variant="outline" disabled={busy !== ""} onClick={() => act("stop")}>
                {busy === "stop" ? <LoaderCircle size={14} className="animate-spin" /> : <Square size={14} />} 停止
              </Button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
