import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import DashboardPanel from '../DashboardPanel';
import type { AccountOption, Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

/*
 * A viewer narrows a traces panel by more than its account: "+ Filter" lists
 * the columns, a column becomes a dropdown of the values it holds, and what is
 * picked is AND-ed onto the panel's own query. These tests are about what
 * reaches the traces service.
 */

const traceV2 = jest.fn();
const traceLabelValues = jest.fn();

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@api1/kubernetes/trace', () => ({
  __esModule: true,
  default: {
    traceV2: (...args: unknown[]) => traceV2(...args),
    traceGroupV2: jest.fn(async () => ({ traces_grouping_v3: [] })),
    traceLabelValues: (...args: unknown[]) => traceLabelValues(...args),
  },
}));

jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));

// The real menu is a MUI popover; its behaviour is its own tests' business.
jest.mock('@ui/DropdownMenu', () => ({
  __esModule: true,
  DropdownMenu: (props: { trigger: React.ReactElement; items: { label: string; onSelect: () => void }[] }) => (
    <div data-testid='dropdown-menu'>
      {props.trigger}
      {props.items.map((item) => (
        <button key={item.label} type='button' data-testid={`menu-item-${item.label}`} onClick={item.onSelect}>
          {item.label}
        </button>
      ))}
    </div>
  ),
}));

/*
 * The dropdown cannot be driven through role queries (collapsed groups, MUI's
 * aria-hidden), so it is doubled: a trigger that fires onOpen the way the real
 * one does, and one button per option toggling the multi-select value.
 */
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: (props: {
    id: string;
    options: { value: string }[];
    value: string[];
    onOpen?: () => void;
    onSelect: (e: unknown, v: unknown) => void;
  }) => (
    <div data-testid={props.id} data-selected={(props.value || []).join(',')}>
      <button type='button' data-testid={`${props.id}-trigger`} onClick={() => props.onOpen?.()} />
      {props.options.map((o) => (
        <button
          key={o.value}
          type='button'
          data-testid={`${props.id}-opt-${o.value}`}
          onClick={() => {
            const current = props.value || [];
            props.onSelect(null, current.includes(o.value) ? current.filter((v) => v !== o.value) : [...current, o.value]);
          }}
        />
      ))}
    </div>
  ),
}));

const accounts = [{ value: 'acc-1', label: 'prod-eu', cloud_provider: 'K8S' }] as AccountOption[];

function tracesPanel(filters: { column: string; operator: string; value: string }[] = []): Panel {
  return {
    id: 1,
    title: 'Slow spans',
    type: 'table',
    datasource: 'traces',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', query: buildEntityQuery({ ...defaultDraft('traces_v2'), filters }) as never }],
  } as Panel;
}

const span = { timestamp: '2026-10-01 10:00:00.000', span_name: 'GET /orders', workload_name: 'api', duration_ns: 1000 };

const renderPanel = (panel: Panel) =>
  render(<DashboardPanel panel={panel} accounts={accounts} variables={{}} startTime={1000} endTime={2000} forceLoad />);

// The column menu only — the panel's own ⋮ menu is a DropdownMenu too.
const filterMenuItems = () =>
  [
    ...(screen.getByTestId('panel-filter-add-1').closest('[data-testid="dropdown-menu"]') as HTMLElement).querySelectorAll(
      '[data-testid^="menu-item-"]'
    ),
  ].map((b) => b.textContent);

const lastWhere = () => traceV2.mock.calls[traceV2.mock.calls.length - 1][0].where;

