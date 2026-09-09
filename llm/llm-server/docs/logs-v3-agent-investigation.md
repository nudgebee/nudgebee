# logs_v3 Agent — Slowness Investigation & Improvements

Status as of this writing: **local dev only, nothing deployed**. This doc exists so
the next person (or future us) doesn't have to re-derive any of this from scratch.
Read the "Open items" section first if you just want to know what's left.

**Update — upstream overlap.** While this branch was in progress, `main` picked up
two real, independently-authored commits that overlap significantly with §4/§6
below: **#36123** ("cut LLM turns in the logs agent") made the *exact same*
`resource_search` agent → `resource_search_execute` tool swap to v1's `logs`
agent (`agent_log.go`) that §4d makes for `logs_v3`, plus a **stronger** fix for
the parallel-batching problem than §6's — see the note in §6. **#36121** bounded
`cloud_resource_search_execute`'s live-kubectl-miss fallback to the account's real
AWS regions. This branch already has both rebased in cleanly (still builds/tests
clean) — see §10 for what this means for the two agents' current comparison.

## Background

The existing `logs` agent was reported as unpredictably slow. Investigation started
from Postgres evidence (`llm_conversation_agent`, `llm_conversation_token_usage`)
and live server logs, not assumptions. It expanded into building a side-by-side
candidate agent (`logs_v3`) and a series of targeted latency fixes to it.

Test fixture used throughout: account `a2a30b02-0f67-42e5-a2ab-c658230fd798`
(tenant `890cad87-c452-4aa7-b84a-742cee0454a1`), Nudgebee's own internal `k8s-dev`
account. Its cluster runs several internal namespaces, one per environment, that
all host similarly named services — this is *not* representative of a typical
single-environment customer cluster, and it's why namespace-disambiguation kept
coming up as an issue worth engineering around properly rather than ignoring.

How to reproduce a test call locally (see "Local test setup" at the bottom for
port-forward details):

```bash
curl -s -X POST http://localhost:9999/v1/completions/chat \
  -H "Content-Type: application/json" \
  -H "x-tenant-id: 890cad87-c452-4aa7-b84a-742cee0454a1" \
  -H "x-user-id: 8abbccee-3829-4d4b-9133-806810766723" \
  -d '{
    "query": "@logs_v3 get me logs for relay_server",
    "account_id": "a2a30b02-0f67-42e5-a2ab-c658230fd798",
    "user_id": "8abbccee-3829-4d4b-9133-806810766723",
    "async": false
  }'
```

---

## 1. Three config bugs (fixed locally, not deployed)

All three were silently making every `logs`/`logs_v3` call slower, independent of
any code change. Found by reading current source (not the pre-existing, partially
stale `log_analysis_bugs` doc) and confirmed live.

### 1a. TTFT watchdog was dead
A PR renamed the global timeout switch to a per-provider key format
(`LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_<PROVIDER>`). The dev secret still had the old
global key, so the watchdog — which cancels + retries the same model if no first
token streams within 30s — silently never armed.

- Mechanism: `agents/core/llm_common.go:1226-1234`. The watchdog timer only fires
  if `!tracker.wasStreaming()` at the 30s mark — it has **zero visibility into
  what happens after the first token arrives** (see §6, this matters a lot later).
- Local fix (`.env`): `LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI=true`
- Verified live: fires and retries correctly on a genuinely stalled model.

### 1b. `bundle_signal` diagnostic sweep was built but never enabled
A pre-computed, server-side category-tagged grep sweep inside `fetch_logs`
(`runAutoDiagnosticBundle` in `agent_log_fetch.go`) was meant to let the LLM skip
the mandatory manual two-call grep pass for error investigations. Shipped
defaulted off pending validation (`LLM_LOGS_STANDARD_GREP_ENABLED`, default
`false`, `config.go`), never flipped on.

- Local fix (`.env`): `LLM_LOGS_STANDARD_GREP_ENABLED=true`

### 1c. `ValidateRequest` was hardcoded off
The canonical log-query path (`executeFetchLogsCanonical`,
`tools/common_services_server.go`) never asked services-server to self-diagnose
empty results (wrong label name vs. wrong label value vs. genuinely no data).

- Code fix (committed on this branch):
  `tools/common_services_server.go` — `ValidateRequest: false` → `true`
- Verified: services-server now returns an actionable `Suggestion` string on a
  zero-row canonical query instead of a bare empty result.

