import { useCallback, useEffect, useState } from "react";
import { api, type AgentInfo, type Orchestrator, type Provider } from "./api";

// Live setup state shared by the onboarding wizard and the account menu.
// Every step's "done" flag is derived from what the server reports right
// now, so re-running the wizard on a configured console shows real progress
// instead of starting over at step one.

export type SetupStepId = "gateway" | "models" | "verify" | "team";

export type SetupStatus = {
  loaded: boolean;
  gateway: { base_url: string; admin_url: string; reachable: boolean; installed: boolean; binary: string } | null;
  providers: Provider[]; // enabled only
  gatewayModels: string[];
  agents: AgentInfo[];
  orch: Orchestrator | null;
  factoryPassword: boolean;
  verified: boolean;
};

const VERIFIED_KEY = "fc-setup-verified";

export function markVerified(providerId: string) {
  try { localStorage.setItem(VERIFIED_KEY, providerId || "1"); } catch { /* per-browser hint only */ }
}

function readVerified(): boolean {
  try { return !!localStorage.getItem(VERIFIED_KEY); } catch { return false; }
}

const EMPTY: SetupStatus = {
  loaded: false, gateway: null, providers: [], gatewayModels: [], agents: [], orch: null,
  factoryPassword: false, verified: false,
};

export function keyReady(s: SetupStatus) {
  return s.providers.some((p) => p.type === "gateway" && p.enabled && !!p.api_key_id);
}

export function directProviders(s: SetupStatus) {
  return s.providers.filter((p) => p.type !== "gateway");
}

export function stepDone(s: SetupStatus, id: SetupStepId): boolean {
  switch (id) {
    case "gateway":
      return (!!s.gateway?.reachable && keyReady(s)) || directProviders(s).length > 0;
    case "models":
      return s.gatewayModels.length > 0 || directProviders(s).length > 0;
    case "verify":
      return s.verified;
    case "team":
      return (s.orch?.workers?.length ?? 0) > 0;
  }
}

// The three steps a newcomer needs before the console is usable; "team"
// (brain + workers) is optional and does not count toward the progress.
export const REQUIRED_STEPS: SetupStepId[] = ["gateway", "models", "verify"];

export function progress(s: SetupStatus) {
  const done = REQUIRED_STEPS.filter((id) => stepDone(s, id)).length;
  return { done, total: REQUIRED_STEPS.length };
}

export function useSetupStatus(enabled = true) {
  const [status, setStatus] = useState<SetupStatus>(EMPTY);
  const refresh = useCallback(async () => {
    const settle = <T,>(p: Promise<T>) => p.then((v) => v, () => null);
    const [gw, prov, models, agents, orch, hint, convs] = await Promise.all([
      settle(api.gatewayStatus()),
      settle(api.providers()),
      settle(api.gatewayModels()),
      settle(api.agents()),
      settle(api.orchestration()),
      settle(api.authHint()),
      settle(api.conversations()),
    ]);
    setStatus({
      loaded: true,
      gateway: gw,
      providers: (prov?.providers ?? []).filter((p) => p.enabled),
      gatewayModels: models?.models ?? [],
      agents: agents?.agents ?? [],
      orch: orch?.orchestrator ?? null,
      factoryPassword: !!hint?.factory_default,
      // A chat conversation on record means the model path has worked
      // before (consoles configured before the wizard tracked this).
      verified: readVerified() || (convs?.conversations ?? []).some((c) => c.Kind === "chat"),
    });
  }, []);
  useEffect(() => {
    if (enabled) void refresh();
  }, [enabled, refresh]);
  return { status, refresh };
}
