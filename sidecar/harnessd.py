# harnessd.py — fusion-console sidecar wrapping fusion-harness.
#
# Line-delimited JSON-RPC over stdio. Console spawns this with the monorepo
# venv python; every chat turn passes through pre_llm (budget gate +
# compaction, the harness implementations themselves — no re-implementation).
#
# Methods:
#   ping                          -> {ok}
#   pre_llm {conv_id, budget_tokens, messages, model}
#                                 -> {proceed, reason, messages, tokens_est}
#   post_llm {conv_id, tokens}   -> {spent, exceeded}
#   tool_admit {conv_id, budget_tokens, tool, args}
#                                 -> {allowed, blocked_by, reason, needs_approval}
#   reset {conv_id}              -> {ok}   (drop budget/funnel state)
#
# State is per conversation id; conversations are cheap (Compactor + one
# budget object each). Audit events go to stderr via the LifecycleBus
# handler so the console log captures them without polluting the RPC stream.

import asyncio
import json
import logging
import sys

from fusion_harness import Compactor, LLMFunnel, LifecycleBus, ToolFunnel
from fusion_harness.compactor import CompactionConfig
from fusion_harness.permissions import PermissionEngine, PermissionRule

logging.basicConfig(stream=sys.stderr, level=logging.INFO,
                    format="%(asctime)s harnessd %(levelname)s %(message)s")
logger = logging.getLogger("harnessd")


class ConvBudget:
    # Minimal budget_ref contract: is_exceeded(graph_id) + spent_tokens.
    def __init__(self, limit_tokens: int):
        self.limit = limit_tokens
        self.spent_tokens = 0

    def is_exceeded(self, graph_id: str = "") -> bool:
        return self.limit > 0 and self.spent_tokens >= self.limit

    def add(self, tokens: int) -> None:
        self.spent_tokens = max(0, self.spent_tokens + tokens)


# Orchestrator tool allowlist. Everything else stays fail-closed (deny) —
# the console planner only ever calls these five tools.
ORCHESTRATOR_TOOLS = ("list_agents", "run_agent", "read_artifact", "run_workflow", "run_status",
                      "kb_search", "kb_ask")


class ConvState:
    def __init__(self, budget_tokens: int):
        self.bus = LifecycleBus()
        self.compactor = Compactor(config=CompactionConfig())
        self.budget = ConvBudget(budget_tokens)
        self.llm_funnel = LLMFunnel(lifecycle_bus=self.bus, compactor=self.compactor)
        self.llm_funnel.set_budget_ref(self.budget)
        engine = PermissionEngine(
            rules=[PermissionRule(action="allow", tool=t, layer="global",
                                  description="fusion-console orchestrator tool")
                   for t in ORCHESTRATOR_TOOLS],
        )
        self.tool_funnel = ToolFunnel(lifecycle_bus=self.bus, permissions=engine)
        self.tool_funnel.set_budget_ref(self.budget)


class Harnessd:
    def __init__(self):
        self.convs: dict[int, ConvState] = {}

    def state(self, conv_id: int, budget_tokens: int) -> ConvState:
        st = self.convs.get(conv_id)
        if st is None or (budget_tokens > 0 and st.budget.limit != budget_tokens):
            st = ConvState(budget_tokens)
            self.convs[conv_id] = st
        return st

    async def handle(self, req: dict) -> dict:
        method = req.get("method", "")
        p = req.get("params", {}) or {}
        try:
            if method == "ping":
                return {"ok": True}
            if method == "pre_llm":
                conv = int(p.get("conv_id", 0))
                st = self.state(conv, int(p.get("budget_tokens", 0)))
                proceed, reason, msgs = await st.llm_funnel.pre_llm(
                    None, node_label=f"conv:{conv}", model=p.get("model", ""),
                    messages=p.get("messages", []),
                )
                return {
                    "proceed": bool(proceed),
                    "reason": reason,
                    "messages": msgs,
                    "spent": st.budget.spent_tokens,
                    "limit": st.budget.limit,
                }
            if method == "post_llm":
                conv = int(p.get("conv_id", 0))
                st = self.state(conv, int(p.get("budget_tokens", 0)))
                st.budget.add(int(p.get("tokens", 0)))
                return {"spent": st.budget.spent_tokens,
                        "exceeded": st.budget.is_exceeded()}
            if method == "tool_admit":
                conv = int(p.get("conv_id", 0))
                st = self.state(conv, int(p.get("budget_tokens", 0)))
                out = await st.tool_funnel.admit(
                    None, tool_name=p.get("tool", ""), args=p.get("args", {}) or {},
                )
                return {
                    "allowed": bool(out.allowed),
                    "blocked_by": out.blocked_by,
                    "reason": out.reason,
                    "needs_approval": bool(out.needs_approval),
                }
            if method == "reset":
                self.convs.pop(int(p.get("conv_id", 0)), None)
                return {"ok": True}
            return {"error": f"unknown method {method}"}
        except Exception as e:  # fail visible to the caller, never crash the loop
            logger.exception("method %s failed", method)
            return {"error": str(e)}


def main() -> None:
    svc = Harnessd()
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError as e:
            logger.warning("bad rpc line: %s", e)
            continue
        result = asyncio.run(svc.handle(req))
        result["id"] = req.get("id")
        sys.stdout.write(json.dumps(result, ensure_ascii=False, default=str) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
