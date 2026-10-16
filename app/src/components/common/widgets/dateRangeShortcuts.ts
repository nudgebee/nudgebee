/**
 * The range picker's rolling shortcuts, mapped to their window in ms. A rolling
 * window ends at the moment it is picked, so it is the same window only if it
 * moves with the clock. The calendar shortcuts — Current Week, Current Month,
 * Last Month — are not in here: their bounds are dates, and their
 * `shortcutClickTime` is just the distance from the start to the click.
 */
export const TIME_ADJUSTMENTS: Record<string, number> = {
  'Last 24 Hours': 24 * 60 * 60 * 1000,
  'Last 1 Hour': 1 * 60 * 60 * 1000,
  'Last 3 Hours': 3 * 60 * 60 * 1000,
  'Last 6 Hours': 6 * 60 * 60 * 1000,
  'Last 12 Hours': 12 * 60 * 60 * 1000,
  'Last 5 Minutes': 5 * 60 * 1000,
  'Last 10 Minutes': 10 * 60 * 1000,
  'Last 15 Minutes': 15 * 60 * 1000,
  'Last 30 Minutes': 30 * 60 * 1000,
};

const ROLLING = new Set(Object.values(TIME_ADJUSTMENTS));

/** Whether a picked range's `shortcutClickTime` is a rolling "last N" window rather than a calendar one. */
export function isRollingShortcut(shortcutClickTime: number): boolean {
  return ROLLING.has(shortcutClickTime);
}
