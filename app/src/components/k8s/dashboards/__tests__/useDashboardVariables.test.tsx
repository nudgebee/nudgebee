import { act, renderHook, waitFor } from '@testing-library/react';
import observability from '@api1/observability';
import apiDashboards from '@api1/dashboards';
import { useDashboardVariables } from '../dashboardVariables';

jest.mock('@api1/observability', () => ({ __esModule: true, default: { metricsQuery: jest.fn() } }));
jest.mock('@api1/dashboards', () => ({ __esModule: true, ...jest.requireActual('@api1/dashboards'), default: { executeEntityQuery: jest.fn() } }));

const metricsQuery = observability.metricsQuery as jest.Mock;
const executeEntityQuery = apiDashboards.executeEntityQuery as jest.Mock;
const answer = (...names: string[]) => ({
  data: { data: { metrics_list: { results: [{ query_key: 'variable', payload: names.map((name) => ({ metric: { name } })) }] } } },
});
const account = { value: 'a', label: 'A' } as any;
const namespace = { name: 'ns', query: 'up', label_key: 'name' };
const scope = (over: object = {}) => ({ variables: [namespace], accounts: [account], panels: [], startTime: 1, endTime: 2, ...over } as any);

describe('useDashboardVariables', () => {
  beforeEach(() => {
    metricsQuery.mockReset().mockResolvedValue(answer('x', 'y'));
    executeEntityQuery.mockReset();
  });

  it('lists the values, and says it is loading until they arrive', async () => {
    const { result } = renderHook(() => useDashboardVariables(scope()));
    expect(result.current.loading.ns).toBe(true);
    await waitFor(() => expect(result.current.options.ns).toEqual(['x', 'y']));
    expect(result.current.loading.ns).toBe(false);
    expect(result.current.errors.ns).toBe('');
  });

  it.each([
    ['a failed request', () => metricsQuery.mockRejectedValue(new Error('unreachable')), 'unreachable'],
    ['a GraphQL error', () => metricsQuery.mockResolvedValue({ data: { errors: [{ message: 'denied' }] } }), 'denied'],
    [
      'a query that failed',
      () => metricsQuery.mockResolvedValue({ data: { data: { metrics_list: { results: [{ query_key: 'variable', error: 'bad query' }] } } } }),
      'bad query',
    ],
  ])('reports %s rather than an empty list', async (_name, arrange, message) => {
    arrange();
    const { result } = renderHook(() => useDashboardVariables(scope()));
    await waitFor(() => expect(result.current.errors.ns).toBe(message));
    expect(result.current.options.ns).toEqual([]);
  });

  it('does not call a query that matched nothing a failure', async () => {
    metricsQuery.mockResolvedValue(answer());
    const { result } = renderHook(() => useDashboardVariables(scope()));
    await waitFor(() => expect(result.current.loading.ns).toBe(false));
    expect(result.current.errors.ns).toBe('');
  });

  it('fills another variable into its query and reloads when that variable changes', async () => {
    const pod = { name: 'pod', query: 'kube_pod_info{namespace=~"$ns"}', label_key: 'name' };
    const variables = [{ name: 'ns', type: 'custom', values: ['a'] }, pod];
    const { result } = renderHook(() => useDashboardVariables(scope({ variables })));
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0].queries.variable).toBe('kube_pod_info{namespace=~".*"}');

    act(() => result.current.select('ns', ['a']));
    await waitFor(() => expect(metricsQuery.mock.calls.at(-1)[0].queries.variable).toBe('kube_pod_info{namespace=~"a"}'));
  });

  it('fills a value the host page supplies', async () => {
    const pod = { name: 'pod', query: 'up{cluster="$cluster"}', label_key: 'name' };
    renderHook(() => useDashboardVariables(scope({ variables: [pod], context: { cluster: 'c1' } })));
    await waitFor(() => expect(metricsQuery.mock.calls[0][0].queries.variable).toBe('up{cluster="c1"}'));
  });

  it('asks the provider the panels using it name', async () => {
    const panel = { title: 'p', datasource: 'metrics', provider: 'prometheus', targets: [{ ref_id: 'A', expr: 'up{n=~"$ns"}' }] };
    renderHook(() => useDashboardVariables(scope({ panels: [panel] })));
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    expect(metricsQuery.mock.calls[0][0]).toMatchObject({ metric_provider: 'prometheus', instant: true });
  });

  it('asks CloudWatch on an AWS account, which has no instant form', async () => {
    renderHook(() => useDashboardVariables(scope({ accounts: [{ ...account, cloud_provider: 'AWS' }] })));
    await waitFor(() => expect(metricsQuery).toHaveBeenCalled());
    const sent = metricsQuery.mock.calls[0][0];
    expect(sent.instant).toBe(false);
    expect(sent.metric_provider_source).toBe('user');
  });

  it('drops the list when no accounts are left', async () => {
    const { result, rerender } = renderHook((props: object) => useDashboardVariables(scope(props)), { initialProps: {} });
    await waitFor(() => expect(result.current.options.ns).toEqual(['x', 'y']));
    rerender({ accounts: [] });
    await waitFor(() => expect(result.current.options.ns).toBeUndefined());
  });

  it('forgets a pick when its variable is removed or redefined, not when it is relabelled', async () => {
    const { result, rerender } = renderHook((props: object) => useDashboardVariables(scope(props)), { initialProps: {} });
    await waitFor(() => expect(result.current.options.ns).toBeDefined());
    act(() => result.current.select('ns', ['x']));

    rerender({ variables: [{ ...namespace, label: 'Namespace' }] });
    await waitFor(() => expect(result.current.selected.ns).toEqual(['x']));

    rerender({ variables: [{ ...namespace, label: 'Namespace', multi: true }] });
    await waitFor(() => expect(result.current.selected.ns).toBeUndefined());
  });

  it('does not refetch a query variable when the time range moves', async () => {
    const { rerender } = renderHook((props: object) => useDashboardVariables(scope(props)), { initialProps: {} });
    await waitFor(() => expect(metricsQuery).toHaveBeenCalledTimes(1));
    rerender({ startTime: 5, endTime: 6 });
    await act(async () => {});
    expect(metricsQuery).toHaveBeenCalledTimes(1);
  });

  it('reloads a variable read from the query engine when the time range moves, and reports a refusal', async () => {
    const status = { name: 'status', type: 'nudgebee', table: 'events_v2', column: 'status' };
    executeEntityQuery.mockResolvedValue({ data: { rows: [['open']] } });
    const { result, rerender } = renderHook((props: object) => useDashboardVariables(scope({ variables: [status], ...props })), { initialProps: {} });
    await waitFor(() => expect(result.current.options.status).toEqual(['open']));

    executeEntityQuery.mockResolvedValue({ data: null, errors: [{ message: 'not allowed' }] });
    rerender({ startTime: 5, endTime: 6 });
    await waitFor(() => expect(result.current.errors.status).toBe('not allowed'));
    expect(result.current.loading.status).toBe(false);
  });
});
