# Issue #45 UX Borrow Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the six borrowed-UX items (reason-code copy map, run drawer, steer bubbles, wakeup cards, presence badges, worktree hardening) in fusion-console against the closed agent-studio RPCs #747-750.

**Architecture:** Console stays a thin proxy over the daemon UDS JSON-RPC: one typed RPC error carrier (`teamRPCError`) propagates `reason_code`/`hint` from every `{"status":"error"}` result to HTTP as `{error, reason_code, hint}`; three read-only run-replay endpoints feed a new RunDrawer component; supplement/wakeup ride the existing `/api/team/op` dispatcher; presence badges and stuck-state alerts derive purely from the existing team snapshot; worktree hardening extends `createWorktree` in place.

**Tech Stack:** Go (stdlib net/http, modernc SQLite store), React + TypeScript + Tailwind v4, bun for the SPA, existing UDS `teamRPC` client.

**Spec:** dahai80/fusion-console#45 (body + GUI prototype comment), plan doc `fusion-research/docs/multica-borrow-plan-1118.zh-CN.md` F1-F5.

## Global Constraints

- reason_code v1 enum (closed): `queued | coalesced | team_not_registered | member_unknown | permission_mode_unknown | invocation_not_allowed | runtime_offline | lead_not_running | budget_exhausted | depth_overflow | task_not_found | spec_unavailable | supplement_cap_reached | wakeup_timeout`
- Every reason_code has EN + CN copy; unknown code renders the raw message, never crashes.
- Degraded path rule: RPC missing / feature not landed hides the entry point, never errors the page.
- Comments/logs/CI/GitHub English only; user-facing UI copy bilingual.
- Go: gofmt tabs; 4-space multiples in TS; no docstrings; logging on new code paths.
- Steer cap 3 per run; wakeup cap 8 per task (daemon-enforced, console renders the error/counter).
- Daemon does NOT expose task metadata (`wakeups`, `supplements`) via teams.status — armed counts render only when metadata is present (upstream exposure tracked separately).

## Review Focus

- Daemon returns `{"status":"error"}` as a *successful* RPC result (not JSON-RPC error) — every new proxy path must check the result map, not just transport errors.
- Unknown/legacy reason codes (old daemon without reason_code) must render legacy raw-message errors.
- `teams.run_messages` with a missing attempt returns `task_not_found` — the drawer must show that as an empty state, not a page error.
- Worktree snapshot must never mutate the source repo (stash create is dangling; no index changes).
- Steer send while no run is active degrades to inbox (`delivered_as_inbox`) — rendered as delivered-to-inbox, not an error.

---

### Task 1: Reason-code plumbing (F1 backend + copy map)

**Files:**
- Modify: `internal/server/teamclient.go` (add `teamRPCError`, `teamResult`)
- Modify: `internal/server/teamhandlers.go` (handleTeamOp + teamSend + teamInbox use it)
- Modify: `internal/server/teamcreate.go` (teamCreateCore error path carries code)
- Modify: `web/src/api.ts` (ApiError with reason_code/hint)
- Create: `web/src/reasons.ts` (code -> {en, cn, action?})
- Create: `web/src/components/ReasonCard.tsx`
- Test: `internal/server/teamhandlers_test.go` (extend teamop_test.go)

**Interfaces:**
- Produces: `type teamRPCError struct{ Code, Message, Hint string }`, `func teamResult(res map[string]any) (map[string]any, error)` in teamclient.go. HTTP error body: `{"error": msg, "reason_code": code, "hint": hint}`. TS: `class ApiError extends Error { reason_code?: string; hint?: string }`, `reasonCopy(code): {en, cn, action?}`, `<ReasonCard error={ApiError} onAction={}/>`.
- Consumes: daemon `error_payload` shape `{status:"error", reason_code, message, hint}`.

