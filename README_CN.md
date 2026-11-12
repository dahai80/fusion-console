# fusion-console（中文说明）

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

```bash
./bin/fusion-console            # 监听 127.0.0.1:11480
```

数据目录 `~/.fusion-console`（可用 `FUSION_CONSOLE_DIR` 覆盖）。首次启动在日志打印一次生成的 admin 密码，首次登录后请修改。

浏览器打开 `http://127.0.0.1:11480`，登录后：

1. Settings → 添加 provider（如 type=gateway、baseURL=`http://127.0.0.1:11432/v1`、model=`glm-5`），并存入 apiKey（keychain 引用 id 与 provider 的 `api_key_id` 一致）。
2. Chat → 选 provider 直接对话；或选 agent + 工作目录，点 agent 按钮把任务交给 CLI。

无 keychain 的 headless 主机：改用环境变量 `FUSION_CONSOLE_APIKEY_<KEY_ID大写>`。

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
