# Outbound Secret EgressFilter

> **Status:** Phase 1 implemented in `security/egressfilter/`. Tracking issue: #29894.
> **Audience:** llm-server contributors.

A small in-process pre-filter sits between every llm-server agent and the
configured LLM provider. It inspects each outbound payload, blocks calls that
contain plausible secrets, and emits audit + metric events.

This document covers the OSS package contract: what the egressfilter does, where
it is wired, and how to extend it. Heavier PII detection (personal data,
reversible tokenization) is a separate concern handled out-of-process by
ml-k8s-server; see #31273 and #31514.

## 1. Problem

llm-server forwards prompts, tool observations, conversation history, and
memory snippets to the configured LLM provider verbatim. Inputs come from
runbooks, ticket bodies, K8s manifests, log lines, and shell tool output —
content that can incidentally contain API keys, bearer tokens, AWS access
keys, kubeconfig material, DB passwords, GitHub PATs, and private-key PEM
headers.

Goals:

- Detect plausible secrets in the outbound payload **before** the LLM
  provider sees it.
- **Block** the call when running in enforce mode, or **log+audit** in audit
  mode.
- Keep latency overhead negligible (<5 ms p50; benchmarked at ~12 ms for a
  32 KB payload, ~1 ms for a typical runbook).
- Be off by default; opt-in via config; safe to roll out audit-first.

Non-goals for this package:

- Personal-data detection (names, emails, addresses, phone numbers, IPs).
  That requires NER and lives in a separate service.
- Reversible tokenization of detected content. The egressfilter's response is
  binary: pass-through or block.
- Inbound prompt-injection sanitization of tool results. Tracked separately.

## 2. Package layout

```
security/egressfilter/
  egressfilter.go        Mode enum, Hit, Redaction, Result, Scan
  regex_filters.go       baseline regex Filters
  registry.go            Filter interface + Register + RegexFilter / FilterFunc adapters
  llm_model_wrapper.go   WrapModel decorator over llms.Model
  event.go               FilterEvent + WithFilterEventReporter context plumbing
  errors.go              typed Error + ErrSecretsBlocked sentinel
  metrics.go             OTel instruments
```

## 3. The wrapper

`WrapModel(model, provider, modelName, enabled, mode)` returns an `llms.Model`
decorator. When `enabled=false` or `model=nil`, it returns the inner model
unchanged. When enabled, every `GenerateContent` call is intercepted:

1. Flatten the message slice into a single payload string. Tool calls,
   tool-call responses, and image-URL parts are all included.
2. Run `Scan(payload)` — see §4.
3. Resolve `Action` via `resolveAction(mode, result)` — see §6.
4. If `Action == ActionBlock`: return a typed `*Error` wrapping
   `ErrSecretsBlocked`. The inner model is never called.
5. If `Action == ActionAudit`: log + record metric + emit `FilterEvent`,
   forward the original payload unchanged.

The wrapper is installed at the LLM factory chokepoint
(`agents/core/llm_config.go:GetLLMModel`), so all call sites inherit it
automatically.

## 4. Detection

`Scan(text)` runs every registered `Filter` against `text` and returns a
`Result`. One registry backs the scan — every detector (regex rule, entropy
gate, future ML-backed) implements the same `Filter` interface:

```go
type Filter interface { Detect(payload string) []Hit }
```

Storage is `atomic.Value`-backed → lock-free, allocation-free reads on the
hot path; writes are copy-on-write under a mutex. Append-only at startup.

### Baseline rule selection

The baseline keeps rules whose match is unambiguous and whose presence in an
outbound LLM call is almost always a leak — patterns where the false-positive
risk on prose, code, or config text is vanishingly low. The current baseline
covers cloud-root credentials, long-lived API tokens, and PEM-format keys.
See `rules.go` for the exact list.

Rules requiring careful anchoring, length tuning, or context-sensitive
boundaries are intentionally left for extensions to register. The same goes
for entropy-style detectors — their threshold/length tuning is opinionated
and their false-positive rate is sensitive to the deployment's log shape, so
they are opt-in. Extension-registered entropy detectors should cap candidate
token length (current extension uses 512 chars) so multi-KB base64 blobs
don't burn O(n) Shannon-entropy compute per call.

## 5. Extending the rule set

Every detector is a `Filter`:

```go
type Filter interface { Detect(payload string) []Hit }
```

Self-hosted operators register additional Filters from any `init()` in a
blank-imported package. Two adapters cover the common shapes — `RegexFilter`
for regex-rule detectors and `FilterFunc` for plain function values:

```go
import (
    "regexp"
    "nudgebee/llm/security/egressfilter"
)

func init() {
    // A regex-based detector.
    egressfilter.Register(
        "internal-foo-token",
        egressfilter.RegexFilter(
            "internal-foo-token",
            regexp.MustCompile(`\bFOO_[A-Z0-9]{32}\b`),
        ),
    )

    // Or a function-based detector (entropy gates, heuristics).
    egressfilter.Register(
        "internal-heuristic",
        egressfilter.FilterFunc(func(text string) []egressfilter.Hit {
            // ... custom detection logic ...
            return nil
        }),
    )
}
```

A few rules in mind:

- Rule ids become metric labels and log fields. Keep them
  cardinality-bounded (no values, no tenant ids) and stable across deploys.
- The package does not expose a deregister API. The registered set grows
  at startup; it does not shrink at runtime.
- Duplicate ids are accepted at registration time and only deduplicated in
  `Result.RuleIDs()`.

## 6. Modes

| Mode | On clean | On hit |
|---|---|---|
| `ModeDetect` (default) | pass-through, no log | log + detect metric + `FilterEvent`, **forward unchanged** |
| `ModeEnforce` | pass-through, no log | log + block-metric, **return `*Error`** — only when an `ActionGate` is registered (see below) |

`ParseMode(s)` normalises `"enforce"` (any case, trimmed) to `ModeEnforce`;
the canonical `"detect"` and the legacy alias `"audit"` both resolve to
`ModeDetect`. Anything else also falls back to `ModeDetect` — wrong-string-defaults-to-safe-mode prevents a config typo from silently breaking the LLM call path.

### Mode → Action via `ActionGate`

The wrapper doesn't read `Mode` directly when deciding whether to block —
it asks `resolveAction(mode, result)`, which delegates to the package
variable `ActionGate`:

```go
var ActionGate func(mode Mode, result Result) Action
```

When `ActionGate` is `nil`, every `Mode` resolves to `ActionAudit` (the
detect-only path) and a one-shot WARN log fires if the operator
configured `ModeEnforce`. A policy provider that wants `ModeEnforce` to
actually block installs a gate from its own `init()`:

```go
egressfilter.ActionGate = func(mode egressfilter.Mode, _ egressfilter.Result) egressfilter.Action {
    if mode == egressfilter.ModeEnforce {
        return egressfilter.ActionBlock
    }
    return egressfilter.ActionAudit
}
```

This indirection lets the same wrapper code support detect-only and
detect+block deployments without conditional builds in the call path.

### Per-tenant overrides

On top of the env-level mode, a single DB-backed table
(`public.llm_egressfilter_tenant_config`) lets an admin override
behaviour for one tenant without restarting the server. The wrapper
calls `Resolve(ctx)` on every LLM call — when the ctx carries a tenant
id (attached at `tryWithModel` upstream of `GenerateContent`) and an
override row exists, the resolved `TenantConfig` is layered on top of
the env baseline before the action gate runs.

| TenantConfig field | Effect | Composition |
|---|---|---|
| `mode` | Overrides operator-configured `Mode` (`detect`/`enforce`/`redact`/`disabled`) | Replaces env mode |
| `enabled` | `false` → wrapper skips Scan entirely for this tenant | Replaces env enable |
| `allowlist []string` | Extra strings to drop from hits | **Additive** on env allowlist |
| `disabled_rules []string` | Rule ids to drop from this tenant's results | **Subtractive** on env-loaded corpus, per-tenant only |
| `custom_rules JSONB` | (Reserved column; no admin-API surface yet) | (Future) additive |

**Composition rule:** tenant config is additive on top of env baselines.
A tenant cannot weaken the env baseline by removing entries — they can
extend (allowlist) or selectively skip env-loaded rules
(`disabled_rules`), but the operator-installed corpus floor stays
intact.

The lookup is fronted by a 5-minute in-memory cache (`Resolve` →
`tenantConfigLoader` on miss). Admin API writes invalidate the local
process's cache; cross-pod broadcast is queued for a later phase. DB
unreachable → fail-open to env defaults (the security baseline keeps
working).

### Admin API

Endpoints under `/api/admin/egressfilter/tenant/:tenant_id` for CRUD on
the override row. Auth via the existing global `LLM_SERVER_TOKEN`
middleware.

```
GET    /api/admin/egressfilter/tenant/:tenant_id   # current override (404 if none)
PUT    /api/admin/egressfilter/tenant/:tenant_id   # full upsert
PATCH  /api/admin/egressfilter/tenant/:tenant_id   # additive patch (allowlist_add/remove, etc.)
DELETE /api/admin/egressfilter/tenant/:tenant_id   # clear override → env defaults take over
GET    /api/admin/egressfilter/tenants             # debug list (max 100)
```

