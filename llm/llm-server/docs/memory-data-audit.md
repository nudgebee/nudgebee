# Memory Data Audit — Baseline & Per-Layer Investigation

Status: in progress. Owner: memory module. DB inspected: dev (shared Cloud SQL), 2026-07-03.

## Baseline requirement (agreed)

For **every remembered item, in every layer**, we must be able to see and verify its full
lifecycle — both *why it's there* and *whether it's worth having*. A layer only "passes" the
audit if all seven points are answerable for its items.

**Collection — why it's there**
1. **What** — the real content is stored (not an empty label).
2. **Why** — the reason/trigger it was collected.
3. **Evidence** — what the user actually said / what in the conversation supports it.
4. **From where** — the exact source: conversation_id + message_id (the turn).
5. **Correct / makes sense** — a human can judge it and reject it if wrong.

**Usage & impact — whether it's worth having**
6. **How used** — which conversations this specific item was *injected into*, and whether the
   LLM actually used it (vs. just sat in the prompt).
7. **Impact** — did using it *change or improve* the outcome (or harm it)?

**Point 0 — Validity (GATE):** is this a real item that *should* exist? (not fabricated/over-fired).
If Validity fails, the item shouldn't be there at all, so points 1–7 are **moot** — a correct
`conversation_id` on a fabricated decision is a valid link to an invalid item, NOT a pass. Score
Validity first; only score 1–7 as "green" when Validity holds.

### Scorecard legend
✅ answerable/works · ⚠️ partial/unreliable · ❌ absent · ⊘ moot (item fails Validity gate)

### Sequencing decision (2026-07-04)
Points **6 (how used)** and **7 (impact)** are **deferred to a single cross-layer phase** run
*after* points **0–5 are complete for every layer**. Rationale: 6/7 need shared infra (an injection
log + outcome/eval wiring) that's identical across layers, so building it once at the end beats
per-layer duplication. Per-layer work focuses on **0–5** (validity + collection provenance).

---

## Layer 1: Preferences

**What it is:** addressable `key = value` settings; injected into `<user_preferences>` every turn.

**Sample (dev, all users, 18 rows):** `default_environment="k8s-dev"`, `preferred_log_source="Loki"`,
`promql_style="Do not use the __CLUSTER__ label"`, `interaction_style="Terse, expert-level CLI…"`,
`on_call_handles="PAYMENTS_SENTINEL, CRASHLOOP_SENTINEL"`.
**Sources:** inferred = 15, explicit = 3. **Scope:** 18 user, 0 tenant. **Owners:** sundar 11,
nil-user 5, two others 1 each. **shiv:** 0 (extractor never emitted a `user_preference` event for him).

| # | Dimension | Status | Evidence from dev |
|---|---|---|---|
| 0 | **Validity (gate)** | ✅ | Items are real preferences (values are genuine config). Gate holds → 1–7 scoreable. |
| 1 | What (content) | ✅ | Values are real and sensible (config-like). Best-quality layer with Collective. |
| 2 | Why (reason) | 🟢 **impl** | Extractor emits `rationale`; stored in new `evidence` JSONB. (branch `fix/memory-layer-data-quality`) |
| 3 | Evidence (quote) | 🟢 **impl** | Extractor emits `evidence_quote` (prompt + `MemoryFact`); stored in `evidence.quote`. Migration V776 adds `evidence jsonb`. |
| 4 | From where (turn) | 🟢 **impl** | `conversation_id` always threaded + `message_id` now captured into `evidence.message_id`. |
| 5 | Correct / rejectable | ⚠️ | Values look right, but unverifiable (no evidence); **3 malformed keyless rows** (a sentence stored as value, no key). |
| 6 | How used (injection log) | ❌ | Injected every turn via `composeMemoryV2Block`, but **no persisted record** of which prefs went into which conversation. Only a transient log line. |
| 7 | Impact | ❌ | No attribution, no with/without measurement. Unknown whether any pref changed an answer. |

