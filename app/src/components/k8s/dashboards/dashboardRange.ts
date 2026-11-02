import type { Panel } from '@api1/dashboards';
import { isRollingShortcut } from '@shared/widgets/dateRangeShortcuts';
import { findTable } from './entityQuery';

const DAY_MS = 24 * 60 * 60 * 1000;

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

/** The window one panel's query runs over, in epoch ms. */
export interface PanelQueryWindow {
  startTime: number;
  endTime: number;
  /** Set when the table's minimum made the window longer than the dashboard's range — what the panel tells the viewer. */
  widened?: { days: number; table: string };
}

/**
 * The window a panel's query runs over.
 *
 * The dashboard's own range, except on a table with a minimum window (see
 * `minWindowDays`). Cloud spend is written once a day, so "Last 1 Hour" — what
 * every dashboard opens on — matched no row and the panel read as "no spend".
 * Such a panel keeps the range's end and reaches back the table's minimum
 * instead. Only a panel that follows the time range at all: with no time
 * column there is no window to widen.
 */
export function panelQueryWindow(panel: Panel, startTime: number, endTime: number): PanelQueryWindow {
  // The target the panel runs: the first it does not hide, as usePanelData reads it.
  const target = (panel.targets || []).find((t) => !t.hide);
  if (panel.datasource !== 'nudgebee' || !target?.query || !target.time_column) return { startTime, endTime };
  const table = findTable(String(target.query.table || ''));
  const days = table.minWindowDays;
  if (!days || endTime - startTime >= days * DAY_MS) return { startTime, endTime };
  return { startTime: endTime - days * DAY_MS, endTime, widened: { days, table: table.label } };
}
