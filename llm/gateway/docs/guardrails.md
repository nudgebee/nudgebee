# Guardrails — egress secret detection (DLP)

Status: ✅ built (secrets, egress) · 🟡 PII + per-tenant config are follow-ups

The gateway scans each **outbound request body** for secrets before it leaves to the
provider, so an org's own credentials (API keys, cloud creds, tokens) can't leak into a
prompt sent to a third-party LLM. It's a pure-regex + (future) entropy detector — no
external calls, a few hundred µs to low-ms per request.

The detector is **lifted from llm-server's `security/egressfilter`** (same rule corpus
and design), adapted to operate on the raw request text rather than langchaingo
messages. The comprehensive rule corpus (~22 patterns: AWS/GCP/Azure creds, GitHub/
GitLab PATs, Slack/Discord tokens, Stripe/Square/Shopify keys, JWTs, bearer headers, DB
URLs with passwords, kubeconfig cert data, npm/PyPI tokens, …) lives behind the EE seam;
the OSS build ships a 5-rule high-precision baseline.

## Modes

Set by `gateway_egress_filter_mode`. Off by default.

| Mode | Behavior |
|------|----------|
| `` (off) | no scan |
| `detect` | record the hit, don't modify or block — **the safe rollout default** |
| `enforce` | block the request (403, `secret_detected`) — a secret must not reach the provider · **EE** |
| `redact` | replace the secret span with `[REDACTED:<rule>]` and forward · **EE** |

**OSS is detect-only.** The active modes (`enforce`/`redact`) block or rewrite the
request — those are EE. On the OSS build they degrade to `detect` (a boot warning is
logged if configured otherwise); `off`/`detect` are unchanged. The EE build unlocks
them via `egressfilter.SetEnforcement(true)` (from `ee/egressfilter` init), mirroring
the rule-corpus seam. Per-tenant mode overrides live in the EE Data & privacy admin
surface (so per-tenant `enforce`/`redact` are inherently EE).

Recommended rollout: run `detect` first, watch what fires (usage `attributes.derived.dlp`
+ the `x-nb-llm-dlp` header), tune the **allowlist** for false positives, then (EE) flip
to `enforce` or `redact`.

## Signal + metering

On a hit, every mode sets an `x-nb-llm-dlp: <mode>:<rules>` response header and records
`attributes.derived.dlp = {mode, rules}` on the usage row. An `enforce` block is a
rejection row (`reject_reason=secret_blocked`, 403) that also carries the fired rules.

## Allowlist

Vendor-docs samples (e.g. AWS's `AKIAIOSFODNN7EXAMPLE`) trip real rules. Exact-match
values registered via `egressfilter.RegisterAllowedValues` are dropped from results —
essential before running `enforce`.

## Not in v1 (follow-ups)
- **PII** (emails, SSNs, cards) — the llm-server design stages PII as Phase 2/3; secrets
  are the highest-value, lowest-false-positive first cut.
- **Per-tenant mode/allowlist** — v1 is a single operator-level mode; per-tenant policy
  (like rate limits/routing) is the natural next step.
- **Ingress** (scanning the provider's response) — egress is the leak vector that matters.
