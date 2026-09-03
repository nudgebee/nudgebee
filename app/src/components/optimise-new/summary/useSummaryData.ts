import { useState, useEffect, useMemo } from 'react';
import recommendationApi from '@api1/recommendation';
import apiHome from '@api1/home';
import apiCloudAccount from '@api1/cloud-account';
import { NON_SECURITY_CATEGORIES, DEFAULT_STATUS, UPGRADE_PLANNER_RULES } from '../utils';
import { getBudgetExpectedMonthlyExpense } from '@lib/budget';
import { transformApiToInsight, mapCategoryAndSubCategory } from './transformRecommendation';
import type { CurrencyCostSummary, AccountCost } from './AccountClusterPane';
import type { MainCategory } from './insights';

// One row per category — always all three, even at 0, so a category with no
// monetised savings still shows up as a real (empty) bucket rather than
// vanishing from the breakdown entirely.
const EMPTY_CATEGORY_BREAKDOWN: Record<MainCategory, { dollars: number; count: number }> = {
  cost: { dollars: 0, count: 0 },
  performance: { dollars: 0, count: 0 },
  security_config: { dollars: 0, count: 0 },
};

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

// accountId / envFilter scope the headline savings total. The findings list is
// filtered client-side, so without this the card keeps showing the tenant-wide
// figure while the list beneath it shows one account. Environment is not a
// column on `recommendation` — it lives on the account — so an env filter
// resolves to the set of account ids in that environment and scopes the same
// aggregates via `_in`.
export function useSummaryData(accountId?: string | null, envFilter?: string | null) {
  const [accounts, setAccounts] = useState<Record<string, { account_name: string; cloud_provider: string; account_env?: string }>>({});
  // Whether the accounts fetch has settled (success OR failure). `accounts` being
  // empty alone can't tell "still loading" from "loaded, none" (upstream down /
  // no cloud accounts) — this flag does, so the cost effect can clear costLoading
  // on a genuinely empty account set instead of leaving the card on a skeleton.
  const [accountsLoaded, setAccountsLoaded] = useState(false);
  const [rawApiRows, setRawApiRows] = useState<any[]>([]);
  // True count of in-scope findings, independent of the curated list's fetch
  // `limit`. The curated list (`insights`) only ever holds the top-N-by-urgency
  // ∪ top-N-by-impact rows, so once a tenant has more open findings than that,
  // `insights.length` under-reports — this is the real total to compare against.
  const [totalFindingsCount, setTotalFindingsCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [totalSavings, setTotalSavings] = useState(0);
  const [savingsLoading, setSavingsLoading] = useState(true);
  // Tenant-wide (or account-scoped, when `accountId` narrows it) category split —
  // same underlying rows as `totalSavings`, just not collapsed into one sum.
  const [categoryBreakdown, setCategoryBreakdown] = useState(EMPTY_CATEGORY_BREAKDOWN);
  // Raw {accountId, dollars} only — the display name is resolved separately via
  // `accounts` in a memo below, so a slow accounts fetch doesn't force a refetch here.
  const [worstAccountRaw, setWorstAccountRaw] = useState<{ accountId: string; dollars: number } | null>(null);
  // True count of in-progress resolutions (tenant-wide, or account-scoped when
  // `accountId` narrows it) — from the same status-counts aggregate the
  // Resolutions tab's stat cards use, not derived from the curated list.
  const [wipCount, setWipCount] = useState(0);
  // True count of resolutions that completed successfully, from the same
  // status-counts aggregate as `wipCount`. There is no savings sum to pair with
  // it: recommendation_resolution_groupings_v2 exposes `count` only.
  const [resolvedCount, setResolvedCount] = useState(0);
  // Findings whose blast-radius assessment came back `safe` — full-set counts
  // and savings from the same groupings table the Safety filter chips use.
  const [safeToApply, setSafeToApply] = useState<{ count: number; dollars: number } | null>(null);
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
        const map: Record<string, { account_name: string; cloud_provider: string; account_env?: string }> = {};
        data.forEach((a: any) => {
          if (a.id) map[a.id] = { account_name: a.account_name, cloud_provider: a.cloud_provider, account_env: a.account_env };
        });
        setAccounts(map);
      } catch (err) {
        console.error('Failed to fetch accounts:', err);
      } finally {
        if (!cancelled) setAccountsLoaded(true);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Account scope shared by every aggregate below. A picked account wins; an
  // env filter resolves to that environment's accounts; neither ⇒ '' (all).
  // `scopeKey` is the effect dependency — the array identity changes on every
  // render, the joined string does not.
  const scopedAccountIds = useMemo((): string | string[] => {
    if (accountId) return accountId;
    if (!envFilter) return '';
    return Object.entries(accounts)
      .filter(([, a]) => (a.account_env === 'prod' ? 'prod' : 'non_prod') === envFilter)
      .map(([id]) => id);
  }, [accountId, envFilter, accounts]);
  // JSON, not a joined string: `[].join(',')` is `''`, which is also the "every
  // account" value, so joining collapses "no accounts in this environment" and
  // "no filter at all" into the same key and the effects below never refire.
  // Selecting an environment that matches nothing would then leave a tenant-wide
  // headline sitting above an empty list.
  const scopeKey = JSON.stringify(scopedAccountIds);
  // With an env filter the scope is only meaningful once accounts have landed —
  // resolving it against an empty map would fetch `_in: []` and flash zeros.
  const scopeReady = !envFilter || accountId ? true : accountsLoaded;

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
        const listArgs = {
          category: NON_SECURITY_CATEGORIES as any,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          status: DEFAULT_STATUS,
          orderAsc: false,
          limit: 100,
        };
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

        // Both calls share the same `where` (only orderBy/limit differ), so their
        // aggregate counts should agree — take the max defensively rather than
        // assuming a specific one always resolves first.
        const urgentCount = urgentResp?.data?.recommendation_aggregate?.aggregate?.count ?? 0;
        const impactCount = impactResp?.data?.recommendation_aggregate?.aggregate?.count ?? 0;
        setTotalFindingsCount(Math.max(urgentCount, impactCount));
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
    if (!scopeReady) return;
    let cancelled = false;
    setSavingsLoading(true);

    (async () => {
      try {
        const rows: any = await recommendationApi.getK8sRecommendationSummaryByRuleName({
          // Empty string means every account, which is what the unfiltered view wants.
          accountId: scopedAccountIds,
          category: NON_SECURITY_CATEGORIES as any,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          status: DEFAULT_STATUS,
        });
        if (cancelled) return;
        const rowList = Array.isArray(rows) ? rows : [];
        setTotalSavings(rowList.reduce((sum: number, row: any) => sum + (row.sum_estimated_savings || 0), 0));

        // Re-derive the same rows into a MainCategory breakdown (cost/performance/
        // security_config) instead of collapsing them into one number — `category`
        // on each row is the raw backend category (RightSizing/Configuration/…),
        // so it goes through the same mapper `transformRecommendation.ts` uses for
        // the curated list, keeping the two classifications from ever drifting apart.
        const breakdown: Record<MainCategory, { dollars: number; count: number }> = {
          cost: { dollars: 0, count: 0 },
          performance: { dollars: 0, count: 0 },
          security_config: { dollars: 0, count: 0 },
        };
        for (const row of rowList) {
          const { category } = mapCategoryAndSubCategory(row.category, row.rule_name);
          breakdown[category].dollars += row.sum_estimated_savings || 0;
          breakdown[category].count += row.count || 0;
        }
        setCategoryBreakdown(breakdown);
      } catch (err) {
        console.error('Failed to fetch savings total:', err);
      } finally {
        if (!cancelled) setSavingsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey, scopeReady]);

  // Cross-account $-at-stake ranking, only meaningful with no account already
  // selected — decoupled from the curated list's 200-row cap, unlike the
  // client-side reduction this replaces.
  useEffect(() => {
    if (accountId || !scopeReady) {
      setWorstAccountRaw(null);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const rows = await recommendationApi.getK8sRecommendationSummaryByAccount({
          accountId: scopedAccountIds,
          category: NON_SECURITY_CATEGORIES as any,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          status: DEFAULT_STATUS,
          limit: 1,
        });
        if (cancelled) return;
        const top = rows?.[0];
        setWorstAccountRaw(top && top.sum_estimated_savings > 0 ? { accountId: top.account_id, dollars: top.sum_estimated_savings } : null);
      } catch (err) {
        console.error('Failed to fetch worst account:', err);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, scopeKey, scopeReady]);

  const worstAccount = useMemo(() => {
    if (!worstAccountRaw) return null;
    return {
      accountId: worstAccountRaw.accountId,
      name: accounts[worstAccountRaw.accountId]?.account_name || worstAccountRaw.accountId,
      dollars: worstAccountRaw.dollars,
    };
  }, [worstAccountRaw, accounts]);

  // In-progress resolution count, for the insight widget's "Work in progress" tile.
  useEffect(() => {
    if (!scopeReady) return;
    // Unlike the recommendation aggregates, this endpoint drops the account
    // filter entirely for an empty array (see getRecommendationResolutionStatusCounts
    // — ResolutionsView relies on that to mean "all accounts"), so an empty scope
    // has to short-circuit here rather than be passed through as a filter.
    if (Array.isArray(scopedAccountIds) && scopedAccountIds.length === 0) {
      setWipCount(0);
      setResolvedCount(0);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const counts = await recommendationApi.getRecommendationResolutionStatusCounts({
          accountId: scopedAccountIds || undefined,
        });
        if (cancelled) return;
        setWipCount(counts?.InProgress || 0);
        setResolvedCount(counts?.Success || 0);
      } catch (err) {
        console.error('Failed to fetch work-in-progress count:', err);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey, scopeReady]);

  // Blast-radius safety, for the insight widget's "Flagged" column. Same scope and
  // status/category filters as the savings aggregate above, so its numbers sit
  // in the same universe as the rest of the widget.
  useEffect(() => {
    if (!scopeReady) return;
    let cancelled = false;
    (async () => {
      try {
        const rows = await recommendationApi.getK8sRecommendationSafetyGroups({
          accountId: scopedAccountIds,
          category: NON_SECURITY_CATEGORIES as any,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          status: DEFAULT_STATUS,
        });
        if (cancelled) return;
        const safe = (rows || []).find((r: any) => r.safety_band === 'safe');
        setSafeToApply(safe ? { count: safe.count || 0, dollars: safe.sum_estimated_savings || 0 } : null);
      } catch (err) {
        console.error('Failed to fetch blast-radius safety counts:', err);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey, scopeReady]);

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
    if (accountIds.length === 0) {
      // No accounts to fetch cost for. Once the accounts fetch has settled with
      // none (upstream unreachable / no cloud accounts), clear costLoading so the
      // Cost & Health card drops to its empty state instead of a perpetual
      // skeleton. While accounts are still loading, leave the skeleton up.
      if (accountsLoaded) setCostLoading(false);
      return;
    }
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
  }, [accounts, accountsLoaded]);

  return {
    accounts,
    insights,
    totalFindingsCount,
    loading,
    lastUpdated,
    totalSavings,
    savingsLoading,
    categoryBreakdown,
    worstAccount,
    wipCount,
    // The account set the page is currently scoped to — a single picked account,
    // or every account in the selected environment. Cross-tab links carry it so
    // a drill-down lands on the same slice the reader was looking at.
    scopedAccountIds,
    resolvedCount,
    safeToApply,
    costByCurrency,
    accountCosts,
    costLoading,
    savingsCurrency,
    savingsSymbol,
  };
}
