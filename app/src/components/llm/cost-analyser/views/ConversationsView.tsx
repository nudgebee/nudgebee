/**
 * Screen 2 — Conversations & Workflows explorer (spec §5).
 *
 * Backed by `ai_list_conversation_costs`. The default ("All") view is paged
 * SERVER-side: sort and offset go to the backend and the row count shown is the
 * filter-wide total, so counts stay comparable when an account filter is applied.
 * It used to fetch one capped top-200 window and paginate it in the browser,
 * which reported "200 results" for both an unfiltered tenant of 761 and a single
 * account of 745 (issue #35686).
 *
 * The preset toggles keep their own scopes: "Top 5 by X" is a 5-row server query
 * ordered by that metric, and "Show failed" filters the top-`listCap`-by-cost
 * window in the browser (the list action has no conversation-status filter).
 */
import * as React from 'react';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import apiUser from '@api1/user';
import { listConversationCosts } from '@api1/ai-cost';
import ConversationsTable, { HEADER_TO_KEY, type ConvSortKey, type ConvView } from '../components/ConversationsTable';
import { downloadFile, runsToCsv } from '../format';
import { rowToRun } from '../adapt';
import { toFilterRequest, useConversationList, type ConversationSortBy } from '../useCostData';
import type { CostFilters } from '../types';

/** Table sort key → the `sort_by` value the list action whitelists. */
const API_SORT_BY: Record<ConvSortKey, ConversationSortBy> = {
  start: 'start_time',
  trigger: 'start_time',
  cost: 'cost',
  tokens: 'tokens',
  duration: 'duration',
  latency: 'latency',
  calls: 'llm_calls',
};

/**
 * "Top 5 by X" presets → the metric they rank by. The table syncs its column sort
 * to the preset, so `sortBy` already follows; this only marks which views are the
 * fixed-size top-N ones (no pagination).
 */
const PRESET_LABEL: Partial<Record<ConvView, string>> = { cost: 'cost', latency: 'latency', calls: 'calls' };
const PRESET_ROWS = 5;

interface ConversationsViewProps {
  /** Account scope for the list query ('' / undefined = every readable account). */
  accountId: string | undefined;
  filters: CostFilters;
  /** Max rows one list request may return (server-side cap). */
  listCap: number;
  /** account_id → display name, for the per-row account label (no extra fetch). */
  accountNameById?: Record<string, string>;
  onSelectRun: (sessionId: string, accountId?: string) => void;
  /** Open the detail modal on the Optimize tab and analyze (cached or fresh). */
  onAnalyse?: (sessionId: string, accountId?: string) => void;
}

/**
 * Encodes the export's scope into its filename so the file self-documents what
 * it is: which account, which date range, and that it's the top-N-by-cost slice
 * of a larger set (issue #35732 — the old fixed name recorded none of this).
 */
function exportFilename(
  accountLabel: string | undefined,
  startDate: string | undefined,
  endDate: string | undefined,
  shown: number,
  total: number
): string {
  const slug = (s: string) =>
    s
      .replace(/[^a-z0-9]+/gi, '-')
      .replace(/^-+|-+$/g, '')
      .toLowerCase();
  const parts = ['ai-cost-conversations'];
  // Skip an all-punctuation account label — its slug is empty and would leave a "__".
  const accountSlug = accountLabel ? slug(accountLabel) : '';
  if (accountSlug) parts.push(accountSlug);
  if (startDate && endDate) parts.push(`${startDate}_to_${endDate}`);
  parts.push(total > shown ? `top${shown}-of-${total}` : `${shown}-rows`);
  return `${parts.join('_')}.csv`;
}

