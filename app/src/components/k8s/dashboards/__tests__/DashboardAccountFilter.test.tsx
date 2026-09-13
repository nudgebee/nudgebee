import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { AccountOption, Dashboard, Panel } from '@api1/dashboards';

/*
 * One account filter at the top of the dashboard narrows every panel at once.
 * These tests are about which accounts each panel then queries, and what a
 * panel says when the filter names none of its own.
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
    Line: (props: { chartLabel: string[] }) => <div data-testid='line-chart'>{(props.chartLabel || []).join(' | ')}</div>,
    Bar: () => null,
  },
}));

/*
 * A test double for the dropdown: one button per option, toggling the value
 * the way the real one does (an array of options for multi-select, one option
 * for single). The dropdown's own behaviour is its own tests' business; these
 * tests are about what the dashboard does with a selection.
 */
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: (props: {
    id: string;
    multiple?: boolean;
    options: { label: string; value: string }[];
    value: any;
    onSelect: (e: any, v: any) => void;
  }) => (
    <div data-testid={props.id} data-selected={props.value == null ? '' : Array.isArray(props.value) ? props.value.join(',') : props.value.value}>
      {props.options.map((o) => (
        <button
          key={o.value}
          type='button'
          data-testid={`${props.id}-opt-${o.value}`}
          onClick={() => {
            if (!props.multiple) return props.onSelect({ target: { value: o.value } }, o);
            const current: { value: string }[] = Array.isArray(props.value)
              ? props.value.map((v: any) => (typeof v === 'string' ? { value: v } : v))
              : [];
            const next = current.some((v) => v.value === o.value) ? current.filter((v) => v.value !== o.value) : [...current, o];
            props.onSelect({ target: { value: next } }, next);
          }}
        >
          {o.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: () => <div data-testid='range-picker' />,
}));

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

const accounts = [
  { value: 'k8s-1', label: 'prod-eu', cloud_provider: 'K8S' },
  { value: 'k8s-2', label: 'prod-us', cloud_provider: 'K8S' },
  { value: 'k8s-3', label: 'dev', cloud_provider: 'K8S' },
  { value: 'aws-1', label: 'billing-aws', cloud_provider: 'AWS' },
  { value: 'aws-2', label: 'unused-aws', cloud_provider: 'AWS' },
] as AccountOption[];

const answer = {
  data: { data: { metrics_list: { results: [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1_700_000_000], values: [1] }] }] } } },
};

function panel(id: number, scope: Partial<Panel>): Panel {
  return {
    id,
    title: `Panel ${id}`,
    type: 'timeseries',
    datasource: 'metrics',
    grid_pos: { x: 0, y: (id - 1) * 4, w: 12, h: 4 },
    targets: [{ ref_id: 'A', expr: 'up' }],
    ...scope,
  } as Panel;
}

// Panel 1 spans every K8S account; panel 2 names one AWS account.
const dashboard = {
  id: 'dash-1',
  title: 'Fleet',
  description: '',
  definition: { panels: [panel(1, { account_type: 'K8S' }), panel(2, { account_ids: ['aws-1'] })] },
} as unknown as Dashboard;

