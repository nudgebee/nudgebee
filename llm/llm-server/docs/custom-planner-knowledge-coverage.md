# Custom planner knowledge coverage

Source audit: 2026-09-06, base ed7084584a. Part of #36421.
This is a source inventory, not a runtime pass. Deployment and DB-configured agent inventory remain unverified.

## Coverage matrix

| Planner implementation | Current path | Remaining work |
| --- | --- | --- |
| FetchLogsAgent | Opts into auto_chunks; log intent consumes KBPrestepContent/SkillsContext | Dynamic exact loading, selective large-document reads, policy/restriction and operational tests |
| LogQueryAgent | Opts into auto_chunks; delegates query authoring to canonical log generation | Trace every authoring path; dynamic loading must preserve index/time window and query-only semantics |
| LogAnalysisAgent | Opts into auto_chunks; analysis prompt consumes KBPrestepContent | Dynamic loading and evidence-backed analysis tests |
| UnifiedSearchAgent | Opts into auto_chunks; has additional policy-gated skill retrieval | Consolidate retrieval behavior and cover all policies without duplicate searches |
| CodeAgent2 | Separate automatic top-K mapped-skill forwarding to code-analysis service | Cross-service exact/large-content consumption and model-directed support need dedicated design |
| metricsAgent | Custom wrapper delegates to provider agent | Verify child knowledge behavior; avoid adding redundant wrapper retrieval |
| fallbackTracesAgent | Custom wrapper around provider execution | Verify fallback/delegated knowledge behavior and scope |
| WorkflowBuilderAgent | Bespoke bounded workflow tool loop; no knowledge mode opt-in | Explicit search/load tool dispatch and policy support; preserve workflow approval/finalization rules |
| SearchAgent | Legacy search flow, no knowledge mode opt-in | Confirm registration/use and intended KB applicability before extending |
| LogGithubAgent | Direct custom prompt path, no knowledge mode opt-in | Confirm registration/use; add knowledge only at relevant investigation stages |
| VisualizationAgent | Specialized rendering path, no knowledge mode opt-in | Verify input propagation; no assumption that rendering should independently retrieve KBs |
| AgentCostOptimizer | Conversation optimization helper, no knowledge mode opt-in | Explicit applicability decision; do not inject unrelated operational knowledge |
| AgentEventRCAReport | Specialized report/evidence flow, no knowledge mode opt-in | Verify report inputs and live-evidence distinction before adding retrieval |
| RouterAgent | Routing custom planner, no knowledge mode opt-in | Preserve task scope into selected agent; avoid duplicate router retrieval |
| HelpAgent | Direct help prompt, no knowledge mode opt-in | Explicit applicability decision |
| LLMAgent | Generic custom LLM helper, no knowledge mode opt-in | Caller/input propagation audit before implicit retrieval |
| ClarificationAgent | Custom clarification helper, no knowledge mode opt-in | Clarification should not silently execute a procedure or expand scope |
| Database-defined nbCustomAgent | Planner type comes from ExecutorType | ReAct/orchestrating agents use shared knowledge path; literal custom executor rows are not proven supported. Inventory configured rows separately. |

## Shared contracts observed

- executor.go defaults custom planners to knowledge disabled unless they implement NBAgentKnowledgeModeProvider.
- Four implementations opt into auto_chunks; this supplies bounded excerpts, not a dynamic tool loop.
- knowledge_policy.go rejects llm_only for opted-in automatic custom consumers, including CodeAgent2. Removing this rejection without implementing a real loop would silently discard knowledge.
- The interface explicitly leaves custom delegators disabled so their underlying provider agent can discover knowledge itself.
- DB-backed KB fetching and database-defined agents are separate concerns. Fetching fixes do not prove custom execution support.

## Active observability flow correction

The initial inventory of literal custom planner declarations is not a priority map. The user confirmed v3 logs is primary. Code registers LogAgentV3 as `logs`, declares ReAct, and exposes `fetch_logs_v3` as a leaf tool. Review this path before legacy FetchLogsAgent work.