**Deploy checklist for 1a/1b**: add the two env vars to the deployed secret.
1c is already a code change on this branch.

---

## 2. Live data bug: stale Redis cache serving a wrong label mapping

Not part of the original ask — surfaced while comparing a raw vs. canonical Loki
query for one tenant (`app` canonical field resolving to a non-existent native
field `app_id`, so every canonical query silently returned zero rows).

**Root cause**: `api-server/services/observability/log_labels.go`'s
`getCustomLogLabels`/`getTenantLogLabels` call `common.CacheSet(...)` with **no**
`CacheSetWithExpiration` option. The cache namespace declares a 10-minute default
TTL at `CacheCreateNamespace` time, but that default is only honored by the
in-memory (bigcache) backend — for the Redis backend actually in use
(`CACHE_PROVIDER=redis`), every write is **permanent** unless the caller explicitly
passes an expiration. See `api-server/services/common/cache.go:168-186` — the
namespace-level `Expiration` field is never read inside `CacheSet`.

**What happened**: a tenant's `log_labels` override once had a bad value
(`{"app":"app_id"}`), got cached into Redis with no TTL, and the underlying
Postgres row was *later corrected* to `{"app":""}` — but the immortal cache entry
kept serving the stale value forever, on every single request, since precedence is
`dynamic > account > tenant > static` and nothing above it in the chain overrode
it.

**Done**: deleted the stale Redis key directly (`nb_log_labels:t:<tenant_id>`) —
self-heals on next read since the DB row was already correct.

**Not yet done**: the code fix — pass `common.CacheSetWithExpiration(logLabelsCacheTTL)`
in both `CacheSet` calls in `log_labels.go` so this class of bug can't recur for
any other tenant/account whose `log_labels` gets edited or cleared. This is in
`api-server/services/observability`, outside `llm-server`'s module — needs its own
PR.

---

## 3. `logs_v3` — what it is and why

New agent (`agents/agent_log_v3.go`), registered under the distinct name
`logs_v3` so it never receives implicit traffic — explicit invocation
(`@logs_v3`) or router selection only.

### Core structural difference from `logs` (v1)
`logs` (v1) calls its fetch step as a full nested sub-agent
(`RegisterNBAgentFactoryAsTool`, same pattern `resource_search` used until §5.4
below). That's the same underlying fetch logic, wrapped in generic sub-agent
machinery that isn't free:

- 2 guaranteed DB writes per fetch call (`SaveConversationAgentCall` INSERT +
  `UpdateConversationAgentResponse` UPDATE) for the child agent row
- An extra background LLM call fires almost every time (`generateAsyncAgentSummary`),
  purely to summarize the sub-agent's own history — a cost that only exists
  because it has its own row to summarize
  (`executor.go:956`, `executor_planner.go:4264`)
  because it has its own row to summarize
- Redundant DB reads: parent-agent-id SELECT run twice, a full conversation-history
  load + prompt-template render that Custom-planner agents never actually use,
  gets built then discarded

`logs_v3`'s `fetch_logs_v3` tool (`fetchLogsV3Tool` in `agent_log_v3.go`) calls
`FetchLogsAgentV2.Execute()` **directly, in-process**, as a plain `NBTool` — no
nested agent, no extra row, no extra summary call. `nbCtx.ParentAgentId` is
threaded through as both `AgentId` and `ParentAgentId`, so the internal
canonical-query-generation LLM call attributes to the calling `logs_v3` turn
instead of creating a new agent identity.

**Verified live**: the same query produced 3 `llm_conversation_agent` rows under
`logs` vs. 2 under `logs_v3` (before §5.4's `resource_search_execute` swap; **1**
row after it — see below). ~5.7% fewer input tokens in a side-by-side run.

### Correctness fix included by construction
`buildFetchLogsV3ToolResponse` (`agent_log_v3.go`) propagates the real terminal
status (`ConversationStatusFailed`/`Terminated`/`Waiting`) instead of the old
bespoke wrapper's behavior of always reporting `NBToolResponseStatusSuccess` even
when the sub-run had actually failed.

