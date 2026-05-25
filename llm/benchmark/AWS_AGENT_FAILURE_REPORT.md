# AWS Agent Failure Investigation Report

**Period:** Last 30 days (Feb 9 - Mar 10, 2026)
**Source:** Production DB (`llm_conversations`, `llm_conversation_agent`, `llm_conversation_tool_calls`)
**Scope:** Agents `aws`, `aws_debug`, `aws_observability`

---

## Overview

| Status | Count |
|--------|-------|
| COMPLETED | 180 |
| KILLED | 41 |
| WAITING (stuck) | 31 |
| FAILED | 8 |
| TERMINATED | 5 |

**Total non-success: 85 out of 265 conversations (32%)**

---

## Failure Category Breakdown

### 1. Tool Hallucination: `shell_execute` (14 agent failures, 13 tool calls)

The LLM hallucinates a `shell_execute` tool that doesn't exist.

**Error:** `"auth: tool not found - shell_execute, agent - aws"`

**Trigger queries:**
- "List all AWS resources in the account across all active regions"
- "Add an inbound rule to a security group"
- "Add a user to an IAM group"
- "List all S3 buckets"

**Root cause:** LLM's system prompt doesn't clearly restrict available tools. It invents `shell_execute` when it wants to run raw CLI commands.

---

### 2. Empty Response Failures (13 agent failures)

Agent status is `fail` but response is empty string — no error message at all. These are **silent failures**. Found across KILLED conversations where `aws_debug` sub-agents die with no output. Mostly RCA/event investigation queries.

---

### 3. Planner Deadlock: `max iterations` (9 occurrences, all in week of Feb 16)

`"agent not finished before max iterations"` — all 9 hits concentrated in account `6c008cf8` during a single week. Agent enters infinite planning loops on complex multi-step RCA queries.

**Affected tools:** `aws` (6), `aws_observability` (3), `github` (3)

---

### 4. STS AssumeRole Failures (26 tool call errors, 3 conversations)

**Retry waste is severe:**

| Conversation | STS Retries | Time Wasted |
|---|---|---|
| `1cc7c347` | **13 retries** | **36 minutes** |
| `e9861417` | 5 retries | 4.3 minutes |
| `ccee7e9b` | 3 retries | 2.3 minutes |

**Affected accounts:**
- `19707e32` — 13 errors across 2 conversations
- `a2a30b02` — 13 errors in 1 conversation

The agent keeps retrying STS AssumeRole with identical parameters on a permanent 403 — completely unrecoverable.

---

### 5. Nil Pointer Crashes (5 occurrences, still active)

`"runtime error: invalid memory address or nil pointer dereference"`

| Date | Account | Agent |
|------|---------|-------|
| Mar 2 | `a2a30b02` | `aws` |
| Mar 2 | `a2a30b02` | `aws` |
| Mar 2 | `a2a30b02` | `aws` |
| Feb 23 | `19707e32` | `aws_observability` |
| Feb 23 | `19707e32` | `aws` |

**Still happening as recently as March 2.** Affects both `aws` and `aws_observability` agents.

---

### 6. Internal Error Cascade (15 tool call errors, concentrated Feb 16 week)

Generic `"unable to process your request due to an internal error"` returned by multiple tools.

| Tool | Count |
|------|-------|
| `aws_observability` | 10 |
| `aws` | 2 |
| `events` | 2 |
| `knowledge_base` | 1 |

All from account `6c008cf8` during RCA investigations. The sub-agent tools fail silently — likely cloud-collector or downstream service outage.

---

### 7. WAITING/Stuck Conversations (31 stuck)

Conversations stuck in WAITING status with **no cleanup**.

**Patterns:**
- **7 have `last_tool=null`** — agent never dispatched a tool call. Stuck at initialization (likely cloud-collector connectivity or tool config missing).
- **4 have `last_tool_status=success`** — tool call succeeded but agent never returned. Stuck in the planner after receiving tool output.

**Account distribution:**
- `6c008cf8` — ~15 stuck conversations
- `ff87fbfd` — 4 stuck conversations

---

