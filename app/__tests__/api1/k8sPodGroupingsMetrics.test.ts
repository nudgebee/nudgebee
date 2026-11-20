import apiKubernetes from '@api1/kubernetes';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  ...jest.requireActual('@lib/HttpService'),
  queryGraphQL: jest.fn(),
}));

const mockQuery = queryGraphQL as jest.Mock;

describe('apiKubernetes.getK8sPodGroupings2 metrics', () => {
  beforeEach(() => {
    mockQuery.mockReset();
    mockQuery.mockResolvedValue({
      data: {
        data: {
          metrics_aggregate_utilisation: {
            results: [
              { query_key: 'cpu_usage', payload: [{ timestamps: [1, 2], values: ['0', 'invalid'] }] },
              { query_key: 'cpu_request', payload: [{ values: ['0.25'] }] },
              { query_key: 'cpu_limit', payload: [{ values: ['0'] }] },
              { query_key: 'memory_usage', payload: [{ values: ['0'] }] },
              { query_key: 'memory_request', payload: [{ values: ['2'] }] },
              { query_key: 'memory_limit', payload: [{ values: ['0'] }] },
              { query_key: 'pod_count', payload: [{ values: ['0'] }] },
              { query_key: 'cpu_usage_max_pod', payload: [{ values: ['0'] }] },
              { query_key: 'memory_usage_max_pod', payload: [{ values: [] }] },
            ],
          },
        },
      },
    });
  });

  it('preserves zero readings and leaves missing readings null', async () => {
    const result = await apiKubernetes.getK8sPodGroupings2(
      10,
      { accountId: 'account-1', startDate: new Date(0), endDate: new Date(1000) },
      ['tenant_id', 'account_id', 'timestamp'],
      'prometheus'
    );

    expect(result.data.k8s_pod_groupings[0]).toMatchObject({
      avg_cpu_used: 0,
      avg_cpu_request: 0.25,
      avg_cpu_limit: 0,
      avg_memory_used: 0,
      avg_memory_request: 2,
      avg_memory_limit: 0,
      pod_count: 0,
      max_pod_cpu_used: 0,
      max_pod_memory_used: null,
    });
    expect(result.data.k8s_pod_groupings[1]).toMatchObject({ avg_cpu_used: null });
  });
});