- [ ] Step 1: Go test — table over daemon fake returning `{"status":"error","reason_code":"lead_not_running","message":"m","hint":"h"}`; `handleTeamOp` writes 502 with all three fields; legacy shape (message only) still 502 with error text.
- [ ] Step 2: Run `go test ./internal/server/ -run TeamOp -count=1` — FAIL.
- [ ] Step 3: Implement teamRPCError/teamResult + wire handleTeamOp, teamSend, teamInbox, teamChatAssign, teamCreateCore.
- [ ] Step 4: Go test PASS; `gofmt -l` clean.
- [ ] Step 5: Commit `feat: reason_code propagation from daemon team RPCs`.

### Task 2: Bilingual copy map + ReasonCard (F1 frontend)

**Files:**
- Create: `web/src/reasons.ts`
- Create: `web/src/components/ReasonCard.tsx`
- Modify: `web/src/api.ts` (ApiError thrown by `req`/`streamPOST`)
- Modify: `web/src/TeamPage.tsx` (err state renders ReasonCard)
- Test: `web/src/reasons.test.ts` (parity)

- [ ] Step 1: Parity test — for every code in the enum list, `reasonCopy(code).en` and `.cn` non-empty; unknown code returns null (caller falls back to raw message).
- [ ] Step 2: `cd web && bun test reasons.test.ts` — FAIL (file missing).
- [ ] Step 3: Implement reasons.ts (all 14 codes, actions: lead_not_running -> start lead loop, budget_exhausted -> raise budget, supplement_cap_reached -> wait hint, wakeup_timeout -> re-arm), ReasonCard (icon, code line, EN copy, CN copy, single primary action button).
- [ ] Step 4: bun test PASS.
- [ ] Step 5: Commit `feat: bilingual reason-code copy map and error card`.

### Task 3: Run drawer (F2)

**Files:**
- Modify: `internal/server/teamhandlers.go` (3 GET handlers + teamOpMethod runs helpers)
- Modify: `internal/server/server.go` (routes)
- Modify: `web/src/api.ts` (teamRuns, teamRunMessages, teamRunUsage)
- Create: `web/src/components/RunDrawer.tsx`
- Modify: `web/src/TeamPage.tsx` (task cards + assignment bubbles get "View run")
- Test: `internal/server/teamruns_test.go`

**Interfaces:**
- Produces: `GET /api/team/runs?team=&task_id=` -> `{status, team, task_id, runs:[{attempt, member, events, status, usage}]}`; `GET /api/team/run_messages?team=&task_id=&attempt=` -> `{messages:[{seq, ts, type, payload}]}`; `GET /api/team/run_usage?team=&task_id=` -> `{usage:[{attempt, status, usage}]}`. Errors carry reason_code (task_not_found / spec_unavailable).
- Consumes: daemon teams.runs_list / run_messages / run_usage.

- [ ] Step 1: Go test — fake daemon serves the three RPCs; handlers proxy params verbatim; daemon `status:error` maps to 502 + reason_code.
- [ ] Step 2: FAIL, then implement handlers + routes in server.go near existing team routes.
- [ ] Step 3: RunDrawer component: right-side drawer, timeline rows (ts, type icon, payload detail), usage footer (`in X · out Y · cache-hit Z% · ≈cost` when present), copy-jsonl button, close. Entry point hidden when `teamRuns` rejects (spec_unavailable / task_not_found -> no button next time, drawer shows empty state if open).
- [ ] Step 4: Wire "View run" on task cards (TeamPage) and on team-chat assistant bubbles whose meta has task_id (App.tsx message render).
- [ ] Step 5: `go test ./... -count=1`, `bun run build` PASS. Commit `feat: run replay drawer over teams.runs_* RPCs`.

### Task 4: Steer bubbles (F3)

**Files:**
- Modify: `internal/server/teamhandlers.go` (op `supplement` -> teams.supplement {team, task_id, content, origin:"operator"})
- Modify: `web/src/App.tsx` (team chat composer + steer bubble rendering)
- Test: `internal/server/teamop_test.go` (supplement mapping)

