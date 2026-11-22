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

/**
 * The instant a held value names, in ms. The picker's own state is a number, but what a page seeds it with
 * is whatever that page holds — a Date, a date string — and the fields already read all of them through
 * `new Date(value)`. Nothing held is not an instant; `new Date(null)` would call it 1970.
 */
function instantOf(value: unknown): number {
  if (value === null || value === undefined || value === '') return NaN;
  return new Date(value as number | string | Date).getTime();
}

/**
 * Why an absolute range cannot be applied, or '' when it can. The picker bounds each field against the
 * calendar — not earlier than the retention floor, not in the future — but not against the other field,
 * so a From after To can be picked and typed like any other value.
 */
export function rangeProblem(startTime: unknown, endTime: unknown): string {
  const start = instantOf(startTime);
  const end = instantOf(endTime);
  if (!Number.isFinite(start) || !Number.isFinite(end)) return 'Enter a valid date and time in both fields.';
  if (start >= end) return 'From must be earlier than To.';
  return '';
}
