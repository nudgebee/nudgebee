/**
 * Spend query windows must be anchored to UTC.
 *
 * Spend rows are stored as a UTC calendar date at midnight in a `timestamp
 * without time zone` column. A window built from a local Date and serialised
 * with toISOString shifts by the viewer's offset, so the same account shows a
 * different month-to-date total depending on who is looking at it.
 *
 * These assert on the serialised ISO string, because that is what actually
 * reaches the query — asserting on the Date object would pass even if the
 * boundary were wrong.
 */

import {
  getStartOfMonthUTC,
  getEndOfMonthUTC,
  getStartOfLastMonthUTC,
  getEndOfLastMonthUTC,
  getStartOfYearUTC,
  getEndOfYearUTC,
  getStartOfMonth,
} from '@lib/datetime';

describe('UTC-anchored spend windows', () => {
  it('starts the month at UTC midnight on the 1st', () => {
    expect(getStartOfMonthUTC(new Date(2026, 7, 19)).toISOString()).toBe('2026-08-01T00:00:00.000Z');
  });

  it('ends the month at the last instant of the last day, in UTC', () => {
    expect(getEndOfMonthUTC(new Date(2026, 7, 19)).toISOString()).toBe('2026-08-31T23:59:59.999Z');
  });

  it('handles February in a leap year', () => {
    expect(getEndOfMonthUTC(new Date(2024, 1, 10)).toISOString()).toBe('2024-02-29T23:59:59.999Z');
  });

  // The bug this replaces: stepping a Date back one month with setMonth
  // overflows on the 29th-31st, so "last month" resolved to the current one.
  it.each([29, 30, 31])('resolves last month correctly on the %ith', (day) => {
    const onALongMonthDay = new Date(2026, 6, day); // July 2026
    expect(getStartOfLastMonthUTC(onALongMonthDay).toISOString()).toBe('2026-06-01T00:00:00.000Z');
    expect(getEndOfLastMonthUTC(onALongMonthDay).toISOString()).toBe('2026-06-30T23:59:59.999Z');
  });

  it('rolls last month back across a year boundary', () => {
    const january = new Date(2026, 0, 15);
    expect(getStartOfLastMonthUTC(january).toISOString()).toBe('2025-12-01T00:00:00.000Z');
    expect(getEndOfLastMonthUTC(january).toISOString()).toBe('2025-12-31T23:59:59.999Z');
  });

  it('covers the whole calendar year', () => {
    const d = new Date(2026, 7, 19);
    expect(getStartOfYearUTC(d).toISOString()).toBe('2026-01-01T00:00:00.000Z');
    expect(getEndOfYearUTC(d).toISOString()).toBe('2026-12-31T23:59:59.999Z');
  });

  // Guards the reason these exist at all. The local-time helper is still correct
  // for its own callers; it is simply the wrong tool for a UTC-dated column, and
  // this fails loudly if someone "simplifies" the spend windows back to it.
  it('differs from the local-time helper whenever the runner is not on UTC', () => {
    const d = new Date(2026, 7, 19);
    const offsetMinutes = new Date(2026, 7, 1).getTimezoneOffset();
    const local = getStartOfMonth(d).toISOString();
    const utc = getStartOfMonthUTC(d).toISOString();
    if (offsetMinutes === 0) {
      expect(local).toBe(utc);
    } else {
      expect(local).not.toBe(utc);
    }
  });
});
