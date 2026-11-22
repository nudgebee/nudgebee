// Helpers for deep-linking workflow LLM tasks to their Ask-Nudgebee conversation.
//
// LLM conversation tasks (llm.investigate, llm.summary, llm.nubi) emit a
// `session_id` in their task output. Both the Executions panel and the Dry Run
// result modal use these helpers to surface a "Go to chat" link.

// LLM task types whose execution spawns an Ask-Nudgebee conversation.
export const LLM_CHAT_TASK_TYPES = new Set(['llm.investigate', 'llm.summary', 'llm.nubi']);

type LlmTask = { type?: string; output?: unknown } | null | undefined;

// Output of an LLM chat task as an object. It may arrive as an object or a JSON
// string; returns null when the task is not an LLM chat task or the output is
// not parsable.
const getLlmOutput = (task: LlmTask): Record<string, unknown> | null => {
  if (!task || !task.type || !LLM_CHAT_TASK_TYPES.has(task.type)) return null;
  let out: unknown = task.output;
  if (typeof out === 'string') {
    try {
      out = JSON.parse(out);
    } catch {
      return null;
    }
  }
  return out && typeof out === 'object' ? (out as Record<string, unknown>) : null;
};

// Extract the Ask-Nudgebee session_id from an LLM task's output; returns null
// when the task is not an LLM chat task, or when no session_id is present.
export const getLlmSessionId = (task: LlmTask): string | null => {
  const sid = getLlmOutput(task)?.session_id;
  return typeof sid === 'string' && sid.length > 0 ? sid : null;
};

// Account the task's conversation lives in. A step can target another account
// than the workflow's (its `account_id` param, often a template), and
// Ask-Nudgebee finds a conversation by account + session, so the link has to use
// the account the task reports. Falls back to the workflow's account for tasks
// that do not report one (llm.summary, and runs recorded before tasks did).
export const getLlmAccountId = (task: LlmTask, workflowAccountId: string): string => {
  const accountId = getLlmOutput(task)?.account_id;
  return typeof accountId === 'string' && accountId.length > 0 ? accountId : workflowAccountId;
};

// Build the Ask-Nudgebee deep link for a given conversation session.
export const buildAskNudgebeeHref = (accountId: string, sessionId: string): string =>
  `/ask-nudgebee?accountId=${encodeURIComponent(accountId)}&session_id=${encodeURIComponent(sessionId)}`;
