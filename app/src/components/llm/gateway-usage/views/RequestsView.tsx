/**
 * RequestsView — the AI Gateway's Requests sub-tab.
 *
 * A paginated, newest-first list of individual gateway requests, fetched via the
 * self-contained `useGatewayRequests` hook (limit 50, offset paging). Rich
 * `@shared/tables/CustomTable` with Time · User · Model (with the requested model
 * as muted subtext when routing changed it) · Provider · Tokens (in/out) ·
 * Latency · Cost (severity dot) · Status (a small pill).
 *
 * When the shell drills in from the Users tab (`userFilter`), a removable chip
 * shows the scope and clears it via `onClearUser`.
 */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import dayjs from 'dayjs';
import CustomTable2 from '@shared/tables/CustomTable';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import HeaderLabel from '@components/llm/cost-analyser/components/HeaderLabel';
import { fmtTokens, fmtDuration } from '@components/llm/cost-analyser/format';
import { makeSeverity, SeverityCell, type Severity } from '@components/llm/cost-analyser/components/severity';
import { useGatewayRequests } from '../useGatewayRequests';
import type { GatewayFilters } from '../useGatewayData';
import type { GatewayRequestRow } from '@api1/gateway-usage';

interface RequestsViewProps {
  filters: GatewayFilters;
  /** Set when drilled in from the Users tab — scopes the query to one user. */
  userFilter: { id: string; name: string } | null;
  onClearUser: () => void;
  /** Set when drilled in from the Tools tab — scopes to requests that offered it. */
  toolFilter: string | null;
  onClearTool: () => void;
}

const LIMIT = 50;

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  time: <HeaderLabel label='Time' info='When the gateway received the request (UTC).' />,
  user: <HeaderLabel label='User' info='The user whose request was forwarded through the gateway.' />,
  model: <HeaderLabel label='Model' info='The model the request was routed to. The requested model is shown below it when routing changed it.' />,
  provider: <HeaderLabel label='Provider' info='The upstream provider that served the request.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Input / output tokens for this request.' />,
  latency: <HeaderLabel label='Latency' info='End-to-end latency for this request.' />,
  cost: <HeaderLabel label='Cost' info='Cost of this request.' />,
  status: <HeaderLabel label='Status' info='HTTP status the gateway returned for this request.' />,
};

const HEADERS = [
  { name: 'Time', width: '12%', component: H.time },
  { name: 'User', width: '14%', component: H.user },
  { name: 'Model', width: '20%', component: H.model },
  { name: 'Provider', width: '11%', component: H.provider },
  { name: 'Tokens', width: '12%', align: 'right' as const, component: H.tokens },
  { name: 'Latency', width: '9%', align: 'right' as const, component: H.latency },
  { name: 'Cost', width: '11%', align: 'right' as const, component: H.cost },
  { name: 'Status', width: '8%', align: 'right' as const, component: H.status },
];

function StatusPill({ code }: { code: number }) {
  if (!code) {
    return <Box sx={{ ...numCell, textAlign: 'right' }}>—</Box>;
  }
  const ok = code >= 200 && code <= 299;
  return (
    <Box
      component='span'
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        minWidth: 34,
        padding: '1px 6px',
        borderRadius: 'var(--ds-radius-sm)',
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-medium)',
        fontVariantNumeric: 'tabular-nums',
        color: ok ? 'var(--ds-green-700)' : 'var(--ds-red-700)',
        backgroundColor: ok ? 'var(--ds-green-100)' : 'var(--ds-red-100)',
        border: `1px solid ${ok ? 'var(--ds-green-300)' : 'var(--ds-red-300)'}`,
      }}
    >
      {code}
    </Box>
  );
}

