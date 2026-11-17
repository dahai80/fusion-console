# Design v2: unified model-source registration + symmetric gateway presets

Date: 2026-11-17 · Status: decisions confirmed in conversation, awaiting final build approval
Supersedes: v1 (2026-11-17, macfabric-specific integration — replaced by unified design)
Branch: `feat/atlascode-install-latest-and-gateway-macfabric`

## 1. Intent

The user cannot find usable model configuration in the deployed console. Root
cause (code-verified): the model config UX is architecturally broken, not a
single bug. This design unifies model-source registration into ONE logic and
makes fusion-gateway / macfabric two symmetric "priority entry" presets, each
with console-managed lifecycle. Also folds in the atlascode `@latest` install
hardening (user decision 2026-11-17, unchanged from v1 §4).

## 2. Core principle (user-confirmed)

> 所有模型注册的逻辑都是统一的；fusion-gateway 和 macfabric 只是提供统一安装
> 和配置的优先入口。

Every model source is the same thing: an OpenAI-compatible (or Anthropic-
compatible) endpoint + API key + model list. Presets differ only in
**management attributes** (who installs/starts them), never in registration or
runtime logic.

## 3. Current-state defects (the case for the redesign)

1. **Chat is hard-wired to fusion-gateway**: frontend sends only the gateway
   provider id (`App.tsx:430`); direct providers are invisible to chat even
   when working (iff-local testcall passed, chat never offers it).
2. **No provider/model config UI in the main app**: provider CRUD renders only
   in Onboarding (one-time) and the Gateway page (only when gateway reachable).
   Gateway down ⇒ zero config surface.
3. **`provider.Model` triple-write**: chat brain select writes it globally
   (`changeBrain` → `saveProvider`), Gateway brain card writes it, new session
   picker would too — needs a single owner.
4. **Mental model fragmentation**: direct provider (Onboarding) vs gateway
   backend (Gateway page) vs brain model (Chat header) — three concepts for
   "configure a model".

## 4. Architecture after the change

```
┌ Registration layer — Models settings page (first-class tab) ─────┐
│ All sources uniformly: { name, endpoint type (openai|anthropic), │
│   base_url, key (keychain ref), model pool, default model }      │
│ Add/edit/delete = live (s.cfg hot-updated, no restart)           │
│ Presets = registration templates + lifecycle entry:              │
│   ◆ fusion-gateway (all platforms) : prefill :11432;             │
│       not installed → console downloads release binary;          │
│       not running → console starts it (detached, log+pid).       │
│       Management plane: Gateway page (backends, key issuance).   │
│   ◆ macfabric (macOS Apple-Silicon ONLY — noted in UI) :           │
│       prefill :8999; not running → launchctl kickstart           │
│       com.macfabric.gateway; not installed → hand off to         │
│       MacFabric's own installer (dmg / release-install.sh when   │
│       present locally). Console manages detect/start/stop only.  │
│   ◆ Custom: manual fill, no lifecycle management.                │
└──────────────────────────────────────────────────────────────────┘
        ↓
┌ Selection layer ─────────────────────────────────────────────────┐
│ Chat: per-conversation provider+model (new conversations inherit │
│   the global default chat model; switching is per-conversation,  │
│   never writes back to provider defaults)                        │
│ Workers/agents: worker.model > agent.model > provider default    │
│   (unchanged, PR #9 chain)                                       │
│ Orchestration brain = the conversation's selected model          │
└──────────────────────────────────────────────────────────────────┘
        ↓
┌ Execution layer (already uniform, minimal change) ───────────────┐
│ buildProvider: OpenAI-compatible endpoints share one SSE path;   │
│   anthropic_compat is a distinct wire protocol (endpoint type,   │
│   orthogonal to which gateway). fusion-gateway's admin API only  │
│   powers its management-plane UI.                                │
└──────────────────────────────────────────────────────────────────┘
```

## 5. Risks and their handling (from the coupling review)