**Extra defects found**
- ~~`confidence` hardcoded `0.70`~~ — **FIXED** (branch `fix/memory-layer-data-quality`): the
  extractor now sets a real per-type score (`memoryConfidence`: pref 0.9 / pattern 0.8 / fact 0.7 /
  decision 0.5), carried on the event payload and read in projection instead of the `0.7` constant.
  A true LLM-emitted per-fact score remains a prompt-side follow-up.
- ~~keyless rows (extractor emitted a description instead of `key=value`)~~ — **FIXED** (decision C):
  DAO `Upsert` now trims + rejects empty/whitespace keys (covers extractor / direct / replay paths);
  the 2 stale keyless rows deleted (dev, 2026-07-04). B (key vocabulary) dropped — not feasible for
  open-ended prefs.
- ~~5 rows orphaned to the nil user~~ — **RESOLVED**: write path already attributes via security
  context + `Record` rejects empty user_id; the 5 stale inert rows were deleted (dev, 2026-07-04).

**Verdict:** the *values* are good, but the layer fails the baseline on **why (2), evidence (3),
partial source (4), and all of usage/impact (6, 7)**. shiv=0 is *correct* (nothing to infer), not
a defect.

**What passing looks like (acceptance criteria)**
- Every inferred pref shows: `key`, `value`, `rationale` (why), `evidence_quote` (user's words),
  `conversation_id` + `message_id`, and a real `confidence`.
- Keyless/malformed writes are rejected or normalized.
- An audit surface can list a pref with its evidence and a keep/reject action.
- A usage log records which prefs were injected into which conversation (for point 6), and an
  eval can show effect (point 7).

**Fix chain (necessary order — a column alone repeats today's empty-field bug)**
1. **Extractor** emits `{key, value, rationale, evidence_quote, source_message_id, confidence}` and
   validates it.
2. **Projection** persists all of it (Preferences needs one `evidence`/`metadata` JSON column;
   always set `source_conversation_id`).
3. **Surface** — an audit/curation read that exposes why+evidence and a keep/drop action, plus an
   injection log for usage.

---

## Layer 2: Decisions

**Snapshot (dev, all users, 526 rows):** `root_cause_agreed = 509` (96.8%). Provenance fill:
`conversation_id = 513` (97.5%), `subject = 163` (31%), `rationale = 30` (5.7%), `evidence/context = 0`.
Of the 163 with a subject, only 8 are raw-prompt junk — most are reasonable short labels.

| # | Dimension | Status | Evidence |
|---|---|---|---|
| 0 | **Validity (gate)** | ❌ | **FAILS.** 96.8% are `root_cause_agreed` fabricated by the extractor (`actor=memory_extractor`, 494/509 have no human `created_by`). The core signal "the user decided X" is manufactured, not observed. → points 1–7 are moot. |
| 1 | What (content) | ⊘ | Extractor emits full root-cause content, but `ProjectFact` drops it; only a short subject survives (31%). Moot — item shouldn't exist. |
| 2 | Why (rationale) | ⊘ | 5.7% populated. Moot. |
| 3 | Evidence | ⊘ | 0%. Moot. |
| 4 | From where | ⊘ | `conversation_id` 97.5% — a **valid link to an invalid item**. Not a pass. |
| 5 | Correct / rejectable | ❌ | Same as validity: fabricated agreements. |
| 6 | How used | ❌ | 0 references; new-module injection untracked (legacy-only audit). |
| 7 | Impact | ❌ | none. |

**Real approvals in this layer:** only **8 rows** (`action_approved/declined`, `recommendation_
accepted/dismissed`, `runbook_chosen`, `tool_selected`) — all attributed to **sundar** and looking
like seed data. Everything else is extractor-fabricated.

**Verdict:** Decisions **fails the Validity gate** — no clean pass on any dimension. The one field
that's populated (source link) points at conversations where no decision was actually made. Headline
defects: (a) over-fire / fabricated agreements; (b) content dropped on write. **Fix:** only record a
decision on a real signal (click / explicit agreement → human `created_by`, `actor=user`); stop the
extractor auto-asserting agreement; then stop dropping content.

**FIXED (Option B, branch `fix/memory-layer-data-quality`):**
- **Over-fire killed (0/5):** the extractor emits `decision_type` only on an explicit user verdict
  with an `evidence_quote`; findings get none. Structural backstop: `Record` refuses a Decisions-target
  fact with no `EvidenceQuote` (`ErrDecisionNeedsEvidence`) — inference can't fabricate "agreed" rows.
  The prompt example teaching "finding = decision" was removed.
- **Evidence stored (1–4):** `ProjectFact` folds `{quote, message_id}` into the decision `context`
  JSONB (no migration — columns existed); `subject` (what) + `rationale` (why) describe the decision.
- **Data cleanup:** 494 fabricated `root_cause_agreed` rows (no human) deleted; 32 real kept.
- **Verified:** `TestMemoryDecisions_EvidenceGate` — un-evidenced finding refused; real verdict stored
  with quote + message_id.

---

## CRITICAL: two memory systems (discovered 2026-07-03)
The UI's "Additional Contexts · memory" is the **legacy** system, not this module:
- `llm_conversation_references` has **44,747** `reference_type=memory` rows; **28,806 join to
  `llm_conversation_memory`** (legacy RAG). **0 join to any `llm_memory_*`** table.
- Legacy memory has per-conversation usage tracking (references) + UI visibility.
- The **new module injects via `composeMemoryV2Block` into the system prompt with NO reference
  written** → its usage (point 6) is untracked and invisible. This is the layer-agnostic gap for 6/7.

## Layer 3: Patterns

**Snapshot (30 rows, all `memory_extractor`):** count=1 on 18/30 (60%), count>1 on 12 (max 73,
avg 4.3). Subject filled 10/30 (33%). Table has **no conversation/message column** (only `source`).

| # | Dimension | Status | Evidence |
|---|---|---|---|
| 0 | **Validity (gate)** | ⚠️ | Mixed — 40% real repetition (one at count=73); 60% "frequent" asserted from a single observation (count=1). Overclaims on the majority. |
| 1 | What (content) | ❌ | Subject empty on 67% (20/30) → dropped at render. |
| 2 | Why | ❌ | No rationale; description 12/30. |
| 3 | Evidence | ❌ | none. |
| 4 | From where | ❌ | **No `conversation_id`/`message_id` column** — can't trace to source. |
| 5 | Correct / rejectable | ❌ | Can't judge empty subjects; 60% frequency-overclaim. |
| 6 | How used | ❌ | Empty-subject rows dropped at render; 0 references. |
| 7 | Impact | ❌ | none. |

**Tragic finding:** the most-reinforced pattern (`frequent_resource_type`, count=73) has an **empty
subject** → dropped at render → never used. Real signal wasted by the empty-subject write bug.
**Verdict:** partially valid but almost entirely unusable — good signal, broken writes, no traceability.
**Fix:** require count>1 before "frequent"; populate subject; add conversation/message linkage.

**FIXED (branch `fix/memory-layer-data-quality`):**
- **Subject required (1/0):** `Record` + the DAO `Upsert` reject a subject-less pattern (can't
  identify/count/act on it); the extractor prompt now **requires** a `subject` for patterns (the
  specific entity that recurs) — subject was previously "optional" in the prompt, the root of the 67%.
- **Provenance (4):** `ProjectFact` folds `{conversation_id, message_id}` into the pattern's metadata
  (no migration — `metadata` JSONB existed), so a pattern is traceable to the turn it was last seen.
- **Evidence = recurrence (3):** the pattern's own `count` + decaying `score`/`last_seen` are its
  evidence; the count-and-recency **gate** (surface ≥2 / act ≥3-and-recent) lives on the read side
  (retrieval phase, parked) — the data is now trustworthy enough to feed it.
- **Data cleanup:** 20 empty-subject patterns deleted; 10 real ones kept.
- **Verified:** `TestMemoryPatterns_SubjectAndProvenance` — subject-less refused; real pattern stored
  with source turn in metadata.

## Layer 4: Collective

**Snapshot (20 rows):** body 100%, subject 80% — the healthiest layer. Content is real; the gap was
**source provenance** (0 rows had it — existing rows predate the `_conversation_id` code, and there was
no `message_id`).

| # | Dimension | Status |
|---|---|---|
| 0 Validity | ⚠️→🟢 — **fill ≠ value**: ~half the rows were junk (see below) |
| 1 What | ✅ body 100% populated |
| 2 Why | n/a — the body *is* the fact |
| 3 Evidence | 🟢 **fixed** — source conversation + turn is the evidence |
| 4 Source | 🟢 **fixed** — `{_conversation_id, _message_id}` in metadata |

**Correction to the earlier "healthiest layer" call:** body-fill was 100%, but *content quality* was
mixed — 12 of 20 rows were junk in three flavours: (a) **ephemeral** (a pinned image tag with a
timestamp — stale next deploy), (b) **incident/recommendation** phrasing ("experienced ImagePullBackOff",
"should be increased to 256Mi"), and (c) **bare commands** (`kubectl get pods -w`) plus GitHub-auth
**near-duplicates**. Fill-rate hid this.

**FIXED (branch `fix/memory-layer-data-quality`):**
- **Provenance:** `ProjectFact` folds `{_conversation_id, _message_id}` into metadata (no migration), so
  a shared fact links to the exact turn that introduced it. Confidence is a real per-type value.
- **Content quality:** deleted the **12 junk rows** (8 genuine durable facts kept). Added extraction
  filters so they can't come back — `imageRefPattern` (pinned image tags / `@sha256`), `bareCommandPattern`
  (content starting with a CLI verb; prose that *uses* a command is kept; preferences exempt), plus prompt
  guidance rejecting image tags / incident descriptions / recommendations / bare commands.
- **Verified:** `TestMemoryCollective_SourceProvenance` (provenance) + `TestIsAcceptableMemoryFact`
  (new filter cases).

## Layer 5: Soul

**What it is:** a user's consolidated persona (tone/verbosity/format…) — a mix: some fields user-set,
most inferred by the `soul_consolidate` job. Already the best-instrumented layer: per-field `Sources`
(`user`/`agent`) with user fields protected from being overwritten by inference.

| # | Dimension | Status |
|---|---|---|
| 0 Validity | ✅ per-field source; user fields locked |
| 1 What | ✅ `style` populated |
| 2 Why | 🟢 **fixed** — per-field reason for inferred fields |
| 3 Evidence | 🟢 **fixed** — the user behaviour that justified each inferred trait |
| 4 Source | ✅ per-field `user`/`agent` label |
| 5 Correct | ✅ user can edit; edits win (OCC) |

**FIXED (branch `fix/memory-layer-data-quality`):** added a per-field `Evidence` map (field → one-line
"why") that rides in the style JSONB envelope alongside `Sources` — **no migration**. The
`soul_consolidate` prompt now emits a `why` object; the parse routes it to `evidence`; the mutate +
`UpsertAgent`/`mergeAgentWrite` path threads it per field, dropping it when a field flips to user-locked.
**Verified:** `TestMemorySoul_AgentEvidence` + the soul OCC merge test still green.

## Layer 6: Session — N/A
`llm_session_working_memory` is **ephemeral** per-session scratchpad (distilled each turn), not durable
knowledge. The why/evidence/source baseline doesn't apply — no fix needed.

## All six layers audited. Points 6–7 (usage / impact) remain the deferred cross-layer phase.
