# ReAct3 Planner — Design Document

## Overview

ReAct3 is a hybrid iterative planner that combines the reasoning-acting loop of ReAct with parallel action execution. It serves as the upgrade path from both ReAct2 (single-action iterative) and ReWoo (plan-then-execute) planners, offering iterative replanning with multi-action parallelism.

ReAct3 is designed to be activated via config flags without requiring agent code changes, making it a drop-in replacement for domain agents.

```
User Query
  → LLM Reasoning (thought + action selection)
  → Action(s) Execution (single or parallel)
  → Observation(s) appended to scratchpad
  → LLM Reasoning (reflect on observations)
  → ... (iterate until final answer or max iterations)
  → Critique (quality gate for top-level investigation agents)
  → Refinement (if rejected, with feedback)
  → Response
```

## Key Files

| File | Purpose |
|------|---------|
| `agents/core/planner_react_3.go` | Main planner: plan generation, output parsing, parallel actions, critique |
| `agents/core/planner_react_2.go` | Predecessor: single-action ReAct (for comparison) |
| `agents/core/executor_planner.go` | Execution engine: iteration loop, parallel/sequential routing, write pre-flight |
| `agents/core/executor.go` | Agent executor: effective planner type, config-driven upgrade, response formatting |
| `agents/prompts_repo/planner_react_3_base.txt` | Base system prompt with parallel action format |
| `agents/prompts_repo/planner_react_critiquer.txt` | Answer critique prompt |

## What Changed from ReAct2

| Aspect | ReAct2 | ReAct3 |
|--------|--------|--------|
| Actions per iteration | 1 | 1 or N (parallel) |
| XML format | `<action>` only | `<action>` or `<actions>` (plural) |
| Parallel execution | Not supported | Via executor worker pool + dependency graph |
| Write safety | N/A | Pre-flight check falls back to sequential |
| Scratchpad grouping | Flat list | Groups parallel actions under shared thought |
| Config-driven upgrade | N/A | Replaces ReAct and ReWoo via flags |

## Core Data Structures

### NBReActPlanner3

```go
type NBReActPlanner3 struct {
    Notebook                string   // Investigation state for long-term memory
    refinementAttempts      int      // Critique rejection count
    postRefinementToolIndex int      // Tool boundary for scratchpad compression
    enableCritique          bool     // Per-agent critique override
    request                 NBAgentRequest
    agent                   NBAgent
}
```

### Action XML Formats

**Single action** (backward compatible with ReAct2):

```xml
<thought_action>
  <thought>I need to check the pod status first</thought>
  <action>
    <tool_name>kubectl</tool_name>
    <tool_input>get pods -n production</tool_input>
  </action>
</thought_action>
```

**Parallel actions** (new in ReAct3):

```xml
<thought_action>
  <thought>I'll check pods, services, and deployments simultaneously</thought>
  <actions>
    <action>
      <tool_name>kubectl</tool_name>
      <tool_input>get pods -n production</tool_input>
    </action>
    <action>
      <tool_name>kubectl</tool_name>
      <tool_input>get services -n production</tool_input>
    </action>
    <action>
      <tool_name>kubectl</tool_name>
      <tool_input>get deployments -n production</tool_input>
    </action>
  </actions>
</thought_action>
```

**Final answer**:

```xml
<final_answer>
  <thought>Based on the investigation...</thought>
  <content>The root cause is...</content>
</final_answer>
```

### Prompt Constraints on Parallel Actions

The system prompt enforces:
- Limit parallel actions to 3-5 tools per step (max 5)
- NEVER parallelize actions that create, modify, or delete resources
- Only read/query operations should be parallelized
- First step: prefer a single discovery action before parallelizing

## Config-Driven Upgrade Mechanism

### Feature Flags

| Flag | Effect |
|------|--------|
| `LlmServerReAct3Enabled` | All ReAct agents → ReAct3 |
| `LlmServerRewooToReact3Enabled` | All ReWoo agents → ReAct3 (hybrid mode) |

### Effective Planner Type Resolution

In `executor.go`:

```go
effectivePlannerType := agent.GetPlannerType()

if effectivePlannerType == AgentPlannerTypeReWoo && config.Config.LlmServerRewooToReact3Enabled {
    effectivePlannerType = AgentPlannerTypeReAct3
} else if effectivePlannerType == AgentPlannerTypeReAct && config.Config.LlmServerReAct3Enabled {
    effectivePlannerType = AgentPlannerTypeReAct3
}
```

This affects:
1. **Prompt template selection**: Upgraded ReWoo agents use ReAct-style prompts (iterative, not plan-upfront)
2. **Planner instantiation**: `NewReActAgent3()` instead of `NewReActAgent2()` or `NewReWooAgent2()`
3. **Execution routing**: Parallel execution enabled for ReAct3 instances
4. **Response formatting**: ReAct3 included in the multi-agent response formatter gate

### Instance Detection at Runtime

```go
_, isReAct3Planner := e.agentPlanner.(*NBReActPlanner3)
```

Used in executor to route to parallel execution logic regardless of the agent's declared planner type.

## Execution Flow

### Phase 1: Plan Generation

The `Plan()` method (main loop, `maxOuterIterations = 5`):

```
for attempt 0..5:
  1. Build scratchpad from intermediateSteps
  2. Call LLM with scratchpad + input
  3. Parse output → actions[] or finishAction
  4. On parse failure: retry with XML recovery prompts (max 2)
  5. On final answer: run critique (if enabled)
  6. Return actions or finish
```

**Special cases**:
- **Direct summarization**: If last tool was successful and `ShouldSummarizeNow()`, invoke summary tool immediately without LLM reflection
- **Summary tool used**: Return observation as final answer directly

### Phase 2: Output Parsing (Multi-Stage Robustness)

`parseOutputInternal()` attempts extraction in order:

1. **Direct extraction**: Try on raw LLM output
2. **XML sanitization**: Close unclosed tags, fix common typos
3. **Escape ampersands**: Handle bare `&` entities
4. **Fix mismatched tags**: Diagnostic repair for common patterns
5. **Fallback**: Return diagnostic error for retry prompt

**Parallel action parsing** (`processToolActions()`):
- Requires `<actions>` (plural) block
- Validates minimum 2 actions (otherwise falls through to single-action parsing)
- Extracts shared `<thought>` across all actions
- Generates deterministic `toolID` via `generateToolId(toolName, input)`
- All actions in a batch share the same thought/log

**Single action parsing** (`processToolAction()`):
- Backward compatible with ReAct2 format
- Looks for `<thought_action>`, `<tool_name>`, `<action>` tags

### Phase 3: Executor Routing (Sequential vs Parallel)

In `executor_planner.go`, the decision point:

```
if len(actions) > 1 && PlannerRewooParallelExecEnabled && (isReWOO || isReAct3) {
    // Pre-flight write detection
    for each action:
        if tool implements ToolRequestInference:
            reqType = InferToolRequestType(action)
            if reqType != Read → hasWriteAction = true
        else if tool implements ToolRequestInferencePrompt:
            // Can't cheaply classify → assume potentially write
            hasWriteAction = true

    if hasWriteAction:
        → sequential (log: "falling back to sequential — potential write actions")
    else:
        → doIterationParallel()
}
```

**Why pre-flight write detection**: Only one approval followup can be active at a time. If 3 parallel `kubectl delete` commands all trigger approval, they collide — only the last one survives. Sequential execution ensures each approval completes before the next begins.

**Classification tiers**:
- `ToolRequestInference`: Static keyword heuristic (fast, no LLM call)
- `ToolRequestInferencePrompt`: LLM-based classification (slow, deferred)
- If only LLM-based available: conservatively assume write → sequential

### Phase 4: Parallel Execution

`doIterationParallel()` in `executor_planner.go`:

```
1. Build ActionNode dependency graph (intra-batch only)
   - Each action → ActionNode with internal dependencies
   - External dependencies (from previous iterations) filtered out

2. Create semaphore: buffered channel with LLMServerAgentReWooMaxParallel permits

3. Submit ready nodes (zero pending deps) to ExecutePlannerWorkerPool:
   - Acquire semaphore permit
   - Check context cancellation
   - Build query context from dependency outputs
   - Evaluate conditions (expression or LLM)
   - Call doAction() → execute tool
   - Mutex-protected result storage
   - Release semaphore, send result to channel

4. Result collection loop:
   - On result: append to steps, clear from dependent nodes, resubmit newly ready
   - On terminal response (waiting, followup): early exit
   - On context done: abort

5. Return accumulated steps
```

**Thread safety**: Mutex protects `completedSteps` map and node status updates.

### Phase 5: Scratchpad Building

The scratchpad reconstructs LLM context from execution history:

**Parallel group detection**: Consecutive steps sharing the same `Action.Log` (thought) are grouped under a single `<actions>` wrapper:

```
Thought: I'll check pods, services, and deployments simultaneously

Observation [kubectl - get pods]:
NAME              READY   STATUS
api-server        1/1     Running
worker            0/1     CrashLoopBackOff

Observation [kubectl - get services]:
NAME          TYPE        CLUSTER-IP
api-service   ClusterIP   10.0.0.5

Observation [kubectl - get deployments]:
NAME          READY   UP-TO-DATE
api-server    3/3     3
worker        0/1     1
```

**Semantic compression**:
- Recent 10 steps (after `postRefinementToolIndex`): full observation context
- Older steps: truncated to ~100 bytes + `[output truncated — N chars]`
- UTF-8 safe truncation at byte boundaries
- Size budget controlled by `LlmServerAgentMaxScratchpadChars`

### Phase 6: Critique & Refinement

**Trigger conditions** (only for meaningful quality gating):

```go
isTopLevel := request.ParentAgentId == "" || request.ParentAgentId == request.AgentId
critiqueAllowed := enableCritique ||
    (LlmServerReActCritiqueEnabled && isTopLevel && IsInvestigationRequestTask())
```

- Only top-level agents (not sub-agents like kubectl, logs)
- Only investigation queries (not simple queries)
- Optional per-agent override via `CritiqueEnabled()` interface

**`runCritique()` method**:

1. Build critique prompt with: input, scratchpad, final answer, available tools, tool descriptions
2. LLM returns `<decision>accept|refine</decision>` + `<feedback>`
3. Up to 2 retries on empty/unparseable decision
4. On `refine`: append feedback + prior answer to message history, loop again (max 2 refinement attempts)
5. `postRefinementToolIndex` updated to track tool boundary for compression
6. Critique persisted to DB for analysis

**Critique rules** (from `planner_react_critiquer.txt`):
- Reject status-only answers ("Running" is not a root cause)
- Reject manual CLI instructions (zero tolerance for "please run kubectl...")
- Enforce side effects when workspace tools available
- Require 5-Whys root cause chain for investigations
- Reject tool failure surfacing when alternatives exist in notebook

### Phase 7: Fallback Summarization

If the main loop breaks with an error but has prior steps:

1. Build scratchpad from all executed steps
2. Call LLM: "synthesize this data into a summary"
3. Return synthesis instead of raw error

## Notebook & Memory

### Notebook

Stores investigation state across iterations:

- Updated via `<update_notebook>` tag in LLM output
- Passed to all LLM calls to maintain long-term context
- Serialized in `Marshal()` for conversation persistence
- Format: plain text, LLM-managed investigation tracking

### Memory Generation (Post-Execution)

Triggered asynchronously after execution completes:
- Background worker extracts patterns/facts from response
- Injected into knowledge base for next conversation turn

## State Persistence

### Marshal/Unmarshal

Serializes full planner state as JSON for conversation resumption:

```go
// Serialized fields
- Notebook
- refinementAttempts
- postRefinementToolIndex

// Executor-level (in executor_planner.go)
- currentIteration
- steps (accumulated tool results)
- currentActions (in-flight batch)
- toolCallCache
- stepKeys (duplicate prevention map)
```

### stepKeys Map

Tracks which tool actions have been processed to prevent duplicates:

```go
stepKeys map[string]bool  // toolID → processed
```

