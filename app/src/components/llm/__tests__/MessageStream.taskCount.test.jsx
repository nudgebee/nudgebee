// MessageStream derives the chip count -> where the fix lands. Deps stubbed.

import { fireEvent, render, screen } from '@testing-library/react';
import MessageStream from '@components/llm/MessageStream';

jest.mock('@components/llm/MessageItem', () => ({
  __esModule: true,
  default: ({ message, responseMeta, siblingTasks }) =>
    responseMeta ? (
      <div data-testid='response-meta'>
        {`taskCount=${responseMeta.taskCount} siblings=${(siblingTasks || []).map((t) => t.id).join(',')}`}
        <button type='button' data-testid='open-tasks' onClick={responseMeta.onOpenTasks}>
          open
        </button>
      </div>
    ) : (
      <div data-testid='row'>{message.type}</div>
    ),
}));

jest.mock('@shared/CustomDrawer', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <div data-testid='drawer'>
      <span data-testid='drawer-title'>{title}</span>
      {children}
    </div>
  ),
  SecondaryDrawer: () => null,
}));
jest.mock('@components/llm/common/TasksDrawerContent', () => ({
  __esModule: true,
  default: ({ tasks }) => <div data-testid='drawer-rows'>{tasks.map((t) => t.id).join(',')}</div>,
}));

jest.mock('@hooks/useMessageAdditionalData', () => ({ __esModule: true, default: () => ({}) }));
jest.mock('@hooks/useTenantBranding', () => ({ useWatchFeatureEnabled: () => false, useTenantBranding: () => ({ assistantName: 'Nubi' }) }));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: {} }));
jest.mock('@components/llm/WatchesTab', () => ({ __esModule: true, default: () => null }));

const noop = () => {};
const baseProps = {
  isProcessing: false,
  collapsedObj: {},
  setCollapsedObj: noop,
  showFullText: {},
  setShowFullText: noop,
  itemProps: { accountId: 'acc-1', conversationId: 'conv-1', getAgentTokenDataForMessage: () => null },
};

const meta = () => screen.getByTestId('response-meta').textContent;
const taskCount = () => meta().split(' ')[0];

describe('MessageStream task count', () => {
  it('counts an acknowledgment + one agent execution as 1 task', () => {
    const messages = [
      { type: 'question', text: 'Review this deployment config' },
      {
        type: 'response',
        id: 'msg-1',
        text: 'Configuration Review',
        drawerTasks: [
          { id: 'msg-1-acknowledgment', type: 'acknowledgment', text: "I understand you're looking for a manual review" },
          { id: 'agent-1', nodeKind: 'agent', type: 'tool_call', tool: 'k8s_orchestrator', response_status: 'success' },
        ],
      },
    ];

    render(<MessageStream messages={messages} {...baseProps} />);
    expect(taskCount()).toBe('taskCount=1');
  });

  it('keeps counting every tool execution under the agent', () => {
    const messages = [
      { type: 'question', text: 'Review this deployment config' },
      {
        type: 'response',
        id: 'msg-1',
        drawerTasks: [
          { id: 'msg-1-acknowledgment', type: 'acknowledgment', text: 'Working on it.' },
          { id: 'agent-1', nodeKind: 'agent', type: 'tool_call', tool: 'k8s_orchestrator' },
          { id: 'tool-1', nodeKind: 'tool', type: 'tool_call', tool: 'kubectl_execute', parentId: 'agent-1' },
        ],
      },
    ];

    render(<MessageStream messages={messages} {...baseProps} />);
    expect(taskCount()).toBe('taskCount=2');
  });

  it('keeps citations aligned with the drawer rows -> both see the whole list', () => {
    const messages = [
      { type: 'question', text: 'q' },
      { type: 'acknowledgment', id: 'ack-1', text: "I understand you're looking for a manual review" },
      { type: 'tool_call', tool: 'k8s_orchestrator', id: 'agent-1' },
      { type: 'tool_call', tool: 'kubectl_execute', id: 'tool-1' },
      { type: 'response', id: 'msg-1' },
    ];

    render(<MessageStream messages={messages} {...baseProps} />);
    // `#task-N` indexes siblings by position -> must match what the drawer lists.
    expect(meta()).toContain('siblings=ack-1,agent-1,tool-1');
  });

  it('lists the acknowledgment in the drawer while leaving it out of the count', () => {
    const messages = [
      { type: 'question', text: 'q' },
      {
        type: 'response',
        id: 'msg-1',
        drawerTasks: [
          { id: 'ack-1', type: 'acknowledgment', text: "I understand you're looking for a manual review" },
          { id: 'agent-1', nodeKind: 'agent', type: 'tool_call', tool: 'k8s_orchestrator' },
        ],
      },
    ];

    render(<MessageStream messages={messages} {...baseProps} />);
    expect(taskCount()).toBe('taskCount=1');
    fireEvent.click(screen.getByTestId('open-tasks'));
    expect(screen.getByTestId('drawer-title').textContent).toBe('Tasks · 1');
    expect(screen.getByTestId('drawer-rows').textContent).toBe('ack-1,agent-1');
  });

  it('falls back to the inline entries for responses predating drawerTasks', () => {
    const messages = [
      { type: 'question', text: 'Review this deployment config' },
      { type: 'acknowledgment', text: "I understand you're looking for a manual review" },
      { type: 'tool_call', tool: 'k8s_orchestrator', id: 'agent-1' },
      { type: 'followup-question', tool: 'followup-question', text: 'Which namespace?' },
      { type: 'response', id: 'msg-1' },
    ];

    render(<MessageStream messages={messages} {...baseProps} />);
    // Only the ack drops out; the follow-up card is still a counted entry.
    expect(taskCount()).toBe('taskCount=2');
  });
});