describe('panel column filters', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    traceV2.mockResolvedValue({ traces_list: [span] });
    traceLabelValues.mockResolvedValue(['web', 'api']);
  });

  it('narrows the panel to the values a viewer picks for a column', async () => {
    renderPanel(tracesPanel());
    await waitFor(() => expect(traceV2).toHaveBeenCalledTimes(1));
    expect(lastWhere()).toEqual({});

    fireEvent.click(screen.getByTestId('menu-item-Workload'));
    // The dropdown opens as it appears, and opening it lists the column's values
    // for the panel's account and window.
    await waitFor(() => expect(traceLabelValues).toHaveBeenCalledWith('acc-1', 'workload_name', 1000, 2000, 500));
    // Adding a column with nothing picked asks nothing new of the store.
    expect(traceV2).toHaveBeenCalledTimes(1);

    fireEvent.click(await screen.findByTestId('panel-filter-1-workload_name-opt-api'));
    await waitFor(() => expect(traceV2).toHaveBeenCalledTimes(2));
    expect(lastWhere()).toEqual({ workload_name: { _in: ['api'] } });

    // The column has a filter now, so the menu no longer offers it.
    expect(screen.queryByTestId('menu-item-Workload')).toBeNull();

    fireEvent.click(screen.getByTestId('panel-filters-clear-1'));
    await waitFor(() => expect(traceV2).toHaveBeenCalledTimes(3));
    expect(lastWhere()).toEqual({});
    expect(screen.queryByTestId('panel-filters-1')).toBeNull();
  });

  it('keeps the author’s filters and ANDs the viewer’s onto them', async () => {
    renderPanel(tracesPanel([{ column: 'span_name', operator: '_neq', value: 'GET /health' }]));
    await waitFor(() => expect(traceV2).toHaveBeenCalledTimes(1));

    traceLabelValues.mockResolvedValue(['prod']);
    fireEvent.click(screen.getByTestId('menu-item-Namespace'));
    fireEvent.click(await screen.findByTestId('panel-filter-1-workload_namespace-opt-prod'));

    await waitFor(() => expect(traceV2).toHaveBeenCalledTimes(2));
    expect(lastWhere()).toEqual({ span_name: { _neq: 'GET /health' }, workload_namespace: { _in: ['prod'] } });
  });

  it('lists a column\u2019s values once per account and window, not on every open', async () => {
    renderPanel(tracesPanel());
    await waitFor(() => expect(traceV2).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByTestId('menu-item-Workload'));
    await screen.findByTestId('panel-filter-1-workload_name-opt-api');

    fireEvent.click(screen.getByTestId('panel-filter-1-workload_name-trigger'));
    expect(traceLabelValues).toHaveBeenCalledTimes(1);
  });

  it('says the filters emptied the panel, and offers to clear them', async () => {
    renderPanel(tracesPanel());
    await waitFor(() => expect(traceV2).toHaveBeenCalledTimes(1));

    traceV2.mockResolvedValue({ traces_list: [] });
    fireEvent.click(screen.getByTestId('menu-item-Workload'));
    fireEvent.click(await screen.findByTestId('panel-filter-1-workload_name-opt-web'));

    expect(await screen.findByText('No rows match these filters')).toBeInTheDocument();
    traceV2.mockResolvedValue({ traces_list: [span] });
    fireEvent.click(screen.getByTestId('panel-state-action'));
    await waitFor(() => expect(lastWhere()).toEqual({}));
  });

  it('offers only the columns the author chose, in their order', () => {
    renderPanel({ ...tracesPanel(), options: { filter_columns: ['workload_namespace', 'workload_name'] } } as Panel);
    expect(filterMenuItems()).toEqual(['Namespace', 'Workload']);
  });

  it('offers every column when none of the chosen ones is on the table any more', () => {
    // Moved onto another table by an import or a JSON edit: a stale choice must not take the menu away.
    renderPanel({ ...tracesPanel(), options: { filter_columns: ['p99_latency', 'no_such_column'] } } as Panel);
    expect(filterMenuItems()).toEqual(expect.arrayContaining(['Workload', 'Namespace', 'Span name']));
  });

  it('offers no column filter on a metrics panel', () => {
    const metrics = { ...tracesPanel(), datasource: 'metrics', type: 'timeseries', targets: [{ ref_id: 'A', expr: 'up' }] } as Panel;
    render(<DashboardPanel panel={metrics} accounts={accounts} variables={{}} startTime={0} endTime={1} sampleData={{ labels: [], series: [] }} />);
    expect(screen.queryByTestId('panel-filter-add-1')).toBeNull();
  });
});