**Critical**: Must be restored in `Unmarshal()`. Without restoration, writes to `stepKeys` after conversation resumption cause `"assignment to entry in nil map"` panic. Belt-and-suspenders approach:
1. Restore from serialized `stepKeys` data
2. Rebuild from restored steps (ensure every step's toolID is present)

## Prompt Structure & Caching

### System Message (stable, cached at Account scope)

```
┌──────────────────────────────────────────────┐
│ 1. Base prompt (planner_react_3_base.txt)     │
│    - Tool names and descriptions              │
│    - Response format rules (single + parallel)│
│    - Shared rules (context, time, data prot.) │
├──────────────────────────────────────────────┤
│ 2. Client tools priority (if any)             │
├──────────────────────────────────────────────┤
│ 3. Account prompt (optional)                  │
├──────────────────────────────────────────────┤
│ 4. Agent-specific prompt (domain expertise)   │
└──────────────────────────────────────────────┘
```

### Human Message (dynamic, changes every iteration)

```
┌──────────────────────────────────────────────┐
│ <task_context>                                │
│   today, conversation_context, history        │
│ </task_context>                               │
│ <question>{user input}</question>             │
│ {scratchpad}  ← grows each iteration          │
└──────────────────────────────────────────────┘
```

This split enables LLM provider-level prompt caching on the stable system prefix. See [caching.md](caching.md) for details.

## Error Handling

### Parse Failure Retries

Up to 2 retry attempts with escalating prompts:
- Diagnostic reason for failure
- Tool names list (prevents hallucinated tools)
- Format reinforcement examples
- Backoff: `time.Sleep(retryCount * time.Second)`

### Consecutive Failed Iterations

Executor tracks iterations with zero valid actions:
- After 2 consecutive failures → break loop
- Triggers summarization fallback (synthesize whatever was collected)

### Tool Call Caching

`turnToolCallCache` normalizes results across plan regeneration within a single turn:
- Prevents duplicate tool executions when the LLM re-emits the same action
- Cache hits/misses logged at executor termination

## Relationship to ReWoo

ReAct3 can replace ReWoo as a hybrid upgrade via `LlmServerRewooToReact3Enabled`:

| Aspect | ReWoo | ReAct3 (Hybrid) |
|--------|-------|-----------------|
| Planning | Full plan upfront, then execute | Iterative: reason → act → observe → reason |
| Replanning | Mid-execution review on failure | Every iteration (inherent in loop) |
| Parallel | DAG-based dependency resolution | `<actions>` blocks with pre-flight write check |
| Solver | Dedicated solver LLM pass | LLM emits `<final_answer>` directly in loop |
| Critique | Plan critique + Answer critique | Answer critique only |
| Adaptability | Fixed plan, review on failure | Naturally adaptive each iteration |
| Token cost | Lower (one plan call + solver) | Higher (LLM call per iteration) |
| Recovery | Skip propagation + replanning | Reflect on failure → try alternative next |

### Upgrade Benefits

- **No agent code changes**: Config flag flips planner for all domain agents
- **Better error recovery**: Iterative loop naturally adapts to failures
- **Parallel reads**: Independent queries grouped by LLM in `<actions>` blocks
- **Write safety**: Pre-flight check prevents parallel approval collisions
- **Gradual rollout**: Flag per environment (dev → test → prod)

## Test Coverage

Key test cases in `planner_react_3_test.go`:

| Test | Validates |
|------|-----------|
| `TestReAct3ParseSingleAction` | Backward compatibility with ReAct2 format |
| `TestReAct3ParseParallelActions` | Three-action parallel parsing |
| `TestReAct3ParseParallelActionsWithCDATA` | CDATA handling in tool_input |
| `TestReAct3ParseParallelMixedCDATAAndPlain` | Mixed CDATA + plain text inputs |
| `TestReAct3ParseParallelEmptyActionsBlock` | Edge case: empty `<actions>` block |
| `TestReAct3MarshalUnmarshal` | State serialization roundtrip including stepKeys |
| Scratchpad building tests | Parallel group detection, compression, size budgeting |
