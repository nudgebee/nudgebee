import type { Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';
import { loadViewerFilterValues, narrowTraceWhere, viewerFilterColumns, withViewerFilters, VIEWER_FILTER_VALUE_LIMIT } from '../panelViewerFilters';

jest.mock('@api1/kubernetes/trace', () => ({
  __esModule: true,
  default: { traceLabelValues: jest.fn(async () => ['web', 'api', '', 'api']) },
}));

jest.mock('@api1/dashboards', () => ({
  ...jest.requireActual('@api1/dashboards'),
  __esModule: true,
  default: { executeEntityQuery: jest.fn(async () => ({ data: { columns: ['cluster'], rows: [['b'], ['a'], ['b'], ['']] } })) },
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const apiTrace = require('@api1/kubernetes/trace').default;
// eslint-disable-next-line @typescript-eslint/no-var-requires
const apiDashboards = require('@api1/dashboards').default;

function panelOn(datasource: string, table?: string, extra: Partial<Panel> = {}): Panel {
  return {
    id: 1,
    title: 'p',
    type: 'table',
    datasource,
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [table ? { ref_id: 'A', query: buildEntityQuery(defaultDraft(table)) as never } : { ref_id: 'A', expr: 'up' }],
    ...extra,
  } as Panel;
}

describe('viewerFilterColumns', () => {
  it('offers the text columns a traces panel can filter on', () => {
    const names = viewerFilterColumns(panelOn('traces', 'traces_v2')).map((c) => c.name);
    expect(names).toEqual(expect.arrayContaining(['workload_name', 'workload_namespace', 'span_name', 'resource', 'http_status_code']));
    // A duration is a range question, which a list of values cannot ask.
    expect(names).not.toContain('duration_ns');
  });

  it('offers a grouping table its filter-only columns but never an aggregate', () => {
    const names = viewerFilterColumns(panelOn('traces', 'traces_groupings_v2')).map((c) => c.name);
    expect(names).toEqual(expect.arrayContaining(['workload_name', 'status_code', 'trace_source']));
    expect(names).not.toContain('count');
    expect(names).not.toContain('p99_latency');
  });

  it('offers a nudgebee grouping table no aggregate either', () => {
    const columns = viewerFilterColumns(panelOn('nudgebee', 'event_groupings_v2'));
    expect(columns.length).toBeGreaterThan(0);
    expect(columns.every((c) => c.type === 'string' && !c.aggregate)).toBe(true);
  });

  it('offers nothing on a datasource whose query is free-form', () => {
    expect(viewerFilterColumns(panelOn('metrics'))).toEqual([]);
    expect(viewerFilterColumns(panelOn('logs'))).toEqual([]);
  });
});

describe('narrowTraceWhere', () => {
  it('adds a filter as "is one of"', () => {
    const where: Record<string, Record<string, unknown>> = { span_name: { _neq: 'GET /health' } };
    expect(narrowTraceWhere(where, [{ column: 'workload_name', values: ['api', 'web'] }])).toBe(true);
    expect(where).toEqual({ span_name: { _neq: 'GET /health' }, workload_name: { _in: ['api', 'web'] } });
  });

  it('intersects with the author’s own list rather than replacing it', () => {
    // Replacing it would let a viewer widen the panel past what it was built to show.
    const where: Record<string, Record<string, unknown>> = { workload_name: { _in: ['api', 'web'] } };
    expect(narrowTraceWhere(where, [{ column: 'workload_name', values: ['web', 'worker'] }])).toBe(true);
    expect(where.workload_name).toEqual({ _in: ['web'] });
  });

  it('reports nothing can match instead of sending an empty list', () => {
    // `_in: []` folds to true on ClickHouse — every row, the opposite of what it means.
    const where: Record<string, Record<string, unknown>> = { workload_name: { _in: ['api'] } };
    expect(narrowTraceWhere(where, [{ column: 'workload_name', values: ['web'] }])).toBe(false);
  });

  it('ignores a filter with nothing picked', () => {
    const where: Record<string, Record<string, unknown>> = {};
    expect(narrowTraceWhere(where, [{ column: 'workload_name', values: [] }])).toBe(true);
    expect(where).toEqual({});
  });
});

describe('withViewerFilters', () => {
  it('ANDs each filter onto the author’s where clause', () => {
    const query = { table: 'events_v2', where: { _and: [{ _binary: { cluster: { _eq: 'a' } } }] } };
    expect(withViewerFilters(query, [{ column: 'namespace', values: ['prod'] }]).where).toEqual({
      _and: [{ _binary: { cluster: { _eq: 'a' } } }, { _binary: { namespace: { _in: ['prod'] } } }],
    });
  });

  it('keeps a where clause of another shape whole', () => {
    const query = { table: 'events_v2', where: { _binary: { cluster: { _eq: 'a' } } } };
    expect(withViewerFilters(query, [{ column: 'namespace', values: ['prod'] }]).where).toEqual({
      _and: [{ _binary: { cluster: { _eq: 'a' } } }, { _binary: { namespace: { _in: ['prod'] } } }],
    });
  });

  it('returns the query untouched when nothing is applied', () => {
    const query = { table: 'events_v2' };
    expect(withViewerFilters(query, [{ column: 'namespace', values: [] }])).toBe(query);
  });
});

describe('loadViewerFilterValues', () => {
  beforeEach(() => jest.clearAllMocks());

  it('asks the traces service for the column on the panel’s account and window', async () => {
    const values = await loadViewerFilterValues({
      panel: panelOn('traces', 'traces_v2'),
      column: 'workload_name',
      accountIds: ['acc-1'],
      variables: {},
      startTime: 10,
      endTime: 20,
    });
    expect(apiTrace.traceLabelValues).toHaveBeenCalledWith('acc-1', 'workload_name', 10, 20, VIEWER_FILTER_VALUE_LIMIT);
    expect(values).toEqual(['api', 'web']);
  });

  it('asks the engine for the one column under the panel’s own where, newest rows first on a row table', async () => {
    const draft = { ...defaultDraft('events_v2'), filters: [{ column: 'cluster', operator: '_eq', value: '$cluster' }] };
    const panel = { ...panelOn('nudgebee'), account_ids: ['acc-1', 'acc-2'] } as Panel;
    panel.targets = [{ ref_id: 'A', query: buildEntityQuery(draft) as never, time_column: 'created_at' }];

    const values = await loadViewerFilterValues({
      panel,
      column: 'cluster',
      accountIds: ['acc-1', 'acc-2'],
      variables: { cluster: 'k8s-dev' },
      startTime: 1,
      endTime: 2,
    });

    const request = apiDashboards.executeEntityQuery.mock.calls[0][0];
    expect(request.account_ids).toEqual(['acc-1', 'acc-2']);
    expect(request.query.columns).toEqual([{ name: 'cluster' }]);
    // The panel's variables are substituted, as they are when the panel itself runs.
    expect(request.query.where).toEqual({ _and: [{ _binary: { cluster: { _eq: 'k8s-dev' } } }] });
    expect(request.query.order_by).toEqual([{ column: defaultDraft('events_v2').sortColumn, order: 'desc' }]);
    expect(request.query.limit).toBe(VIEWER_FILTER_VALUE_LIMIT);
    expect(request).toMatchObject({ time_column: 'created_at', start_time: 1, end_time: 2 });
    expect(values).toEqual(['a', 'b']);
  });

  it('sorts numbers by value, not as text', async () => {
    apiTrace.traceLabelValues.mockResolvedValueOnce(['1000', '200', '503', '404']);
    const values = await loadViewerFilterValues({
      panel: panelOn('traces', 'traces_v2'),
      column: 'http_status_code',
      accountIds: ['acc-1'],
      variables: {},
      startTime: 1,
      endTime: 2,
    });
    expect(values).toEqual(['200', '404', '503', '1000']);
  });

  it('orders a grouping table by the column itself', async () => {
    await loadViewerFilterValues({
      panel: panelOn('nudgebee', 'event_groupings_v2'),
      column: 'cluster',
      accountIds: ['acc-1'],
      variables: {},
      startTime: 1,
      endTime: 2,
    });
    expect(apiDashboards.executeEntityQuery.mock.calls[0][0].query.order_by).toEqual([{ column: 'cluster', order: 'asc' }]);
  });
});
