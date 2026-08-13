# Design Spec v2: Code-Analysis Agent — Cost, Token & Run-Waste Reduction

Status: **Revised after adversarial review + local validation** · Scope: `llm/code-analysis`

v1 of this spec ranked prompt-caching and context restructuring first. Local
instrumentation then **falsified** parts of that plan (see §3), and a challenge
review re-scoped it: the dominant waste is **runs that go wrong** (auth flail,
discarded-correct answers, premium model in dev), not tokens-per-call. Token
efficiency work is kept, but behind flags and after the defect fixes.

## 1. Measured facts (evidence, not assumptions)

| Fact | Evidence |
|---|---|
| Prod runs `gemini-3-flash-preview`; real cost ≈ **$0.30/run avg, $0.79 max** | Loki `analysis_complete` per-run tokens × `llm_model_pricing` rates |
| The "$4.49 run" is **dev** on `gemini-3.1-pro-preview` (long-ctx tier $4/M); same tokens on flash ≈ $0.58 | per-call `llm_conversation_token_usage` + pricing table; math reconciles to $4.49 |
| History prefix is **already append-only & byte-stable** (hash-verified per call) | W1a diff instrumentation: `first_changed_index=-1`, `system_changed=false` on every call |
| Gemini **implicit** caching: cold for ~4-6 calls, then holds 90-95%; fleet avg ~68% | per-call `cache_hit_pct` curves (local + Loki) |
| Observations accumulate unbounded (0 → 118K chars in 13 calls); compaction trigger (140K tok / 40 msgs) never fires on normal runs | per-call `chars_tool_observations` |
| Repo-access failures (repo not found / dead credentials) cause 30-iteration multi-tool flail and garbage submissions | prod trace: 101 steps, clone→git→gh→find rotation; 0.1-correctness evals |
| Relevance validator **discards correct analyses** (false negative confirmed: its own reasoning acknowledged the answer was right, then replaced it with a placeholder) | live run + `agentic_analyze.go` gate branch |
| ~59% of completions hit the 30-iteration cap | `planning_complete` iterations distribution |

## 2. What shipped in this change

1. **Per-call token & accumulation observability** (`llm/genai_tools.go`):
   real `UsageMetadata` (prompt/cached/fresh/output) + per-role char breakdown
   (`chars_tool_observations`, `chars_system`, `chars_tool_schema`) logged as
   `LLM per-call token usage`, plus a history byte-stability diff
   (`history cache-stability diff`). Fixed `SetLogger` never propagating to the
   LLM client — per-call logging was dead code before this.
2. **Memory hierarchy: ledger injection + pressure-gated observation aging**
   (`planners/react_planner.go`, `planners/reflection.go`, `planners/ledger.go`):
   - **Ledger injection (always on):** the reflection-maintained investigation
     ledger — findings + citations **with verbatim snippets** — is appended to
     every main-loop prompt. It existed before but was never shown to the main
     loop. In the pinned A/B this was the best-quality arm (beat the no-ledger
     baseline: correctly named `generateContentWithGenAI`).
   - **Observation aging (ON by default, pressure-gated):** once the estimated
     prompt exceeds `OBSERVATION_AGING_BUDGET_TOKENS` (default 45K), tool
     observations that are (a) older than the recent-`OBSERVATION_RECENT_WINDOW`
     (default 3) AND (b) already distilled into the ledger by a reflection
     (distill-then-drop watermark) are replaced with self-describing stubs
     (`[ELIDED: full output of \`file_view planners/react_planner.go\` ...]`).
     Below the budget aging is a strict no-op — normal runs are byte-identical
     to aging-off, so the quality risk is confined to the long-tail runs where
     the O(N^2) resend already explodes cost and rots context. Deterministic
     (cache-safe); AI messages/ThoughtSignatures untouched.
   - **Reflection hardening:** per-observation distillation window 1200→4000
     runes; verbatim-identifier requirement (no paraphrased code symbols);
     explicit-instruction guard on `ready_to_submit` (unfinished enumerated
     directives = not ready — fixed a measured premature termination); JSON-only
     response reinforcement.
   - **Measured limit (why the pressure gate):** distill-then-drop can only
     preserve what reflection sees. A needle deep in a 15K-char read outside
     the 4K distillation window cannot survive aggressive always-on aging —
     measured as confabulated identifiers on a pinned precision question. The
     pressure gate confines that risk to runs that were already degrading.
