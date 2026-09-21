// Regression: ack + one agent run read "2 tasks" -> the ack was counted as a task.

import { actionableTasks, isActionableTask } from '../taskClassification';

// Shapes as produced by buildDrawerTasks.
const ackEntry = {
  id: 'msg-1-acknowledgment',
  tool_id: 'msg-1-acknowledgment',
  parentId: null,
  type: 'acknowledgment',
  text: "I understand you're looking for a manual review of the provided deployment configuration",
};
const agentEntry = { id: 'agent-1', nodeKind: 'agent', type: 'tool_call', tool: 'k8s_orchestrator', response_status: 'success' };
const toolEntry = { id: 'tool-1', nodeKind: 'tool', type: 'tool_call', tool: 'kubectl_execute', parentId: 'agent-1' };

describe('isActionableTask', () => {
  it('rejects nullish entries', () => {
    expect(isActionableTask(undefined)).toBe(false);
    expect(isActionableTask(null)).toBe(false);
  });

  it('rejects the turn acknowledgment', () => {
    expect(isActionableTask(ackEntry)).toBe(false);
  });

  it('accepts follow-up question cards -> only the acknowledgment is excluded', () => {
    expect(isActionableTask({ type: 'followup-question', tool: 'followup-question', text: 'Which cluster?' })).toBe(true);
  });

  it('accepts agent and tool executions', () => {
    expect(isActionableTask(agentEntry)).toBe(true);
    expect(isActionableTask(toolEntry)).toBe(true);
  });

  it('accepts the planner row — a real agent execution, it just has no Tool Details pane', () => {
    expect(isActionableTask({ id: 'planner-1', type: 'tool_call', tool: 'planner' })).toBe(true);
  });

  it('accepts an execution row on nodeKind even if its tool name collides with the ack kind', () => {
    expect(isActionableTask({ id: 'tool-2', nodeKind: 'tool', type: 'tool_call', tool: 'acknowledgment' })).toBe(true);
  });

  it('rejects a malformed entry that identifies neither a tool nor a type', () => {
    expect(isActionableTask({})).toBe(false);
    expect(isActionableTask({ id: 'x', created_at: '2026-08-24T10:00:00Z' })).toBe(false);
  });

  it('accepts legacy inline rows that carry no nodeKind', () => {
    expect(isActionableTask({ id: 'legacy-1', type: 'tool_call', tool: 'logql_query' })).toBe(true);
  });
});

describe('actionableTasks', () => {
  it('counts an acknowledgment + one real task as 1 task, not 2', () => {
    expect(actionableTasks([ackEntry, agentEntry])).toEqual([agentEntry]);
  });

  it('keeps every real execution in order', () => {
    expect(actionableTasks([ackEntry, agentEntry, toolEntry])).toEqual([agentEntry, toolEntry]);
  });

  it('returns an empty list for a turn that only acknowledged', () => {
    expect(actionableTasks([ackEntry])).toEqual([]);
  });

  it('tolerates a missing or non-array list rather than throwing mid-render', () => {
    expect(actionableTasks(undefined)).toEqual([]);
    expect(actionableTasks(null)).toEqual([]);
    expect(actionableTasks({})).toEqual([]);
    expect(actionableTasks('nope')).toEqual([]);
  });
});
