# Design: atlascode latest-install hardening + gateway one-click install with macfabric

Date: 2026-11-17 · Status: approved decisions recorded, awaiting build approval
Branch: `feat/atlascode-install-latest-and-gateway-macfabric`

## 1. Intent

Two follow-ups to the 2026-11-16 protocol/external-models release, requested on the
Coding Agent page and Gateway page of the deployed console:

1. **Coding Agent page**: the atlascode one-click install must succeed reliably
   ("要保证成功的那种") and install the **latest** version (`@latest`), not a pinned
   one — user decision 2026-11-17.
2. **Gateway page**: support integrating macfabric (local OpenAI-compatible model
   pool at `127.0.0.1:8999/v1`, models `fabric-small` / `fabric-fast` /
   `fabric-premium`), with a one-click install-and-run gateway flow.

## 2. Decisions already made by the user

- atlascode npm install uses `@latest`, not a pinned version.
- macfabric integration via option A (one-click backend registration once the
  gateway is reachable).
- Gateway install should be "one click to the end" (download → configure → start →
  register macfabric → provision key), not just dropping a binary.
- Adjust within the existing architecture — no new abstractions.

## 3. Non-goals

- No gateway auth model changes; no editing an existing user `config.yaml`
  (generate only when missing).
- No automatic gateway upgrades (install once, upgrade stays manual).
- No separate atlascode card on the Coding Agent page (stays in the existing
  dropdown selector — minimal adjustment).
- claude/codex installs stay pinned (my call, see §4.3).

## 4. Part 1 — atlascode install hardening

### 4.1 Registry ladder + retries

`startNpmInstall` (internal/server/codeagents.go) gains a registry ladder instead of
a single `npm install -g`:

1. `npm install -g <pkg>@latest --registry=https://registry.npmmirror.com`
   (fast in CN; 1 retry)
2. fallback `npm install -g <pkg>@latest` (official registry; 1 retry)

Total budget: 5-minute context timeout. Each attempt's registry and outcome go to
the streamed install log (the CodePage log UI already renders this). Failure after
the ladder = visible error with the last npm stderr line (existing
`ragInstallFail` path).

npmmirror edge case: sync lag can 404 a just-published version. With `@latest` this
manifests as "latest" resolving to an older-but-valid version on the mirror —
acceptable; the official-registry fallback only triggers on hard failure.

### 4.2 Post-install verification (replaces LookPath-only)

After install: run `<cli> --version`, log the line, and compare against the
repurposed known-good table (§4.3):

- version ≥ known-good → done, log "verified vX.Y.Z".
- version < known-good → done with a warning line in the log (npmmirror lag), not
  a failure — the CLI works, the console's sentinel guardrails (protocol-drift
  error, missing-usage notice, version fingerprint) cover behavior.

### 4.3 Known-good table repurposed

`knownGoodNpmVersions` becomes a **minimum known-good** reference, not a pin:
{claude: 2.1.325, codex: 0.167.0, atlascode: 0.1.121} (0.1.121 verified
wire-compatible on 2026-11-17: result-event usage + conditional total_cost_usd,
identical to 0.1.120).

- atlascode: installs `@latest` (user decision). Guards cover drift.
- claude/codex: still install pinned (my call — they are the reference protocol
  implementations; silent auto-upgrade buys little and risks surprise. Flipping
  them to `@latest` later is a one-line change each).

## 5. Part 2 — gateway one-click install + macfabric

### 5.1 Console-side gateway lifecycle (new file `internal/server/gatewayrun.go`)

State machine driven by what exists on disk + port probe (all already probed by
`handleGatewayStatus`):

| State | UI action offered |
|---|---|
| binary missing | **Install & start** |
| binary present, :11432 not reachable | **Start** |
| reachable, no `macfabric` backend | **Integrate macfabric** |
| reachable, backend present | nothing (current panels) |

`POST /api/gateway/install` extended with `start=true` (new default from the UI
button) to run the full chain; a plain install stays available for scripted use.

### 5.2 Install & start chain

1. **Binary**: existing path — bundled copy if present, else release download
   (reachability probed OK 2026-11-17: v0.9.182 darwin-arm64, 21MB).
2. **Config**: if `~/.fusion-console/gateway/config.yaml` missing → generate from
   the shipped template (scripts/gateway-config.yaml, embedded as a Go string):
   port 11432, admin enabled, `jwt_secret` = fresh 64-hex random,
   `users.admin` = fresh 24-char random password. Write once, never overwrite.
   Admin credentials stored into the OS keychain under the same key the existing
   "Gateway admin login" card uses, so "Open Admin GUI" auto-signs immediately.
3. **Start**: `os/exec` the binary detached (`Setsid`-equivalent via
   `cmd.SysProcAttr`), args `-config <path>` (exact flag verified against the
   gateway release at implementation; config-path env fallback), stdout/stderr →
   `~/.fusion-console/logs/gateway.log` (append), pid → `~/.fusion-console/gateway.pid`.
   Detached means console restarts don't kill the gateway; a stale pid file is
   harmless (state is port-probe-driven).
4. **Health**: poll `:11432` up to 30s; timeout = visible error with the last
   20 log lines.
5. **Register macfabric**: server-side admin-API call (same code path as
   `handleGatewayBackendAdd`): `{name: "macfabric", type: "openai-compatible",
   base_url: "http://127.0.0.1:8999/v1", models: [fabric-small, fabric-fast,
   fabric-premium], api_key: <resolved from keychain for provider iff-local>}`.
   Key material never leaves the server process. Existing cloud_models merge +
   `/v1/models` verification give the success/failure note for free.
   Idempotency: skip registration when a backend named `macfabric` already exists.
6. **Provision gateway API key**: reuse `handleGatewayKeyProvision` logic so
   chat/brain/workers can authenticate immediately.

A standalone `POST /api/gateway/start` (and a Stop button killing via pid file)
ships with the same change; Start is the Install-chain step 3–6 extracted.

### 5.3 Frontend (web/src/App.tsx Gateway panel + small api.ts additions)

- Not reachable + not installed → single **Install & start** button, install-log
  stream (reuse the CodePage log pattern).
- Not reachable + installed → **Start** button.
- Reachable + no macfabric backend → **Integrate macfabric** button with the
  verification note rendered on completion.
- All three call new/extended endpoints; no new panels, existing cards untouched.

## 6. Testing

- Unit: config generation (random secrets, generate-once/no-overwrite, yaml
  parses); registry ladder (injectable command runner: mirror failure → official
  fallback → success); version compare; macfabric registration handler against an
  httptest fake gateway admin (idempotent skip, key resolved from keychain stub).
- Local E2E on this host: run the real chain — install & start gateway,
  register macfabric, verify fabric models appear in `/v1/models` aggregation and
  the Brain model dropdown, then clean up.
- CI: existing suite + new tests; cross-build matrix unchanged.

## 7. Delivery

Same route as PR #9: fork branch → CI green → PR to dahai80:main → merge →
rebuild + redeploy the local console.
