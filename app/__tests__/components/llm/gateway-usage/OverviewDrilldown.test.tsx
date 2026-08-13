import { render, screen, fireEvent } from '@testing-library/react';
import OverviewView from '@components/llm/gateway-usage/views/OverviewView';

// Clicking a column on the Overview "over time" chart scopes the whole report to
// that column's day. This half covers OverviewView resolving a click index back to
// a date; the shell applying it (and moving the date picker) is covered in
// GatewayUsageDrilldown.test.tsx, which needs this view mocked out.

// ─── Chart stub: exposes onSelectPoint as one button per column ────────────────
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    TimeSeries: ({ labels, onSelectPoint }: any) => (
      <div data-testid='chart' data-clickable={onSelectPoint ? 'true' : 'false'}>
        {labels?.map((l: string, i: number) => (
          <button key={l} data-testid={`col-${i}`} onClick={() => onSelectPoint?.(l, i)}>
            {l}
          </button>
        ))}
      </div>
    ),
  },
}));

jest.mock('@shared/tables/CustomTable', () => ({ __esModule: true, default: () => <div /> }));
jest.mock('@ui/ToggleGroup', () => ({ ToggleGroup: () => <div /> }));

const filters = { startDate: '2026-07-09', endDate: '2026-07-15', granularity: 'day' as const };

// A deliberately gappy series: the API only returns buckets that had traffic, so
// column index must resolve through the rows themselves, not a start→end day-walk.
const rows = [
  { bucket: '2026-07-09T00:00:00Z', cost_usd: 1, tokens: 10, requests: 2 },
  { bucket: '2026-07-14T00:00:00Z', cost_usd: 13, tokens: 100, requests: 20 },
  { bucket: '2026-07-15T00:00:00Z', cost_usd: 0.15, tokens: 5, requests: 1 },
];

const metrics = { totals: {}, time_series: { by_dimension: { overall: rows } }, breakdowns: {} } as any;

describe('Overview chart drill-down', () => {
  describe('OverviewView: column → date', () => {
    it('resolves a clicked column to its bucket date, skipping gaps in the series', () => {
      const onSelectDay = jest.fn();
      render(<OverviewView metrics={metrics} filters={filters} loading={false} error={null} onSelectDay={onSelectDay} />);
      // Column 1 is 14 Jul — the second *row*, not the second day of the range.
      fireEvent.click(screen.getByTestId('col-1'));
      expect(onSelectDay).toHaveBeenCalledWith('2026-07-14');
    });

    it('resolves the last column', () => {
      const onSelectDay = jest.fn();
      render(<OverviewView metrics={metrics} filters={filters} loading={false} error={null} onSelectDay={onSelectDay} />);
      fireEvent.click(screen.getByTestId('col-2'));
      expect(onSelectDay).toHaveBeenCalledWith('2026-07-15');
    });

    // NOTE: the bucket is read in UTC (dayjs.utc), because the window is sent as UTC
    // day bounds — reading it locally would, in any zone behind UTC, name the day
    // before and filter to a window excluding the clicked column. That isn't asserted
    // here: local and UTC formatting are identical on a UTC runner, so the distinction
    // is invisible to CI, and setting process.env.TZ mid-run doesn't move Node's
    // cached zone. To exercise it, run this file with the zone set on the command:
    //   TZ=America/New_York npx jest OverviewDrilldown
    // which fails on a local-time mapping and passes on the UTC one.

    it('leaves the chart inert when no drill handler is supplied', () => {
      render(<OverviewView metrics={metrics} filters={filters} loading={false} error={null} />);
      expect(screen.getByTestId('chart')).toHaveAttribute('data-clickable', 'false');
    });
  });
});

export {};
