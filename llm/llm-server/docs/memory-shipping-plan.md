# Memory Architecture — Shipping Plan

What to ship, in what order, and why. Decisions captured so we don't re-litigate.

---

## Decision: what counts as "bare minimum" memory

A useful memory product for end users needs at least two of these three capabilities:

1. **Identity across conversations** — agent recognizes the user and honours their style/preferences next time.
2. **Learning from chat** — agent picks up facts the user states, no forms, no API calls.
3. **Continuity within a conversation** — agent holds active task / target across turns.

Shipping none of these = no perceived memory.
Shipping only (1) without (2) = nobody will ever populate it.
Shipping (1) + (2) = real memory product; long-conversation seams may surface later.
Shipping (1) + (2) + (3) = the experience people expect from a memory system.

---

## Phase-to-capability map

| Capability | Phase that delivers it |
|---|---|
| Identity storage (Soul + Preferences) | **1** |
| Learning from chat (auto-extract) | **2** |
| Session continuity | **4** |
| Tenant governance | 3 (Policy) |
| Operational target carry-over | 3 (Account) |
| Live tenant state | 5 (Heartbeat + Signals) |
| Admin UI / CLI for tenant stores | 6 |
| Compose observability | 7 |

---

## Shipping order

### v1 — Phase 1 + Phase 2 (ship now)
**Delivers:** identity + auto-learning. Users get "the agent remembers me and learns from what I say."

**What's included:**
- Soul (user style / identity)
- Preferences (typed user settings)
- Compose (reads both, applies per-layer budgets, returns a rendered block)
- Executor bridge (appends block to every system prompt)
- Event log + async projection (auditable mutations)
- Feature-flag gating (`MEMORY_MODULE` per-tenant)
- `/v1/memory_v2` RPC action (Soul/Prefs get/set/clear)
- Patterns / Decisions / Collective stores (populated by extractor)
- Classifier + extractor bridge (auto-writes on turn end)
- Migration FSM (`off | shadow | dual | cutover | retired`)

**What's explicitly not included yet:**
- Session store
- Policy
- Account context
- Heartbeat / signals
- Admin API
- Compose trace emitter

**Rollout:** flag defaults to off. Enable per-tenant via `MEMORY_TENANT_ALLOWLIST` env or DB feature flag. Start in `MEMORY_MIGRATION_MODE=shadow` (dual-write, read from legacy). Graduate to `dual` once confidence is high.

### v1.1 — Phase 4 (add next, quickly)
**Delivers:** long-conversation continuity. Prevents the "it forgot what we were debugging" seam.

Reason to ship fast follow, not v1: Session is self-contained, but users won't miss it on simple conversations. Add when we see history-window ceiling hurting or when we start building longer agent flows.

### v1.x — the rest, conditionally
Add only when a customer asks or a metric tells us we need it.

| Phase | Trigger to ship |
|---|---|
| 3 Policy | Enterprise / regulated customer requires governance |
| 3 Account | Users complain that Session doesn't carry target across conversations |
| 5 Heartbeat | Agent needs to know live tenant state without running a tool |
| 6 Admin API | Phase 3 or 5 has data worth managing via UI/CLI |
| 7 Trace | Need to tune per-layer budgets with real data |

Each is additive, flag-gated, and has no dependency on the others beyond what's already in v1.

---

## How to ship v1 (this PR)

### Branch
`feat/memory-phase-2` contains everything needed for v1. Phase-1 commits are ancestors of phase-2.

### Migrations
`V707, V708, V709, V710, V711, V712, V717` — all additive, applied by the golang-migrate Helm job on deploy.

### Env vars (defaults keep memory OFF)
```bash
# Master switch
MEMORY_MODULE_ENABLED=false            # flip to true when ready
MEMORY_COMPOSE_ENABLED=true            # tests override in-process; harmless default

# Per-layer toggles
MEMORY_LAYER_SOUL_ENABLED=true
MEMORY_LAYER_PREFERENCES_ENABLED=true
MEMORY_LAYER_PATTERNS_ENABLED=true
MEMORY_LAYER_DECISIONS_ENABLED=true
MEMORY_LAYER_COLLECTIVE_ENABLED=true

# Per-layer caps
MEMORY_SOUL_MAX_TOKENS=800
MEMORY_PREFS_MAX_TOKENS=600
MEMORY_PATTERNS_MAX_TOKENS=600
MEMORY_DECISIONS_MAX_TOKENS=800
MEMORY_COLLECTIVE_MAX_TOKENS=800

# Workers + cache
MEMORY_PROJECTION_WORKERS=4
MEMORY_CACHE_TTL_SECONDS=60

# Migration FSM (start in shadow)
MEMORY_MIGRATION_MODE=shadow           # off | shadow | dual | cutover | retired

# Rollout gating (empty env = DB feature flag decides)
MEMORY_TENANT_ALLOWLIST=               # set to <tenant-uuid> for pilot
```

