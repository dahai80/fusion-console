# RPC namespacing discipline

Adopted from Paseo's `docs/rpc-namespacing.md` (issue #100 N3): every
JSON-RPC surface this console extends uses dotted `namespace.request` /
`namespace.response` method-name pairs, never bare verbs.

## The rule

Any new method we add to a JSON-RPC surface — the harness sidecar
(`internal/harness`), the agent-studio proxy (console-side additions only;
the daemon's own methods are upstream-owned), or any future WS/RPC channel —
is named:

```
<namespace>.<request>          -> <namespace>.<response>
```

- **namespace** = the owning subsystem, one word, lowercase: `guard`,
  `chat`, `dispatch`, `team`, `memory`.
- **request/response** = the operation, lowercase, no extra dots. The
  response name exists even when replies ride the same JSON-RPC `result`
  field — the pair names the conversation, which keeps logs greppable and
  makes one-sided methods visible in review.

## Existing surface (grandfathered)

The harness sidecar protocol v1 is shared with fusion-harness upstream
(harness issue #110): its methods (`hello`, `pre_llm`, `post_llm`,
`tool_admit`, `report`) are fixed by that contract and are NOT renamed
here. New console-side additions to that protocol must still follow the
dotted rule and require an upstream issue first.

Console routes that proxy the agent-studio daemon (`/api/team/op` op verbs,
`teams.*` RPC methods) are upstream names — pass through verbatim.

## Examples

- A future sidecar method exposing token funnels per turn:
  `guard.turn_usage` / `guard.turn_usage_response`.
- A future daemon-extension method for memory outbox queries:
  `memory.outbox_peek` / `memory.outbox_peek_response`.

## References

- Issue #100 (adoption tracking)
- `../fusion-insight/docs/paseo-analysis-2026-11-22.md` §5.4 N3
