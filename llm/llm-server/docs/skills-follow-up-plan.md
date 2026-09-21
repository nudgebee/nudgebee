# Knowledge and skills follow-up plan

This document tracks work intentionally left after PR #37206. The current
runtime contract and completed verification remain documented in
[`skills.md`](skills.md).

## Current baseline

PR #37206 establishes the following baseline:

- Account-wide, relevance-ranked knowledge discovery; agent mappings reserve
  capacity for specialist knowledge but are not visibility boundaries.
- Compact, turn-scoped candidate menus for ReAct and orchestrating agents, with
  full content loaded on demand through `load_skills`.
- Bounded relevant chunks for code-backed custom planners that explicitly opt
  into automatic knowledge.
- Account policy values `always`, `auto`, `llm_only`, and `disabled`, defaulting
  to `auto` when the account attribute is absent.
- One policy lookup per top-level invocation, reused by in-process delegates;
  each new user invocation reads the account policy again.
- A shared three-second automatic RAG deadline, cancellation of unfinished
  requests, and preservation of completed partial results.
- Disabled or inactive KBs excluded from automatic discovery, manual search,
  and exact-name loading.

## Pending implementation

| Priority | Work | Intended outcome | Acceptance evidence |
| --- | --- | --- | --- |
| P1 | Fix legacy integration-KB identity during exact-name loading. | Retrieval is scoped to the resolved integration KB, and unrelated account results are never relabelled under the requested KB name. | Two integration KBs with distinct canaries cannot return one another's content, including a multi-name load. Missing content is reported honestly. |
| P1 | Separate query-result caching from exact document-content caching. | A query-dependent integration result is not cached only by KB name and reused for a different question. | Two different questions against one integration KB retrieve their respective documents; repeating either request does not contaminate the other. |
| P1 | Require live evidence for operational requests. | Loading documentation may guide an investigation but cannot be presented as current metrics, logs, traces, endpoint health, or procedure completion. | ReAct3 and ReAct4 either call the relevant live tools and cite target/time-window evidence, or state the concrete access/input blocker. Documentation-only questions may complete without operational tools. |
| P1 | Preserve target and environment scope across turns and delegation. | Host, environment, resource, index, and time-window identity remain stable unless the user changes them or ambiguity requires clarification. | A multi-turn Prod/PreProd test proves that parent and child agents use the same resolved scope and never infer environment from a document title alone. |
| P1 | Add account policy API and UI. | Account administrators can view and set `always`, `auto`, `llm_only`, or `disabled`, with `auto` shown for a missing value and clear explanations of automatic discovery versus model-directed search. | API authorization/default/validation tests plus UI read, update, error, and missing-value tests. No per-agent override in the first version. |
| P2 | Add scope-safe candidate reuse for follow-ups and delegation. | Equivalent work avoids another RAG search without reusing message-scoped IDs, stale knowledge, or a result from a different target/environment. | Reuse keys include account, task/target scope, access boundary, and knowledge version; reused documents receive fresh candidate IDs for the consuming message. Negative tests cover changed target, changed KB, and changed permissions. |
| P2 | Enrich the `auto` intent decision. | The platform skips low-value automatic retrieval more often without adding a separate LLM round trip or suppressing likely knowledge/procedure questions. | Offline fixtures and production telemetry compare retrieval rate, useful-result rate, false skips, and end-to-end latency against the current conservative rules. |
| P2 | Add a whole-preparation deadline. | Mapping lookup, memory preparation, RAG, attribution, and prompt assembly respect one observable latency budget rather than only bounding RAG HTTP time. | Stage timings and cancellation tests prove the total preparation bound and show which completed partial inputs were retained. |
| P2 | Support knowledge tool loops in code-backed custom planners where needed. | A custom planner can dynamically search and load knowledge only when its implementation explicitly supports the loop; tools are not injected into code that cannot execute them. | Contract tests cover supported, unsupported, disabled-policy, and restricted-tool cases. Existing auto-chunk consumers remain compatible. |
| P2 | Add source-specific delegated operational tests. | Synced Confluence and ServiceNow articles are tested through discovery, loading, operational execution, evidence, and delegation—not only manual-KB canaries. | A child loads the intended article, runs the prescribed read-only tool against the resolved scope, persists the correct source, and the parent reports evidence-backed results. |

## Reference documents versus procedures

Today every KB/article is reference knowledge. A loaded item can instruct the
model, but loading it does not mean its steps ran or its claims were observed.

The proposed procedure model should be explicit and author-declared rather than
inferred from titles or prose. Existing untyped content remains a reference
document. A procedure definition should include at least:

- applicability and required target/environment inputs;
- prerequisites and required permissions;
- ordered steps and which steps need approval;
- permitted tools or capability requirements;
- success, failure, and blocked completion criteria;
- evidence expected from each operational step.

Classification must not grant tools, bypass capability restrictions, or imply
that a procedure is safe for the current target.

### Focused procedure execution

Evaluate executing non-trivial procedures in a dedicated child scope. The child
would receive the selected procedure, resolved target/environment, allowed
tools, and completion criteria. It would retain step progress and evidence while
the parent continues to own the user request.

Adopt this only when tests show it improves adherence enough to justify another
agent invocation. Short reference lookups and simple one-step guidance should
not require delegation.

Acceptance requires a multi-step test proving that:

- ordered required steps execute or are explicitly blocked;
- target/environment scope survives delegation;
- approval-required actions cannot bypass confirmation;
- tool restrictions remain authoritative; and
- the parent distinguishes successful loading from evidence-backed completion.

## Performance and rollout validation

The product target is p50 30 seconds. A three-second automatic RAG deadline is a
budget, not proof that the end-to-end target is met. Before broad rollout,
measure by policy, planner type, and delegation depth:

- automatic-discovery invocation and skip rates;
- RAG p50/p95, timeout rate, cancellation rate, and retained-result count;
- mapping lookup, memory, attribution, and total preparation p50/p95;
- end-to-end response p50/p95;
- candidate load/adherence rate and useful-result rate;
- parent/child duplicate searches and reuse hit rate; and
- operational claims with and without matching live evidence.

RAG-server throughput and reranking performance can be improved independently.
Those optimizations must not weaken account scoping, KB enablement, attribution,
or evidence correctness.

## Explicit non-goals

- Eagerly loading every account or mapped KB.
- Restoring agent mappings as knowledge visibility boundaries.
- Treating `load_skills` as an executable workflow.
- Granting tools or permissions named inside a document.
- Adding a per-agent policy override before an account-level operational need is
  demonstrated.
- Inferring procedure type, environment, or execution success from document
  titles.
