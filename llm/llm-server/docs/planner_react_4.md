# ReAct4 Planner — Design Document

> **Status:** Phases 0–3b implemented and merged on the feature branch, all gated
> off by default (`LlmServerReAct4Enabled=false`). Remaining: Phase 4 (provider
> round-trip validation + eval parity, both needing a live env) and Phase 5 (XML
> deletion). This document is the plan of record for moving from prompt-driven
> (XML) function calling to **provider-native tool calling**. It mirrors the
> structure of [`planner_react_3.md`](planner_react_3.md); read that first for the
> baseline this replaces. Per-phase status is in the Phased Implementation Plan
> table below.

## Overview

ReAct3 manages function calling *in the prompt*: the base prompt teaches the model an
XML action grammar (`<thought_action>`, `<action>`, `<actions>`, `<final_answer>`,
`<update_notebook>`), and the planner parses that grammar back out of the raw completion
with a multi-stage XML-repair pipeline. This design existed because, when it was written,
not every provider supported function/tool calling.

ReAct4 moves to **provider-native tool calling**: tool schemas are sent to the provider as
first-class tool definitions, the model returns structured `tool_use` / `ToolCall` blocks,
and the planner consumes them directly. The XML action grammar, its parser, and its repair
pipeline are removed on the native path.

### Why this is far less risky than it looks

Three pieces of scaffolding already exist:

1. **Tool schemas already exist.** `NBTool.InputSchema() ToolSchema`
   (`tools/core/interface.go`) already returns a JSON-schema shape, and tools already accept
   structured args via `NBToolCallRequest{Command, Arguments, Context}`. We are not starting
   from `Call(input string)`.
2. **The provider layer already supports native tools — and we already drive it.**
   `bedrock`, `azure`, and `googleai/vertex` all implement `convertTools` / `WithTools`
   and emit `llms.ToolCall` / `llms.ToolCallResponse` (vendored langchaingo). More: the
   `AgentPlannerTypeTool` planner already calls native tools in prod via
   `nbToolsToLlmTools(tools)` + `llms.WithTools(...)` (`executor.go:1239`,
   `planner_prompt.go:72`). ReAct4 reuses that exact `ToolSchema → []llms.Tool`
   converter — it is not new code.
3. **The system already detects native calls — to suppress them.** `llm_common.go:1093`
   (`hasMalformedFunctionCall`) catches native/malformed function calls today and force-feeds
   the model back into XML. ReAct4 removes that guardrail rather than inventing detection.

### Goals

- Replace the XML action grammar with provider-native tool calling on capable providers.
- Delete the XML parsing / sanitization / repair code on the native path.
- Preserve every behavioral capability of ReAct3: parallel actions, write pre-flight safety,
  notebook discipline, critique/refinement, scratchpad compression, conversation resume.
- Coexist with ReAct3 behind a planner type + capability gate — **no unconditional cutover.**

### Non-goals