function toRow(r: GatewayRequestRow, costSev: (v: number) => Severity) {
  const routed = r.requested_model && r.requested_model !== r.model;
  return [
    { component: <Box sx={numCell}>{dayjs(r.created_at).format('DD MMM HH:mm')}</Box> },
    {
      component: <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', overflowWrap: 'anywhere' }}>{r.user || '—'}</Box>,
    },
    {
      component: (
        <Box sx={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
          <Box
            sx={{
              fontSize: 'var(--ds-text-body)',
              color: 'var(--ds-gray-700)',
              fontWeight: 'var(--ds-font-weight-medium)',
              overflowWrap: 'anywhere',
            }}
          >
            {r.model || '—'}
          </Box>
          {routed && (
            <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', overflowWrap: 'anywhere' }}>requested {r.requested_model}</Box>
          )}
        </Box>
      ),
    },
    { component: <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{r.provider || '—'}</Box> },
    {
      align: 'right' as const,
      component: (
        <Box sx={{ ...numCell, textAlign: 'right' }}>
          {fmtTokens(r.input_tokens)} / {fmtTokens(r.output_tokens)}
        </Box>
      ),
    },
    { align: 'right' as const, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{fmtDuration(r.latency_ms)}</Box> },
    {
      align: 'right' as const,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <SeverityCell severity={costSev(r.cost_usd)} metric='cost'>
            <CostCallout value={r.cost_usd} size='sm' tone='neutral' fractionDigits={4} />
          </SeverityCell>
        </Box>
      ),
    },
    {
      align: 'right' as const,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <StatusPill code={r.status_code} />
        </Box>
      ),
    },
  ];
}

export function RequestsView({ filters, userFilter, onClearUser, toolFilter, onClearTool }: RequestsViewProps) {
  const [offset, setOffset] = React.useState(0);

  // Reset paging whenever the scope (date window or user filter) changes.
  React.useEffect(() => {
    setOffset(0);
  }, [filters.startDate, filters.endDate, userFilter?.id, toolFilter]);

  const { loading, error, data } = useGatewayRequests(filters, {
    userId: userFilter?.id,
    tool: toolFilter ?? undefined,
    limit: LIMIT,
    offset,
  });

  const rows = data?.rows ?? [];
  const total = data?.total ?? 0;
  const costSev = React.useMemo(() => makeSeverity(rows.map((r) => r.cost_usd)), [rows]);
  const tableData = React.useMemo(() => rows.map((r) => toRow(r, costSev)), [rows, costSev]);

  const showEmpty = !loading && !error && rows.length === 0;

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        {(userFilter || toolFilter) && (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
            {userFilter && (
              <Chip tone='info' onDismiss={onClearUser} id='gateway-requests-user-chip'>
                User: {userFilter.name}
              </Chip>
            )}
            {toolFilter && (
              <Chip tone='info' onDismiss={onClearTool} id='gateway-requests-tool-chip'>
                Tool: {toolFilter}
              </Chip>
            )}
          </Box>
        )}

        {error && <Banner tone='critical' title='Could not load gateway requests' message={error} />}

        {!error && !showEmpty && (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            Showing {rows.length} of {total.toLocaleString()} · newest first
          </Box>
        )}

        {showEmpty ? (
          <EmptyState
            size='section'
            illustration='no-results'
            title='No requests'
            description='Try widening the date range or clearing the user filter.'
          />
        ) : (
          !error && (
            <>
              {loading && !rows.length ? (
                <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
                  <CircularProgress size={28} />
                </Box>
              ) : (
                <CustomTable2 id='gateway-requests-table' headers={HEADERS} tableData={tableData} loading={loading} />
              )}

              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--ds-space-2)' }}>
                <Button
                  id='gateway-requests-prev'
                  tone='secondary'
                  size='sm'
                  disabled={offset === 0 || loading}
                  onClick={() => setOffset((o) => Math.max(0, o - LIMIT))}
                >
                  Prev
                </Button>
                <Button
                  id='gateway-requests-next'
                  tone='secondary'
                  size='sm'
                  disabled={offset + LIMIT >= total || loading}
                  onClick={() => setOffset((o) => o + LIMIT)}
                >
                  Next
                </Button>
              </Box>
            </>
          )
        )}
      </Box>
    </Card>
  );
}

export default RequestsView;
