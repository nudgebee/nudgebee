/**
 * Screen 6 — Users leaderboard (cross-conversation).
 *
 * Backed by `ai_aggregate_usage_metrics` with `group_by: ["user"]`: a per-user
 * rollup across every conversation in range. One row per user, showing spend
 * (cost), volume (input / output tokens, requests, conversations), efficiency
 * (cache-hit %) and responsiveness (avg model latency).
 *
 * Same self-contained pattern as AgentsView / ToolsView (own fetch + loading /
 * error), rendered through `@shared/tables/CustomTable`. The leaderboard is the
 * server's top-100 users by cost; the column-header sort reorders those loaded
 * rows client-side (covers every column).
 *
 * This view deliberately does NOT honour the global `userId` scope — a user
 * leaderboard filtered to one user is meaningless. Clicking a user row sets that
 * scope on the report and jumps to Conversations (see `onSelectUser`). The
 * breakdown rows are keyed by display NAME; we resolve name → user_id via the
 * filter option-set so the drill-in can scope by id. A row whose name doesn't
 * resolve renders as plain text (no drill).
 */
import * as React from 'react';
import { Box } from '@mui/material';
import CustomTable2 from '@shared/tables/CustomTable';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import HeaderLabel from '../components/HeaderLabel';
import { fmtTokens } from '../format';
import { makeSeverity, SeverityCell, type Severity } from '../components/severity';
import { aggregateUsageMetrics, type UsageGroupRow, type UsageFilterOption } from '@api1/ai-cost';
import type { CostFilters } from '../types';

interface UsersViewProps {
  accountId?: string;
  filters: CostFilters;
  /** {id, name} list for resolving a leaderboard row's display name → user_id. */
  userOptions?: UsageFilterOption[];
  /** Drill-in: scope the report to this user and jump to their conversations. */
  onSelectUser: (userId: string, userName: string) => void;
}

const secs = (s: number) => `${s.toFixed(1)}s`;
const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  user: (
    <HeaderLabel
      label='User'
      info='The user who initiated the conversations. Aggregated across every conversation in range. Click to view their conversations.'
    />
  ),
  cost: <HeaderLabel label='Cost' info='Total model spend across this user’s conversations in range.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Input / output tokens across this user’s model calls.' />,
  requests: <HeaderLabel label='Requests' info='Model (LLM) API calls across this user’s conversations.' />,
  conversations: <HeaderLabel label='Convs' info='Distinct conversations this user initiated in range.' />,
  cache: <HeaderLabel label='Cache hit' info='Share of input tokens served from the prompt cache (higher is cheaper).' />,
  latency: <HeaderLabel label='Avg latency' info='Average model round-trip time per call (not wall-clock).' />,
};

const HEADERS = [
  { name: 'User', width: '26%', sortEnabled: true, component: H.user },
  { name: 'Cost', width: '12%', align: 'right' as const, sortEnabled: true, component: H.cost },
  { name: 'Tokens', width: '15%', align: 'right' as const, sortEnabled: true, component: H.tokens },
  { name: 'Requests', width: '11%', align: 'right' as const, sortEnabled: true, component: H.requests },
  { name: 'Convs', width: '10%', align: 'right' as const, sortEnabled: true, component: H.conversations },
  { name: 'Cache hit', width: '11%', align: 'right' as const, sortEnabled: true, component: H.cache },
  { name: 'Avg latency', width: '15%', align: 'right' as const, sortEnabled: true, component: H.latency },
];

// Header name → the value a column sorts on (client-side reorder of loaded rows).
const SORT_VALUE: Record<string, (r: UsageGroupRow) => number | string> = {
  User: (r) => r.key || '',
  Cost: (r) => r.cost_usd,
  Tokens: (r) => r.input_tokens,
  Requests: (r) => r.requests,
  Convs: (r) => r.conversations,
  'Cache hit': (r) => r.cache_hit_rate_pct,
  'Avg latency': (r) => r.avg_latency_seconds,
};

