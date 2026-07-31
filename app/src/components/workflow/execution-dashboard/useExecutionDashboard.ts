import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import apiWorkflow from '@api1/workflow';
import apiHome from '@api1/home';
import apiUser from '@api1/user';
import type { AccountExecutionItem, AccountExecutionListRequest, ExecutionAggregateResponse } from '@api1/workflow/types';
import { isExecutionCompleted } from '../utils/executionStatus';
import { getUserSession } from '@lib/auth';
import {
  AUTOMATION_OPTIONS_LIMIT,
  DASHBOARD_POLL_INTERVAL_MS,
  DEFAULT_PAGE_SIZE,
  DEFAULT_RANGE_DAYS,
  MAX_PAGEABLE_ROWS,
  SYSTEM_USER_ID,
  SYSTEM_USER_LABEL,
  TOP_FAILED_LIMIT,
  executionUserLabel,
} from './constants';

export interface ExecutionDashboardFilters {
  startDate: Date;
  endDate: Date;
  /** Empty means every account the caller can read — the tab is tenant-level. */
  accountIds: string[];
  workflowIds: string[];
  triggeredBy: string[];
  statuses: string[];
}

export interface FilterOption {
  value: string;
  label: string;
}

const DAY_MS = 24 * 60 * 60 * 1000;

const toCsv = (values: string[]) => (values.length ? values.join(',') : undefined);
const fromCsv = (raw: unknown): string[] => (typeof raw === 'string' && raw ? raw.split(',').filter(Boolean) : []);

const parseDate = (raw: unknown, fallback: Date): Date => {
  if (typeof raw !== 'string' || !raw) return fallback;
  const parsed = new Date(raw);
  return Number.isNaN(parsed.getTime()) ? fallback : parsed;
};

/**
 * Owns the dashboard's filter state, URL sync and data fetching.
 *
 * Two requests back the page and they refresh on different triggers: the table
 * on every filter/page change (and on a poll while something is running), and
 * the summary exactly once per account on mount. Filtering the table never
 * refetches the summary.
 */
