// The approvals and clarifications pills on a finished turn each open the drawer on
// their own kind, not one shared list (#41642). Deps stubbed.

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import MessageStream from '@components/llm/MessageStream';

jest.mock('@components/llm/MessageItem', () => ({
  __esModule: true,
  default: ({ responseMeta }) =>
    responseMeta?.onOpenFollowups ? (
      <>
        <button onClick={() => responseMeta.onOpenFollowups('approvals')}>approvals</button>
        <button onClick={() => responseMeta.onOpenFollowups('clarifications')}>clarifications</button>
      </>
    ) : null,
}));
jest.mock('@shared/CustomDrawer', () => ({
  __esModule: true,
  default: ({ children, open, title }) =>
    open ? (
      <div data-testid='drawer'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null,
  SecondaryDrawer: () => null,
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

const followup = (config, text) => ({
  type: 'followup-question',
  tool: 'followup-question',
  text: config.question,
  asked_at: '2026-10-19T10:34:00Z',
  last_updated_at: '2026-10-19T10:35:00Z',
  response: { type: 'followup-response', text, status: 'COMPLETED', message_config: JSON.stringify(config) },
});

const turn = [
  { type: 'question', text: 'fix the failing build' },
  followup({ followupType: 'clarification', question: 'Which branch should I use?' }, 'main'),
  followup(
    {
      followupType: 'tool_confirmation',
      toolName: 'shell_execute',
      followupOptions: ['yes', 'no'],
      question: 'Tool(shell_execute) is trying to run a command. Do you want to continue?\nCommand - {"command":"git push origin fix"}',
    },
    'yes'
  ),
  { type: 'response', id: 'msg-1' },
];

describe('MessageStream approvals/clarifications drawer', () => {
  it('opens only the approvals from the approvals pill', async () => {
    render(<MessageStream messages={turn} {...baseProps} />);
    await userEvent.setup().click(screen.getByText('approvals'));
    expect(screen.getByRole('heading')).toHaveTextContent('Approvals · 1');
    expect(screen.getByText('git push origin fix')).toBeInTheDocument();
    expect(screen.queryByText('Which branch should I use?')).not.toBeInTheDocument();
  });

  it('opens only the clarifications from the clarifications pill', async () => {
    render(<MessageStream messages={turn} {...baseProps} />);
    await userEvent.setup().click(screen.getByText('clarifications'));
    expect(screen.getByRole('heading')).toHaveTextContent('Clarifications · 1');
    expect(screen.getByText('Which branch should I use?')).toBeInTheDocument();
    expect(screen.queryByText('git push origin fix')).not.toBeInTheDocument();
  });
});