3. **Ledger carry-over across phases and retry attempts** (`planners/ledger.go`
   `SeedFrom`/`AddOpenQuestion`, `planners/react_planner.go` `SetSeedLedger`,
   `agents/{orchestrator,code_fixer}_agent.go`): previously every `Plan()`
   wiped the ledger and each fixer attempt (up to 3) plus the specialist→fixer
   handoff restarted the investigation from step 1 — the "re-investigates and
   re-runs identical calls" waste in #32926. Now the specialist's ledger is
   handed to the fixer, each fixer attempt inherits the previous attempt's
   ledger, and reviewer feedback lands as an explicit open sub-question.
   Knowledge (findings/citations/open questions, deduplicated) is carried;
   conclusions (`answer`/`ready_to_submit`) deliberately are not — a new
   attempt must not inherit "done". Rides on the ledger injection above, which
   puts the seeded knowledge in front of the model from call 1.
4. **Fail-fast on unrecoverable repo access** (`planners/react_planner.go`):
   failed steps of repo tools matching not-found/credential-rejected patterns
   increment a cross-tool counter — strike 1 injects a hard "do NOT retry with
   any tool" instruction; strike 2 aborts the run with an actionable error.
   Kills the 30-iteration clone-flail class.
4. **Relevance gate made non-destructive** (`api/handlers/agentic_analyze.go`):
   a not-relevant verdict now **appends an advisory note** instead of replacing
   the analysis with the "Manual Review Required" placeholder; prompt tightened
   to reject only analyses of a *different* problem, not partial answers.

## 3. Falsified / dropped (do not re-try without new evidence)

- **"We bust our own cache" (v1 W1b: append-only restructure)** — falsified.
  Hash-diff proved the prefix is already byte-stable; implicit-cache flakiness
  is cold-start + provider nondeterminism, not mutation. W1b would be a no-op.
- **Tool-schema trim / per-mode tool removal** — dropped: schema is cached-
  prefix weight (cold-start only ≈ cents) and removing tools limits the agent.
- **"$4.49 is a dashboard artifact"** — falsified; cost is real (long-ctx tier).

## 4. Roadmap v3 — consolidated next phase (priority order)

| # | Change | Size | Where |
|---|---|---|---|
| 1 | **Reflection structured-output**: genai `responseSchema` for ledger JSON — kills the parse-failure class throttling aging/carry-over/termination | S | code-analysis |
| 2 | **Instruction-file pointer**: detect `AGENTS.md`/`CLAUDE.md`/`GEMINI.md`/`CONVENTIONS.md` at clone → ONE-LINE pointer in repo context; agent reads on demand via file_view (query-based, never injected wholesale, never executed) | XS | code-analysis |
| 3 | **Chat RaisePr intent**: explicit phrase ("raise a pr", "create a pull request") in a direct `@agent_code_2` message → `RaisePr=true` at the chat entrypoint (entrypoint-owns-intent rule); ack message confirms. Today chat can NEVER produce a PR | S | llm-server |
| 4 | **Model tiering** planning/execution/summary (`LLM_MODEL_PLANNING/EXECUTION/SUMMARY`, each falling back to the single model): specialist loops=planning; **fixer=execution with slim fresh context + leaner template** (Aider architect/editor pattern — editor gets plan text only, no history, no map); router/reflection/compaction/evidence/PR-assembly=summary. One model per loop, never mid-loop | M | code-analysis |
| 5 | **Tier forwarding + dev config**: `ForwardedLLMConfig` carries the 3 tier picks (llm-server already resolves `LlmTierModels`); dev drops 3.1-pro-for-everything → planning-only or flash. Biggest pure-$ change | S+config | llm-server + env |
| 6 | **Post-edit syntax gate** (parse-only, tri-state, advisory): after each `replace`/`write_file`, run a dependency-free parse check (`gofmt -e` for .go, `python3 -m py_compile` for .py; no matching parser → silently not-checked). NEVER runs project builds/linters/tests (workspace pod has no dependency builder — env-shaped failures must not exist). Failure appends ONE bounded observation; never blocks | S | code-analysis |
| 7 | **replace.go hardening** (Aider editblock lessons): near-miss "did you mean these actual lines" failure report; applied-edits bookkeeping ("don't re-send"); uniform-leading-whitespace-offset and `...`-ellipsis matchers. NO edit-distance fuzzy replace (Aider deliberately dead-coded it — too risky) | S | code-analysis |
| 8 | **Persistence across runs/follow-ups** — version-keyed knowledge, NEVER conclusions (the event-RCA-pinned-to-first-occurrence bug is the proven failure mode): (a) store the run's final ledger on the analysis record; follow-ups forward it → `SetSeedLedger`, stamped with repo HEAD ("from commit X — verify" when HEAD moved); (b) repo-knowledge cache keyed repo@HEAD (module roots, instruction-file locations) beside the existing clone cache; (c) long-term gotchas → emit to llm-server's existing memory system, no parallel store | M | both |
| 9 | **Telemetry**: dev/prod readout of aging activation, iteration saturation (was 59%), gate-advisory rate, fix-loop call counts; per-run token totals in `/status` so llm-server records them | XS | both |

