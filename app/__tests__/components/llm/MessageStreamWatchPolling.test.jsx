import React from 'react';
import { act, render } from '@testing-library/react';
import MessageStream from '@components/llm/MessageStream';
import api from '@api1/ask-nudgebee';
import { WATCH_FOLLOWUP_BUDGET_MS, watchUpdateMarker } from '@components/llm/utils/watchFollowup';

// Wiring-level cover for the watch follow-up poller. watchFollowup.test.js pins
// the decision logic; this pins that MessageStream actually drives it — the ref
// mirroring, the effect deps, and both reschedule paths.

jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { listWatchesByConversation: jest.fn() } }));
jest.mock('@hooks/useTenantBranding', () => ({ useWatchFeatureEnabled: () => true }));
jest.mock('@hooks/useMessageAdditionalData', () => ({ __esModule: true, default: () => ({}) }));
jest.mock('@components/llm/MessageItem', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/WatchesTab', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/CustomDrawer', () => ({ __esModule: true, default: () => null, SecondaryDrawer: () => null }));
jest.mock('@components/llm/common/TasksDrawerContent', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/common/MemoriesDrawerContent', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/common/ReferencesDrawerContent', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/common/ChannelContextDrawerContent', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/common/ToolDetails', () => ({ __esModule: true, default: () => null }));

const CID = 'conv-1';
const W1 = '11111111-1111-1111-1111-111111111111';
const POLL_MS = 5000;

const reply = (text) => ({ type: 'response', text });
const PENDING_REPLY = [reply('Watching the rollout.')];
const SETTLED_REPLY = [reply(`Watching the rollout.\n\n${watchUpdateMarker(W1)}\n**✅ Watch update — COMPLETED**\n`)];

const renderStream = (props = {}) => {
  const onWatchTerminal = props.onWatchTerminal || jest.fn();
  const view = render(
    <MessageStream
      messages={props.messages || PENDING_REPLY}
      isProcessing={props.isProcessing || false}
      collapsedObj={{}}
      setCollapsedObj={jest.fn()}
      showFullText={{}}
      setShowFullText={jest.fn()}
      itemProps={{ accountId: 'acct-1', conversationId: CID, onWatchTerminal }}
    />
  );
  return { ...view, onWatchTerminal };
};

// The effect's first tick runs synchronously on mount; everything after it is
// timer-driven. Both need their promise chains flushed.
const settle = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};
const advance = async (ms = POLL_MS) => {
  await act(async () => {
    jest.advanceTimersByTime(ms);
    await Promise.resolve();
    await Promise.resolve();
  });
};

beforeEach(() => {
  jest.useFakeTimers();
  api.listWatchesByConversation.mockReset();
});
afterEach(() => {
  jest.useRealTimers();
});

