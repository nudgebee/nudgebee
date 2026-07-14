/**
 * RequestsView — the AI Gateway's Requests sub-tab.
 *
 * A paginated, newest-first list of individual gateway requests, fetched via the
 * self-contained `useGatewayRequests` hook (limit 50, offset paging). Rich
 * `@shared/tables/CustomTable` with Time · User · Model (with the requested model
 * as muted subtext when routing changed it) · Provider · Tokens (in/out) ·
 * Latency · Cost (severity dot) · Status (a small pill).
 *
 * A toolbar of FilterDropdowns scopes the list by user · model · provider ·
 * status (Success 2xx / Error). User/model/provider options come from the
 * shell's already-fetched aggregate `metrics` breakdowns (same date window —
 * no extra fetch); filtering happens server-side so the pager total matches.
 * The user scope is owned by the shell (`userFilter`/`onChangeUser`) because
 * the Users tab drills into it; the User dropdown reads and writes that same
 * state. A Tools-tab drill-in (`toolFilter`) still shows as a removable chip.
 */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import dayjs from 'dayjs';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import CustomTable2 from '@shared/tables/CustomTable';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import FilterDropdown from '@ui/FilterDropdown';
import HeaderLabel from '@components/llm/cost-analyser/components/HeaderLabel';
import { fmtTokens, fmtDuration } from '@components/llm/cost-analyser/format';
import { makeSeverity, SeverityCell, type Severity } from '@components/llm/cost-analyser/components/severity';
import { useGatewayRequests } from '../useGatewayRequests';
import type { GatewayFilters } from '../useGatewayData';
import type { GatewayRequestRow, GatewayUsageMetrics } from '@api1/gateway-usage';

// The per-request "view body" viewer is an ENTERPRISE surface — the EE body
// viewer lives under the ee/ subtree, which is removed from the OSS build, so
// this () => null stub stands in. The trigger only renders when row.can_view_body,
// which is always false in OSS (body-logging is EE-only), so it never renders.
const RequestBodyModal = (_props: { requestId: string; label: string; onClose: () => void }) => null;

interface RequestsViewProps {
  filters: GatewayFilters;
  /** Shell's aggregate metrics — sources the user/model/provider filter options. */
  metrics: GatewayUsageMetrics | null;
  metricsLoading: boolean;
  /** The user scope — set here via the User dropdown or by a Users-tab drill-in. */
  userFilter: { id: string; name: string } | null;
  onChangeUser: (user: { id: string; name: string } | null) => void;
  /** Set when drilled in from the Tools tab — scopes to requests that offered it. */
  toolFilter: string | null;
  onClearTool: () => void;
}

const LIMIT = 50;

const STATUS_OPTIONS = [
  { label: 'Success (2xx)', value: 'success' },
  { label: 'Error', value: 'error' },
];

type FDOption = string | { value?: string };
const toValues = (sel: FDOption[] | undefined): string[] => (sel ?? []).map((o) => (typeof o === 'string' ? o : String(o?.value ?? '')));

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
  body: <HeaderLabel label='Body' info='View the captured request and response body (when available for your own requests).' />,
};

const HEADERS = [
  { name: 'Time', width: '12%', component: H.time },
  { name: 'User', width: '13%', component: H.user },
  { name: 'Model', width: '19%', component: H.model },
  { name: 'Provider', width: '10%', component: H.provider },
  { name: 'Tokens', width: '11%', align: 'right' as const, component: H.tokens },
  { name: 'Latency', width: '9%', align: 'right' as const, component: H.latency },
  { name: 'Cost', width: '10%', align: 'right' as const, component: H.cost },
  { name: 'Status', width: '8%', align: 'right' as const, component: H.status },
  { name: 'Body', width: '8%', align: 'right' as const, component: H.body },
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

function toRow(r: GatewayRequestRow, costSev: (v: number) => Severity, onViewBody: (r: GatewayRequestRow) => void) {
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
    {
      align: 'right' as const,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          {r.can_view_body ? (
            <Button
              id={`gateway-request-body-btn-${r.id}`}
              data-testid={`gateway-request-body-btn-${r.id}`}
              tone='ghost'
              size='sm'
              composition='icon-only'
              icon={<VisibilityOutlinedIcon />}
              aria-label='View request body'
              tooltip='View request body'
              onClick={() => onViewBody(r)}
            />
          ) : (
            <Box sx={{ ...numCell, textAlign: 'right' }}>—</Box>
          )}
        </Box>
      ),
    },
  ];
}

