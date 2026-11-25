# fusion-console

[English](README.md) | [中文](README_CN.md) · [Start guide](docs/START_GUIDE.md) · [Phone access](deploy/TAILSCALE_QUICKSTART.md) · [Releases](https://github.com/dahai80/fusion-console/releases)

A browser console for coding agents, shipped as one Go binary. Run it on any
office machine (macOS, Ubuntu, Windows), open it from a desktop browser or your
phone, chat with cloud or local LLMs, and let the chat model hand real coding
work to the agent CLIs installed on that machine — Claude Code, Codex,
OpenCode, fusion-code.

- **One binary, no runtime to install.** Server and web UI are embedded; data
  is a SQLite file and a JSON config.
- **Chat that can delegate.** Turn on `orchestrate` and the model you are
  chatting with becomes the *brain*: it splits the task, dispatches workers,
  reads their output and reports back. Every dispatch can wait for your
  approval.
- **Real agents, real repos.** Workers are local CLIs bound to a directory, a
  git worktree or a GitHub repository, each with its own budget cap.
- **One gateway, one ledger.** Model keys live in the bundled
  [fusion-gateway](https://github.com/dahai80/fusion-gateway); the console
  only holds a gateway key in the OS keychain. Token usage for the brain and
  every worker shows up on one dashboard.
- **Private by design.** Listens on `127.0.0.1` only. Remote use goes through
  Tailscale (with an optional self-hosted relay); login is always on.

## Contents

- [How it fits together](#how-it-fits-together)
- [Quick start](#quick-start)
- [First-run wizard](#first-run-wizard)
- [Everyday use](#everyday-use)
- [Remote access from your phone](#remote-access-from-your-phone)
- [Orchestration in depth](#orchestration-in-depth)
- [Configuration reference](#configuration-reference)
- [Adding models to fusion-gateway by hand](#adding-models-to-fusion-gateway-by-hand)
- [Build and develop](#build-and-develop)
- [Security model](#security-model)
- [Troubleshooting](#troubleshooting)
- [Scope](#scope)

## How it fits together

```
 Browser (desktop / phone)
        │  HTTPS via tailscale serve, or http://127.0.0.1:11480 locally
        ▼
 fusion-console :11480 ──────────────► fusion-gateway :11432 ──► cloud LLMs (DeepSeek, Qwen,
   chat · brain · dashboard              one key, one ledger      Kimi, Claude, OpenRouter …)
   SQLite + config.json                                       └─► local engines (fusion-mlx …)
        │
        └─ spawns headless agent CLIs (workers) in their own workspaces
             claude · codex · opencode · fusion-code
```

Optional add-ons, each installed only when you click it: fusion-rag
(knowledge base), fusion-code, fusion-mlx (local models on Apple Silicon),
fusion-agent-studio (teams).

## Quick start

### Option A — release package (recommended)

Download the package for your machine from
[Releases](https://github.com/dahai80/fusion-console/releases/latest):

| Machine | File |
|---|---|
| macOS (Apple Silicon or Intel) | `fusion-console-<version>-darwin-universal.dmg` |
| macOS, tarball | `fusion-console-<version>-darwin-arm64.tar.gz` / `-darwin-amd64.tar.gz` |
| Linux x86_64 / arm64 | `fusion-console-<version>-linux-amd64.tar.gz` / `-linux-arm64.tar.gz` |
| Windows x64 | `fusion-console-<version>-windows-amd64.zip` |

Each package contains `fusion-console`, the matching `fusion-gateway`, the
harness sidecar, a ready-to-run gateway `config.yaml` and a start script.

```bash
# macOS / Linux: unpack, then from the package folder
./startup.sh          # starts gateway + console, opens the browser already signed in
# Windows: double-click startup.cmd
```

The browser lands in the setup wizard. Five minutes later you can chat.

### Option B — from source

Requires Go 1.26+ and [Bun](https://bun.sh).

```bash
git clone https://github.com/dahai80/fusion-console && cd fusion-console
make web && make build           # SPA into internal/server/webdist, binary into bin/
./scripts/start.sh start --open  # or just ./bin/fusion-console
```

A source build does not include `fusion-gateway`; the wizard offers to
download it, or run `./scripts/package.sh` to produce the same bundle as a
release.

### Sign in

Open `http://127.0.0.1:11480` and sign in as `admin` / `fusion-admin-2026`
(factory default; when opened on the same machine the login form fills it in).
Set `FUSION_CONSOLE_ADMIN_PASSWORD` before the first start to choose your own,
and change the factory password before allowing any remote access — see
[Security model](#security-model).

## First-run wizard

The wizard opens by itself when no model is configured, and any time later
from the avatar menu (top right), which also shows how far setup has got.
Each step checks live state, so a re-run ticks off what is already done and
jumps to the first open step.

| Step | What it does |
|---|---|
| 1. Start the gateway | Starts (or installs) the bundled fusion-gateway and creates the console's gateway API key in one click. The key goes to the OS keychain and a gateway model source is added automatically. |
| 2. Add a model | Pick a vendor (DeepSeek, Qwen/DashScope, Kimi, Zhipu, MiniMax, OpenRouter, Anthropic or any OpenAI-compatible endpoint), paste its API key and model ids. Vendor presets fill the base URL. |
| 3. Try it | Sends a short test prompt. A reply means gateway, key and model work end to end. |
| 4. Brain & workers *(optional)* | Choose how dispatches are approved, see which agent CLIs were detected, and add workers. |

After that: **Start chatting**.

## Everyday use

- **Chat** — streaming conversations; switch model per conversation from the
  header. Attach files, use voice input on supported browsers.
- **`@worker`** — mention a worker to send it a task straight away in the
  background. Tick **Requires Git repository** when the task needs a checkout.
- **`orchestrate`** — the toggle beside the input box (on by default) lets the
  brain dispatch workers itself. In *confirm* mode each dispatch shows an
  approval card.
- **Task rail** — running and queued dispatches with their stage, collapsible
  to a status bubble on phones.
- **Weekly report** — ask in chat (with `orchestrate` on) and the brain calls
  `weekly_report`: per-worker runs, outcomes, tokens, cost and task titles for
  any date range, narrowed to the workers you name. Same data is available at
  `GET /api/report/weekly?start=YYYY-MM-DD&end=YYYY-MM-DD&worker=<name>`.
- **Dashboard** — dispatch queue, running tasks, worker availability, an
  execution timeline, and token usage for the brain and each worker
  (today / 7 days / 30 days / all time, calendar, chart or table view).
- **Agents** — detected CLIs, the brain settings and the worker fleet.
  **Skills → Market** browses the [fusion-skills](https://github.com/dahai80/fusion-skills)
  catalog; skills install in one click into `~/.claude/skills/` (pinned
  artifact checked against its sha256, or the in-repo folder at a pinned
  commit). Symlinked skills are never replaced; a same-name local skill is
  replaced only after you confirm.
- **Gateway** — gateway status, keys, cloud backends; the **Models** tab
  manages model sources, including direct OpenAI/Anthropic-compatible
  endpoints that bypass the gateway.
- **Knowledge Base** — one-click fusion-rag install; chats then get
  `kb_search` / `kb_ask` tools.
- **Team** — live boards for fusion-agent-studio teams created from the Team
  page or from chat.
- **Updates (macOS)** — the update button beside the theme switch installs new
  builds; see [deploy/README.md](deploy/README.md#macos-user-installation-with-updates).

## Remote access from your phone

fusion-console never opens a public port. To use it away from the office:

1. Install Tailscale on the office machine and your phone, same account.
2. Publish the console inside your tailnet: `tailscale serve --bg 11480`.
3. Open `https://<machine>.<tailnet>.ts.net` on the phone and sign in.

When the phone sits behind mobile-carrier NAT, traffic falls back to a relay.
Tailscale's shared relays can be slow from mainland China, so the guide also
shows how to run your own relay (DERP) on a small Hong Kong VPS with one
script, [`deploy/tailscale/setup-derp.sh`](deploy/tailscale/setup-derp.sh).

First time: **[quickstart](deploy/TAILSCALE_QUICKSTART.md)** · **[小白版](deploy/TAILSCALE_QUICKSTART_CN.md)**.
Full walkthrough: **[deploy/TAILSCALE.md](deploy/TAILSCALE.md)** ·
**[中文版](deploy/TAILSCALE_CN.md)** (more detailed).

## Orchestration in depth

**Brain.** With `orchestrate` on, the chat model gets three native tools —
`list_agents`, `run_agent`, `read_artifact` — and a planner loop bounded at 8
iterations per turn. Two brain modes (Agents → Workers → Brain):

- *chat model* (default): the planner prompt includes the workspace config
  and up to 12 KB of `brain_context_files` (one path per line: working
  conventions, repo inventory).
- *claude code*: each orchestrated turn runs a `claude` session in the brain
  work dir, with the same dispatch tools exposed over MCP
  (`fusion-console mcp`). It also reads `CLAUDE.md` from that directory.

The brain work dir (default `~/.fusion/fusion-console/agents`) is the
sandbox for `read_artifact` and the parent of worker worktrees. It can be
bound to a GitHub URL (cloned, fast-forwarded before each dispatch, local
changes never overwritten) or a local repository.

**Workers.** Each worker is an agent CLI plus a workspace, in one of three
modes:

| Mode | Behaviour |
|---|---|
| `standalone` | One fixed directory per worker, shared across tasks |
| `worktree` | Each dispatch gets a fresh detached `git worktree` of a whitelisted local repo |
| `github` | Bound to a repository URL: cloned on first dispatch, fetched and fast-forwarded later; a dirty tree or local commits are reported, never overwritten |

Workers are repo-bound: a dispatch to a repository the worker does not own
is refused. Each worker can be pinned to a model source and model (the
console injects `ANTHROPIC_*` / `OPENAI_*` variables from the keychain) and
keeps notes in `NOTES.md` by default (or fusion-memory, or off).

**Guardrails.** Per-worker enable switch and budget cap; background runs
share a bounded pool (4 at a time by default) with a 48-hour per-run
timeout; every dispatch passes the fusion-harness tool-admission chain,
which fails closed when the sidecar is down. Approval modes: `confirm`
(ask per dispatch), `auto` (dispatch within budget), `off` (no tools). A
task can report a missing prerequisite with a final
`{"status":"blocked","reason":"…"}`; the dispatch card then shows
**blocked** instead of a false success.

**Harness sidecar.** Guards run the Python fusion-harness through
`sidecar/harnessd.py`. The console looks for an interpreter in
`FUSION_CONSOLE_HARNESS_PYTHON`, then the install and workspace virtualenvs,
then `python3` / `python` on PATH. With `harness.enabled` in `config.json`,
chat stops before the model call if the sidecar cannot start. Kernel 0.22
covers guard checks and budget reporting; 0.23 adds LLM metrics and failure
attribution.

**Accounting.** Planner turns are booked as `chat:<provider>` (or
`brain:claude`), dispatched runs as `agent:<name>`. Failures are tagged with
the MAST taxonomy (`fc1_brief`, `fc2_handoff`, `fc3_tool`) and exportable as
JSONL from Dashboard → Failures.

## Configuration reference

**Files.** Data directory `~/.fusion/fusion-console/` (override with
`FUSION_CONSOLE_DIR`): `config.json` (users, model sources, agents,
orchestration — never key material), `console.db` (SQLite), `log/`.

**Environment variables**

| Variable | Purpose |
|---|---|
| `FUSION_CONSOLE_DIR` | Data directory |
| `FUSION_CONSOLE_ADMIN_PASSWORD` | Initial `admin` password, read on the very first start only |
| `FUSION_CONSOLE_APIKEY_<KEY_ID>` | API key for a keychain id (uppercase; e.g. `..._GATEWAY_KEY`) — for headless hosts without a keychain |
| `FUSION_CONSOLE_HARNESS_PYTHON` | Python interpreter that can `import fusion_harness` |
| `FUSION_STUDIO_SOCKET` | agent-studio daemon socket (default `/tmp/fusion-studio.sock`) |
| `FUSION_CODE_DOWNLOAD_URL` | Alternate download source for the fusion-code installer |

**Ports** (all bound to `127.0.0.1`)

| Port | Service |
|---|---|
| 11480 | fusion-console |
| 11432 | fusion-gateway |
| 11434 | fusion-mlx (optional) |
| 11436 | fusion-rag (optional) |
| 11441 / 11482 | fusion-code API / web (optional) |

**Command line.** `fusion-console` starts the server and opens the browser
with a one-time sign-in link; use `-open=false` on headless hosts.
`fusion-console mcp` runs the MCP stdio brain server.

**Auto-start.** launchd (macOS), systemd (Linux) and Task Scheduler
(Windows) recipes are in [deploy/README.md](deploy/README.md).

## Adding models to fusion-gateway by hand

The wizard and the Gateway page cover the common case. For anything else,
edit the gateway's `config.yaml` (next to the gateway binary) and restart it.

```yaml
backends:                        # where models actually run
  deepseek:
    type: deepseek               # or openai-compatible, anthropic, zhipu, dashscope, …
    base_url: https://api.deepseek.com   # without /v1 — the gateway appends it
    api_key: sk-...              # the real vendor key lives here, never in the console
    enabled: true

models:
  supply:                        # model id -> backends (list = failover order)
    deepseek-chat: [deepseek]

routing:
  cloud_models: [deepseek-chat]  # allowlist of cloud model ids (wildcards like claude-* work)
  fallback:
    enabled: true
    cloud_default: deepseek-chat # unknown cloud model ids land here
```

Check what the console will see:

```bash
curl -s http://127.0.0.1:11432/v1/models -H "Authorization: Bearer <gateway-key>" | grep '"id"'
```

## Build and develop

```bash
make web      # build the SPA (bun) into internal/server/webdist (go:embed)
make build    # bin/fusion-console for this machine
make cross    # darwin / linux / windows, amd64 + arm64
make test     # go test ./... -count=1
make vet fmt
./scripts/package.sh vX.Y.Z   # release bundles, including the matching fusion-gateway
```

`internal/server/webdist` is committed build output: run `make web` before
`go build`, or the binary serves stale UI.

```
cmd/console/        entry point (server, `mcp` subcommand)
internal/server/    HTTP routes, SSE, orchestration, dispatch, embedded SPA
internal/chat/      model sources: OpenAI-compatible, Anthropic-compatible, gateway
internal/agent/     agent CLI adapters and subprocess lifecycle
internal/harness/   fusion-harness sidecar client
internal/config/    config.json and OS keychain
internal/auth/      bcrypt login, cookie sessions
internal/store/     SQLite (conversations, messages, costs)
sidecar/            harnessd.py
web/                React + Tailwind source
deploy/             launchd / systemd units, remote access guides, relay script
docs/               start guide, design notes
```

## Security model

- The server binds `127.0.0.1:11480`. Nothing is exposed to the internet;
  never use `tailscale funnel` or port forwarding for it.
- Login is required for every request, including inside the tailnet.
  Passwords are bcrypt hashes in `config.json`.
- **Change the factory password** before any remote use: generate a hash
  with `htpasswd -bnBC 10 "" 'new-password' | tr -d ':\n'`, put it in the
  `password_hash` of the `admin` entry in `config.json`, restart. Add more
  users the same way.
- API keys live in the OS keychain (Keychain / DPAPI / libsecret) or
  `FUSION_CONSOLE_APIKEY_*`, never in config files.
- Agents run in their declared workspaces with budget caps; keep the
  approval mode on *confirm* if you dispatch from a phone.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Forgot the console password | Write a new hash into `config.json` (see Security model) and restart; or delete `config.json` to reset to the factory admin (this also resets other settings) |
| Wizard: creating the API key fails | The gateway admin password was changed — save the new admin credentials in the wizard's help section, then retry |
| `401 auth_error` from the gateway | Wrong or deleted gateway key — recreate it from the avatar menu |
| `model not allowed` / model missing | The model id is not in the gateway, or misspelled; ids must match the vendor's exactly |
| Backend added but its models never appear | Wrong base URL (do not include `/v1`), wrong key, or the vendor is unreachable from this machine |
| Agent greyed out | CLI not on PATH; install it or set an absolute `command` in `config.json` |
| Agent exits immediately | Wrong work dir, or the CLI needs its own login on this machine |
| Worker starts in a strange `～/…` directory | A Chinese IME turned `~` into full-width `～`; paste absolute paths |
| Worker stalls with no output | Headless runs cannot answer prompts; use `permission_mode: bypassPermissions` for unattended workers and rely on the budget cap |
| Copy button does nothing on the phone | The page is served over plain HTTP; use `tailscale serve --bg 11480` (HTTPS) as in the remote access guide |

## Scope

Deliberately out of scope: multi-level DAG orchestration and issue queues
(use fusion-cowork or fusion-agent-studio — the brain here dispatches one
level deep), an interactive PTY terminal (planned for v2), public internet
exposure, SSO and cloud sync.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
