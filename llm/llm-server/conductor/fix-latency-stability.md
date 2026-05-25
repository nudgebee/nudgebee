# Fix for Critical System Latency and LLM Stability Issues

## Objective
Address the critical 15-30 minute latencies and frequent model instabilities identified in the logs (`conversation.log` and `conversation_5w.log`).

## Background & Root Causes
1.  **Unbounded Tool Outputs**: Tools like `aws_execute` can return massive datasets (e.g., CloudFormation event logs) that saturate the context window, causing the LLM to take 5+ minutes to process or hit token limits.
2.  **Inefficient Continuation Loop**: When a response is truncated, the continuation loop appends the massive context and retries, leading to exponential slowdowns.
3.  **Model Instability**: `gemini-3-flash-preview` frequently returns empty content, triggering retries that often encounter the same high-latency issues.
4.  **Timeout Leaks**: Specific sub-calls like `CountTokens` appear to ignore the dynamic `callTimeout`, leading to 15-minute stalls.
5.  **Planner Overhead**: Significant gaps (6+ minutes) between planner steps suggest inefficiencies in state management or history processing.

## Proposed Solution

### 1. Tool Output "Firewall" (Smart Truncation)
*   **Immediate Change**: Reduce `applyPreflightMessageSizeCap` from 1.5MB to **500KB**.
*   **Feature**: Implement `SmartTruncateToolOutput` in `agents/core/executor_planner.go`. If a tool output exceeds 50KB:
    *   Keep the first 25KB and last 25KB.
    *   Insert a message: `[TRUNCATED ... X bytes removed ... please use more specific filters if you need more data]`.
    *   This prevents "garbage" data from bogging down the LLM.

### 2. LLM Continuation & Budget Hardening
*   **Fix Timeout Logic**: Ensure `tryWithModel` passes a strictly capped context to `ApplyCache` and `CountTokens`. If the global budget has 9 minutes left, the `CountTokens` call must NOT take 15 minutes.
*   **Budget Alignment**: Set `LlmServerMaxIndividualCallTimeoutMinutes` to **5 minutes** by default.
*   **Continuation Limit**: Cap the total number of chunks in a continuation loop to **3** (currently appears higher or unbounded).

### 3. Error Strategy Refinement
*   **Slow Model Fallback**: If a primary model call takes > 3 minutes AND fails (timeout or empty), the system should skip the remaining retries for that model and immediately use a **Fallback Model** (e.g., `gemini-1.5-pro` or a stable Flash version).
*   **Empty Response Jitter**: Increase the backoff for "Empty Response" retries slightly to allow the backend to stabilize.

### 4. Planner Performance & Observability
*   **Overhead Profiling**: Add granular timing logs for `rewooagent2` phases (Initialization, Execution, Critique, Solver).
*   **History Optimization**: Optimize `messageFormatterToString`. If history is massive, only include the last N turns or a summary of older turns.

## Implementation Plan

### Phase 1: Tool & Timeout Safeguards (Immediate)
1.  Modify `config/config.go` to update default timeouts.
2.  Implement `applyPreflightMessageSizeCap` reduction and `SmartTruncateToolOutput` in `agents/core/llm_message_summarization.go`.
3.  Fix the context deadline propagation in `agents/core/llm_common.go`.

### Phase 2: Continuation & Model Switching (Short-term)
1.  Update `generateLLMContentWithRetry` to include "Slow Model Detection".
2.  Refine Strategy 3a (Empty Response) to be less aggressive when latency is high.

### Phase 3: Planner Optimizations
1.  Add heartbeat logs to `agents/core/planner_rewoo_2.go`.
2.  Implement history windowing in `agents/core/executor.go` to prevent cumulative slowdowns in long conversations.

## Verification
1.  **Unit Tests**: Add tests for `SmartTruncateToolOutput` ensuring it preserves JSON structure or essential markers.
2.  **Latency Benchmarks**: Run `aws_execute` with a simulated large output and verify the system completes in < 2 minutes.
3.  **Timeout Test**: Force a `CountTokens` delay and verify the context deadline is respected.
