# fusion-console

[English](README.md) | [中文](README_CN.md) | [🚀 Start Guide](docs/START_GUIDE.md)

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

> Step-by-step setup from a release download: [docs/START_GUIDE.md](docs/START_GUIDE.md) · [中文版](docs/START_GUIDE_CN.md)

```bash
./bin/fusion-console            # listens on 127.0.0.1:11480
```

Data dir: `~/.fusion-console` (override with `FUSION_CONSOLE_DIR`).
First run prints a generated `admin` password to the log — change it after
first login.

Open `http://127.0.0.1:11480`, sign in, then:

1. Settings → add a provider (see the fusion-gateway guide below, or a cloud
   provider), store its API key (keychain ref id must match the provider's
   `api_key_id`).
2. Chat → pick the provider, send a message; or pick an agent + workdir and
   press the agent button to hand the task to the CLI.

Headless hosts without a keychain: export
`FUSION_CONSOLE_APIKEY_<KEY_ID_UPPERCASE>` instead.

## End-to-end setup with fusion-gateway

[fusion-gateway](https://github.com/dahai80/fusion-gateway) is the local
mixed-inference gateway (OpenAI-compatible, default port `11432`). The
console talks to it like any OpenAI-compatible provider — verified end to
end with `glm-5`.

### 1. Start the gateway

On the same host (or any reachable host):

```bash
~/fusion/fusion-gateway/start.sh start     # listens on 0.0.0.0:11432
curl -s http://127.0.0.1:11432/api/health  # health check
```

### 2. Get (or create) a gateway client API key

Gateway client keys live in `~/fusion/fusion-gateway/config.yaml` under
`auth.api_keys`. Pick a key whose `allowed_models` covers your model (e.g.
`glm*`), or add one:

```yaml
auth:
  api_keys:
    - name: fusion-console
      key: fsx-fusion-console-<random-hex>
      allowed_models: ["glm*", "gpt-4o*", "claude-*"]
      rpm: 60
      tpm: 100000
```

Restart the gateway after editing. The console never needs the upstream
provider keys — the gateway holds those.

### 3. Store the key in the console

Console UI → Settings → **API key (stored in OS keychain)**:

- key id: `gw-main` (any stable id; this is the keychain reference name)
- secret: paste the gateway client key (`fsx-...`)

### 4. Add the gateway provider

Same page, **Edit / add provider**:

| Field | Value |
|---|---|
| id | `gw-main` (must match the key id above) |
| name | `fusion-gateway` |
| type | `fusion-gateway` |
| base URL | `http://127.0.0.1:11432/v1` (use the host IP if the gateway runs on another machine) |
| model | `glm-5` |
| enabled | ✓ |

Click **Save provider**, then **Fetch models** to list what the gateway
exposes and pick one.

### 5. Verify

Chat page → select the `fusion-gateway` provider → send "Reply with exactly:
E2E-OK". You should see a streamed reply plus token usage under the bubble.
The message and usage are persisted in SQLite and show up in the cost report
(`GET /api/costs`).

Troubleshooting:

- `401 auth_error` — the key is wrong or not in the gateway config
- `model not allowed` — the key's `allowed_models` does not cover `glm-5`
- Connection refused — gateway not running, or the console and gateway are
  on different hosts with a firewall in between

## Appendix: configuring models in fusion-gateway

The console only sees models the gateway exposes. A model must exist in the
gateway config (all in `~/fusion/fusion-gateway/config.yaml`), then restart
the gateway (`~/fusion/fusion-gateway/start.sh restart`). Three places:

### 1. Backend — where the model actually runs

`backends:` maps a backend name to an upstream endpoint. One entry per
upstream; `type` decides the wire protocol (`openai-compatible` or
`anthropic`):

```yaml
backends:
  glm-5:                        # backend name (referenced by supply below)
    api_key: <upstream-key>     # the REAL provider key lives here, not in the console
    base_url: https://<upstream-host>
    enabled: true
    timeout: 300s
    type: anthropic             # or: openai-compatible
  fusion-mlx:                   # local MLX engine is just another backend
    api_key: mlx-<token>
    base_url: http://127.0.0.1:11434
    enabled: true
    type: openai-compatible
```

### 2. Model supply — model name → backends

`models.supply:` declares which model names the gateway serves and which
backends can serve them (list = failover order):

```yaml
models:
  supply:
    glm-5:
      - glm-5                  # model "glm-5" served by backend "glm-5"
    glm-5.3-flash:
      - volc-coding
    qwen3.5-9b:
      - fusion-mlx             # local model, appears after fusion-mlx loads it
```

A model name here is exactly what the console's model dropdown shows and
what goes into the provider's `model` field.

### 3. Routing — cloud model registration and fallback

`routing.cloud_models:` lists cloud model names (supports `claude-*`
wildcards); `routing.fallback.model_mapping:` aliases other model names
onto served ones, and `routing.fallback.cloud_default:` is the catch-all
used when a request names an unknown cloud model:

```yaml
routing:
  cloud_models:
    - glm-5
    - glm-5.3-flash
    # ... add new cloud models here
  fallback:
    enabled: true
    cloud_default: glm-5       # unknown cloud models land here
    model_mapping:
      claude-sonnet-4-6: glm-5  # claude-family requests route to glm-5
```

### 4. Verify from the console side

```bash
curl -s http://127.0.0.1:11432/v1/models \
  -H "Authorization: Bearer <gateway-client-key>" | python3 -m json.tool | grep '"id"'
```

Then in the console: Settings → select provider → **Fetch models** → pick
the new model (or just type it into the provider's `model` field) → chat.

Minimal path for "it must run today": gateway already ships with `glm-5`
configured on the `glm-5` backend — steps 1–3 are only needed when adding a
new model.

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

Apache License 2.0 — see [LICENSE](LICENSE).
