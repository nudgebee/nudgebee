# Harness² Research — Analysis & NB Applicability

**Date:** 2026-10-25
**Source:** [google-research/harness2](https://github.com/google-research/harness2) · [arXiv:2610.14224](https://arxiv.org/pdf/2610.14224)
**Authors:** Google Cloud AI Research + Northwestern University (Oct 2026, Apache 2.0)

---

## What Harness² Is

A framework for **recursive self-improvement of the agent scaffold** (everything around the model) while keeping model weights frozen. The core insight: don't fine-tune the model — optimize the system prompt, skills, tools guidance, memory, scripts, and plugins that surround it, using the model itself as the optimizer.

---

## How It Works

### The Eight Editable Components

Harness² decomposes the agent scaffold into eight component types, each a file on disk:

| # | Component | Runtime | NB Equivalent |
|---|-----------|---------|---------------|
| c1 | `systemprompt.md` | Always-on | Agent's `GetSystemPrompt()` |
| c2 | `guardrails.md` | Always-on | Behavioral rules in planner base prompt |
| c3 | `LongTermMEMORY.md` | Always-on | `AccountContext` / conversation memory |
| c4 | `tool_guidance.md` | Always-on | Tool descriptions in `GetSupportedTools()` |
| c5 | `sub_agents/<name>/AGENT.md` | On-demand | `DelegateAgentTool` / sub-agent prompts |
| c6 | `skills/<name>/SKILL.md` | On-demand | Skills via `SelectedSkillIds` / KB mappings |
| c7 | `scripts/<name>.py` | On-demand via bash | Deterministic tool implementations |
| c8 | `plugin/<name>.ts` | Always registered | No direct equivalent (tool-loop middleware) |

Key design principle: **prefer on-demand components over always-on**. Always-on text competes with the model's output budget. Skills (on-demand, free until invoked) are preferred over scripts, subagents, plugins, and always-on text in that order.

### The Propose-Probe-Compose Loop

Three-phase optimization per task:

**Phase 1 — Propose.** Two editor agents run in parallel, each scoped to a dimension:
- **INV** (invariant): procedure, tool use, completion mechanics → owns scripts, subagents, plugins, tool guidance
- **DOM** (domain): practitioner knowledge, domain rules, artifact contracts → owns skills, memory, domain-specific rules

Each editor gets a full workspace on disk: task description, base execution trajectories (untruncated), produced deliverables, current harness files, and the experience library from prior tasks. The editor reads this evidence and edits harness files in place — the file edits ARE the proposal. Every edit must trace to a specific trajectory moment. No evidence = `KEEP_BASE` (keeping the base unchanged is a valid outcome).

**Phase 2 — Probe.** Each proposed harness runs the full task. No comparison happens here — just execution to produce traces and deliverables as evidence for the next phase.

**Phase 3 — Compose.** A composer agent receives ALL evidence: base rollouts, all candidate harnesses with their rollouts, deliverables, and unified diffs. It judges each edit against the evidence ("did this help, hurt, or do nothing?") and produces a single merged harness. Valid outcomes: merge across candidates, adopt one wholesale, or keep base. An edit whose rollout produced nothing is treated as UNSAFE. This is an LLM doing holistic merge, not automated diff-merge.

**Selection.** After composition, a blind judge picks the final output. Candidates are anonymized (shuffled IDs A, B, C...). The judge sees only task + source material + produced artifacts — never trajectories or harnesses. Evaluates: coverage, correctness, fidelity, internal consistency, finish quality. Picking the unmodified base is explicitly valid.

### Cost Formula

Per task at budget k:
- **4k executor rollouts** (k base + 2k candidate + k composition)
- **3k improver sessions** (k INV + k DOM + k compose)
- **+1 selector + 1 reflector**

At k=1: 9 LLM sessions. At k=3: 23 sessions. Overhead is **4-12x** the base single-execution cost.

### Cross-Task Transfer (Experience Library)

After selection, a reflector agent analyzes what worked:
1. Diffs every candidate harness against base
2. Writes per-component verdicts: `helped` / `hurt` / `no observed effect` / `confounded`
3. Extracts at most one transferable lesson with: `cue` (situation pattern), `lesson` (what to do, naming the component), `evidence` (the edit rows), `confidence` (high/medium/low)
4. Only stores lessons where all three of cue/lesson/evidence are present

Library is per (run, domain). Retrieved by construction — the entire domain directory is copied into each proposer's workspace. No embedding search; proposers browse `INDEX.md` and read relevant entries.

### Failure Modes

1. **Output budget exhaustion** — the most common failure; always-on prose makes it worse
2. **Confounded edits** — multiple changes in one proposal prevent attribution
3. **Shared model bias** — same model generates and judges (no independent verifier)
4. **Unbounded library growth** — no eviction strategy acknowledged
5. **Rubric leak** — executors with network access could retrieve benchmark grading criteria

---

## What NB Can Learn

### Directly Applicable Ideas

#### 1. Component-Scoped Self-Improvement (the big one)

**Harness² insight:** Decompose the scaffold into independently editable components and optimize each.

**NB already has the decomposition.** The eight components map almost perfectly to NB's existing architecture:

| Harness² | NB's Existing Mechanism |
|----------|------------------------|
| System prompt | `GetSystemPrompt()` per agent |
| Guardrails | Planner base prompt rules |
| Memory | `llm_conversation_memory` + AccountContext |
| Tool guidance | Tool `Description()` strings |
| Subagents | `DelegateAgentTool` with custom prompts |
| Skills | KB mappings via `SelectedSkillIds` |
| Scripts | Deterministic tool implementations |
| Plugins | No equivalent (but closest: middleware in `doAction` pipeline) |

**What's missing:** NB doesn't have a feedback loop that improves these components based on execution outcomes. The prompts, tool descriptions, and delegation strategies are static (human-authored, A/B tested via `prompts/` package). Harness² shows you can use the LLM itself to propose improvements to these components by showing it execution traces of what went wrong.

**Practical entry point:** The `prompts/` package already supports account-specific overrides and DB-backed config. A lightweight version of Propose-Probe-Compose could:
1. After a conversation where the agent struggled (high iteration count, critique rejection, budget exhaustion), extract the trajectory
2. Run a proposer pass that suggests prompt/skill edits scoped to that agent
3. Store proposed edits as account-level prompt overrides (the existing mechanism)
4. A/B test them against the baseline on subsequent similar queries

This is NOT full Harness² (no 4x cost multiplier) — it's using the reflection pattern on naturally-occurring failures rather than probing every task.

#### 2. Experience Library → Structured Lesson Extraction

**Harness² insight:** After each task, extract a transferable lesson with `cue` / `lesson` / `evidence` / `confidence` fields. Future tasks consult the library.

**NB already does memory extraction** (background worker after execution), but it's unstructured — patterns/facts injected into knowledge base. Harness²'s format is more actionable:

```
cue: "When investigating OOMKilled pods, the agent repeatedly checks pod status instead of checking resource limits first"
lesson: "Add to k8s_orchestrator skills: a skill that directs resource-limit and LimitRange checks before pod status when the symptom is OOMKilled"
evidence: "Conversation X: 4 redundant kubectl get pods before kubectl describe showed the limit"
confidence: high
```

This structured format lets you:
- Filter by confidence before injection
- Match cues to incoming queries (keyword or embedding)
- Track which lessons actually improved outcomes
- Evict lessons that never fire or whose confidence degrades

#### 3. INV/DOM Dimension Scoping

**Harness² insight:** Separate "how to use tools" (INV) from "domain knowledge" (DOM) improvements. Don't let one editor touch both.

**NB application:** When reflecting on a failed investigation, separate:
- **INV issues** (procedure): wrong tool order, missed parallel opportunity, unnecessary iterations, tool input formatting errors
- **DOM issues** (domain): wrong kubectl flags for the symptom, missed AWS region scoping, incorrect metric name

This scoping prevents a single reflection pass from producing muddled improvements that conflate "use kubectl describe before get pods" (procedure) with "check CoreDNS when seeing DNS resolution failures" (domain knowledge).

#### 4. "KEEP_BASE Is Valid" Philosophy

**Harness² insight:** Explicitly make "no change" a first-class outcome at every phase. The proposer can output KEEP_BASE. The selector can pick the unmodified harness. An edit without trajectory evidence is discarded.

**NB application:** Any self-improvement loop must resist the temptation to always produce changes. The critique system already has this partially — it can `accept` without refinement. But if NB adds prompt optimization, the optimizer must be equally comfortable saying "the current prompt is fine for this failure class."

### Ideas That Don't Apply

#### Full Propose-Probe-Compose Per Task
The 4-12x cost overhead makes no sense for NB's real-time investigation use case. Users need answers in seconds, not after 9-23 parallel agent sessions. Harness² is a batch optimization framework — it improves an agent offline, not in the hot path.

#### Blind Selection Judge
NB's critique system already evaluates answer quality inline. A separate blind judge adds latency and cost without clear benefit when you already have the critiquer.

#### File-on-Disk Harness Editing
Harness² has agents edit markdown files via coding tools. NB's prompts are Go-embedded YAML loaded via `prompts/loader.go` with DB overrides. The editing mechanism doesn't transfer — but the concept of "let the LLM propose prompt edits based on execution evidence" does.

---

## Concrete Recommendations for NB

### Tier 1 — Low-Cost, High-Value (weeks, not months)

**A. Structured Lesson Extraction from Failed Investigations**

After conversations where `budget_exhausted: true` or critique rejects ≥2 times or iteration count hits the cap:
1. Extract the scratchpad + final answer
2. Run a reflection pass (similar to existing memory extraction, but with the Harness² structured format: cue/lesson/evidence/confidence)
3. Store in `llm_conversation_memory` with the structured fields
4. On future queries, match cues against the incoming query and inject relevant lessons into `AccountContext`

This reuses the existing memory extraction pipeline but makes it structured and retrievable.

**B. Tool Guidance Auto-Improvement**

Track which tools produce the most `failed` observations per agent. When a tool's failure rate crosses a threshold:
1. Extract the failure patterns (wrong inputs, auth issues, timeout patterns)
2. Run a proposer pass that suggests an improved tool `Description()` or adds input validation hints
3. Store as an account-level tool guidance override
4. Monitor whether failure rate drops

This is the narrowest possible version of Harness² — improving tool descriptions based on observed failures.

### Tier 2 — Medium Investment (1-2 months)

**C. Agent-Scoped Prompt Optimization**

For each orchestrator (k8s, aws, gcp, azure, datadog):
1. Collect a corpus of conversations with their critique outcomes (accept/refine) and iteration counts
2. Periodically (weekly batch job), run an INV/DOM-scoped reflection:
   - INV: "Given these trajectories, what procedural improvements would reduce iteration count?"
   - DOM: "Given these trajectories, what domain knowledge additions would prevent the wrong-tool-first pattern?"
3. Produce candidate prompt edits (using the existing `prompts/` override mechanism)
4. A/B test via the experiment config system already in `prompts/loader.go`

**D. Delegation Strategy Learning**

When `DelegateAgentTool` returns with `budget_exhausted: true`:
1. Analyze: was the budget too low, the tool subset too narrow, or the prompt too vague?
2. Extract a lesson about delegation strategy for that agent+task-type combination
3. Feed into future delegation decisions (e.g., auto-increase budget for known-complex subtask patterns)

### Tier 3 — Full Implementation (quarter+)

**E. Offline Harness Optimization Loop**

A batch system that runs the full Propose-Probe-Compose on a curated eval set:
1. Maintain a benchmark of representative queries per orchestrator
2. Weekly: run each query with current prompts, collect trajectories
3. Run Harness²-style optimization (k=1 for cost sanity: 9 sessions per task)
4. Validate improvements on a holdout set
5. Graduate to production via the prompt experiment system

This is the full Harness² loop adapted to NB, but running offline against an eval corpus rather than in the hot path.

---

## Key Takeaway

Harness² proves that **LLMs can improve their own scaffolding** given execution traces as evidence. NB already has all the infrastructure pieces (structured prompts, DB overrides, A/B testing, memory extraction, critique). The gap is the **feedback loop**: currently, prompt improvements are human-authored. The path from here is incremental:

1. Start with structured lesson extraction from failures (Tier 1A) — almost free, reuses existing pipeline
2. Add tool guidance auto-improvement (Tier 1B) — narrow scope, measurable
3. Scale to agent-scoped prompt optimization (Tier 2C) — uses existing A/B infrastructure
4. Eventually, offline harness optimization on eval corpus (Tier 3E) — the full loop
