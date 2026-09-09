// Helpers for the "has this watch's follow-up landed yet?" check the chat
// stream polls on.
//
// Background: a watch reaching a terminal state does NOT mean its in-thread
// follow-up has been delivered. `watch.Executor.terminate`
// (llm/llm-server/watch/executor.go) commits the terminal status FIRST, then
// spends up to LLM_SERVER_WATCH_SUMMARIZER_TIMEOUT_SEC (default 30s) on the
// summarizer LLM call, and only then appends the rendered block to the parent
// message's `response` column. That order is deliberate — MarkTerminal is the
// claim that stops a duplicate dispatch from paying for a second summarizer
// call — so the client has to tolerate a window where a watch reads COMPLETED
// while its follow-up is still missing from the conversation.
//
// The responder stamps every block with `<!-- watch-update:<watch id> -->` as
// its idempotency token (llm/llm-server/watch/responder.go; the format is
// pinned by responder_test.go). We reuse that token as the client-side
// has-it-landed signal so the stream stops refetching on evidence rather than
// after a guessed delay.

// Watch statuses that mean "this watch will not poll again".
export const TERMINAL_WATCH_STATUSES = ['COMPLETED', 'EXPIRED', 'FAILED', 'CANCELLED'];

// How long to keep refetching for a terminal watch's follow-up before giving
// up. Covers the server-side worst case (30s summarizer + 5s responder write)
// with headroom for the 5s poll granularity. This is a runaway guard, not a
// correctness parameter — polling normally stops the moment the marker shows
// up. Worth raising if an operator raises the summarizer timeout.
export const WATCH_FOLLOWUP_BUDGET_MS = 60_000;

export const watchUpdateMarker = (watchId) => `<!-- watch-update:${watchId} -->`;

// True once the responder's block for `watchId` is present in the loaded
// message bodies. The marker is an HTML comment, so it round-trips through the
// markdown renderer without being shown to the user.
export const hasWatchUpdateBlock = (messages, watchId) => {
  const marker = watchUpdateMarker(watchId);
  return (messages || []).some((m) => typeof m?.text === 'string' && m.text.includes(marker));
};

/**
 * Recompute which watches still owe this conversation a follow-up block.
 *
 * @param {object} args
 * @param {Array}  args.rows      Watch rows from listWatchesByConversation.
 * @param {Array}  args.messages  Currently-loaded UI messages.
 * @param {Map}    args.pending   Previous result: watch id -> deadline (epoch ms).
 * @param {number} args.now       Current epoch ms.
 * @param {number} [args.budgetMs]
 * @returns {Map} watch id -> deadline (epoch ms)
 */
export function reconcilePendingFollowups({ rows, messages, pending, now, budgetMs = WATCH_FOLLOWUP_BUDGET_MS }) {
  // Nothing loaded yet. The responder appends to an existing message, so there
  // is nothing a refetch could reveal — leave any in-flight tracking alone
  // rather than starting deadlines against a conversation we haven't read.
  if (!messages?.length) {
    return pending || new Map();
  }
  const next = new Map();
  (rows || []).forEach((r) => {
    if (!r?.id || !TERMINAL_WATCH_STATUSES.includes(r.status)) {
      return;
    }
    // Landed — either just now or already present on initial load. Drop it for
    // good; the marker check short-circuits it on every later tick too.
    if (hasWatchUpdateBlock(messages, r.id)) {
      return;
    }
    // Carry an existing deadline forward. Entries past their deadline are KEPT
    // (with the original deadline) rather than dropped: dropping them would
    // make the next tick treat the watch as newly-seen and restart the budget,
    // polling forever for a block that is never coming.
    next.set(r.id, pending?.get(r.id) ?? now + budgetMs);
  });
  return next;
}

// How many tracked watches are still inside their budget.
export function countAwaitingFollowups(pending, now) {
  let awaiting = 0;
  (pending || new Map()).forEach((deadline) => {
    if (now < deadline) {
      awaiting += 1;
    }
  });
  return awaiting;
}
