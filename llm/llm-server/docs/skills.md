# Knowledge discovery and skills

This document defines the runtime meaning of knowledge bases and skills in `llm-server`.

## Terminology

| Term | Meaning |
| --- | --- |
| Knowledge base (KB) | The account-owned record in `llm_knowledgebases`. It may contain manual content or describe an external integration. |
| Knowledge document | A searchable RAG document. For an integration KB, this is usually one Confluence, ServiceNow, or similar article. |
| Knowledge candidate | A compact, turn-scoped search result shown to a ReAct planner. It has an opaque `knowledge:<hash>` ID, title, source, and short snippet. |
| Skill | The planner-facing use of knowledge as expert instructions. `load_skills` loads a candidate or, for compatibility, a KB by name. A skill is not a separate storage model. |
| Agent mapping | A legacy association between a KB and an agent name. It is retained for UI/backward compatibility and legacy-document attribution, but it does not limit what an agent may discover. |

In short: KBs and documents are stored knowledge; candidates are search results; a loaded skill is knowledge admitted into the planner's working context.

## Storage model

Manual KBs store their authored body on the KB record. Integration KBs represent an external source, while the individual synchronized articles live as RAG documents with source and collection metadata.

Both forms are searched through the same account-scoped knowledge discovery operation. Confluence and ServiceNow are not a separate preload mechanism: their synchronized articles enter the same RAG result set as other knowledge documents.

## Runtime flow

The account's `knowledge_policy` is resolved first (missing means `auto`). When
the policy permits automatic discovery and the agent declares a knowledge mode:

1. The executor builds a search query from the original user question, the current delegated task, and useful resource identifiers.
2. RAG searches active knowledge across the account. In parallel, a small relevance-selected set of KBs mapped to the executing agent receives reserved collection-level retrieval capacity.
3. The two result pools are merged, deduplicated, and converted to turn-scoped candidates. Mapped results lead the merged list so a specialist runbook cannot be crowded out by a large integration KB.
4. Consumption depends on the planner type.

### ReAct and orchestrating agents

These agents use `AgentKnowledgeIndexOnly`. The executor adds only a compact `<skill-lists>` candidate index to the human message. Full document bodies are not added to the prompt.

The planner decides whether a candidate is useful and calls:

```json
{"skill_name":"knowledge:<hash>"}
```

`load_skills` resolves the ID from the turn-scoped cache and returns the bounded document content. It also accepts an exact, case-insensitive active KB name within the account, without requiring an agent mapping. Unknown names are reported as missing: the loader never substitutes substring matches or nearest-neighbour RAG results. Known integration KB records retain their content-retrieval path.

`search_skills` remains an explicit fallback tool for follow-up discovery. It returns exact names for manual KBs and cached candidate IDs for RAG documents, with compact previews. Load those IDs in the same turn. It is not required for the automatic first-pass candidate search. Search ranking can still return weak matches; discovery results do not prove relevance or count as loaded knowledge.

### Custom-planner agents

Custom agents build their own LLM calls and do not necessarily have a `load_skills` loop. A custom agent that directly needs customer conventions must implement `NBAgentKnowledgeModeProvider` and return `AgentKnowledgeAutoChunks`.

The executor then places only bounded, relevant content in `NBAgentRequest.KBPrestepContent`. The custom agent must include that field in the appropriate LLM message. Direct log fetch, log query, log analysis, and unified search currently opt in.

Database-backed agents created by users are declarative agents, despite the
`nbCustomAgent` implementation name. When their `executor_type` is `react` or
`orchestrating`, they run through the shared planner and receive the same compact
candidate index as built-in ReAct agents. Their configured tools remain authoritative
for operational capabilities, but the framework exposes `search_skills` and
`load_skills` for every knowledge-enabled ReAct invocation, including when no
candidate menu is available. This does not grant shell or watch tools; explicit
tool restrictions remain authoritative.

