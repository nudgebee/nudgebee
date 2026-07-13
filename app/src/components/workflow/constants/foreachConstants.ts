// Container / control-flow task types that cannot be used as foreach sub-tasks.
// The v1 foreach editor is one level deep: containers either need graph
// semantics the inline accordion can't represent (group, switch, nested
// foreach, call-workflow) or signal plumbing an iteration doesn't have
// (approval, router, event investigate). core.wait is deliberately allowed —
// sequential waits inside iterations are a legitimate rate-limiting pattern.
export const FOREACH_SUBTASK_BLOCKED_TYPES = new Set([
  'core.foreach',
  'core.group',
  'core.switch',
  'core.call-workflow',
  'core.approval',
  'ai.router',
  'ai.llm_event_investigate',
]);
