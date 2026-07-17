// Helpers for deep-linking workflow LLM tasks to their Ask-Nudgebee conversation.
//
// LLM conversation tasks (llm.investigate, llm.summary, llm.nubi) emit a
// `session_id` in their task output. Both the Executions panel and the Dry Run
// result modal use these helpers to surface a "Go to chat" link.

// LLM task types whose execution spawns an Ask-Nudgebee conversation.
export const LLM_CHAT_TASK_TYPES = new Set(['llm.investigate', 'llm.summary', 'llm.nubi']);

// Extract the Ask-Nudgebee session_id from an LLM task's output. Output may
// arrive as an object or a JSON string; returns null when the task is not an
// LLM chat task, or when no session_id is present/parsable.
export const getLlmSessionId = (task: { type?: string; output?: unknown } | null | undefined): string | null => {
  if (!task || !task.type || !LLM_CHAT_TASK_TYPES.has(task.type)) return null;
  let out: unknown = task.output;
  if (typeof out === 'string') {
    try {
      out = JSON.parse(out);
    } catch {
      return null;
    }
  }
  const sid = (out as Record<string, unknown> | null | undefined)?.session_id;
  return typeof sid === 'string' && sid.length > 0 ? sid : null;
};

// Build the Ask-Nudgebee deep link for a given conversation session.
export const buildAskNudgebeeHref = (accountId: string, sessionId: string): string =>
  `/ask-nudgebee?accountId=${encodeURIComponent(accountId)}&session_id=${encodeURIComponent(sessionId)}`;
