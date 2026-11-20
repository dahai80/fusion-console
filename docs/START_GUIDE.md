# fusion-console Start Guide (from release download)

Install and run fusion-console without building anything — everything below
uses the prebuilt packages from the
[releases page](https://github.com/dahai80/fusion-console/releases).

[English](START_GUIDE.md) | [中文](START_GUIDE_CN.md)

## 1. Download

From the [latest release](https://github.com/dahai80/fusion-console/releases/latest),
pick the package for your office host:

| Host | File |
|---|---|
| macOS (Apple Silicon / Intel) | `fusion-console-<version>-darwin-universal.dmg` |
| Linux x86_64 / arm64 | `fusion-console-<version>-linux-amd64.tar.gz` / `-linux-arm64.tar.gz` |
| Windows x64 | `fusion-console-<version>-windows-amd64.zip` |

Every package contains `fusion-console`, the matching `fusion-gateway`, the
harness sidecar (`sidecar/harnessd.py`), a ready-to-run gateway
`config.yaml`, and `startup.sh` / `startup.cmd`.

## 2. Install

Keep the package folder together — the console starts the gateway sitting
next to it.

- **macOS**: open the DMG and copy the `fusion-console` folder somewhere
  permanent, e.g. `~/Applications/fusion-console`.
- **Linux**: `tar xzf fusion-console-<version>-linux-amd64.tar.gz -C ~/` .
- **Windows**: unzip to e.g. `C:\Tools\fusion-console\`.

## 3. First run and login

```bash
cd ~/Applications/fusion-console   # the package folder
./startup.sh                       # Windows: startup.cmd
```

`startup.sh` starts the bundled gateway and the console, then opens your
default browser already signed in. Behind the scenes the first start:

1. Creates `~/.fusion/fusion-console/` (config + SQLite database)
2. Creates user `admin` with the factory default password
   `fusion-admin-2026` (override with `FUSION_CONSOLE_ADMIN_PASSWORD`
   before the first start)
3. Listens on `http://127.0.0.1:11480`

To sign in manually, open `http://127.0.0.1:11480` and use
`admin` / `fusion-admin-2026` (prefilled when opened on the same machine).

> The factory default password is for local use only. Before the console is
> reachable from other devices (e.g. over Tailscale), change it — see
> [Security model](../README.md#security-model).

## 4. Setup wizard

With no model configured, the browser opens the setup wizard. You can
reopen it at any time from the avatar menu (top right), which also shows
setup progress.

1. **Start the gateway** — the gateway should already be running; click
   **One-click create** to issue the console's gateway API key. It is stored
   in the OS keychain and a `fusion-gateway` model source is added for you.
2. **Add a model** — pick a vendor, paste its API key and the model ids
   (comma separated, exactly as the vendor names them). Common vendors fill
   the base URL for you; for others enter it **without** `/v1`.
3. **Try it** — sends a short prompt. A reply means the whole path works.
4. **Brain & workers** (optional) — choose dispatch approval, see detected
   agent CLIs, add workers. Skip it if you only want to chat.

Then click **Start chatting**.

## 5. Other model sources

- **A gateway on another machine**: Gateway page → **Models** tab →
  **Add model source**, type `fusion-gateway`, base URL
  `http://<gateway-host>:11432/v1`, and a client key from that gateway's
  `config.yaml` → `auth.api_keys` (its `allowed_models` must cover the
  model).
- **Direct, without the gateway**: same page, type `openai_compat` or
  `anthropic_compat`, the provider's base URL, model and API key. Usage then
  bypasses the gateway ledger.

## 6. Run a coding agent

Agents are the local CLIs on the office host. Install at least one:

- Claude Code: `claude` on PATH
- Codex CLI: `codex` on PATH
- OpenCode: `opencode` on PATH

Check the **Agents** page in the console — agents found on PATH show
`detected`; missing ones are grayed out (config path override: edit the
agent's `command` in `~/.fusion/fusion-console/config.json` to an absolute path).

To run a task: **Chat page** → select the agent → fill **workdir**
(the project directory the agent may touch) → type the task → press the
robot button. The agent runs headless in that workdir; tool calls, result
and cost stream into the chat and land in the cost report.

## 7. Auto-start on boot (optional)

See `DEPLOY.md` in your package (also [deploy/README.md](../deploy/README.md)):
launchd (macOS), systemd (Ubuntu), schtasks/NSSM (Windows), plus the
Tailscale-only remote access setup for phone/laptop use outside the office.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Login password lost | delete `~/.fusion/fusion-console/config.json`, restart — admin is recreated with the factory default `fusion-admin-2026` (or your `FUSION_CONSOLE_ADMIN_PASSWORD`) |
| Gateway admin GUI asks for login again | save the gateway admin username/password once on the Gateway page (OS keychain) — "Open Admin GUI" then signs in automatically |
| `401 auth_error` from gateway | wrong client key, or key not in gateway `auth.api_keys` |
| `model not allowed` | gateway key's `allowed_models` does not cover the model |
| Agent grayed out | CLI not on PATH — install it or set absolute `command` in config.json |
| Agent exits immediately | wrong workdir, or the CLI needs login (`claude` needs an account session on that host) |
