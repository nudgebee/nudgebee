# NB AI Gateway — Routing Design

Status: ✅ built (task 6) — config-file + DB per-tenant rules, endpoint-scoped,
explainable. Semantic/learned/cross-provider routing remain P2/P3. This captures the
decisions the build follows.

## Landscape (why we designed it this way)

**No standard exists** for policy/config routing across SaaS providers — every
gateway invents its own DSL. Two different problems get called "routing":

- **(i) across external SaaS APIs** (Anthropic/OpenAI/Gemini) — cost / fallback /
  quality. *This is NB's problem.* (LiteLLM, Portkey, OpenRouter, Kong.)
- **(ii) to self-hosted inference** (vLLM/llm-d) — KV-cache / LoRA / queue-aware.
  The **K8s Gateway API Inference Extension** (GA'd, CNCF, 2026) standardizes this.
  *Not ours — watch only.*

The only de-facto interface standard is the **OpenAI-compatible API**, which we
already honor via passthrough.

**How others do it — three tiers:**
- **A. Static / config** (P1): model groups/aliases, weighted load-balance,
  latency/usage/cost/health selection, ordered fallbacks, metadata-conditional
  rules. (LiteLLM strategies; Portkey conditional routing on request metadata;
  Kong load-balancer algorithms + semantic match.)
- **B. Semantic / content**: classifier/embedding routes by *what the prompt is
  about* (vLLM Semantic Router, Kong semantic match).
- **C. Learned brain**: RouteLLM, Not Diamond, bandit online-learners (BaRP/PILOT)
  — a *separate, pluggable* routing brain, not a gateway.

**Load-bearing insight:** the gateway is the *route-and-govern layer*; the routing
*brain* is separate and pluggable. Our `Account`/passthrough seam matches this.

## P1 model (what we will build)

A deterministic **rule engine**, evaluated at the edge before the creds hook:

```
match:   identity (tenant / user / token)  +  tier/alias/model  +  request attrs
target:  provider + deployment/key selection  +  ordered fallback chain  +  weight
```

Config-driven (file → metastore per-tenant), hot-reloadable. Stateless rules first;
usage/latency/cost strategies need shared Redis state (task 5) and land after it.

### The workload hint = the model/alias (NB's edge)

LLM provider APIs have **no native "route by complexity" field** (`reasoning_effort`
/ `thinking` budget are model-behavior knobs, not routing hints). A router gets the
hint three ways: (a) a **logical model / alias = the tier** (`fast`/`smart`/
`nb-reasoning` → concrete model — the most common pattern); (b) caller metadata /
header; (c) router-**inferred** complexity (Tier C).

**NB's differentiator:** most gateways must *infer* the workload class; **NB's
agents already know the tier** (reasoning / retrieval / summary). So NB callers send
an **explicit tier** (a header like `x-nb-tier` or a logical alias) and skip the
guessing. External tools express intent by the concrete model they pick.

### Fidelity constraint (P1 vs P2)

P1 routing only does what keeps bytes **faithful**:
- pick the provider **deployment / key / region**,
- ordered **fallback** (only fires on failure — cache-neutral),
- **same-family aliasing / tier resolution** (`/anthropic` + `nb-reasoning` →
  `claude-opus-4-8`).

**Cross-provider substitution** (`claude → gpt`) needs a lossy translation layer →
**P2, not P1.** So tier/alias routing is P1 *only when the alias resolves within the
addressed provider family*; cross-family is P2.

### Endpoint scoping — routing "lanes" (P1)

**Routing in P1 is confined to the provider family of the endpoint the client hits.**
The endpoint fixes both the request *format* and the provider *family*:

| endpoint  | format          | routes only within |
|-----------|-----------------|--------------------|
| `/anthropic` | Anthropic     | Anthropic |
| `/openai`    | OpenAI        | OpenAI |
| `/genai`     | Gemini        | Gemini |

Think of the endpoints as **lanes**. P1 routing picks which *car in the same lane* —
which deployment/key/region, a same-family alias/tier (`nb-reasoning → claude-opus`),
or a fallback car on failure. **Switching lanes** (Anthropic → OpenAI) requires
translating the request body between schemas — the lossy layer — which is **P2**.

Consequences:
- A rule matching `/anthropic` requests can only **target Anthropic**. A rule like
  `match: {provider: anthropic} → target: {provider: openai}` is cross-family and is
  **rejected at config-validate** (*"cross-provider routing requires translation =
  P2"*). Such a request stays in-lane (passthrough or same-family resolution).
- Rules are evaluated for **every** endpoint — global when `match.provider=""`, or
  endpoint-specific when set — but the resolved target is **always same-family in P1**.

**Client model/alias:** the model is a normal request field any client sets — Claude
Code (`--model` / `ANTHROPIC_MODEL` / `/model`), SDKs (the `model` param), curl. So
aliases/tiers work from any client; they just resolve **within the addressed provider
family** in P1. (Using `nb-reasoning` from Claude Code on `/anthropic` resolves to an
Anthropic model — not OpenAI.)

**The provider-agnostic tier** ("reasoning → whichever provider is best, regardless of
endpoint") is **P2**: a **unified endpoint** (one format in) + translation to the
chosen provider. Native endpoints (`/anthropic` …) stay faithful/in-family; a future
unified endpoint opts into translation + cross-provider routing.

### Caching interaction (critical for NB)

NB relies heavily on **provider prompt caching** (Anthropic `cache_control` etc. —
supported today via passthrough, metered as cache-read/write). **Provider prompt
cache is scoped to a specific (API key + model + region).** So any routing that
*spreads* requests across a pool (round-robin / weighted / random) **fragments the
cache** → cache miss, full price, lost hit rate.

Mitigation — **cache affinity**:
- **Default: single target** per (tenant, provider) — most tenants are BYO single
  key → no spread → cache preserved for free.
- **Pools use prefix-hash consistent hashing** — hash the cacheable prompt prefix
  (system + tools), route the same prefix to the same key/deployment. This is how
  llm-d / GKE Inference Gateway / SGLang do it; it **does not require session_id**
  (clients rarely set it). Affinity key: prompt-prefix-hash → identity(tenant+user)
  → session_id (bonus).
- Gateway-side *response* caching (semantic/exact) is a **separate** future feature,
  not routing, and lower value here (provider cache is cheaper, no staleness risk).

### Explainable routing — decision capture (hard requirement)

A router that silently changes the request is a debugging, trust, and cost problem.
Every request captures a **RoutingDecision**:

```
RequestedModel / RequestedProvider   (what the client sent)
ResolvedModel  / ResolvedProvider    (what actually ran)
RuleID
Reason:  passthrough | alias | tier | fallback | load_balance
FallbackChain + trigger              (e.g. "anthropic 529 → deployment B")
Strategy
```

Captured on **every** request (`reason=passthrough` when unchanged), in three places
(plumbing already exists):
1. **Metering row** — structured columns (`requested_model`, `requested_provider`,
   resolved = existing `model`/`provider`, `routing_reason`, `routing_rule`).
   Audit query: `WHERE routing_reason != 'passthrough'`; measure cost/quality impact
   of fallbacks per tenant.
2. **`attributes.derived.routing`** (JSON) — full detail. The routing decision is the
   canonical *derived* signal — exactly why the `actual` + `derived` split exists.
3. **OTel `routing` span** — per-request trace for the tracing UI.

## Schema & storage

Canonical Go types: `routing/types.go`. A rule is `match → target`:

```jsonc
{
  "id": "reasoning-tier",
  "tenant_id": "",          // "" = global default rule
  "priority": 10,           // lower evaluated first
  "enabled": true,
  "match":  { "provider": "anthropic", "model": "nb-reasoning", "user_id": "" },
  "target": {
    "provider": "anthropic",          // P1: same family as match (fidelity)
    "model": "claude-opus-4-8",        // "" = keep requested
    "key_ref": "",                     // named credential/pool; "" = tenant default
    "weight": 0,                       // weighted LB within a pool
    "affinity": "prefix_hash",         // "single" (default) | "prefix_hash"
    "fallbacks": [ { "key_ref": "pool-b" } ]   // ordered; tried on failure only
  }
}
```

**Match** fields are "any" when empty. **Target.provider** must be the same family as
the addressed provider in P1 (cross-family = P2, rejected at config-validate time).

### Two-tier storage (mirrors how NB config works)

1. **Config file (YAML) — global defaults + dev.** Path via `GATEWAY_ROUTING_CONFIG`;
   loaded at boot into `[]Rule` with `tenant_id=""`. Works with **no DB**.
2. **Metastore table — per-tenant rules (canonical).** Postgres only (routing rules
   are not in the CH sink), so `jsonb` is fine:

   ```sql
   CREATE TABLE llm_gateway_routing_rules (
       id         uuid PRIMARY KEY,
       tenant_id  text        NOT NULL DEFAULT '',   -- '' = global
       priority   int         NOT NULL DEFAULT 100,  -- lower first
       enabled    boolean     NOT NULL DEFAULT true,
       match      jsonb       NOT NULL DEFAULT '{}',
       target     jsonb       NOT NULL,
       created_at timestamptz NOT NULL DEFAULT now(),
       updated_at timestamptz NOT NULL DEFAULT now()
   );
   CREATE INDEX idx_llm_gw_routing_tenant ON llm_gateway_routing_rules (tenant_id, priority) WHERE enabled;
   ```

Per-tenant rules cached in-memory with TTL + invalidation (llm-server's config-cache
pattern). Precedence: **DB tenant rules → DB global rules → config-file rules →
passthrough default**.

### Resolution algorithm

1. Gather applicable rules: config-file globals + DB (tenant + global).
2. Sort by (tenant-specific first, then `priority` asc).
3. First rule whose `Match` matches (`provider` matches addressed, `model` matches
   the requested model *or an alias/tier token* or is any, `user_id` any/matches) →
   apply `Target`. Determine `Reason` (alias/tier/load_balance).
   Config-validate **rejects** any P1 rule whose `target.provider` (or a fallback's
   provider) differs from `match.provider` — cross-provider routing is P2.
4. No match → **passthrough** (resolved = requested, `Reason=passthrough`).
5. On the actual call, a failure walks the `fallbacks` chain (`Reason=fallback`).
6. Emit the `Decision` (requested vs resolved + rule + reason + chain) to the usage
   row, `attributes.derived.routing`, and an OTel span.

### P1 build order

Config-file rules first (proves the engine, no DB dependency) → DB DAO + per-tenant
cache → wire the `Decision` into metering/attributes/trace. Weighted/usage/latency
strategies need Redis (task 5) and land after it; static single-target + alias/tier +
fallback + prefix-hash affinity are stateless and ship now.

## Out of scope for P1

Cross-provider model substitution; semantic/inferred complexity routing; learned
routing brain; gateway-side response caching. All plug into the same seam later.

## Open decisions

- Rule/config schema shape (`match → target` DSL) — file first; metastore per-tenant.
- Build order vs Redis (task 5): static rules now, stateful strategies after Redis.
- Alias namespace for NB tiers (`nb-reasoning` / `nb-fast` / …) and per-tenant maps.