export function useExecutionDashboard() {
  const router = useRouter();

  const [filters, setFilters] = useState<ExecutionDashboardFilters>(() => ({
    startDate: new Date(Date.now() - DEFAULT_RANGE_DAYS * DAY_MS),
    endDate: new Date(),
    accountIds: [],
    workflowIds: [],
    triggeredBy: [],
    statuses: [],
  }));
  // id -> display name for the Account filter and the table's Account column.
  const [accounts, setAccounts] = useState<Record<string, { name: string; cloud_provider: string }>>({});
  const [page, setPage] = useState(1);
  // Seed from the user's saved table page size — the same preference every
  // other table in the app reads, written by CustomTablePagination when the
  // user picks a size. DEFAULT_PAGE_SIZE only applies when none is stored.
  const [pageSize, setPageSize] = useState(() => apiUser.getUserPreferencesTablePageSize() ?? DEFAULT_PAGE_SIZE);
  // The size this session started at. `size` is only worth putting in the URL
  // when it differs from that, so a shared link doesn't carry the viewer's own
  // preference as if it were an explicit choice.
  const initialPageSizeRef = useRef(pageSize);

  const [executions, setExecutions] = useState<AccountExecutionItem[]>([]);
  const [totalRows, setTotalRows] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [aggregate, setAggregate] = useState<ExecutionAggregateResponse | null>(null);
  const [automationOptions, setAutomationOptions] = useState<FilterOption[]>([]);
  const [tenantUserOptions, setTenantUserOptions] = useState<FilterOption[]>([]);
  // Deepest page a forward cursor is known for. Random-access paging is capped
  // server-side (MaxExecutionDeepPageRows), but the token path is not — so
  // every page the user walks to extends how far the pager may reach.
  const [deepestTokenPage, setDeepestTokenPage] = useState(1);

  const isMountedRef = useRef(true);
  const requestIdRef = useRef(0);
  // Cursor for each page we have already walked to. Temporal only hands out
  // forward-only tokens; where one is known it is used, otherwise the server
  // falls back to a single over-fetch for that page.
  const pageTokensRef = useRef<Record<number, string>>({});
  // State, not a ref: the sync effect below must not run in the same commit as
  // the seed. With a ref it would see "seeded" while `filters` still held the
  // defaults, and immediately write those defaults over the URL it was meant
  // to read — two replaces in one tick, which Next.js reports as
  // "Cancel rendering route".
  const [seeded, setSeeded] = useState(false);
  // Flipped by updateFilters / changePage. Gates the URL sync below so the
  // hook never writes to the URL on mount.
  const userChangedFiltersRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  // Seed once from the URL so a shared link reproduces the same view. Runs
  // after router.isReady because router.query is empty on first render.
  useEffect(() => {
    if (!router.isReady || seeded) return;
    const { from, to, accounts: accountsParam, accountId, automations, users, statuses, page: pageParam, size } = router.query;
    setFilters((prev) => ({
      startDate: parseDate(from, prev.startDate),
      endDate: parseDate(to, prev.endDate),
      accountIds: fromCsv(accountsParam).length ? fromCsv(accountsParam) : fromCsv(accountId),
      workflowIds: fromCsv(automations),
      triggeredBy: fromCsv(users),
      statuses: fromCsv(statuses),
    }));
    const parsedPage = Number(pageParam);
    if (Number.isFinite(parsedPage) && parsedPage > 0) setPage(parsedPage);
    const parsedSize = Number(size);
    if (Number.isFinite(parsedSize) && parsedSize > 0) setPageSize(parsedSize);
    setSeeded(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.isReady, seeded]);

  // Mirror state back into the URL, but only once the user has actually
  // changed something.
  //
  // /automation has two other owners of its URL: withAccountGuard appends
  // accountId, and AnchorComponent#manageRoute owns the `#<tab>` hash. Writing
  // on mount raced both of them — the replace landed between their
  // navigations, dropped the hash, and bounced the page back to the first tab.
  // Nothing is lost by staying quiet until there is a user choice to record.
  useEffect(() => {
    if (!router.isReady || !userChangedFiltersRef.current) return;

    const query: Record<string, string> = {};
    Object.entries(router.query).forEach(([key, value]) => {
      if (typeof value === 'string') query[key] = value;
    });

    const nextParams: Record<string, string | undefined> = {
      from: filters.startDate.toISOString(),
      to: filters.endDate.toISOString(),
      accounts: toCsv(filters.accountIds),
      automations: toCsv(filters.workflowIds),
      users: toCsv(filters.triggeredBy),
      statuses: toCsv(filters.statuses),
      page: page > 1 ? String(page) : undefined,
      size: pageSize !== initialPageSizeRef.current ? String(pageSize) : undefined,
    };
    // Delete rather than assign undefined: Next serializes an undefined query
    // value as a bare `key=`, which would also make this effect see a change
    // on every render and replace forever.
    Object.entries(nextParams).forEach(([key, value]) => {
      if (value) {
        query[key] = value;
      } else {
        delete query[key];
      }
    });

    // Read the hash from the live location rather than router.asPath, which
    // can already have been rewritten by one of the owners above.
    const hash = typeof window === 'undefined' ? '' : window.location.hash.replace('#', '');
    router.replace(
      {
        pathname: router.pathname,
        query,
        ...(hash ? { hash } : {}),
      },
      undefined,
      { shallow: true }
    );
    // router is intentionally omitted — including it re-runs on every replace.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters, page, pageSize, router.isReady]);

  const filterRequest = useMemo(
    () => ({
      start_date: filters.startDate.toISOString(),
      end_date: filters.endDate.toISOString(),
      workflow_ids: filters.workflowIds.length ? filters.workflowIds : undefined,
      triggered_by: filters.triggeredBy.length ? filters.triggeredBy : undefined,
      statuses: filters.statuses.length ? filters.statuses : undefined,
    }),
    [filters]
  );

  const fetchExecutions = useCallback(
    async (isPolling = false) => {
      // Wait for the URL seed, otherwise a shared link fetches the default
      // range first and immediately refetches the real one.
      if (!seeded) return;
      const requestId = ++requestIdRef.current;
      if (!isPolling) setLoading(true);

      const request: AccountExecutionListRequest = {
        ...filterRequest,
        limit: pageSize,
        page,
      };
      const knownToken = pageTokensRef.current[page];
      if (knownToken) request.next_page_token = knownToken;

      const response = await apiWorkflow.listAccountExecutions(filters.accountIds, request);
      // Drop a response the user has already navigated away from.
      if (!isMountedRef.current || requestId !== requestIdRef.current) return;

      const payload = response?.data?.executions_list;
      if (!payload) {
        setError(response?.errors?.[0]?.message || 'Failed to load executions');
        setExecutions([]);
        setTotalRows(0);
      } else {
        setError(null);
        setExecutions(payload.executions || []);
        setTotalRows(payload.total_count || 0);
        if (payload.next_page_token) {
          pageTokensRef.current[page + 1] = payload.next_page_token;
          setDeepestTokenPage((prev) => Math.max(prev, page + 1));
        }
      }
      if (!isPolling) setLoading(false);
    },
    [filters.accountIds, filterRequest, page, pageSize, seeded]
  );

  /**
   * The summary is a fixed header, not a reflection of the table: it is sent
   * with no filters and no date bounds, so it covers everything Temporal still
   * holds — i.e. exactly the namespace retention window. Deliberately does not
   * depend on `filterRequest`, so filtering the table costs zero extra calls.
   */
  const fetchAggregate = useCallback(async () => {
    const response = await apiWorkflow.aggregateExecutions(filters.accountIds, { top_failed_limit: TOP_FAILED_LIMIT });
    if (!isMountedRef.current) return;
    setAggregate(response?.data?.executions_aggregate || null);
  }, [filters.accountIds]);

  useEffect(() => {
    fetchExecutions();
  }, [fetchExecutions]);

  useEffect(() => {
    fetchAggregate();
  }, [fetchAggregate]);

  // Options for the Automation filter. Fetched once per account — the filter
  // lists automations, not executions, so it doesn't move with the date range.
  useEffect(() => {
    let cancelled = false;
    apiWorkflow.listWorkflows(filters.accountIds, undefined, undefined, undefined, AUTOMATION_OPTIONS_LIMIT).then((response: any) => {
      if (cancelled || !isMountedRef.current) return;
      const workflows = response?.data?.workflow_list?.workflows || [];
      setAutomationOptions(workflows.map((workflow: any) => ({ value: workflow.id, label: workflow.name || workflow.id })));
    });
    return () => {
      cancelled = true;
    };
  }, [filters.accountIds]);

  // Accounts the caller can see, for the Account filter and the Account column.
  useEffect(() => {
    apiHome
      .getCloudAccounts()
      .then((response: any) => {
        if (!isMountedRef.current || !Array.isArray(response)) return;
        setAccounts(Object.fromEntries(response.map((a: any) => [a.id, { name: a.account_name, cloud_provider: a.cloud_provider || '' }])));
      })
      .catch(() => {
        /* names degrade to ids */
      });
  }, []);

  const accountOptions = useMemo(
    () =>
      Object.entries(accounts).map(([id, info]) => ({
        label: info.name || id,
        value: id,
        group: (info.cloud_provider || '').toUpperCase() || 'Other',
      })),
    [accounts]
  );

  // Poll the table only, and only while something is actually running.
  const hasRunningExecution = executions.some((execution) => !isExecutionCompleted(execution.status));
  useEffect(() => {
    if (!hasRunningExecution) return;
    const timer = setInterval(() => fetchExecutions(true), DASHBOARD_POLL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [hasRunningExecution, fetchExecutions]);

  /**
   * The tenant's users, which is what the User filter should offer — the
   * visible page is not a complete list of who has ever triggered a run.
   * Only tenant-level roles may read the tenant user list, so for everyone
   * else this stays empty and the filter falls back to the page-derived ids
   * below.
   */
  useEffect(() => {
    const roles: string[] = getUserSession()?.roles || [];
    if (!roles.includes('tenant_admin') && !roles.includes('tenant_admin_readonly')) return;
    let cancelled = false;
    apiUser
      .listUsers({ status: 'active' })
      .then((response: any) => {
        if (cancelled || !isMountedRef.current) return;
        const rows = Array.isArray(response?.data) ? response.data : [];
        setTenantUserOptions(
          rows.filter((user: any) => user?.id).map((user: any) => ({ value: user.id, label: user.display_name || user.username || user.id }))
        );
      })
      .catch(() => {
        /* falls back to the page-derived ids */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  /**
   * Options for the User filter: System first (no person triggers a scheduled
   * run, yet most rows are one), then every tenant user, then any id seen on
   * the current page that the tenant list did not cover — a deactivated user,
   * or the whole list when the caller may not read it. Already-selected ids
   * are kept so a selection never vanishes from its own dropdown.
   */
  const userOptions = useMemo<FilterOption[]>(() => {
    const byId = new Map<string, string>();
    byId.set(SYSTEM_USER_ID, SYSTEM_USER_LABEL);
    tenantUserOptions.forEach((option) => byId.set(option.value, option.label));
    executions.forEach((execution) => {
      if (execution.triggered_by && !byId.has(execution.triggered_by)) {
        byId.set(execution.triggered_by, executionUserLabel(execution.user_name, execution.triggered_by));
      }
    });
    filters.triggeredBy.forEach((id) => {
      if (!byId.has(id)) byId.set(id, executionUserLabel(undefined, id));
    });
    return Array.from(byId, ([value, label]) => ({ value, label }));
  }, [tenantUserOptions, executions, filters.triggeredBy]);

  /**
   * How many rows the pager may address.
   *
   * The server refuses a random-access jump past MaxExecutionDeepPageRows
   * (Temporal has no OFFSET, so page N without a cursor costs an N*limit
   * over-fetch). Paging *forward* with a cursor has no such cost and no such
   * cap, so every page walked to raises this ceiling — the user can keep
   * stepping past the first 1000 rows, they just cannot jump there directly.
   */
  const pageableRows = useMemo(
    () => Math.min(totalRows, Math.max(MAX_PAGEABLE_ROWS, deepestTokenPage * pageSize)),
    [totalRows, deepestTokenPage, pageSize]
  );

  // Any filter change invalidates the cursor ledger and returns to page 1.
  const updateFilters = useCallback((update: Partial<ExecutionDashboardFilters>) => {
    userChangedFiltersRef.current = true;
    pageTokensRef.current = {};
    setDeepestTokenPage(1);
    setPage(1);
    setFilters((prev) => ({ ...prev, ...update }));
  }, []);

  const changePage = useCallback(
    (nextPage: number, nextPageSize?: number) => {
      userChangedFiltersRef.current = true;
      // Resizing the page invalidates every cursor, which are per page size.
      if (nextPageSize && nextPageSize !== pageSize) {
        pageTokensRef.current = {};
        setDeepestTokenPage(1);
        setPageSize(nextPageSize);
      }
      setPage(nextPage);
    },
    [pageSize]
  );

  const refresh = useCallback(() => {
    pageTokensRef.current = {};
    setDeepestTokenPage(1);
    fetchExecutions();
    fetchAggregate();
  }, [fetchExecutions, fetchAggregate]);

  return {
    filters,
    updateFilters,
    page,
    pageSize,
    changePage,
    executions,
    totalRows,
    pageableRows,
    loading,
    error,
    aggregate,
    automationOptions,
    userOptions,
    accounts,
    accountOptions,
    refresh,
  };
}

export default useExecutionDashboard;