- Deleting ReAct3. It stays as the fallback for providers without native tool support (see
  [Provider Capability Gating](#provider-capability-gating)).
- Changing tool *implementations* or the `NBTool` interface. Tools already expose schemas and
  take structured args.
- Reworking the critique LLM call's own prompt logic (it is independent of tool calling; only
  the transcript it is shown changes shape).

## What Changes from ReAct3

| Aspect | ReAct3 | ReAct4 |
|--------|--------|--------|
| Action encoding | XML text (`<action>`/`<actions>`) in the completion | Native `ToolCall` blocks from the provider |
| Action parsing | Multi-stage XML extract + sanitize + repair + retry | Read `completion.Choices[0].ToolCalls` directly |
| Parallel actions | `<actions>` plural block, parsed and split | Multiple `ToolCall` blocks in one assistant turn (native) |
| Thought / reasoning | `<thought>` XML tag | Assistant text content block (or provider reasoning block) |
| Final answer | `<final_answer><content>` XML | Assistant turn with **zero** tool calls |
| Notebook | Inline `<update_notebook>` XML tag, extracted by planner | A registered **`update_notebook` tool** |
| Conversation state fed to LLM | Text **scratchpad** rebuilt into the human message each iteration | Native message history: `assistant(tool_use)` → `tool(tool_result)` turns |
| Format-error handling | XML repair pipeline + reformat retries | Native failure surface (malformed JSON args, bad tool name) — far smaller |
| Provider requirement | Any text model | Native tool-calling support (else falls back to ReAct3) |

## Provider Capability Gating

Native tool support is **not universal in this codebase.** Confirmed:

| Provider | Native tools | ReAct4 eligible |
|----------|-------------|-----------------|
| `bedrock` | Yes (`convertTools`) | Yes |
| `azure` | Yes (`WithTools`) | Yes |
| `googleai` / `vertex` | Yes (`convertTools`) | Yes |
| `sagemaker` | **No** | No — stays on ReAct3 |
| `huggingface` | **No** | No — stays on ReAct3 |
| `ollama` (config option) | Verify per-model | Gate on capability probe |

Introduce a capability check the executor consults before selecting the planner:

```go
// pseudo — resolved from provider + model
func SupportsNativeTools(provider, model string) bool
```

**Routing rule:** an agent that would run ReAct4 falls back to ReAct3 when its resolved
provider/model returns `false`. This is why ReAct3 cannot be deleted and why "clean up the
XML code" is really "add a capability-gated second path first, delete XML later."

## Effective Planner Type Resolution

ReAct3 slots in today via `executor.go` promoting `ReAct` / `Orchestrating` →
`AgentPlannerTypeReAct3` (`interface.go:351`, `AgentPlannerTypeReAct3 = "react_3"`).

ReAct4 adds `AgentPlannerTypeReAct4 = "react_4"` and one more resolution layer:

```
declared type (ReAct | Orchestrating)
  → ReAct4  if  LlmServerReAct4Enabled(agent/account) AND SupportsNativeTools(provider, model)
  → ReAct3  otherwise
```

The declared agent type still expresses *intent* (orchestrating vs task); ReAct4 vs ReAct3 is
the *implementation*, chosen at runtime by flag + capability — exactly the pattern used for
ReWoo→ReAct2→ReAct3 (which were flag-gated, then the losers deleted).

## Conversation Representation (the core change)

This is the substantive rewrite. ReAct3 keeps execution history as `intermediateSteps` and
**reconstructs a text scratchpad** into the human message every iteration
(`buildScratchpad`, `scratchpad.go`). ReAct4 keeps history as a **native message list**:

```
system:    <base prompt (no XML grammar)> + agent prompt + tool DEFINITIONS
human:      <task_context> + <question>          (no scratchpad)
assistant:  text("thought") + ToolCall(k8s, {...}) + ToolCall(logs, {...})
tool:       ToolCallResponse(k8s, "<observation>")
tool:       ToolCallResponse(logs, "<observation>")
assistant:  text("reflection") + ToolCall(update_notebook, {...})
tool:       ToolCallResponse(update_notebook, "ok")
assistant:  text("final answer")                 ← zero tool calls ⇒ terminal
```

Everything that hangs off the text scratchpad must move to operate on this message list:

- **Compression** (`scratchpad.go`, summarizer, `LlmServerScratchpadCompressionActivationFraction`,
  `LlmServerScratchpadMaxObservationChars`): today it truncates/summarizes observation *text*.
  In ReAct4 it truncates/summarizes the `ToolCallResponse` content blocks in history. Same
  budgeting rules (window-gated activation, last-N steps full, UTF-8-safe truncation), applied
  to message content instead of a rebuilt string.
- **Parallel-group rendering** disappears — parallelism is the natural "multiple `ToolCall`
  blocks in one assistant turn," not a text grouping heuristic.
- **Observation injection hardening** (zero-width escaping of `#PlanId` markers): tool_result
  content is still model-visible text, so keep the injection hardening; it just moves onto the
  `ToolCallResponse` payload.

## Tool Definitions & the Notebook Tool

> **Not to be confused with main's `k8s_orchestrator_native`.** That agent (`608e5d7d`) is a
> *kubectl-native* orchestrator — a curated lean tool set — and its own doc says it runs on
> "Reasoning + **ReAct3**". "Native" there means direct-cluster, not provider-native tool
> calling. It's an orthogonal axis; an Orchestrating agent, it would itself route to ReAct4
> when the flag + provider allow. No conflict with this work.

### Tool-resolution parity (react_3 ⇄ react_4)

`NewReActAgent4` resolves its tool set through the **same steps** `reActCreatePrompt3` applies
for react_3, so a react_4 agent runs with an identical tool surface (`resolveReact4Tools`):
client tools → account-configured tools (`AgentAdditionalInstructionsAndToolsAndConfigs`) →
`FilterAndInjectDefaultTools` (injects `load_skills` on KB-mapped agents, plus shell/watch) →
`FilterTools(capabilities)` → then the `update_notebook` tool (added last so capability
filtering never drops it). The account-configured `<additional_agent_prompt>` is placed in the
system prefix, and the human message carries the global-preferences / KB-prestep / skill-lists
blocks — matching react_3's human-message context so KB/skill flows behave identically.

### Tool definitions

Map each agent's `GetSupportedTools()` → `[]llms.Tool` from `NBTool.InputSchema()`. This is a
mechanical `ToolSchema → llms.Tool.Function.Parameters` conversion (the providers already
convert `llms.Tool` onward). Two invariants for caching (below): the tool list must be
**order-stable** and **content-stable** per (agent, account, model).

### Notebook as a tool (dispatch-and-derive)

The notebook becomes a real registered tool: `update_notebook` with a `{ content: string }`
schema (`tools/tool_notebook.go`, canonical name in `tools/core`). **Implemented as
dispatch-and-derive, not intercept-and-drop** — the cleaner of the two:

- The planner injects `update_notebook` into the tool list when the agent runs with a notebook
  (`ensureNotebookTool`, via the tool registry since `agents/core` can't import `tools`).
- The model's `update_notebook` call is **dispatched like any other tool**; the tool's `Call`
  validates and echoes the content back as the observation. This means the tool_use/tool_result
  pairing every provider requires is satisfied for free, and there is **no empty-turn edge case**
  (a turn that only updates the notebook still produces a real step).
- At the start of every `Plan()`, `refreshNotebookFromSteps` scans `intermediateSteps` for the
  most recent `update_notebook` step and sets `o.Notebook`; it is injected into the human
  message as a `<notebook>` block. `renderStepsToMessages` **skips** notebook steps (both halves,
  so nothing dangles) to keep the reconstructed tool history clean — the notebook lives in the
  human block instead.

> Rejected the intercept-and-drop variant (mutate state, don't dispatch): it creates a turn with
> zero dispatchable actions when the model only updates the notebook, which collides with the
> executor's `ErrAgentNoReturn` contract. Dispatch-and-derive sidesteps that entirely.

## Execution Flow

### Plan loop

```
for iteration 0..maxIterations:
  1. Assemble messages: system (cached) + history (native turns)
  2. GenerateContent(messages, WithTools(toolDefs), ...cacheOpts)
  3. completion.Choices[0].ToolCalls:
       - empty  → terminal: assistant text is the final answer → critique (if enabled)
       - nonempty → append assistant turn (text + ToolCalls) to history;
                    route ToolCalls to the executor (parallel/sequential)
  4. Append each ToolCallResponse to history
```

No parse-failure retry ladder. Native failure modes are narrower and handled discretely:

| Failure | Handling |
|---------|----------|
| Malformed JSON in tool args | Return a `ToolCallResponse` error naming the bad field; model retries (the `MissingFieldsResponder` escape hatch in `interface.go:56` already models this) |
| Hallucinated tool name | Providers usually reject; if surfaced, return an error tool_result listing valid tools |
| Empty assistant turn (no text, no tools) | Nudge once (`llm_common.go` already has an empty-content nudge), then treat as terminal |

### Parallel execution & write safety

Providers emit multiple `ToolCall`s in one turn natively, replacing the `<actions>` batch.
**The write pre-flight is retained unchanged in spirit:** before dispatching a multi-tool
turn, classify each call; if any is non-read, fall back to sequential (only one approval
followup can be active at a time — `planner_react_3.md` Phase 3). The one code change:
`ToolRequestInference.InferToolRequestType(ctx, toolName, input string)` is fed the
**serialized `Arguments`** instead of the XML `tool_input` string.

`doIterationParallel()`, the dependency graph, the semaphore
(`LLMServerAgentMaxParallel`), and `PlannerParallelExecEnabled` are reused as-is — they operate
on resolved actions, which now come from `ToolCall`s instead of parsed XML.

### Critique & refinement

Unchanged in logic. The critique LLM call (`planner_react_critiquer.txt`) is independent of
tool calling. Only the transcript it renders changes from "XML scratchpad" to "native history
flattened to text." Refinement still appends feedback + prior answer and re-enters the loop
(max 2 attempts); `postRefinementToolIndex` still marks the compression boundary.

## Caching

**No regression, because the tail was never cached for most agents.** Per
[`caching.md`](caching.md): Account/Global scope (the common case) caches **only system
messages**; the scratchpad tail is already uncached. ReAct4's native history tail is likewise
uncached there — caching-neutral.

Two implementation invariants:

1. **Tool definitions must sit in the cached prefix.** They are stable per (agent, account,
   model), so they belong with the system messages for cache byte-matching. Verify each
   provider includes tool defs in the cached prefix (Anthropic/Bedrock: before the cache
   breakpoint; Google AI: in `CreateCachedContent`). Order/content must be stable — sort tool
   lists deterministically.
2. **`Conversation`-scope agents** (the default when `NBAgentCacheScopeProvider` is
   unimplemented, 10m TTL) cache "up to the last human message." Under native history this
   prefix now includes prior `tool_use`/`tool_result` turns — still cacheable, arguably a
   better hit shape, but confirm the breakpoint placement per provider.

## State Persistence (Marshal / Unmarshal)

ReAct3 serializes `Notebook`, `refinementAttempts`, `postRefinementToolIndex`, and executor
`steps` / `stepKeys`. ReAct4 serializes the **native message list** (or the steps it's derived
from) plus the same planner fields. Resume (`POST /v2/chat` with the same `conversation_id`)
rebuilds history from persisted turns instead of rebuilding a scratchpad string.

> `stepKeys` duplicate-prevention still matters — keep the belt-and-suspenders restore
> (`planner_react_3.md` — "assignment to entry in nil map" panic). Native `ToolCall` IDs give a
> natural dedupe key, replacing `generateToolId(tool, input)`.

## Coexistence & Rollout

1. **Land ReAct4 behind `LlmServerReAct4Enabled` (default off)**, capability-gated. ReAct3 is
   untouched and remains the default runtime.
2. **Eval before flip.** Run the existing eval framework (`agents/core/evaluator.go`:
   Correctness / Relevance / Completeness / Helpfulness) and the `prompts/` A/B harness on the
   same query set through both planners. Compare scores, token cost, and tool-call correctness.
3. **Canary by account/agent**, orchestrators last (largest blast radius).
4. **Only after parity holds**, delete the XML path *for native-capable providers* and shrink
   `planner_react_4_base.txt`. ReAct3 survives as long as `sagemaker`/`huggingface`/non-tool
   `ollama` are supported.

## Code Deletion Inventory (the payoff, realized in step 4 above — not up front)

- XML action grammar in `planner_react_*_base.txt` (single + parallel action format, examples).
- `parseOutputInternal` and its stages: direct extract, XML sanitization, ampersand escaping,
  mismatched-tag repair, reformat-retry ladder (`planner_react_3.md` Phase 2).
- `<update_notebook>` inline extraction (`processNotebookUpdate`, ~`planner_react_3.go:990-1108`).
- The `hasMalformedFunctionCall` suppression path (`llm_common.go:1093-1099`) — inverted:
  native calls become the desired output, not a rejected one.
- Parallel-group text detection in scratchpad building.

## Open Questions / Risks

1. **Thought visibility.** Some providers don't reliably emit assistant text alongside tool
   calls (or bury it in a reasoning block). If "thought" is load-bearing for critique/telemetry,
   confirm each provider surfaces it, or make the notebook/first-tool-arg carry intent.
2. **Tool-definition token cost.** Sending schemas for large tool sets each call — cacheable,
   but confirm net token delta vs. today's XML-in-system-prefix. Per-agent tool-set size
   matters; orchestrators with many tools are the worst case.
3. **`ollama` / self-hosted model tool fidelity.** Even where the API accepts tools, small
   models may call them poorly. Keep those on ReAct3 until measured.
4. **Sub-agent nesting.** Each sub-agent runs its own loop; confirm native tool history nests
   cleanly (sub-agent tool_use turns don't leak into the parent's history in a way that breaks
   the parent's caching prefix).
5. **Critique transcript fidelity.** Flattening native history to text for the critiquer must
   preserve the evidence chain the 5-Whys rules depend on.

## Phased Implementation Plan

| Phase | Work | Gate |
|-------|------|------|
| 0 ✅ | `SupportsNativeTools`, `AgentPlannerTypeReAct4`, `LlmServerReAct4Enabled`, routing seam in `executor.go` (`useReAct4Engine`, logs decision, runs react_3 until the native planner lands) | Unit tests on routing — **done** |
| 1 ✅ | `ToolSchema → []llms.Tool` conversion (reuses existing `nbToolsToLlmTools`); `update_notebook` control tool (`tools/tool_notebook.go`) | Tool defs render + call round-trips — **done** |
| 2 ✅ (engine) | `NBReActPlanner4` native plan loop, terminal detection, steps→native-message reconstruction, `ToolCalls`→actions, notebook-as-tool, Marshal/Unmarshal, parallel/write gate extended, wired into `createAgentPlanner` | Unit tests on parse/render/marshal — **done**; provider round-trip is an eval concern (Phase 4) |
| 2b ✅ | `planner_react_4_base.txt` (native operating instructions + notebook-tool discipline + shared rules, no XML grammar), rendered + prepended in `NewReActAgent4`. `effectivePlannerType` stays react_3 so the react-style formatter/citation gates keep working unchanged (react_4 assigns the same E1/E2 DisplayIDs) | Base-prompt gating tests — **done** |
| 3a ✅ | Window-gated compression of `tool_result` content in `renderStepsToMessages` (reuses `SummarizeObservation`/`scratchpadBudget`/`TruncateMiddle`/`recentStepsFullContext`; caches on `CompressedObservation`) | Recency + threshold unit tests — **done** |
| 3b ✅ | Answer critique/refinement retargeted: `Plan()` refine loop runs the critiquer on a text-flattened native transcript (`flattenTranscript`); on `refine` it appends the rejected answer + feedback and re-prompts, bounded by `maxRefinementAttempts`. Gate mirrors react_3 (`shouldCritique`). DB persistence of critiques not yet wired. | Gate + transcript unit tests — **done** |
| 4 | Eval parity, canary rollout. Comparison harness landed: `planner_react_comparison_e2e_test.go` (`//go:build e2e`) runs seed cases through both planners against a live backend, scores via `EvaluateAgentResponse`, and fails on regression (outcome, correctness drop, dropped must-hit tool). **Provider round-trip validation + running the harness need a live env.** | Eval scores ≥ ReAct3 on shared set; no HARD/CORRECTNESS/TOOL regressions |
| 5 | Delete XML path for native providers; shrink base prompt | ReAct3 retained only for non-tool providers |

## Key Files (anticipated)

| File | Purpose |
|------|---------|
| `agents/core/planner_react_4.go` | Native plan loop, terminal detection, history assembly |
| `agents/core/planner_react_shared.go` | Shared symbols (extend, don't fork) |
| `agents/core/executor.go` | ReAct4 resolution + capability gate |
| `agents/core/executor_planner.go` | Reused: parallel exec, write pre-flight (args serialized) |
| `agents/core/llm_common.go` | Remove `hasMalformedFunctionCall` suppression; add `WithTools` on native path |
| `agents/prompts_repo/planner_react_4_base.txt` | Thin base prompt (no XML grammar) |
| `llms/*/` | Verify tool defs land in the cached prefix per provider |
| `tools/tool_notebook.go` (new) | `update_notebook` tool |
