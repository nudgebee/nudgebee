import { render, screen } from '@testing-library/react';
import ConversationLoader from '@shared/ConversationLoader';

// Regression coverage for the elapsed-time stopwatch anchoring: a page refresh
// remounts ConversationLoader from scratch, so without a `startedAt` anchor the
// clock always restarted at 0:00 even mid-step. `startedAt` (the in-progress
// row's created_at) lets it seed from real elapsed time instead.
describe('ConversationLoader elapsed timer', () => {
  it('seeds the stopwatch from startedAt instead of 0:00', () => {
    const startedAt = new Date(Date.now() - 31000).toISOString(); // 31s ago
    render(<ConversationLoader query='scanning your cluster' startedAt={startedAt} />);
    expect(screen.getByRole('timer').textContent).toBe('0:31');
  });

  it('falls back to counting from mount time when startedAt is absent', () => {
    render(<ConversationLoader query='scanning your cluster' />);
    // Under 3s elapsed, the timer isn't shown yet (see the elapsedSec >= 3 gate).
    expect(screen.queryByRole('timer')).toBeNull();
  });
});
