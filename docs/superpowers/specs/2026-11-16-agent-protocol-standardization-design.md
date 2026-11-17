# fusion-console Agent 接入标准化设计（A+B 方案）

日期：2026-11-16 ｜ 状态：待批准 ｜ 上游联动：AtlasCode issue [#2](https://github.com/vincentlau2046/AtlasCode/issues/2)（已采纳，排期 0.1.120）

## 背景与动机

fusion-console 目前接入 headless coding agent 靠两处硬编码：adapter 注册（`internal/server/server.go:50-51`，四个零尺寸 struct）和安装菜单（`internal/server/codeagents.go`，三个条目）。问题：

1. 接入任何新 CLI（如 atlascode，`@atlasharness/atlascode`，claude stream-json 同构实现）都要改 Go 代码；
2. CLI 升级引发的协议漂移是**静默劣化**：字段挪走 → 费用变 0 / tool 事件消失，预算熔断随之失效，用户无感知；
3. atlascode 0.1.x 的 `result` 事件缺 `usage`/`total_cost_usd`（上游已采纳补齐提案，0.1.120 落地）——过渡期内 console 需要自愈手段。

## 目标 / 非目标

**目标**
- atlascode 开箱可用：token 统计完整（含 cache），费用缺失时 UI 明示、熔断不失效；
- 协议族成员（claude-stream-json 同构 CLI）通过**纯配置**接入，零 Go 代码；
- 静默劣化变为显式信号（哨兵 + 版本指纹）；
- console 代装的 CLI 锁定 known-good 版本，升级显式化。

**非目标**
- 不做进程外/插件式 adapter（违背单二进制、cgo-free 边界）；
- 不做 orchestration 扩展、不接管用户自装 CLI 的升级（只警告不阻止）。

## 备选方案与取舍

| 方案 | 取舍 | 结论 |
|---|---|---|
| A. 保持薄解析器 + 协议护栏 | 架构不动，改动小；护栏补齐漂移可见性 | ✅ 采纳 |
| B. `protocol` 字段配置化接入 | 新同构 CLI 零代码接入；控制代码增长但不降低漂移风险，需 A 配合 | ✅ 采纳（A+B 组合） |
| C. 进程外 sidecar adapter | 兼容性最好但复杂度高，违背设计哲学 | ❌ 放弃 |

## 设计

### 1. 协议族抽象（方案 B）

**config.json**（`internal/config`）：`AgentSettings` 增加字段，全部向后兼容：

```jsonc
{
  "name": "atlascode",
  "command": "atlascode",
  "protocol": "claude-stream-json",   // 新增；空 = 按 name 走 legacy 映射
  "permission_mode": "acceptEdits",
  "max_budget_usd": 5,
  "max_budget_tokens": 2000000,       // 新增；费用缺失时的熔断上限
  "enabled": true
}
```

protocol 取值：`claude-stream-json` | `codex-jsonl` | `opencode-jsonl`。

**adapter 解析**：`server.go` 的硬编码列表改为按 settings 构造：
- `protocol` 非空 → 按 protocol 选 adapter 族；
- 为空 → 按 name 走 legacy 映射（claude/fusion-code→claude-stream-json，codex→codex-jsonl，opencode→opencode-jsonl），**现有 config.json 零迁移成本**。

adapter 结构从零尺寸 struct 改为参数化：`ClaudeCompatAdapter{Name, Command string}`，fusion-code/atlascode 都是它的实例；codex/opencode 同理。解析器（`parseClaudeJSON` 等）保持包级函数不变。

**内置 agent 补齐**：`DefaultAgents` 只在首次运行生效，老配置永远拿不到新内置项。增加 additive merge：config 加载时，按 name 缺失的内置 agent（带 `builtin: true` 标记）追加到 `agents` 数组。用户改过/禁用的条目不被覆盖。新增内置项：`atlascode`（protocol=claude-stream-json，command=atlascode，permission_mode=acceptEdits）。

**安装菜单**（`codeagents.go`）：增加第四条 `atlascode`（LookPath 检测 + `--version` 详情，npm 包 `@atlasharness/atlascode`）。

### 2. claude-stream-json 解析器扩展（usage 累计回退）

`result` 事件的 usage 优先级（与上游 0.1.120 终裁口径一致）：
1. `result.usage` 存在且非零（claude / atlascode ≥0.1.120）→ 直接采用；
2. 否则用本次 run 内**从 assistant 消息 `message.usage` 累计**的值（input/output/cache_creation/cache_read 四项求和）。

累计状态放 `processHandle`（parser 保持逐行无状态），`done` 时归零。`Event` 增加可选字段 `TokensCacheRead/TokensCacheWrite int`；SQLite cost 行加两列（可空，旧行补 0，additive migration）。

**费用缺失语义**：`total_cost_usd` 缺失 → `CostUSD=0`，且该 run 标记 `cost_unavailable`：
- 预算熔断退化：`max_budget_tokens` 生效（默认 2,000,000，含 input+output，cache_read 不计），超限 kill + 显式 error（"费用数据不可用，按 token 上限熔断"）；
- UI 运行详情显示"该 CLI 版本无费用数据"。

### 3. 协议护栏（方案 A）

**a. 运行后哨兵**（`process.go` 流末尾）：
- 有效事件数 = 0（无 text/tool/result）且退出码 0 → `EventError`："疑似协议漂移（CLI v<version>）：输出流可解析事件为 0"；
- 有效事件 >0 但 usage 事件缺失 → `EventNotice`（新事件类型，SSE 透传，UI 弱化显示）："本次运行无用量数据"。

**b. 版本指纹**：run 启动时 `--version`（同 command+路径结果缓存 10 分钟），与上次成功 run 记录的版本比对；变化 → `EventNotice`："CLI 版本变更 X→Y，建议运行冒烟探测"。版本号写入 run/cost 行（新列 `cli_version`）。

**c. 冒烟探测**：`POST /api/agent/probe`（per agent）——跑最小 headless 任务（"Reply with the single word OK."，60s 超时），校验 ≥1 个 text/result 事件并报告 usage/cost 可用性；结果按（command, version）缓存。`/api/agents` 响应带 `protocol`、`cli_version`、`last_probe`。UI：agent 卡片加"探测"按钮。

**d. 版本锁定**：`startNpmInstall` 从内置 known-good 表锁版本安装（`pkg@version`），菜单提供显式"升级"按钮（解开锁重装）。用户自装的 CLI 不干预，仅靠 b 的指纹警告。

### 4. 测试

- **fixtures**：录一份真实 atlascode 0.1.101 stream-json 输出 → 解析器测试（usage 累计路径 + result 无 cost 路径 + 未知事件忽略）；上游 0.1.120 发布后补 result.usage 路径 fixture；
- 哨兵：合成流三例（零有效事件 / 无 usage / 正常）；
- 版本指纹：mock `--version` 变更触发 Notice；
- probe：fake CLI 脚本（httptest 层）；
- config 迁移：老 config（无 protocol/atlascode）加载后 builtin 补齐且用户字段不丢；
- **E2E**：真实 atlascode 在临时 workdir 跑一个任务，验证 artifact + token 报表 + "无费用数据"提示 + token 熔断。

### 5. 实施顺序

1. 解析器扩展 + usage 累计 + Event/cache token 字段 + fixtures（纯 `internal/agent`，无 UI）；
2. protocol 字段 + adapter 参数化重构 + config builtin merge + atlascode 接入；
3. 哨兵 + 版本指纹（`process.go` + 存储列）；
4. 冒烟探测 API + UI 按钮 + EventNotice 前端呈现；
5. 安装菜单第四项 + npm 版本锁定；
6. E2E 全链 + `make test`/`vet`。

每步独立可验证，1-2 步落地后 atlascode 即可用（3-5 为护栏增强）。

### 6. 已裁决点（2026-11-16 用户确认）

1. `max_budget_tokens` 默认 2,000,000；
2. `EventNotice` 作为新 SSE 事件类型进前端（弱化呈现）；
3. npm 锁版本的 known-good 表内置代码；
4. **atlascode 的 provider 强制不走 env 注入**（0.1.120 实测：settings.json 已有 provider 时 `OPENAI_BASE_URL`/`OPENAI_API_KEY` 被忽略；`ATLAS_<ROLE>_*` env 虽可用但用户裁定不用）——改为**合并写入 `~/.atlas/settings.json`**：console 按 provider 配置生成 `providers.<id>`（baseURL 含 /v1、api=openai-completions、models 带 id）与 `modelRoles.*`（完整引用 `provider/model`，实测裸 id 不做归一化），原子写 + `.bak` 备份 + 幂等合并（保留用户其它条目），在带 provider_id 的 agent run 前执行；
5. 外部模型配置粒度：`Provider` 增加 `models`（池，字符串数组，`model` 作为缺省），`AgentSettings` 增加 `model` 覆盖（per-agent 缺省模型），run 请求可临时指定 model——满足"只配 openai url/api key/模型即可接入"。

### 7. 实测勘误（2026-11-16，atlascode 0.1.120 本机实跑）

- 0.1.120 的 `result` 事件已带 claude 标准 `usage` 四字段（issue #2 随版落地）；`total_cost_usd` 价格未知时缺省（诚实缺失设计生效）；
- 角色池裸 id **不**做 provider 归一化（README 与代码不符），必须写完整引用 `provider/model`；
- `baseURL` 必须含 `/v1`（OpenAI SDK 拼接风格）；
- 真实成功 fixture 已录：`input_tokens/output_tokens/cache_read_input_tokens/cache_creation_input_tokens` 齐全。