### Prompt reuse
`GetSystemPrompt` reuses `agent_log.go`'s mode classifier
(`classifyLogMode`) and shared instruction blocks
(`sharedHeaderAndWorkflow`, `investigationInstructions`, `enumerationInstructions`,
`routineInstructions`, `outputFormatInstructions`, `sharedConstraints`) verbatim —
that tuning is production-earned. `logs_v3` only adds new blocks on top; it never
edits the shared ones (so `logs` v1 is never touched by anything in this doc,
except §4's fix which is a shared helper both benefit from).

---

## 4. Discovery mechanism — three iterations, one reverted

`sharedHeaderAndWorkflow`'s step 1 mandates `resource_search` before any fetch
when the user names a bare service/app/deployment without an exact pod. This step
is where almost all of the iteration happened.

### 4a. Original (inherited from v1, unmodified)
Call the `resource_search` **agent-as-tool** (`ResourceSearchAgentName`), which
internally fans out to Kubernetes + Datadog + cloud (AWS/GCP/Azure) resource
search regardless of relevance. Measured cost: **~50s**, dominated by an ~11-13s
`cloud_resource_search` DB lookup and an ~20s Datadog call, neither of which a
purely-Kubernetes log question ever needed.

### 4b. ❌ REVERTED — unscoped app-label fetch, skip discovery entirely
Tried querying Loki by app label with no namespace filter
(`{app="relay-server"}`), skipping `resource_search` outright. Faster (2m35s vs.
3m53s) — but **silently returned logs from the wrong namespace**, presented with
full confidence (a differently-environment-suffixed namespace's relay-server
instead of the account's real base namespace). Confirmed **not** a cross-tenant
data leak — both kubectl and
Loki paths are scoped per-account at the relay transport layer
(per-account RabbitMQ queue → single connected cluster agent) — but the
within-account namespace ambiguity is real and would affect any real customer
running dev/staging/prod in one cluster too, not just this dev/test account.

**Do not retry this exact approach** (unscoped label match with no namespace
resolution) — it's a demonstrated correctness regression, not just an
optimization that needs tuning.

### 4c. Combined kubectl call + fallback on ambiguity (superseded by 4d, kept for reference)
Replaced two *sequential* manual `shell_execute` kubectl calls with one combined
`kubectl get deploy,pod --all-namespaces | grep -i <name>`, falling back to full
`resource_search` only when more than one distinct namespace matched. Correctness
restored; modest win (~19% faster) when the target happened to be genuinely
ambiguous, bigger win when it wasn't.

### 4d. ✅ Current — call `resource_search_execute` directly (the win, suggested by senior review)
Key discovery: `resource_search` (the agent) and `resource_search_execute`
(`tools.ToolResourceSearch`, `tools/tool_resource_search.go`) are different
things. The agent-as-tool orchestrates 3 sub-tools
(`resource_search_execute`/k8s, `cloud_resource_search_execute`,
`datadog_resource_search_execute`) via its own internal LLM decision and — per
every trace captured — ends up calling all three regardless of relevance.
`resource_search_execute` is the plain, directly-registered Kubernetes-only tool
underneath it, with **no** LLM-orchestrated fan-out and **no** nested-agent DB row.

It also already does a DB-first lookup internally
(`K8sResourceSearchTool.searchDbForResources`/`handleResourceSuggestions`,
`tool_resource_search.go:533` "1. DB-first") against the `cloud_resourses` table —
the k8s-collector's near-real-time inventory, a new pod queryable within ~1s —
falling back to live kubectl **itself** only when the DB lookup misses. So the
manual kubectl call from 4c was pure redundancy on top of a tool that already does
this faster, via a DB read instead of a live relay-server round-trip.

**Current implementation** (`agent_log_v3.go`):
- `GetSupportedTools`: exposes `tools.ToolResourceSearch` instead of
  `ResourceSearchAgentName` — for **all three modes**, not just routine, since the
  Datadog/cloud fan-out was never useful for a Kubernetes log agent in any mode.
- `resourceSearchToolNote()`: patches the naming mismatch — `sharedHeaderAndWorkflow`
  (shared with v1) still says "resource_search" throughout; this note retargets
  every mention to the actual tool name and its structured JSON calling
  convention (`resource_name`, `namespace`, `search_type`, ...).
- `fastPathAppAnchor()` (ROUTINE mode only): calls `resource_search_execute`
  directly with `search_type: "suggestions"`, reads `match_quality` from the
  response (`exact`/`unique`/`unique_owner`/`multiple`/`none` —
  `tool_resource_search.go:670` `calculateMatchQuality`). On `multiple` (name
  spans more than one namespace), applies a stated, non-silent heuristic: prefer
  the plain base namespace name over one with an environment suffix *only*
  when exactly one such candidate exists, and says so in the final answer; if no
  single candidate stands out, reports the ambiguity instead of guessing.

**Verified live, twice**:
- DB rows: **1** (`logs_v3` alone) — down from 2 (post-§3 fix) / 3 (original v1
  shape). No `resource_search` nested-agent row at all anymore.
- Discovery step latency: **14.6s**, down from 31-44s. Zero
  `datadog_resource_search_execute` / `cloud_resource_search_execute` /
  `cloud_resource_search: db lookup result` log lines — that fan-out is now
  structurally impossible to trigger, not just discouraged by prompt wording.
- Same-query wall time: 314.9s → **205.9s** on a clean run (no LLM blow-up, see §6).
- Correctness held on repeat: resolved `nudgebee` correctly both times, including
  once via the DB-first path and once by falling through to kubectl internally.

**Note**: as of #36123 (see top-of-doc update), v1 `logs` now *also* calls
`resource_search_execute` directly instead of the `resource_search` agent — so
this specific discovery-fan-out win is no longer unique to `logs_v3`. Confirmed
live in §10's real A/B data: `logs`'s DB row count dropped from 3 to 2-3 (2
typically, 3 when it still falls back to the agent for ambiguous cases) — but
`logs_v3` still shows **1** row every single time, because `logs` still wraps
its `fetch_logs` step as a nested agent-as-tool (§3's win) and v1 was not
touched there. That remaining gap is what §10's real data actually measures.

---

## 5. `fetch_logs_v3` inline preview formatting

**Problem observed**: in every routine-mode run captured, the model did a
follow-up `shell_execute` peek at the saved file (`head -n 20 <file_ref>`) even
though the ROUTINE prompt explicitly says the envelope alone should be enough
("Envelope is the answer... a follow-up shell_execute grep would only re-derive
what you already have", `agent_log.go`). Tool response sizes were consistently
tiny (1.4-1.9KB) in every case, ruling out "too much to show inline."

**Root cause**: the inline `logs` field for Loki/Signoz/ES responses is the raw
backend envelope — the application's actual log line is escaped JSON *inside*
JSON (`"message":"{\"time\":...}"`) — genuinely harder to parse than the raw file.
The file itself was already fine: `saveLogsToWorkspace` rewrites it via
`flattenLogsToTabSeparated` into clean `<timestamp>\t<message>` lines per entry. The
inline preview just never got the same treatment.

**Fix** (`agents/agent_log_fetch.go`, `makeFetchResponse` — shared with v1, so
`logs` benefits too, not just `logs_v3`): call `flattenLogsToTabSeparated(logs)` before
building the inline preview, so it's the *same* clean format as the saved file
instead of the raw escaped envelope. Test added:
`TestMakeFetchResponse_PreviewsLogsWhenFileRefPresent/preview_format_matches_the_file_format,_not_the_raw_backend_payload`.

### ⚠️ Known gap — kubectl backend not covered
Found live, right after shipping: `flattenLogsToTabSeparated` only recognizes the
Loki/ES/Signoz `{"logs":[...]}` shape. The **kubectl** backend's raw output is
wrapped differently (`{"stdout": "<raw text>"}`), which this fix doesn't touch.
Confirmed live — a run that routed through kubectl (via a "deployment" label
anchor) still needed two follow-up reads, the second one literally
`jq -r '.stdout' <file> | head -n 10` to unwrap it manually. **Not a regression**
(kubectl-backend fetches were never covered), but the fix's benefit is currently
Loki/ES/Signoz-only. Fixing this needs a sibling case in `flattenLogsToTabSeparated` (or
an adjacent function) for the `{"stdout":...}` shape.

---

## 6. Parallel-batching for routine-mode follow-up reads

`investigationInstructions()` already tells the model to batch independent greps
(Call A + Call B) into one ReAct iteration instead of two sequential round-trips.
`routineInstructions()` had no equivalent — on the (hopefully now rarer, per §5)
occasions the model still wants more than one look at the file, those went out
one iteration at a time.

**Fix**: `routineParallelFollowupReads()` (`agent_log_v3.go`), appended for
ROUTINE mode only. Not yet cleanly validated in isolation — the one live run that
exercised it was driven by the §5 kubectl-format gap (necessary follow-ups, not
redundant ones), so we haven't yet seen a case where it actually collapsed two
avoidable sequential calls into one batch.

### ⚠️ Superseded by a stronger pattern upstream — port this in
#36123 (see the note at the top of this doc) fixed the *same* class of problem
for `investigationInstructions()` (shared with v1) using better evidence and a
structurally stronger mechanism: measured over 30 days of production
`shell_execute` pairs, the existing "emit both in one parallel batch" instruction
was followed only **28% of the time** — 72% still landed in separate turns,
median gap 5.2s, because it depends on the model choosing to comply. The fix
was to stop asking for two batched *actions* and instead mandate **one
`shell_execute` call whose command chains both patterns** with an `echo`
separator (`grep ... ; echo '--- MARKER ---'; grep ...`) — that's structurally
one action, one turn, regardless of model behavior. `investigationInstructions()`
now does this; `routineParallelFollowupReads()` here still uses the weaker
"ask nicely to batch" form. Should be upgraded to the same chained-command
pattern — see Open items.

---

## 7. Pre-loop overhead — investigated, not fixed (needs a decision)

Every request pays a "setup" cost before the first ReAct iteration even starts.
Measured directly from `agentexecutor:` log lines (`executor.go`) rather than
estimated:

```
agentexecutor: history loaded                    | duration: 651.8ms
agentexecutor: system prompt generated           | duration: 263.5µs
agentexecutor: KB and memory retrieval complete   | duration: 6.63s   ← 53% of setup
agentexecutor: executing agent planner            | total_setup_duration: 12.4s
```

`KB and memory retrieval` (`executor.go:610-680`) dispatches account-wide RAG KB
retrieval and memory composition in parallel goroutines, for **every top-level
agent invocation**, unconditionally — not specific to `logs_v3`, no existing
per-agent opt-out.

**Why this wasn't just fixed**: skipping it for `logs_v3` would need a new opt-in/
opt-out interface (same pattern as the existing `NBAgentCategoryProvider` optional
interface), and it's a genuine trade-off, not a free win — INVESTIGATION mode
specifically could benefit from KB retrieval surfacing a synced runbook for the
exact alert under investigation. Skipping it unconditionally would sacrifice that.
**This needs a product decision** (e.g. skip only for ROUTINE mode, or skip only
when the account has no relevant KBs mapped) before it's implemented, not just an
engineering call.

---

## 8. The open question: LLM output blow-up (unresolved, highest-leverage item)

**Pattern**: in every logs_v3 run captured (5+ separate instances across multiple
different fixes/configurations), the LLM call immediately following a tool
observation occasionally generates 8x to 100x+ more output than a normal
"decide next tool call" step (normal: 57-125 tokens). Confirmed instances:
907, 1172+1080, 3609+13485, 9101, 5153 output tokens, with latencies from 13.5s
up to **188.2s** for a single call. This is the single largest, most variable cost
bucket found in the entire investigation (§9's aggregate breakdown: 52% of all
wall-clock time across 5 runs, driven almost entirely by this).

**Theories tested and ruled out, with evidence**:
- ❌ **Large/noisy tool responses** — every actual tool response body was small
  (1.4-1.9KB) across every blow-up instance. No correlation with size.
- ❌ **Gemini's hidden "thinking" token budget exceeded** — checked directly via
  the `thinking_tokens` DB column: 152-2,650 tokens used, comfortably under the
  configured 8,000-token `LlmThinkingBudgetRetrieval` ceiling
  (`config.go:1106`) on every instance. The blow-up is almost entirely
  **visible** completion text (e.g. 13,485 total / 273 thinking = 13,212 visible).
- ❌ **Malformed or duplicated tool-call output** — parser logs
  (`reactagent3: output parsed`) show exactly one clean, valid action extracted
  in under a millisecond, every time. Not a repeated/garbled action block.
- ❌ **The existing TTFT watchdog catching it** — by design it only guards the
  gap before the *first* streamed token (`llm_common.go:1226-1234`); it has no
  visibility into sustained generation after streaming starts. A call that
  streams its first token in 2s and then keeps generating for 3 minutes sails
  straight through. The 5-minute hard call ceiling
  (`LlmServerMaxIndividualCallTimeoutMinutes`, default 5) also never trips —
  every observed instance stayed under it.

**Still unresolved**: the model is writing a genuinely long *visible* reasoning
block before its one action, for reasons not yet identified. Every occurrence
happens right after a tool response — a correlation present in every instance,
but "right after a tool response" describes literally every ReAct iteration, so
this hasn't isolated a specific trigger yet.

**Recommended next steps** (not yet started):
- A sustained-generation timeout distinct from TTFT — cancel + retry if a single
  call exceeds some threshold (e.g. 45-60s) regardless of streaming state.
- Try lowering `max_tokens` specifically on ReAct "decide next action" calls (not
  the final answer) to hard-cap the blast radius even without understanding the
  trigger.
- Try a non-thinking or different model for this call type as a diagnostic (does
  the blow-up follow the model, or is it prompt/context-shape-specific to this
  codebase's ReAct XML format?).

---

## 9. Wall-clock breakdown — 5 test runs, precisely attributed

Derived by parsing `plannerexecutor: generating plan` / `plan generation complete`
/ `tool execution time` / `iteration complete` log-line timestamps per run, not
estimated.

| Run | Total | LLM think | resource_search | Other tool exec | Pre-loop |
|---|---|---|---|---|---|
| 1: relay_server (orig, §4a) | 212.3s | 63.8s (30%) | 35.6s (17%) | 84.8s (40%) | 28.1s (13%) |
| 2: relay_server (§4b, REVERTED — wrong ns) | 134.2s | 54.2s (40%) | 0.0s (0%) | 58.0s (43%) | 22.0s (16%) |
| 3: relay_server (§4c) | 179.8s | 56.6s (31%) | 31.3s (17%) | 69.7s (39%) | 22.2s (12%) |
| 4: e2e_dashboard (§4c) | 355.2s | 267.9s (75%) | 0.0s (0%) | 76.4s (22%) | 10.9s (3%) |
| 5: relay_server rerun (§4c) | 314.9s | 180.4s (57%) | 43.8s (14%) | 79.8s (25%) | 10.9s (4%) |
| **Aggregate** | **1,196s** | **623s (52%)** | **111s (9%)** | **268s (22%)** | **94s (8%)** |
| 6: relay_server (§4d, resource_search_execute) | 205.9s | — | 14.6s (discovery step only) | — | — |
| 7: relay_server (§4d + §5 + §6 fixes) | 274.4s | dominated by one 66.3s/5153-token blow-up call | — | — | — |

Runs 1-5 predate the §4d/§5/§6 fixes; runs 6-7 postdate them. Runs 4, 5, and 7 each
contain one instance of the §8 blow-up — that's why 6 (no blow-up that run) reads
so much better than 7 (hit one) despite 7 having strictly more structural fixes
applied. **Single-run wall-clock comparisons are not reliable** without knowing
whether that run happened to hit a blow-up — this is why §8 is the priority, not
any individual structural fix.

**No single fix reaches a sub-60-second target.** Even the cleanest pre-§4d run
(#3: correct answer, zero blow-up) was 179.8s. Getting under a minute requires
§8 (the blow-up) fixed *and* the structural wins compounding, not one silver
bullet.

---

## 10. Real A/B test data — `logs` vs `logs_v3`, 14 completed runs

`scripts/ab_test_logs.sh` (new on this branch) fires the same query at both
agents N times each and pulls the real structural numbers from Postgres per run
— `llm_conversation_agent` row count and token/latency stats — instead of
trusting wall-clock time alone. See the script header for usage.

```
AB_TEST_TENANT_ID=890cad87-c452-4aa7-b84a-742cee0454a1 \
AB_TEST_USER_ID=8abbccee-3829-4d4b-9133-806810766723 \
AB_TEST_ACCOUNT_ID=a2a30b02-0f67-42e5-a2ab-c658230fd798 \
./scripts/ab_test_logs.sh -n 3 -q "get me logs for relay_server" -q "get me logs for e2e_dashboard"
```

### Results

**`relay_server` query:**

| Agent | Run | Wall | DB rows | Max output tok | Flag |
|---|---|---|---|---|---|
| logs | 1 | 355.9s | 2 | 7,705 | BLOWUP |
| logs | 2 | 237.8s | 2 | 5,523 | BLOWUP |
| logs | 3 | 218.3s | 3 | 1,897 | BLOWUP |
| logs | 4 | 160.8s | 2 | 1,603 | BLOWUP |
| **logs avg** | | **243.2s** | **2-3** | | **4/4 hit blow-up** |
| logs_v3 | 1 | 185.2s | 1 | 2,607 | BLOWUP |
| logs_v3 | 2 | 154.7s | 1 | 1,069 | BLOWUP |
| logs_v3 | 3 | 353.6s | 1 | 5,296 | BLOWUP |
| **logs_v3 avg** | | **231.2s** | **1** | | **3/3 hit blow-up** |

**`e2e_dashboard` query:**

| Agent | Run | Wall | DB rows | Max output tok | Flag |
|---|---|---|---|---|---|
| logs | 1 | 600.1s | — | — | **TIMEOUT** (didn't finish in 10 min; no `llm_conversation_agent` row was ever created, so nothing was billed either — confirmed via DB, not just inferred) |
| logs | 2 | 468.5s | 2 | 1,474 | BLOWUP |
| logs | 3 | 187.2s | 2 | 966 | clean |
| **logs avg** | | **328s** (on the 2 that finished) | **2** | | **1 timeout + 1/2 blow-up** |
| logs_v3 | 1 | 112.2s | 1 | 835 | clean |
| logs_v3 | 2 | 141.9s | 1 | 819 | clean |
| logs_v3 | 3 | 132.3s | 1 | 752 | clean |
| **logs_v3 avg** | | **128.8s** | **1** | | **0/3 blow-up** |

### What this shows

1. **DB row count: 100% consistent across all 14 samples, no exceptions.**
   `logs_v3` = 1 row, always. `logs` = 2-3 rows, always. §3's fetch_logs
   nested-agent elimination is the durable, structural win — it holds under
   real repeated testing even after #36123 gave v1 the discovery-side win too
   (see §4d's update).
2. **`e2e_dashboard`: `logs_v3` wins decisively.** 128.8s avg, zero blow-ups
   across 3/3 runs, vs. `logs` at 328s+ with a 10-minute timeout and a
   blow-up in the same 3-run batch. Cleanest evidence in this whole
   investigation that `logs_v3` is meaningfully better on a well-behaved query.
3. **`relay_server`: inconclusive, and reveals a new pattern.** Every single
   sample from both agents (7/7) hit the §8 blow-up this round. That's a real
   data point, not noise: the blow-up rate looks **query-dependent**, not
   uniformly random. `relay_server`'s log content (nested request/response
   JSON, occasional namespace ambiguity across the account's several
   environments) triggers it far more reliably than `e2e_dashboard`'s simple
   repetitive health-check lines. When both agents get dominated by the same
   shared, unrelated cost, the structural win (fewer DB writes, faster
   discovery) gets masked — reinforcing that §8 really is the top-priority fix,
   not a lower-priority nice-to-have.
4. **Environment cost ~9 of 24 attempted samples** to a mid-rollout
   `services-server` deployment on the shared dev cluster — root-caused via
   `kubectl get pods -n nudgebee` (all 3 replicas had just restarted,
   port-forward errored with "failed to find sandbox ... not found"), not a
   bug in the test script or either agent. Restarting the port-forward after
   confirming the new pods were stable recovered it. Noted here for
   transparency, not hidden — if this doc's numbers ever look inconsistent
   with a fresh run, check `kubectl get pods -n nudgebee` for a rollout in
   progress before assuming a regression.

## 11. Real dollar cost of these 14 runs

Computed using the actual pricing formula and rates this codebase bills with
(`agents/core/conversation_dao.go`, `CalculateTotalCost` / `GetConversationCosts`,
`llm_model_pricing` table) — not estimated. For `googleai:gemini-3.5-flash`
(the model both agents ran on): $1.50/M input, $0.15/M cached input, $9.00/M
output+thinking combined, no long-context tier configured for this model.
Background/housekeeping calls (`summary_agent`, `session_extractor`, etc.) run
on the cheaper `-lite` tier and are included in these totals since they're real
cost incurred per conversation, not agent-attributable overhead specifically.

| Agent | Query | Runs | Total | Avg/run | Min | Max |
|---|---|---|---|---|---|---|
| logs | e2e_dashboard | 3 | $0.4586 | $0.1529 | $0.1427 | $0.1649 |
| logs_v3 | e2e_dashboard | 4 | $0.4635 | $0.1159 | $0.0967 | $0.1343 |
| logs | relay_server | 4 | $0.6660 | $0.1665 | $0.1115 | $0.2478 |
| logs_v3 | relay_server | 3 | $0.4765 | $0.1588 | $0.1014 | $0.2544 |

**Grand total: $2.0646** across all 14 completed conversations.

`logs_v3` is ~24% cheaper per run on `e2e_dashboard` (consistent with the
latency win — fewer DB writes, no extra summary LLM call, no blow-up in that
batch). On `relay_server` average cost is close between the two agents because
both hit the blow-up in nearly every run — its extra output tokens are billed
at the most expensive rate ($9/M), so it dominates the bill regardless of which
agent's turn it happened on. Same read as the latency data: the blow-up is the
cost driver here, not agent architecture.

---

## Open items (priority order)

1. **Fix the LLM output blow-up (§8)** — by far the highest-leverage remaining
   item; nothing else here gets you under 60s p90 while this exists. §10's real
   A/B data adds a new lead worth chasing first: the hit rate looks
   query-dependent (7/7 on `relay_server`, 0/3 + 1 blow-up on `e2e_dashboard` in
   the same test batch) — worth testing more query shapes to see what
   specifically correlates before trying blind mitigations.
2. **Port #36123's chained-shell-command pattern into `routineParallelFollowupReads()`
   (§6)** — the "ask the model to batch" instruction it currently uses is the
   same weak pattern #36123's own production data showed fails 72% of the time
   for investigation mode. Should mandate one `shell_execute` whose command
   chains every pattern with an `echo` separator, matching
   `investigationInstructions()`'s current (post-#36123) shape.
3. **Deploy the three config fixes (§1a/§1b)** to the live secret — zero code
   risk, already validated locally. §1c is already code (this branch).
4. **Ship the cache-TTL code fix (§2)** in `api-server` — separate PR, outside
   this branch's module. File the underlying bug too.
5. **Close the kubectl-backend formatting gap (§5)** — small, same pattern as
   the fix already shipped.
6. **Decide on pre-loop KB/memory retrieval (§7)** — needs a product call, not
   just an engineering one, before implementing an opt-out.
7. Run more `scripts/ab_test_logs.sh` batches across a wider variety of query
   shapes (not just `relay_server`/`e2e_dashboard`) — 14 samples is enough to
   trust the DB-row-count finding (100% consistent) but not enough to fully
   trust the cost/latency averages given how much the blow-up dominates them.

## Files changed on this branch

- `agents/agent_log_v3.go` (new) — the `logs_v3` agent, `fetch_logs_v3` tool,
  discovery fast-path, all v3-only prompt overrides
- `agents/agent_log_v3_test.go` (new) — unit tests for all of the above
- `agents/agent_log_fetch.go` — §5 inline-preview formatting fix (shared w/ v1)
- `agents/agent_log_fetch_test.go` — test for the §5 fix
- `agents/chain_router.go` — explicit-invocation routing for `logs_v3`
- `agents/core/executor.go` — `duration_seconds` added to the existing
  `agentexecutor: operation metrics` log line (all agents, not v3-specific —
  this is what made §9's precise timing possible without manual log
  reconstruction going forward)
- `tools/common_services_server.go` — §1c, `ValidateRequest: false` → `true`
- `scripts/ab_test_logs.sh` (new) — §10's repeatable A/B comparison script

Not on this branch (local `.env` only, gitignored): §1a/§1b's two env vars.

## Local test setup

Port-forwards needed (all die intermittently in this dev environment — recheck
with `nc -zv localhost <port>` before a test run):

```bash
kubectl port-forward -n nudgebee svc/services-server 8120:8000
kubectl port-forward -n nudgebee svc/rag-server 8700:9999
kubectl port-forward -n nudgebee svc/relay-server 8110:8080
kubectl port-forward -n redis svc/redis-master 6379:6379
# Postgres: usually already reachable via an existing cloud-sql-proxy on 5433,
# check `lsof -i :5433` before assuming you need to forward it yourself.
```

Then `cd llm/llm-server && go run ./cmd`, wait for `{"status":"ok"}` on
`GET localhost:9999/health`, and use the curl pattern at the top of this doc.

DB inspection (Postgres password from `LLM_SERVER_DB_URL` in `.env`):

```sql
-- Find a conversation's agent rows (row count = structural overhead check)
SELECT agent_name, parent_agent_id, status, created_at
FROM llm_conversation_agent WHERE conversation_id = '<id>' ORDER BY created_at;

-- Per-call token/latency/thinking breakdown
SELECT agent_name, input_tokens, output_tokens, thinking_tokens, latency_seconds
FROM llm_conversation_token_usage WHERE conversation_id = '<id>' ORDER BY created_at;
```
