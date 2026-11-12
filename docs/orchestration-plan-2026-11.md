# fusion-console Orchestration Plan — Chat as the Main Brain

Status: proposal (no code yet)
Date: 2026-11-12
Owner: dahai80

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

#### 4.2 Tool protocol: JSON, not provider tool-calls

Phase 1 uses a prompt-declared JSON tool protocol (the planner replies with a
fenced `{"tool": ..., "args": {...}}` block), parsed with the same strict
tolerance rules as the rest of the fusion stack. Rationale:

- Provider-agnostic: works with glm-5 through the gateway regardless of
  whether tool-call passthrough is supported end to end
- No new provider surface; SSE event schema unchanged
- Phase 3 can add native OpenAI `tools` passthrough as an optimization once
  the gateway path is verified — same tool set, different wire format

#### 4.3 Tools exposed to the planner (phase 1)

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

#### 4.4 fusion-harness integration (Go side)

Console does not import the Python kernel. It conforms to the same behavior
contract (`fusion-harness/docs/contract.md`) in Go, mirroring how fusion-code
(TypeScript) stays compatible:

- Budget funnel: per-run cap exists; add per-conversation and per-day caps,
  enforced before each tool execution, kill on breach (fail visible)
- Permission engine: planner tool allow-list per conversation
  (`orchestrator.mode` in config: `off` / `confirm` / `auto`); `confirm`
  (default) requires one click before any `run_agent` / `run_workflow`
- Compaction: tool transcripts larger than N KiB are summarized into the
  tool result before re-entering the planner context
- Contract parity tests in Go against the same fixtures the harness uses

#### 4.5 fusion-cowork integration

- Console → cowork over HTTP `:11438` with `FUSION_CLIENT_KEY` (env, same
  keychain pattern as gateway keys); cowork is optional — when unreachable,
  `run_workflow` returns a visible error and chat continues without it
- Workflow run events are proxied into the chat SSE stream as a typed
  `workflow` event; the SPA renders a DAG progress card (nodes/edges from
  `GET /api/workflows/{id}`)
- Approval gates (cowork `review` endpoints) surface as interactive cards;
  user clicks resume → console calls `POST /api/runs/{id}/resume`

#### 4.6 UX

- Composer toggle: "Orchestrator" (off by default; config default)
- Inline cards: agent run card (live text/tool events, cost), workflow card
  (DAG progress), confirmation card (mode `confirm`)
- All costs roll into the existing cost report with a `planner` row

#### 4.7 Persistence

- `messages.meta` already JSON — tool calls/results serialize there
- Agent runs gain a `conversation_id` column; cowork run ids are stored in
  the message meta for resume/replay

### 5. Milestones

1. **M1 — planner loop + `run_agent`**: JSON tool protocol in Go providers,
   orchestrator loop with iteration/budget caps, agent run card in UI.
   Success: in chat, "fix the failing test in ~/fusion/fusion-console" ends
   with a verified artifact and a cost row, hands-free.
2. **M2 — cowork workflows**: `run_workflow` / `run_status`, DAG progress
   card, approval/resume cards. Success: a stored workflow runs from chat and
   its result lands in the conversation.
3. **M3 — harness-grade guardrails**: per-conversation budget funnel,
   permission modes, transcript compaction, contract parity tests.
4. **M4 (optional) — planner-in-cowork mode**: move the planner loop into a
   cowork-hosted agent (it already imports harness) for workflow-heavy
   setups; console keeps the chat UX and proxies events. Decide after M2
   usage data.

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

### 4. 设计

#### 4.1 编排循环放在 console（Go）

循环三要素（会话、agent 适配器、成本记账）console 都已拥有，所以循环留在
console；cowork 与 harness 提供能力，而不是接管循环。

```
浏览器 ──SSE── console 编排循环 ──► chat.Provider（规划 LLM）
                  │  ▲
      tool_call   ▼  │ tool_result
                  │
   ┌──────────────┼──────────────────┐
   ▼              ▼                  ▼
 run_agent     run_workflow      ask_user(*)
 (本地子进程)   (cowork :11438)    (结束本轮，等用户输入)
```

`ask_user` 结束本轮，用户下一条消息继续循环。循环有界：每轮最大工具迭代数
（默认 8）、会话级预算漏斗（扩展现有单次 cap）、墙钟上限。

#### 4.2 工具协议：JSON，而非 provider 原生 tool-calls

