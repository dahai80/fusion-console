# Implementation Plan: unified model sources (design v2)

Date: 2026-11-17 · Design: `docs/superpowers/specs/2026-11-17-atlascode-install-and-gateway-macfabric-design.md`
Branch: `feat/atlascode-install-latest-and-gateway-macfabric` · Tracking issue: dahai80/fusion-console#15

Verified facts this plan builds on: keyring already exposes
`GetAPIKey/SetAPIKey/DeleteAPIKey`; `providerOpts{convID,budget,model,user}`
carries the chat model today; `gatewayBase()`/`gatewayPortOpen()` exist; no
chat-default config section exists yet; `handleSaveProvider` hot-updates
`s.cfg` (no restart); `migrate()` is CREATE-only (needs idempotent ALTER).

## Phase 1 — backend foundation (config, migration, chat model)

**`internal/config/config.go`**
- `Provider` gains `Lifecycle string \`json:"lifecycle,omitempty"\`` —
  `"fusion-gateway"` | `"macfabric"` | `""` (custom). Presets set it; it
  drives the lifecycle endpoint dispatch and UI affordances.
- New `ChatConfig` section on Config: `Chat ChatConfig \`json:"chat"\`` with
  `DefaultProviderID string` + `DefaultModel string` ("default chat model",
  replaces the gateway brain-card role).
- `ValidateProvider(p *Provider) error` (new): id non-empty + slug charset;
  type ∈ {openai_compat, anthropic_compat, gateway}; base_url http(s), trailing
  slash trimmed; anthropic type must NOT end in /v1 (error), openai type
  missing /v1 → allowed but `ValidateNote` returned (warning surfaced to UI);
  models unique, non-empty strings.

**`internal/store/store.go`**
- `migrate()`: new idempotent helper `addColumn(db, table, column, ddl)` —
  reads `PRAGMA table_info(<table>)`, executes `ALTER TABLE ... ADD COLUMN`
  only when absent. Adds `conversations.provider_id TEXT NOT NULL DEFAULT ''`
  and `conversations.model TEXT NOT NULL DEFAULT ''`.
- `CreateConversation` keeps its signature; new
  `SetConversationModel(convID, providerID, model string) error` and
  `ListConversations`/`ListMessages` unchanged (model read via conversation
  row by ChatPage).

**`internal/server/handlers.go`**
- `handleSaveProvider`: run `ValidateProvider`; reject with 400 + specific
  message; store warning note in response (`{"ok":true,"note":"..."}`).
- `handleDeleteProvider`: call `config.DeleteAPIKey(providerKeyID(prov))`
  (best-effort, logged); 409 with consumer list when conversation/agent/
  worker references exist unless `?force=1`.
- `handleChat` request gains `Model string \`json:"model"\``; resolution
  order: request.model > conversation.model > chat-default (cfg.Chat) >
  provider default; resolved triple persisted via `SetConversationModel`
  before streaming. `providerOpts.model` carries the resolved model — the
  orchestration brain loop follows automatically (verified: same request
  provider object).
- New `GET /api/model-catalog` (auth): merged selector feed from all enabled
  providers — `[{provider_id, provider_name, kind, model, reachable}]`;
  gateway-kind providers probed with `gatewayPortOpen`-style 300ms dial,
  cached 30s (existing verMu-style mutex pattern).

## Phase 2 — lifecycle management (both presets)

**`internal/server/gatewayrun.go` (new)**
- `ensureGatewayConfig(dir) (path, adminPass string, err)`: if
  `~/.fusion-console/gateway/config.yaml` missing → write embedded template
  (port 11432, admin enabled, jwt_secret = 64-hex random, users.admin =
  24-char random), store admin creds into the keychain id the Gateway admin
  card already uses; never overwrite existing file.
- `startGateway()`: spawn `gatewayBinaryPath() -config <path>` detached
  (`SysProcAttr.Setpgid`), stdout/stderr append to
  `~/.fusion-console/logs/gateway.log`, pid file `~/.fusion-console/gateway.pid`;
  poll :11432 up to 30s; timeout error includes last 20 log lines.
- `stopGateway()`: SIGTERM pid-file process, wait 5s, SIGKILL fallback.
- `handleGatewayInstall` extended: `start=true` (UI default) runs install →
  config → start → (existing) backend/key steps stay manual except the
  macfabric-equivalent generic registration below is NOT auto-run for
  gateway (its backends are user-configured); health result returned.
- New `POST /api/gateway/start`, `POST /api/gateway/stop` (auth).

**`internal/server/macfabric.go` (new)**
- Detect: port 8999 probe OR `launchctl print gui/$UID/com.macfabric.gateway`.
- `start`: `launchctl kickstart -k gui/$UID/com.macfabric.gateway`; `stop`:
  `launchctl bootout` equivalent via `kill` on pid (launchctl list lookup);
  poll 8999 up to 20s.
