/**
 * useCostData — fetches AI Cost Analyser data for the current filters.
 *
 * Translates the UI `CostFilters` into the shared backend filter request and
 * fetches, on every change to an *API-backed* filter field (date / account /
 * source / model / provider / status):
 *
 *   - `usageFilters` — filter-bar option-sets        (ai_get_usage_filters)
 *   - `metrics`      — KPI totals + per-dim breakdowns (ai_aggregate_usage_metrics)
 *   - `conversations`— up to 200 rows, cost-desc       (ai_list_conversation_costs)
 *
 * Mock-only filters (trigger / assistant / template) are intentionally NOT sent
 * and do not trigger a refetch — they scope the mock-backed widgets only.
 */
import * as React from 'react';
import {
  aggregateUsageMetrics,
  getConversationTree,
  getConversationUsageMetrics,
  getUsageFilters,
  listConversationCosts,
  type ConversationCostList,
  type ConversationUsageSummary,
  type UsageDimension,
  type UsageFilterRequest,
  type UsageFilters,
  type UsageMetrics,
  type UsageTotals,
} from '@api1/ai-cost';
import { treeToRun } from './adapt';
import type { CostFilters, Run } from './types';

const OVERVIEW_DIMS: UsageDimension[] = ['source', 'model', 'agent', 'user', 'account'];
const LIST_LIMIT = 200;
const DAY_MS = 86_400_000;

export interface CostData {
  loading: boolean;
  error: string | null;
  usageFilters: UsageFilters | null;
  metrics: UsageMetrics | null;
  /** KPI totals for the immediately-preceding comparable window (for deltas). */
  prevTotals: UsageTotals | null;
  conversations: ConversationCostList | null;
  /** Whether the conversation list was truncated at LIST_LIMIT rows. */
  listCap: number;
  reload: () => void;
}

/** Previous comparable window (same length, immediately before) as RFC3339 bounds. */
function previousWindow(startDate: string, endDate: string): { startDate: string; endDate: string } {
  const start = Date.parse(`${startDate}T00:00:00Z`);
  const end = Date.parse(`${endDate}T00:00:00Z`);
  const lenDays = Math.max(1, Math.round((end - start) / DAY_MS) + 1);
  const prevEndMs = start - DAY_MS;
  const prevStartMs = prevEndMs - (lenDays - 1) * DAY_MS;
  const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);
  return { startDate: `${iso(prevStartMs)}T00:00:00Z`, endDate: `${iso(prevEndMs)}T23:59:59Z` };
}

function toFilterRequest(accountId: string | undefined, f: CostFilters): UsageFilterRequest {
  return {
    accountIds: accountId ? [accountId] : [],
    startDate: `${f.startDate}T00:00:00Z`,
    endDate: `${f.endDate}T23:59:59Z`,
    sources: f.sources ?? [],
    models: f.models,
    providers: f.providers,
    statuses: f.statuses,
  };
}

export function useCostData(accountId: string | undefined, filters: CostFilters): CostData {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [usageFilters, setUsageFilters] = React.useState<UsageFilters | null>(null);
  const [metrics, setMetrics] = React.useState<UsageMetrics | null>(null);
  const [prevTotals, setPrevTotals] = React.useState<UsageTotals | null>(null);
  const [conversations, setConversations] = React.useState<ConversationCostList | null>(null);
  const [nonce, setNonce] = React.useState(0);

  // The filter-bar option-sets depend ONLY on account + date window (not on the
  // selected model/provider/source/status), so they get their own fetch keyed on
  // just those — otherwise every filter click would needlessly re-query identical
  // option-sets (a wasted round-trip on a slow DB link).
  const filtersKey = JSON.stringify({ accountId: accountId ?? '', startDate: filters.startDate, endDate: filters.endDate, nonce });

  // The content (metrics / conversations / prev-period) depends on every
  // API-backed filter field.
  const dataKey = JSON.stringify({
    accountId: accountId ?? '',
    startDate: filters.startDate,
    endDate: filters.endDate,
    granularity: filters.granularity,
    sources: filters.sources ?? [],
    models: filters.models,
    providers: filters.providers,
    statuses: filters.statuses,
    nonce,
  });

  // Filter-bar options — refetch only when account/date changes. Non-critical:
  // a failure leaves the dropdowns empty but doesn't break the content view.
  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    const req = toFilterRequest(accountId, filters);
    getUsageFilters({ accountIds: req.accountIds, startDate: req.startDate, endDate: req.endDate }, controller.signal)
      .then((uf) => {
        if (!cancelled) setUsageFilters(uf);
      })
      .catch(() => {
        /* options are supplementary — don't surface as a page error */
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filtersKey]);

  // Content — metrics + conversation list + prev-period totals.
  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setLoading(true);
      setError(null);
      const req = toFilterRequest(accountId, filters);
      const prev = previousWindow(filters.startDate, filters.endDate);
      try {
        const [m, cl, pm] = await Promise.all([
          aggregateUsageMetrics({ ...req, groupBy: OVERVIEW_DIMS, topN: 15, granularity: filters.granularity }, controller.signal),
          listConversationCosts({ ...req, sortBy: 'cost', sortDir: 'desc', limit: LIST_LIMIT, offset: 0 }, controller.signal),
          aggregateUsageMetrics({ ...req, startDate: prev.startDate, endDate: prev.endDate, groupBy: [], topN: 0 }, controller.signal),
        ]);
        if (cancelled) return;
        setMetrics(m);
        setConversations(cl);
        setPrevTotals(pm?.totals ?? null);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load cost data');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    run();
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataKey]);

  return {
    loading,
    error,
    usageFilters,
    metrics,
    prevTotals,
    conversations,
    listCap: LIST_LIMIT,
    reload: () => setNonce((n) => n + 1),
  };
}

export interface ConversationTreeData {
  loading: boolean;
  error: string | null;
  run: Run | null;
  /** Legacy per-conversation summary (requests / cache / success / latency split). */
  usage: ConversationUsageSummary | null;
}

/**
 * Fetch one conversation's detail for the drill-down: the full tree (adapted into
 * a rich `Run`) plus the legacy usage-metrics summary (basic panel), in parallel.
 */
export function useConversationTree(accountId: string | undefined, sessionId: string | null): ConversationTreeData {
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [run, setRun] = React.useState<Run | null>(null);
  const [usage, setUsage] = React.useState<ConversationUsageSummary | null>(null);

  React.useEffect(() => {
    if (!sessionId || !accountId) {
      setRun(null);
      setUsage(null);
      return;
    }
    const controller = new AbortController();
    let cancelled = false;

    const fetchDetail = async () => {
      setLoading(true);
      setError(null);
      try {
        // Tree drives the view; usage-metrics is supplementary — don't fail the
        // whole view if the legacy action errors.
        const [tree, summary] = await Promise.all([
          getConversationTree({ conversationId: sessionId, accountId }, controller.signal),
          getConversationUsageMetrics({ conversationId: sessionId, accountId }, controller.signal).catch(() => null),
        ]);
        if (cancelled) return;
        setRun(tree ? treeToRun(tree) : null);
        setUsage(summary);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load conversation');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    fetchDetail();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [accountId, sessionId]);

  return { loading, error, run, usage };
}