function toRow(r: UsageGroupRow, userId: string | undefined, costSev: (v: number) => Severity, onSelectUser: UsersViewProps['onSelectUser']) {
  const name = r.key || 'unknown';
  return [
    {
      data: name,
      // Clickable only when we can resolve the name to a user_id (the value the
      // report scopes on). Otherwise plain text — never a dead link.
      component: userId ? (
        <Box
          component='button'
          type='button'
          title={`View ${name}’s conversations`}
          onClick={() => onSelectUser(userId, name)}
          id={`user-link-${userId}`}
          sx={{
            all: 'unset',
            cursor: 'pointer',
            boxSizing: 'border-box',
            width: '100%',
            textAlign: 'left',
            color: 'var(--ds-blue-600)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            overflowWrap: 'anywhere',
            '&:hover': { textDecoration: 'underline' },
            '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: 2, borderRadius: 'var(--ds-radius-sm)' },
          }}
        >
          {name}
        </Box>
      ) : (
        <Box
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)', overflowWrap: 'anywhere' }}
        >
          {name}
        </Box>
      ),
    },
    {
      align: 'right' as const,
      data: r.cost_usd,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <SeverityCell severity={costSev(r.cost_usd)} metric='cost'>
            <CostCallout value={r.cost_usd} size='sm' tone='neutral' fractionDigits={3} />
          </SeverityCell>
        </Box>
      ),
    },
    {
      align: 'right' as const,
      data: r.input_tokens,
      component: (
        <Box sx={{ ...numCell, textAlign: 'right' }}>
          {fmtTokens(r.input_tokens)} / {fmtTokens(r.output_tokens)}
        </Box>
      ),
    },
    { align: 'right' as const, data: r.requests, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{r.requests.toLocaleString()}</Box> },
    {
      align: 'right' as const,
      data: r.conversations,
      component: <Box sx={{ ...numCell, textAlign: 'right' }}>{r.conversations.toLocaleString()}</Box>,
    },
    {
      align: 'right' as const,
      data: r.cache_hit_rate_pct,
      component: <Box sx={{ ...numCell, textAlign: 'right' }}>{r.cache_hit_rate_pct.toFixed(0)}%</Box>,
    },
    {
      align: 'right' as const,
      data: r.avg_latency_seconds,
      component: <Box sx={{ ...numCell, textAlign: 'right' }}>{secs(r.avg_latency_seconds)}</Box>,
    },
  ];
}

export function UsersView({ accountId, filters, userOptions = [], onSelectUser }: UsersViewProps) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: '', order: 'asc' });
  const [state, setState] = React.useState<{ loading: boolean; error: string | null; rows: UsageGroupRow[] }>({
    loading: false,
    error: null,
    rows: [],
  });

  // display name → user_id, from the filter option-set (built over the same window).
  const idByName = React.useMemo(() => new Map(userOptions.map((u) => [u.name, u.id])), [userOptions]);

  const sourcesKey = (filters.sources ?? []).join(',');
  const modelsKey = (filters.models ?? []).join(',');
  const providersKey = (filters.providers ?? []).join(',');
  const statusesKey = (filters.statuses ?? []).join(',');

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    aggregateUsageMetrics(
      {
        accountIds: accountId ? [accountId] : [],
        startDate: `${filters.startDate}T00:00:00Z`,
        endDate: `${filters.endDate}T23:59:59Z`,
        sources: filters.sources ?? [],
        models: filters.models,
        providers: filters.providers,
        statuses: filters.statuses,
        // Intentionally NOT scoped by filters.userId — this IS the per-user view.
        groupBy: ['user'],
        topN: 100,
      },
      controller.signal
    )
      .then((m) => {
        if (!cancelled) setState({ loading: false, error: null, rows: m?.breakdowns?.user ?? [] });
      })
      .catch((e) => {
        if (!cancelled) setState({ loading: false, error: e instanceof Error ? e.message : 'Failed to load users', rows: [] });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, filters.startDate, filters.endDate, sourcesKey, modelsKey, providersKey, statusesKey]);

  // Client-side reorder of the loaded rows. Empty sort.name = keep server order (cost-desc).
  const sortedRows = React.useMemo(() => {
    const get = sort.name ? SORT_VALUE[sort.name] : undefined;
    if (!get) return state.rows;
    const arr = [...state.rows].sort((a, b) => {
      const av = get(a);
      const bv = get(b);
      const comparison = typeof av === 'string' ? String(av).localeCompare(String(bv)) : (av as number) - (bv as number);
      return sort.order === 'desc' ? -comparison : comparison;
    });
    return arr;
  }, [state.rows, sort]);

  const costSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.cost_usd)), [sortedRows]);
  const tableData = React.useMemo(
    () => sortedRows.map((r) => toRow(r, idByName.get(r.key || ''), costSev, onSelectUser)),
    [sortedRows, idByName, costSev, onSelectUser]
  );
  const showEmpty = !state.loading && !state.error && state.rows.length === 0;

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        {!state.loading && (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            Top {state.rows.length} users by cost · each links to their conversations · latency is average model time (not wall-clock)
          </Box>
        )}

        {state.error && <Banner tone='critical' title='Could not load users' message={state.error} />}

        {showEmpty ? (
          <EmptyState
            size='section'
            illustration='no-results'
            title='No user activity'
            description='Try widening the date range or clearing a filter.'
          />
        ) : (
          !state.error && (
            <CustomTable2
              id='users-leaderboard'
              headers={HEADERS}
              tableData={tableData}
              loading={state.loading}
              sort={sort}
              onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
            />
          )
        )}
      </Box>
    </Card>
  );
}

export default UsersView;
