import { useState } from "react";
import { Bot, Brain, Database, Puzzle, Terminal, Wrench } from "lucide-react";
import { SkillsPanel } from "../SkillsPanel";
import WorkersSection from "./WorkersSection";
import BrainSection from "./BrainSection";
import ToolsSection from "./ToolsSection";
import MemorySection from "./MemorySection";

// Agents page: two top-level tabs — Coding agents (workers, brain, skills,
// and the command-line tools workers run on) and Memory (Tools) (the
// fusion-memory hub installer). The open tabs survive a reload of the same
// browser tab.
type TopTab = "coding" | "memory";
type Tab = "workers" | "brain" | "skills" | "tools";
const TOPS: { id: TopTab; label: string; icon: typeof Bot }[] = [
  { id: "coding", label: "Coding agents", icon: Terminal },
  { id: "memory", label: "Memory (Tools)", icon: Database },
];
const TABS: { id: Tab; label: string; icon: typeof Bot }[] = [
  { id: "workers", label: "Worker", icon: Bot },
  { id: "brain", label: "大脑", icon: Brain },
  { id: "skills", label: "Skills", icon: Puzzle },
  { id: "tools", label: "工具", icon: Wrench },
];
const TAB_KEY = "fc-agents-tab";
const TOP_KEY = "fc-agents-top";

function initialTab(): Tab {
  try {
    const t = sessionStorage.getItem(TAB_KEY);
    if (TABS.some((x) => x.id === t)) return t as Tab;
  } catch { /* storage blocked */ }
  return "workers";
}

function initialTop(): TopTab {
  try {
    const t = sessionStorage.getItem(TOP_KEY);
    if (TOPS.some((x) => x.id === t)) return t as TopTab;
  } catch { /* storage blocked */ }
  return "coding";
}

export default function AgentsPage() {
  const [top, setTopState] = useState<TopTab>(initialTop);
  const [tab, setTabState] = useState<Tab>(initialTab);
  const setTop = (t: TopTab) => {
    setTopState(t);
    try { sessionStorage.setItem(TOP_KEY, t); } catch { /* storage blocked */ }
  };
  const setTab = (t: Tab) => {
    setTabState(t);
    try { sessionStorage.setItem(TAB_KEY, t); } catch { /* storage blocked */ }
  };
  const goTools = () => { setTop("coding"); setTab("tools"); };
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl px-4 pb-10 pt-4 sm:px-6 sm:pt-6">
        <div role="tablist" aria-label="Agents sections" className="mb-3 flex gap-1 overflow-x-auto rounded-xl bg-zinc-200/60 p-1 dark:bg-zinc-800/60 sm:inline-flex">
          {TOPS.map(({ id, label, icon: Icon }) => (
            <button key={id} role="tab" aria-selected={top === id} onClick={() => setTop(id)}
              className={`inline-flex min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-lg px-4 py-2 text-sm font-semibold transition-colors sm:flex-none ${
                top === id ? "bg-white text-zinc-900 shadow-sm dark:bg-zinc-900 dark:text-white" : "text-zinc-500 hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200"}`}>
              <Icon size={15} className="shrink-0" /> {label}
            </button>
          ))}
        </div>
        {top === "memory" && <MemorySection />}
        {top === "coding" && (
          <>
            <div role="tablist" aria-label="Coding agents" className="mb-4 flex gap-1 overflow-x-auto rounded-xl bg-zinc-200/60 p-1 dark:bg-zinc-800/60 sm:inline-flex">
              {TABS.map(({ id, label, icon: Icon }) => (
                <button key={id} role="tab" aria-selected={tab === id} onClick={() => setTab(id)}
                  className={`inline-flex min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-lg px-3 py-2 text-sm font-medium transition-colors sm:flex-none sm:py-1.5 ${
                    tab === id ? "bg-white text-zinc-900 shadow-sm dark:bg-zinc-900 dark:text-white" : "text-zinc-500 hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200"}`}>
                  <Icon size={15} className="shrink-0" /> {label}
                </button>
              ))}
            </div>
            {tab === "workers" && <WorkersSection onGoTools={goTools} />}
            {tab === "brain" && <BrainSection onGoTools={goTools} />}
            {tab === "skills" && <SkillsPanel />}
            {tab === "tools" && <ToolsSection />}
          </>
        )}
      </div>
    </div>
  );
}
