# Tool-context reduction for the k8s / AWS orchestrators

**Status:** design / proposal (no runtime change yet)
**Goal:** shrink the number of tools loaded into the *main context* of the top-level
orchestrators (`k8s_orchestrator`, `aws_orchestrator`) without losing any capability
or any of the routing/usage guidance currently carried in tool descriptions.

This doc records what was verified in code, corrects one wrong assumption, lays out the
candidate reductions, and proposes a phased, A/B-gated rollout.

---

## 1. What is loaded today (verified)

Every top-level orchestrator renders each supported tool's `Description()` + `InputSchema()`
into its ReAct3 system prompt (`{{.tool_descriptions}}`), cached per account.

### k8s_orchestrator — `agent_k8s_orchestrator.go:347`
~28 agents-as-tools in the base list:

```
kubectl(/kubectl_execute), logs, websearch, postgres, events, traces, metrics,
redis, mysql, mssql, oracle, rabbitmq, security, helm, github, tickets, workflow,
service_dependency_graph, visualizer, recommendations, aws, aws_observability,
gcp, azure, resource_search, server, code_analyzer, delegate_agent
```
plus conditionals: `remediation`, `memory`, `followup`, and any MCP integration tools.

Notes for the record (current runtime reality, factored into the categorization below):
- **`shell_execute`** — included when `LlmServerShellToolEnabled` is set (enabled in current envs, so effectively always present there); it is flag-gated in code, not unconditional. It is also auto-injected by `FilterAndInjectDefaultTools` when that flag is on.
- **`think` is deprecated/removed** — not a live tool; ignore in any trim accounting.
- **`datadog_orchestrator` is slated for removal** — do not spend effort keeping it in context;
  it exits regardless of this work.

### aws_orchestrator — `agent_aws_orchestrator.go:263`
~17 in the base list (`aws_observability`, `aws_execute`, `tickets`, `github`, `websearch`,
`recommendations`, `events`, `visualizer`, `postgres`, `mysql`, `mssql`, `oracle`, `redis`,
`rabbitmq`, `kubectl`, `delegate_agent`, `aws`) + `service_dependency_graph`, `memory`, `think`, MCP.

Order of magnitude: **~1.5–2.5k tokens of tool catalog** in the cached system prompt before
the model reads the query.

---

## 2. The reduction is mostly *free* — the reach-back machinery already exists

Removing a specialist agent from the base list does **not** remove the capability, and does
**not** lose the agent's own domain intelligence:

1. **`delegate_agent`** (`agent_delegate.go`) — `resolveToolsForDelegate`
   (`agent_delegate.go:333-359`) resolves **any registered agent by name** via
   `GetNBAgent → NewToolFromAgent`. So `helm`, `redis`, `mysql`, `mssql`, `oracle`,
   `rabbitmq`, `security`, `code_analyzer`, `github`, `workflow`, `visualizer`, `server`
   are all still invocable when dropped from the base list — **provided the model knows the
   name exists**.
2. A specialist's real value is its **own system prompt** (schema-discovery steps, safety
   constraints, few-shots — e.g. `agent_redis.go`, `agent_postgres.go`). That prompt only
   loads *when the agent runs*, so trimming the base list leaves it untouched. The base-list
   entry is only a routing pointer (`Description()` ≈ 40–80 tokens).
3. **`load_skills` / `search_skills`** (`skills.go`) and **`search_docs`** (`tool_docs.go`)
   already provide on-demand KB / schema / docs retrieval, and `load_skills` is auto-injected
   when the prompt contains `<skill-lists>` (`agents/core/utils.go:155`).

### The one real gap
There is **no `list_tools` / `search_tools` meta-tool**. Drop a tool from the base list and
the model has no way to *discover* it exists. So any trim must be paired with **one** of the
reach-back-discoverability mechanisms in §4.

### Corrected assumption
An earlier read suggested the k8s orchestrator was missing the `IsToolConfigured` gate that
the AWS orchestrator applies. **This is false.** `GetEnabledNBTools` (`tools/core/tool.go:441`)
already calls `IsToolConfigured` per tool, and the k8s list is built from it — so
`aws`/`gcp`/`azure`/`datadog` already drop out for accounts without those integrations
configured. There is no free config-gating win to take; the cloud tools only load where relevant.