PATCH applies removes first, then adds, so callers don't need to think
about ordering. Per-tenant budgets: ≤500 allowlist entries,
≤100 disabled_rules.

## 7. Errors

A blocked call returns `*Error` wrapping `ErrSecretsBlocked`:

```go
type Error struct {
    AuditID string   // short request-scoped id for log correlation
    RuleIDs []string // which rules fired, sorted
}
```

The user-facing message contains only the audit id, not rule names or matched
values. Callers can detect via `errors.Is(err, ErrSecretsBlocked)` and unwrap
via `egressfilter.AsError(err)` to recover the audit id and rule ids for
logging.

`api/chains.go` translates the typed error into HTTP 400 with the audit-id
hint.

## 7b. Per-message events (UI surface)

In addition to the typed error (enforce only) and the structured log line
(both modes), every hit emits a `FilterEvent` that callers can collect and
persist alongside the message. This is the surface the UI uses to render a
per-message indicator without having to scrape logs.

```go
type FilterEvent struct {
    AuditID      string   // "egress-<12 hex>"
    Mode         Mode     // audit | enforce | (future redact / tokenize)
    PayloadBytes int

    Hits     []Hit    // per-detection detail (offsets + future Placeholder)

    // Derived aggregates, cached so UI consumers don't recompute.
    HitCount int
    RuleIDs  []string
}

type Hit struct {
    RuleID      string `json:"rule_id"`
    Start, End  int    `json:"start","end"`     // byte offsets in serialized payload
    Placeholder string `json:"placeholder,omitempty"` // populated under future redact / tokenize
}
```

The wrapper invokes a callback registered on the request `context.Context`:

```go
ctx = egressfilter.WithFilterEventReporter(ctx, func(e egressfilter.FilterEvent) {
    // collect e for later persistence
})
```

The conversation handler attaches a reporter at the start of every turn,
accumulates events under a mutex (one message can produce multiple LLM
calls), and writes the slice to `llm_conversation_messages.metadata` under
the `egressfilter` key when the message is finalized:

```json
{
  "egressfilter": [
    {
      "audit_id": "egress-3a5ae6bf4317",
      "mode": "enforce",
      "payload_bytes": 6638,
      "hit_count": 1,
      "rule_ids": ["aws-access-key-id"],
      "hits": [
        { "rule_id": "aws-access-key-id", "start": 6591, "end": 6611 }
      ]
    }
  ]
}
```

The UI reads `message.metadata.egressfilter` and renders however it wants —
a banner, a badge, an icon, nothing. The shape is structured JSON, not an
inline marker in the response body, so JSON-typed responses are not
corrupted.

The `metadata` column was added in migration V761. It is **general-purpose
and JSONB-merged on write**, not overwritten: each per-message subsystem
owns one top-level key (`egressfilter`, future `pii_tokenization`, …) and
the DAO's `UpdateConversationMessageMetadata` uses Postgres
`COALESCE(metadata, '{}'::jsonb) || $2::jsonb` so a later writer can never
wipe an earlier writer's namespace. Don't reach for `SET metadata = $2` in
new writers — use the DAO method or replicate the merge.

### Reserved for the future redact / tokenize modes

`Hit.Placeholder` is populated when `Mode` is `redact` or `tokenize`. The
shape is identical to today's event — the future redact mode replaces each
hit range in place with a marker (e.g. `[REDACTED:secret]`) and stamps the
marker into `Placeholder`; tokenize emits a deterministic stable token (e.g.
`<NB_PII_PERSON_3>`) for the same purpose and additionally keeps an
**in-memory request-scoped map** `{placeholder → original}` for rehydrating
the response. The DB-persisted event captures only `Placeholder`; original
values never leave the wrapper.

## 8. Metrics

Five OTel instruments under the `nb_llm_egressfilter_*` prefix:

- `scans_total` — counter, labels `{status="clean|audit|blocked"}`,
  `{provider}`, `{model}`, `{mode}`
- `hits_total` — counter, label `{rule_id}` (cardinality-bounded; never
  carries values)
- `blocks_total` — counter, labels `{provider}`, `{model}`
- `latency_seconds` — histogram of Scan + dispatch time
- `payload_bytes` — histogram of serialized payload sizes

Label values come from the rule id, never the matched substring.

## 9. Configuration

The configuration is layered: a master switch gates the whole subsystem, and
per-detector flags sit underneath.

