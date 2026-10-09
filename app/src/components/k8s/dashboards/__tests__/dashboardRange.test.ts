import type { Panel, PanelTarget } from '@api1/dashboards';
import { panelQueryWindow, refreshedRange } from '../dashboardRange';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

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

/*
 * A table written once a day has nothing to show for an hour. The range the
 * dashboard hands over is widened for such a panel, and only for such a panel.
 */
describe('panelQueryWindow', () => {
  const spend = buildEntityQuery(defaultDraft('spend_groupings_v2')) as never;
  const events = buildEntityQuery(defaultDraft('events_v2')) as never;
  const panel = (target: Partial<PanelTarget>, datasource = 'nudgebee') =>
    ({ id: 1, title: 'p', type: 'table', datasource, targets: [{ ref_id: 'A', ...target }] } as Panel);

  it('reaches back seven days on cloud spend when the range is shorter, keeping its end', () => {
    const window = panelQueryWindow(panel({ query: spend, time_column: 'spend_date' }), NOW - HOUR, NOW);
    expect(window).toEqual({ startTime: NOW - 7 * DAY, endTime: NOW, widened: { days: 7, table: 'Cloud spend' } });
  });

  it('leaves a range that is already seven days or longer alone', () => {
    const spendPanel = panel({ query: spend, time_column: 'spend_date' });
    expect(panelQueryWindow(spendPanel, NOW - 7 * DAY, NOW)).toEqual({ startTime: NOW - 7 * DAY, endTime: NOW });
    expect(panelQueryWindow(spendPanel, SEP_1, NOW)).toEqual({ startTime: SEP_1, endTime: NOW });
  });

  it('leaves a table with no minimum on the range as picked', () => {
    expect(panelQueryWindow(panel({ query: events, time_column: 'created_at' }), NOW - HOUR, NOW)).toEqual({ startTime: NOW - HOUR, endTime: NOW });
  });

  it('has nothing to widen on a spend panel with the time range off', () => {
    expect(panelQueryWindow(panel({ query: spend, time_column: '' }), NOW - HOUR, NOW)).toEqual({ startTime: NOW - HOUR, endTime: NOW });
    expect(panelQueryWindow(panel({ query: spend }), NOW - HOUR, NOW)).toEqual({ startTime: NOW - HOUR, endTime: NOW });
  });

  it('goes by the target the panel runs — the first it does not hide', () => {
    const withTargets = (targets: PanelTarget[]): Panel => ({ ...panel({}), targets });
    const spendSecond = withTargets([
      { ref_id: 'A', hide: true, query: events },
      { ref_id: 'B', query: spend, time_column: 'spend_date' },
    ]);
    expect(panelQueryWindow(spendSecond, NOW - HOUR, NOW).widened).toEqual({ days: 7, table: 'Cloud spend' });

    const spendHidden = withTargets([
      { ref_id: 'A', hide: true, query: spend, time_column: 'spend_date' },
      { ref_id: 'B', query: events, time_column: 'created_at' },
    ]);
    expect(panelQueryWindow(spendHidden, NOW - HOUR, NOW)).toEqual({ startTime: NOW - HOUR, endTime: NOW });
  });

  it('leaves every other datasource alone', () => {
    expect(panelQueryWindow(panel({ expr: 'up' }, 'metrics'), NOW - HOUR, NOW)).toEqual({ startTime: NOW - HOUR, endTime: NOW });
  });
});
