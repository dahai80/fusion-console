import { useState } from "react";
import { Bot, Brain, Puzzle, Wrench } from "lucide-react";
import { SkillsPanel } from "../SkillsPanel";
import WorkersSection from "./WorkersSection";
import BrainSection from "./BrainSection";
import ToolsSection from "./ToolsSection";

// Agents page: Workers first (status + management), then the brain, skills,
// and the command-line tools workers run on. The open tab survives a reload
// of the same browser tab.
type Tab = "workers" | "brain" | "skills" | "tools";
const TABS: { id: Tab; label: string; icon: typeof Bot }[] = [
  { id: "workers", label: "Worker", icon: Bot },
  { id: "brain", label: "大脑", icon: Brain },
  { id: "skills", label: "Skills", icon: Puzzle },
  { id: "tools", label: "工具", icon: Wrench },
];
const TAB_KEY = "fc-agents-tab";

function initialTab(): Tab {
  try {
    const t = sessionStorage.getItem(TAB_KEY);
    if (TABS.some((x) => x.id === t)) return t as Tab;
  } catch { /* storage blocked */ }
  return "workers";
}

export default function AgentsPage() {
  const [tab, setTabState] = useState<Tab>(initialTab);
  const setTab = (t: Tab) => {
    setTabState(t);
    try { sessionStorage.setItem(TAB_KEY, t); } catch { /* storage blocked */ }
  };
  const goTools = () => setTab("tools");
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl px-4 pb-10 pt-4 sm:px-6 sm:pt-6">
        <div role="tablist" aria-label="Agents" className="mb-4 flex gap-1 overflow-x-auto rounded-xl bg-zinc-200/60 p-1 dark:bg-zinc-800/60 sm:inline-flex">
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
      </div>
    </div>
  );
}
