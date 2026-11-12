# fusion-console Start Guide (from release download)

Install and run fusion-console without building anything — everything below
uses the prebuilt packages from the
[releases page](https://github.com/dahai80/fusion-console/releases).

[English](#english) | [中文](#中文)

---

## English

### 1. Download

From [v0.1.0](https://github.com/dahai80/fusion-console/releases/tag/v0.1.0),
pick the package for your office host:

| Host | File | Contains |
|---|---|---|
| macOS (Apple Silicon / Intel) | `fusion-console-0.1.0-darwin-universal.dmg` | universal binary + docs |
| Windows x64 | `fusion-console-0.1.0-windows-amd64.zip` | `fusion-console.exe` + docs |
| Ubuntu x64 | `fusion-console-0.1.0-linux-amd64.tar.gz` | binary + systemd unit + docs |

### 2. Install

#### macOS

Open the DMG, copy the `fusion-console` folder anywhere (e.g.
`/usr/local/bin` or `~/bin`):

```bash
hdiutil attach fusion-console-0.1.0-darwin-universal.dmg
sudo cp /Volumes/fusion-console/fusion-console/fusion-console /usr/local/bin/
hdiutil detach /Volumes/fusion-console
```

#### Windows

Unzip `fusion-console-0.1.0-windows-amd64.zip` to a folder, e.g.
`C:\Tools\fusion-console\`. The only file you need is `fusion-console.exe`.

#### Ubuntu

```bash
tar xzf fusion-console-0.1.0-linux-amd64.tar.gz
sudo mv fusion-console /usr/local/bin/
sudo mv fusion-console.service /etc/systemd/system/fusion-console@.service
sudo systemctl daemon-reload
sudo systemctl enable --now fusion-console@$USER   # optional: auto-start
```

### 3. First run and login

```bash
fusion-console                 # macOS / Ubuntu
fusion-console.exe             # Windows
```

The first start:

1. Creates `~/.fusion-console/` (config + SQLite database)
2. Creates user `admin` with a generated password and **prints it once to
   the console log** — copy it:

   ```
   console: FIRST RUN: created user 'admin' with generated password: xxxx...
   ```

3. Listens on `http://127.0.0.1:11480`

Open `http://127.0.0.1:11480` in a browser (same machine or any machine
that can reach the host), sign in as `admin` with the printed password,
then change the password (add users by editing
`~/.fusion-console/config.json` with a bcrypt hash and restarting).

### 4. Connect an LLM — fusion-gateway

If you run [fusion-gateway](https://github.com/dahai80/fusion-gateway) in
the office (recommended — model keys stay in the gateway):

1. **Get a gateway client key** — from the gateway admin, or from
   `fusion-gateway/config.yaml` → `auth.api_keys`. The key's
   `allowed_models` must cover the model you want (e.g. `glm*` for
   `glm-5`).
2. **Console UI → Settings → API key**: key id `gw-main`, secret = the
   gateway key (starts with `fsx-`).
3. **Settings → Edit / add provider**:
   - id: `gw-main` (same as the key id)
   - type: `fusion-gateway`
   - base URL: `http://<gateway-host>:11432/v1`
   - model: `glm-5`
   - enabled: ✓
   - Save provider → Fetch models → pick the model
4. **Chat page** → select `fusion-gateway` → send a message. Streaming
   reply + token usage = working.

### 5. Connect an LLM — cloud provider (no gateway)

Any OpenAI-compatible or Anthropic-compatible API works the same way:

1. Settings → API key: key id e.g. `cloud-main`, secret = the provider key
2. Edit / add provider: type `openai_compat` (or `anthropic_compat`),
   base URL `https://api.example.com/v1`, model from the provider
3. Save → Fetch models → chat

### 6. Run a coding agent

Agents are the local CLIs on the office host. Install at least one:

- [Claude Code](https://claude.com/claude-code): `claude` on PATH
- Codex CLI: `codex` on PATH
- OpenCode: `opencode` on PATH

Check the **Agents** page in the console — agents found on PATH show
`detected`; missing ones are grayed out (config path override: edit the
agent's `command` in `~/.fusion-console/config.json` to an absolute path).

To run a task: **Chat page** → select the agent → fill **workdir**
(the project directory the agent may touch) → type the task → press the
robot button. The agent runs headless in that workdir; tool calls, result
and cost stream into the chat and land in the cost report.

### 7. Auto-start on boot (optional)

See `DEPLOY.md` in your package (also [deploy/README.md](../deploy/README.md)):
launchd (macOS), systemd (Ubuntu), schtasks/NSSM (Windows), plus the
Tailscale-only remote access setup for phone/laptop use outside the office.

### Troubleshooting

| Symptom | Fix |
|---|---|
| Login password lost | delete `~/.fusion-console/config.json`, restart — a new admin password is printed |
| `401 auth_error` from gateway | wrong client key, or key not in gateway `auth.api_keys` |
| `model not allowed` | gateway key's `allowed_models` does not cover the model |
| Agent grayed out | CLI not on PATH — install it or set absolute `command` in config.json |
| Agent exits immediately | wrong workdir, or the CLI needs login (`claude` needs an account session on that host) |

---

## 中文

### 1. 下载

从 [v0.1.0](https://github.com/dahai80/fusion-console/releases/tag/v0.1.0) 下载对应主机系统的包：

| 主机 | 文件 | 内容 |
|---|---|---|
| macOS（Apple Silicon / Intel） | `fusion-console-0.1.0-darwin-universal.dmg` | universal 二进制 + 文档 |
| Windows x64 | `fusion-console-0.1.0-windows-amd64.zip` | `fusion-console.exe` + 文档 |
| Ubuntu x64 | `fusion-console-0.1.0-linux-amd64.tar.gz` | 二进制 + systemd unit + 文档 |

### 2. 安装

**macOS**：打开 DMG，把 `fusion-console` 二进制拷到任意目录（如 `/usr/local/bin`）。

**Windows**：解压到任意目录（如 `C:\Tools\fusion-console\`），只需要 `fusion-console.exe`。

**Ubuntu**：

```bash
tar xzf fusion-console-0.1.0-linux-amd64.tar.gz
sudo mv fusion-console /usr/local/bin/
sudo mv fusion-console.service /etc/systemd/system/fusion-console@.service
sudo systemctl daemon-reload
sudo systemctl enable --now fusion-console@$USER   # 可选：开机自启
```

### 3. 首次启动与登录

```bash
fusion-console                 # macOS / Ubuntu
fusion-console.exe             # Windows
```

首次启动会：

1. 创建 `~/.fusion-console/`（配置 + SQLite 数据库）
2. 创建 `admin` 用户并生成随机密码，**只在控制台日志打印一次**，务必复制：
   ```
   console: FIRST RUN: created user 'admin' with generated password: xxxx...
   ```
3. 监听 `http://127.0.0.1:11480`

浏览器打开 `http://127.0.0.1:11480`，用 `admin` + 打印的密码登录，登录后修改密码（加用户：编辑 `~/.fusion-console/config.json` 加 bcrypt hash 后重启）。

### 4. 接 LLM — fusion-gateway（推荐）

办公室跑着 [fusion-gateway](https://github.com/dahai80/fusion-gateway) 时（推荐 — 模型 key 留在网关，不进 console）：

1. **拿网关客户端 key** — 找网关管理员，或看 `fusion-gateway/config.yaml` → `auth.api_keys`。key 的 `allowed_models` 要覆盖目标模型（如用 `glm-5` 需要 `glm*`）。
2. **Console UI → Settings → API key**：key id 填 `gw-main`，secret 粘贴网关 key（`fsx-` 开头）。
3. **Settings → Edit / add provider**：
   - id：`gw-main`（与 key id 一致）
   - type：`fusion-gateway`
   - base URL：`http://<网关主机>:11432/v1`
   - model：`glm-5`
   - enabled：✓
   - Save provider → Fetch models → 选模型
4. **Chat 页** → 选 `fusion-gateway` → 发消息。看到流式回复和 token 统计即通。

### 5. 接 LLM — 云端 provider（无网关）

任何 OpenAI 兼容 / Anthropic 兼容 API 同理：

1. Settings → API key：key id 如 `cloud-main`，secret 填 provider key
2. Edit / add provider：type 选 `openai_compat`（或 `anthropic_compat`），base URL 填 `https://api.example.com/v1`，model 填 provider 的模型名
3. Save → Fetch models → 对话

### 6. 跑 coding agent

agent 是办公室主机上的本地 CLI，至少装一个：

- Claude Code：`claude` 在 PATH
- Codex CLI：`codex` 在 PATH
- OpenCode：`opencode` 在 PATH

到 console 的 **Agents** 页检查：在 PATH 上显示 `detected`，否则灰显（改路径：编辑 `~/.fusion-console/config.json` 里该 agent 的 `command` 为绝对路径）。

跑任务：**Chat 页** → 选 agent → 填 **workdir**（agent 允许操作的项目目录）→ 输入任务 → 点机器人按钮。agent 在该 workdir 内 headless 执行，工具调用、结果、成本实时流式显示并入成本报表。

### 7. 开机自启（可选）

见包内 `DEPLOY.md`（即 [deploy/README.md](../deploy/README.md)）：launchd（macOS）、systemd（Ubuntu）、schtasks/NSSM（Windows），以及 Tailscale 远程访问（手机/笔记本在办公室外使用）。

### 排错

| 现象 | 处理 |
|---|---|
| 登录密码丢失 | 删 `~/.fusion-console/config.json` 重启，重新打印新生成的 admin 密码 |
| 网关返回 `401 auth_error` | 客户端 key 错误，或网关 `auth.api_keys` 里没有 |
| `model not allowed` | key 的 `allowed_models` 不含目标模型 |
| agent 灰显 | CLI 不在 PATH — 安装或在 config.json 填绝对路径 |
| agent 立即退出 | workdir 不对，或 CLI 需要登录（`claude` 在该主机要有账号会话） |