export function RequestsView({ filters, metrics, metricsLoading, userFilter, onChangeUser, toolFilter, onClearTool }: RequestsViewProps) {
  const [offset, setOffset] = React.useState(0);
  const [models, setModels] = React.useState<string[]>([]);
  const [providers, setProviders] = React.useState<string[]>([]);
  const [status, setStatus] = React.useState('');
  // The row whose captured body is being viewed (EE). Null = modal closed.
  const [bodyRow, setBodyRow] = React.useState<GatewayRequestRow | null>(null);

  // Reset paging whenever the scope (date window or any filter) changes. The
  // array states are safe as deps: useState references are stable and only
  // change via their setters (which always receive a fresh array).
  React.useEffect(() => {
    setOffset(0);
  }, [filters.startDate, filters.endDate, userFilter?.id, toolFilter, models, providers, status]);

  const { loading, error, data } = useGatewayRequests(filters, {
    userId: userFilter?.id,
    providers,
    models,
    status: status || undefined,
    tool: toolFilter ?? undefined,
    limit: LIMIT,
    offset,
  });

  // Filter options from the shell's aggregate breakdowns (same date window as
  // this list). Values with an empty key would filter on '' — drop them.
  const userOptions = React.useMemo(
    () => (metrics?.breakdowns.user ?? []).filter((g) => g.id).map((g) => ({ label: g.key, value: g.id as string })),
    [metrics]
  );
  const modelOptions = React.useMemo(() => (metrics?.breakdowns.model ?? []).map((g) => g.key).filter(Boolean), [metrics]);
  const providerOptions = React.useMemo(() => (metrics?.breakdowns.provider ?? []).map((g) => g.key).filter(Boolean), [metrics]);

  const onSelectUser = (id: string | null) => {
    if (!id) {
      onChangeUser(null);
      return;
    }
    const name = userOptions.find((o) => o.value === id)?.label ?? id;
    onChangeUser({ id, name });
  };

  const rows = data?.rows ?? [];
  const total = data?.total ?? 0;
  const costSev = React.useMemo(() => makeSeverity(rows.map((r) => r.cost_usd)), [rows]);
  const tableData = React.useMemo(() => rows.map((r) => toRow(r, costSev, setBodyRow)), [rows, costSev]);

  const showEmpty = !loading && !error && rows.length === 0;

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          <FilterDropdown
            id='gateway-requests-filter-user'
            label='User'
            options={userOptions}
            // Pass the {label, value} pair so the trigger shows the NAME even when
            // the drilled-in user isn't in the options yet (metrics still loading,
            // or the date window changed); selection matching uses .value (the id).
            value={userFilter ? { label: userFilter.name, value: userFilter.id } : ''}
            isOptionsLoading={metricsLoading}
            onSelect={(e: { target: { value: string | null } }) => onSelectUser(e?.target?.value ?? null)}
          />
          <FilterDropdown
            id='gateway-requests-filter-model'
            label='Model'
            multiple
            options={modelOptions}
            value={models}
            isOptionsLoading={metricsLoading}
            onSelect={(_e: unknown, sel: FDOption[]) => setModels(toValues(sel))}
          />
          <FilterDropdown
            id='gateway-requests-filter-provider'
            label='Provider'
            multiple
            options={providerOptions}
            value={providers}
            isOptionsLoading={metricsLoading}
            onSelect={(_e: unknown, sel: FDOption[]) => setProviders(toValues(sel))}
          />
          <FilterDropdown
            id='gateway-requests-filter-status'
            label='Status'
            options={STATUS_OPTIONS}
            value={status}
            onSelect={(e: { target: { value: string | null } }) => setStatus(e?.target?.value ?? '')}
          />
          {toolFilter && (
            <Chip tone='info' onDismiss={onClearTool} id='gateway-requests-tool-chip'>
              Tool: {toolFilter}
            </Chip>
          )}
        </Box>

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
            description='Try widening the date range or clearing the filters.'
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

              {/* Server-side pager (50 rows/page). Hidden when everything fits on
                  one page — two disabled buttons are just noise. `offset > 0`
                  keeps it visible on later pages even when a shrinking `total`
                  (filters applied mid-paging) drops below one page. */}
              {(total > LIMIT || offset > 0) && (
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
              )}
            </>
          )
        )}
      </Box>

      {/* EE-only body viewer. RequestBodyModal is a () => null stub in OSS, and
          bodyRow can never be set there (the trigger only shows when
          can_view_body, which is always false in OSS), so this never renders. */}
      {bodyRow && (
        <RequestBodyModal
          requestId={bodyRow.id}
          label={`${bodyRow.model || 'request'} · ${dayjs(bodyRow.created_at).format('DD MMM HH:mm')}`}
          onClose={() => setBodyRow(null)}
        />
      )}
    </Card>
  );
}

export default RequestsView;
