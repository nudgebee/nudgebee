import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { llmConversationHistory: jest.fn() },
}));
jest.mock('@lib/auth', () => ({ getUserSession: () => ({ user: { email: 'me@example.com' } }) }));
jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ assistantName: 'nubi' }) }));

import apiAskNudgebee from '@api1/ask-nudgebee';
import PendingFollowUps from '@components/home/PendingFollowUps';

const page = (count) => ({
  data: {
    data: {
      llm_conversations: [{ session_id: `s-${count}`, title: `conversation ${count}`, updated_at: '2026-10-01T00:00:00Z' }],
      llm_conversations_aggregate: { aggregate: { count } },
    },
  },
});

// The component asks for "yours" (user_username) and "team" (user_username_neq) separately.
const mockCounts = ({ yours, team }) =>
  apiAskNudgebee.llmConversationHistory.mockImplementation((params) => Promise.resolve(page(params.user_username_neq ? team : yours)));

describe('PendingFollowUps "View all" link', () => {
  let openSpy;

  beforeEach(() => {
    openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
  });

  afterEach(() => {
    openSpy.mockRestore();
    jest.clearAllMocks();
  });

  it('opens my own waiting conversations from the Yours tab', async () => {
    mockCounts({ yours: 50, team: 50 });
    render(<PendingFollowUps accountId='acct-1' />);

    fireEvent.click(await screen.findByTestId('follow-ups-view-all'));

    expect(openSpy).toHaveBeenCalledWith('/ask-nudgebee?accountId=acct-1&status=WAITING&filter=Mine', '_blank', 'noopener,noreferrer');
  });

  it("opens everyone's waiting conversations from the Team tab", async () => {
    // No conversations of my own, so the card lands on the Team tab by itself.
    mockCounts({ yours: 0, team: 50 });
    render(<PendingFollowUps accountId='acct-1' />);

    await waitFor(() => expect(screen.getByText('conversation 50')).toBeInTheDocument());
    fireEvent.click(await screen.findByTestId('follow-ups-view-all'));

    expect(openSpy).toHaveBeenCalledWith('/ask-nudgebee?accountId=acct-1&status=WAITING&filter=Everyone', '_blank', 'noopener,noreferrer');
  });
});
