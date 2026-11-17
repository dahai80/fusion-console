# 大脑（Orchestrator）优化方案

日期：2026-11-17 · 状态：**待人工确认**（确认后才分工派活）
范围：仅 fusion-console 仓库；上游（agent-studio 等）只提 issue 不改代码
约束：代码落地后**不重启服务**，等用户发话再重启

## 一、背景与证据（本日实测观察）

| # | 现象 | 根因 | 证据 |
|---|------|------|------|
| 1 | 多个 worker 派活全串行，后台 run 也排队等全局锁 | `agentRuns sync.Mutex` 全局一把锁，一次只允许一个 agent 子进程 | dispatch.go / orchestrator.go 共用；后台 run #1 等 #2 |
| 2 | 大脑把 repo 传成本地路径被守卫拒绝（今日两次） | orchestratorPrompt 无 worker↔repo 路由表，大脑靠猜 | run #1 "worker bound to repo" 拒绝；fusion-site #76 首派失败 |
| 3 | 后台 run 无法取消，UI 只能干等 30min 超时 | registry 无 cancel 路径，前端无按钮 | DispatchPanel 只有 copy/dismiss |
| 4 | 大脑每轮 tokens_in 4~6 万 | orchestratorPrompt + 65 个 worker 全量列表 + 历史每轮重发 | 日志 `tokens_in=58610 / 46708 / 43395` |
| 5 | run 结果只有一段文本 | finishDispatch 只回 `final string`，无 files/commits 结构 | summarizeDispatch |
| 6 | worker 失败后大脑盲目重派 | 无失败分类反馈、无重试预算控制 | fusion-health clone 失败后反复重派 |

## 二、优化项（P0 先做，P1 次之，P2 择机）

### P0-1 并行执行：全局锁 → 信号量（maxConcurrentAgents）
- 现状：`agentRuns sync.Mutex` 串行所有 worker 子进程
- 方案：`channel` 信号量，容量可配置（config `orchestrator.max_concurrent_agents`，默认 3）；per-run context 支持单独 cancel
- 风险：多 claude 进程并发 = token 花费并发上涨（预算上限已按 worker 各自 $20 控制，可接受）；内存/CPU 需上限
- 工作量：后端 1 处改造 + 配置项 + 测试；前端无需改
- 验收：同时派 2 个后台 run，两个同时 running（Dispatches 面板两条转圈）

### P0-2 worker 路由表注入大脑 prompt
- 方案：orchestratorPrompt 增加一段显式路由表：`worker 名 → 绑定 repo URL + work_dir 绝对路径 + isolation`，并明确"repo 参数优先传 URL，传本地路径也可以（已归一化）"
- 收益：消除派错 repo 一整类失败；大脑不再猜
- 代价：prompt 增加约 65 行 × ~20 token ≈ 1.3k token/轮；与 P1-2 瘦身对冲
- 工作量：prompt 生成函数一处 + 测试
- 验收：新会话说"让 fusion-web-shell 修 issue #163"，run_agent 参数 repo 为 URL 或路径均成功

### P0-3 后台 run 可取消
- 后端：`POST /api/dispatches/{id}/cancel` → 按	run 记录的 `context.CancelFunc` 取消（queued 直接置 error "cancelled"，running 走 ctx.Done → 子进程组 kill 已有）
- 前端：running/queued 卡加停止按钮
- 工作量：registry 存 cancelFn（现有 ctx 传递改造小）+ 前端按钮
- 验收：取消 running run 后 5s 内状态变 error，子进程消失（ps 验证）

### P1-1 大脑上下文瘦身
- worker 列表：65 个全量 → 大脑 prompt 只给名字+一句话能力；详细配置走 `list_agents` 按需取（工具已有）
- 历史：orchestrator 轮次内已完成 run 的 task 全文截断为 200 字符摘要（现在可能整段回传）
- 验收：同类对话 tokens_in 下降 ≥30%

### P1-2 run 结果结构化回传
- finishDispatch 返回 `{summary, files_changed[], commits[], cost, tokens}`：从 worker 工作区 `git status --porcelain` / `git log origin..HEAD` 采集（有 .git 时）
- summarizeDispatch 渲染文件清单与 commit 列表，便于验收
- 验收：修复类任务完成消息列出改动文件与 commit

### P2（确认 P0/P1 后再排）
- 失败分类重试：fc3_tool 类失败（网络等）自动重试 1 次，带退避；fc1/fc2 不重试
- 大脑自身预算熔断：单会话 orchestrator 轮次 token 上限
- run 优先级：用户前台派活插队后台队列

## 三、分工建议（确认后执行）

| 项 | 派给 | 说明 |
|----|------|------|
| P0-1 并行执行 | fusion-console worker（claude） | 后端单点改造，改动集中在 dispatch.go/orchestrator.go |
| P0-2 路由表 | fusion-console worker | prompt 生成 + 测试，改动小 |
| P0-3 取消 | fusion-console worker | 前后端联动，与 P0-1 有文件交集 → 串行派 |
| P1-1 / P1-2 | fusion-console worker | P0 落地后第二批 |

执行顺序：P0-2 → P0-1 → P0-3（避免同文件冲突）；每个完成即跑 `go test ./... + bun run check`，全绿再派下一个。

## 四、不做的事

- 不改 fusion-agent-studio / fusion-harness（上游边界）
- 不动持久化 schema（dispatch_runs 已够用）
- 期间不重启服务；所有改动 commit 后等用户统一重启

## 五、待确认问题

1. `max_concurrent_agents` 默认值：3 还是 5？（并发 = 并发 claude 进程数 = 并发 token 消耗）
2. P0-2 路由表全量 65 worker 注入 vs 只注入 enabled 的（建议后者）
3. P1-2 结构化结果是否要写入 fusion-memory（worker memory）？（建议暂不，先控制范围）
