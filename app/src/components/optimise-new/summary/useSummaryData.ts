import { useState, useEffect, useMemo } from 'react';
import recommendationApi from '@api1/recommendation';
import apiHome from '@api1/home';
import apiCloudAccount from '@api1/cloud-account';
import { NON_SECURITY_CATEGORIES, DEFAULT_STATUS } from '../utils';
import { getBudgetExpectedMonthlyExpense } from '@lib/budget';
import { transformApiToInsight } from './transformRecommendation';
import type { CurrencyCostSummary, AccountCost } from './AccountClusterPane';

const DEFAULT_SYMBOL = '$';

// Resolve an ISO currency code (USD/INR/EUR/GBP/…) to its narrow symbol via Intl,
// so any billing currency a cloud provider reports renders correctly rather than
// falling back to '$' for everything outside a hardcoded USD/INR map.
const getCurrencySymbol = (currency: string): string => {
  try {
    return (
      new Intl.NumberFormat('en-US', { style: 'currency', currency, currencyDisplay: 'narrowSymbol' })
        .formatToParts(0)
        .find((p) => p.type === 'currency')?.value || DEFAULT_SYMBOL
    );
  } catch {
    return DEFAULT_SYMBOL;
  }
};

export function useSummaryData() {
  const [accounts, setAccounts] = useState<Record<string, { account_name: string; cloud_provider: string }>>({});
  const [rawApiRows, setRawApiRows] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [totalSavings, setTotalSavings] = useState(0);
  const [savingsLoading, setSavingsLoading] = useState(true);
  const [costByCurrency, setCostByCurrency] = useState<CurrencyCostSummary[]>([]);
  const [accountCosts, setAccountCosts] = useState<Record<string, AccountCost>>({});
  const [currencySymbols, setCurrencySymbols] = useState<Record<string, string>>({});
  // Currency the headline / briefing savings total is rendered in. estimated_savings
  // is denominated in each account's billing currency, so a single-currency tenant
  // (the common case) gets its real currency; a mixed-currency tenant falls back to
  // USD since one summed figure across currencies is meaningless either way.
  const [savingsCurrency, setSavingsCurrency] = useState<string>('USD');
  const [savingsSymbol, setSavingsSymbol] = useState<string>(DEFAULT_SYMBOL);
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

  // Curated list = most urgent (finops_score) ∪ highest-impact (estimated_savings).
  // The finops ranking alone is dominated by the large volume of high-severity
  // config findings, which crowds high-$ cost recs out of the top-N entirely; a
  // savings-ordered fetch unioned in guarantees the biggest savings opportunities
  // surface. Both list queries run in parallel and share a 10-min TTL cache, so
  // second visits are instant.
  useEffect(() => {
    let cancelled = false;
    setLoading(true);

    (async () => {
      try {
        const listArgs = { category: NON_SECURITY_CATEGORIES as any, status: DEFAULT_STATUS, orderAsc: false, limit: 100 };
        const [urgentResp, impactResp]: [any, any] = await Promise.all([
          recommendationApi.getOptimisationSummaryRecommendations({ ...listArgs, orderBy: 'finops_score' }),
          recommendationApi.getOptimisationSummaryRecommendations({ ...listArgs, orderBy: 'estimated_savings' }),
        ]);

        if (cancelled) return;

        // Merge by id, urgency-ranked rows first so the finops order is preserved.
        const byId = new Map<string, any>();
        for (const r of urgentResp?.data?.recommendation || []) byId.set(r.id, r);
        for (const r of impactResp?.data?.recommendation || []) if (!byId.has(r.id)) byId.set(r.id, r);
        setRawApiRows(Array.from(byId.values()));
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

  // Total potential savings across the FULL in-scope set (same aggregate the
  // Recommendations tab uses), so the two tabs always agree. Fetched separately
  // from the list so the slower full-set aggregate never blocks the curated list
  // from rendering — it only gates the headline number.
  useEffect(() => {
    let cancelled = false;
    setSavingsLoading(true);

    (async () => {
      try {
        const rows: any = await recommendationApi.getK8sRecommendationSummaryByRuleName({
          accountId: '',
          category: NON_SECURITY_CATEGORIES as any,
          status: DEFAULT_STATUS,
        });
        if (cancelled) return;
        setTotalSavings(Array.isArray(rows) ? rows.reduce((sum: number, row: any) => sum + (row.sum_estimated_savings || 0), 0) : 0);
      } catch (err) {
        console.error('Failed to fetch savings total:', err);
      } finally {
        if (!cancelled) setSavingsLoading(false);
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

  // Fetch cost data for all accounts in one batched query. The previous version
  // fanned out 2 calls per account (a 13-aggregate summary + a 7-day trend used
  // only for its currency symbol) — ~70 requests that queued behind the browser's
  // per-origin connection limit and dominated first load.
  useEffect(() => {
    const accountIds = Object.keys(accounts);
    if (accountIds.length === 0) return;
    let cancelled = false;
    setCostLoading(true);

    (async () => {
      try {
        const { mtd: mtdRows, prevMonth: prevRows, ytd: ytdRows } = await apiCloudAccount.listAccountsSpendSummary(accountIds);

        if (cancelled) return;

        // Fold (account_id, currency) grouped rows into per-account totals. We track
        // spend per currency rather than latching onto the first currency_type seen,
        // so an account with a few stray cross-currency rows resolves to the currency
        // its spend is actually denominated in (dominant by amount), deterministically.
        const folded: Record<string, { mtd: number; prevMonth: number; ytd: number; currencyAmounts: Record<string, number> }> = {};
        const fold = (rows: any[], key: 'mtd' | 'prevMonth' | 'ytd') => {
          for (const row of rows || []) {
            const id = row?.account_id;
            if (!id) continue;
            if (!folded[id]) folded[id] = { mtd: 0, prevMonth: 0, ytd: 0, currencyAmounts: {} };
            folded[id][key] += row.spend_amount || 0;
            if (row.currency_type) {
              folded[id].currencyAmounts[row.currency_type] = (folded[id].currencyAmounts[row.currency_type] || 0) + Math.abs(row.spend_amount || 0);
            }
          }
        };
        fold(mtdRows, 'mtd');
        fold(prevRows, 'prevMonth');
        fold(ytdRows, 'ytd');

        // Dominant currency = highest total spend; ties broken alphabetically so the
        // pick is stable across reloads regardless of row order.
        const dominantCurrency = (amounts: Record<string, number>): string =>
          Object.entries(amounts).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))[0]?.[0] || '';

        const accountCurrency: Record<string, string> = {};
        const byCurrency: Record<string, { mtd: number; prevMonth: number; ytd: number; accountNames: string[] }> = {};
        const perAccount: Record<string, AccountCost> = {};
        const realCurrencies = new Set<string>();

        accountIds.forEach((id) => {
          const data = folded[id];
          const currencyIso = dominantCurrency(data?.currencyAmounts || {});
          if (currencyIso) realCurrencies.add(currencyIso);
          const symbol = currencyIso ? getCurrencySymbol(currencyIso) : DEFAULT_SYMBOL;
          accountCurrency[id] = symbol;
          const acctName = accounts[id]?.account_name || id;

          const mtd = data?.mtd || 0;
          const prevMonth = data?.prevMonth || 0;
          const ytd = data?.ytd || 0;

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

        const tenantCurrency = realCurrencies.size === 1 ? [...realCurrencies][0] : 'USD';
        setCostByCurrency(costSummaries);
        setAccountCosts(perAccount);
        setCurrencySymbols(accountCurrency);
        setSavingsCurrency(tenantCurrency);
        setSavingsSymbol(getCurrencySymbol(tenantCurrency));
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

  return {
    accounts,
    insights,
    loading,
    lastUpdated,
    totalSavings,
    savingsLoading,
    costByCurrency,
    accountCosts,
    costLoading,
    savingsCurrency,
    savingsSymbol,
  };
}