- Install (stage-1 per design §6.1): if `~/Documents/MacFabric/scripts/
  release-install.sh` exists → offer "run installer" (`POST /api/macfabric/
  install` streams script output like the RAG installer); else return
  guidance text (repo URL). macOS-only guard: `runtime.GOOS != "darwin"` →
  the endpoint 400s with "macfabric is macOS-only" and the UI hides the
  preset on non-darwin.
- Generic endpoint `POST /api/sources/{id}/action` with
  `{action: "start"|"stop"|"install"}` dispatches on `Provider.Lifecycle`
  (fusion-gateway → gatewayrun, macfabric → macfabric, "" → 400). This is
  the single surface the Models page calls.

## Phase 3 — atlascode install hardening (from v1 §4, unchanged)

- `codeagents.go`: atlascode install spec becomes
  `@atlasharness/atlascode@latest`; `startNpmInstall` gains registry ladder
  (npmmirror → official, 1 retry each, 5-min ctx budget, registry + outcome
  logged); post-install `--version` capture vs `knownGoodNpmVersions`
  repurposed as minimum-known-good {claude 2.1.325, codex 0.167.0,
  atlascode 0.1.121} — below-known-good logs a warning, never fails.

## Phase 4 — binding visibility + orchestrator notice

- `providerEnv` (orchestrator.go): every silent fallback branch (provider
  missing/disabled/no key/type mismatch) returns a reason string alongside
  the existing `(env, ref)`; `handleAgentRun`/orchestrator dispatch emits
  `EventNotice` with that reason before the run starts (PR #9 notice
  channel, frontend already renders it).
- Agents page: per-CLI binding description data in `/api/agents` (env vars /
  settings.json / ANTHROPIC_MODEL per design §4.1B) — static map served by
  the backend so UI text cannot drift from code paths that tests assert.

## Phase 5 — frontend

- **`web/src/ModelsPage.tsx` (new)**: source list cards (name, type, base_url,
  pool count, default model, lifecycle badge, reachability dot) + add/edit
  dialog implementing design §4.1A (stepped form, type-aware placeholders,
  URL normalization, key status without echo, pool editor with
  fetch-from-endpoint + drag default, testcall-gated save, consumer list on
  edit, delete confirmation with keychain cleanup note). "Set as default chat
  model" writes cfg.Chat. Lifecycle buttons call `/api/sources/{id}/action`.
- **`web/src/App.tsx` Chat page**: header selector replaced by ModelPicker
  fed by `/api/model-catalog` (grouped by provider, gateway items annotated
  `· gateway`, unreachable greyed); selection stored on the conversation
  (send `{provider_id, model}` in `/api/chat`; conversation row carries it
  across sessions). `gwProvider` hardcode removed from the send path.
- **Gateway page**: brain-model card removed; Install & start / Start / Stop
  buttons wired to the new endpoints; backends + key cards unchanged.
- **`web/src/Onboarding.tsx`**: providers step replaces forms with a link/
  embed of ModelsPage; success checks accept any enabled provider (drop the
  gateway-preference lookup).
- **`web/src/api.ts`**: types for model catalog, lifecycle actions, chat
  model field, provider validation note.

## Phase 6 — tests & E2E

Unit:
- `config`: ValidateProvider table test (types, /v1 rules, slug, duplicates).
- `store`: idempotent addColumn (fresh + pre-existing column), 
  SetConversationModel round-trip.
- `handlers`: chat model resolution order (request > conv > default >
  provider), model-catalog reachability cache with fake dialer.
- `gatewayrun`: config generate-once + secret randomness + yaml parses;
  start/stop against a stub binary script.
- `macfabric`: detect/start/stop with injectable launchctl runner; non-darwin
  guard.
- `orchestrator`: fallback-reason → EventNotice mapping for all four branches.
- `codeagents`: registry ladder with injectable runner (mirror fail →
  official), version compare warning.

E2E (this host, real): design §9 scenarios 1 (macfabric preset add → chat),
2 (mid-conversation switch), 4 (gateway install & start → backend/model in
catalog), 5 (gateway down → direct still works), 6 (atlascode dispatch chain
regression); scenario 3 covered by unit + manual custom-source add.

## Execution order & risk gates

1 → 2 → 3 (independent, may land first) → 4 → 5 → 6. Phase 1 is the
regression-risk gate: existing chat (gateway + direct) must pass the
current smoke before Phase 2 starts. Phase 5 Chat-page rewiring is the UI
regression gate: scenario 6 must stay green.

## Out of scope (explicit)

Gateway auto-upgrade; MacFabric dmg automation (stage-2, upstream P4);
provider usage analytics; multi-admin key management.