`search_skills` requires `load_skills` even when initial discovery times out or
returns no menu. The framework adds this dependency for built-in and curated
agents alike, without enabling unrelated default tools. Explicit `allowed_tools`
and `disabled_tools` restrictions still apply to the loader.

An `executor_type` of `custom` is reserved for code-backed `NBCustomAgent`
implementations with their own `Execute()` method. It is not a generic external-agent
transport. Those implementations must opt into `AgentKnowledgeAutoChunks` and place
`KBPrestepContent` in their own LLM message.

Custom agents that only delegate should remain `AgentKnowledgeDisabled`. Metrics and traces follow this pattern: their provider ReAct subagents independently search using the original question plus the delegated task.

`SkillsContext` is a legacy compatibility field. New custom-agent integrations should use `KBPrestepContent`; they must not eagerly concatenate all mapped KB bodies.

## Delegation

Each ReAct or orchestrating subagent resolves the account policy and, when eligible,
performs its own account-wide discovery. It does not yet reuse a parent agent's
candidate list because the delegated task may need different documents.

Mappings do not restrict what the subagent can discover, but mappings on that subagent receive the bounded supplemental retrieval described above. This preserves explicit specialist guidance without restoring eager injection or mapping-only visibility.

The search query includes both:

- the original user question, preserving investigative intent; and
- the subagent's task, adding provider- or resource-specific detail.

Dynamic delegates also use this executor path. Their explicit default-skills override ensures `load_skills` is available even though their other default tools are intentionally suppressed.

## Prompt-size behavior

The design avoids inserting every account KB or every mapped KB into prompts:

- ReAct agents receive compact metadata only and load full content on demand.
- Custom agents receive only reranked chunks under `LlmServerMaxSkillContentLength`.
- Candidate cache entries retain bounded excerpts and exact document handles; large content is read selectively through the workspace when enabled.
- ReAct knowledge menus are placed in the human message, leaving cacheable system
  prompts stable. Custom chunk consumers currently vary: log-fetch/log-analysis
  use human content; unified-search adds a system context message.

## Configuration

### Account knowledge policy

Store `knowledge_policy` in `cloud_account_attrs` (`cloud_account_id`, `name`,
`value`). A missing/empty value resolves to `auto`. There is no tenant fallback
or `llm_agents_installation.config` override. This uses existing storage; no
schema migration is required. Invalid values and lookup failures fail the
invocation explicitly rather than accidentally bypassing a disabled policy.

| Value | Automatic behavior | Dynamic tools |
| --- | --- | --- |
| `always` | Discover on every eligible nonempty invocation | Search/load available, subject to tool restrictions |
| `auto` (default) | NudgeBee controls when to discover, skip, and eventually reuse | Search/load available, subject to tool restrictions |
| `llm_only` | No automatic discovery or knowledge injection | LLM decides when to search/load |
| `disabled` | No new discovery or knowledge injection; inherited dedicated knowledge fields cleared | Search/load denied |

`always` does not mean inject entire KBs: ReAct/declarative agents receive a
compact menu, while opted-in code-backed planners receive bounded chunks.
The policy is resolved from the executing account on every invocation; it is
not supplied by the model and does not change account authorization. Disabling
knowledge does not erase knowledge already present in conversation history or
user-authored prompts.

The initial `auto` algorithm uses existing investigation/event signals and
conservative exact-turn checks. Greetings/thanks skip discovery; exact requests
to shorten or reformat an existing answer skip only when conversation context
exists. Unknown questions, documentation requests, changed tasks, and ambiguous
continuations still search. No extra LLM classification call is introduced.
This is not yet semantic intent gating or cross-turn/parent-result reuse.

Code-backed planners that opt into auto-chunks cannot execute search/load loops;
`llm_only` therefore produces an explicit unsupported-policy error for them
(also for the legacy code-analysis skill-forwarding consumer).
They support `always`, `auto`, and `disabled`. Planners without any knowledge
integration remain ineligible. No tools are automatically added to custom code
that cannot execute them.

### Retrieval limits

