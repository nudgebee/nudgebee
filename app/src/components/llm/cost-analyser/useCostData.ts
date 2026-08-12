/**
 * useCostData — fetches AI Cost Analyser data for the current filters.
 *
 * Translates the UI `CostFilters` into the shared backend filter request and
 * fetches, on every change to an *API-backed* filter field (date / account /
 * source / model / provider / status):
 *
 *   - `usageFilters` — filter-bar option-sets        (ai_get_usage_filters)
 *   - `metrics`      — KPI totals + per-dim breakdowns (ai_aggregate_usage_metrics)
 *   - `conversations`— Overview's top-10 "recent runs"  (ai_list_conversation_costs)
 *
 * `metrics`/`conversations` are only ever rendered by a subset of tabs (Overview,
 * Models — see METRICS_TABS/CONVERSATIONS_TABS below); Agents, Tools, Users and
 * Critiques fetch their own data independently and never read these fields. So
 * both are fetched lazily, keyed on the active `tab`, instead of unconditionally
 * on every mount — a tab that doesn't use them triggers no request. Each domain
 * remembers the filter key it was last fetched for so flipping between tabs that
 * share data (Overview ↔ Models) doesn't refetch.
 *
 * The Conversations explorer does NOT read the shared window — it pages the same
 * action server-side through `useConversationList` (below), so its row count is
 * the filter-wide total rather than a capped window, and no shared list fetch
 * runs while that tab is open.
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
import { rowToRun, treeToRun } from './adapt';
import type { CostFilters, Run } from './types';

const OVERVIEW_DIMS: UsageDimension[] = ['source', 'model', 'agent', 'user', 'account'];
const LIST_LIMIT = 200;
/** Overview's "recent runs" widget only ever renders the top 10 (OverviewView.tsx) —
 * fetch a small page there instead of the full explorer page. */
const OVERVIEW_RUNS_LIMIT = 10;
const DAY_MS = 86_400_000;

/** Tabs whose view reads `metrics` (KPI totals + dimension breakdowns + prevTotals). */
const METRICS_TABS = new Set(['overview', 'models']);
/** Tabs whose view reads `conversations`. Only Overview's "recent runs" widget —
 * the Conversations explorer pages the same action itself (`useConversationList`). */
const CONVERSATIONS_TABS = new Set(['overview']);
/** FilterBar (and its dropdown options) is hidden only on the Critiques tab. */
const FILTER_BAR_TABS_EXCLUDED = new Set(['critiques']);

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
    agents: f.agents ?? [],
    statuses: f.statuses,
    // userId intentionally applied to ALL tabs (Overview, Models, Conversations, Agents, Tools) —
    // the drill-in from UsersView sets this scope so the whole report narrows to one user.
    userId: f.userId || undefined,
  };
}

