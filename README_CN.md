# fusion-console

[English](README.md) | [中文](README_CN.md) · [启动指南](docs/START_GUIDE_CN.md) · [手机远程访问](deploy/TAILSCALE_QUICKSTART_CN.md) · [下载](https://github.com/dahai80/fusion-console/releases)

一个给编程 Agent 用的浏览器控制台，整个就是一个 Go 二进制。装在办公室任意一台机器上（macOS、Ubuntu、Windows），电脑浏览器或手机都能打开：和云端或本地大模型聊天，并让对话模型把真正的写代码工作交给这台机器上装好的 Agent 命令行——Claude Code、Codex、OpenCode、fusion-code。

- **一个文件，不用装运行时。** 服务端和网页都打包在里面；数据就是一个 SQLite 文件加一个 JSON 配置。
- **会派活的对话。** 打开 `orchestrate`，你正在聊天的模型就成了 *Brain*：拆任务、派给 Worker、读结果、向你汇报。每次派工都可以先等你批准。
- **真 Agent，真仓库。** Worker 是本机的 Agent 命令行，绑定一个目录、一个 git worktree 或一个 GitHub 仓库，各有预算上限。
- **一个网关，一本账。** 各家模型的 key 放在随包附带的 [fusion-gateway](https://github.com/dahai80/fusion-gateway) 里；console 只在系统钥匙串里存一把网关 key。Brain 和每个 Worker 的 Token 用量都在同一个看板上。
- **默认私有。** 只监听 `127.0.0.1`。远程使用走 Tailscale（可选自建中继），登录始终开启。

## 目录

- [整体结构](#整体结构)
- [快速开始](#快速开始)
- [首次运行向导](#首次运行向导)
- [日常使用](#日常使用)
- [用手机远程访问](#用手机远程访问)
- [编排详解](#编排详解)
- [配置参考](#配置参考)
- [手动给 fusion-gateway 加模型](#手动给-fusion-gateway-加模型)
- [构建与开发](#构建与开发)
- [安全模型](#安全模型)
- [排错](#排错)
- [不做什么](#不做什么)

## 整体结构

```
 浏览器（电脑 / 手机）
        │  远程：tailscale serve 提供的 HTTPS；本机：http://127.0.0.1:11480
        ▼
 fusion-console :11480 ──────────────► fusion-gateway :11432 ──► 云端模型（DeepSeek、通义、
   对话 · Brain · 看板                     一把 key、一本账           Kimi、Claude、OpenRouter…）
   SQLite + config.json                                       └─► 本地引擎（fusion-mlx…）
        │
        └─ 在各自的工作区里拉起 Agent 命令行（Worker）
             claude · codex · opencode · fusion-code
```

可选组件都是点了才装：fusion-rag（知识库）、fusion-code、fusion-mlx（Apple Silicon 本地模型）、fusion-agent-studio（团队）。

## 快速开始

### 方式一：下载发布包（推荐）

到 [Releases](https://github.com/dahai80/fusion-console/releases/latest) 下载对应的包：

| 机器 | 文件 |
|---|---|
| macOS（Apple Silicon 或 Intel） | `fusion-console-<版本>-darwin-universal.dmg` |
| macOS 压缩包 | `fusion-console-<版本>-darwin-arm64.tar.gz` / `-darwin-amd64.tar.gz` |
| Linux x86_64 / arm64 | `fusion-console-<版本>-linux-amd64.tar.gz` / `-linux-arm64.tar.gz` |
| Windows x64 | `fusion-console-<版本>-windows-amd64.zip` |

每个包里有 `fusion-console`、配套的 `fusion-gateway`、harness sidecar、开箱即用的网关 `config.yaml` 和启动脚本。

```bash
# macOS / Linux：解压后在包目录里
./startup.sh          # 启动网关和 console，并打开已登录的浏览器
# Windows：双击 startup.cmd
```

浏览器会直接进入新手引导，大约五分钟就能开始对话。

### 方式二：从源码构建

需要 Go 1.26+ 和 [Bun](https://bun.sh)。

```bash
git clone https://github.com/dahai80/fusion-console && cd fusion-console
make web && make build           # 前端构建到 internal/server/webdist，二进制在 bin/
./scripts/start.sh start --open  # 或者直接 ./bin/fusion-console
```

源码构建不带 `fusion-gateway`：向导里可以一键下载；或者运行 `./scripts/package.sh` 打出和发布包一样的目录。

### 登录

打开 `http://127.0.0.1:11480`，用 `admin` / `fusion-admin-2026` 登录（出厂密码；在本机打开时登录框会自动填好）。想一开始就用自己的密码，首次启动前设置 `FUSION_CONSOLE_ADMIN_PASSWORD`。开放远程访问之前一定要改掉出厂密码，见[安全模型](#安全模型)。

## 首次运行向导

还没配置任何模型时向导会自动打开；之后随时可以从右上角头像菜单进入，菜单里同时显示配置进度。每一步都读取实时状态，重新打开时已完成的会直接打勾，并跳到第一个没完成的步骤。

| 步骤 | 做什么 |
|---|---|
| 1. 启动网关 | 启动（或安装）随包附带的 fusion-gateway，一键给 console 创建网关 API key。key 存进系统钥匙串，并自动添加网关这个模型来源 |
| 2. 接入模型 | 选厂商（DeepSeek、通义/百炼、Kimi、智谱、MiniMax、OpenRouter、Anthropic，或任意 OpenAI 兼容地址），粘贴 API key 和模型名。常见厂商会自动填好 Base URL |
| 3. 试一次 | 发一句很短的测试。模型回答了，就说明网关、key、模型整条链都通 |
| 4. Brain 与 Worker（可选） | 选派工审批方式，查看本机检测到哪些 Agent 命令行，添加 Worker |

最后点 **开始对话**。

## 日常使用

- **对话**：流式输出；在顶部为每个会话切换模型；支持附件，支持的浏览器上可以语音输入。
- **`@Worker`**：点名某个 Worker，任务立刻在后台交给它。任务需要已有仓库时勾选 **Requires Git repository**。
- **`orchestrate`**：输入框旁的开关（默认开），决定 Brain 能不能自己派工。审批模式为“每次派工先问我”时，每次派工会弹出确认卡片。
- **任务栏**：运行中和排队的派工及其阶段；手机上可以收成一个状态气泡。
- **Dashboard**：派工队列、进行中的任务、Worker 可用情况、执行时间线，以及 Brain 和每个 Worker 的 Token 用量（今日 / 近 7 天 / 近 30 天 / 全部，日历、柱状或表格视图）。
- **Agents**：检测到的命令行、Brain 设置和 Worker 列表。
- **Gateway**：网关状态、key、云端后端；**Models** 标签管理模型来源，也可以添加不经网关的 OpenAI / Anthropic 兼容地址。
- **Knowledge Base**：一键安装 fusion-rag，之后对话里多出 `kb_search` / `kb_ask` 工具。
- **Team**：fusion-agent-studio 团队的实时看板，团队可以在 Team 页或对话里创建。
- **更新（macOS）**：主题切换旁的更新按钮安装新版本，见 [deploy/README.md](deploy/README.md#macos-user-installation-with-updates)。

## 用手机远程访问

fusion-console 从不开放公网端口。在办公室外使用：

1. 办公室机器和手机都装 Tailscale，登录同一个账号。
2. 在 tailnet 内发布 console：`tailscale serve --bg 11480`。
3. 手机打开 `https://<机器名>.<tailnet>.ts.net` 并登录。

手机在运营商 NAT 后面打不通洞时，流量会走中继。Tailscale 官方共享中继从大陆访问常常很慢，所以指南里也写了怎么用一个脚本 [`deploy/tailscale/setup-derp.sh`](deploy/tailscale/setup-derp.sh) 在一台香港小 VPS 上自建中继（DERP）。

第一次配置看小白版：**[deploy/TAILSCALE_QUICKSTART_CN.md](deploy/TAILSCALE_QUICKSTART_CN.md)**；完整步骤：**[deploy/TAILSCALE_CN.md](deploy/TAILSCALE_CN.md)**。

## 编排详解

**Brain。** `orchestrate` 打开后，对话模型获得三个原生工具——`list_agents`、`run_agent`、`read_artifact`——以及每轮最多 8 次迭代的规划循环。Brain 有两种模式（Agents → Workers → Brain）：

- *对话模型*（默认）：规划提示词里带上工作区配置，以及最多 12 KB 的 `brain_context_files`（每行一个路径：工作规范、仓库清单）。
- *claude code*：每个编排轮次在 Brain 工作目录里跑一个 `claude` 会话，通过 MCP（`fusion-console mcp`）拿到同样的派工工具；它还会读取该目录下的 `CLAUDE.md`。

Brain 工作目录（默认 `~/.fusion/fusion-console/agents`）是 `read_artifact` 的沙箱，也是 Worker worktree 的父目录。它可以绑定一个 GitHub 地址（clone 下来，每次派工前 fast-forward，本地改动绝不覆盖）或一个本地仓库。

**Worker。** 每个 Worker = 一个 Agent 命令行 + 一个工作区，三种模式：

| 模式 | 行为 |
|---|---|
| `standalone` | 每个 Worker 一个固定目录，跨任务共用 |
| `worktree` | 每次派工从白名单里的本地仓库新建一个 detached `git worktree` |
| `github` | 绑定仓库地址：首次派工 clone，之后 fetch 并 fast-forward；有未提交改动或本地领先提交时只报告，不覆盖 |

Worker 和仓库绑定：派给不属于它的仓库会被拒绝。每个 Worker 可以指定模型来源和模型（console 从钥匙串取 key，注入 `ANTHROPIC_*` / `OPENAI_*` 环境变量），默认把笔记记在 `NOTES.md`（也可以用 fusion-memory 或关闭）。

**护栏。** 每个 Worker 可单独停用，各有预算上限；后台任务共用一个有界的执行池（默认同时 4 个），单次最长 48 小时；每次派工都经过 fusion-harness 工具准入链，sidecar 不可用时直接拒绝（fail-closed）。审批模式：`confirm`（每次先问）、`auto`（预算内自动）、`off`（不给工具）。任务缺少前置条件时可以用最终回复 `{"status":"blocked","reason":"…"}` 上报，派工卡片显示 **blocked**，不会误报成功。

**Harness sidecar。** 防护由 `sidecar/harnessd.py` 调用 Python 版 fusion-harness。console 依次查找 `FUSION_CONSOLE_HARNESS_PYTHON`、安装目录和工作区的虚拟环境、PATH 里的 `python3` / `python`。`config.json` 里开启 `harness.enabled` 时，sidecar 起不来，对话会在调用模型前停下。内核 0.22 支持防护检查和预算报告；0.23 起增加模型调用指标和失败归因。

**记账。** 规划轮次记为 `chat:<来源>`（或 `brain:claude`），派出的任务记为 `agent:<名字>`。失败按 MAST 分类打标（`fc1_brief`、`fc2_handoff`、`fc3_tool`），可在 Dashboard → Failures 导出 JSONL。

## 配置参考

**文件。** 数据目录 `~/.fusion/fusion-console/`（用 `FUSION_CONSOLE_DIR` 改）：`config.json`（用户、模型来源、Agent、编排设置，不含任何 key）、`console.db`（SQLite）、`log/`。

**环境变量**

| 变量 | 用途 |
|---|---|
| `FUSION_CONSOLE_DIR` | 数据目录 |
| `FUSION_CONSOLE_ADMIN_PASSWORD` | `admin` 的初始密码，只在第一次启动时读取 |
| `FUSION_CONSOLE_APIKEY_<KEY_ID>` | 某个钥匙串 id 对应的 key（大写，非字母数字换成 `_`，如 `..._GATEWAY_KEY`），给没有钥匙串的无界面主机用 |
| `FUSION_CONSOLE_HARNESS_PYTHON` | 能 `import fusion_harness` 的 Python 解释器 |
| `FUSION_STUDIO_SOCKET` | agent-studio 守护进程 socket（默认 `/tmp/fusion-studio.sock`） |
| `FUSION_CODE_DOWNLOAD_URL` | fusion-code 安装器的备用下载地址 |

**端口**（全部只绑定 `127.0.0.1`）

| 端口 | 服务 |
|---|---|
| 11480 | fusion-console |
| 11432 | fusion-gateway |
| 11434 | fusion-mlx（可选） |
| 11436 | fusion-rag（可选） |
| 11441 / 11482 | fusion-code API / 网页（可选） |

**命令行。** `fusion-console` 启动服务，并用一次性登录链接打开浏览器；无界面主机加 `-open=false`。`fusion-console mcp` 运行 MCP stdio Brain 服务。

**开机自启。** launchd（macOS）、systemd（Linux）、任务计划程序（Windows）的做法见 [deploy/README.md](deploy/README.md)。

## 手动给 fusion-gateway 加模型

向导和 Gateway 页覆盖了常见情况。其他情况编辑网关的 `config.yaml`（在网关二进制旁边），然后重启网关。

```yaml
backends:                        # 模型实际在哪里跑
  deepseek:
    type: deepseek               # 或 openai-compatible、anthropic、zhipu、dashscope…
    base_url: https://api.deepseek.com   # 不要带 /v1，网关会自己补
    api_key: sk-...              # 真正的厂商 key 只放在这里，不进 console
    enabled: true

models:
  supply:                        # 模型名 -> 后端（列表顺序即故障转移顺序）
    deepseek-chat: [deepseek]

routing:
  cloud_models: [deepseek-chat]  # 云端模型白名单（支持 claude-* 这类通配）
  fallback:
    enabled: true
    cloud_default: deepseek-chat # 请求了未知云端模型时落到这里
```

看 console 能看到哪些模型：

```bash
curl -s http://127.0.0.1:11432/v1/models -H "Authorization: Bearer <网关key>" | grep '"id"'
```

## 构建与开发

```bash
make web      # 用 bun 构建前端到 internal/server/webdist（go:embed）
make build    # 本机的 bin/fusion-console
make cross    # darwin / linux / windows，amd64 + arm64
make test     # go test ./... -count=1
make vet fmt
./scripts/package.sh vX.Y.Z   # 打发布包，带上配套的 fusion-gateway
```

`internal/server/webdist` 是提交进仓库的构建产物：`go build` 之前先 `make web`，否则二进制里是旧界面。

```
cmd/console/        入口（服务、mcp 子命令）
internal/server/    HTTP 路由、SSE、编排、派工、内嵌前端
internal/chat/      模型来源：OpenAI 兼容、Anthropic 兼容、网关
internal/agent/     Agent 命令行适配和子进程生命周期
internal/harness/   fusion-harness sidecar 客户端
internal/config/    config.json 与系统钥匙串
internal/auth/      bcrypt 登录、cookie 会话
internal/store/     SQLite（会话、消息、成本）
sidecar/            harnessd.py
web/                React + Tailwind 源码
deploy/             launchd / systemd 配置、远程访问指南、中继脚本
docs/               启动指南、设计笔记
```

## 安全模型

- 服务只绑定 `127.0.0.1:11480`，不对公网暴露；不要对它用 `tailscale funnel` 或端口转发。
- 每个请求都要登录，tailnet 内也一样。密码以 bcrypt 哈希存在 `config.json`。
- 远程使用前**改掉出厂密码**：`htpasswd -bnBC 10 "" '新密码' | tr -d ':\n'` 生成哈希，填进 `config.json` 里 `admin` 的 `password_hash`，重启。加用户同理。
- API key 只在系统钥匙串（Keychain / DPAPI / libsecret）或 `FUSION_CONSOLE_APIKEY_*` 里，从不写进配置文件。
- Agent 只在声明的工作区里运行，有预算上限；从手机派工时建议保持“每次派工先问我”。

## 排错

| 现象 | 处理 |
|---|---|
| 忘了 console 密码 | 按安全模型一节生成新哈希写进 `config.json` 并重启；或删掉 `config.json` 恢复出厂 admin（其他设置也会一起清空） |
| 向导里创建 API key 失败 | 网关管理员密码被改过：在向导的帮助区保存新的管理员账号密码后重试 |
| 网关返回 `401 auth_error` | 网关 key 错了或被删了，从头像菜单重新创建 |
| `model not allowed` / 找不到模型 | 网关里没有这个模型，或名字拼错；必须和厂商文档完全一致 |
| 后端加上了但模型一直不出现 | Base URL 不对（不要带 `/v1`）、key 不对，或这台机器访问不到厂商 |
| Agent 灰显 | 命令行不在 PATH 里；安装它，或在 `config.json` 里把 `command` 写成绝对路径 |
| Agent 一启动就退出 | 工作目录不对，或这个命令行需要先在本机登录 |
| Worker 跑到奇怪的 `～/…` 目录 | 中文输入法把 `~` 变成了全角 `～`；工作目录请粘贴绝对路径 |
| Worker 卡住没输出 | 无人值守时没人回答确认提示；给这类 Worker 用 `permission_mode: bypassPermissions`，靠预算上限兜底 |
| 手机上复制按钮没反应 | 页面走的是普通 HTTP；按远程访问指南用 `tailscale serve --bg 11480`（HTTPS） |

## 不做什么

刻意不做：多层 DAG 编排和 issue 队列（用 fusion-cowork 或 fusion-agent-studio；这里的 Brain 只做一层派工）、交互式 PTY 终端（v2 再评估）、公网暴露、SSO 和云同步。

## 许可

Apache License 2.0，见 [LICENSE](LICENSE)。