| Setting | Default | Purpose |
| --- | ---: | --- |
| `llm_server_kb_prestep_timeout_seconds` | `3` | Shared RAG deadline for account-wide and mapped discovery. Completed results are retained; unfinished HTTP calls are cancelled. Mapping/attribution are outside this budget. |
| `llm_server_max_skill_content_length` | `5000` | Caps each load_skills content page and relevant content supplied to an auto-chunk custom agent. |
| `llm_server_skill_selection_top_k` | `0` | Legacy/code-analysis-specific mapped-skill selection. It no longer controls executor-wide discovery. |

There is no separate integration-KB flag: manual and synchronized KBs follow
the same account policy. Existing ENV timeout overrides remain effective.

### Performance acceptance and remaining work

The end-to-end target is p50 30 seconds. The initial automatic RAG budget is
3 seconds, not a claim that the SLA is met. Earlier port-forwarded integration
runs measured 14–17 seconds for combined KB/memory preparation with the former
12-second RAG budget; those are not production percentiles or pure RAG timings.

Before broad rollout, measure timeout/result-retention rate, mapping, RAG,
attribution, and total preparation duration alongside end-to-end latency and
knowledge adherence. ReAct agents retain dynamic search/load after timeouts;
custom auto-chunk planners have no such fallback, so their retention rate needs
separate validation. Do not claim a successful empty retrieval proves quality.

Pending algorithm work: scope/access/freshness-safe parent and follow-up reuse
(reissue message-scoped candidate IDs), richer intent signals on the existing
classification path, a whole-preparation deadline, and explicit tool-loop support
for code-backed planners. Do not reuse by KB name alone or infer that two tasks
share an environment from their wording.

## References and observability

Loaded candidates retain their underlying KB/article reference ID and are emitted as `knowledge_base` references. Auto-chunk custom agents persist references for the documents actually retrieved. Reference persistence is de-duplicated by the conversation DAO.

Useful log events include discovery query timing/result counts, candidate cache misses, and `load_skills` resolution paths.

## Live skill lifecycle test

With service credentials and `TEST_TENANT`, `TEST_USER`, and `TEST_ACCOUNT`
exported, run from `llm/llm-server`:

```bash
RUN_KB_PRESTEP_E2E=true go test -tags=e2e -count=1 -v -timeout 15m \
  -run '^TestKBPrestepE2E$' ./agents
```

This creates a uniquely named manual KB with a hidden canary, waits for indexing,
maps it to the K8s orchestrator, and runs a real LLM conversation with only
`search_skills`/`load_skills` allowed. It asserts candidate-menu traces, a successful
loader response containing the canary, the exact KB reference, and the canary in the
final answer. It deletes its KB/mapping in `t.Cleanup` and verifies DB deletion;
conversation traces remain for inspection. Cleanup errors fail the test. The KB
service requests vector deletion, but the test does not independently verify it.

Set `TEST_SKILL_AGENT=<existing-custom-agent-name>` to test a declarative ReAct agent
(leave `load_skills` out of its configured tools to exercise automatic injection).
Set `TEST_SKILL_UNMAPPED=true` to test account-wide discovery without a mapping.
No existing agent configuration is modified. An outer process timeout/SIGKILL can
prevent cleanup; the exact created KB ID is logged for recovery. This test uses real
LLM calls and tests natural skill selection, so a failure can reveal a discovery or
adherence problem rather than just broken plumbing.

### Delegated skill lifecycle

```bash
RUN_KB_PRESTEP_E2E=true go test -tags=e2e -count=1 -v -timeout 15m \
  -run '^TestSkillDelegationE2E$' ./agents
```

This variant uses the built-in K8s orchestrator and maps the temporary KB only
to `delegate_agent`, the dynamic specialist's registered name. It ignores
`TEST_SKILL_AGENT` and `TEST_SKILL_UNMAPPED`. The parent is asked to delegate
exactly once, with only `search_skills` and `load_skills` available to the child;
infrastructure tools are excluded by the invocation's capability allowlist.