---

## 3. Candidate categorization (k8s orchestrator)

| Keep (core investigation loop) | Move to on-demand (reach via delegate/discovery) | Already integration-gated |
|---|---|---|
| kubectl(/kubectl_execute), logs, events, metrics, traces, resource_search, service_dependency_graph, recommendations, delegate_agent, shell_execute (when LlmServerShellToolEnabled — on in current envs) (+ memory / remediation when their flags are on) | helm, redis, mysql, mssql, oracle, rabbitmq, security, github, tickets, workflow, visualizer, server, code_analyzer, websearch | aws, aws_observability, gcp, azure |

(`think` is deprecated/removed — not counted. `datadog_orchestrator` is slated for removal — not
counted; it leaves context independent of this trim.)

Result: **~28 → ~9 core**, dropping ~14–18 descriptions from the cached prompt.

Rationale for the split:
- **Keep** = tools used on nearly every investigation, or heavily promoted by the k8s prompt
  (`resource_search` as first action; `service_dependency_graph` for topology).
- **Move** = specialist / single-domain agents that only matter when the investigation reaches
  that domain (a DB, a Helm release, a repo, a diagram). Each is a `delegate_agent` target today.
- **Gated** = already conditional on account integration config; no action needed.

The AWS orchestrator gets the analogous split in a follow-up (§5, Phase 3).

---

## 3a. Wrapper audit — COLLAPSE vs KEEP (verified per-agent)

Every orchestrator agent was read and classified. **"Holds one leaf tool" is not the collapse
test** — nearly all of them do. The real test is: *does the agent's system prompt, multi-tool
coordination, RAG binding, or custom service logic add value the parent planner would otherwise
have to re-implement?* Result: **only 2 net collapses** (`helm`, `redis`); `kubectl` is already
collapsed for the k8s orchestrator via `direct` mode. Everything else earns KEEP.

### COLLAPSE (agent → parent holds leaf tool directly)
| Agent | Leaf tool | Why collapsible | Rehome before collapsing |
|---|---|---|---|
| `helm` | `helm_execute` | Thin prompt (3 instr + 2 constraints, one is copy-paste kubectl noise); one leaf, no coordination | "always `-n <ns>` / `--all-namespaces`", "pipe large output to grep/tail" → tool `Description()`. Preserve `IsWatchCapable`. |
| `redis` | `redis_command_executer` | Thin prompt; only real guidance is a SCAN-over-KEYS nudge (its own examples violate it) | SCAN-over-`KEYS *` hint + read-only preference → tool `Description()`. Confirm `Rag.Module:"redis"` is unused (no keys) before dropping. |
| `kubectl` | `kubectl_execute` | **Already collapsed** for k8s orch via `K8sOrchestratorModeDirect` (`use_kubectl_direct`); guidance already rehomed into the orchestrator prompt | Done — this is the working template for the pattern. |

### KEEP (value-add — reason)
| Reason | Agents |
|---|---|
| Dialect-specific schema-introspection playbook (+ some carry a **RAG `Module` binding a leaf tool cannot hold**) | `postgres`, `mysql`(RAG), `mssql`(RAG), `oracle` |
| Multi-surface / multi-tool coordination | `rabbitmq` (rabbitmqadmin vs HTTP API), `events` (11 tools), `logs` (resource_search+fetch_logs+shell), `automation` (26 tools), `security` (3), `service_dependency_graph` (3–4 KG tools), `argocd` (argocd+kubectl+github+MCP) |
| Multi-backend **router** (`Custom` planner — collapsing forces parents to re-implement provider selection/fallback) | `metrics`, `traces` |
| Custom-planner agents with their own service logic, **zero declared leaf tools** | `visualizer` (generate→validate→retry), `resource_search` (parallel fan-out + rank + enrich), `code_analyzer` (external RCA engine + git-credential minting) |
| Rich single-tool domain methodology | `github`, `tickets_v2`, `recommendations`, `aws`, `gcp`, `azure`, `aws_observability`, `server` (weak keep — needs its JSON input schema + RAG binding rehomed first) |