### Rollout steps
1. Merge `feat/memory-phase-2` → `main`. CI auto-promotes to `test` → dev deploy runs.
2. The migrations Helm job applies V707–V712, V717 on dev.
3. Leave `MEMORY_MODULE_ENABLED=false` → prompts byte-identical to pre-change. Verify no regression.
4. Set `MEMORY_TENANT_ALLOWLIST=<pilot-tenant>` and `MEMORY_MODULE_ENABLED=true`. Restart llm-server.
5. Pilot tenant user sets a Soul marker via `/v1/memory_v2`.
6. User runs a chat turn. Check `llm_conversation_token_usage.prompt_messages` contains the `<user_style>` block.
7. Let auto-extract run for a week in `shadow` mode. Audit `llm_memory_events`.
8. Promote to `MEMORY_MIGRATION_MODE=dual` when extractor outputs look right.

### Rollback
- Flag off: `MEMORY_MODULE_ENABLED=false` → Compose returns empty → prompts unchanged.
- Per-tenant off: remove from `MEMORY_TENANT_ALLOWLIST` or disable DB feature flag.
- Migration back: set `MEMORY_MIGRATION_MODE=shadow` or `off`.

### Known open issue (not memory-caused)
**#28871** — token-limit handler misclassifies Bedrock 429 throttle as context overflow. Pre-existing. Memory E2E tests surfaced it. Not blocking v1.

---

## What v1 does not try to do

- **Does not learn Session state.** Each conversation runs with the existing history-window mechanism only. Long-turn continuity is "good enough" today; improved in v1.1.
- **Does not inject tenant policy.** Compliance-motivated customers need Phase 3; not part of v1.
- **Does not surface live operational state.** Agent still runs tools to check cluster health; Heartbeat (Phase 5) is later.
- **Does not expose admin UI.** Admin-facing changes are Phase 6 / later.

---

## How we verify v1 is healthy in dev

### Pre-merge gates (on the PR)
- `make lint` clean
- `make test` (fast unit tests) green
- `feat/memory-phase-2` E2E ladder already verified: 9/9 green on real LLM

### Post-deploy smoke test
```sql
-- confirm migrations applied
SELECT migration_name FROM public.hdb_catalog.schema_migrations
WHERE migration_name LIKE '%V7%memory%' OR migration_name LIKE '%V717%';

-- confirm feature flag row exists
SELECT value FROM public.feature WHERE value = 'MEMORY_MODULE';

-- after pilot tenant enabled + one chat turn
SELECT layer, action, created_at FROM llm_memory_events
WHERE tenant_id = '<pilot-tenant>'
ORDER BY created_at DESC LIMIT 20;

-- confirm prompt carries the block
SELECT id, agent_name,
       (prompt_messages LIKE '%user_style%') AS has_soul_block,
       (prompt_messages LIKE '%user_preferences%') AS has_prefs_block
FROM llm_conversation_token_usage
WHERE conversation_id = '<recent-conv>'
ORDER BY created_at DESC LIMIT 10;
```

Expected: every `k8s_debug` top-level call has `has_soul_block = true` (if soul seeded) after the pilot tenant turn.

### Ongoing metrics to watch (pre-Phase-7)
- `llm_memory_events` row count per hour (writes happening)
- `llm_memory_soul` / `llm_memory_preferences` row count per tenant (data accumulating)
- Legacy `llm_conversation_memory` row count per hour (should stay flat or decline in `dual` mode)
- Error rate of `context_memories_extractions` and `memory_extractor` agents (should be unchanged vs pre-merge)

---

## Decision record

| Question | Answer |
|---|---|
| Is Phase 1 alone a shippable product? | No. It's the foundation. Users won't populate it via API. |
| Is Phase 1 + 2 a shippable product? | Yes. First real v1. |
| Is Session (Phase 4) blocking v1? | No, but ship in v1.1. |
| Are Phases 3, 5, 6, 7 blocking v1? | No. Ship as customer / metric demands. |
| Can we ship Phase 1 alone later than Phase 2? | No — Phase 2 writes into Phase 1 stores. They must ship together or 1 before 2. |
| Can we ship Phase 4 before Phase 2? | Technically yes, but users won't feel "it remembers" without (1)+(2). Order 1 → 2 → 4. |
| What's the safe rollout mode for v1 on dev? | `MEMORY_MIGRATION_MODE=shadow` for at least a week, then graduate. |

---

## Single source of truth

This document supersedes prior discussion. Open a PR updating this file if we re-plan — do not re-argue the ordering in chat.