The test requires a successful root delegation with a persisted child link.
Candidate-menu traces, the canary-bearing load, and the fixture reference must
all belong to that same child agent. It rejects parent-side fixture loading or
the canary appearing in the delegated question, then checks the final answer
and fixture cleanup. A parent-only answer cannot satisfy the test. Account-wide
discovery remains enabled for both agents; mapping is not an access restriction.

## Improvement plan and verification status (2026-09-04)

This section tracks planned work, not deployed guarantees. The discovery work is
in PR #37206; collection scoping is separated into RAG PR #37649. Live results
below were observed against the local skill-discovery worktree and configured
services; they do not establish that the changes are deployed for customers.
The consolidated post-PR tracker is [`skills-follow-up-plan.md`](skills-follow-up-plan.md).

### Implemented and verified in the worktree

- Account-wide, question-relevant candidate listing without requiring mappings;
  bounded mapped retrieval preserves specialist guidance.
- Compact candidate menus and exact, turn-scoped document loading instead of
  eager full-KB injection. Unknown names no longer trigger substitute searches.
- Shared discovery for declarative agents and delegates, with loader injection
  respecting explicit tool restrictions; opted-in code-backed planners receive
  bounded chunks in their own prompts.
- Timeout handling retains completed individual searches. Document references
  use `KB_UUID:document_hash`; the lifecycle assertion now accepts those IDs.
- Live lifecycle tests passed for mapped K8s orchestration, unmapped discovery,
  and the declarative `test_code_agent`: listing, loading, answer canary,
  persisted reference, and fixture cleanup all verified.
- Eleven live search/load integration tests and nine focused regression tests
  passed without skips. These include delegation scope, custom-prompt knowledge
  propagation, loader availability, exact-chunk loading, and partial timeouts.

The RAG collection-scoping fix has separate unit coverage. Deployed verification
must still show one collection per mapped search and measure discovery latency;
retaining partial results does not itself remove the reranking bottleneck.
The delegated lifecycle variant passed with child-scoped assertions: the child
received a candidate menu, loaded the canary-bearing document, persisted its
reference, and the parent returned the canary without loading it itself. A later
policy/latency rerun also passed with both automatic RAG searches completing
inside the 3-second deadline; the fixture was removed after each run.

### Planned improvements

Rebase update: main's #37653 fixes invalidation on KB edits/renames, creation,
deletion, sync and status changes, including old names and mapped-agent menus.
Namespace TTL handling is also included. This addresses stale cached content
after a KB changes; it does **not** fix caching query-dependent integration
search results under only a KB name. The second item below therefore remains
open, with the invalidation/TTL work no longer part of its scope.

