# Memory Architecture

What the problem is, what the new shape fixes, and how we verify — at the architecture level.

---

## 1. The problem

The current memory system has **one shape and one retrieval mode**.

- **Shape:** a single untyped pool of notes. Every kind of thing the agent wants to remember — preferences, patterns, runbooks, investigation results, facts — is stored with the same structure and distinguished only by a string label.
- **Retrieval:** cosine similarity. The agent submits the current query, gets back the top N closest notes across all labels, and injects them into the prompt.

That's the entire architecture. Flat, untyped, similarity-driven.

### The concepts the architecture is missing

| Concept the agent needs | Present in current architecture? |
|---|---|
| User identity (who is this person?) | No — scope stops at account level |
| Team knowledge (what does the team know collectively?) | No — no tenant-level concept |
| Addressable retrieval ("give me this specific thing") | No — only similarity |
| Per-category budget (bounded prompt size) | No — size depends on retrieval luck |
| Governance / tenant rules | No — no place for them |
| Live operational state (what's on fire right now?) | No — agent polls tools each turn |
| Short-term session intent | No |
| Current operational target (active account / cluster) | No |
| Immutable episodic record of decisions | No — everything is mutable, mixed together |
| Audit trail for mutations | No — writes are destructive |
| Per-customer rollout gating | No — one global switch |
| Lifecycle per kind of memory | No — blanket deletion by age + usage |

Every row in that table means "one or more of these concepts has no home in the architecture."

### What this looks like in practice

1. **Personalization is a lottery.** User style lives in the same pool as bug reports and runbook notes, competing for top-N slots by accidental word overlap.
2. **Team knowledge is invisible.** One engineer's runbook stays in their scope; teammates re-discover the same fix.
3. **Prompts bloat unpredictably.** N hits × whatever length = unbounded. Token-limit failures surface as canned "internal error" responses (issue #28871).
4. **Compliance lives in human wiki pages.** Nothing in the runtime carries tenant rules.
5. **Agent is blind to live state.** No cached operational signals.
6. **Memories are accumulated, not used.** The "was this memory useful?" signal never fires (100% of rows show `use_count = 0` across the whole DB) — retrieval doesn't propagate usefulness back into the store.
7. **Duplicates compound at scale.** Same fact gets re-extracted conversation after conversation; one observation is stored 481 times in the current snapshot.
8. **Writes are destructive.** Bad extractions silently overwrite good state; no audit, no replay.
9. **Cleanup nukes indiscriminately.** The age-based TTL uses the broken usefulness signal as its gate — everything is slated for eventual deletion regardless of value.
10. **Rollout is all-or-nothing.** No path to pilot a change on one customer.

### Why this is an architectural problem, not a tuning problem

No amount of re-tuning similarity thresholds, TTL days, or extraction prompts fixes any of 1–10. Each of them is the absence of a concept in the architecture. You can't retrieve "the user's style" specifically when identity isn't a named concept. You can't enforce tenant rules when governance isn't a named concept. You can't cap prompt size predictably when there are no named categories to budget.

---

## 2. What the new architecture fixes

Replaces the single untyped pool with **nine named layers**, each answering a specific question the agent asks. Writes go through an event log; each layer is a projection with its own scope, lifecycle, and budget.

### The layers and the question each answers

| Layer | Scope | Question it answers |
|---|---|---|
| **Soul** | user | Who is this user? How do they like to be spoken to? |
| **Preferences** | user | What settings have they explicitly declared? |
| **Patterns** | user | What do they tend to do? |
| **Decisions** | user, per-conversation | What have we committed to this session? |
| **Policy** | tenant | What rules must the agent obey? |
| **Account** | account | What are we currently operating on? |
| **Session** | session | What's the short-term intent? |
| **Signals + Heartbeat** | tenant | What's live and on fire right now? |
| **Collective** | tenant | What does the team as a whole know? |

### Mechanisms that sit underneath

| Mechanism | What it changes architecturally |
|---|---|
| Event-sourced writes | Mutations are events; state is a projection. Auditable, replayable, reversible. |
| Per-layer token budget | Prompt size is the sum of caps, not the length of whatever retrieval returned. |
| Addressable retrieval per layer | Agent asks a specific question; it no longer searches. |
| Per-tenant feature gating | Rollout is a dial, not a switch. |
| Migration state machine (off → shadow → dual → cutover → retired) | Old and new coexist safely until cutover. |
| Intentional lifecycle per layer | Each category has its own decay / immutability / ephemerality rule — no blanket TTL. |

### Problem → fix mapping

| Problem (from §1) | Fix |
|---|---|
| No user identity | Soul + Preferences |
| No team / tenant knowledge | Collective |
| Similarity-only retrieval | Addressable per-layer reads |
| No governance | Policy |
| No live state | Heartbeat + Signals |
| No session concept | Session |
| No current operational target | Account |
| Decisions lost in the pool | Decisions (immutable, append-only) |
| Destructive writes, no audit | Event log + projections |
| Prompt bloat | Per-layer budgets |
| Broken usefulness signal | Lifecycle per layer |
| Duplicates at scale | Preferences overwrite by key; Decisions are immutable |
| All-or-nothing rollout | Per-tenant flag + migration FSM |

---

## 3. How we verify

A layered test pyramid. Each level proves a different architectural property.

| Level | Architectural property proved |
|---|---|
| Unit | Each layer renders and budgets correctly on its own |
| Integration | Each layer round-trips through its storage without leaking to other layers |
| Edges | Concurrency, erasure, overwrite, isolation — all layer-level invariants |
| Bridge | The executor pulls the slab and the agent sees it — wiring is correct |
| Race (`-race`) | Parallel fetch across layers is safe |
| Real-LLM single / multi-turn / extended | Memory block actually lands on every prompt; continuity holds across turns |
| Scenarios (4) | Soul update propagates; users are isolated; flag-off rolls back; cold-start works |
| Investigations (2) | Long real SRE arcs preserve identity + preferences across 8–10 turns with mid-session flips |

### What each level asserts, architecturally

- **Addressability** — Compose returns the exact layer requested, with expected shape.
- **Scope boundaries** — user A cannot see user B's soul; tenant A cannot see tenant B's policy.
- **Budget boundedness** — rendered slab never exceeds sum-of-layer-caps.
- **Event-sourced correctness** — every state change is reflected in the event log; projections rebuild the stores.
- **Flag gating** — with flag off, Compose returns empty; prompts are byte-identical to the pre-architecture baseline.
- **Migration safety** — in `dual` mode, old and new paths coexist; reads from new fall back to old on miss.
- **Continuity** — memory block survives across every turn of a multi-turn conversation.

### What the pass/fail signal is based on

- **Prompt capture**: the test asserts substring presence of the rendered layer block in what the LLM actually saw.
- **Layer invariants**: sentinel values seeded into the soul / prefs must appear in every prompt until explicitly changed.
- **Negative assertions**: stale values must not appear after an update; other users' values must never leak.
- **Response quality guard**: canned "internal error" fallbacks are flagged as failures even though the conversation-level status says success.

### Rollback verification

Three independently testable rollback paths, all covered by scenarios:

1. Global: module flag off → bridge returns empty → prompts unchanged from pre-architecture baseline.
2. Per-tenant: tenant removed from allowlist → that tenant's prompts unchanged; others still see the slab.
3. Migration: move FSM mode back to `shadow` or `off` → reads flow through the old path.

---

## 4. Current verification status

| Phase | Adds | Impl | Backend | Real-LLM E2E |
|---|---|---|---|---|
| 1 | Soul + Preferences + Compose + Observe + event log + executor bridge | done | green | 9/9 |
| 2 | Patterns + Decisions + Collective + classifier + migration FSM | done | green | 9/9 |
| 3 | Policy + Account | done | green | 9/9 |
| 4 | Session | done | green | 9/9 |
| 5 | Signals + Heartbeat | done | green | 9/9 |
| 6 | Admin API | done | green | 9/9 |
| 7 | Compose trace emitter | done | pending test | pending |

**54/54 real-LLM E2E tests green across phases 1–6.**

### Architectural regressions caught by the harness

| Regression | Where it was introduced | Caught by |
|---|---|---|
| Similarity SQL for Patterns had unresolved-type hazard | Phase 2 | Integration warnings surfaced during Compose |
| Parallel fetch across layers racing on shared trace state | Phases 2, 3, 4, 5 (once per new layer added) | `-race` test + E2E crashes under real load |
| Dead request-struct in admin API | Phase 6 | Static analysis |

Every regression was a violation of an architectural rule (safe concurrent fetch, addressable retrieval, clean contracts) — caught before reaching production.

### Known issue, not memory-caused

**#28871** — Token-limit handler in the shared LLM path misclassifies rate-limit errors as context overflow and gives up after one iteration. Pre-existing; the memory E2E tests just surfaced it because they exercise multi-turn flows end-to-end. Tracked separately.

---

## 5. Summary

| | Old architecture | New architecture |
|---|---|---|
| Number of concepts | 1 (a "note") | 9 (named layers) |
| Retrieval model | Similarity over the pool | Addressable per layer |
| Scope expressiveness | Account only | User / account / tenant / session |
| Prompt sizing | Whatever retrieval returned | Sum of per-layer budgets |
| Write model | Destructive inserts | Event-sourced projections |
| Governance | Nowhere | First-class layer |
| Live state | Nowhere | First-class layer |
| Session state | Nowhere | First-class layer |
| Team knowledge | Nowhere | First-class layer |
| Decision log | Mixed with everything else | Immutable, append-only |
| Rollout control | Global switch | Per-tenant flag + migration FSM |
| Audit | None | Event log, replayable |

The old system optimized for *"store everything, search it later."* The new one optimizes for *"put the right kind of context in the prompt, within a bounded budget, with named concepts the agent can address directly."*

Evidence-backed. Reproducible. Verified end-to-end.
