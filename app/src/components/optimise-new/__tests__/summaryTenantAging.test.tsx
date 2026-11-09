import { renderHook, waitFor } from '@testing-library/react';
import { useSummaryData } from '@components/optimise-new/summary/useSummaryData';
import { AGING_DAYS } from '@components/optimise-new/summary/insights';

const summaryByRuleName = jest.fn();
const listRecommendations = jest.fn();
jest.mock('@api1/home', () => ({ __esModule: true, default: { getCloudAccounts: () => Promise.resolve([]) } }));
jest.mock('@api1/cloud-account', () => ({ __esModule: true, default: { listAccountsSpendSummary: () => Promise.resolve({}) } }));
jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getOptimisationSummaryRecommendations: () => Promise.resolve({ data: { recommendation: [] } }),
    getK8sRecommendationSummaryByRuleName: (args: any) => summaryByRuleName(args),
    getK8sRecommendationSummaryByAccount: () => Promise.resolve([]),
    getRecommendationResolutionStatusTotals: () => Promise.resolve({ Success: { count: 12, savings: 340 }, InProgress: { count: 3, savings: 90 } }),
    getK8sRecommendation: (args: any) => listRecommendations(args),
    getK8sRecommendationSafetyGroups: () => Promise.resolve([]),
  },
}));

const NOW = new Date('2026-10-05T12:00:00.000Z').getTime();
const CUTOFF = new Date(NOW - AGING_DAYS * 24 * 60 * 60 * 1000).toISOString();

beforeEach(() => {
  summaryByRuleName.mockReset();
  listRecommendations.mockReset();
  jest.spyOn(Date, 'now').mockReturnValue(NOW);
  // The headline total and the aging roll-up share one function; the aging call
  // is the one carrying createdAtLt.
  summaryByRuleName.mockImplementation((args: any) =>
    Promise.resolve(
      args.createdAtLt
        ? [
            { count: 400, sum_estimated_savings: 1000 },
            { count: 500, sum_estimated_savings: 500 },
          ]
        : [{ count: 2000, sum_estimated_savings: 6000, category: 'RightSizing', rule_name: 'r' }]
    )
  );
  listRecommendations.mockResolvedValue({
    data: {
      recommendation: [
        {
          id: 'rec-oldest',
          account_id: 'acct-1',
          resource_id: 'res-1',
          resource_name: 'payments-api',
          category: 'RightSizing',
          rule_name: 'vertical_rightsizing',
          severity: 'High',
          estimated_savings: 75,
          created_at: '2026-06-07T12:00:00.000Z',
          recommendation: {},
        },
      ],
    },
  });
});

afterEach(() => jest.restoreAllMocks());

describe('Summary age and resolution figures are tenant-wide', () => {
  it('counts findings first raised before the cutoff across the full set, not the curated list', async () => {
    const { result } = renderHook(() => useSummaryData());
    await waitFor(() => expect(result.current.aging).not.toBeNull());

    expect(result.current.aging).toEqual({ count: 900, dollars: 1500 });
    const agingCall = summaryByRuleName.mock.calls.map(([a]) => a).find((a) => a.createdAtLt);
    expect(agingCall).toMatchObject({ createdAtLt: CUTOFF, status: ['Open', 'InProgress'] });
    expect(agingCall.updatedAtGte).toBeUndefined();
  });

  it('fetches the oldest finding past the cutoff on its own, oldest first', async () => {
    const { result } = renderHook(() => useSummaryData());
    await waitFor(() => expect(result.current.longestStanding).not.toBeNull());

    expect(listRecommendations).toHaveBeenCalledWith(
      expect.objectContaining({ orderBy: 'created_at', orderAsc: true, limit: 1, createdAtLt: CUTOFF })
    );
    expect(result.current.longestStanding?.id).toBe('rec-oldest');
    expect(Math.floor(result.current.longestStanding!.ageDays)).toBe(120);
    expect(result.current.longestStanding?._raw?.id).toBe('rec-oldest');
  });

  it('skips the age fetches for an environment with no accounts', async () => {
    const { result } = renderHook(() => useSummaryData(null, 'prod'));
    await waitFor(() => expect(result.current.aging).toEqual({ count: 0, dollars: 0 }));

    expect(listRecommendations).not.toHaveBeenCalled();
    expect(summaryByRuleName.mock.calls.some(([a]) => a.createdAtLt)).toBe(false);
    expect(result.current.longestStanding).toBeNull();
  });

  it('pairs the resolved count with the savings of what was resolved', async () => {
    const { result } = renderHook(() => useSummaryData());
    await waitFor(() => expect(result.current.resolvedCount).toBe(12));

    expect(result.current.resolvedSavings).toBe(340);
    expect(result.current.wipCount).toBe(3);
  });
});
