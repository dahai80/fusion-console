# fusion-console（中文说明）

[English](README.md) | [中文](README_CN.md)

单 Go 二进制的跨平台 Coding Agent 控制台：浏览器瘦客户端（含手机），服务端跑在办公室任一主机（Windows / Ubuntu / macOS）。聊天直连云端 LLM 或本地 fusion-gateway，也可把任务交给 headless coding agent（Claude Code、fusion-code、Codex、OpenCode）。

设计文档见 fusion 主仓 `insight/fusion-console-design-2026-11.md` 与 `insight/fusion-console-remote-access-plan-2026-11.md`。

## 功能

- **聊天**：SSE 流式对话，支持 OpenAI 兼容 / Anthropic 兼容 provider；fusion-gateway（:11432）只是其中一个可选项，不是硬依赖。
- **Agent 会话**：同一聊天 UI 承载 headless agent CLI（`claude -p --output-format stream-json`、`codex exec --json`、`opencode run`、`fusion`）。一个任务进，统一事件流出（文本 / 工具调用 / 结果 / 成本）。
- **配置界面**：provider + baseURL + 模型（从 /v1/models 拉取）；apiKey 存 OS keychain（Keychain / DPAPI / libsecret），绝不写进配置文件。
- **成本账**：每轮入 SQLite，按天按来源出报表。
- **轻量多用户**：每人 bcrypt 口令，HttpOnly session cookie，tailnet 内也必须登录。

## 明确不做

- ❌ 编排 / 任务分解 / issue 队列 — 用 fusion-cowork 或 fusion-agent-studio
- ❌ PTY 交互终端（v2 评估）
- ❌ 公网暴露、SSO、云同步

## 构建

