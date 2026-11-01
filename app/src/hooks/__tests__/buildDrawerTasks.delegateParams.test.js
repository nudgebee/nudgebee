import { buildDrawerTasks } from '@hooks/useLLMInvestigationControl';

const ROOT = '00000000-0000-0000-0000-000000000000';
const prompt = '{"prompt":"Run PostgreSQL query on k8s-prod","tools":["postgres_query_execute"]}';

const agents = [
  {
    id: 'orch',
    agent_name: 'k8s_orchestrator',
    parent_agent_id: ROOT,
    status: 'success',
    llm_conversation_tool_calls: [
      {
        id: 'tc-delegate',
        tool_name: 'delegate_agent',
        child_agent_id: 'delegate',
        parameters: prompt,
        metadata: '{"nb_executed_target":{"provider":"kubernetes","name":"k8s-prod"}}',
        status: 'success',
      },
    ],
  },
  { id: 'delegate', agent_name: 'delegate_agent', parent_agent_id: 'orch', status: 'success', response: 'done' },
];

describe('drawer row for a delegated sub-agent', () => {
  it("carries the parent's prompt as the step's Query, alongside the call's metadata", () => {
    const row = buildDrawerTasks(agents, {}).find((t) => t.id === 'delegate');
    expect(row.toolParameters).toBe(prompt);
    expect(row.metadata).toContain('k8s-prod');
  });

  it('has no Query for the root agent, which nothing spawned', () => {
    const row = buildDrawerTasks(agents, {}).find((t) => t.id === 'orch');
    expect(row.toolParameters).toBeUndefined();
  });
});
