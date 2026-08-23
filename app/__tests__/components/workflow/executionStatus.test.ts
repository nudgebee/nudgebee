import { getDuration, getStatusTone, isExecutionCompleted } from '@components/workflow/utils/executionStatus';

describe('isExecutionCompleted', () => {
  it('treats every terminal Temporal status as completed', () => {
    ['COMPLETED', 'COMPLETE_WITH_ERROR', 'FAILED', 'TERMINATED', 'TIMED_OUT', 'CANCELED', 'CONTINUED_AS_NEW'].forEach((status) => {
      expect(isExecutionCompleted(status)).toBe(true);
    });
  });

  it('treats in-flight statuses as not completed', () => {
    expect(isExecutionCompleted('RUNNING')).toBe(false);
    expect(isExecutionCompleted('running')).toBe(false);
  });

  it('is safe on a missing status', () => {
    expect(isExecutionCompleted(undefined)).toBe(false);
  });
});

describe('getStatusTone', () => {
  // These are exactly the statuses Label's built-in text auto-detection gets
  // wrong (it would render them neutral), which is why the tone is explicit.
  it('tones the statuses Label cannot auto-detect', () => {
    expect(getStatusTone('RUNNING')).toBe('info');
    expect(getStatusTone('TERMINATED')).toBe('critical');
    expect(getStatusTone('TIMED_OUT')).toBe('warning');
    expect(getStatusTone('COMPLETE_WITH_ERROR')).toBe('warning');
  });

  it('tones success and failure', () => {
    expect(getStatusTone('COMPLETED')).toBe('success');
    expect(getStatusTone('FAILED')).toBe('critical');
  });

  it('falls back to neutral for anything unrecognised', () => {
    expect(getStatusTone('CANCELED')).toBe('neutral');
    expect(getStatusTone('WHATEVER')).toBe('neutral');
  });
});

describe('getDuration', () => {
  it('scales the unit with the elapsed time', () => {
    expect(getDuration('2026-07-27T10:00:00Z', '2026-07-27T10:00:00.400Z')).toBe('400ms');
    expect(getDuration('2026-07-27T10:00:00Z', '2026-07-27T10:00:12.400Z')).toBe('12.4s');
    expect(getDuration('2026-07-27T10:00:00Z', '2026-07-27T10:03:00Z')).toBe('3.0m');
  });

  // Temporal omits the trailing Z on some paths; without this the browser
  // would read the timestamp as local time and skew every duration.
  it('treats a timestamp without a trailing Z as UTC', () => {
    expect(getDuration('2026-07-27T10:00:00', '2026-07-27T10:00:05')).toBe('5.0s');
  });

  // start_time comes from the server but an open-ended run is measured against
  // the client clock, so skew must not produce a negative duration.
  it('clamps to zero when the client clock is behind the start time', () => {
    const future = new Date(Date.now() + 5000).toISOString();
    expect(getDuration(future)).toBe('0ms');
  });

  it('returns N/A when there is no start time', () => {
    expect(getDuration(undefined, '2026-07-27T10:00:00Z')).toBe('N/A');
  });

  it('measures an open-ended run against now', () => {
    const start = new Date(Date.now() - 5000).toISOString();
    expect(getDuration(start)).toMatch(/^[45]\.\ds$/);
  });
});