## Tool Error Rates

| Tool | Total Calls | Errors | Error % |
|------|-------------|--------|---------|
| `aws_execute` | 878 | 32 | 3.6% |
| `aws_observability` | 126 | 29 | **23.0%** |
| `aws` (sub-agent) | 131 | 26 | **19.8%** |
| `github` | 7 | 3 | 42.9% |
| `events` | 2 | 2 | 100% |

`aws_execute` is healthy (3.6%). The sub-agent tools (`aws_observability`, `aws`) have ~20% error rates — these are the tools called during RCA flows.

---

## Account Failure Rates

| Account | Total Agents | Failures | Fail % |
|---------|-------------|----------|--------|
| `6c008cf8` | 276 | 52 | 18.8% |
| `a2a30b02` | 90 | 11 | 12.2% |
| `49145907` | 29 | 6 | 20.7% |
| `19707e32` | 9 | 2 | 22.2% |
| `ff87fbfd` | 4 | 0 | 0.0% |

---

## Weekly Trend

| Week | Total | Failures | Nil Crashes | Tool Hallucinations | Deadlocks |
|------|-------|----------|-------------|---------------------|-----------|
| Mar 9 | 18 | 1 | 0 | 0 | 0 |
| Mar 2 | 36 | 6 | **3** | 2 | 0 |
| Feb 23 | 86 | 15 | 2 | 9 | 0 |
| Feb 16 | 198 | **46** | 0 | 12 | **9** |
| Feb 9 | 70 | 3 | 0 | 3 | 0 |

Feb 16 week was the worst — 46 failures (23%), with all 9 planner deadlocks concentrated there. Failures have trended down since, but nil pointer crashes are **new** (appearing only from Feb 23 onward).

---

## Priority Issues (llm-server / Go)

| # | Issue | Impact | Frequency | Severity |
|---|-------|--------|-----------|----------|
| **B1** | STS AssumeRole retry waste — agent retries permanent 403 up to 13 times, wasting 36 min | 26 tool errors, 3 convos | Active | **P0** |
| **B2** | Nil pointer crashes — `invalid memory address` in agent execution | 5 crashes | Active (Mar 2) | **P0** |
| **B3** | WAITING conversations never cleaned up — 31 stuck with no timeout | 31 stuck convos | Active | **P1** |
| **B4** | Tool hallucination (`shell_execute`) — LLM invents non-existent tools | 14 agent failures | Active | **P1** |
| **B5** | Planner deadlock (`max iterations`) — agent loops forever on complex queries | 9 occurrences | Burst (Feb 16) | **P1** |
| **B6** | Silent failures — agent fails with empty response, no error details | 13 agent failures | Active | **P2** |
| **B7** | `aws_observability` sub-agent 23% error rate — "internal error" cascade | 29 tool errors | Active | **P2** |

---

## Recommended Fixes

### P0 — Immediate

- **B1 (STS retry waste):** Add error classification in `cloud-collector` — detect `STS: AssumeRole 403` and return a non-retryable error code. Agent should abort after first STS 403 instead of retrying 13 times.
- **B2 (Nil pointer):** Add nil checks in Go agent execution path. Likely null pointer on tool response parsing or agent state access. Need stack trace from logs to pinpoint exact location.

### P1 — This Sprint

- **B3 (Stuck WAITING):** Add conversation timeout in llm-server — if a conversation is in WAITING for >30 minutes, mark it as TERMINATED and release resources.
- **B4 (Tool hallucination):** Update agent system prompt to explicitly list only available tools and add instruction: "Do NOT invent or use tools not listed above."
- **B5 (Planner deadlock):** Reduce max iterations from current value and add cycle detection — if the agent generates the same tool call twice in a row, force termination.

### P2 — Next Sprint

- **B6 (Silent failures):** Ensure all agent failure paths write a non-empty error message to the `response` field. Add a catch-all error handler that captures the failure reason.
- **B7 (Internal errors):** Investigate why `aws_observability` returns generic "internal error" for 23% of calls. Likely a downstream dependency failure (cloud-collector) that needs better error propagation.
