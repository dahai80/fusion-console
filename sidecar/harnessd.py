# harnessd.py — fusion-console sidecar wrapping fusion-harness.
#
# Sidecar protocol v1 (fusion-harness issue #110): line-delimited JSON-RPC
# over stdio with a hello handshake, typed error objects and kernel-owned
# approvals. Console spawns this with the monorepo venv python; every chat
# turn passes through pre_llm (budget gate + compaction, the harness
# implementations themselves — no re-implementation).
#
# Wire:
#   {id, method:"hello", params:{protocol_version:1}} -> {result:{protocol_version,
#       kernel_version, commit, methods, features}}; mismatch = protocol error
#   any other method before a successful hello -> error protocol
#   errors are {"id", "error":{code, message}} with code in
#       protocol|state|budget|permission|internal — never a bare string
#   pre_llm {conv_id, budget_tokens, node_label, model, messages}
#                                 -> {proceed, reason, messages, spent_tokens, limit_tokens}
#   post_llm {conv_id, budget_tokens, usage:{prompt_tokens, completion_tokens}}
#                                 -> {ok, spent_tokens, budget_exceeded}
#   tool_admit {conv_id, budget_tokens, tool_name, args}
#                                 -> outcome + approval_id when needs_approval
#   resolve {approval_id, approve} -> outcome (approve grants the tool for
#       the session and re-runs the admission chain)
#   meter {conv_id}              -> funnel meter + budget state
#   reset {conv_id}              -> {ok}
#
# State is per conversation id; conversations are cheap (Compactor + one
# budget object each). Audit events go to stderr via the LifecycleBus
# handler so the console log captures them without polluting the RPC stream.

import json
import logging
import subprocess
import sys
import uuid

from fusion_harness import KERNEL_VERSION, Compactor, LLMFunnel, LifecycleBus, ToolFunnel
from fusion_harness.compactor import CompactionConfig
from fusion_harness.permissions import PermissionEngine, PermissionRule

PROTOCOL_VERSION = 1
METHODS = ["hello", "ping", "pre_llm", "post_llm", "tool_admit", "resolve", "meter", "report", "reset"]
FEATURES = ["approvals", "meter", "compaction", "report"]

logging.basicConfig(stream=sys.stderr, level=logging.INFO,
                    format="%(asctime)s harnessd %(levelname)s %(message)s")
logger = logging.getLogger("harnessd")


def _error(req_id, code, message):
    return {"id": req_id, "error": {"code": code, "message": message}}


def _commit():
    try:
        out = subprocess.run(["git", "rev-parse", "--short", "HEAD"], capture_output=True,
                             text=True, timeout=5,
                             cwd=__file__.rsplit("/", 2)[0])
        if out.returncode == 0:
            return out.stdout.strip()
    except (OSError, subprocess.SubprocessError) as e:
        logger.debug("commit resolve failed: %s", e)
    return "unknown"


class ConvBudget:
    # Minimal budget_ref contract: is_exceeded / record_usage / spent_tokens.
    def __init__(self, limit_tokens: int):
        self.limit = limit_tokens
        self.spent_tokens = 0

    def is_exceeded(self, graph_id: str = "") -> bool:
        return self.limit > 0 and self.spent_tokens >= self.limit

    def record_usage(self, prompt_tokens: int, completion_tokens: int, graph_id: str = "") -> None:
        self.spent_tokens = max(0, self.spent_tokens + int(prompt_tokens) + int(completion_tokens))

    def add(self, tokens: int) -> None:
        self.spent_tokens = max(0, self.spent_tokens + tokens)


# Orchestrator tool allowlist. Everything else stays fail-closed (the engine
# is armed by these rules, so unmatched tools ask -> needs_approval).
ORCHESTRATOR_TOOLS = ("list_agents", "run_agent", "read_artifact", "run_workflow", "run_status",
                      "kb_search", "kb_ask", "team_status", "create_team_member")


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
        self.pending_approvals = {}

    def llm_meter(self) -> dict:
        # Kernel 0.22 exposes budget and lifecycle events but no funnel meter.
        return self.llm_funnel.meter() if hasattr(self.llm_funnel, "meter") else {}