| Env | Default | Effect |
|---|---|---|
| `LLM_SERVER_EGRESSFILTER_ENABLED` | `true` | **Master switch.** When false, the LLM factory does not install the wrapper at all — no decorator, no metric emission, no payload serialization, zero overhead. Per-detector flags below have no effect until this is true. |
| `LLM_SERVER_EGRESSFILTER_SECRETS_ENABLED` | `true` | Per-detector knob for the secrets scanner. When master is on and this is true, `Scan` runs against every outbound payload. |
| `LLM_SERVER_EGRESSFILTER_SECRETS_MODE` | `detect` | One of `detect` or `enforce`. `ParseMode` accepts the legacy alias `audit` (treated as `detect`); any other string also falls back to `detect`. Whether `enforce` actually blocks depends on whether an `ActionGate` is registered (see §6). |
| `LLM_SERVER_EGRESSFILTER_ALLOWLIST` | unset | Comma-separated list of values to exclude from detection even if they match a rule. Typical use: vendor docs samples (`AKIAIOSFODNN7EXAMPLE`, `AIzaSyExampleKey...`) so enforce mode doesn't block prompts that quote documentation. Loaded once at startup; runtime changes require a restart. Match semantics: strict bytewise equality on the matched substring — no prefix, no regex, no case folding. Whitespace around each comma-separated entry is trimmed; empty entries are skipped. |

| Master | Secrets | Effect |
|---|---|---|
| `false` | any | Wrapper never installed. `GetLLMModel` returns the raw provider. |
| `true` | `false` | Wrapper installed but returns inner on every call (no scan). |
| `true` | `true` | Full scan per call; mode controls block vs audit. |

All values are read at LLM-factory time and baked into the cached wrapper.
Changing them requires either a process restart or
`InvalidateAllLLMClientCache()` to flush the cache so the next
`GetLLMModel` call re-reads config.

## 10. Threat model (in scope for this package)

| Vector | Today | After |
|---|---|---|
| Runbook contains hardcoded API key | Sent to LLM verbatim, logged in conversation history | Scanner hits a rule; in detect mode logged + metric; in enforce mode (with `ActionGate` registered) blocked with audit id |
| Tool output (e.g. `kubectl get secret -o yaml`) contains a base64 PEM header | Sent verbatim | Same as above |
| LLM-generated plan re-quotes a value the prior turn saw | Re-sent on next outbound call | Scanned on every call (no per-call caching of "this was already inspected") |
| Memory snippet from a prior turn carries a secret | Re-sent on every conversation turn | Scanned on every outbound call (memory at rest is out of scope here) |

Out of scope here (covered by other tracks):

- Personal-data detection in any of the above — needs NER, handled
  out-of-process. See #31273.
- Inbound tool-result sanitization (prompt injection in fetched content).
- Reversible tokenization of detected content. The egressfilter today is
  detect-and-decide; it does not rewrite payloads. A future redact mode
  (one-way, secrets) and a future tokenize mode (two-way, PII rehydration)
  reuse the existing `Hit.Placeholder` field; see §7b.

## 11. Performance

Benchmarks in `egressfilter_bench_test.go`:

- Disabled path (`WrapModel` returns inner): ~124 ns/op.
- Clean payload, baseline rules: ~1 ms on a typical runbook.
- 32 KB payload, baseline rules: ~12 ms.

Numbers are illustrative — exercise `make benchmark` for your machine.

## 12. Related work & follow-ons

- **#31273 + #31514** — out-of-process personal-data scrubbing service in
  ml-k8s-server, plus the llm-server-side wrapper that calls it. Different
  threat surface (PII, not secrets), different latency budget (network hop
  acceptable), and a different action (reversible tokenization, not block).
  Composes naturally with this package: the OSS egressfilter runs first
  (cheap, in-process, fast-fail on secrets); the remote layer runs after on
  payloads that pass. Writes its own `metadata.pii_tokenization` key (the
  JSONB merge contract means the two never clobber each other).
- **#29894** — umbrella tracking issue for this package and follow-ons.

Known follow-on work (not in this package today):

- **Rule corpus expansion (EE)** — 10 vendor-prefix rules added in
  PR #32644; bringing total EE coverage to 22 detectors. OSS baseline
  remains 5.
- **Redact / tokenize modes** — reuses `Hit.Placeholder`; see §7b.
- **Per-tenant policy** — DB-backed rule enable/disable per tenant.
- **Sibling `ingressfilter`** — same `Filter` interface applied at the
  tool-result aggregation point (`executor_planner.go ~line 521`) for
  prompt-injection defense on content fetched from external systems.
