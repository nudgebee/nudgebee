# Memory goes into the prompt only when the question needs it

Status: built 2026-10-14 behind `LLM_MEMORY_PRESTEP_ENABLED` (default off), revision 5 — updated to what shipped (one LLM pre-step, text lookups, stated-beats-guessed in code). The RAG read spec (`memory-read-rag-spec.md`) is parked; it later replaces the substring lookups below without changing this design.

## The problem

Every user turn gets a `<user_memory>` block of up to ~1,700 tokens: style, all preferences, top-10 patterns, decisions, top-8 facts, session notes (`compose.go:226-231`; `memory_v2_bridge.go:78` always uses mode All). The "only if relevant" filter falls back to top-N by score when RAG is off or the question is empty (`compose.go:646`), and dev holds ~100 guessed pattern rows per stated one. The model is told to ignore what doesn't apply (`utils.go:619-629`); it often doesn't. "Get me pod count from nudgebee NS" still carries the user's default namespace, patterns and facts it never needed.

## The plan

**Always in the prompt, no lookup:** `<user_style>`; and `<session_working_memory>` from the second turn of a conversation.

**Everything else only when the pre-step asks for it.** Before the planner, one small LLM call reads the question and the last ~1,500 characters of history (no preference rows) and returns one list of search phrases. Code runs the phrases as text lookups and injects only the hits. An empty list → no memory block beyond style and session.

```
turn arrives
  ├─ always: style (+ session from turn 2)                      ┐
  ├─ pre-step LLM: question + last 2–3 turns                    │ in parallel
  │     → {search[]}: what to look for, in words                │
  └─ KB lookup (already runs here)                              ┘
  → run lookups → inject hits only → planner
  lookups return rows with source + confidence → code merges:
     stated or tenant row beats a guessed row at the same key
  pre-step fails or times out → inject stated preferences only (explicit + tenant)
```

**Pre-step rules (in its prompt):**
- Write, in words, what stored memory this question needs: a value it leaves unsaid ("default namespace", "which cloud"), a service, resource or problem it names ("checkout"), or a fact it would need ("checkout node pool"). Don't ask for a value the question already states.
- Ask for how-to-answer and how-to-behave settings when they could apply: "timezone" when the answer will contain a date or time, "confirm destructive" when the question changes something, "output format" for a report or summary. Not for a current reading ("pod count now").
- Empty when the question is self-contained.

**Reviewer check on dev**

| Question (user has `default_namespace: payments`, `confirm_destructive: true`, `timezone: Asia/Calcutta`) | Expected `<user_memory>` |
|---|---|
| "get me pod count from nudgebee NS" | style only; pre-step `[]` — **no** `default_namespace`, no patterns, no facts (verified 2026-10-14) |
| "get me pod count" | style + `default_namespace`; pre-step `["default namespace"]` (verified 2026-10-14) |
| "delete the stuck pods" | style + `confirm_destructive`; pre-step `["confirm destructive"]`; answer asks before deleting |
| "when did payment-api last restart?" | style + `timezone`; pre-step `["payment-api", "timezone"]` |
| "why is checkout slow?" (fact "checkout runs on node pool B" stored) | style + that fact; no unrelated patterns |
| "what's 2+2" | style only |
| second turn of any chat | plus `<session_working_memory>` |

## How we know it worked

| Measure | Bar |
|---|---|
| Memory tokens per turn (median, dev, 1 week), against the real floor (style + footer) | at least 60% lower than today |
| Preference applied when the question leaves it unsaid (golden read cases) | no worse than today |
| Preference not injected when the question states the value | ≥ 95% on the golden cases |
| Behaviour preferences present | 100% of top-level turns for users who have them |
| Fact reaches the prompt when the question names its subject (golden cases) | no worse than today |
| Pre-step failures (timeout or bad output) | measured; each falls back to stated preferences and is logged |
| p50 latency per turn on dev | measured before and after; tuned afterwards, not a gate for the first A/B |

<details>
<summary>Design detail</summary>

### Pre-step

- **Where:** `agents/core/executor.go` where compose starts (`:734-773`); same gate: top-level agent, memory on for tenant and request, not a sub-agent. Runs in the same goroutine group as the KB lookup.
- **Model:** the classification tier when configured, else summary tier (as `classify.go:240-249` resolves), temperature 0, `ThinkingLevelFastTask`. Not `core.Classify` itself (fixed labels only, `classify.go:131`); a small structured call with JSON output, one retry on parse failure. New prompt `prompts/default/v1/utilities/memory_prestep.yaml`.
- **Input:** `OriginalQuery` on a resumed follow-up else the question; the last 2–3 human/assistant messages from `messageHistoryFomatter` (`executor.go:482`). No preference rows: the pre-step works on text, not on keys.
- **Output:** `{"search": ["default namespace", "checkout", "checkout node pool"]}`.
- **Timeout:** 5 s (same as the KB pre-step, `config.go:1288`). Timeout or two bad outputs → stated preferences only (explicit user + tenant), no lookups; `slog.Warn` with a counter.
- **Not skipped for short questions.**