export function useCostData(accountId: string | undefined, filters: CostFilters, tab: string): CostData {
  const needUsageFilters = !FILTER_BAR_TABS_EXCLUDED.has(tab);
  const needMetrics = METRICS_TABS.has(tab);
  const needConversations = CONVERSATIONS_TABS.has(tab);

  const [error, setError] = React.useState<string | null>(null);
  const [usageFilters, setUsageFilters] = React.useState<UsageFilters | null>(null);
  const [metrics, setMetrics] = React.useState<UsageMetrics | null>(null);
  const [prevTotals, setPrevTotals] = React.useState<UsageTotals | null>(null);
  const [conversations, setConversations] = React.useState<ConversationCostList | null>(null);
  const [metricsLoading, setMetricsLoading] = React.useState(needMetrics);
  const [conversationsLoading, setConversationsLoading] = React.useState(needConversations);
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
    agents: filters.agents ?? [],
    statuses: filters.statuses,
    userId: filters.userId,
    nonce,
  });

  // Filter-bar options — refetch only when account/date changes, and only while a
  // tab that renders FilterBar is active. Non-critical: a failure leaves the
  // dropdowns empty but doesn't break the content view.
  const usageFiltersKeyRef = React.useRef<string | null>(null);
  React.useEffect(() => {
    if (!needUsageFilters || usageFiltersKeyRef.current === filtersKey) return;
    const controller = new AbortController();
    let cancelled = false;
    const req = toFilterRequest(accountId, filters);
    getUsageFilters({ accountIds: req.accountIds, startDate: req.startDate, endDate: req.endDate }, controller.signal)
      .then((uf) => {
        if (cancelled) return;
        setUsageFilters(uf);
        usageFiltersKeyRef.current = filtersKey;
      })
      .catch(() => {
        /* options are supplementary — don't surface as a page error */
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filtersKey, needUsageFilters]);

  // Metrics + prev-period totals — Overview (KPI cards + breakdown charts) and
  // Models (per-model breakdown + over-time charts) only. Cached per dataKey so
  // flipping Overview <-> Models doesn't refetch identical data.
  const metricsKeyRef = React.useRef<string | null>(null);
  React.useEffect(() => {
    if (!needMetrics || metricsKeyRef.current === dataKey) return;
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setMetricsLoading(true);
      setError(null);
      const req = toFilterRequest(accountId, filters);
      const prev = previousWindow(filters.startDate, filters.endDate);
      try {
        const [m, pm] = await Promise.all([
          aggregateUsageMetrics({ ...req, groupBy: OVERVIEW_DIMS, topN: 15, granularity: filters.granularity }, controller.signal),
          // Totals-only comparison window — no breakdown/time-series/storage needed,
          // only pm.totals is read below, so skip the (expensive) storage scan.
          aggregateUsageMetrics(
            { ...req, startDate: prev.startDate, endDate: prev.endDate, groupBy: [], topN: 0, skipStorage: true },
            controller.signal
          ),
        ]);
        if (cancelled) return;
        setMetrics(m);
        setPrevTotals(pm?.totals ?? null);
        metricsKeyRef.current = dataKey;
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load cost data');
      } finally {
        if (!cancelled) setMetricsLoading(false);
      }
    };

    run();
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataKey, needMetrics]);

  // Conversation list — Overview's top-10 "recent runs" widget only; the
  // Conversations explorer pages the same action itself (`useConversationList`).
  // Cached per dataKey so returning to Overview doesn't refetch.
  const conversationsKeyRef = React.useRef<string | null>(null);
  React.useEffect(() => {
    if (!needConversations || conversationsKeyRef.current === dataKey) return;
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setConversationsLoading(true);
      setError(null);
      const req = toFilterRequest(accountId, filters);
      try {
        const cl = await listConversationCosts({ ...req, sortBy: 'cost', sortDir: 'desc', limit: OVERVIEW_RUNS_LIMIT, offset: 0 }, controller.signal);
        if (cancelled) return;
        setConversations(cl);
        conversationsKeyRef.current = dataKey;
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load cost data');
      } finally {
        if (!cancelled) setConversationsLoading(false);
      }
    };

    run();
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataKey, needConversations]);

  return {
    loading: (needMetrics && metricsLoading) || (needConversations && conversationsLoading),
    error,
    usageFilters,
    metrics,
    prevTotals,
    conversations,
    listCap: LIST_LIMIT,
    reload: () => setNonce((n) => n + 1),
  };
}

/** Server-side sort targets accepted by `ai_list_conversation_costs`. */
export type ConversationSortBy = 'cost' | 'start_time' | 'duration' | 'llm_calls' | 'tokens' | 'latency';

export interface ConversationListParams {
  sortBy: ConversationSortBy;
  sortDir: 'asc' | 'desc';
  /** Rows per page. Capped server-side at LIST_LIMIT. */
  limit: number;
  offset: number;
}

export interface ConversationListData {
  loading: boolean;
  error: string | null;
  runs: Run[];
  /** Filter-wide conversation count — NOT the length of the fetched page. */
  total: number;
}

/**
 * The Conversations explorer's own page of rows.
 *
 * Unlike the shared `useCostData().conversations` window (a fixed top-200 slice),
 * this pages server-side: sorting and offset go to the backend and `total` is the
 * filter-wide `COUNT(DISTINCT c.id)`. That's what keeps the row count the user
 * sees comparable across account filters — the capped window made an unfiltered
 * "200" and a per-account "200" look like the same number when the real totals
 * were 761 and 745 (issue #35686).
 */
export function useConversationList(accountId: string | undefined, filters: CostFilters, params: ConversationListParams): ConversationListData {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [page, setPage] = React.useState<ConversationCostList | null>(null);

  const key = JSON.stringify({
    accountId: accountId ?? '',
    startDate: filters.startDate,
    endDate: filters.endDate,
    sources: filters.sources ?? [],
    models: filters.models,
    providers: filters.providers,
    agents: filters.agents ?? [],
    statuses: filters.statuses,
    userId: filters.userId,
    ...params,
  });

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setLoading(true);
      setError(null);
      try {
        const req = toFilterRequest(accountId, filters);
        const cl = await listConversationCosts({ ...req, ...params }, controller.signal);
        if (cancelled) return;
        setPage(cl);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load conversations');
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
  }, [key]);

  const runs = React.useMemo(() => (page?.rows ?? []).map(rowToRun), [page]);
  return { loading, error, runs, total: page?.page?.total ?? runs.length };
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
