/**
 * KeysView — the AI Gateway's Keys sub-tab.
 *
 * A per-API-key rollup over `metrics.breakdowns.api_key`: key name, owner, spend
 * (relative severity dot), tokens (in/out), requests, cache-hit %, and avg latency.
 * Each key links to the Requests tab scoped to that key (see `onSelectKey`), the
 * same drill-in the Users tab offers per user. Non-admins only ever receive their
 * own keys (the gateway scopes the aggregate to the caller).
 *
 * A revoked key's record is deleted, so the gateway falls back to the key id for
 * its name; those rows read "Deleted key".
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

interface KeysViewProps {
  metrics: GatewayUsageMetrics | null;
  loading: boolean;
  error: string | null;
  /** Drill-in: scope the Requests tab to this key and jump to it. */
  onSelectKey: (keyId: string, keyName: string) => void;
}

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  key: <HeaderLabel label='API key' info='The API key the requests were sent with. Click to view its requests.' />,
  owner: <HeaderLabel label='Owner' info='The user who owns the key.' />,
  cost: <HeaderLabel label='Cost' info='Total gateway spend across this key’s requests in range.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Input / output tokens across this key’s requests.' />,
  requests: <HeaderLabel label='Requests' info='Requests sent with this key in range.' />,
  cache: <HeaderLabel label='Cache hit' info='Share of input tokens served from the prompt cache (higher is cheaper).' />,
  latency: <HeaderLabel label='Avg latency' info='Request-weighted average end-to-end latency for this key.' />,
};

const HEADERS = [
  { name: 'API key', width: '22%', sortEnabled: true, component: H.key },
  { name: 'Owner', width: '15%', sortEnabled: true, component: H.owner },
  { name: 'Cost', width: '12%', align: 'right' as const, sortEnabled: true, component: H.cost },
  { name: 'Tokens', width: '14%', align: 'right' as const, sortEnabled: true, component: H.tokens },
  { name: 'Requests', width: '11%', align: 'right' as const, sortEnabled: true, component: H.requests },
  { name: 'Cache hit', width: '11%', align: 'right' as const, sortEnabled: true, component: H.cache },
  { name: 'Avg latency', width: '15%', align: 'right' as const, sortEnabled: true, component: H.latency },
];

// A deleted key's name falls back to its id on the server.
const isDeleted = (r: GatewayGroupRow) => Boolean(r.id) && r.key === r.id;
const keyLabel = (r: GatewayGroupRow) => (isDeleted(r) ? `Deleted key (${(r.id as string).slice(0, 8)})` : r.key || 'unknown');

// Header name → the value a column sorts on (client-side reorder of loaded rows).
const SORT_VALUE: Record<string, (r: GatewayGroupRow) => number | string> = {
  'API key': (r) => keyLabel(r),
  Owner: (r) => r.owner || '',
  Cost: (r) => r.cost_usd,
  Tokens: (r) => r.input_tokens,
  Requests: (r) => r.requests,
  'Cache hit': (r) => r.cache_hit_rate_pct,
  'Avg latency': (r) => r.avg_latency_seconds,
};

function toRow(r: GatewayGroupRow, costSev: (v: number) => Severity, latSev: (v: number) => Severity, onSelectKey: KeysViewProps['onSelectKey']) {
  const name = keyLabel(r);
  const latencyMs = (r.avg_latency_seconds ?? 0) * 1000;
  return [
    {
      data: name,
      component: r.id ? (
        <Box
          component='button'
          type='button'
          title={`View requests sent with ${name}`}
          onClick={() => onSelectKey(r.id as string, name)}
          id={`gateway-key-link-${r.id}`}
          sx={{
            all: 'unset',
            cursor: 'pointer',
            boxSizing: 'border-box',
            width: '100%',
            textAlign: 'left',
            color: isDeleted(r) ? 'var(--ds-gray-500)' : 'var(--ds-blue-600)',
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
        <Box sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>{name}</Box>
      ),
    },
    { data: r.owner || '', component: <Box sx={{ ...numCell, overflowWrap: 'anywhere' }}>{r.owner || '—'}</Box> },
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

export function KeysView({ metrics, loading, error, onSelectKey }: KeysViewProps) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: 'Cost', order: 'desc' });

  const rows = React.useMemo(() => metrics?.breakdowns?.api_key ?? [], [metrics]);

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
  const tableData = React.useMemo(() => sortedRows.map((r) => toRow(r, costSev, latSev, onSelectKey)), [sortedRows, costSev, latSev, onSelectKey]);

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
        title='No API key activity'
        description='No requests were sent with an API key in this range. Try widening the date range.'
      />
    );
  }

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          API keys by cost · each links to its requests · latency is request-weighted average end-to-end time
        </Box>
        <CustomTable2
          id='gateway-keys-table'
          headers={HEADERS}
          tableData={tableData}
          sort={sort}
          onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
        />
      </Box>
    </Card>
  );
}

export default KeysView;
