# fusion-console

A cross-platform coding-agent console in a single Go binary: browser thin
client (desktop + phone), server runs on any office host (Windows / Ubuntu /
macOS). Chat with cloud LLM providers or the local `fusion-gateway`, and hand
tasks to headless coding agents (Claude Code, fusion-code, Codex, OpenCode).

Design docs: `insight/fusion-console-design-2026-11.md` and
`insight/fusion-console-remote-access-plan-2026-11.md` in the fusion monorepo.

## What it does

- **Chat** — streaming (SSE) conversations against any OpenAI-compatible or
  Anthropic-compatible provider; `fusion-gateway` (:11432) is one provider
  among many, never a hard dependency.
- **Agent runs** — the same chat UI drives headless agent CLIs
  (`claude -p --output-format stream-json`, `codex exec --json`,
  `opencode run`, `fusion`). One task in, one normalized event stream out
  (text / tool calls / result / cost).
- **Config UI** — providers + baseURL + model (fetched from `/v1/models`);
  API keys stored in the OS keychain (Keychain / DPAPI / libsecret), never in
  the config file.
- **Cost accounting** — every turn lands in SQLite; per-day per-source report.
- **Multi-user (light)** — per-user bcrypt passwords, HttpOnly session cookies,
  login required even inside the tailnet.

## What it deliberately does NOT do

- No orchestration / task decomposition / issue queues — use `fusion-cowork`
  or `fusion-agent-studio`.
- No PTY interactive terminal (planned for v2).
- No public internet exposure, no SSO, no cloud sync.

## Build

Requirements: Go 1.26+, [Bun](https://bun.sh).

```bash
make web        # build the SPA into internal/server/webdist (go:embed)
make build      # host binary -> bin/fusion-console
make cross      # darwin/linux/windows binaries (amd64+arm64)
make test       # go test ./...
```

## Run

```bash
./bin/fusion-console            # listens on 127.0.0.1:11480
```

Data dir: `~/.fusion-console` (override with `FUSION_CONSOLE_DIR`).
First run prints a generated `admin` password to the log — change it after
first login.

Open `http://127.0.0.1:11480`, sign in, then:

1. Settings → add a provider (e.g. type `gateway`, base URL
   `http://127.0.0.1:11432/v1`, model `glm-5`), store its API key
   (keychain ref id must match the provider's `api_key_id`).
2. Chat → pick the provider, send a message; or pick an agent + workdir and
   press the agent button to hand the task to the CLI.

Headless hosts without a keychain: export
`FUSION_CONSOLE_APIKEY_<KEY_ID_UPPERCASE>` instead.

## Remote access (Tailscale only)

See [deploy/README.md](deploy/README.md). Short version: run
`tailscale serve --bg 127.0.0.1:11480` on the office host and open
`https://<host>.<tailnet>.ts.net` from any tailnet device. The application
login is always on; no public exposure, ever.

## Layout

```
cmd/console/          entry point
internal/server/      HTTP routes, SSE, embedded SPA
internal/chat/        Provider abstraction (OpenAI-compat, Anthropic-compat, gateway)
internal/agent/       headless agent adapters + process lifecycle
internal/config/      config file + OS keychain
internal/auth/        bcrypt login + cookie sessions
internal/store/       SQLite persistence (conversations, messages, cost log)
web/                  React + Tailwind SPA source
deploy/               launchd / systemd / schtasks + Tailscale guide
```

## License

Private project — see the fusion monorepo for context.
