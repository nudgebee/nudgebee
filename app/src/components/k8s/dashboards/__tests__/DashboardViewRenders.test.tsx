import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { AccountOption, Dashboard, Panel } from '@api1/dashboards';

/*
 * Two things a dashboard with a hundred panels cannot afford, both of which it
 * did: pressing Edit remounted every panel (and a remounted panel runs its
 * query again), and opening the panel editor re-rendered every panel (and a
 * re-rendered panel hands Chart.js fresh arrays, which is a full chart update
 * and legend rebuild). The chart is a render counter here, so both show up as
 * numbers.
 */

const metricsQuery = jest.fn();
const chartRenders = jest.fn();

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: (...args: unknown[]) => metricsQuery(...args), fetchLogs: jest.fn() },
}));

// The editor's provider row asks each account for its default provider.
jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: async () => ({ data: { data: null } }) } }));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { updateDashboard: jest.fn(), createDashboard: jest.fn() },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
}));

// Counts renders of the charts on the GRID. The editor's preview draws its own
// chart, inside the dialog; that one is meant to render.
jest.mock('@ui/Chart', () => {
  const R = jest.requireActual('react');
  return {
    __esModule: true,
    default: {
      Line: (props: { chartLabel: string[] }) => {
        const ref = R.useRef(null) as React.RefObject<HTMLDivElement>;
        R.useEffect(() => {
          if (!ref.current?.closest('[role="dialog"]')) chartRenders(props.chartLabel);
        });
        return <div ref={ref} data-testid='line-chart' />;
      },
      Bar: () => null,
    },
  };
});

// The range picker pulls in a date library tree the test does not need.
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

const accounts = [{ value: 'acc-1', label: 'Account 1' }] as AccountOption[];

function panel(id: number): Panel {
  return {
    id,
    title: `cpu ${id}`,
    type: 'timeseries',
    datasource: 'metrics',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', expr: `sum(rate(cpu_${id}[5m]))` }],
  } as Panel;
}

const dashboard = {
  id: 'dash-1',
  title: 'Fleet',
  description: '',
  definition: { panels: [panel(1), panel(2), panel(3)] },
} as unknown as Dashboard;

const answer = {
  data: { data: { metrics_list: { results: [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1_700_000_000], values: [1] }] }] } } },
};

describe('DashboardView keeps its panels still', () => {
  beforeEach(() => {
    metricsQuery.mockReset().mockResolvedValue(answer);
    chartRenders.mockReset();
    (global as any).IntersectionObserver = SeenObserver;
    (global as any).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  });
  afterEach(cleanup);

  const mount = () => render(<DashboardView dashboard={dashboard} accounts={accounts} canEdit />);

  it('does not run every panel query again when Edit is pressed', async () => {
    mount();
    await waitFor(() => expect(screen.getAllByTestId('line-chart')).toHaveLength(3));
    expect(metricsQuery).toHaveBeenCalledTimes(3);

    // Before: view and edit mode rendered different element types at the same
    // key, and the grid sat under a DndContext only in edit mode — so Edit
    // unmounted every panel and the remount ran every query again.
    await act(async () => {
      fireEvent.click(screen.getByTestId('dashboard-edit-btn'));
    });
    expect(screen.getAllByTestId('drag-panel-1')).toHaveLength(1);
    expect(screen.getAllByTestId('line-chart')).toHaveLength(3);
    expect(metricsQuery).toHaveBeenCalledTimes(3);
  });

  it('does not re-render every chart when the panel editor opens and closes', async () => {
    mount();
    await waitFor(() => expect(screen.getAllByTestId('line-chart')).toHaveLength(3));
    await act(async () => {
      fireEvent.click(screen.getByTestId('dashboard-edit-btn'));
    });
    const drawn = chartRenders.mock.calls.length;

    // Before: the editor's open state lived on the view, the view re-rendered
    // every unmemoised panel, and each panel handed Chart.js new arrays — a
    // full update of every chart on the page, twice per open/close.
    await act(async () => {
      fireEvent.click(screen.getByTestId('dashboard-panel-2'));
    });
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument());
    await act(async () => {
      fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());

    expect(chartRenders.mock.calls.length).toBe(drawn);
    // The grid's three, plus the preview's own — the preview is the one thing
    // that is supposed to run a query when the editor opens.
    expect(metricsQuery).toHaveBeenCalledTimes(4);
  });
});
