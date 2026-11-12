# fusion-console Orchestration Plan — Chat as the Main Brain

Status: proposal v2, under review (no code yet)
Date: 2026-11-13 (v2 after review feedback)
Owner: dahai80

Review decisions baked into v2:
1. **fusion-harness first** — integrate the harness into the chat pipeline
   before any orchestration, so long conversations stay grounded (no drift)
2. **Native tool-calls** — verified working: gateway glm-5 passthrough,
   streaming tool_call deltas + finish_reason OK (2026-11-13 spike)
3. **Per-agent dedicated workspace** — fixed directory per agent, isolated
   by default, configurable

[English](#english) | [中文](docs/orchestration-plan-2026-11.md) — single file, EN first, CN follows.

---

## English

### 1. Goal

Let the chat LLM act as the orchestrating "main brain": it decides when to
delegate work to a headless coding agent or a cowork workflow, watches the
run, reads the result, and continues the conversation. fusion-console stays a
thin client; fusion-harness and fusion-cowork provide the guardrail and
workflow capability instead of console reimplementing them.

### 2. Non-goals

- No PTY terminal (v2 boundary unchanged)
- No public internet exposure — orchestration inherits Tailscale-only access
- Console does not become a second DAG engine — workflow execution stays in
  fusion-cowork, accessed over HTTP

### 3. Current state

- Chat: browser → console → `chat.Provider` (gateway glm-5) → SSE. No memory
  of tools, no delegation.
- Agents: one-shot runs through `agent.Adapter` (claude / fusion-code / codex
  / opencode), stream-json events, budget-cap kill, 30-min timeout. The human
  is the scheduler.
- fusion-cowork (`:11438`): `POST /api/runs` executes a DAG workflow,
  `/api/runs/{id}` status, cancel/pause/resume, event streams. Owns the
  WorkflowEngine + node library.
- fusion-harness: pure-stdlib Python kernel (budget funnels, lifecycle hooks,
  permission engine, compaction). Consumed by cowork/agent-studio as a
  library; fusion-code mirrors it via contract tests.

### 4. Design

#### 4.1 Orchestration loop lives in console (Go)

Console owns the loop because it already owns the three things the loop
needs: the chat session, the agent adapters, and cost accounting.

```
browser ──SSE── console orchestrator loop ──► chat.Provider (planner LLM)
                    │  ▲
        tool_call   ▼  │ tool_result
                    │
     ┌──────────────┼───────────────────┐
     ▼              ▼                   ▼
 run_agent      run_workflow        ask_user(*)
 (agent.Adapter, (cowork HTTP        (pause loop,
  subprocess)     :11438 /api/runs)   prompt in UI)
```

`ask_user` ends the turn; the user's next message resumes the loop. The loop
is bounded: max tool iterations per turn (default 8), per-conversation budget
funnel (extends the existing per-run cap), wall-clock cap.

#### 4.2 Step 1 (first) — fusion-harness in the chat pipeline

Console is Go; the harness is pure-stdlib Python. Console ships a small
sidecar (`sidecar/harnessd.py` inside the fusion-console repo) that imports
`fusion_harness` (already installed in the monorepo venv) and speaks
JSON-RPC over stdio. Console starts it on boot, restarts it on crash, and
every chat turn passes through it:

- **Compaction**: when the persisted conversation exceeds the token
  threshold, the Compactor summarizes old turns and keeps the recent N turns
  verbatim — the model never sees an unbounded, drifting context ("no
  hallucinations")
- **Budget funnel (LLMFunnel)**: per-conversation token budget metered
  before each LLM call; breach = visible refusal, never a silent truncation
- **Audit**: LifecycleBus events persist to
  `~/.fusion-console/log/harness.jsonl`

No Go re-implementation, no contract drift — the harness runs as-is.

#### 4.3 Tool protocol: native tool-calls (verified)

The planner uses native OpenAI `tools` / `tool_calls` through the gateway.
Spike result (2026-11-13, real glm-5 via gateway `:11432`): non-streaming
`finish_reason=tool_calls` with correct arguments; streaming emits the
tool_call id/name in the first delta and streams argument fragments — the
console provider gains tool-call parsing (accumulate `arguments` by index,
emit a `tool_call` event on `finish_reason`). Same behavior for the
Anthropic-compat provider (`tool_use` blocks) for non-gateway providers.

#### 4.4 Tools exposed to the planner

| Tool | Backing | Notes |
|---|---|---|
| `run_agent(agent, task, workdir)` | existing `agent.Adapter` | returns run id; console streams events into the transcript as tool results; budget/timeout caps unchanged |
| `read_artifact(path)` | sandboxed read inside the run workdir | bounded size; lets the planner verify results |
| `list_agents()` | config | detected CLIs only |

Phase 2 adds:

| Tool | Backing | Notes |
|---|---|---|
| `run_workflow(workflow_id, params)` | cowork `POST /api/runs` | console proxies and tails cowork events; DAG progress renders as a card in the chat transcript |
| `run_status(run_id)` | cowork / console | poll or event-tail |

Phase 3 candidates: `schedule` (cron a workflow), `memory` (fusion-memory
lookup), file tools beyond the workdir (permission-gated).

#### 4.5 fusion-harness on the orchestration path

The same sidecar guards the planner loop: every tool call passes
`ToolFunnel.admit()` semantics (lifecycle → hooks → permission → budget →
execute) via the sidecar, so the 12 dangerous-pattern content scans, the
deny-first permission chain, and the per-conversation budget funnel are the
harness implementation, not a Go copy. Agent-dispatch permissions:
`orchestrator.mode` in config — `off` / `confirm` / `auto`; `confirm`
(default) requires one click before any `run_agent` / `run_workflow`.

#### 4.6 fusion-cowork integration

- Console → cowork over HTTP `:11438` with `FUSION_CLIENT_KEY` (env, same
  keychain pattern as gateway keys); cowork is optional — when unreachable,
  `run_workflow` returns a visible error and chat continues without it
- Workflow run events are proxied into the chat SSE stream as a typed
  `workflow` event; the SPA renders a DAG progress card (nodes/edges from
  `GET /api/workflows/{id}`)
- Approval gates (cowork `review` endpoints) surface as interactive cards;
  user clicks resume → console calls `POST /api/runs/{id}/resume`

#### 4.7 UX

- Composer toggle: "Orchestrator" (off by default; config default)
- Inline cards: agent run card (live text/tool events, cost), workflow card
  (DAG progress), confirmation card (mode `confirm`)
- All costs roll into the existing cost report with a `planner` row

#### 4.8 Persistence and agent workspaces

- `messages.meta` already JSON — tool calls/results serialize there
- Agent runs gain a `conversation_id` column; cowork run ids are stored in
  the message meta for resume/replay
- **Per-agent dedicated workspace** (review decision 3): each agent gets a
  fixed, isolated directory — default `~/.fusion-console/agents/<name>/`,
  overridable per agent in `config.json` (`agents[].workdir`). Orchestrator
  dispatches always run inside the agent's own workspace; agents never share
  scratch state. Manual runs still allow an explicit workdir override.

### 5. Milestones

1. **M1 — harness in the chat pipeline** (review decision 1): sidecar
   process, compaction before every LLM call, per-conversation LLM budget
   funnel, harness audit log. Success: a 100-turn conversation keeps coherent
   context and stays within budget; breach fails visibly.
2. **M2 — planner loop with native tool-calls + `run_agent`**: tool-call
   parsing in providers (verified passthrough), bounded loop, per-agent
   dedicated workspaces, agent run card in UI, ToolFunnel-guarded dispatch.
   Success: in chat, "fix the failing test in ~/fusion/fusion-console" ends
   with a verified artifact and a cost row, hands-free.
3. **M3 — cowork workflows**: `run_workflow` / `run_status`, DAG progress
   card, approval/resume cards. Success: a stored workflow runs from chat and
   its result lands in the conversation.
4. **M4 (optional) — planner-in-cowork mode**: move the planner loop into a
   cowork-hosted agent for workflow-heavy setups; console keeps the chat UX
   and proxies events. Decide after M3 usage data.

### 6. Risks / open questions

- glm-5 JSON discipline on the tool protocol — mitigate with strict parse +
  one repair round-trip + iteration cap
- Gateway tool-call passthrough unverified — phase 1 deliberately avoids it
- Double budget accounting (planner tokens + agent cost) — planner usage
  already arrives via provider usage events; keep them separate rows
- cowork auth model (client key vs identity JWT) — reuse whatever
  `FUSION_CLIENT_KEY` gives today, revisit with fusion-identity later

---

## 中文

### 1. 目标

让 chat 大模型成为"主大脑"：自主判断何时把任务派给 headless 编码 agent 或
cowork 工作流，跟踪执行、读取结果、继续对话。fusion-console 保持薄客户端；
护栏能力来自 fusion-harness，工作流执行来自 fusion-cowork，console 不重复造。

### 2. 非目标

- 不做 PTY 终端（v2 边界不变）
- 不暴露公网（沿用 Tailscale-only）
- console 不做第二个 DAG 引擎 — 工作流执行留在 cowork，走 HTTP

### 3. 现状

- Chat：浏览器 → console → `chat.Provider`（gateway glm-5）→ SSE；无工具、无派生。
- Agent：`agent.Adapter` 一次性任务（claude/fusion-code/codex/opencode），
  stream-json 事件、预算熔断、30 分钟超时；调度者是人。
- fusion-cowork（`:11438`）：`POST /api/runs` 执行 DAG 工作流，含状态/取消/暂停/恢复/事件流。
- fusion-harness：纯标准库 Python 内核（预算漏斗、生命周期钩子、权限引擎、上下文压缩），
  cowork/agent-studio 直接 import；fusion-code 用契约测试对齐。

### 4. 设计（v2）

#### 4.1 第一步：fusion-harness 进 chat 管线（评审决定 1）

console 是 Go，harness 是纯标准库 Python。console 仓库内自带一个轻量
sidecar（`sidecar/harnessd.py`），import `fusion_harness`（monorepo venv 已
安装），走 stdio JSON-RPC。console 启动时拉起、崩溃自动重启，每次 chat
请求先过它：

- **上下文压缩**：持久化会话超过 token 阈值时，Compactor 摘要旧轮次、保留
  最近 N 轮原文 — 模型永远看不到无界漂移的上下文（"不胡言乱语"）
- **预算漏斗（LLMFunnel）**：按会话计量 token 预算，每次 LLM 调用前检查；
  超限 = 可见拒绝，绝不静默截断
- **审计**：LifecycleBus 事件落 `~/.fusion-console/log/harness.jsonl`

不重写、不翻译 harness — 原样运行，零契约漂移。

#### 4.2 工具协议：原生 tool-calls（已验证）

规划模型经 gateway 用原生 OpenAI `tools`/`tool_calls`。Spike 结果
（2026-11-13，gateway `:11432` 真 glm-5）：非流式 `finish_reason=tool_calls`
且参数正确；流式首 delta 带 tool_call id/name、arguments 分片增量到达 —
console provider 增加工具调用解析（按 index 累积 `arguments`，
`finish_reason` 触发 `tool_call` 事件）。Anthropic-compat provider 同步支持
（`tool_use` 块），覆盖非 gateway 场景。

#### 4.3 编排循环放在 console（Go）

循环三要素（会话、agent 适配器、成本记账）console 都已拥有；cowork 与
harness 提供能力，而不是接管循环。

```
浏览器 ──SSE── console 编排循环 ──► chat.Provider（规划 LLM，原生 tool-calls）
                  │  ▲
      tool_call   ▼  │ tool_result
                  │
   ┌──────────────┼──────────────────┐
   ▼              ▼                  ▼
 run_agent     run_workflow      ask_user(*)
 (本地子进程)   (cowork :11438)    (结束本轮，等用户输入)
```

循环有界：每轮最大工具迭代数（默认 8）、墙钟上限。

#### 4.4 规划模型可用的工具

| 工具 | 后端 | 说明 |
|---|---|---|
| `run_agent(agent, task)` | 现有 `agent.Adapter` | 在该 agent 专属工作区内运行；事件流入会话作为 tool result；预算/超时上限不变 |
| `read_artifact(path)` | agent 工作区内沙箱读 | 限大小；让规划模型自己验收 |
| `list_agents()` | 配置 | 仅列出已检测到的 CLI |
| `run_workflow(workflow_id, params)`（二期） | cowork `POST /api/runs` | console 代理并尾随 cowork 事件；DAG 进度渲染为会话内卡片 |
| `run_status(run_id)`（二期） | cowork / console | 轮询或事件尾随 |

三期候选：`schedule`（定时跑工作流）、`memory`（fusion-memory 检索）、
工作区之外的文件工具（需权限门）。

#### 4.5 fusion-harness 守护编排路径

编排循环的工具调用同样过 sidecar 的 `ToolFunnel.admit()` 语义（lifecycle →
hooks → permission → budget → execute）：12 类危险模式内容扫描、deny-first
权限链、会话预算漏斗都是 harness 本体实现，不是 Go 复制品。派活权限：
`orchestrator.mode`（`off`/`confirm`/`auto`），默认 `confirm` — 任何
`run_agent`/`run_workflow` 执行前需一次点击确认。

#### 4.6 fusion-cowork 集成

- console → cowork 走 HTTP `:11438`，鉴权用 `FUSION_CLIENT_KEY`（env +
  keychain，同 gateway key 模式）；cowork 可选 — 不可达时 `run_workflow`
  返回可见错误，chat 继续可用
- 工作流事件以 `workflow` 类型事件代理进 chat SSE；SPA 渲染 DAG 进度卡片
  （节点/边来自 `GET /api/workflows/{id}`）
- cowork 审批门（`review` 接口）渲染为交互卡片；点击恢复 →
  `POST /api/runs/{id}/resume`

#### 4.7 交互与持久化、agent 专属工作区

- 输入框 "Orchestrator" 开关（默认关，可配默认值）
- 内联卡片：agent 运行卡（实时事件+成本）、工作流卡（DAG 进度）、确认卡
- 成本计入现有成本报表，planner 单列一行
- `messages.meta` 本就是 JSON — 工具调用/结果直接序列化进去
- agent runs 增加 `conversation_id` 列；cowork run id 存消息 meta，支持回放
- **每 agent 固定专属工作区**（评审决定 3）：默认
  `~/.fusion-console/agents/<name>/`，可在 `config.json` 按 agent 覆盖
  （`agents[].workdir`）。编排派活永远在 agent 自己的工作区内执行，互相
  不共享状态；手动运行仍可显式指定目录覆盖。

### 5. 里程碑（v2 顺序）

1. **M1 — harness 进 chat 管线**：sidecar、每次 LLM 调用前压缩、会话级
   预算漏斗、harness 审计日志。验收：100 轮长对话上下文保持连贯且预算内；
   超限可见失败。
2. **M2 — 原生 tool-calls 规划循环 + `run_agent`**：provider 工具调用解析
   （透传已验证）、有界循环、每 agent 专属工作区、UI agent 运行卡、
   ToolFunnel 守护派活。验收：chat 里说"修复 fusion-console 失败的测试"，
   无人值守产出验证过的产物和成本记录。
3. **M3 — cowork 工作流**：`run_workflow`/`run_status`、DAG 进度卡、
   审批/恢复卡。验收：从 chat 发起已保存工作流并拿到结果。
4. **M4（可选）— planner-in-cowork 模式**：把规划循环搬进 cowork 托管的
   agent，适配工作流重度场景；console 保留聊天 UX 并代理事件。M3 上线后看
   使用数据再定。

### 6. 风险 / 待定

- glm-5 原生 tool-calls 已验证可用（2026-11-13 spike）；剩余风险是复杂参数
  的多轮工具调用稳定性 — 迭代上限 + 解析失败可见报错兜底
- sidecar 生命周期（venv 路径、Python 版本）— console 启动时探测
  `fusion_harness` 可导入，失败则降级为无压缩直连并日志告警
- 双份成本记账（规划 token + agent 成本）— 分行记录
- cowork 鉴权模型（client key vs identity JWT）— 先沿用
  `FUSION_CLIENT_KEY`，后续随 fusion-identity 演进
