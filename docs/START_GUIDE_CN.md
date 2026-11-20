# fusion-console 配置指南（从 release 下载开始）

无需构建，全程使用 [release 页面](https://github.com/dahai80/fusion-console/releases) 的预编译包。

[English](START_GUIDE.md) | [中文](START_GUIDE_CN.md)

## 1. 下载

到[最新发布](https://github.com/dahai80/fusion-console/releases/latest)下载对应主机系统的包：

| 主机 | 文件 |
|---|---|
| macOS（Apple Silicon / Intel） | `fusion-console-<版本>-darwin-universal.dmg` |
| Linux x86_64 / arm64 | `fusion-console-<版本>-linux-amd64.tar.gz` / `-linux-arm64.tar.gz` |
| Windows x64 | `fusion-console-<版本>-windows-amd64.zip` |

每个包里都有 `fusion-console`、配套的 `fusion-gateway`、harness sidecar（`sidecar/harnessd.py`）、开箱即用的网关 `config.yaml`，以及 `startup.sh` / `startup.cmd`。

## 2. 安装

整个包目录放在一起，console 会启动它旁边的网关。

- **macOS**：打开 DMG，把 `fusion-console` 文件夹拷到固定位置，比如 `~/Applications/fusion-console`。
- **Linux**：`tar xzf fusion-console-<版本>-linux-amd64.tar.gz -C ~/`。
- **Windows**：解压到比如 `C:\Tools\fusion-console\`。

## 3. 首次启动与登录

```bash
cd ~/Applications/fusion-console   # 包目录
./startup.sh                       # Windows：startup.cmd
```

`startup.sh` 会启动随包网关和 console，然后用默认浏览器打开已经登录的页面。首次启动时会：

1. 创建 `~/.fusion/fusion-console/`（配置 + SQLite 数据库）
2. 创建 `admin` 用户，出厂密码 `fusion-admin-2026`（首次启动前设置 `FUSION_CONSOLE_ADMIN_PASSWORD` 可覆盖）
3. 监听 `http://127.0.0.1:11480`

手动登录：打开 `http://127.0.0.1:11480`，用 `admin` / `fusion-admin-2026`（在本机打开时会自动填好）。

> 出厂密码只适合本机使用。让其他设备访问之前（比如通过 Tailscale），先改掉，见 [安全模型](../README_CN.md#安全模型)。

## 4. 新手引导

还没配置模型时，浏览器会直接打开新手引导。之后随时可以从右上角头像菜单重新打开，菜单里也显示配置进度。

1. **启动网关**：网关通常已经在跑，点 **一键创建** 给 console 发网关 API key。key 存进系统钥匙串，并自动添加 `fusion-gateway` 模型来源。
2. **接入模型**：选厂商，粘贴 API key 和模型名（逗号分隔，和厂商文档完全一致）。常见厂商会自动填 Base URL；其他厂商自己填，**不要带** `/v1`。
3. **试一次**：发一句很短的测试，模型回答了就说明整条链路是通的。
4. **Brain 与 Worker**（可选）：选派工审批方式，查看检测到的 Agent 命令行，添加 Worker。只想聊天可以跳过。

最后点 **开始对话**。

## 5. 其他模型来源

- **另一台机器上的网关**：Gateway 页 → **Models** 标签 → **Add model source**，type 选 `fusion-gateway`，base URL 填 `http://<网关主机>:11432/v1`，key 用那台网关 `config.yaml` → `auth.api_keys` 里的客户端 key（`allowed_models` 要覆盖目标模型）。
- **不经网关直连**：同一页面，type 选 `openai_compat` 或 `anthropic_compat`，填厂商的 base URL、模型和 API key。这样用量不进网关的账本。

## 6. 跑 coding agent

agent 是办公室主机上的本地 CLI，至少装一个：

- Claude Code：`claude` 在 PATH
- Codex CLI：`codex` 在 PATH
- OpenCode：`opencode` 在 PATH

到 console 的 **Agents** 页检查：在 PATH 上显示 `detected`，否则灰显（改路径：编辑 `~/.fusion/fusion-console/config.json` 里该 agent 的 `command` 为绝对路径）。

跑任务：**Chat 页** → 选 agent → 填 **workdir**（agent 允许操作的项目目录）→ 输入任务 → 点机器人按钮。agent 在该 workdir 内 headless 执行，工具调用、结果、成本实时流式显示并入成本报表。

## 7. 开机自启（可选）

见包内 `DEPLOY.md`（即 [deploy/README.md](../deploy/README.md)）：launchd（macOS）、systemd（Ubuntu）、schtasks/NSSM（Windows），以及 Tailscale 远程访问（手机/笔记本在办公室外使用）。

## 排错

| 现象 | 处理 |
|---|---|
| 登录密码丢失 | 删 `~/.fusion/fusion-console/config.json` 重启，重新创建 `admin`（出厂默认 `fusion-admin-2026` 或 `FUSION_CONSOLE_ADMIN_PASSWORD` 指定值） |
| Gateway 管理界面又要登录 | 在 Gateway 页保存一次网关管理员用户名/密码（存 OS keychain），之后 "Open Admin GUI" 自动登录 |
| 网关返回 `401 auth_error` | 客户端 key 错误，或网关 `auth.api_keys` 里没有 |
| `model not allowed` | key 的 `allowed_models` 不含目标模型 |
| agent 灰显 | CLI 不在 PATH — 安装或在 config.json 填绝对路径 |
| agent 立即退出 | workdir 不对，或 CLI 需要登录（`claude` 在该主机要有账号会话） |
