import { refreshedRange } from '../dashboardRange';

/*
 * What Refresh fetches, for each kind of range the picker hands over. The
 * shapes are the picker's own: a rolling shortcut's `shortcutClickTime` is its
 * length; a calendar one's is the distance from its start to the click.
 */

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const SEP_1 = Date.UTC(2026, 8, 1);
const OCT_1 = Date.UTC(2026, 9, 1);
const CLICK = Date.UTC(2026, 9, 7, 15, 0);
const NOW = CLICK + 10 * 60 * 1000;

describe('refreshedRange', () => {
  it('moves a rolling window to end now, keeping its length', () => {
    const range = { startDate: CLICK - HOUR, endDate: CLICK, shortcutClickTime: HOUR };
    expect(refreshedRange(range, NOW)).toEqual({ startDate: NOW - HOUR, endDate: NOW, shortcutClickTime: HOUR });
  });

  it('leaves Last Month where it was — a closed window under a label naming its dates', () => {
    const range = { startDate: SEP_1, endDate: OCT_1 - 1, shortcutClickTime: CLICK - SEP_1 };
    expect(refreshedRange(range, NOW)).toBe(range);
  });

  it('keeps Current Month starting on the 1st and runs it to now, refresh after refresh', () => {
    const range = { startDate: OCT_1, endDate: CLICK, shortcutClickTime: CLICK - OCT_1 };
    const once = refreshedRange(range, NOW);
    expect(once).toEqual({ ...range, endDate: NOW });
    expect(refreshedRange(once, NOW + HOUR)).toEqual({ ...range, endDate: NOW + HOUR });
  });

  it('leaves an absolute range as picked', () => {
    const range = { startDate: SEP_1, endDate: SEP_1 + DAY, shortcutClickTime: 0 };
    expect(refreshedRange(range, NOW)).toBe(range);
  });

  it('treats Current Week as the picker does — the last seven days', () => {
    const range = { startDate: CLICK - 7 * DAY, endDate: CLICK, shortcutClickTime: 7 * DAY };
    expect(refreshedRange(range, NOW)).toEqual({ startDate: NOW - 7 * DAY, endDate: NOW, shortcutClickTime: 7 * DAY });
  });
});
