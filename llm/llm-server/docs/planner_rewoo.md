# ReWoo Planner — Design Document

## Overview

ReWoo (Reasoning Without Observation) is the plan-then-execute planner used by top-level domain agents (AWS, K8s, GCP, Azure debug agents). It generates a complete execution plan upfront, executes steps respecting a dependency DAG, and synthesizes observations into a final answer via a dedicated Solver.

ReWoo is designed for multi-step autonomous troubleshooting where the problem space can be decomposed into parallel-capable investigation branches.

```
User Query
  → Classifier (direct answer or plan?)
  → Planner LLM (generates full XML plan)
  → Plan Critiquer (validates plan structure & logic)
  → Executor Loop (runs steps, respects dependency order)
      → Sub-Agents (ReAct) execute individual tools
  → Reviewer (mid-execution plan updates on failure)
  → Solver LLM (compiles observations into final answer)
  → Answer Critiquer (quality gate: rejects shallow answers)
  → Response
```

## Key Files

| File | Purpose |
|------|---------|
| `agents/core/planner_rewoo_2.go` | Main planner: plan generation, dependency graph, review loop |
| `agents/core/planner_rewoo_solver.go` | Answer synthesis from observations |
| `agents/core/planner_rewoo_critiquer.go` | Answer quality gate |
| `agents/core/planner_rewoo_plan_critiquer.go` | Plan structure validation |
| `agents/core/executor_planner.go` | Execution engine: iteration loop, parallel/sequential execution |
| `agents/core/executor.go` | Agent executor: planner selection, response formatting |
| `agents/prompts_repo/planner_rewoo_2_base.txt` | Base planner system prompt |
| `agents/prompts_repo/planner_rewoo_solver.txt` | Solver prompt |
| `agents/prompts_repo/prompt_rewoo_critiquer.txt` | Answer critiquer prompt |
| `agents/prompts_repo/prompt_rewoo_plan_critiquer.txt` | Plan critiquer prompt |
| `agents/prompts_repo/planner_rewoo_2_reviewer.txt` | Mid-execution reviewer prompt |

## Core Data Structures

### PlannerNode

Represents a single step in the execution graph:

```go
type PlannerNode struct {
    Step       rewooPlannerStep2  // ID, tool, query, reason, dependencies, condition
    Status     string             // pending | running | completed | failed | skipped | waiting
    Output     string             // Observation/result from execution
    Iteration  int                // Which iteration generated this node
    References []Reference        // Citation links from tool results
}
```

### ReWooPlanner2

```go
type ReWooPlanner2 struct {
    executionGraph      map[string]*PlannerNode  // Step ID → node
    Notebook            string                   // Long-term memory across iterations
    solver              *ReWooSolver             // Answer synthesizer
    planCritiquer       *ReWooPlanCritiquer      // Plan validator
    refinementAttempts  int                      // Current count
    maxRefinementAttempts int                    // Default: 3
}
```

### XML Plan Format

```xml
<plan_response>
  <thought>High-level reasoning about investigation strategy</thought>
  <plan>
    <step>
      <id>E1</id>
      <tool>kubectl</tool>
      <query>get pods -n production --field-selector status.phase!=Running</query>
      <reason>Check for non-running pods in production</reason>
      <dependency></dependency>
      <condition></condition>
    </step>
    <step>
      <id>E2</id>
      <tool>kubectl</tool>
      <query>describe pod {{E1.failing_pod}} -n production</query>
      <reason>Get details on the failing pod identified in E1</reason>
      <dependency>E1</dependency>
      <condition></condition>
    </step>
  </plan>
</plan_response>
```

- **Dependencies** form a DAG — independent steps can run in parallel
- **Conditions** gate execution on previous step outcomes (expression or LLM-based)
- **Macros** like `{{E1.field}}` are resolved at execution time from dependency outputs

## Execution Flow

### Phase 1: Classification (Fast Path)

On the first iteration, the planner classifies the query:

1. **Keyword matching**: greetings, investigation keywords, data retrieval patterns
2. If **direct answer** candidate: call Solver immediately, skip planning
3. Falls back to full planning on solver failure or missing information

### Phase 2: Plan Generation

