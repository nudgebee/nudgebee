import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import LLMAnswerRenderer from '@components/llm/common/LLMAnswerRenderer';
import { findCitedTask, findLegacyCitedTask, parseCitationHref } from '@components/llm/utils/citationLinks';
import { buildDrawerTasks } from '@hooks/useLLMInvestigationControl';

// The markdown viewer is replaced by one button per link so a test can click a citation.
jest.mock('@shared/viewers/MarkDowns', () => {
  return function MockMarkDowns({ data, onLinkClick }) {
    const links = [...String(data).matchAll(/\[([^\]]*)\]\(([^)]*)\)/g)];
    return (
      <div>
        {links.map(([, label, href]) => (
          <button key={href + label} type='button' onClick={() => onLinkClick?.(href, label)}>
            {label}
          </button>
        ))}
      </div>
    );
  };
});

const ROW = '91797f8b-71d7-4b64-9105-1de67bdee886';

describe('parseCitationHref', () => {
  it('reads a link to a stored step', () => {
    expect(parseCitationHref(`#task-${ROW}`)).toEqual({ rowId: ROW });
    expect(parseCitationHref(`#task-${ROW.toUpperCase()}`)).toEqual({ rowId: ROW });
  });

  it('marks an old step-number citation as legacy', () => {
    expect(parseCitationHref('#task-E3')).toEqual({ legacy: true, step: 3 });
  });

  it('ignores every other link', () => {
    expect(parseCitationHref('https://example.com/pr/1')).toBeNull();
    expect(parseCitationHref('#section-2')).toBeNull();
    expect(parseCitationHref('#task-runner')).toBeNull();
    expect(parseCitationHref(undefined)).toBeNull();
  });
});

describe('findCitedTask', () => {
  // An orchestrator that ran one command itself, started a logs sub-agent, and whose
  // inventory lookup fanned out into a sub-step.
  const agents = [
    {
      id: 'agent-root',
      agent_name: 'k8s_orchestrator',
      llm_conversation_tool_calls: [
        { id: 'row-kubectl', tool_id: 'shell-1', tool_name: 'shell_execute' },
        { id: 'row-spawn-logs', tool_id: 'logs-1', tool_name: 'logs', child_agent_id: 'agent-logs' },
        { id: 'row-lookup', tool_id: 'lookup-1', tool_name: 'resource_search_execute' },
        { id: 'row-substep', tool_id: 'lookup-1#0', tool_name: 'cluster_command', metadata: { parent_tool_call_id: 'lookup-1' } },
      ],
    },
    {
      id: 'agent-logs',
      agent_name: 'logs',
      parent_agent_id: 'agent-root',
      llm_conversation_tool_calls: [{ id: 'row-loki', tool_id: 'loki-1', tool_name: 'fetch_logs_direct' }],
    },
  ];
  const tasks = buildDrawerTasks(agents, { id: 'msg-1' });

  it('opens the step itself', () => {
    expect(findCitedTask('row-kubectl', tasks)?.id).toBe('row-kubectl');
    expect(findCitedTask('row-loki', tasks)?.id).toBe('row-loki');
  });

  it('opens the sub-agent a cited call started', () => {
    expect(findCitedTask('row-spawn-logs', tasks)?.id).toBe('agent-logs');
  });

  it('opens the call a cited sub-step belongs to', () => {
    expect(findCitedTask('row-substep', tasks)?.id).toBe('row-lookup');
  });

  it('finds nothing for a step that is not in the answer', () => {
    expect(findCitedTask('row-elsewhere', tasks)).toBeNull();
    expect(findCitedTask('row-kubectl', undefined)).toBeNull();
  });
});

describe('findLegacyCitedTask', () => {
  // The planner numbered the root agent's steps E1, E2, … in the order they ran. The critique's
  // review row and a sub-step are not numbered steps.
  const agents = [
    {
      id: 'agent-root',
      agent_name: 'k8s_orchestrator',
      llm_conversation_tool_calls: [
        { id: 'row-1', tool_id: 'shell-a', tool_name: 'shell_execute', created_at: '2026-10-17T01:00:01' },
        { id: 'row-review', tool_id: 'critique', tool_name: 'react_critique', created_at: '2026-10-17T01:00:02' },
        { id: 'row-2', tool_id: 'logs-b', tool_name: 'logs', child_agent_id: 'agent-logs', created_at: '2026-10-17T01:00:03' },
        {
          id: 'row-sub',
          tool_id: 'shell-a#0',
          tool_name: 'cluster_command',
          metadata: { parent_tool_call_id: 'shell-a' },
          created_at: '2026-10-17T01:00:04',
        },
        { id: 'row-4', tool_id: 'shell-d-E4-1', tool_name: 'shell_execute', created_at: '2026-10-17T01:00:05' },
        { id: 'row-3', tool_id: 'shell-c-E3-0', tool_name: 'shell_execute', created_at: '2026-10-17T01:00:05' },
      ],
    },
    { id: 'agent-logs', agent_name: 'logs', parent_agent_id: 'agent-root', llm_conversation_tool_calls: [] },
  ];
  const tasks = buildDrawerTasks(agents, { id: 'msg-1' });

  it('counts the root agent steps in the order they ran', () => {
    expect(findLegacyCitedTask(1, tasks)?.id).toBe('row-1');
    expect(findLegacyCitedTask(2, tasks)?.id).toBe('agent-logs');
  });

  it('prefers the step whose stored id carries the number', () => {
    // A parallel batch stored E3 and E4 with the same timestamp; order alone could swap them.
    expect(findLegacyCitedTask(3, tasks)?.id).toBe('row-3');
    expect(findLegacyCitedTask(4, tasks)?.id).toBe('row-4');
  });

  it('finds nothing past the last step', () => {
    expect(findLegacyCitedTask(9, tasks)).toBeNull();
  });
});

describe('LLMAnswerRenderer citations', () => {
  const messages = [{ tool: 'shell_execute', tool_id: ROW, originalIndex: 4 }];

  it('passes a stored-step citation to the stream with the task it matched', () => {
    const onNavigateToTask = jest.fn();
    const toolCall = { text: `Pod is running [Shell Execute - E1](#task-${ROW}).` };
    render(<LLMAnswerRenderer toolCall={toolCall} messages={messages} onNavigateToTask={onNavigateToTask} groupIndex={2} />);
    fireEvent.click(screen.getByText('Shell Execute - E1'));
    expect(onNavigateToTask).toHaveBeenCalledWith(2, 4, { rowId: ROW });
  });

  it('does not guess the step behind an old step-number citation', () => {
    const onNavigateToTask = jest.fn();
    const toolCall = { text: 'Pod is running [Shell Execute - E1](#task-E1).' };
    render(<LLMAnswerRenderer toolCall={toolCall} messages={messages} onNavigateToTask={onNavigateToTask} groupIndex={2} />);
    fireEvent.click(screen.getByText('Shell Execute - E1'));
    expect(onNavigateToTask).toHaveBeenCalledWith(2, null, { legacy: true, step: 1 });
  });
});
