import { latestLiveStatus, liveStatusText } from '../common/liveStatus';

describe('latestLiveStatus', () => {
  it('returns null when there is nothing to show', () => {
    expect(latestLiveStatus(undefined)).toBeNull();
    expect(latestLiveStatus({})).toBeNull();
  });

  it('picks the most recently updated agent entry', () => {
    const status = latestLiveStatus({
      a1: { text: 'old', updated_at: '2026-10-09T10:00:00Z' },
      a2: { text: 'new', updated_at: '2026-10-09T10:00:05Z' },
    });
    expect(status.text).toBe('new');
  });
});

describe('liveStatusText', () => {
  const now = new Date('2026-10-09T10:00:00Z').getTime();

  it('counts down to the retry', () => {
    const text = liveStatusText(
      { text: "Couldn't reach the platform AI model.", retry_at: '2026-10-09T10:00:08Z', attempt: 2, max_attempts: 4 },
      now
    );
    expect(text).toContain("Couldn't reach the platform AI model");
    expect(text).toContain('Retrying in 8s');
    expect(text).toContain('attempt 2 of 4');
  });

  it('says retrying now once the wait is over', () => {
    const text = liveStatusText({ text: 'X is rate limited.', retry_at: '2026-10-09T09:59:59Z' }, now);
    expect(text).toContain('X is rate limited');
    expect(text).toContain('Retrying now');
  });

  it('shows plain text when there is no retry', () => {
    const text = liveStatusText({ text: 'Context too long. Summarising earlier steps…' }, now);
    expect(text).toContain('Context too long');
    expect(text).toContain('Summarising earlier steps');
    expect(text).not.toContain('Retrying');
  });
});
