// Turn entries that count as a *task*: agent/tool executions, not chat.
// The turn's `ack_message` row -> excluded.

const ACKNOWLEDGMENT_KIND = 'acknowledgment';
const EXECUTION_NODE_KINDS = new Set(['agent', 'tool']); // stamped by buildDrawerTasks

// `planner` counts -> real run, just no details pane.
export const isActionableTask = (entry) => {
  if (!entry) {
    return false;
  }
  if (EXECUTION_NODE_KINDS.has(entry.nodeKind)) {
    return true;
  }
  const kind = entry.tool ?? entry.type; // no nodeKind, no kind -> not evidence of a run
  return Boolean(kind) && kind !== ACKNOWLEDGMENT_KIND;
};

// Backs the "N tasks" chip, drawer title and tab label.
// isArray, not `?? []` -> a mid-render throw would kill the conversation.
export const actionableTasks = (entries) => (Array.isArray(entries) ? entries.filter(isActionableTask) : []);