class Harnessd:
    def __init__(self):
        self.convs: dict[int, ConvState] = {}
        self.helloed = False

    def state(self, conv_id: int, budget_tokens: int) -> ConvState:
        st = self.convs.get(conv_id)
        if st is None or (budget_tokens > 0 and st.budget.limit != budget_tokens):
            st = ConvState(budget_tokens)
            self.convs[conv_id] = st
        return st

    async def handle(self, req: dict) -> dict:
        req_id = req.get("id")
        method = req.get("method", "")
        p = req.get("params", {}) or {}
        try:
            if method == "hello":
                return self._hello(req_id, p)
            if not self.helloed:
                return _error(req_id, "protocol", "hello required before %s" % method)
            handler = {
                "ping": self._ping,
                "pre_llm": self._pre_llm,
                "post_llm": self._post_llm,
                "tool_admit": self._tool_admit,
                "resolve": self._resolve,
                "meter": self._meter,
                "report": self._report,
                "reset": self._reset,
            }.get(method)
            if handler is None:
                return _error(req_id, "protocol", "unknown method %s" % method)
            return await handler(req_id, p)
        except Exception as e:  # fail-closed: typed error, never crash the loop
            logger.exception("method %s failed", method)
            return _error(req_id, "internal", "internal error in %s: %s" % (method, e))

    def _hello(self, req_id, p) -> dict:
        client_v = p.get("protocol_version")
        if client_v != PROTOCOL_VERSION:
            logger.warning("hello version mismatch client=%s kernel=%s", client_v, PROTOCOL_VERSION)
            return _error(req_id, "protocol",
                          "protocol version mismatch: client=%s kernel=%s"
                          % (client_v, PROTOCOL_VERSION))
        self.helloed = True
        return {"id": req_id, "result": {
            "protocol_version": PROTOCOL_VERSION,
            "kernel_version": KERNEL_VERSION,
            "commit": _commit(),
            "methods": METHODS,
            "features": FEATURES,
        }}

    async def _ping(self, req_id, p) -> dict:
        return {"id": req_id, "result": {"pong": True}}

    async def _pre_llm(self, req_id, p) -> dict:
        conv = int(p.get("conv_id", 0))
        st = self.state(conv, int(p.get("budget_tokens", 0)))
        proceed, reason, msgs = await st.llm_funnel.pre_llm(
            None, node_label=p.get("node_label") or "conv:%d" % conv,
            model=p.get("model", ""), messages=p.get("messages", []),
        )
        return {"id": req_id, "result": {
            "proceed": bool(proceed),
            "reason": reason,
            "messages": msgs,
            "spent_tokens": st.budget.spent_tokens,
            "limit_tokens": st.budget.limit,
        }}

    async def _post_llm(self, req_id, p) -> dict:
        conv = int(p.get("conv_id", 0))
        st = self.state(conv, int(p.get("budget_tokens", 0)))
        usage = p.get("usage") or {}
        prompt = int(usage.get("prompt_tokens", 0) or 0)
        completion = int(usage.get("completion_tokens", 0) or 0)
        # Legacy clients may still send a flat token count.
        if prompt + completion == 0 and p.get("tokens"):
            completion = int(p.get("tokens", 0) or 0)
        st.budget.record_usage(prompt, completion)
        return {"id": req_id, "result": {
            "ok": True,
            "spent_tokens": st.budget.spent_tokens,
            "budget_exceeded": st.budget.is_exceeded(),
        }}

    async def _tool_admit(self, req_id, p) -> dict:
        conv = int(p.get("conv_id", 0))
        st = self.state(conv, int(p.get("budget_tokens", 0)))
        out = await st.tool_funnel.admit(
            None, tool_name=p.get("tool_name") or p.get("tool", ""),
            args=p.get("args", {}) or {},
        )
        result = out.to_dict()
        if out.needs_approval:
            approval_id = uuid.uuid4().hex
            st.pending_approvals[approval_id] = {"conv_id": conv, "params": p}
            result["approval_id"] = approval_id
        return {"id": req_id, "result": result}

    async def _resolve(self, req_id, p) -> dict:
        approval_id = str(p.get("approval_id", ""))
        conv_id = 0
        entry = None
        for st in self.convs.values():
            if approval_id in st.pending_approvals:
                entry = st.pending_approvals.pop(approval_id)
                conv_id = entry["conv_id"]
                break
        if entry is None:
            return _error(req_id, "state", "unknown approval_id: %s" % approval_id)
        st = self.state(conv_id, int(entry["params"].get("budget_tokens", 0)))
        tool_name = entry["params"].get("tool_name") or entry["params"].get("tool", "")
        if not p.get("approve", False):
            logger.info("approval denied id=%s tool=%s", approval_id, tool_name)
            return {"id": req_id, "result": {
                "allowed": False, "blocked_by": "human_gate", "reason": "approval denied",
            }}
        # Kernel-owned approval: session allow rule for the tool, then the
        # FULL admission chain again — budget and the allowlist still apply.
        # The grant is session-scoped (rules have no removal API by design;
        # reset conv state drops it).
        st.tool_funnel.permissions.add_rule(
            PermissionRule(action="allow", tool=tool_name, layer="graph",
                           description="approved via console resolve")
        )
        out = await st.tool_funnel.admit(
            None, tool_name=tool_name, args=entry["params"].get("args", {}) or {},
        )
        result = out.to_dict()
        result["granted_tool"] = tool_name if out.allowed else ""
        return {"id": req_id, "result": result}

    async def _meter(self, req_id, p) -> dict:
        conv = int(p.get("conv_id", 0))
        st = self.state(conv, int(p.get("budget_tokens", 0)))
        meter = st.llm_meter()
        meter["budget_spent_tokens"] = st.budget.spent_tokens
        meter["budget_exceeded"] = st.budget.is_exceeded()
        return {"id": req_id, "result": meter}

    async def _report(self, req_id, p) -> dict:
        # G4 observability report (fusion-console issue #78): one call returns
        # the funnel snapshot (llm meter, budget, recent lifecycle events) and
        # the failure attribution over the recorded events. No trajectories
        # are recorded by this sidecar, so attribution runs over lifecycle
        # events; when the kernel cannot derive verdicts the attribution
        # section stays empty and the UI hides it.
        conv = int(p.get("conv_id", 0))
        st = self.state(conv, int(p.get("budget_tokens", 0)))
        snapshot = {
            "llm_meter": st.llm_meter(),
            "budget": {
                "spent": st.budget.spent_tokens,
                "limit": st.budget.limit,
                "exceeded": st.budget.is_exceeded(),
            },
            "events": [ev.to_dict() for ev in st.bus.replay(limit=200)],
        }
        attribution = {"runs": {}, "mode_counts": {}, "cluster_counts": {}}
        try:
            from types import SimpleNamespace
            from fusion_harness import FailureAnalyzer  # Optional on older kernels.

            analyzer = FailureAnalyzer()
            records = []
            for ev in st.bus.replay():
                payload = ev.payload or {}
                records.append(SimpleNamespace(
                    run_id=payload.get("run_id") or ev.session_id or ("conv:%d" % conv),
                    ts=ev.ts, event=ev.event, domain=ev.domain, **{
                        k: v for k, v in payload.items() if k not in ("run_id",)},
                ))
            if records:
                attribution = analyzer.analyze(records)
        except Exception as e:  # attribution is best-effort, never break report
            logger.warning("report attribution unavailable: %s", e)
        return {"id": req_id, "result": {"snapshot": snapshot, "attribution": attribution}}

    async def _reset(self, req_id, p) -> dict:
        self.convs.pop(int(p.get("conv_id", 0)), None)
        return {"id": req_id, "result": {"ok": True}}


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
            print(json.dumps(_error(None, "protocol", "malformed JSON: %s" % e)), flush=True)
            continue
        import asyncio

        result = asyncio.run(svc.handle(req))
        sys.stdout.write(json.dumps(result, ensure_ascii=False, default=str) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
