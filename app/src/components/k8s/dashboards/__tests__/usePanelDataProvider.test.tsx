import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { usePanelData } from '../usePanelData';
import type { AccountOption, Panel } from '@api1/dashboards';

const metricsQuery = jest.fn((_request: Record<string, unknown>) => Promise.resolve({ data: { data: { metrics_list: { results: [] } } } }));

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: (request: Record<string, unknown>) => metricsQuery(request) },
}));
jest.mock('@api1/dashboards', () => ({ __esModule: true, default: {}, isCommandDatasource: () => false }));
// Admit the panel immediately; the render queue is not what these tests are about.
jest.mock('../panelQueue', () => ({ acquirePanelSlot: () => Promise.resolve(() => {}) }));

const account = (value: string, cloud_provider = 'K8S'): AccountOption => ({ label: value, value, cloud_provider });

const panelWith = (extra: Partial<Panel>): Panel =>
  ({
    id: 1,
    title: 'cpu',
    type: 'timeseries',
    datasource: 'metrics',
    grid_pos: { x: 0, y: 0, w: 6, h: 6 },
    targets: [{ ref_id: 'A', expr: 'sum(rate(x[5m]))' }],
    ...extra,
  } as Panel);

const render = (panel: Panel, accounts: AccountOption[]) =>
  renderHook(() =>
    usePanelData({ panel, accounts, accountFilter: [accounts[0].value], variables: {}, startTime: 1, endTime: 2, refreshKey: 'r', enabled: true })
  );

beforeEach(() => metricsQuery.mockClear());
afterEach(cleanup);

describe('usePanelData provider', () => {
  it('sends the panel’s declared provider, so every account answers in one query language', async () => {
    render(panelWith({ provider: 'prometheus', account_ids: ['a1'] }), [account('a1')]);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).toMatchObject({ account_id: 'a1', metric_provider: 'prometheus' });
    // Only CloudWatch resolves to a single source; every other provider is left
    // for the server to resolve per account.
    expect(metricsQuery.mock.calls[0][0]).not.toHaveProperty('metric_provider_source');
  });

  it('sends nothing when no provider is named, leaving each account on its own default', async () => {
    render(panelWith({ account_ids: ['a1'] }), [account('a1')]);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).not.toHaveProperty('metric_provider');
  });

  it('names CloudWatch with its source for an AWS account that declares nothing', async () => {
    // CloudWatch is never an account default, so it has to be named — and only
    // resolves as `user`, which the server would otherwise get wrong.
    render(panelWith({ account_ids: ['a1'] }), [account('a1', 'AWS')]);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).toMatchObject({ metric_provider: 'aws_cloudwatch', metric_provider_source: 'user' });
  });

  it('sends a pinned ES index in the slot the metrics contract gives it', async () => {
    // resolveESMetricsIndex reads the index out of `metric_name` — odd, but it is
    // the contract, and the same slot QueryMetrics uses.
    render(panelWith({ provider: 'ES', provider_index: 'metricbeat-*', account_ids: ['a1'] }), [account('a1')]);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).toMatchObject({ metric_provider: 'ES', request: { metric_name: 'metricbeat-*' } });
  });

  it('sends no index when the panel pins none, leaving each account on its configured one', async () => {
    render(panelWith({ provider: 'ES', account_ids: ['a1'] }), [account('a1')]);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).not.toHaveProperty('request');
  });

  it('never sends an index for a non-ES provider, which has nowhere to put one', async () => {
    render(panelWith({ provider: 'prometheus', provider_index: 'stale-*', account_ids: ['a1'] }), [account('a1')]);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).not.toHaveProperty('request');
  });

  it('lets a declared provider override the AWS default rather than silently redirecting', async () => {
    // An author who picked Prometheus meant it; sending CloudWatch instead would
    // run PromQL against a backend that cannot read it.
    render(panelWith({ provider: 'prometheus', account_ids: ['a1'] }), [account('a1', 'AWS')]);
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).toMatchObject({ metric_provider: 'prometheus' });
    expect(metricsQuery.mock.calls[0][0]).not.toHaveProperty('metric_provider_source');
  });
});
