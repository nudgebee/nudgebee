# NB AI Gateway — Architecture

Status legend: ✅ built + verified · 🟡 designed, not built · ⬜ future phase

## What it is

An everyone-facing platform service. Any org tool (Claude Code, an SDK, an app)
points its provider base URL at the gateway and authenticates with an **NB token**;
the gateway forwards to the real provider (Anthropic/OpenAI/Gemini/…) with **cost
attribution, identity, metering, rate limits, routing, and audit** — reusing NB's
existing auth/tenancy instead of a parallel identity system.

Design spine: an **NB Go edge (control plane)** in front, the **provider engine
(data plane)** behind a seam, so intelligence (P2/P3) drops in without a rewrite.

## Topology

```
client (ANTHROPIC_BASE_URL = https://<subdomain>/anthropic, NB token as key)
   │
   ▼  nginx ingress (dedicated subdomain) — routes straight to the gateway,
   │  bypassing Next.js. Streaming annotations REQUIRED:
   │    proxy-buffering off, proxy-request-buffering off,
   │    proxy-read-timeout/send-timeout 3600
   ▼
┌──────────────────────────────────────────────┐  nudgebee/llm-gateway (gin)
│ 1. auth mw      NB token → {tenant,user,token}│  ✅ user_auths sha256 lookup
│ 2. route        (rule engine) ✅              │  requested → resolved target
│ 3. capture      session + structure-only attrs│  ✅
│ 4. creds        Account injects provider key   │ ✅ per-provider / per-tenant
│ 5. passthrough  client.Passthrough(Stream)     │  ✅ native bytes, SSE, caching
│ 6. meter        usage → PG/CH sink (+ body log)│  ✅
└──────────────────────────────────────────────┘
   │  in-process (no sidecar)
   ▼
   embedded bifrost-core → provider (Anthropic / OpenAI / Gemini / cloud BYO)
```

## Why embed `bifrost-core` (not a sidecar, not a fork)

Decided after reading Bifrost source under NB's real constraints (multi-tenant,
per-tenant **cloud BYO** — Bedrock/Vertex own-keys — and multi-replica). See the
root `docs/architecture-decisions.md` (2026-07 entries) for the full rationale.

- **Not a sidecar:** OSS config/rate-limit are per-node (multi-replica drift);
  plugins load as fragile `.so`; and it means a second process.
- **Not a fork:** avoids maintaining a hot 2000-line file, and avoids depending on
  an unmerged branch (missing upstream fixes while it's open; stranded if it never
  merges).
- **Embed the published module:** we depend on `github.com/maximhq/bifrost/core`
  (v1.7.0) as a normal library — no fork, no `replace`. We implement the core
  `Account` interface (`nbAccount`): `GetKeysForProvider(ctx, provider)` returns the
  right credential per addressed provider (api-key or structured cloud BYO), and
  `GetConfigForProvider` supplies each configured provider's network config (with an
  optional base-URL override). The gin edge + a thin passthrough handler call
  `client.Passthrough(Stream)`, so **fidelity + per-tenant creds coexist**. The `ctx`
  passed to `GetKeysForProvider` is where per-tenant resolution lands.

## Request lifecycle

1. **Auth** (`auth/`) — extract the NB token from the provider api-key slot
   (`x-api-key` / `Authorization: Bearer` / `x-goog-api-key`), resolve to identity.
   Modes: `user_auths` (real PAT via `sha256(token)` lookup — aligns with api-server
   migration V780; the **secure default**), `static` (one configured token), `none`
   (no auth — an open proxy; refused at boot unless `gateway_auth_allow_insecure=true`
   is explicitly set, so a deploy can't fall back to it by accident). Fail-closed.
2. **Route** ✅ (`routing/`) — first-match rule engine maps `(identity + tier/model)`
   to a same-family target (alias/tier model + fallbacks); config-file + DB
   per-tenant rules; passthrough default; explainable decision captured. See
   `routing.md`.
3. **Capture** (`proxy/capture.go`) — session correlation (client-supplied only)
   and structure-only attributes (`actual` + NB-`derived`), never message content.
4. **Creds injection** (`engine/account.go`) — core asks the `nbAccount` for the
   addressed provider's key via `GetKeysForProvider(ctx, provider)`; the account
   returns the resolved credential (api-key or structured cloud BYO). This is where
   per-tenant BYO creds resolve.
5. **Passthrough** (`proxy/handler.go`) — wrap the raw request as a
   `BifrostPassthroughRequest`, call `client.Passthrough`/`PassthroughStream`,
   stream the response back with per-chunk flush. Original bytes preserved
   (prompt caching / tools / SSE verified intact).
6. **Meter + capture** (`metering/`) — one usage row per request; full bodies when
   body logging is enabled (off by default).

## The seam (how P2/P3 land without a rewrite)

The credential `Account` + the passthrough handler are the seam. Today they do
faithful passthrough + per-tenant creds. P2 (semantic routing / model substitution)
and P3 (learned routing) plug in at the same points — the routing decision picks a
different target, and (for substitution) a translation step converts the request.
The industry pattern is "gateway = govern layer, routing brain = pluggable"; our
seam matches it.

## Data model

- **`llm_gateway_usage`** ✅ — one row per request: identity (tenant/user/token),
  session_id, structure-only `attributes` (JSON), provider/model/method/path,
  status/latency/request-id, token usage (input/output/cache-read/cache-write),
  service_tier. Cost is **computed on read** by joining the existing
  `llm_model_pricing` catalog (input / cache-read / cache-write / output / long-ctx
  tiers) — nothing provider-derived; providers return tokens, not cost.
- **`llm_gateway_request_log`** ✅ — full request/response bodies, only when body
  logging is enabled; TTL + background soft-cleanup; linked to the usage row by id.
- Migrations live in the shared tree: Postgres at
  `api-server/migrations/migrations/app/…_V781_llm_gateway_tables.{up,down}.sql`
  and ClickHouse at `api-server/migrations/migrations/clickhouse/19_llm_gateway.*.sql`
  (applied by the central migrate job; CH only when `CLICKHOUSE_ENABLED=true`).

## Cross-cutting

- **Config** (`config/`) — viper + `.env`, mirrors llm-server key naming.
- **Sink** (`common/rdbms.go`) — PG or ClickHouse, driver-agnostic (CH via a
  registered hook). Lifted from llm-server. Metering write is async + batched;
  the sink DB connects lazily so a slow DB never stalls startup.
- **Metrics/traces** — otelgin spans, runtime metrics, sloggin request logs.
  Business metrics (Prometheus counters) are 🟡.
- **OSS/enterprise** — `//go:build enterprise` + blank-import registration ⬜.

## Phasing

- **P1 — routing + governance** (current): native passthrough, identity auth,
  metering, session/attributes, body logging, static routing ✅, rate-limits ✅.
- **P2 — intelligence** ⬜: semantic/content routing, model substitution (needs a
  lossy translation layer — the reason it's not P1).
- **P3 — learned routing** ⬜: adaptive/bandit routing brain behind the seam.

## Non-negotiables

Provider keys live server-side (client sends only the NB token); **body logging
off by default** (data custody); streaming failover only pre-first-byte; BYO-key
first (not resell); never hardcode credentials.
