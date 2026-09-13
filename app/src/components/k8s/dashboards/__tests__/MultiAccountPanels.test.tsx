import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { AccountOption, Dashboard, Panel } from '@api1/dashboards';
import { MAX_CHART_SERIES } from '../panelBounds';

/*
 * A stat panel scoped to several accounts used to query ONE of them and render
 * that account's number captioned with its name — which a viewer reads as the
 * total for all of them. It now queries every account and gives each its own
 * number. These tests are about what the card actually shows.
 */

const metricsQuery = jest.fn();
const fetchLogs = jest.fn();

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: (...args: unknown[]) => metricsQuery(...args), fetchLogs: (...args: unknown[]) => fetchLogs(...args) },
}));

jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: async () => ({ data: { data: null } }) } }));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { updateDashboard: jest.fn(), createDashboard: jest.fn() },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
}));

// Renders the series names so a test can see WHICH accounts reached the chart,
// and each line's points and dash so it can see what the total line is made of.
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    Line: (props: { chartLabel: string[]; dataset?: { label: string; data: unknown[]; borderDash?: number[] }[] }) => (
      <div>
        <div data-testid='line-chart'>{(props.chartLabel || []).join(' | ')}</div>
        {(props.dataset || []).map((d) => (
          <div key={d.label} data-testid='line-dataset' data-dashed={Boolean(d.borderDash)}>
            {d.label}={d.data.join(',')}
          </div>
        ))}
      </div>
    ),
    Bar: (props: { chartLabel: string[] }) => <div data-testid='bar-chart'>{(props.chartLabel || []).join(' | ')}</div>,
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

const RUNNERS: Record<string, number> = { 'acc-1': 90, 'acc-2': 0, 'acc-3': 10, 'acc-4': 12 };

const accounts = [
  { value: 'acc-1', label: 'prod-eu' },
  { value: 'acc-2', label: 'prod-us' },
  { value: 'acc-3', label: 'dev' },
  { value: 'acc-4', label: 'staging' },
] as AccountOption[];

/** Many series from one account, for the chart cap. Peaks ascend with the index. */
const seriesFor = (accountId: string, count: number) =>
  Array.from({ length: count }, (_, i) => ({
    metric: { pod: `${accountId}-pod-${i}` },
    timestamps: [1_700_000_000],
    values: [i + 1],
  }));

const wideAnswerFor = (accountId: string, count: number) => ({
  data: { data: { metrics_list: { results: [{ query_key: 'A', payload: seriesFor(accountId, count) }] } } },
});

/** An aggregate answers with one series carrying no labels — how a stat query is written. */
const answerFor = (accountId: string) => ({
  data: {
    data: {
      metrics_list: {
        results: [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1_700_000_000], values: [RUNNERS[accountId]] }] }],
      },
    },
  },
});

function statPanel(accountIds: string[], type: string, datasource: string): Panel {
  return {
    id: 1,
    title: 'Registered runners',
    type,
    datasource,
    account_ids: accountIds,
    grid_pos: { x: 0, y: 0, w: 4, h: 4 },
    targets: [{ ref_id: 'A', expr: 'sum(gha_registered_runners{__CLUSTER__})' }],
  } as Panel;
}

const dashboardWith = (accountIds: string[], type = 'stat', datasource = 'metrics') =>
  ({ id: 'dash-1', title: 'Fleet', description: '', definition: { panels: [statPanel(accountIds, type, datasource)] } } as unknown as Dashboard);

