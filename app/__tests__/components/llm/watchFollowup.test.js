import {
  WATCH_FOLLOWUP_BUDGET_MS,
  countAwaitingFollowups,
  hasWatchUpdateBlock,
  reconcilePendingFollowups,
  watchUpdateMarker,
} from '@components/llm/utils/watchFollowup';

const W1 = '11111111-1111-1111-1111-111111111111';
const W2 = '22222222-2222-2222-2222-222222222222';

// Shape the chat stream actually holds: the agent reply carries the raw
// response text, markers included.
const reply = (text) => ({ type: 'response', text });
const withBlock = (watchId) => reply(`All good.\n\n---\n\n${watchUpdateMarker(watchId)}\n**✅ Watch update — COMPLETED**\n\n> Rollout finished.\n`);

describe('hasWatchUpdateBlock', () => {
  it('finds the responder marker in a loaded reply', () => {
    expect(hasWatchUpdateBlock([reply('Watching.'), withBlock(W1)], W1)).toBe(true);
  });

  it('does not match another watch marker on the same message', () => {
    expect(hasWatchUpdateBlock([withBlock(W2)], W1)).toBe(false);
  });

  it('tolerates missing, empty and non-string message bodies', () => {
    expect(hasWatchUpdateBlock(null, W1)).toBe(false);
    expect(hasWatchUpdateBlock([], W1)).toBe(false);
    expect(hasWatchUpdateBlock([null, {}, { text: 42 }, reply(undefined)], W1)).toBe(false);
  });
});

describe('reconcilePendingFollowups', () => {
  const NOW = 1_700_000_000_000;
  const run = (rows, messages, pending = new Map(), now = NOW) => reconcilePendingFollowups({ rows, messages, pending, now });

  it('tracks a terminal watch whose block has not landed yet', () => {
    const next = run([{ id: W1, status: 'COMPLETED' }], [reply('Watching.')]);
    expect(next.get(W1)).toBe(NOW + WATCH_FOLLOWUP_BUDGET_MS);
    expect(countAwaitingFollowups(next, NOW)).toBe(1);
  });

  it('ignores a terminal watch whose block is already present — the initial load case, zero refetches', () => {
    const next = run([{ id: W1, status: 'COMPLETED' }], [withBlock(W1)]);
    expect(next.size).toBe(0);
    expect(countAwaitingFollowups(next, NOW)).toBe(0);
  });

  it('ignores watches that are still running', () => {
    const next = run(
      [
        { id: W1, status: 'PENDING' },
        { id: W2, status: 'ACTIVE' },
      ],
      [reply('Watching.')]
    );
    expect(next.size).toBe(0);
  });

  it('tracks every terminal status, not just COMPLETED', () => {
    ['COMPLETED', 'EXPIRED', 'FAILED', 'CANCELLED'].forEach((status) => {
      expect(run([{ id: W1, status }], [reply('Watching.')]).size).toBe(1);
    });
  });

  it('carries an existing deadline forward instead of restarting the budget', () => {
    const rows = [{ id: W1, status: 'COMPLETED' }];
    const messages = [reply('Watching.')];
    const first = run(rows, messages);
    const later = run(rows, messages, first, NOW + 20_000);
    expect(later.get(W1)).toBe(first.get(W1));
  });

  it('leaves tracking untouched while no messages are loaded', () => {
    const pending = new Map([[W1, NOW + 1000]]);
    expect(run([{ id: W1, status: 'COMPLETED' }], [], pending)).toBe(pending);
    expect(run([{ id: W1, status: 'COMPLETED' }], null, pending)).toBe(pending);
  });

  it('skips rows with no id', () => {
    expect(run([{ status: 'COMPLETED' }], [reply('Watching.')]).size).toBe(0);
  });
});

describe('the poll loop this drives', () => {
  const NOW = 1_700_000_000_000;
  const rows = [{ id: W1, status: 'COMPLETED' }];

  // Reproduces the bug: llm-server flips the watch to COMPLETED, then spends
  // ~8s on the summarizer before the responder appends the block. The old
  // fire-once-on-transition trigger refetched inside that window and stopped.
  it('keeps polling across the summarizer window, then stops once the block lands', () => {
    let pending = new Map();
    let messages = [reply('Watching.')];
    let fetches = 0;

    for (let elapsed = 0; elapsed <= 15_000; elapsed += 5_000) {
      const now = NOW + elapsed;
      pending = reconcilePendingFollowups({ rows, messages, pending, now });
      if (countAwaitingFollowups(pending, now) > 0) {
        fetches += 1;
        // The responder's write lands ~8s after the terminal transition, so the
        // t=10s refetch is the one that sees it.
        if (elapsed >= 8_000) {
          messages = [withBlock(W1)];
        }
      }
    }

    expect(fetches).toBe(3); // t=0, t=5s, t=10s — t=15s finds the marker and stands down
    expect(pending.size).toBe(0);
  });

  it('gives up at the budget and stays given up when the block never lands', () => {
    let pending = new Map();
    const messages = [reply('Watching.')];
    let fetches = 0;

    for (let elapsed = 0; elapsed <= WATCH_FOLLOWUP_BUDGET_MS + 60_000; elapsed += 5_000) {
      const now = NOW + elapsed;
      pending = reconcilePendingFollowups({ rows, messages, pending, now });
      if (countAwaitingFollowups(pending, now) > 0) {
        fetches += 1;
      }
    }

    // Bounded by the budget, and the expired entry is retained so later ticks
    // cannot re-arm it with a fresh deadline.
    expect(fetches).toBe(WATCH_FOLLOWUP_BUDGET_MS / 5_000);
    expect(pending.get(W1)).toBe(NOW + WATCH_FOLLOWUP_BUDGET_MS);
  });

  it('fires for a watch already terminal on first sighting — the mid-summarize page open', () => {
    const now = NOW;
    const pending = reconcilePendingFollowups({ rows, messages: [reply('Watching.')], pending: new Map(), now });
    expect(countAwaitingFollowups(pending, now)).toBe(1);
  });
});