describe('the dashboard account filter', () => {
  beforeEach(() => {
    metricsQuery.mockReset().mockResolvedValue(answer);
    (global as any).IntersectionObserver = SeenObserver;
    (global as any).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  });
  afterEach(cleanup);

  const mount = () => render(<DashboardView dashboard={dashboard} accounts={accounts} canEdit />);

  const filter = () => screen.getByTestId('dashboard-account-filter');
  const pick = (id: string, account: string) => fireEvent.click(screen.getByTestId(`${id}-opt-${account}`));

  it('offers only the accounts some panel queries, not every account the viewer has', async () => {
    mount();
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(4));
    for (const label of ['prod-eu', 'prod-us', 'dev', 'billing-aws']) {
      expect(within(filter()).getByText(label)).toBeInTheDocument();
    }
    // No panel is scoped to it, so narrowing to it could only blank the page.
    expect(within(filter()).queryByText('unused-aws')).not.toBeInTheDocument();
  });

  it('narrows every panel at once to the accounts picked', async () => {
    mount();
    // Unfiltered: the K8S panel fans out to three, the AWS panel to its one.
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(4));
    metricsQuery.mockClear();

    // Each pick refetches, so the calls are read after the last one.
    pick('dashboard-account-filter', 'k8s-1');
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    metricsQuery.mockClear();
    pick('dashboard-account-filter', 'k8s-3');

    // The K8S panel refetches for exactly the two picked, and nothing else does.
    await waitFor(() => expect(metricsQuery.mock.calls.map((c) => c[0].account_id).sort()).toEqual(['k8s-1', 'k8s-3']));
    const chart = await screen.findByTestId('line-chart');
    await waitFor(() => expect(chart.textContent).toContain('prod-eu · A | dev · A'));
    expect(chart.textContent).not.toContain('prod-us');
  });

  it('tells a panel the filter names none of its accounts, and the action clears the dashboard filter', async () => {
    mount();
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(4));

    pick('dashboard-account-filter', 'aws-1');

    // The K8S panel has no AWS account: a filter miss, not a broken query.
    const missed = await screen.findByTestId('panel-error-1');
    expect(within(missed).getByText('No accounts match the current filter.')).toBeInTheDocument();
    // The AWS panel is untouched.
    expect(screen.queryByTestId('panel-error-2')).not.toBeInTheDocument();

    metricsQuery.mockClear();
    fireEvent.click(within(missed).getByText('Show all accounts'));
    // The dashboard filter is what was cleared: the K8S panel fans out again.
    await waitFor(() => expect(metricsQuery.mock.calls.map((c) => c[0].account_id).sort()).toEqual(['k8s-1', 'k8s-2', 'k8s-3']));
    expect(screen.queryByTestId('panel-error-1')).not.toBeInTheDocument();
  });

  it("lets a panel's own picker narrow further, within the dashboard filter", async () => {
    mount();
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(4));

    pick('dashboard-account-filter', 'k8s-1');
    pick('dashboard-account-filter', 'k8s-2');
    await waitFor(() => expect(screen.getByTestId('line-chart').textContent).toBe('prod-eu · A | prod-us · A | All accounts'));

    // The panel's picker now offers only what the dashboard filter left it.
    metricsQuery.mockClear();
    const panelPicker = screen.getByTestId('panel-account-filter-1');
    expect(within(panelPicker).queryByText('dev')).not.toBeInTheDocument();
    pick('panel-account-filter-1', 'k8s-2');

    await waitFor(() => expect(metricsQuery.mock.calls.map((c) => c[0].account_id)).toEqual(['k8s-2']));
  });

  it("shows the dashboard's pick as the panel's own selection when it narrows the panel to one account", async () => {
    mount();
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(4));
    // Unfiltered, the K8S panel's picker is empty: it is showing all three.
    expect(screen.getByTestId('panel-account-filter-1').getAttribute('data-selected')).toBe('');

    pick('dashboard-account-filter', 'k8s-2');
    // The picker stays, and reads prod-us — the account this panel is now showing.
    await waitFor(() => expect(screen.getByTestId('panel-account-filter-1').getAttribute('data-selected')).toBe('k8s-2'));
    expect(within(screen.getByTestId('panel-account-filter-1')).getByText('prod-us')).toBeInTheDocument();
    // Two picked is not one selection a single-select picker can show.
    pick('dashboard-account-filter', 'k8s-3');
    await waitFor(() => expect(screen.getByTestId('line-chart').textContent).toContain('prod-us · A | dev · A'));
    expect(screen.getByTestId('panel-account-filter-1').getAttribute('data-selected')).toBe('');
  });

  it('names the account a panel is showing on the panel itself', async () => {
    mount();
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(4));
    // Unfiltered, the chip says what the panel is scoped to.
    expect(screen.getByTestId('panel-scope-1').textContent).toBe('All K8S');

    pick('dashboard-account-filter', 'k8s-2');
    await waitFor(() => expect(screen.getByTestId('panel-scope-1').textContent).toBe('prod-us'));
    // The AWS panel is unaffected by a K8S pick, and keeps its own name.
    expect(screen.getByTestId('panel-scope-2').textContent).toBe('billing-aws');

    // Two picked: both names. Three of three is the whole scope again.
    pick('dashboard-account-filter', 'k8s-3');
    await waitFor(() => expect(screen.getByTestId('panel-scope-1').textContent).toBe('prod-us, dev'));
    pick('dashboard-account-filter', 'k8s-1');
    await waitFor(() => expect(screen.getByTestId('panel-scope-1').textContent).toBe('All K8S'));
  });

  it('is not offered when the dashboard reaches only one account', async () => {
    render(
      <DashboardView
        dashboard={{ ...dashboard, definition: { panels: [panel(2, { account_ids: ['aws-1'] })] } } as unknown as Dashboard}
        accounts={accounts}
        canEdit
      />
    );
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId('dashboard-account-filter')).toBeNull();
  });
});
