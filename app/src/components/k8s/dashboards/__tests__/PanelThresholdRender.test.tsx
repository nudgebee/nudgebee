import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { AccountOption, Dashboard, Panel, PanelThresholdStep } from '@api1/dashboards';

/*
 * A stat or gauge panel carrying thresholds colours its FRAME when the number it
 * shows crosses one — the border, the title band, and a badge naming the step.
 * These tests are about what the card looks like from outside, which is the only
 * thing the feature is for.
 */

const metricsQuery = jest.fn();

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: (...args: unknown[]) => metricsQuery(...args), fetchLogs: jest.fn() },
}));

jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: async () => ({ data: { data: null } }) } }));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { updateDashboard: jest.fn(), createDashboard: jest.fn() },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
}));

jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    Line: () => <div data-testid='line-chart' />,
    Bar: () => <div data-testid='bar-chart' />,
  },
}));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: () => <div data-testid='range-picker' />,
}));

// Panels fetch when scrolled into view; jsdom has no layout, so every panel is "seen".
class SeenObserver {
  private cb: IntersectionObserverCallback;
  constructor(cb: IntersectionObserverCallback) {
    this.cb = cb;
  }
  observe(target: Element) {
    this.cb([{ isIntersecting: true, target } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
  }
  unobserve() {}
  disconnect() {}
}

const accounts = [{ value: 'acc-1', label: 'prod-eu' }] as AccountOption[];

/** An aggregate answers with one unlabelled series — how a stat query is written. */
const answerWith = (value: number) => ({
  data: { data: { metrics_list: { results: [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1_700_000_000], values: [value] }] }] } } },
});

const steps = (...values: [number, string][]): PanelThresholdStep[] => values.map(([value, color]) => ({ value, color } as PanelThresholdStep));

function panelWith(thresholds: PanelThresholdStep[] | undefined, type = 'stat'): Panel {
  return {
    id: 1,
    title: 'CPU used',
    type,
    datasource: 'metrics',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 4, h: 4 },
    targets: [{ ref_id: 'A', expr: 'sum(cpu)' }],
    ...(thresholds ? { options: { thresholds } } : {}),
  } as unknown as Panel;
}

const dashboardWith = (thresholds: PanelThresholdStep[] | undefined, type = 'stat') =>
  ({ id: 'dash-1', title: 'Fleet', description: '', definition: { panels: [panelWith(thresholds, type)] } } as unknown as Dashboard);

describe('a panel with thresholds', () => {
  beforeEach(() => {
    metricsQuery.mockReset().mockResolvedValue(answerWith(90));
    (global as any).IntersectionObserver = SeenObserver;
    (global as any).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  });
  afterEach(cleanup);

  const mount = (thresholds: PanelThresholdStep[] | undefined, type = 'stat') =>
    render(<DashboardView dashboard={dashboardWith(thresholds, type)} accounts={accounts} canEdit />);

  const panel = async () => {
    await screen.findByTestId('panel-stat-1');
    return screen.getByTestId('dashboard-panel-1');
  };

  it('marks the frame with the crossed step and names it in the header', async () => {
    mount(steps([80, 'red']));
    const card = await panel();
    await waitFor(() => expect(card).toHaveAttribute('data-threshold', 'red'));
    expect(within(card).getByTestId('panel-threshold-1')).toHaveTextContent('≥ 80');
  });

  it('says which number crossed which line, on hover', async () => {
    mount(steps([80, 'red']));
    const card = await panel();
    await waitFor(() => expect(card).toHaveAttribute('data-threshold', 'red'));

    fireEvent.mouseOver(within(card).getByTestId('panel-threshold-1'));
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('90.00 is at or above the 80 threshold.');
  });

  it('leaves the panel plain below every step', async () => {
    mount(steps([95, 'red']));
    const card = await panel();
    await waitFor(() => expect(within(card).getByText(/^90/)).toBeInTheDocument());
    expect(card).not.toHaveAttribute('data-threshold');
    expect(screen.queryByTestId('panel-threshold-1')).not.toBeInTheDocument();
  });

  it('takes the highest step crossed', async () => {
    mount(steps([70, 'amber'], [90, 'red']));
    const card = await panel();
    await waitFor(() => expect(card).toHaveAttribute('data-threshold', 'red'));
    expect(within(card).getByTestId('panel-threshold-1')).toHaveTextContent('≥ 90');
  });

  it('draws a panel with no thresholds exactly as it always did', async () => {
    mount(undefined);
    const card = await panel();
    await waitFor(() => expect(within(card).getByText(/^90/)).toBeInTheDocument());
    expect(card).not.toHaveAttribute('data-threshold');
  });

  /*
   * The editor drops thresholds when the visualisation changes, but an imported
   * panel can still carry them onto a chart — which has a value per series, and
   * so the tint is about a LINE, and the badge says which.
   */
  it('judges a chart line by line and names the line that crossed', async () => {
    // A chart judges only lines that are current, so the sample is from now, not a fixed date.
    const now = Math.floor(Date.now() / 1000);
    metricsQuery.mockResolvedValue({
      data: { data: { metrics_list: { results: [{ query_key: 'A', payload: [{ metric: {}, timestamps: [now - 30, now], values: [85, 90] }] }] } } },
    });
    mount(steps([80, 'red']), 'timeseries');
    await screen.findByTestId('line-chart');
    const card = screen.getByTestId('dashboard-panel-1');
    await waitFor(() => expect(card).toHaveAttribute('data-threshold', 'red'));
    expect(within(card).getByTestId('panel-threshold-1')).toHaveTextContent('≥ 80');
  });

  it('leaves a chart plain when no line crosses', async () => {
    const now = Math.floor(Date.now() / 1000);
    metricsQuery.mockResolvedValue({
      data: { data: { metrics_list: { results: [{ query_key: 'A', payload: [{ metric: {}, timestamps: [now - 30, now], values: [85, 90] }] }] } } },
    });
    mount(steps([95, 'red']), 'timeseries');
    await screen.findByTestId('line-chart');
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(screen.getByTestId('dashboard-panel-1')).not.toHaveAttribute('data-threshold');
    expect(screen.queryByTestId('panel-threshold-1')).not.toBeInTheDocument();
  });
});
