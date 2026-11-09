import { queryGraphQL } from '@lib/HttpService';
import apiKubernetes from '../index';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn(), hitRelayServer: jest.fn() }));
jest.mock('@api1/mock', () => ({}));
jest.mock('@api1/observability', () => ({}));
jest.mock('@context/DataContext', () => ({ getClusterData: jest.fn() }));

// The Account Overview renders two tiles per cluster (ClusterViewCard and
// KubernetesSaving) that both ask for the yearly-saving rollup on mount, so a
// 10-cluster tenant sent 20 identical requests. Concurrent callers must share
// one request; a later call must still reach the network.
describe('listk8ClustersYearlySaving in-flight sharing', () => {
  beforeEach(() => jest.clearAllMocks());

  const response = {
    data: {
      data: {
        recommendation_aggregate: { rows: [{ sum_estimated_savings: 10 }] },
        spends_aggregate: { rows: [{ spend_amount: 100 }] },
        yearly_spends_aggregate: { rows: [{ spend_amount: 1000 }] },
        lm_spends_aggregate: { rows: [{ spend_amount: 90 }] },
      },
    },
  };

  it('sends one request when two callers ask for the same account at once', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue(response);

    const [a, b] = await Promise.all([apiKubernetes.listk8ClustersYearlySaving('acct-1'), apiKubernetes.listk8ClustersYearlySaving('acct-1')]);

    expect(queryGraphQL).toHaveBeenCalledTimes(1);
    expect(a).toBe(b);
    expect(a.data.mtd_cost).toBe(100);
    expect(a.data.previous_cost).toBe(90);
    expect(a.data.yearly_recommendation_saving).toBe(120);
  });

  it('keeps different accounts and later calls on their own requests', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue(response);

    await Promise.all([apiKubernetes.listk8ClustersYearlySaving('acct-1'), apiKubernetes.listk8ClustersYearlySaving('acct-2')]);
    expect(queryGraphQL).toHaveBeenCalledTimes(2);

    await apiKubernetes.listk8ClustersYearlySaving('acct-1');
    expect(queryGraphQL).toHaveBeenCalledTimes(3);
  });

  it('does not pin a failed request: the next caller retries', async () => {
    (queryGraphQL as jest.Mock).mockRejectedValueOnce(new Error('boom')).mockResolvedValue(response);

    const failed = await apiKubernetes.listk8ClustersYearlySaving('acct-1');
    expect(failed).toBeInstanceOf(Error);

    const ok = await apiKubernetes.listk8ClustersYearlySaving('acct-1');
    expect(ok.data.mtd_cost).toBe(100);
    expect(queryGraphQL).toHaveBeenCalledTimes(2);
  });
});
