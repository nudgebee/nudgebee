import apiOverview from '@api1/overview';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
}));

const mockQuery = queryGraphQL as jest.Mock;

// These two rollups are what makes the Account Overview page one request per
// provider family rather than one per account, so the row -> summary folding is
// the part worth pinning: the query engine returns one row per (account_id,
// currency) / (account_id, severity) pair, not one row per account.
describe('apiOverview.listCloudAccountSummaries', () => {
  beforeEach(() => mockQuery.mockReset());

  it('makes no request and returns nothing for an empty account list', async () => {
    await expect(apiOverview.listCloudAccountSummaries([])).resolves.toEqual({});
    expect(mockQuery).not.toHaveBeenCalled();
  });

  it('folds grouped rows onto their account and zero-fills accounts with no data', async () => {
    mockQuery.mockResolvedValue({
      data: {
        data: {
          mtd: { rows: [{ account_id: 'a1', spend_amount: 100, currency_type: 'USD' }] },
          prev_month: { rows: [{ account_id: 'a1', spend_amount: 240, currency_type: 'USD' }] },
          ytd: { rows: [{ account_id: 'a1', spend_amount: 900, currency_type: 'USD' }] },
          recommendations: { rows: [{ account_id: 'a1', count: 4, sum_estimated_savings: 30 }] },
          resources: { rows: [{ account_id: 'a1', count: 12 }] },
          alerts: { rows: [{ account_id: 'a1', event_count: 3 }] },
        },
      },
    });

    const summaries = await apiOverview.listCloudAccountSummaries(['a1', 'a2']);

    expect(summaries.a1).toEqual({
      accountId: 'a1',
      currency: 'USD',
      mtdSpend: 100,
      lastMonthSpend: 240,
      ytdSpend: 900,
      resourceCount: 12,
      recommendationCount: 4,
      estimatedSavings: 30,
      alertCount: 3,
    });
    // a2 is connected but has no spend/recommendations yet — it still needs a
    // summary so the page renders a card rather than dropping the account.
    expect(summaries.a2).toMatchObject({ accountId: 'a2', mtdSpend: 0, resourceCount: 0, alertCount: 0 });
  });

  it('sums multi-currency spend rows for the same account', async () => {
    mockQuery.mockResolvedValue({
      data: {
        data: {
          mtd: {
            rows: [
              { account_id: 'a1', spend_amount: 100, currency_type: 'USD' },
              { account_id: 'a1', spend_amount: 25, currency_type: 'EUR' },
            ],
          },
        },
      },
    });

    const summaries = await apiOverview.listCloudAccountSummaries(['a1']);
    expect(summaries.a1.mtdSpend).toBe(125);
  });

  it('ignores rows for accounts that were not asked for', async () => {
    mockQuery.mockResolvedValue({
      data: { data: { resources: { rows: [{ account_id: 'other', count: 99 }] } } },
    });

    const summaries = await apiOverview.listCloudAccountSummaries(['a1']);
    expect(Object.keys(summaries)).toEqual(['a1']);
    expect(summaries.a1.resourceCount).toBe(0);
  });
});

describe('apiOverview.listVmAccountSummaries', () => {
  beforeEach(() => mockQuery.mockReset());

  it('keys vulnerability counts by severity and totals them', async () => {
    mockQuery.mockResolvedValue({
      data: {
        data: {
          resources: { rows: [{ account_id: 'v1', count: 7 }] },
          vulnerabilities: {
            rows: [
              { account_id: 'v1', severity: 'Critical', count: 2 },
              { account_id: 'v1', severity: 'High', count: 5 },
              { account_id: 'v1', severity: null, count: 1 },
            ],
          },
        },
      },
    });

    const summaries = await apiOverview.listVmAccountSummaries(['v1']);
    expect(summaries.v1).toEqual({
      accountId: 'v1',
      vmCount: 7,
      severities: { Critical: 2, High: 5, Unknown: 1 },
      vulnerabilityCount: 8,
    });
  });

  it('makes no request for an empty account list', async () => {
    await expect(apiOverview.listVmAccountSummaries([])).resolves.toEqual({});
    expect(mockQuery).not.toHaveBeenCalled();
  });
});