依赖：Go 1.26+、[Bun](https://bun.sh)。

```bash
make web        # 前端构建到 internal/server/webdist（go:embed 嵌入）
make build      # 本机二进制 -> bin/fusion-console
make cross      # darwin/linux/windows 交叉编译
make test       # go test ./...
```

## 运行

> 从 release 下载开始的逐步配置指南：[docs/START_GUIDE.md](docs/START_GUIDE.md)（中英双语）

```bash
./bin/fusion-console            # 监听 127.0.0.1:11480
```

数据目录 `~/.fusion-console`（可用 `FUSION_CONSOLE_DIR` 覆盖）。首次启动在日志打印一次生成的 admin 密码，首次登录后请修改。

浏览器打开 `http://127.0.0.1:11480`，登录后：

1. Settings → 添加 provider（fusion-gateway 端到端配置见下节），并存入 apiKey（keychain 引用 id 与 provider 的 `api_key_id` 一致）。
2. Chat → 选 provider 直接对话；或选 agent + 工作目录，点 agent 按钮把任务交给 CLI。

无 keychain 的 headless 主机：改用环境变量 `FUSION_CONSOLE_APIKEY_<KEY_ID大写>`。

## fusion-gateway 端到端配置

[fusion-gateway](https://github.com/dahai80/fusion-gateway) 是本地混合推理网关（OpenAI 兼容，默认端口 `11432`）。console 把它当一个普通 OpenAI 兼容 provider 接入 — 已用 `glm-5` 端到端真机验证。

### 1. 启动网关

```bash
~/fusion/fusion-gateway/start.sh start     # 监听 0.0.0.0:11432
curl -s http://127.0.0.1:11432/api/health  # 健康检查
```

### 2. 获取（或新建）网关客户端 API key

网关客户端 key 在 `~/fusion/fusion-gateway/config.yaml` 的 `auth.api_keys` 下。选一个 `allowed_models` 覆盖目标模型（如 `glm*`）的 key，或新增：

```yaml
auth:
  api_keys:
    - name: fusion-console
      key: fsx-fusion-console-<随机hex>
      allowed_models: ["glm*", "gpt-4o*", "claude-*"]
      rpm: 60
      tpm: 100000
```

改完重启网关。console 不需要上游 provider 的真实 key — 那些由网关持有。

### 3. 把 key 存入 console

Console UI → Settings → **API key (stored in OS keychain)**：

- key id：`gw-main`（任意稳定 id，这是 keychain 引用名）
- secret：粘贴网关客户端 key（`fsx-...`）

### 4. 添加 gateway provider

同一页面 **Edit / add provider**：

| 字段 | 值 |
|---|---|
| id | `gw-main`（必须与上面的 key id 一致） |
| name | `fusion-gateway` |
| type | `fusion-gateway` |
| base URL | `http://127.0.0.1:11432/v1`（网关在别的机器就填那台机器的 IP） |
| model | `glm-5` |
| enabled | ✓ |

点 **Save provider**，再点 **Fetch models** 拉取网关暴露的模型列表并选择。

### 5. 验证

Chat 页 → 选 `fusion-gateway` provider → 发送任意消息，应看到流式回复和气泡下方的 token 统计。消息与用量持久化在 SQLite，成本报表（`GET /api/costs`）可见。

排错：

- `401 auth_error` — key 错误或网关配置里没有
- `model not allowed` — key 的 `allowed_models` 不含 `glm-5`
- Connection refused — 网关没起，或 console 与网关不同机且防火墙拦截

## 附录：fusion-gateway 侧的模型配置

console 能看到哪些模型，取决于网关暴露了哪些。模型配置全部在 `~/fusion/fusion-gateway/config.yaml`，改完 `~/fusion/fusion-gateway/start.sh restart`。共三处：

### 1. backend — 模型真正跑在哪

`backends:` 把后端名映射到上游端点。每个上游一条；`type` 决定协议（`openai-compatible` 或 `anthropic`）：

```yaml
backends:
  glm-5:                        # 后端名（下面 supply 引用）
    api_key: <上游真实key>       # 真 provider key 放这里，不放 console
    base_url: https://<上游地址>
    enabled: true
    timeout: 300s
    type: anthropic             # 或 openai-compatible
  fusion-mlx:                   # 本地 MLX 引擎就是一个普通 backend
    api_key: mlx-<token>
    base_url: http://127.0.0.1:11434
    enabled: true
    type: openai-compatible
```

### 2. models.supply — 模型名 → 后端

`models.supply:` 声明网关对外提供哪些模型名、由哪些后端服务（列表即故障切换顺序）：

```yaml
models:
  supply:
    glm-5:
      - glm-5                  # 模型 "glm-5" 由后端 "glm-5" 服务
    glm-5.3-flash:
      - volc-coding
    qwen3.5-9b:
      - fusion-mlx             # 本地模型，fusion-mlx 加载后即出现
```

这里的模型名就是 console 模型下拉框显示的名字，也是 provider `model` 字段该填的值。

### 3. routing — 云端模型注册与兜底

`routing.cloud_models:` 登记云端模型名（支持 `claude-*` 通配）；`routing.fallback.model_mapping:` 把别的模型名别名到已登记模型；`routing.fallback.cloud_default:` 是未知云端模型的兜底：

```yaml
routing:
  cloud_models:
    - glm-5
    - glm-5.3-flash
    # ... 新云端模型加这里
  fallback:
    enabled: true
    cloud_default: glm-5       # 未知云端模型落到这
    model_mapping:
      claude-sonnet-4-6: glm-5  # claude 系列请求路由到 glm-5
```

### 4. console 侧验证

```bash
curl -s http://127.0.0.1:11432/v1/models \
  -H "Authorization: Bearer <网关客户端key>" | python3 -m json.tool | grep '"id"'
```

然后 console：Settings → 选 provider → **Fetch models** → 选新模型（或直接在 provider `model` 字段手填）→ 对话。

最小可用路径：网关出厂已配好 `glm-5`（backend `glm-5`）— 只有加新模型才需要上面 1–3 步。

## 远程访问（只走 Tailscale）

见 [deploy/README.md](deploy/README.md)。一句话：办公室主机执行 `tailscale serve --bg 127.0.0.1:11480`，tailnet 内设备访问 `https://<主机名>.<tailnet>.ts.net`。应用层登录始终开启；永不暴露公网。

## 目录

```
cmd/console/          入口
internal/server/      HTTP 路由、SSE、嵌入 SPA
internal/chat/        Provider 抽象（OpenAI 兼容 / Anthropic 兼容 / gateway）
internal/agent/       headless agent 适配器 + 子进程生命周期
internal/config/      配置文件 + OS keychain
internal/auth/        bcrypt 登录 + cookie 会话
internal/store/       SQLite 持久化（会话/消息/成本账）
web/                  React + Tailwind SPA 源码
deploy/               launchd / systemd / schtasks + Tailscale 指南
```

## 许可

Apache License 2.0 — 见 [LICENSE](LICENSE)。
