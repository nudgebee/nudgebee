import { queryGraphQL } from '@lib/HttpService';
import apiKubernetes from '../index';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn(), hitRelayServer: jest.fn() }));
jest.mock('@api1/mock', () => ({}));
jest.mock('@api1/observability', () => ({}));
jest.mock('@context/DataContext', () => ({ getClusterData: jest.fn() }));

// `new Date(); setMonth(month - 1)` on the 29th–31st overflows into the current
// month (Oct 31 -> "Sep 31" -> Oct 1), so "Last Month" on the Account Overview
// read this month's spend on those days.
describe('last-month window on the 31st', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    jest.useFakeTimers().setSystemTime(new Date(2026, 9, 31, 12, 0, 0)); // Oct 31 2026, local time
  });
  afterEach(() => jest.useRealTimers());

  it('asks for September, not October, in the yearly-saving rollup', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({ data: { data: {} } });

    await apiKubernetes.listk8ClustersYearlySaving('acct-1');

    const vars = (queryGraphQL as jest.Mock).mock.calls[0][2];
    expect([vars.lmStartDate.getMonth(), vars.lmStartDate.getDate()]).toEqual([8, 1]);
    expect([vars.lmEndDate.getMonth(), vars.lmEndDate.getDate()]).toEqual([8, 30]);
    expect([vars.startDate.getMonth(), vars.startDate.getDate()]).toEqual([9, 1]);
  });

  it('tolerates an empty recommendation aggregate instead of throwing', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({
      data: { data: { recommendation_aggregate: { rows: [] }, spends_aggregate: { rows: [{ spend_amount: 10 }] } } },
    });

    const out = await apiKubernetes.listk8ClustersYearlySaving('acct-2');

    expect(out).not.toBeInstanceOf(Error);
    expect(out.data.yearly_recommendation_saving).toBe(0);
    expect(out.data.mtd_cost).toBe(10);
  });
});
