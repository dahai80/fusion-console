# fusion-console Start Guide (from release download)

Install and run fusion-console without building anything — everything below
uses the prebuilt packages from the
[releases page](https://github.com/dahai80/fusion-console/releases).

[English](START_GUIDE.md) | [中文](START_GUIDE_CN.md)

## 1. Download

From [v0.1.0](https://github.com/dahai80/fusion-console/releases/tag/v0.1.0),
pick the package for your office host:

| Host | File | Contains |
|---|---|---|
| macOS (Apple Silicon / Intel) | `fusion-console-0.1.0-darwin-universal.dmg` | universal binary + docs |
| Windows x64 | `fusion-console-0.1.0-windows-amd64.zip` | `fusion-console.exe` + docs |
| Ubuntu x64 | `fusion-console-0.1.0-linux-amd64.tar.gz` | binary + systemd unit + docs |

## 2. Install

### macOS

Open the DMG, copy the `fusion-console` binary anywhere (e.g.
`/usr/local/bin`):

```bash
hdiutil attach fusion-console-0.1.0-darwin-universal.dmg
sudo cp /Volumes/fusion-console/fusion-console/fusion-console /usr/local/bin/
hdiutil detach /Volumes/fusion-console
```

### Windows

Unzip `fusion-console-0.1.0-windows-amd64.zip` to a folder, e.g.
`C:\Tools\fusion-console\`. The only file you need is `fusion-console.exe`.

### Ubuntu

```bash
tar xzf fusion-console-0.1.0-linux-amd64.tar.gz
sudo mv fusion-console /usr/local/bin/
sudo mv fusion-console.service /etc/systemd/system/fusion-console@.service
sudo systemctl daemon-reload
sudo systemctl enable --now fusion-console@$USER   # optional: auto-start
```

## 3. First run and login

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

## 4. Connect an LLM — fusion-gateway

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

## 5. Connect an LLM — cloud provider (no gateway)

Any OpenAI-compatible or Anthropic-compatible API works the same way:

1. Settings → API key: key id e.g. `cloud-main`, secret = the provider key
2. Edit / add provider: type `openai_compat` (or `anthropic_compat`),
   base URL `https://api.example.com/v1`, model from the provider
3. Save → Fetch models → chat

## 6. Run a coding agent

Agents are the local CLIs on the office host. Install at least one:

- Claude Code: `claude` on PATH
- Codex CLI: `codex` on PATH
- OpenCode: `opencode` on PATH

Check the **Agents** page in the console — agents found on PATH show
`detected`; missing ones are grayed out (config path override: edit the
agent's `command` in `~/.fusion-console/config.json` to an absolute path).

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
| Login password lost | delete `~/.fusion-console/config.json`, restart — a new admin password is printed |
| `401 auth_error` from gateway | wrong client key, or key not in gateway `auth.api_keys` |
| `model not allowed` | gateway key's `allowed_models` does not cover the model |
| Agent grayed out | CLI not on PATH — install it or set absolute `command` in config.json |
| Agent exits immediately | wrong workdir, or the CLI needs login (`claude` needs an account session on that host) |
