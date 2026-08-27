import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';

jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn(), info: jest.fn(), warning: jest.fn() } }));

const mockAiFollowupResponse = jest.fn();
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    aiFollowupResponse: (...a) => mockAiFollowupResponse(...a),
  },
}));

// Matches the repo-wide DropdownMenu test double (see TriageRulesManager.test.tsx) — renders
// the trigger plus each item as a plain clickable button, sidestepping the real Popper overlay.
jest.mock('@ui/DropdownMenu', () => ({
  DropdownMenu: ({ items, trigger }) => (
    <div data-testid='followup-cancel-menu'>
      {trigger}
      {(items || []).map((it) => (
        <button key={it.label} data-testid={`menu-${it.label}`} onClick={() => it.onSelect?.()} disabled={it.disabled}>
          {it.label}
        </button>
      ))}
    </div>
  ),
}));

import FollowupSheet from '@components/llm/common/FollowupSheet';

const baseFollowup = {
  response: {
    message_id: 'msg-1',
    agent_id: 'agent-1',
    parent_agent_id: null,
    account_id: 'acct-1',
    message_config: JSON.stringify({ question: 'Continue?', followupType: 'text' }),
    status: 'WAITING',
  },
};

beforeEach(() => {
  jest.clearAllMocks();
  mockAiFollowupResponse.mockResolvedValue({});
});

describe('FollowupSheet cancel menu', () => {
  it('always offers "Skip this question", regardless of onStop', () => {
    render(<FollowupSheet followup={baseFollowup} accountId='acct-1' conversationId='conv-1' />);
    expect(screen.getByTestId('followup-skip-button')).toBeInTheDocument();
    expect(screen.queryByTestId('menu-End conversation')).not.toBeInTheDocument();
  });

  it('offers "End conversation" only when onStop is provided, and calls it directly (no API call)', () => {
    const onStop = jest.fn();
    render(<FollowupSheet followup={baseFollowup} accountId='acct-1' conversationId='conv-1' onStop={onStop} />);

    fireEvent.click(screen.getByTestId('menu-End conversation'));

    expect(onStop).toHaveBeenCalledTimes(1);
    expect(mockAiFollowupResponse).not.toHaveBeenCalled();
  });

  it('dismisses via aiFollowupResponse with resolution "dismiss" and no query text', async () => {
    render(<FollowupSheet followup={baseFollowup} accountId='acct-1' conversationId='conv-1' />);

    fireEvent.click(screen.getByTestId('followup-skip-button'));

    expect(mockAiFollowupResponse).toHaveBeenCalledTimes(1);
    expect(mockAiFollowupResponse).toHaveBeenCalledWith(
      expect.objectContaining({
        account_id: 'acct-1',
        conversation_id: 'conv-1',
        message_id: 'msg-1',
        agent_id: 'agent-1',
        resolution: 'dismiss',
      })
    );
  });
});