describe('MessageStream watch follow-up polling', () => {
  it('keeps re-fetching across the summarizer window and stands down once the block lands', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    const { onWatchTerminal, rerender } = renderStream();

    await settle();
    expect(onWatchTerminal).toHaveBeenCalledTimes(1); // t=0, block not written yet

    await advance();
    await advance();
    expect(onWatchTerminal).toHaveBeenCalledTimes(3); // t=5s, t=10s — still waiting

    // The responder's write lands; the next re-fetch pulls it into `messages`.
    rerender(
      <MessageStream
        messages={SETTLED_REPLY}
        isProcessing={false}
        collapsedObj={{}}
        setCollapsedObj={jest.fn()}
        showFullText={{}}
        setShowFullText={jest.fn()}
        itemProps={{ accountId: 'acct-1', conversationId: CID, onWatchTerminal }}
      />
    );

    await advance();
    expect(onWatchTerminal).toHaveBeenCalledTimes(3); // marker found: no further fetch

    const callsAtStandDown = api.listWatchesByConversation.mock.calls.length;
    await advance();
    await advance();
    expect(api.listWatchesByConversation).toHaveBeenCalledTimes(callsAtStandDown); // poller stopped
  });

  it('never fires when the block was already present on load', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    const { onWatchTerminal } = renderStream({ messages: SETTLED_REPLY });

    await settle();
    await advance();
    expect(onWatchTerminal).not.toHaveBeenCalled();
  });

  it('fires for a watch already terminal on first sighting — the mid-summarize page open the old transition check missed', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    const { onWatchTerminal } = renderStream();

    await settle();
    expect(onWatchTerminal).toHaveBeenCalledTimes(1);
  });

  it('gives up at the budget when the block never lands', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    const { onWatchTerminal } = renderStream();

    await settle();
    for (let elapsed = POLL_MS; elapsed <= WATCH_FOLLOWUP_BUDGET_MS + 4 * POLL_MS; elapsed += POLL_MS) {
      await advance();
    }
    expect(onWatchTerminal).toHaveBeenCalledTimes(WATCH_FOLLOWUP_BUDGET_MS / POLL_MS);
  });

  it('does not restart the budget when isProcessing flips mid-flight', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    const onWatchTerminal = jest.fn();
    const props = (isProcessing) => ({
      messages: PENDING_REPLY,
      isProcessing,
      collapsedObj: {},
      setCollapsedObj: jest.fn(),
      showFullText: {},
      setShowFullText: jest.fn(),
      itemProps: { accountId: 'acct-1', conversationId: CID, onWatchTerminal },
    });
    const { rerender } = render(<MessageStream {...props(false)} />);
    await settle();

    // Halfway through the budget, the user sends a follow-up question:
    // isProcessing goes true then false, tearing the poll effect down and
    // re-arming it twice. (Each re-arm fires one immediate tick — that is the
    // poller's pre-existing behaviour and not what this test measures.)
    for (let i = 0; i < 6; i += 1) {
      await advance(); // t = 30s
    }
    rerender(<MessageStream {...props(true)} />);
    await settle();
    rerender(<MessageStream {...props(false)} />);
    await settle();

    // Past the ORIGINAL deadline.
    for (let i = 0; i < 8; i += 1) {
      await advance(); // t = 70s
    }
    const afterOriginalBudget = onWatchTerminal.mock.calls.length;

    // Past where a budget restarted at t=30s would have expired. If the toggles
    // had cleared the deadline map, the loop would still be running here.
    for (let i = 0; i < 12; i += 1) {
      await advance(); // t = 130s
    }
    expect(onWatchTerminal.mock.calls.length).toBe(afterOriginalBudget);
  });

  it('starts a fresh budget for a different conversation', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    const onWatchTerminal = jest.fn();
    const props = (conversationId) => ({
      messages: PENDING_REPLY,
      isProcessing: false,
      collapsedObj: {},
      setCollapsedObj: jest.fn(),
      showFullText: {},
      setShowFullText: jest.fn(),
      itemProps: { accountId: 'acct-1', conversationId, onWatchTerminal },
    });
    const { rerender } = render(<MessageStream {...props(CID)} />);
    await settle();
    for (let elapsed = POLL_MS; elapsed <= WATCH_FOLLOWUP_BUDGET_MS; elapsed += POLL_MS) {
      await advance();
    }
    const exhausted = onWatchTerminal.mock.calls.length;

    rerender(<MessageStream {...props('conv-2')} />);
    await settle();
    expect(onWatchTerminal.mock.calls.length).toBe(exhausted + 1);
  });

  it('does not fire for a watch that is still running, and keeps polling it', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'ACTIVE' }]);
    const { onWatchTerminal } = renderStream();

    await settle();
    await advance();
    await advance();
    expect(onWatchTerminal).not.toHaveBeenCalled();
    expect(api.listWatchesByConversation.mock.calls.length).toBeGreaterThanOrEqual(3);
  });

  it('stops polling entirely on a settled chat with no live watch and nothing outstanding', async () => {
    api.listWatchesByConversation.mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    renderStream({ messages: SETTLED_REPLY });

    await settle();
    expect(api.listWatchesByConversation).toHaveBeenCalledTimes(1);
    await advance();
    await advance();
    expect(api.listWatchesByConversation).toHaveBeenCalledTimes(1);
  });

  it('stands down at the budget even while the watch-list endpoint keeps failing', async () => {
    // Deadlines are only refreshed on a successful tick, so the error path has
    // to re-read them rather than trusting the last good tick's flag —
    // otherwise a sustained outage retries at the 30s backoff cap forever.
    api.listWatchesByConversation.mockResolvedValueOnce([{ id: W1, status: 'COMPLETED' }]).mockRejectedValue(new Error('boom'));
    renderStream();
    await settle();

    for (let i = 0; i < 20; i += 1) {
      await advance(); // t = 100s, well past the budget
    }
    const callsWellPastBudget = api.listWatchesByConversation.mock.calls.length;

    for (let i = 0; i < 30; i += 1) {
      await advance(); // t = 250s
    }
    expect(api.listWatchesByConversation.mock.calls.length).toBe(callsWellPastBudget);
  });

  it('survives a transient watch-list failure while a follow-up is outstanding', async () => {
    api.listWatchesByConversation
      .mockResolvedValueOnce([{ id: W1, status: 'COMPLETED' }])
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValue([{ id: W1, status: 'COMPLETED' }]);
    const { onWatchTerminal } = renderStream();

    await settle();
    expect(onWatchTerminal).toHaveBeenCalledTimes(1);

    await advance(); // the rejected tick — backs off rather than dying
    expect(onWatchTerminal).toHaveBeenCalledTimes(1);

    await advance(POLL_MS * 2); // backoff elapses, poller recovers
    expect(onWatchTerminal.mock.calls.length).toBeGreaterThan(1);
  });
});