export function ConversationsView({ accountId, filters, listCap, accountNameById, onSelectRun, onAnalyse }: ConversationsViewProps) {
  const [view, setView] = React.useState<ConvView>('all');
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: 'Cost', order: 'desc' });
  const [page, setPage] = React.useState(1);
  const [rowsPerPage, setRowsPerPage] = React.useState(() => apiUser.getUserPreferencesTablePageSize() ?? 10);
  const [exporting, setExporting] = React.useState(false);

  const sortBy = API_SORT_BY[HEADER_TO_KEY[sort.name] ?? 'cost'];
  const sortDir = sort.order;

  // "Top 5 by X" asks the server for exactly those 5 (so it ranks the whole
  // filtered set, not a page); "Show failed" needs the wide window it filters in
  // the browser; "All" pages normally.
  const params = React.useMemo(() => {
    if (PRESET_LABEL[view]) return { sortBy, sortDir, limit: PRESET_ROWS, offset: 0 };
    if (view === 'failed') return { sortBy, sortDir, limit: listCap, offset: 0 };
    return { sortBy, sortDir, limit: rowsPerPage, offset: (page - 1) * rowsPerPage };
  }, [view, sortBy, sortDir, listCap, rowsPerPage, page]);

  const { loading, error, runs, total } = useConversationList(accountId, filters, params);

  // A filter change re-scopes the whole set — page 2 of the old scope means
  // nothing in the new one, and an out-of-range offset would show an empty table.
  const filterKey = JSON.stringify([
    accountId ?? '',
    filters.startDate,
    filters.endDate,
    filters.sources,
    filters.models,
    filters.providers,
    filters.agents,
    filters.statuses,
    filters.userId,
  ]);
  React.useEffect(() => {
    setPage(1);
  }, [filterKey]);

  const handleViewChange = (next: ConvView) => {
    setView(next);
    setPage(1);
  };
  const handleSortChange = (next: { name: string; order: 'asc' | 'desc' }) => {
    setSort(next);
    setPage(1);
  };
  const handlePageChange = (nextPage: number, nextRowsPerPage: number) => {
    setPage(nextPage);
    setRowsPerPage(nextRowsPerPage);
  };

  // Export covers the top `listCap` rows of the current sort — the page on screen
  // is only a slice of the set, so it can't be the export.
  const handleExport = async () => {
    setExporting(true);
    try {
      // Same filter mapping the list query uses, so a new CostFilters field
      // reaches the export without a second place to remember.
      const cl = await listConversationCosts({
        ...toFilterRequest(accountId, filters),
        sortBy,
        sortDir,
        limit: listCap,
        offset: 0,
      });
      const exported = (cl?.rows ?? []).map(rowToRun);
      // Filename records the export's scope — account, date range, and that it is
      // the top-N slice of the filter-wide total (issue #35732).
      const accountLabel = accountId ? accountNameById?.[accountId] : undefined;
      downloadFile(exportFilename(accountLabel, filters.startDate, filters.endDate, exported.length, total), runsToCsv(exported, accountNameById));
    } finally {
      setExporting(false);
    }
  };

  const failedCount = runs.filter((r) => r.status === 'failed').length;
  const totalLabel = `${total.toLocaleString()} conversation${total === 1 ? '' : 's'}`;
  // Hide the count caption while loading — the row counts aren't meaningful yet.
  let caption: React.ReactNode;
  if (!loading) {
    if (PRESET_LABEL[view]) caption = `Top ${runs.length} by ${PRESET_LABEL[view]} of ${totalLabel}`;
    else if (view === 'failed') caption = `${failedCount} failed within the top ${listCap.toLocaleString()} by cost · ${totalLabel} in total`;
    else caption = `${totalLabel} · click a column header to sort`;
  }

  if (error) {
    return <Banner tone='critical' title='Could not load conversations' message={error} />;
  }

  return (
    <Card>
      <ConversationsTable
        runs={runs}
        accountNameById={accountNameById}
        onSelectRun={onSelectRun}
        onAnalyse={onAnalyse}
        loading={loading}
        defaultSort={{ key: 'cost', order: 'desc' }}
        id='conversations-table'
        caption={caption}
        view={view}
        onViewChange={handleViewChange}
        sort={sort}
        onSortChange={handleSortChange}
        serverPage={view === 'all' ? { page, rowsPerPage, totalRows: total, onPageChange: handlePageChange } : undefined}
        headerActions={
          <Button
            tone='secondary'
            size='sm'
            disabled={exporting}
            icon={<FileDownloadOutlinedIcon sx={{ fontSize: 16 }} />}
            onClick={handleExport}
            id='cost-export-csv'
          >
            Export CSV
          </Button>
        }
      />
    </Card>
  );
}

export default ConversationsView;