| Priority | Improvement | Acceptance evidence |
| --- | --- | --- |
| P1 | Preserve identity in legacy integration-KB name loading. The current enrichment path searches account-wide, applies the same content to every requested integration KB, and caches it by KB name. Scope retrieval to the resolved KB, or direct callers to document candidates; do not relabel unrelated results. | Two integration KBs with distinct canaries never return each other's content, including multi-name requests. Unavailable content is reported honestly. |
| P1 | Correct cache scope for query-dependent integration content. Keep exact document content caching distinct from search-result caching; a previous question must not pin arbitrary content to a KB name. | Two different questions against the same integration KB retrieve the appropriate documents; repeating the first load does not contaminate the second. |
| P1 | Distinguish knowledge retrieval from live investigation completion. Documentation may guide the query, but cannot satisfy a request for current metrics/logs. Keep knowledge loading separate from permission-controlled tool discovery/execution. | An operational request either invokes relevant live tools and reports their results, or explicitly reports the access/input blocker. A documentation-only request may finish without operational tools. Verify both ReAct3 and ReAct4. |
| P1 | Preserve resource and environment intent across turns and delegation. Do not silently switch between jumphost health and application endpoint health, or infer Prod solely from a document title. Resolve actual host/environment identity and clarify material ambiguity. | A multi-turn Prod/PreProd fixture verifies the selected host, environment, index, and delegated task; no unsupported scope switch. |
| P2 | Make evidence type and provenance explicit. A successful load means content was retrieved, not that a procedure ran or that a system is healthy. Retain KB/article identity and source, and distinguish documented guidance from observed measurements in answers. | Documentation-only responses contain no unsupported live-health claims; operational claims trace to matching tool results, target, and time window. |
| P2 | Add operational and source-specific end-to-end assertions. Existing canary tests intentionally ask for a documented procedure, and older subagent smoke tests do not assert that the child loaded a skill. | A delegated agent loads a known relevant article, executes the prescribed read-only investigation, and cites the correct source. Include synced Confluence/ServiceNow articles, not only manual KB fixtures. |
| P2 | Distinguish reference documents from procedures. Design an explicit author-declared type rather than inferring executable intent from arbitrary titles; existing untyped content remains reference knowledge unless explicitly classified. | Document lookup can answer reference questions without executing steps. A classified procedure exposes its applicability, prerequisites, ordered steps, and completion criteria. Classification alone grants no tools or permissions. |
| P2 | Evaluate focused delegation for procedure execution. Give a specialist the selected procedure, resolved target/environment, permitted tools, and success criteria; keep progress, blockers, approvals, and step evidence in that scope while the parent owns the user request. | A multi-step procedure test proves required steps are followed or explicitly blocked, scope survives delegation, approval-required actions cannot bypass confirmation, and the parent reports evidence-backed completion rather than successful loading. Decide when delegation is warranted versus unnecessary overhead for a short procedure. |

The procedure distinction and execution scope are design proposals, not current
runtime guarantees. A reference article can still guide an investigation; a
procedure is not automatically safe, executable, or applicable merely because
it has been classified. Existing canary/delegation tests verify loading plumbing,
not ordered procedure execution or operational completion.

Customer evidence motivating these additions: conversation
`9137b002-f32c-4a6b-b7cc-f0db044c1da6` (2026-09-02) used only knowledge/document
tools in its first two root turns, and operational Elastic tools in later turns.
The second answer supplied query/dashboard guidance rather than performing the
requested search. Repeated PreProd loads and a later Prod-to-non-prod scope shift
show inconsistent targeting; the initial user question did not specify an
environment, so the trace alone does not prove Prod was the correct target.

Non-goals: making `load_skills` an executable workflow, automatically granting
tools named inside documents, eagerly loading every mapped KB, or treating one
successful canary run as proof of operational correctness.

## Main implementation files

- `agents/core/executor.go` — selects the planner-aware knowledge mode and runs discovery.
- `agents/core/kb_prestep.go` — builds the combined query, searches/reranks, and creates candidate menus.
- `agents/core/interface.go` — knowledge modes and request fields.
- `tools/core/knowledge_candidates.go` — turn-scoped candidate cache.
- `tools/skills.go` — exact candidate/name loading, search candidate creation, and known integration-KB retrieval.
- `agents/core/planner_callback_handler.go` — reference persistence for loaded knowledge.


### Fetching correctness follow-up

Manual vector hits resolve to active account KB IDs. Exact integration names
search only their resolved collection; query-dependent results never enter the
name cache. Discovery caches at most 4 KiB of text and exact document handles.

`LLM_SERVER_KNOWLEDGE_WORKSPACE_ENABLED` defaults to true. With this default,
`load_skills` accepts one candidate/name and optional `keyword` or `start_line`.
Small documents stay inline; large indexed documents are saved to the conversation
workspace and read selectively through the same tool. No shell tool is required.
Account access, source version and workspace integrity are rechecked on reads.

When disabled, legacy loads label partial content as an excerpt. They do not offer byte pagination of the source. Exact-name loads recheck live
rows and integration retrieval. Candidate TTL is 30 minutes and large bodies
are never retained as continuation snapshots.

See [knowledge-size-design.md](knowledge-size-design.md) for limits, rollout
ordering, failure semantics and the distinction between indexed documents and
complete upstream pages.
