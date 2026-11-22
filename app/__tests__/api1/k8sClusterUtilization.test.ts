import apiKubernetes from '@api1/kubernetes';
import { queryGraphQL } from '@lib/HttpService';
import { formatDateTime } from '@lib/datetime';

jest.mock('@lib/HttpService', () => ({
  ...jest.requireActual('@lib/HttpService'),
  queryGraphQL: jest.fn(),
}));

it('aligns cluster utilization samples by their own timestamps', async () => {
  const first = 1_700_000_000_000;
  const second = first + 86_400_000;
  const last = second + 86_400_000;
  (queryGraphQL as jest.Mock).mockResolvedValueOnce({
    data: {
      data: {
        metrics_aggregate_utilisation: {
          results: [
            { query_key: 'network_receive_bytes_increase', payload: [{ timestamps: [first], values: ['0'] }] },
            { query_key: 'network_transmit_bytes_increase', payload: [{ timestamps: [last], values: ['32'] }] },
            { query_key: 'cpu_usage_trend', payload: [{ timestamps: [first, last], values: ['2', '4'] }] },
            { query_key: 'cpu_total', payload: [{ timestamps: [second, last], values: ['8', '16'] }] },
            { query_key: 'mem_usage_trend', payload: [{ timestamps: [second], values: ['invalid'] }] },
          ],
        },
      },
    },
  });

  const account_id = 'cluster-test';
  const result = await apiKubernetes.getClusterMetrices2({
    accountId: account_id,
    metric: ['networkReceiveBytes', 'networkTransferBytes', 'cpu', 'memory'],
    startDate: new Date(first),
    endDate: new Date(last),
    limit: 100,
    dateUnit: 'day',
  });

  expect(result).toEqual({
    data: {
      cloud_resource_metrics_groupings: [
        { timestamp: formatDateTime(first), metric: 'networkReceiveBytes', avg_value: 0, account_id },
        { timestamp: formatDateTime(first), avg_cpu_used_node: 2, total_cpu_allocatable: null, account_id },
        { timestamp: formatDateTime(second), avg_cpu_used_node: null, total_cpu_allocatable: 8, account_id },
        { timestamp: formatDateTime(last), metric: 'networkTransferBytes', avg_value: 32, account_id },
        { timestamp: formatDateTime(last), avg_cpu_used_node: 4, total_cpu_allocatable: 16, account_id },
      ],
    },
  });
});
