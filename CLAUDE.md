# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

fusion-console — single Go binary (server + embedded SPA) exposing a browser
console for cloud/local LLM chat and headless coding agents (claude,
fusion-code, codex, opencode). Design docs live in the fusion monorepo:
`insight/fusion-console-design-2026-11.md` and
`insight/fusion-console-remote-access-plan-2026-11.md`.

Hard boundaries (from the design doc): no orchestration (that is
fusion-cowork / fusion-agent-studio), no PTY terminal (v2), no public internet
exposure — remote access is Tailscale only, app auth always on.

## Commands

```bash
make web          # build SPA (bun) into internal/server/webdist (go:embed)
make build        # host binary -> bin/fusion-console
make cross        # darwin/linux/windows amd64+arm64
make test         # go test ./... -count=1
make vet / fmt    # go vet / gofmt

./scripts/build-web.sh   # SPA only (required before go build; embed needs webdist)
./scripts/package.sh vNX # release packages; bundle the matching fusion-gateway binary
./scripts/start.sh       # start gateway+console, open default browser with one-time auto-login
```

Run: `./bin/fusion-console` → `http://127.0.0.1:11480` (port registered in
the monorepo `architecture/port-registry.yaml`; validate changes with
`python scripts/check-ports.py` from the repo root). `--open` launches the
OS default browser at `/?fc_token=<one-time token>` which redeems itself via
`POST /api/autologin`.

Data dir `~/.fusion-console` (override: `FUSION_CONSOLE_DIR`). First run
creates `admin` with the factory default password `fusion-admin-2026`
(override before first start: `FUSION_CONSOLE_ADMIN_PASSWORD`). API keys and
the gateway admin credentials resolve from env
`FUSION_CONSOLE_APIKEY_<KEY_ID_UPPERCASE>` first, then OS keychain — env
override exists for CI/headless hosts.

## Architecture

Two abstractions only:

- `internal/chat.Provider` — `ChatStream(ctx, model, apiKey, msgs) -> Event
  channel` (delta/usage/error/done). Implementations: OpenAI-compatible SSE
  (also used by fusion-gateway), Anthropic-compatible SSE.
- `internal/agent.Adapter` — `Run(ctx, RunOpts) -> Event channel`
  (text/tool/result/usage/error/done). One thin JSONL parser per CLI; claude
  and fusion-code share the stream-json parser. `process.go` owns subprocess
  lifecycle: process-group kill, 30min timeout, budget-cap kill (cost arrives
  on the `result` event for claude, not only `usage`).

Events flow to the browser over SSE (`fetch` + ReadableStream parsing in
`web/src/api.ts`). Sessions/messages/cost rows persist in SQLite
(`internal/store`, modernc.org pure-Go driver — keep it cgo-free so
cross-compilation keeps working).

`internal/server/webdist` is build output committed to the embed; the server
binary must be rebuilt after `make web` or it serves stale assets.

## Orchestration (M1+M2)

Chat-as-main-brain: the `orchestrate` toggle (per-turn consent) gives the chat
model native tool-calls (`list_agents` / `run_agent` / `read_artifact`) and a
bounded planner loop (`maxOrchestratorTurns = 8`) in
`internal/server/orchestrator.go`. Each agent runs in its own dedicated
workspace (`config.ResolveWorkDir`: per-agent `work_dir` override or
`~/.fusion-console/agents/<name>`); `read_artifact` is sandboxed to the agents
root. Guards run the real fusion-harness via the Python sidecar
`sidecar/harnessd.py` (stdio JSON-RPC; `internal/harness/client.go` spawns it,
respawn-on-next-use on failure): `pre_llm` = budget gate + compaction before
every LLM call, `post_llm` = accounting, `tool_admit` = deny-first admission on
every dispatch — fail-closed when the sidecar is down. Wire format gotchas:
OpenAI tools must be nested `{"type":"function","function":{...}}` (flat
entries are silently ignored and the model hallucinates results); gateways may
emit usage chunks twice per stream (partial on finish chunk + full trailing
chunk) — take the max, never the sum. Cost rows: planner `chat:<provider>` vs
dispatched `agent:<name>` kept separate.

## Conventions

- Go: gofmt (tabs) wins over the monorepo 4-space rule; CI enforces gofmt -l.
- Comments/logs/CI/GitHub: English only. User-facing docs bilingual:
  README.md (EN) + README_CN.md (CN).
- Config file never contains API key material — keychain references only
  (`internal/config/keyring.go`).
- Agent parse failures must fail visible (error events), never silently drop.
- Frontend: React + Tailwind v4 + shadcn-style tokens (zinc neutrals, indigo
  accent), `lucide-react` icons only, dark mode default with light toggle.

## Testing

Unit tests cover both parsers with recorded fixtures (see
`internal/agent/agent_test.go`), provider SSE via httptest, auth round-trip,
SQLite store. Real E2E (done at implementation, rerun on protocol changes):
start server with temp `FUSION_CONSOLE_DIR`, login, stream chat through
fusion-gateway glm-5, run a real `claude` agent task in a temp workdir,
verify artifact + cost report, then clean up temp data.
