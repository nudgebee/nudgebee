import { useState, useEffect, useMemo } from 'react';
import recommendationApi from '@api1/recommendation';
import apiHome from '@api1/home';
import apiCloudAccount from '@api1/cloud-account';
import { NON_SECURITY_CATEGORIES, DEFAULT_STATUS } from '../utils';
import { getBudgetExpectedMonthlyExpense } from '@lib/budget';
import { transformApiToInsight } from './transformRecommendation';
import type { CurrencyCostSummary, AccountCost } from './AccountClusterPane';

const CURRENCY_MAP: Record<string, string> = { USD: '$', INR: '₹' };
const DEFAULT_SYMBOL = '$';

export function useSummaryData() {
  const [accounts, setAccounts] = useState<Record<string, { account_name: string; cloud_provider: string }>>({});
  const [rawApiRows, setRawApiRows] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [costByCurrency, setCostByCurrency] = useState<CurrencyCostSummary[]>([]);
  const [accountCosts, setAccountCosts] = useState<Record<string, AccountCost>>({});
  const [currencySymbols, setCurrencySymbols] = useState<Record<string, string>>({});
  const [costLoading, setCostLoading] = useState(true);

  // Fetch accounts
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const data = await apiHome.getCloudAccounts();
        if (cancelled || !Array.isArray(data)) return;
        const map: Record<string, { account_name: string; cloud_provider: string }> = {};
        data.forEach((a: any) => {
          if (a.id) map[a.id] = { account_name: a.account_name, cloud_provider: a.cloud_provider };
        });
        setAccounts(map);
      } catch (err) {
        console.error('Failed to fetch accounts:', err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Fetch recommendations — runs in parallel with accounts since this query
  // doesn't need account metadata (transform joins on accounts later via useMemo).
  // Uses a 10-min TTL cache; second visits are instant.
  useEffect(() => {
    let cancelled = false;
    setLoading(true);

    (async () => {
      try {
        const resp: any = await recommendationApi.getOptimisationSummaryRecommendations({
          category: NON_SECURITY_CATEGORIES as any,
          status: DEFAULT_STATUS,
          orderBy: 'finops_score',
          orderAsc: false,
          limit: 100,
        });

        if (cancelled) return;

        setRawApiRows(resp?.data?.recommendation || []);
        setLastUpdated(new Date());
      } catch (err) {
        console.error('Failed to fetch recommendations:', err);
        if (!cancelled) setRawApiRows([]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const insights = useMemo(() => {
    if (rawApiRows.length === 0) return [];
    return rawApiRows.map((r: any) => transformApiToInsight(r, accounts, currencySymbols));
  }, [rawApiRows, accounts, currencySymbols]);

  // Fetch cost data per account (with currency awareness)
  useEffect(() => {
    const accountIds = Object.keys(accounts);
    if (accountIds.length === 0) return;
    let cancelled = false;
    setCostLoading(true);

    (async () => {
      try {
        const [summaryResults, trendResults] = await Promise.all([
          Promise.allSettled(accountIds.map((id) => apiCloudAccount.cloudAccountSummary(id))),
          Promise.allSettled(
            accountIds.map((id) =>
              apiCloudAccount.listCloudAccountTrend({ accountId: id }, new Date(Date.now() - 7 * 24 * 60 * 60 * 1000), new Date(), 'Day')
            )
          ),
        ]);

        if (cancelled) return;

        const accountCurrency: Record<string, string> = {};
        trendResults.forEach((result, idx) => {
          const id = accountIds[idx];
          if (result.status !== 'fulfilled') return;
          const firstRecord = (result.value as any)?.data?.spend_groupings?.[0];
          accountCurrency[id] = CURRENCY_MAP[firstRecord?.currency_type] || DEFAULT_SYMBOL;
        });

        const byCurrency: Record<string, { mtd: number; prevMonth: number; ytd: number; accountNames: string[] }> = {};
        const perAccount: Record<string, AccountCost> = {};

        summaryResults.forEach((result, idx) => {
          const id = accountIds[idx];
          if (result.status !== 'fulfilled' || !result.value) return;
          const data: any = result.value;
          const symbol = accountCurrency[id] || DEFAULT_SYMBOL;
          const acctName = accounts[id]?.account_name || id;

          const mtd = data.spends_aggregate?.aggregate?.sum?.amount || 0;
          const prevMonth = data.lm_spends_aggregate?.aggregate?.sum?.amount || 0;
          const ytd = data.yearly_spends_aggregate?.aggregate?.sum?.amount || 0;

          if (!byCurrency[symbol]) byCurrency[symbol] = { mtd: 0, prevMonth: 0, ytd: 0, accountNames: [] };
          byCurrency[symbol].mtd += mtd;
          byCurrency[symbol].prevMonth += prevMonth;
          byCurrency[symbol].ytd += ytd;
          byCurrency[symbol].accountNames.push(acctName);

          const change = prevMonth > 0 ? ((mtd - prevMonth) / prevMonth) * 100 : 0;
          const projected = getBudgetExpectedMonthlyExpense(mtd);
          perAccount[id] = { mtd, change: Math.round(change * 10) / 10, projected, currencySymbol: symbol };
        });

        const costSummaries: CurrencyCostSummary[] = Object.entries(byCurrency)
          .filter(([, v]) => v.mtd > 0 || v.prevMonth > 0)
          .map(([symbol, v]) => {
            const projected = getBudgetExpectedMonthlyExpense(v.mtd);
            const mtdChange = v.prevMonth > 0 ? ((v.mtd - v.prevMonth) / v.prevMonth) * 100 : 0;
            const projectedChange = v.prevMonth > 0 ? ((projected - v.prevMonth) / v.prevMonth) * 100 : 0;
            return {
              currencySymbol: symbol,
              accountNames: v.accountNames,
              mtd: v.mtd,
              prevMonth: v.prevMonth,
              projected,
              ytd: v.ytd,
              mtdChange: Math.round(mtdChange * 10) / 10,
              projectedChange: Math.round(projectedChange * 10) / 10,
            };
          });

        setCostByCurrency(costSummaries);
        setAccountCosts(perAccount);
        setCurrencySymbols(accountCurrency);
      } catch (err) {
        console.error('Failed to fetch cost data:', err);
      } finally {
        if (!cancelled) setCostLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [accounts]);

  return { accounts, insights, loading, lastUpdated, costByCurrency, accountCosts, costLoading };
}