```
initializeGraph()
  → LLM generates XML plan (up to 3 structural refinement attempts)
  → XML parsing with 4-stage recovery (unmarshal → escape ampersands → fix tags → regex fallback)
  → Dependency validation (fuzzy-match IDs, detect cycles)
  → Plan critique (if enabled): reject → regenerate with feedback
  → Store plan in DB
```

**Structural Error Recovery**: The parser detects and corrects:
- Invalid dependency IDs (fuzzy matching)
- Missing required XML tags
- JSON/markdown returned instead of XML
- Mismatched/unclosed tags

### Phase 3: Execution Loop

The executor (`executor_planner.go`) drives the main iteration loop:

```
for iteration 0..maxIterations:
  1. planner.Plan(intermediateSteps, input) → actions[], finish?
  2. If finish: return final answer
  3. Execute actions (sequential or parallel)
  4. Accumulate results in executor.steps
  5. Fast-fail: 2+ consecutive empty/failed iterations → break
```

#### Batch Selection

`getRunnableBatch()` selects the next set of executable steps:

1. Find all pending steps whose dependencies are all completed
2. Apply serialization policy: tools requiring user configuration (postgres, mysql) run one per batch
3. Handle tool confirmations from `QueryConfig`
4. Return batch + any waiting steps with followup context

#### Sequential vs Parallel Execution

**Decision point** (executor_planner.go):

```
if len(actions) > 1 && PlannerRewooParallelExecEnabled {
    // Pre-flight: check for write actions
    if hasWriteAction {
        → sequential (approval followups can't run in parallel)
    } else {
        → doIterationParallel()
    }
}
```

**Parallel execution** (`doIterationParallel()`):
1. Build ActionNode dependency graph (intra-batch only)
2. Create semaphore with `LLMServerAgentReWooMaxParallel` permits
3. Submit all zero-dependency nodes to worker pool
4. On result: clear from dependent nodes, resubmit newly ready nodes
5. Early termination on terminal responses (waiting, followup)
6. Thread safety via mutex on `completedSteps` and node status

### Phase 4: State Synchronization

After each iteration, `syncState()`:

1. Maps tool execution results back to executionGraph nodes
2. Converts tool status → graph status (success, failed, waiting, skipped)
3. Triggers **skip propagation**: iteratively marks pending nodes as skipped if they depend on failed/skipped nodes from the same or later iteration
4. Does NOT skip if dependency failed in an earlier iteration (allows recovery retries)
5. Persists updated plan state to database

### Phase 5: Dynamic Review (Mid-Execution Replanning)

Triggered when steps fail OR `load_skills` completes:

```
reviewAndRefinePlan()
  → Sends current progress + failures + remaining plan to Reviewer LLM
  → Reviewer can: add/remove/reorder steps, mark as complete, or continue
  → Enriches with loaded skill context if available
  → Increments refinementAttempts (max 3)
```

### Phase 6: Solver (Answer Synthesis)

When all steps are done (`allStepsDone()`):

```
Solver.Solve()
  → Build structured observation context (ConstructScratchPad)
  → Detect data sufficiency (hasToolFailureMajority heuristic)
  → LLM compiles observations into <final_answer> or <missing_information>
  → Meta-talk rejection: rejects "please run kubectl..." style answers
  → Up to 3 LLM attempts with corrective instructions
```

**Output formats**:
- `<final_answer>` with `<thought>` + `<content>` — proceeds to critique
- `<missing_information>` with `<required_information>` — triggers replanning

### Phase 7: Answer Critique

Quality gate that rejects shallow or incomplete answers:

1. **No status-only answers**: "Running" or "Healthy" is not a root cause
2. **No manual CLI instructions**: Agent must execute tools, not suggest commands
3. **5-Whys depth**: For investigations, must identify foundational root cause
4. **Evidence-based**: Findings must cite tool observations
5. **Actionable feedback**: On rejection, specifies what tool action to take next

Decisions: `accept` (return answer) or `refine` (re-enter solve/critique cycle, max 2 refinement attempts)

## Observation Context Building

### Scratchpad Format

Each completed step contributes a structured observation:

```
#PlanId E1
#ToolName kubectl
#Question get pods -n production
#Answer
NAME                    READY   STATUS    RESTARTS   AGE
api-server-abc123       1/1     Running   0          2d
worker-def456           0/1     CrashLoopBackOff   5  10m
```

### Semantic Compression