describe('a metrics panel spanning several accounts', () => {
  beforeEach(() => {
    metricsQuery.mockReset().mockImplementation((req: { account_id: string }) => Promise.resolve(answerFor(req.account_id)));
    (global as any).IntersectionObserver = SeenObserver;
    (global as any).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  });
  afterEach(cleanup);

  const mount = (accountIds: string[], type = 'stat', datasource = 'metrics') =>
    render(<DashboardView dashboard={dashboardWith(accountIds, type, datasource)} accounts={accounts} canEdit />);

  it('queries every scoped account when the viewer has picked none', async () => {
    mount(['acc-1', 'acc-2', 'acc-3', 'acc-4']);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(4));
    expect(metricsQuery.mock.calls.map((c) => c[0].account_id).sort()).toEqual(['acc-1', 'acc-2', 'acc-3', 'acc-4']);
  });

  it('uses the one account a single-account panel names, without waiting for a choice', async () => {
    mount(['acc-2']);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(1));
    expect(metricsQuery.mock.calls[0][0].account_id).toBe('acc-2');
  });

  it('adds the accounts up into one number', async () => {
    // 90 + 0 + 10 = 100. Before, the card showed 90 — one account's figure,
    // captioned with that account's name, which reads as the total.
    mount(['acc-1', 'acc-2', 'acc-3']);
    const card = await screen.findByTestId('panel-stat-1');
    await waitFor(() => expect(within(card).getByText('100')).toBeInTheDocument());
    expect(within(card).getByText('3 accounts')).toBeInTheDocument();
  });

  it('breaks the total down by account on hover', async () => {
    mount(['acc-1', 'acc-2', 'acc-3']);
    const card = await screen.findByTestId('panel-stat-1');
    await waitFor(() => expect(within(card).getByText('100')).toBeInTheDocument());

    fireEvent.mouseOver(within(card).getByText('100'));
    const tip = await screen.findByRole('tooltip');
    for (const [label, value] of [
      ['prod-eu', '90.00'],
      ['prod-us', '0.00'],
      ['dev', '10.00'],
    ]) {
      expect(within(tip).getByText(label)).toBeInTheDocument();
      expect(within(tip).getByText(value)).toBeInTheDocument();
    }
  });

  it('counts a real zero, and does not count an account that could not answer', async () => {
    // The sharp edge of a total: a failed account contributes nothing, so the
    // number is low for a reason the viewer has to be able to find.
    metricsQuery.mockImplementation((req: { account_id: string }) =>
      req.account_id === 'acc-3' ? Promise.reject(new Error('unreachable')) : Promise.resolve(answerFor(req.account_id))
    );
    mount(['acc-1', 'acc-2', 'acc-3']);
    const card = await screen.findByTestId('panel-stat-1');
    // 90 + 0, with dev missing — never 100, and never counted as a zero.
    await waitFor(() => expect(within(card).getByText(/^90/)).toBeInTheDocument());
    expect(within(card).queryByText('100')).not.toBeInTheDocument();
    // The caveat is the count under the number; the hover below names who is missing.
    expect(within(card).getByText('2 of 3 accounts')).toBeInTheDocument();
    expect(within(card).queryByText('*')).not.toBeInTheDocument();
    expect(within(card).queryByText(/No answer from/)).not.toBeInTheDocument();
    // And not in the banner above it either.
    expect(screen.queryByTestId('panel-warning-1')).not.toBeInTheDocument();

    fireEvent.mouseOver(within(card).getByText(/^90/));
    const tip = await screen.findByRole('tooltip');
    expect(within(tip).getByText('dev')).toBeInTheDocument();
    expect(within(tip).getByText('no answer')).toBeInTheDocument();
  });

  it('keeps the "No answer" banner on a chart, which has nowhere else to say it', async () => {
    metricsQuery.mockImplementation((req: { account_id: string }) =>
      req.account_id === 'acc-3' ? Promise.reject(new Error('unreachable')) : Promise.resolve(answerFor(req.account_id))
    );
    mount(['acc-1', 'acc-2', 'acc-3'], 'timeseries');
    expect(await screen.findByTestId('panel-warning-1')).toHaveTextContent('No answer from dev — showing the rest.');
  });

  it('leaves a single-account stat exactly as it was — one number, no breakdown', async () => {
    mount(['acc-2']);
    const card = await screen.findByTestId('panel-stat-1');
    expect(within(card).getByText('0.00')).toBeInTheDocument();
    expect(within(card).queryByText(/accounts$/)).not.toBeInTheDocument();
  });

  it("merges every account into one chart, keeping each account's busiest series", async () => {
    // 177 / 200 / 350 series. Ranked in one pool the biggest account would take
    // every slot; split evenly, all three reach the chart.
    const counts: Record<string, number> = { 'acc-1': 177, 'acc-2': 200, 'acc-3': 350 };
    metricsQuery.mockImplementation((req: { account_id: string }) => Promise.resolve(wideAnswerFor(req.account_id, counts[req.account_id])));

    mount(['acc-1', 'acc-2', 'acc-3'], 'timeseries');
    const chart = await screen.findByTestId('line-chart');
    await waitFor(() => expect(chart.textContent).toContain('prod-eu'));

    const drawn = chart.textContent!.split(' | ');
    const share = Math.floor(MAX_CHART_SERIES / 3);
    expect(drawn).toHaveLength(share * 3);
    for (const label of ['prod-eu', 'prod-us', 'dev']) {
      expect(drawn.filter((d) => d.startsWith(`${label} ·`))).toHaveLength(share);
    }
    // The cap is a fact the viewer is told, not data that silently vanished.
    expect(await screen.findByText(/6 of 177 from prod-eu, 6 of 200 from prod-us and 6 of 350 from dev/)).toBeInTheDocument();
  });

  it('draws every account and, alongside them, the line that adds them up', async () => {
    // Each account's trend is its own line; the consolidated view is a dashed
    // line on the same axis, not a replacement for the parts.
    mount(['acc-1', 'acc-2', 'acc-3'], 'timeseries');
    const chart = await screen.findByTestId('line-chart');
    await waitFor(() => expect(chart.textContent).toContain('All accounts'));
    expect(chart.textContent!.split(' | ')).toEqual(['prod-eu · A', 'prod-us · A', 'dev · A', 'All accounts']);

    const lines = screen.getAllByTestId('line-dataset');
    const total = lines.find((l) => l.textContent!.startsWith('All accounts='))!;
    // 90 + 0 + 10, and only the total is dashed.
    expect(total.textContent).toBe('All accounts=100');
    expect(total.getAttribute('data-dashed')).toBe('true');
    expect(lines.filter((l) => l.getAttribute('data-dashed') === 'true')).toHaveLength(1);
  });

  it('draws a single-account chart with no total line', async () => {
    mount(['acc-1'], 'timeseries');
    const chart = await screen.findByTestId('line-chart');
    await waitFor(() => expect(chart.textContent).toBe('A'));
    expect(screen.getAllByTestId('line-dataset')).toHaveLength(1);
  });

  it('stacks the accounts on a bar chart rather than adding a total bar', async () => {
    // The bar chart is stacked, so the stack's height already IS the
    // consolidated view; a total series would double it.
    mount(['acc-1', 'acc-2', 'acc-3'], 'bar');
    const chart = await screen.findByTestId('bar-chart');
    await waitFor(() => expect(chart.textContent).toContain('prod-eu'));
    expect(chart.textContent!.split(' | ')).toEqual(['prod-eu · A', 'prod-us · A', 'dev · A']);
  });

  it('gives a multi-account table its own Account column', async () => {
    mount(['acc-1', 'acc-3'], 'table');
    const table = await screen.findByTestId('dashboard-panel-1');
    // Waits on a ROW: the panel's own Account picker already says "Account"
    // before any data lands, so the header alone is not proof the table drew.
    await waitFor(() => expect(within(table).getByText('prod-eu')).toBeInTheDocument());
    expect(within(table).getAllByText('Account').length).toBeGreaterThan(0);
    // The account is a cell, and the Series cell is the metric alone.
    for (const label of ['prod-eu', 'dev']) expect(within(table).getByText(label)).toBeInTheDocument();
    expect(within(table).getAllByText('A')).toHaveLength(2);
    expect(within(table).queryByText(/prod-eu ·/)).not.toBeInTheDocument();
  });

  it('keeps a single-account table to Series and Latest', async () => {
    mount(['acc-1'], 'table');
    const table = await screen.findByTestId('dashboard-panel-1');
    await waitFor(() => expect(within(table).getByText('Series')).toBeInTheDocument());
    expect(within(table).queryByText('Account')).not.toBeInTheDocument();
  });

  it('does not fan out a datasource other than metrics', async () => {
    // Scoped to this datasource deliberately: logs and the command panels merge
    // per-account tables of their own, and the query engine takes a list in one
    // call. Only metrics fans out and adds up.
    mount(['acc-1', 'acc-2', 'acc-3', 'acc-4'], 'timeseries', 'logs');
    await waitFor(() => expect(fetchLogs).toHaveBeenCalled());
    expect(fetchLogs).toHaveBeenCalledTimes(1);
    expect(metricsQuery).not.toHaveBeenCalled();
  });
});
