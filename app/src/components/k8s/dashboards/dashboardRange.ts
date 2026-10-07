import { isRollingShortcut } from '@shared/widgets/dateRangeShortcuts';

/** A dashboard's time range as the range picker hands it over, in epoch ms. */
export interface DashboardRange {
  startDate: number;
  endDate: number;
  /** Set when a shortcut was picked: a rolling window's length, or a calendar window's start-to-click distance. */
  shortcutClickTime: number;
}

/**
 * The range a Refresh should fetch.
 *
 * A rolling window ("Last 1 Hour") has to move to now — refetching the same
 * fixed window would return the same data and read as a broken button. A
 * calendar window must not: "Last Month" moved to now became "start of last
 * month → today" under a label still reading "Sep 1 - Sep 30". "Current Month"
 * keeps its start and runs to now; an absolute range stays as picked.
 */
export function refreshedRange(range: DashboardRange, now: number): DashboardRange {
  const { startDate, endDate, shortcutClickTime } = range;
  if (!(shortcutClickTime > 0)) return range;
  if (isRollingShortcut(shortcutClickTime)) return { ...range, startDate: now - shortcutClickTime, endDate: now };
  // A calendar window that ran to the instant it was picked is open-ended; one that ended before it is closed.
  if (endDate >= startDate + shortcutClickTime) return { ...range, endDate: now };
  return range;
}