Aider comparison findings backing items 4/6/7: architect/editor split (planner in prose, cheap
editor with fresh minimal context), default lint = tree-sitter/parse-only fatal-only (NOT
project toolchains), bounded reflection (max 3) with block-level failure reports. Aider does
NOT auto-read AGENTS.md/CLAUDE.md (manual `--read` only) — item 2 goes beyond it, following
the Claude Code / Codex / Cursor convention. Skipped from Aider: cache-keepalive pings (our
runs are continuous), edit-distance fuzzy matching (they dead-coded it themselves).

## 4b. Next steps (previous list, still valid)

- **A1 — Dev model config**: move dev's code agent off `gemini-3.1-pro-preview`
  to a flash-tier model (verify ALL override layers: env, helm, DB llm-config).
  Biggest single $ lever (~7.7×). Config-only.
- **B2 — Aging rollout**: set `OBSERVATION_RECENT_WINDOW=3` in dev, measure the
  per-call curve via Loki, then decide prod default. Target: last-call prompt
  ≤1.5× steady-state; billed input ↓≥30% on ≥15-call runs; no quality regression.
- **A4 — Re-measure iteration saturation** after fail-fast lands (most cap-hits
  looked like flail); only invest in convergence work if the rate stays >20%.
- **Gate telemetry**: track validator verdicts to quantify the false-negative
  rate now that verdicts are non-destructive.

## 5. Parked (with reconsider-if conditions)

- **Explicit Gemini `CachedContent`** — cold-start loss ≈ $0.03/run (flash) /
  $0.20 (3.1-pro). *Reconsider if:* prod default becomes a pro-tier model, or
  run volume grows ~10×.
- **Repo-map layer (Aider-style tree-sitter outline)** — genuinely improves the
  agent (navigate instead of grep) but is a large lift. *Reconsider if:* after
  fail-fast + aging, agents still read >10 files/run or stubbing hurts quality.
- **Working-set dedup for file reads** — cleaner than stubbing but constrained:
  Gemini-3 ThoughtSignature replay requires byte-stable FC history, so only
  content replacement is allowed, never message removal/reordering.
- **Stage-level model tiering** (PR body, reflection → cheap model) — after A1.

## 6. Validation & measurement

- Unit: `planners` (aging ×4, fail-fast patterns ×10), `api/handlers`
  (advisory ×2). `make check` green.
- Live smoke (local CLI, explore mode): real investigation completes, per-call
  events emitted, no ThoughtSignature/genai errors; dead-repo run aborts fast
  with the actionable error instead of flailing.
- Fleet: per-call events land in workspace-pod logs → Loki
  (`{namespace="nudgebee", pod=~"workspace-.*"} |= "LLM per-call token usage"`);
  per-run totals in `llm_conversation_token_usage` (joins to `llm_model_pricing`).

## Appendix: key code anchors

- `llm/genai_tools.go` — per-call usage log + history hash diff (post splice/reattach).
- `planners/react_planner.go` — `ageOldObservations` (send-site hook in `callLLM`),
  `isUnrecoverableRepoAccess` + cross-tool breaker in the step loop,
  `recentObservationWindowFromEnv` (default 0).
- `api/handlers/agentic_analyze.go` — `applyRelevanceAdvisory`, tightened
  validator prompt.