**High:**
- **Onboarding assumption chain** (`Onboarding.tsx:383-389` hardcodes "prefer
  type=gateway provider"): must be refactored in the same change to link/embed
  the new Models page; first-run must succeed with a direct-provider preset.
- **DB migration**: conversations table gains `provider_id`, `model` columns.
  `migrate()` has only CREATE-IF-NOT-EXISTS today — add an idempotent
  check-schema-then-ALTER pattern for the new columns.

**Medium:**
- `provider.Model` single-owner rule: it means "this provider's default model"
  (workers/agents keep consuming it). Chat selection never writes it. Gateway
  brain card is removed; its "global default" role moves to the Models page
  ("default chat model" setting).
- Provider delete must clean its keychain entry (confirm when run history
  exists); key material is never returned to the frontend.
- Onboarding must not keep a second copy of the config forms (that duplication
  is the current disease).

**Low (verified non-issues):**
- Orchestration brain loop uses the request's provider object — follows the
  conversation model automatically.
- Worker dispatch + atlascode settings.json forcing consume the unchanged
  default-model chain.
- Cost attribution `chat:<provider>` naturally supports multi-provider.
- Same-origin SPA embed: no API version skew.

## 6. macfabric lifecycle (macOS-only, user-noted)

- Service: launchd/XPC label `com.macfabric.gateway`, uv venv at
  `~/Library/MacFabric/prod`, dual-protocol gateway on :8999.
- Console capabilities: detect (port probe + launchctl label), start/stop via
  launchctl, health read-out in the Models page entry.
- Fresh install: MacFabric ships as a dmg with its own installer; console does
  not reproduce that installer — when absent, the preset entry shows
  "requires one-time MacFabric install" with the local installer script
  (`~/Library/MacFabric/prod/scripts/release-install.sh`) executed only if
  present.
- OPEN QUESTION for the user: on a fresh Mac, where should the console point
  for the MacFabric dmg (public release URL? internal channel?) — until
  answered, the preset's install affordance is guidance, not automation.

## 7. Frontend changes

1. **New Models settings tab** (first-class): source list + add/edit/delete,
   type dropdown with presets, model-pool editor, testcall per source, "set as
   default chat model". Key input writes via existing `/api/settings/apikey`.
2. **Chat page**: header selector lists every enabled source's models
   (gateway items annotated `· gateway`, unreachable ones greyed but direct
   items always usable — chat availability never depends on a gateway). The
   selection is per-conversation; `POST /api/chat` accepts provider_id+model
   per conversation.
3. **Gateway page** slims to: fusion-gateway lifecycle (install & start /
   start / stop) + backends + key management. Brain-model card removed.
4. **Onboarding**: providers step links to the Models page instead of owning
   forms; success checks accept any enabled provider.

## 8. Part carried over from v1 unchanged

atlascode install hardening (§4 of v1): `@latest` per user decision, registry
ladder npmmirror → official with retries and 5-min budget, post-install
version capture vs minimum-known-good table (atlascode 0.1.121), claude/codex
stay pinned (my call, flip-able). The gateway one-click install chain
(binary → config generate-once with random secrets → detached start → health
→ register backends → provision key) lives under the fusion-gateway preset.

## 9. Acceptance scenarios (from the interaction walkthrough)

1. Fresh model source: Models page → Add → macfabric preset → key confirm →
   model list auto-fetch → testcall → set default → chat works. No config
   file, no restart.
2. Mid-conversation model switch takes effect on the next message, scoped to
   the conversation.
3. Adding a custom OpenAI-compatible source = fill URL+key → test → done.
4. fusion-gateway absent: preset entry offers install & start; after start its
   `backend/model` items appear in the chat selector.
5. Gateway down: gateway items greyed, direct models unaffected.
6. Agent dispatch chain (@worker, model precedence, atlascode forcing, cost
   rows) behaves exactly as after PR #9 — regression line.

## 10. Testing

Unit: idempotent column migration; registry ladder (injectable runner);
version compare; gateway lifecycle state machine against a fake binary/port;
macfabric launchd detect/start behind an injectable launcher; chat per-
conversation model persistence. E2E on this host: scenario 1, 2, 5, 6 for
real (iff-local + fusion-gateway install). CI unchanged + new tests.

## 11. Delivery

Same route as PR #9: fork branch → CI green → PR to dahai80:main → merge →
rebuild + redeploy local console.