### Lookups

- Preferences: the user's and tenant's rows are read once; a row is picked when its key or value contains a phrase — as a substring, with separators removed ("timezone" finds `time_zone`), or with every word of a multi-word phrase present in any order ("loki time window" finds `loki_log_query_time_window`; keys are free-form and the extractor spells them differently per call).
- Tool and data-source settings are asked for by a prompt rule ("loki time window" for a log query, "metrics window", "default cluster"). Verified 2026-10-14: with a saved 6h Loki window, both Loki questions were answered over the last 6 hours.
- A pre-step timeout is the caller's budget, not an endpoint failure: `generateLLMContentWithRetry` returns at once when the caller's context is done, with no retries, no fallback chain and no circuit-breaker trip (before this, every pre-step timeout opened the pod's breaker for the primary model). Facts, patterns and decisions: each consented layer is fetched once, scoped to the asking agent module like the always-inject path (`TopForTenantScoped` / `TopForUserPerKindScoped` / `RecentForUserScoped`, 200 rows), and every phrase runs the memory_read substring filters (`narrow*`) over those rows in memory; hits are merged round-robin across phrases, ≤4 rows per layer. Later the parked RAG read, same interface.
- Rows come back with `Source` and `Confidence`. Merge rule in code: at the same preference key, explicit beats inferred and tenant beats inferred; a guessed row never replaces a stated one. (Today's overlay lets a guessed user row overwrite a tenant default, `compose.go:508-514`; this path does not.)
- Behaviour settings (confirmations, time zone, format) are asked for by the prompt rule when they could apply; a pre-step failure or timeout falls back to the stated preferences, which include them. A miss on a successful pre-step is the accepted risk until the rule-vs-slot taxonomy is decided (item 15).
- Preferences, style and session are loaded once by Compose, consent-gated as today; the pre-step and lookups reuse that load. No second read path.
- Scope and consent per layer unchanged (`memory_dao.go:144-181`); a layer the user opted out of is never looked up.
- No per-layer LLM rerank on this path.

### Prompt wiring

- `ComposeRequest` gains `Lookups`; a new `ComposeModePrompt` runs soul + session, then preferences by name, then the subject/search lookups. Existing Ambient/OnDemand/All stay untouched (they split by explicit vs inferred, a different axis).
- `<memory_index>` footer (`memory_v2_bridge.go:198-242`) emitted only when the injected list is non-empty.
- `_memory_refs` tool schema (`planner_react_4.go:163-165`): on at top level whenever memory is enabled for the conversation, so tool definitions don't change per turn.
- Fix `memory_consumption_rules.yaml:5,20`: `<soul>` → `<user_style>` (block name from `soul/render.go:16`).
- One sentence in `memoryReadWriteNudge`: facts, playbooks, patterns and decisions are not pre-loaded unless the question needs them; call `memory_read` with a subject to dig deeper.

### Flag and rollout

- `LLM_MEMORY_PRESTEP_ENABLED`, default false; off → today's behaviour. Dev A/B via the tenant check used by `RagEnabledFor`.
- `llm_memory_tools_enabled` on wherever this is on (sops change, follow-up item 3). `memory_read` is registered only for k8s, AWS, GCP, Azure and Datadog orchestrators today; log which agents call it and register it where used (follow-up item 16).

### Eval

- `llm/benchmark/memory_golden`, `--mode read`, ~24 cases from the reviewer table: stated vs unsaid preference (8), behaviour preference present (4), subject lookup (6), self-contained → style only (6). Seeded through `memory_write` for the dedicated test user; cleanup through the normal delete path.
- Tokens per turn and p50 from dev traces, one week before and after.

### PRs

1. Pre-step prompt + call + JSON parse + unit tests with a fake LLM; `<soul>` fix; read-mode eval baseline. No behaviour change until the flag.
2. `ComposeModePrompt`, bridge wiring, footer and `_memory_refs` fixes, nudge sentence, behind the flag.
3. Flag on for dev, measure, then tenant rollout.

### Not in this work

Rule-vs-slot key taxonomy (follow-up item 15); layer and shape definitions (item 12); RAG search by meaning (item 10); extractor and daily-job noise at the source; telling the user what was saved.

### Risks

- Pre-step leaves out a needed preference. Mitigation: "behaviour settings always" rule, fallback to stated preferences, golden cases, `memory_read` for the planner.
- Pre-step returns too much and the noise returns. Mitigation: per-layer caps stay, "empty when self-contained" rule, token bar.
- One LLM call before the planner. Accepted for the first A/B; p50 measured before and after and tuned then.
- `search` phrases rarely hit on substring until the RAG read lands. Accepted; subjects carry the load until then.

</details>