Near-wrapper but retained for v1/v2 coexistence only: `tickets` (v1, thin JQL prompt over `ticket_master`) — governed by `TicketV2Enabled`, leave as-is.

> **Caveat — `rabbitmq`:** its KEEP rests on the `rabbitmqadmin`-vs-HTTP-Management-API routing
> tree, but the HTTP-API path in `rabbit_execute` is currently mis-implemented (wrong endpoints,
> won't work) and needs a separate bug fix. Until then its dual-surface value is partly theoretical.
> Track as its own issue; out of scope for this reduction.

## 3b. DB agents → DB tools + on-demand methodology (progressive disclosure)

The four SQL agents (`postgres`, `mysql`, `mssql`, `oracle`) scored KEEP **only** because their
value is a rich prompt (dialect rules, mandatory schema-introspection, RAG binding) a bare leaf tool
can't carry. That objection dissolves if the methodology is **pulled at point-of-use** instead of
preloaded as an agent prompt:

1. Planner needs a DB → calls the search/discovery method for it.
2. Search returns the **leaf tool** (`postgres_query_execute`, …) **+ its usage detail** (schema-
   introspection SQL, read-only rule, `pg_stat_*`/DMV/`V$` view catalog, dialect gotchas) **+ any
   RAG-retrieved DB docs**.
3. Planner calls the leaf tool directly, guidance already in its scratchpad.

This removes 4 preloaded agents + their hops. It is **cache-safe** under per-account loading: the
detail returns as an *observation* (human message), never touching the cached system prefix (§5a).

**Two things that must be preserved or capability is silently lost:**
- **RAG binding moves into the search method.** `mysql`/`mssql`/`oracle` agents pull DB-specific docs
  via `Rag.Module`. A leaf tool has no RAG channel, so the search/discovery step must run that
  retrieval and fold it into what it returns. (The existing `load_skills`/`search_skills` + RAG path
  already does exactly this — reuse it, don't rebuild it.)
- **Serial discovery shifts into the parent scratchpad.** The "introspect schema → query → check
  `pg_stat_activity`" sequence runs today inside the DB sub-agent's isolated scratchpad; collapsed,
  those 2–4 serial steps run in the orchestrator's loop (more clutter; compression absorbs it).
  Mitigation: keep `delegate_agent` as the escape hatch for heavy multi-step DB investigations; use
  direct-tool-plus-loaded-methodology for the common simple-query case.

**Recommended (proposal — not yet approved):**
- *Methodology home:* reuse the skills infra (one methodology skill per DB, surfaced by the search
  method) rather than a parallel usage registry — the RAG binding comes for free and it's already
  cache-safe. Alternative considered: a richer `search_tools` payload with inline usage; rejected as
  a second store to maintain. Do **not** fatten leaf `Description()` (always-in-context; can't carry RAG).
- *Pilot:* prove the full pattern end-to-end on **Postgres** first (leaf tool + methodology-on-demand
  + RAG, agent dropped from preload behind an eval handle), validate, then template to mysql/mssql/oracle.
- *Generalizes:* the same "leaf tool + on-demand detail" pattern applies to every KEEP-by-rich-prompt
  agent (`recommendations`, `github`, `aws`/`gcp`/`azure`).

### Strategic implication — two orthogonal levers
The audit separates the goal into two independent axes:
- **Lever A — collapse the agent hop** (removes a sub-agent LLM round-trip): applies to **`helm`,
  `redis`** only. Its win is **latency/cost**, not context — and note `helm_execute`'s
  `Description()` is *fatter* than the `helm` agent's blurb, so collapse may not shrink the prompt
  at all; the round-trip elimination is the payoff.
- **Lever B — trim which agents load in the always-on list** (§3), reaching the rest on-demand via
  `delegate_agent`: applies to **many** KEEP agents (they stay full agents, just not preloaded).
  This is where the **context reduction** actually comes from.

So context bloat is *not* mostly collapsible wrappers — it's ~28 legitimately-distinct agents all
preloaded. **Lever B (curate the preloaded set) is the bigger token lever; Lever A is a small,
clean latency win on two agents.** Both are cache-safe (§5a).

## 4. Reach-back / discoverability mechanisms (pick one; combination recommended)

Any trim must tell the model how to reach the moved tools. Three options, increasing cost:

**(A) Static prompt catalog + `delegate_agent`** — cheapest.
Add a short block to the orchestrator prompt: *"Specialist agents available on-demand via
`delegate_agent` (call by name when the investigation reaches that domain): helm, redis,
mysql, mssql, oracle, rabbitmq, security, github, tickets, workflow, visualizer, server,
code_analyzer."* No new tool, no code beyond the list. Doesn't scale past a handful of names,
but the moved set here is small and stable.

**(B) `search_tools` discovery meta-tool** — scales to 190+ agents.
New tool returning `{name, description, input-usage}` for the registry, filtered by a query.
Data sources already exist: `ListAgents` (`agents/core/api_service_agent.go:128`, returns
`Name`/`Description`/`Tools`) and `ListRegisteredSystemToolNames` + `GetNBTool.Description()`.
The model calls `search_tools`, then invokes the discovered specialist via `delegate_agent`.
More work, cleanest long-term, matches "new tools around searching tools & details".

**(C) Workspace/shell note** — only for genuine CLI wrappers.
Note in the shell/workspace instructions that `helm`, `kubectl`, etc. are preinstalled CLIs
usable via `shell_execute`. **Does not** replace the DB agents (`postgres`/`mysql`/… inject
*configured* connection credentials and run schema-discovery logic — a bare shell has neither).
Use (C) only for `helm` and similar pure passthroughs, and only when `shell_execute` is enabled.

**Recommended:** **(A) now + (B) when the moved set grows.** (A) is enough for today's ~14-agent
move, costs one prompt block, and loses no routing info. (C) applies narrowly to `helm`.

---

## 5. Rollout — behind the existing eval-handle pattern

These are the **router-selected production orchestrators**; a base-list change measurably shifts
routing and must be A/B'd, not shipped raw. The codebase already has the exact machinery for this:
`K8sOrchestratorMode` (`delegating`/`direct`/`lean`) and the always-on eval handles
`@k8s_orchestrator_2`, `@k8s_orchestrator_lean` (`agent_k8s_orchestrator.go:30`,
`agent_k8s_orchestrator_lean.go`). Reuse it.

- **Phase 1** — Build the trimmed base list as a new mode/handle (e.g. a `trimmed` variant, or
  a new `@k8s_orchestrator_trim` eval handle following the `_lean` template). Router never
  selects it; invoke via `@name` and A/B against `@k8s_orchestrator` / `@k8s_orchestrator_2`
  on the same query. Reach-back = mechanism (A): append the specialist catalog to the prompt in
  Go (same way the lean agent appends `memoryNudge`, `agent_k8s_orchestrator_lean.go:96`) — **no
  edit to existing files in `prompts_repo/`**.
- **Phase 2** — Once eval shows parity/gain, promote by flipping the mode default (config, not
  code) exactly as `direct`/`lean` are flipped. Rollback = flip back + redeploy.
- **Phase 3** — Repeat for `aws_orchestrator`. Optionally add mechanism (B) `search_tools` if the
  moved set grows beyond a comfortable static catalog.

### Phase 1 status — `@k8s_orchestrator_trim` shipped (eval handle)

`agent_k8s_orchestrator_trim.go` adds the `@k8s_orchestrator_trim` (alias `k8s_debug_trim`) eval
handle. **Base = v2 (direct-kubectl orchestrator), deliberately — not lean, not delegate.** It reuses
the exact v2 prompt (`renderK8sDebugReactPrompt(..., useKubectlDirect=true)`), planner type, model
tier, and kubectl log filter, changing exactly two things so the tool set is the *single* A/B
variable against `@k8s_orchestrator_2`:
1. **Trimmed preloaded set** (`trimmedK8sCoreToolNames`): kubectl_execute, logs, events, metrics,
   traces, resource_search, service_dependency_graph, recommendations, delegate_agent, search_tools
   + the same remediation/shell/memory/followup conditional tail as production. Every specialist
   agent is dropped.
2. **One appended override instruction** (`trimOnDemandInstruction`): tells the planner its set is
   lean and to reach specialists via `search_tools`+`delegate_agent` (flag on) or `delegate_agent`
   by name (flag off) — and never to emit a specialist as a direct action (the dispatch auth check
   would reject it anyway, §5b).

Router never selects it; invoke via `@k8s_orchestrator_trim`. Its own cache key
(`account:…:k8s_orchestrator_trim:…`) keeps the A/B off the primary's warm cache (§5a). Basing on v2
(not lean) is intentional: lean would confound *minimal-prompt* with *trimmed-tools*; v2-base
isolates the reduction itself. A combined lean+trim handle is a possible later variant.

### Verifying the trim handle
- **Unit** (`agent_k8s_orchestrator_trim_test.go`, always runs): trimmed name list includes
  core+reach-back and excludes specialists; delegator uniqueness; `trimOnDemandInstruction` flag variants.
- **Integration** (`agent_k8s_orchestrator_trim_e2e_test.go`, `//go:build e2e`, needs
  `TEST_ACCOUNT`/`TEST_USER`/`TEST_TENANT` + live LLM):
  1. `..._ToolSetExcludesSpecialists` — resolved set against a **real account** preloads core+reach-back,
     excludes specialists, and is strictly smaller than `@k8s_orchestrator_2`.
  2. `..._ContextReductionAB` — same query set through `_2` vs `_trim`; logs per-query
     Δ input-tokens / llm-calls / tool-routing and the total token ratio (the headline reduction metric).
     `TRIM_MAX_INPUT_TOKEN_RATIO=1.2` turns it into a hard gate.
  3. `..._ReachesSpecialistOnDemand` — a helm-requiring query must complete via `delegate_agent`/
     `search_tools` and **never** emit a specialist as a direct action (auth would reject it).
  Run the discovery path with `LLM_SERVER_SEARCH_TOOLS_ENABLED=true`; without it the handle falls back
  to delegate-by-name.

### What must not regress (prompt-info preservation)
The k8s prompt names moved tools directly and will emit a tool it no longer holds if left as-is:
- `agent_k8s_debug_react.txt:94` "Specialized Component Pivot Protocol" names
  `postgres` / `redis` / `aws`.
- `agent_k8s_debug_react.txt:90-91` names `server`, `aws`, `gcp`, `azure`.

For the trimmed variant these lines must route via `delegate_agent` (or `search_tools`) instead of
naming a held tool. This is the concrete "don't lose existing prompt information" work item: every
moved tool's routing hint is rehomed into the catalog block or the pivot protocol, not deleted.

---

## 5a. Caching impact (governing constraint)

The provider caches the **system-message prefix by byte-match**, keyed
`account:{accountId}:{agent}:{model}` (12h, shared across all conversations in the account —
`docs/caching.md`). That prefix **includes `{{.tool_descriptions}}`** (rendered into
`planner_react_3_base.txt`, a system message). The cache key does **not** encode the query or the
selected tool set. So any change to the *content* or *order* of the tool list changes the prefix
bytes → cache miss. `GetEnabledNBTools` already sorts deterministically for exactly this reason.

Impact per approach:

- **Collapse pure wrapper → leaf tool (static list): cache-safe.** One-time prefix change at
  deploy, stable thereafter, and it removes a whole sub-agent LLM call (a separate request with its
  own full system prompt). Caveat: some leaf-tool `Description()`s are fatter than the agent's
  tool-facing blurb (`helm_execute` ≫ the `helm` agent's one line), so the stable prefix can grow
  slightly; cache reads are ~10% token cost, so this loses easily to killing a round-trip. Net win.
- **Conditional-by-account (`IsToolConfigured`, today): cache-safe.** Set is a function of the
  account → prefix stable per account → the existing `account:` key is already correct.
- **Conditional per-query / dynamic selection: cache-hostile — avoid.** The set would vary per
  query while the key stays `account:...`: near-total cache miss AND the cache-write premium
  (~25% over base input) paid every request → can cost more than no caching. Also breaks ReAct3
  (the system prefix must be byte-identical across a message's iterations) and the in-process
  `agentSupportedToolsCache` (keyed `account:agent`, not query). Only viable if queries are bucketed
  into a small stable class folded into the cache key — a real redesign, rarely worth it.
- **Tiered core + on-demand: cache-safe if specialists are introduced *outside* the cached
  prefix** — via `delegate_agent` (sub-agent has its own cache scope) or discovery-as-observation
  (a `search_tools`/`load_skills`-style tool returns usage into the human-message scratchpad; its
  own definition is static). Never by mutating `tool_descriptions`.

**Conclusion:** the cache-correct way to "load less up front" is **static-core collapse (§3) +
on-demand-via-delegate**, *not* dynamic per-query loading. Preserve deterministic tool ordering
(reordering alone busts the cache). The trimmed eval handle gets its own cache key
(`account:{acct}:k8s_orchestrator_trim:{model}`), so A/B does not pollute the primary's warm cache.

## 5b. Auth / access-control impact (verified)

**The authorization model is account-scoped, enforced per tool action.** Every action passes
`IsAgentToolAuthorizedToProcessRequest` (`agents/core/auth_agent.go`, called from
`executor_planner.go:1937` for *every* agent execution — top-level and delegated sub-agents alike):
1. the tool must be in `agent.GetSupportedTools()` (else rejected: "auth: tool not found"), plus a
   small builtin whitelist (`load_skills`, `shell_execute`, watch tools, client tools);
2. if the action is classified write/create/update/delete, it requires
   `HasAccountAccess(accountId, "create")` — else it's turned into a finish action ("user doesn't
   have access to update").

There is **no per-user-within-account tool/agent ACL**: read access to the account gates
enumeration/use; write access gates mutations. The per-agent `allowed_tools`/`disabled_tools`
capabilities are **prompt-level curation** (applied when building the advertised tool list), *not*
an auth-layer boundary — the auth check reads the raw `GetSupportedTools()`.

Impact of this reduction work:
- **The trim is auth-enforced-safe.** Removing a tool from an orchestrator's `GetSupportedTools`
  means the planner literally cannot emit it directly — the dispatch auth check rejects it as "tool
  not found." The only reach-back is `delegate_agent` (which stays in the list). The design is
  backed by the auth layer, not just by prompt discipline.
- **`delegate_agent` does not escalate write access.** The delegated sub-agent runs through the same
  executor, so `IsAgentToolAuthorizedToProcessRequest` re-runs with the sub-agent as `agent` and the
  `HasAccountAccess("create")` gate still applies to every mutating action inside it.
- **`search_tools` adds no new execution authority.** It only enumerates capability metadata;
  actually using anything still funnels through `delegate_agent` + the per-action write gate. Since
  access is account-scoped, it discloses nothing a read-access user couldn't already reach.
  Hardening applied: `search_tools.Call` now checks `HasAccountAccess(read)` up front —
  `GetEnabledNBTools` self-guards, but `ListAgents` (the other enumeration source) does not.

**One pre-existing gap the trim makes more load-bearing:** `resolveToolsForDelegate`
(`agent_delegate.go`) resolves tools/agents by raw name via `GetNBTool`/`GetNBAgent` and does **not**
re-apply the account's `allowed_tools`/`disabled_tools` capability filter (nor the `IsToolConfigured`
gate). So a tool an account *disabled* for an agent via `disabled_tools` remains reachable through
`delegate_agent`. This predates this work and is not a hard-boundary break (the account read/write
gates still hold; `disabled_tools` is curation, not auth). But since trimming pushes more traffic
through `delegate_agent`, if any account relies on `disabled_tools` as a control, decide whether the
on-demand path should re-apply capability filtering in `resolveToolsForDelegate`. Tracked as a
follow-up, not a blocker.

## 5c. `search_tools` experimental gating + side-effects

**Feature-flagged for experimental use.** `search_tools` registers only when
`LLM_SERVER_SEARCH_TOOLS_ENABLED=true` (config `SearchToolsEnabled`, default **false**). The gate is
at *registration* (`init()`), not at use — config is populated before the `agents` package
initializes (Go runs imported-package `init()` first; `viper.Unmarshal(&Config)` completes in
`config`'s `init()`), so when the flag is off the tool is **completely absent**: not in the system
tool registry (`ListRegisteredSystemToolNames`), not in `GetEnabledNBTools`, not in the account tool
catalog / UI picker, and not resolvable by `delegate_agent`. This is stricter than the
register-always-gate-at-use pattern (`think`/`shell`) and is the right default for an experiment.

Side-effects considered (flag OFF → none; flag ON → contained):
- **Account tool catalog / UI picker** (`GetEnabledNBTools`, `api/tools.go`): with the flag on,
  `search_tools` appears as an enabled tool (it needs no config, so `IsToolConfigured` passes) and can
  be added to custom agents. This is the main reason to gate registration rather than just wiring.
- **`delegate_agent` resolution:** with the flag on, a sub-agent could resolve `search_tools` by name.
  Harmless (read-only discovery), and it excludes itself from its own results.
- **Registry-enumeration tests:** `agents/core/utils_schema_render_test.go` asserts on specific
  crafted schemas, not the full tool set, so a new registered tool does not perturb it. No golden
  full-registry snapshot exists to break.
- **Cache:** `search_tools` is not in any orchestrator's curated list, so it does not alter any
  cached system prefix. It changes the account tool-DTO/enabled-tools cache contents only (adds one
  entry) when the flag is on.
- **No DB, no network, no external calls; read-only.** Adds one metric label (`search_tools`).
- **Auth:** covered in §5b — `Call` gates on `HasAccountAccess(read)`; execution authority is
  unchanged (discovery only).

## 5d. Live A/B results (dev cluster, real LLM) — 2026-07

First real numbers (a dev GKE cluster, `gemini-3-flash-preview`, real kubectl/helm), `@k8s_orchestrator_2`
(full ~28-tool set) vs `@k8s_orchestrator_trim`:

- **No specialist needed** ("get pods in nudgebee"): trim saved **~11% orchestrator input tokens**
  (40,688 → ~36k), same single `kubectl_execute` call, correct answer. The core hypothesis holds —
  trimming the preloaded catalog reduces prompt cost with no behavior change.
- **Specialist needed** ("list helm releases"):
  | Variant | Path | Latency | Tokens |
  |---|---|---|---|
  | `_2` | direct preloaded `helm` sub-agent | 2m4s | ~66,874 |
  | `_trim`, shell **on** | bypassed discovery — ran `helm list` via `shell_execute` | 1m9s | ~36,597 |
  | `_trim`, shell **off** (forced) | `search_tools`→`delegate_agent`→`helm` sub-agent | 3m23s | ~107,961 |
- **Eval blindspot (Finding 3):** with `LLM_SERVER_SHELL_TOOL_ENABLED=true` (common on dev), the trim
  agent satisfies specialist queries via `shell_execute` and **never exercises search_tools/delegate**
  — an eval on such an account looks like a clean win without testing the discovery mechanism. Force
  the intended path by running with shell disabled (the `ReachesSpecialistOnDemand` e2e test now skips
  unless shell is off).

**What this changes (empirically, not hypothetically):**
1. **Trimming rarely-needed specialists is a real win** (~11%, no downside) — proceed.
2. **`search_tools`→`delegate` reach-back is the WEAKEST lever.** For a needed specialist it pays
   discovery *on top of* the delegate→sub-agent hop the full orchestrator already pays — a net loss on
   both cost and latency. §4's "not yet approved" caution is now backed by numbers.
3. **The cheapest correct path ran helm directly as a leaf command** (shell 36.6k ≪ sub-agent 66.9k) —
   strong evidence for the **leaf-tool collapse lever (§3a)**: hold `helm_execute` (leaf) directly
   rather than routing through the `helm` sub-agent *or* discovery. The sub-agent's own ReAct loop —
   not the preload — is the dominant cost when a specialist is actually needed.

**Reshaped priority:** (a) trim rarely-needed specialists + (b) collapse frequently-needed specialists
to leaf tools held directly; treat `search_tools` discovery as the lowest-priority lever until a
cheaper reach-back exists (or restrict it to the "specialist rarely needed" regime where the preload
saving dominates). Whether a given account nets out positive depends on its query mix.

## 6. Open decisions for the owner
1. Mechanism: confirm (A) + narrow (C) for now, or invest in (B) `search_tools` up front.
2. First scope: k8s-only trim (recommended) vs. k8s + aws together.
3. Whether the trimmed variant is a new `K8sOrchestratorMode` value or a standalone `@`-handle.