- Logs: the ReAct executor performs discovery and supplies candidate handles; `load_skills` loads exact content or workspace references. The model must translate relevant guidance into the fetch command. `buildFetchLogsV3Request` forwards command, original query, account/query context, but does not copy KBPrestepContent or SkillsContext. The natural-language translator uses buildLogIntentMessages, which can consume those fields when supplied; the v3 leaf request does not populate them. Canonical JSON bypasses translation. This is an existing boundary, not yet a demonstrated regression: test whether KB-derived index/filter/time constraints survive both command paths before changing it.
- Metrics: custom wrapper appends its name to InheritSkillsFromAgents and forwards OriginalQuery and SelectedSkillIds through ExecuteAgentToolCall. Provider ReAct execution performs its own discovery/load. Do not add a redundant loader to the wrapper.
- Traces: analogous inheritance for each fallback provider; references are accumulated from child responses. Verify each fallback's source/target scope and provenance.
- Shared executor now discovers account-wide per task. Mapping inheritance contributes attribution/specialist selection; old comments describing one top-level selected set are not proof that document bodies or candidate handles are transferred unchanged.
- Workspace loading resolves candidate handles against account/conversation/message. Parent handles must not be assumed valid in a child message; test child discovery and its own exact load.
- RAG eligibility fixes apply to these shared searches. Default-enabled workspace loading changes load behavior/schema for ReAct consumers, not the wrapper planner type. This change defaults workspace loading on; explicit false remains supported.

Revised first delivery: tests for parent -> logs v3, metrics provider, and traces fallback using relevant KB canaries, exact/workspace loading, and actual read-only tool arguments. Include a large document's late-section instruction and check that operational evidence—not the loaded document—backs completion. Fix demonstrated propagation failures before introducing a new custom knowledge loop.

## Subsequent delivery candidates (after active observability verification)

1. Implement a bounded, reusable knowledge preparation loop for the direct log planners, initially FetchLogsAgent and LogQueryAgent. It may search/load/read selectively, then return reference context to the existing query/execution path. Keep operational actions in their existing permission-controlled path.
2. Extend to LogAnalysisAgent and UnifiedSearchAgent, proving no duplicate retrieval. Add workflow-loop integration separately because it has its own dispatch and completion rules.
3. Audit wrappers and database-defined agents with integration tests; handle code-analysis forwarding as a separate cross-service change.
4. Decide applicability explicitly for routing, clarification, rendering, reporting and legacy helper agents rather than treating planner_type=custom as sufficient reason to retrieve.

Before implementation, finalize the loop call/time/output budgets and cancellation behavior. Do not load complete large documents into prompts; retain workspace-backed selective reads and existing size caps.

Acceptance: all four policies; source disabled/archived; unavailable exact content; large document with relevant content beyond its initial excerpt; denied tools; no invented operational completion; unchanged host/environment/index/time window across delegation. Tests must verify actual dispatched tools and output, not just prompt strings. Initial regression tests use controlled dependencies; subsequent live results are recorded in observability-knowledge-local-tests.md.

## Later priorities

After custom-planner coverage: explicit reference/procedure type with UI and runtime guidance, then evidence-led evaluation of dedicated procedure execution scope. Tenant ownership remains a separate lower-priority migration project.

## Offline observability handoff regression (2026-09-06)

A wrapper-dispatch test reproduced metrics/traces dropping KnowledgePolicy and KnowledgePolicyResolved under all four policies. The underlying executor then repeats the DB lookup instead of retaining the parent invocation's policy. Local fix forwards both fields. The traces test exercises primary and fallback dispatch; assertions also cover inherited mappings, selected IDs, original question, account, and command/context with PreProd/index/time constraints.

Logs v3 regression covers preservation of a parent-authored constrained command through request construction and either canonical JSON recognition or the human translation message. This does not prove the model extracted the correct instruction from a loaded document.

Still required for end-to-end acceptance: real parent and provider LLM turns, loading a large document's late-section canary, actual read-only logs/metrics/traces tool parameters, source references, and evidence-backed final answers. No live services or DB were used for these offline tests. The handoff fix does not add a loader or pass parent-scoped candidate handles into child messages.
