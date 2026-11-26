import { act, renderHook } from '@testing-library/react';
import observability from '@api1/observability';
import { useLiveResourceMetrics } from '@components/cloudaccount/cloud-metrics/useLiveResourceMetrics';

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: jest.fn() },
}));

const query = observability.metricsQuery as jest.Mock;
const args = { accountId: 'account-1', serviceName: 'EC2', startDate: 1_000, endDate: 2_000 };
const instance = { resourse_id: 'instance-1' };

beforeEach(() => query.mockReset());

it('stops loading when the resources disappear during a request', async () => {
  let finish!: (value: unknown) => void;
  query.mockReturnValueOnce(
    new Promise((resolve) => {
      finish = resolve;
    })
  );

  const { result, rerender } = renderHook(({ resources }) => useLiveResourceMetrics({ ...args, resources }), {
    initialProps: { resources: [instance] },
  });
  expect(result.current.loading).toBe(true);
  const emptyCharts = result.current.dataByMetric;

  rerender({ resources: [] });
  expect(result.current.dataByMetric).toBe(emptyCharts);
  expect(result.current).toEqual({ dataByMetric: {}, loading: false, error: null });
  await act(async () => finish({ data: { data: { metrics_list: { results: [] } } } }));
  expect(result.current).toEqual({ dataByMetric: {}, loading: false, error: null });
  expect(query).toHaveBeenCalledTimes(1);
});

it('clears a previous request error when no resources remain', async () => {
  query.mockRejectedValueOnce(new Error('provider unavailable'));
  const { result, rerender } = renderHook(({ resources }) => useLiveResourceMetrics({ ...args, resources }), {
    initialProps: { resources: [instance] },
  });
  await act(async () => {});
  expect(result.current.error).toBe('Failed to fetch metrics');

  rerender({ resources: [] });
  expect(result.current).toEqual({ dataByMetric: {}, loading: false, error: null });
});
