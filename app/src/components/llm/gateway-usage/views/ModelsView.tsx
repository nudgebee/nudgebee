/**
 * ModelsView — the AI Gateway's Models sub-tab.
 *
 * A rich, client-sortable per-model economics table over
 * `metrics.breakdowns.model`: cost (with a relative-severity dot), tokens
 * (in/out), requests, cache-hit %, and avg latency (severity dot). Mirrors the
 * cost-analyser's ModelsView sort pattern — a SORT_VALUE map keyed by header name
 * reorders the loaded rows client-side (default Cost desc).
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

interface ModelsViewProps {
  metrics: GatewayUsageMetrics | null;
  loading: boolean;
  error: string | null;
}

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  model: <HeaderLabel label='Model' info='The model the gateway routed each request to (the resolved provider model).' />,
  cost: <HeaderLabel label='Cost' info='Total spend on this model across the selected window.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Input / output tokens across this model’s requests.' />,
  requests: <HeaderLabel label='Requests' info='Requests routed to this model in range.' />,
  cache: <HeaderLabel label='Cache hit' info='Share of input tokens served from the prompt cache (higher is cheaper).' />,
  latency: <HeaderLabel label='Avg latency' info='Request-weighted average end-to-end latency for this model.' />,
};

const HEADERS = [
  { name: 'Model', width: '26%', sortEnabled: true, component: H.model },
  { name: 'Cost', width: '13%', align: 'right' as const, sortEnabled: true, component: H.cost },
  { name: 'Tokens', width: '16%', align: 'right' as const, sortEnabled: true, component: H.tokens },
  { name: 'Requests', width: '12%', align: 'right' as const, sortEnabled: true, component: H.requests },
  { name: 'Cache hit', width: '12%', align: 'right' as const, sortEnabled: true, component: H.cache },
  { name: 'Avg latency', width: '15%', align: 'right' as const, sortEnabled: true, component: H.latency },
];

// Header name → the value a column sorts on (client-side reorder of loaded rows).
const SORT_VALUE: Record<string, (r: GatewayGroupRow) => number | string> = {
  Model: (r) => r.key || '',
  Cost: (r) => r.cost_usd,
  Tokens: (r) => r.input_tokens,
  Requests: (r) => r.requests,
  'Cache hit': (r) => r.cache_hit_rate_pct,
  'Avg latency': (r) => r.avg_latency_seconds,
};

function toRow(r: GatewayGroupRow, costSev: (v: number) => Severity, latSev: (v: number) => Severity) {
  const latencyMs = (r.avg_latency_seconds ?? 0) * 1000;
  return [
    {
      data: r.key || '—',
      component: (
        <Box
          sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)', overflowWrap: 'anywhere' }}
        >
          {r.key || '—'}
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

export function ModelsView({ metrics, loading, error }: ModelsViewProps) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: 'Cost', order: 'desc' });

  const rows = React.useMemo(() => metrics?.breakdowns?.model ?? [], [metrics]);

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
  const tableData = React.useMemo(() => sortedRows.map((r) => toRow(r, costSev, latSev)), [sortedRows, costSev, latSev]);

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
        title='No model activity'
        description='Try widening the date range or selecting a different account.'
      />
    );
  }

  return (
    <Card>
      <CustomTable2
        id='gateway-models-table'
        headers={HEADERS}
        tableData={tableData}
        sort={sort}
        onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
      />
    </Card>
  );
}

export default ModelsView;
