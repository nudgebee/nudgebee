// A Slack reply links a citation to the chat page with the cited step in the URL hash;
// the step opens once the answer that holds it has loaded. Deps stubbed.

import { render, screen } from '@testing-library/react';
import MessageStream from '@components/llm/MessageStream';

jest.mock('@components/llm/MessageItem', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/CustomDrawer', () => ({
  __esModule: true,
  default: ({ children, open }) => (open ? <div data-testid='drawer'>{children}</div> : null),
  SecondaryDrawer: () => null,
}));
jest.mock('@components/llm/common/TasksDrawerContent', () => ({
  __esModule: true,
  default: ({ activeTaskKey }) => <div data-testid='active-step'>{String(activeTaskKey)}</div>,
}));
jest.mock('@hooks/useMessageAdditionalData', () => ({ __esModule: true, default: () => ({}) }));
jest.mock('@hooks/useTenantBranding', () => ({ useWatchFeatureEnabled: () => false, useTenantBranding: () => ({ assistantName: 'Nubi' }) }));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: {} }));
jest.mock('@components/llm/WatchesTab', () => ({ __esModule: true, default: () => null }));

const STEP = '8341b390-60ad-40b8-b9c7-e2b90d2cba70';
const noop = () => {};
const baseProps = {
  isProcessing: false,
  collapsedObj: {},
  setCollapsedObj: noop,
  showFullText: {},
  setShowFullText: noop,
  itemProps: { accountId: 'acc-1', conversationId: 'conv-1', getAgentTokenDataForMessage: () => null },
};
const answer = [
  { type: 'question', text: 'is it healthy?' },
  {
    type: 'response',
    id: 'msg-1',
    drawerTasks: [
      { id: 'agent-1', nodeKind: 'agent', type: 'tool_call', tool: 'k8s_orchestrator' },
      { id: STEP, nodeKind: 'tool', type: 'tool_call', tool: 'metrics', parentId: 'agent-1' },
    ],
  },
];

describe('MessageStream step from the URL hash', () => {
  afterEach(() => window.history.replaceState(null, '', '/'));

  it('opens the cited step once the answer has loaded', () => {
    window.history.replaceState(null, '', `/ask-nudgebee#task-${STEP}`);
    const { rerender } = render(<MessageStream messages={[]} {...baseProps} />);
    expect(screen.queryByTestId('drawer')).not.toBeInTheDocument();

    rerender(<MessageStream messages={answer} {...baseProps} />);
    expect(screen.getByTestId('active-step').textContent).toBe(STEP);
  });

  it('opens nothing when the hash names a step this conversation does not have', () => {
    window.history.replaceState(null, '', '/ask-nudgebee#task-00000000-0000-0000-0000-000000000000');
    render(<MessageStream messages={answer} {...baseProps} />);
    expect(screen.queryByTestId('drawer')).not.toBeInTheDocument();
  });

  it('opens nothing without a step in the hash', () => {
    render(<MessageStream messages={answer} {...baseProps} />);
    expect(screen.queryByTestId('drawer')).not.toBeInTheDocument();
  });
});