- **Recent 10 steps**: Keep full observation context
- **Older steps**: Truncate to 100 bytes + `[output truncated — N chars]` marker
- **UTF-8 safe**: Truncation walks byte boundaries
- **Failed/skipped** results prefixed with `[FAILED]` / `[SKIPPED]`

### Data Quality Tag

```xml
<data_quality failed="2" empty="1" success="5" />
```

Used by Solver and Critiquer to calibrate expectations when many tools failed.

## Condition Evaluation

Steps can be conditionally executed based on previous outputs:

### Expression Conditions

Govaluate expressions evaluated against dependency outputs:

```xml
<condition>previousOutputs["E1"] != "" && contains(previousOutputs["E1"], "error")</condition>
```

### LLM Conditions

Natural language classification via lite model:

```xml
<condition type="llm" prompt="Does the output indicate a failure?">yes,no</condition>
```

Sent to lite model with dependency outputs; expects one of the allowed responses.

## State Persistence & Conversation Resumption

### Serialization

`Marshal()` / `Unmarshal()` serialize the full planner state as JSON:

- Execution graph (all nodes, statuses, outputs)
- Notebook content
- Refinement state (attempts, plan ID)
- Executor state (iteration count, steps, tool call cache)

### Resumption Protocol

1. Client re-sends same `conversation_id` with new `message_id`
2. Executor checks `message_termination` cache (prevents duplicate processing)
3. Deserializes planner state, restores executor steps from conversation history
4. Resumes at the same iteration count

## Configuration

| Flag | Purpose |
|------|---------|
| `PlannerRewooParallelExecEnabled` | Enable parallel tool execution |
| `LLMServerAgentReWooMaxParallel` | Max concurrent tool executions (semaphore size) |
| `LLMServerAgentRewooMaxIterations` | Max plan execution iterations |
| `LlmConfigAutoSelectionMaxObservationLen` | Max observation chars before truncation (default 65536) |
| `EnableCritique` | Enable/disable answer critique |
| `QueryConfig.ToolConfigs` | Pre-resolved tool configurations |
| `QueryConfig.ToolConfirmations` | User-approved write actions |

## Error Handling & Recovery

### XML Parsing (7-stage recovery)

1. Standard XML unmarshal
2. Escape bare ampersands (`&` → `&amp;`)
3. Fix mismatched/unclosed tags via regex
4. Regex-based step extraction fallback
5. Extract thought as diagnostic if no steps found
6. Return structured error for retry prompt
7. After 3 failures: use whatever was extracted

### Retry Strategies

| Component | Max Attempts | Recovery |
|-----------|-------------|----------|
| Plan generation | 3 | Escalating feedback with structural error details |
| Plan critique | 1 | Feedback passed to regeneration |
| Mid-execution review | 3 | Refinement counter, skill context enrichment |
| Solver | 3 | Corrective instructions on parse failure |
| Answer critique | 3 | XML parse retries |
| Executor iterations | 2 consecutive failures | Early termination + summarization fallback |

### Summarization Fallback

When all iterations are exhausted without a finish action:

1. Build tool call summary (status per tool: SUCCESS/FAILED/NO_DATA)
2. Construct LLM message history from all tool invocations
3. Send to lite model with "synthesize all conversations" instruction
4. Return synthesis rather than raw error

## Prompt Structure & Caching

See [caching.md](caching.md) for the full message layout. Key principle:

- **System messages** (stable): Base prompt, agent prompt, account prompt → cached at Account scope
- **Human message** (dynamic): Task context, conversation history, notebook, user query → changes every request

This enables LLM provider-level prompt caching on the stable prefix across requests.

## Relationship to ReAct3

ReWoo agents can be upgraded to ReAct3 at runtime via `LlmServerRewooToReact3Enabled`. See [planner_react_3.md](planner_react_3.md) for the hybrid approach. Key differences:

| Aspect | ReWoo | ReAct3 |
|--------|-------|--------|
| Planning | Full plan upfront | Iterative reason-act-observe |
| Replanning | Mid-execution review | Every iteration |
| Parallel execution | Via dependency DAG | Via `<actions>` blocks |
| Solver | Dedicated solver LLM | LLM emits `<final_answer>` directly |
| Critique | Plan critique + Answer critique | Answer critique only |
