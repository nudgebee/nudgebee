/**
 * UsersView — the AI Gateway's Users sub-tab.
 *
 * A per-user rollup over `metrics.breakdowns.user`: spend (with a relative
 * severity dot), tokens (in/out), requests, cache-hit %, and avg latency
 * (severity dot). Each user with a resolved `id` is a blue button link that
 * scopes the Requests tab to that user (see `onSelectUser`) — mirroring the
 * cost-analyser's UsersView link styling and client-side sort pattern.
 *
 * Presentational: reads the shared `metrics` from the shell, no own fetch.
 */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import CustomTable2 from '@shared/tables/CustomTable';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import HeaderLabel from '@components/llm/cost-analyser/components/HeaderLabel';
import { fmtTokens, fmtDuration, fmtPct } from '@components/llm/cost-analyser/format';
import { makeSeverity, SeverityCell, type Severity } from '@components/llm/cost-analyser/components/severity';
import type { GatewayGroupRow, GatewayUsageMetrics } from '@api1/gateway-usage';

interface UsersViewProps {
  metrics: GatewayUsageMetrics | null;
  loading: boolean;
  error: string | null;
  /** Drill-in: scope the Requests tab to this user and jump to it. */
  onSelectUser: (userId: string, userName: string) => void;
}

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  user: <HeaderLabel label='User' info='The user whose requests were forwarded through the gateway. Click to view their requests.' />,
  cost: <HeaderLabel label='Cost' info='Total gateway spend across this user’s requests in range.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Input / output tokens across this user’s requests.' />,
  requests: <HeaderLabel label='Requests' info='Requests this user forwarded through the gateway in range.' />,
  cache: <HeaderLabel label='Cache hit' info='Share of input tokens served from the prompt cache (higher is cheaper).' />,
  latency: <HeaderLabel label='Avg latency' info='Request-weighted average end-to-end latency for this user.' />,
};

const HEADERS = [
  { name: 'User', width: '26%', sortEnabled: true, component: H.user },
  { name: 'Cost', width: '13%', align: 'right' as const, sortEnabled: true, component: H.cost },
  { name: 'Tokens', width: '16%', align: 'right' as const, sortEnabled: true, component: H.tokens },
  { name: 'Requests', width: '12%', align: 'right' as const, sortEnabled: true, component: H.requests },
  { name: 'Cache hit', width: '12%', align: 'right' as const, sortEnabled: true, component: H.cache },
  { name: 'Avg latency', width: '15%', align: 'right' as const, sortEnabled: true, component: H.latency },
];

// Header name → the value a column sorts on (client-side reorder of loaded rows).
const SORT_VALUE: Record<string, (r: GatewayGroupRow) => number | string> = {
  User: (r) => r.key || '',
  Cost: (r) => r.cost_usd,
  Tokens: (r) => r.input_tokens,
  Requests: (r) => r.requests,
  'Cache hit': (r) => r.cache_hit_rate_pct,
  'Avg latency': (r) => r.avg_latency_seconds,
};

function toRow(r: GatewayGroupRow, costSev: (v: number) => Severity, latSev: (v: number) => Severity, onSelectUser: UsersViewProps['onSelectUser']) {
  const name = r.key || 'unknown';
  const latencyMs = (r.avg_latency_seconds ?? 0) * 1000;
  return [
    {
      data: name,
      // Clickable only when the row carries a user_id (the value the Requests tab
      // scopes on). Otherwise plain text — never a dead link.
      component: r.id ? (
        <Box
          component='button'
          type='button'
          title={`View ${name}’s requests`}
          onClick={() => onSelectUser(r.id as string, name)}
          id={`gateway-user-link-${r.id}`}
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
      data: r.cache_hit_rate_pct,
      component: <Box sx={{ ...numCell, textAlign: 'right' }}>{fmtPct((r.cache_hit_rate_pct ?? 0) / 100, 0)}</Box>,
    },
    {
      align: 'right' as const,
      data: r.avg_latency_seconds,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <SeverityCell severity={latSev(latencyMs)} metric='latency'>
            <Box sx={numCell}>{fmtDuration(latencyMs)}</Box>
          </SeverityCell>
        </Box>
      ),
    },
  ];
}

export function UsersView({ metrics, loading, error, onSelectUser }: UsersViewProps) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: 'Cost', order: 'desc' });

  const rows = React.useMemo(() => metrics?.breakdowns?.user ?? [], [metrics]);

  // Client-side reorder of the loaded rows.
  const sortedRows = React.useMemo(() => {
    const get = sort.name ? SORT_VALUE[sort.name] : undefined;
    if (!get) return rows;
    return [...rows].sort((a, b) => {
      const av = get(a);
      const bv = get(b);
      const comparison = typeof av === 'string' ? String(av).localeCompare(String(bv)) : (av as number) - (bv as number);
      return sort.order === 'desc' ? -comparison : comparison;
    });
  }, [rows, sort]);

  const costSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.cost_usd)), [sortedRows]);
  const latSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => (r.avg_latency_seconds ?? 0) * 1000)), [sortedRows]);
  const tableData = React.useMemo(() => sortedRows.map((r) => toRow(r, costSev, latSev, onSelectUser)), [sortedRows, costSev, latSev, onSelectUser]);

  if (error) return <Banner tone='critical' title='Could not load gateway usage' message={error} />;

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
        <CircularProgress size={28} />
      </Box>
    );
  }

  if (!rows.length) {
    return (
      <EmptyState
        size='section'
        illustration='no-results'
        title='No user activity'
        description='Try widening the date range or selecting a different account.'
      />
    );
  }

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          Users by cost · each links to their requests · latency is request-weighted average end-to-end time
        </Box>
        <CustomTable2
          id='gateway-users-table'
          headers={HEADERS}
          tableData={tableData}
          sort={sort}
          onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
        />
      </Box>
    </Card>
  );
}

export default UsersView;
