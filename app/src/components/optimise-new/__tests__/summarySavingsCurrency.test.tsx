import '@testing-library/jest-dom';
import { cleanup, render, renderHook, screen, waitFor } from '@testing-library/react';
import AccountClusterPane from '@components/optimise-new/summary/AccountClusterPane';
import { useSummaryData } from '@components/optimise-new/summary/useSummaryData';
import type { AccountSummary } from '@components/optimise-new/summary/insights';

const spendRows = jest.fn();
jest.mock('@api1/home', () => ({
  __esModule: true,
  default: {
    getCloudAccounts: () =>
      Promise.resolve([
        { id: 'acct-inr', account_name: 'gcp-inr', cloud_provider: 'GCP', account_env: 'non_prod' },
        { id: 'acct-usd', account_name: 'aws-usd', cloud_provider: 'AWS', account_env: 'prod' },
      ]),
  },
}));
jest.mock('@api1/cloud-account', () => ({ __esModule: true, default: { listAccountsSpendSummary: (ids: string[]) => spendRows(ids) } }));
// The recommendation aggregates are not under test; every call answers empty.
jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getOptimisationSummaryRecommendations: () => Promise.resolve({ data: { recommendation: [] } }),
    getK8sRecommendationSummaryByRuleName: () => Promise.resolve([]),
    getK8sRecommendationSummaryByAccount: () => Promise.resolve([]),
    getRecommendationResolutionStatusTotals: () => Promise.resolve({}),
    getK8sRecommendation: () => Promise.resolve({ data: { recommendation: [] } }),
    getK8sRecommendationSafetyGroups: () => Promise.resolve([]),
  },
}));

afterEach(cleanup);

// Every producer prices savings in USD; only spend is in the account's billing
// currency. Summary used to relabel the USD savings with the spend symbol, so an
// account billed in INR read "₹23/mo" for a $23 estimate.
describe('Summary savings stay in USD', () => {
  const account = (accountId: string, accountName: string): AccountSummary => ({
    accountId,
    accountName,
    provider: 'gcp',
    criticalCount: 0,
    highCount: 1,
    totalDollarImpact: 23,
    topItems: [],
    totalItems: 1,
    categoryCounts: { performance: 0, cost: 1, security_config: 0 },
  });

  it('prints account card savings in $ and flags USD only where the bill is in another currency', () => {
    render(
      <AccountClusterPane
        accounts={[account('acct-inr', 'gcp-inr'), account('acct-usd', 'aws-usd')]}
        accountCosts={{
          'acct-inr': { mtd: 1486911, change: 0, projected: 1500000, currencySymbol: '₹' },
          'acct-usd': { mtd: 1200, change: 0, projected: 1300, currencySymbol: '$' },
        }}
        costLoading={false}
      />
    );
    expect(screen.getByText('$23/mo savings (USD)')).toBeInTheDocument();
    expect(screen.getByText('$23/mo savings')).toBeInTheDocument();
    expect(screen.queryByText(/₹23/)).not.toBeInTheDocument();
  });

  // The hook: spend rows carry INR for one account, USD for the other. The spend
  // symbols must follow the rows; the savings symbol must not.
  it('keeps the savings currency USD while the spend keeps its billing currency', async () => {
    spendRows.mockResolvedValue({
      mtd: [
        { account_id: 'acct-inr', spend_amount: 1486911, currency_type: 'INR' },
        { account_id: 'acct-usd', spend_amount: 1200, currency_type: 'USD' },
      ],
      prevMonth: [],
      ytd: [],
    });
    const { result } = renderHook(() => useSummaryData());
    await waitFor(() => expect(result.current.costLoading).toBe(false));
    expect(result.current.savingsSymbol).toBe('$');
    expect(result.current.savingsCurrency).toBe('USD');
    expect(result.current.billedOutsideUsd).toBe(true);
    expect(result.current.accountCosts['acct-inr'].currencySymbol).toBe('\u20b9');
    expect(result.current.accountCosts['acct-usd'].currencySymbol).toBe('$');
  });

  it('does not flag a tenant whose every account bills in USD', async () => {
    spendRows.mockResolvedValue({ mtd: [{ account_id: 'acct-usd', spend_amount: 1200, currency_type: 'USD' }], prevMonth: [], ytd: [] });
    const { result } = renderHook(() => useSummaryData());
    await waitFor(() => expect(result.current.costLoading).toBe(false));
    expect(result.current.billedOutsideUsd).toBe(false);
    expect(result.current.savingsSymbol).toBe('$');
  });
});
