// Container / control-flow task types that cannot be used as inline sub-tasks
// inside a container node's accordion editor (core.foreach, core.group). The v1
// sub-task editor is one level deep: containers either need graph semantics the
// inline accordion can't represent (group, switch, nested foreach, call-workflow)
// or signal plumbing an inline body doesn't have (approval, router, event
// investigate). core.wait is deliberately allowed — sequential waits are a
// legitimate pattern inside both loops and groups.
export const SUBTASK_BLOCKED_TYPES = new Set([
  'core.foreach',
  'core.group',
  'core.switch',
  'core.call-workflow',
  'core.approval',
  'ai.router',
  'ai.llm_event_investigate',
]);
