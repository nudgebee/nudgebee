import { withUtcTimezone } from '../timestamp';

describe('withUtcTimezone', () => {
  it('adds UTC only to timezone-naive timestamps', () => {
    expect(withUtcTimezone('2025-01-15T02:48:08')).toBe('2025-01-15T02:48:08Z');
    expect(withUtcTimezone('2025-01-15T02:48:08Z')).toBe('2025-01-15T02:48:08Z');
    expect(withUtcTimezone('2025-01-15T02:48:08+05:30')).toBe('2025-01-15T02:48:08+05:30');
    expect(withUtcTimezone('2025-01-15T02:48:08+05')).toBe('2025-01-15T02:48:08+05');
    expect(new Date(withUtcTimezone('2025-01-15T02:48:08+05:30')).toISOString()).toBe('2025-01-14T21:18:08.000Z');
  });
});
