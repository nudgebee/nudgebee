import { buildDrawerTasks } from '../useLLMInvestigationControl';
import { actionableTasks } from '@components/llm/utils/taskClassification';

describe('buildDrawerTasks', () => {
  it('renders spawned agents instead of duplicate agent-wrapper tool rows', () => {
    const metadata = JSON.stringify({
      execution_batch_id: 'batch-1',
      execution_mode: 'parallel_dispatch',
      execution_batch_size: 2,
      planner_iteration: 1,
    });
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [
          {
            id: 'wrapper-1',
            tool_name: 'traces',
            child_agent_id: 'child-agent',
            thought: 'Inspect traces in parallel.',
            metadata,
          },
        ],
      },
      {
        id: 'child-agent',
        parent_agent_id: 'root-agent',
        agent_name: 'traces',
        status: 'success',
        llm_conversation_tool_calls: [],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'wrapper-1')).toBeUndefined();
    expect(tasks.find((task) => task.id === 'child-agent')).toMatchObject({
      parentId: 'root-agent',
      log: 'Inspect traces in parallel.',
      thought: 'Inspect traces in parallel.',
      metadata,
      isPlannerAction: true,
    });
  });

  it('keeps unresolved child wrappers and ordinary tools visible', () => {
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [
          { id: 'unresolved-wrapper', tool_name: 'delegate_agent', child_agent_id: 'missing-agent' },
          { id: 'ordinary-tool', tool_name: 'kubectl_execute' },
        ],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'unresolved-wrapper')).toBeDefined();
    expect(tasks.find((task) => task.id === 'ordinary-tool')).toBeDefined();
  });

  it('merges a uniquely matching legacy agent wrapper without child_agent_id', () => {
    const metadata = '{"planner_iteration":1}';
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [{ id: 'traces-wrapper', tool_name: 'traces', thought: 'Inspect traces.', metadata }],
      },
      {
        id: 'traces-agent',
        parent_agent_id: 'root-agent',
        agent_name: 'traces',
        llm_conversation_tool_calls: [],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'traces-wrapper')).toBeUndefined();
    expect(tasks.find((task) => task.id === 'traces-agent')).toMatchObject({
      parentId: 'root-agent',
      log: 'Inspect traces.',
      metadata,
      isPlannerAction: true,
    });
  });

  it('does not guess when multiple legacy wrappers match one child agent', () => {
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [
          { id: 'traces-wrapper-1', tool_name: 'traces' },
          { id: 'traces-wrapper-2', tool_name: 'traces' },
        ],
      },
      {
        id: 'traces-agent',
        parent_agent_id: 'root-agent',
        agent_name: 'traces',
        llm_conversation_tool_calls: [],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'traces-wrapper-1')).toBeDefined();
    expect(tasks.find((task) => task.id === 'traces-wrapper-2')).toBeDefined();
    expect(tasks.find((task) => task.id === 'traces-agent')).toMatchObject({ isPlannerAction: false });
  });

  it('re-homes a uniquely forwarded nested-agent response under its direct agent', () => {
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [{ id: 'traces-wrapper', tool_name: 'traces' }],
      },
      {
        id: 'traces-agent',
        parent_agent_id: 'root-agent',
        agent_name: 'traces',
        response: 'trace result',
        llm_conversation_tool_calls: [],
      },
      {
        id: 'traces-provider',
        parent_agent_id: 'root-agent',
        agent_name: 'traces_clickhouse',
        response: 'trace result',
        llm_conversation_tool_calls: [{ id: 'provider-tool', tool_name: 'traces_execute' }],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'traces-provider')).toMatchObject({
      parentId: 'traces-agent',
      isPlannerAction: false,
    });
  });

  it('keeps the stored parent when forwarded-response ownership is ambiguous', () => {
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [
          { id: 'wrapper-1', tool_name: 'agent-1' },
          { id: 'wrapper-2', tool_name: 'agent-2' },
        ],
      },
      {
        id: 'agent-1',
        parent_agent_id: 'root-agent',
        agent_name: 'agent-1',
        response: 'same result',
        llm_conversation_tool_calls: [],
      },
      {
        id: 'agent-2',
        parent_agent_id: 'root-agent',
        agent_name: 'agent-2',
        response: 'same result',
        llm_conversation_tool_calls: [],
      },
      {
        id: 'provider-agent',
        parent_agent_id: 'root-agent',
        agent_name: 'provider',
        response: 'same result',
        llm_conversation_tool_calls: [],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'provider-agent')).toMatchObject({ parentId: 'root-agent' });
  });

  it('does not compare large legacy responses when recovering missing parent links', () => {
    const largeResponse = 'x'.repeat(16 * 1024 + 1);
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [{ id: 'traces-wrapper', tool_name: 'traces' }],
      },
      {
        id: 'traces-agent',
        parent_agent_id: 'root-agent',
        agent_name: 'traces',
        response: largeResponse,
        llm_conversation_tool_calls: [],
      },
      {
        id: 'traces-provider',
        parent_agent_id: 'root-agent',
        agent_name: 'traces_clickhouse',
        response: largeResponse,
        llm_conversation_tool_calls: [],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'traces-provider')).toMatchObject({ parentId: 'root-agent' });
  });

  it('records the turn acknowledgment as a row, but not as a task', () => {
    const agents = [{ id: 'root-agent', agent_name: 'k8s_orchestrator', status: 'success', llm_conversation_tool_calls: [] }];
    const message = { id: 'msg-1', ack_message: "I understand you're looking for a manual review of the provided deployment configuration" };

    const tasks = buildDrawerTasks(agents, message);
    // ack stays in the turn record; only the orchestrator run counts as a task
    expect(tasks).toHaveLength(2);
    expect(tasks.find((task) => task.type === 'acknowledgment')).toBeDefined();
    const counted = actionableTasks(tasks);
    expect(counted).toHaveLength(1);
    expect(counted[0]).toMatchObject({ id: 'root-agent', nodeKind: 'agent', tool: 'k8s_orchestrator' });
  });

  it('counts every agent and tool execution under an acknowledged turn', () => {
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        llm_conversation_tool_calls: [{ id: 'tool-1', tool_name: 'kubectl_execute' }],
      },
    ];
    const message = { id: 'msg-2', ack_message: 'Working on it.' };

    expect(actionableTasks(buildDrawerTasks(agents, message)).map((task) => task.id)).toEqual(['root-agent', 'tool-1']);
  });

  it('normalizes non-string agent and tool thoughts', () => {
    const agents = [
      {
        id: 'root-agent',
        agent_name: 'k8s_orchestrator',
        thought: 42,
        llm_conversation_tool_calls: [{ id: 'tool-1', tool_name: 'kubectl_execute', thought: true }],
      },
    ];

    const tasks = buildDrawerTasks(agents, {});
    expect(tasks.find((task) => task.id === 'root-agent')).toMatchObject({ log: '42', thought: '42' });
    expect(tasks.find((task) => task.id === 'tool-1')).toMatchObject({ log: 'true', thought: 'true' });
  });
});