一期用提示词声明的 JSON 工具协议（规划模型回复 fenced
`{"tool": ..., "args": {...}}` 块），按全栈统一的严格容错规则解析。理由：

- 与 provider 无关：不依赖 gateway 是否端到端透传 tool-calls
- 不新增 provider 面；SSE 事件结构不变
- 三期可在验证 gateway 透传后加原生 `tools` 支持 — 同一套工具，换线格式

#### 4.3 一期暴露给规划模型的工具

| 工具 | 后端 | 说明 |
|---|---|---|
| `run_agent(agent, task, workdir)` | 现有 `agent.Adapter` | 返回 run id；事件流入会话作为 tool result；预算/超时上限不变 |
| `read_artifact(path)` | run 工作目录内沙箱读 | 限大小；让规划模型自己验收 |
| `list_agents()` | 配置 | 仅列出已检测到的 CLI |

二期增加：

| 工具 | 后端 | 说明 |
|---|---|---|
| `run_workflow(workflow_id, params)` | cowork `POST /api/runs` | console 代理并尾随 cowork 事件；DAG 进度渲染为会话内卡片 |
| `run_status(run_id)` | cowork / console | 轮询或事件尾随 |

三期候选：`schedule`（定时跑工作流）、`memory`（fusion-memory 检索）、
工作目录之外的文件工具（需权限门）。

#### 4.4 fusion-harness 集成（Go 侧）

console 不 import Python 内核，而是按同一行为契约
（`fusion-harness/docs/contract.md`）在 Go 内对齐 — 与 fusion-code（TS）的
做法一致：

- 预算漏斗：单次 cap 已有；增加会话级与按日 cap，每次工具执行前检查，超限
  熔断且失败可见
- 权限引擎：按会话的规划工具白名单（`orchestrator.mode`：`off`/`confirm`/`auto`）；
  默认 `confirm` — 任何 `run_agent`/`run_workflow` 执行前需要一次点击确认
- 压缩：超过阈值的工具输出先摘要再回到规划上下文
- 用 harness 同款 fixture 写 Go 契约对齐测试

#### 4.5 fusion-cowork 集成

- console → cowork 走 HTTP `:11438`，鉴权用 `FUSION_CLIENT_KEY`（env +
  keychain，同 gateway key 模式）；cowork 可选 — 不可达时 `run_workflow`
  返回可见错误，chat 继续可用
- 工作流事件以 `workflow` 类型事件代理进 chat SSE；SPA 渲染 DAG 进度卡片
  （节点/边来自 `GET /api/workflows/{id}`）
- cowork 审批门（`review` 接口）渲染为交互卡片；点击恢复 →
  `POST /api/runs/{id}/resume`

#### 4.6 交互

- 输入框 "Orchestrator" 开关（默认关，可配默认值）
- 内联卡片：agent 运行卡（实时事件+成本）、工作流卡（DAG 进度）、确认卡
- 成本计入现有成本报表，planner 单列一行

#### 4.7 持久化

- `messages.meta` 本就是 JSON — 工具调用/结果直接序列化进去
- agent runs 增加 `conversation_id` 列；cowork run id 存消息 meta，支持回放

### 5. 里程碑

1. **M1 — 规划循环 + `run_agent`**：Go 侧 JSON 工具协议、有界编排循环、
   UI agent 运行卡。验收：在 chat 里说"修复 fusion-console 里失败的测试"，
   无人值守产出验证过的产物和成本记录。
2. **M2 — cowork 工作流**：`run_workflow`/`run_status`、DAG 进度卡、审批/恢复卡。
   验收：从 chat 发起已保存工作流并拿到结果。
3. **M3 — harness 级护栏**：会话预算漏斗、权限模式、压缩、契约对齐测试。
4. **M4（可选）— planner-in-cowork 模式**：把规划循环搬进 cowork 托管的
   agent（它已 import harness），适配工作流重度场景；console 保留聊天 UX 并
   代理事件。M2 上线后看使用数据再定。

### 6. 风险 / 待定

- glm-5 对 JSON 工具协议的稳定性 — 严格解析 + 一次修复重试 + 迭代上限兜底
- gateway tool-call 透传未验证 — 一期刻意绕开
- 双份成本记账（规划 token + agent 成本）— planner 用量已有 usage 事件，分行记录
- cowork 鉴权模型（client key vs identity JWT）— 先沿用 `FUSION_CLIENT_KEY`，
  后续随 fusion-identity 演进
