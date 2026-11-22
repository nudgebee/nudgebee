import { buildMessagePlan, filterDrawerTasksForStep, groupStepsByPhase, groupToolCallsByStep, parsePlanState, summarizePlan } from '../planState';

const planState = JSON.stringify({
  version: 1,
  steps: [
    { id: 's1', phase: 'Scope', title: 'Find pod and window', kind: 'task', status: 'done', finding: 'api-7f' },
    { id: 'h1', phase: 'Verify', title: 'OOMKill', kind: 'hypothesis', status: 'refuted', finding: 'exit 130' },
    { id: 'h2', phase: 'Verify', title: 'Spot preemption', kind: 'hypothesis', status: 'testing', prior: 'high' },
    { id: 'h2a', phase: 'Verify', title: 'check node events', kind: 'task', status: 'pending', parent: 'h2' },
  ],
});

describe('planState', () => {
  it('parses plan_state and ignores missing or broken values', () => {
    expect(parsePlanState(planState)).toHaveLength(4);
    expect(parsePlanState(null)).toBeNull();
    expect(parsePlanState('{not json')).toBeNull();
    expect(parsePlanState(JSON.stringify({ version: 1, steps: [] }))).toBeNull();
  });

  it('groups tool calls by the plan_step in their metadata', () => {
    const { calls } = groupToolCallsByStep([
      {
        llm_conversation_tool_calls: [
          { id: 't1', tool_name: 'kubectl_execute', metadata: '{"plan_step":"h2","planner_iteration":3}' },
          { id: 't2', tool_name: 'kubectl_execute', metadata: { plan_step: 'h2' } },
          { id: 't3', tool_name: 'logs', metadata: '{"planner_iteration":1}' },
          { id: 't4', tool_name: 'logs', metadata: 'broken' },
        ],
      },
    ]);
    expect(Object.keys(calls)).toEqual(['h2']);
    expect(calls.h2.map((c) => c.id)).toEqual(['t1', 't2']);
  });

  it('counts sub-agent work under the step that started it, and filters the drawer to it', () => {
    const agents = [
      {
        id: 'orch',
        llm_conversation_tool_calls: [
          { id: 'd1', tool_name: 'events', child_agent_id: 'ev', metadata: '{"plan_step":"h1"}' },
          { id: 'k1', tool_name: 'shell_execute', metadata: '{"plan_step":"s1"}' },
        ],
      },
      { id: 'ev', llm_conversation_tool_calls: [{ id: 'e1' }, { id: 'e2', child_agent_id: 'nested' }] },
      { id: 'nested', llm_conversation_tool_calls: [{ id: 'n1' }] },
    ];
    const { counts, nodeIds } = groupToolCallsByStep(agents);
    expect(counts).toEqual({ h1: 4, s1: 1 });
    const drawer = [
      { id: 'orch', nodeKind: 'agent', parentId: null },
      { id: 'ev', nodeKind: 'agent', parentId: 'orch' },
      { id: 'e1', nodeKind: 'tool', parentId: 'ev' },
      { id: 'k1', nodeKind: 'tool', parentId: 'orch' },
    ];
    expect(filterDrawerTasksForStep(drawer, nodeIds.h1).map((t) => t.id)).toEqual(['orch', 'ev', 'e1']);
  });

  it('counts a sub-agent call once even if it carries a plan step itself', () => {
    const { calls, counts } = groupToolCallsByStep([
      { id: 'orch', llm_conversation_tool_calls: [{ id: 'd1', tool_name: 'logs', child_agent_id: 'lg', metadata: '{"plan_step":"h1"}' }] },
      { id: 'lg', llm_conversation_tool_calls: [{ id: 'l1', metadata: '{"plan_step":"h1"}' }] },
    ]);
    expect(counts).toEqual({ h1: 2 });
    expect(calls.h1.map((c) => c.id)).toEqual(['d1']);
  });

  it('builds a message plan only when the notebook row carries one', () => {
    const plan = buildMessagePlan([
      {
        id: 'a1',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [{ id: 't1', tool_name: 'kubectl_execute', metadata: '{"plan_step":"h2"}' }],
      },
      { id: 'nb', agent_name: 'notebook', plan_state: planState },
    ]);
    expect(plan.notebookAgentId).toBe('nb');
    expect(plan.toolCallsByStep.h2).toHaveLength(1);
    expect(buildMessagePlan([{ id: 'nb', agent_name: 'notebook', plan_state: null }])).toBeNull();
  });

  it("puts the planner's untagged calls after its first plan update under Outside the plan", () => {
    const plan = buildMessagePlan([
      {
        id: 'orch',
        llm_conversation_tool_calls: [
          { id: 'd0', tool_name: 'shell_execute', created_at: '2026-10-17T05:34:00Z' },
          { id: 'p1', tool_name: 'update_notebook_v2', created_at: '2026-10-17T05:35:00Z' },
          { id: 'k1', tool_name: 'shell_execute', created_at: '2026-10-17T05:36:00Z', metadata: '{"plan_step":"h1"}' },
          { id: 'k2', tool_name: 'logs', child_agent_id: 'lg', created_at: '2026-10-17T05:47:00Z' },
        ],
      },
      { id: 'lg', llm_conversation_tool_calls: [{ id: 'l1' }, { id: 'l2' }] },
      { id: 'nb', agent_name: 'notebook', plan_state: planState },
    ]);
    expect(plan.callCountByStep._outside).toBe(3);
    expect(plan.callCountByStep.h1).toBe(1);
    expect(plan.toolCallsByStep._outside.map((c) => c.id)).toEqual(['k2']);
  });

  it('ignores a plan call with an unreadable timestamp when finding the first plan update', () => {
    const plan = buildMessagePlan([
      {
        id: 'orch',
        llm_conversation_tool_calls: [
          { id: 'p0', tool_name: 'update_notebook_v2' },
          { id: 'p1', tool_name: 'update_notebook_v2', created_at: '2026-10-17T05:35:00Z' },
          { id: 'k2', tool_name: 'logs', created_at: '2026-10-17T05:47:00Z' },
        ],
      },
      { id: 'nb', agent_name: 'notebook', plan_state: planState },
    ]);
    expect(plan.toolCallsByStep._outside.map((c) => c.id)).toEqual(['k2']);
  });

  it('groups steps by phase and nests checks under their hypothesis', () => {
    const phases = groupStepsByPhase(parsePlanState(planState));
    expect(phases.map((p) => p.name)).toEqual(['Scope', 'Verify']);
    expect(phases[1].steps.map((s) => s.id)).toEqual(['h1', 'h2']);
    expect(phases[1].steps[1].children.map((s) => s.id)).toEqual(['h2a']);
  });

  it('summarizes closed and active steps', () => {
    expect(summarizePlan(parsePlanState(planState))).toEqual({ total: 4, closed: 2, activeTitle: 'Spot preemption' });
  });
});
