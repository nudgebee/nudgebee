import React, { useState } from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import ConversationList from '@components/llm/ConversationListV2';
import apiAskNudgebee from '@api1/ask-nudgebee';

// The list is driven entirely by what the API returns, so the network layer is the
// only thing that has to be faked. Everything else is stubbed to keep the tree light.
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { llmConversationHistory: jest.fn() },
}));

jest.mock('next/router', () => ({
  useRouter: () => ({ query: {}, pathname: '/ask-nudgebee', replace: jest.fn() }),
}));

jest.mock('@lib/auth', () => ({
  getUserSession: () => ({ user: { email: 'me@nudgebee.com' } }),
}));

jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

jest.mock('@components/workflow/NewToggleButtons', () => {
  return function MockToggleButtons({ options, onChange }) {
    return (
      <div>
        {options.map((option) => (
          <button key={option.value} onClick={() => onChange(option.value)}>
            {option.label}
          </button>
        ))}
      </div>
    );
  };
});

const deferred = () => {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
};

const conversation = ({ id, title, email }) => ({
  id,
  session_id: `sess-${id}`,
  title,
  status: 'COMPLETED',
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
  source: 'UserInvestigation',
  user: { display_name: email, username: email },
  for_status: [],
  llm_conversation_saveds: [],
});

const response = (rows) => ({ data: { data: { llm_conversations: rows } } });

const Harness = () => {
  const [rawConversations, setRawConversations] = useState([]);
  return (
    <ConversationList
      accountId='acct-1'
      onSelectConversation={() => {}}
      selectedId={null}
      isConversationListVisible
      triggerHandleNewChat={() => {}}
      handleShare={() => {}}
      likedConversations={[]}
      setLikedConversations={() => {}}
      savingStates={{}}
      handleLike={() => {}}
      setSelectedConversation={() => {}}
      rawConversations={rawConversations}
      setRawConversations={setRawConversations}
      onCollapseConversationList={() => {}}
    />
  );
};

describe('ConversationListV2 — filter switching', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  // Regression for #35204: switching to "Mine" while the "All" request was still in
  // flight used to (a) drop the "Mine" fetch on the shared loading guard and (b) merge
  // the late "All" response into the freshly cleared list, so the "Mine" tab listed
  // other users' conversations.
  it('does not render the late "All" response after the user switched to "Mine"', async () => {
    const allRequest = deferred();
    const mineRequest = deferred();
    apiAskNudgebee.llmConversationHistory.mockImplementationOnce(() => allRequest.promise).mockImplementationOnce(() => mineRequest.promise);

    render(<Harness />);

    expect(apiAskNudgebee.llmConversationHistory).toHaveBeenCalledTimes(1);
    expect(apiAskNudgebee.llmConversationHistory.mock.calls[0][0].activeFilter).toBe('All');

    await act(async () => {
      fireEvent.click(screen.getByText('Mine'));
    });

    // The "Mine" fetch must actually be issued even though "All" is still pending.
    expect(apiAskNudgebee.llmConversationHistory).toHaveBeenCalledTimes(2);
    expect(apiAskNudgebee.llmConversationHistory.mock.calls[1][0].activeFilter).toBe('Mine');

    await act(async () => {
      allRequest.resolve(response([conversation({ id: 'other', title: 'Hi', email: 'someone.else@nudgebee.com' })]));
      await allRequest.promise;
    });
    expect(screen.queryByText('Hi')).not.toBeInTheDocument();

    await act(async () => {
      mineRequest.resolve(response([conversation({ id: 'mine', title: 'My own query', email: 'me@nudgebee.com' })]));
      await mineRequest.promise;
    });
    expect(screen.getByText('My own query')).toBeInTheDocument();
    expect(screen.queryByText('Hi')).not.toBeInTheDocument();
  });

  it('does not let the abandoned "All" request reschedule its poll', async () => {
    jest.useFakeTimers();
    try {
      const allRequest = deferred();
      const mineRequest = deferred();
      apiAskNudgebee.llmConversationHistory
        .mockImplementationOnce(() => allRequest.promise)
        .mockImplementationOnce(() => mineRequest.promise)
        .mockImplementation(() => Promise.resolve(response([])));

      render(<Harness />);
      await act(async () => {
        fireEvent.click(screen.getByText('Mine'));
      });

      await act(async () => {
        allRequest.resolve(response([]));
        await allRequest.promise;
      });
      await act(async () => {
        mineRequest.resolve(response([conversation({ id: 'mine', title: 'My own query', email: 'me@nudgebee.com' })]));
        await mineRequest.promise;
      });

      await act(async () => {
        jest.advanceTimersByTime(6000);
      });

      // Only the live "Mine" poll may fire; the abandoned "All" closure must not.
      const calls = apiAskNudgebee.llmConversationHistory.mock.calls;
      expect(calls.length).toBeGreaterThanOrEqual(3);
      expect(calls.slice(1).map((call) => call[0].activeFilter)).toEqual(calls.slice(1).map(() => 'Mine'));
    } finally {
      jest.useRealTimers();
    }
  });
});