- [ ] Step 1: Go test — op supplement maps method/params; cap error propagates reason_code `supplement_cap_reached`.
- [ ] Step 2: FAIL, implement.
- [ ] Step 3: App.tsx: when the team conversation has a running task (teamView snapshot, status running), composer shows `[STEER]` badge and send routes to supplement instead of add_task; steer bubble renders `{content, receipt}` with states pending (in-flight) / `delivered to <assignee> (n/3)` / `delivered_as_inbox`; receipt from the RPC response; cap error renders ReasonCard.
- [ ] Step 4: Tests + build PASS. Commit `feat: mid-run steering via teams.supplement with receipts`.

### Task 5: Wakeup cards (F4)

**Files:**
- Modify: `internal/server/teamhandlers.go` (op `wakeup_create` -> teams.wakeup_create with nested rule; op `wakeup_now` builds kind=at at=now)
- Modify: `web/src/TeamPage.tsx` (blocked-task card with arm form)
- Test: `internal/server/teamop_test.go` (wakeup mapping)

- [ ] Step 1: Go test — wakeup_create nests rule {kind, at/every, on_timeout, expires_at}; wakeup_now sends at=now; daemon error carries reason_code.
- [ ] Step 2: FAIL, implement.
- [ ] Step 3: TeamPage: tasks with status `blocked` render wakeup card (task title, blocked-since, arm form kind at/every + value + expiry + on_timeout select, `[Wake now]`, `[Cancel task]`); armed count `n/8` rendered only when `t.metadata.wakeups` present (absent today — hidden, not zero).
- [ ] Step 4: Tests + build PASS. Commit `feat: wakeup arm/fire cards for blocked tasks`.

### Task 6: Presence dual-axis badges (F5)

**Files:**
- Modify: `web/src/TeamPage.tsx` (member badge derivation + rendering)
- Modify: `internal/server/teamclient.go` (stuck-state alert in pollTeam)
- Test: `internal/server/teamclient_test.go` (stuck alert)

- [ ] Step 1: Go test — roster member with presence offline/paused and queued/running tasks -> snapshot alert `member X stuck (offline, N queued)`; online members never alert.
- [ ] Step 2: FAIL, implement alert derivation in pollTeam (member name match against task assignee; workload from task statuses).
- [ ] Step 3: TeamPage badges: availability axis (online/paused/offline from `presence`) x workload axis (working r/c when running tasks, queued n, idle); `offline + queued` renders distinct red stuck badge.
- [ ] Step 4: Tests + build PASS. Commit `feat: dual-axis presence badges with stuck-state alert`.

### Task 7: Worktree hardening (F6)

**Files:**
- Modify: `internal/server/orchestrator.go` (createWorktree: snapshot replay + hidden ref; failure preservation in finishDispatch error path)
- Modify: `internal/server/dispatch.go` (preserve marker on run error; result detail carries workdir)
- Test: `internal/server/orchestrator_test.go` (extend)

- [ ] Step 1: Test — temp repo with (a) uncommitted tracked modification + untracked file: worktree contains both; source repo working tree unchanged (git status clean as before, no stash entries); (b) ref `refs/console-tasks/<slug>` exists and equals worktree HEAD.
- [ ] Step 2: FAIL, implement: `git -C abs stash create` (dangling snapshot, may be empty) -> if non-empty `git -C wt checkout <snap> -- .`; untracked copy via `git -C abs ls-files --others --exclude-standard` (cap 200 files / 1MB each, skipped with log); hidden ref `refs/console-tasks/<agent>-wt-<unix>` = HEAD via `git -C abs update-ref`.
- [ ] Step 3: Test — dispatch failure with worktree isolation leaves worktree in place + `.console-preserved` marker file containing error text; failed dispatch row exposes workdir for re-dispatch.
- [ ] Step 4: Implement marker write in finishDispatch error path (worktree isolation only).
- [ ] Step 5: `go test ./... -count=1`, gofmt clean. Commit `feat: worktree snapshot replay, hidden refs, failure preservation`.

### Task 8: SPA rebuild + merge

- [ ] Step 1: `make web` (bun build into internal/server/webdist), `make build`, `make test`, `make vet`.
- [ ] Step 2: Full go test + web build green on the branch.
- [ ] Step 3: Merge to main (no PR needed per workspace flow: branch + test pass), push, rebuild running service via scripts/start.sh restart.
