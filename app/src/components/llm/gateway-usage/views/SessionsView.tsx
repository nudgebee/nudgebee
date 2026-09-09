/**
 * SessionsView — the AI Gateway's Sessions sub-tab.
 *
 * A paginated list of conversations aggregated per `session_id` (via
 * `useGatewaySessions`): user · models · request count · in/out tokens · total
 * cost · started · last active (with duration). A toolbar filters by user,
 * model, and a session-id search; sortable columns (requests / tokens / cost /
 * started / last active) drive server-side ordering so the pager total stays
 * correct. Session id, user, and models are copyable; clicking a row drills into
 * the Requests tab scoped to that session (reusing the shell's session filter).
 */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import dayjs from 'dayjs';
import utc from 'dayjs/plugin/utc';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import CheckOutlinedIcon from '@mui/icons-material/CheckOutlined';
import CustomTable2 from '@shared/tables/CustomTable';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import FilterDropdown from '@ui/FilterDropdown';
import HeaderLabel from '@components/llm/cost-analyser/components/HeaderLabel';
import { fmtTokens, fmtDuration } from '@components/llm/cost-analyser/format';
import { useGatewaySessions } from '../useGatewaySessions';
import type { GatewayFilters } from '../useGatewayData';
import type { GatewaySession, GatewayUsageMetrics } from '@api1/gateway-usage';

dayjs.extend(utc);

interface SessionsViewProps {
  filters: GatewayFilters;
  /** Shell's aggregate metrics — sources the user/model filter options. */
  metrics: GatewayUsageMetrics | null;
  metricsLoading: boolean;
  /** The user scope — shared with the Requests tab (shell-owned). */
  userFilter: { id: string; name: string } | null;
  onChangeUser: (user: { id: string; name: string } | null) => void;
  /** Drill into one session → the Requests tab scoped to it. */
  onDrillSession: (id: string) => void;
}

const LIMIT = 50;
const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

// Header display name → backend sort key (server-side sort, since the list is paged).
const SORT_KEY: Record<string, string> = {
  Requests: 'requests',
  Tokens: 'tokens',
  Cost: 'cost',
  Started: 'first_seen',
  'Last active': 'last_seen',
};

const H = {
  session: (
    <HeaderLabel
      label='Session'
      info='One conversation, with a preview of its opening message below the id (shown when body capture is on and you may view it). Exact ids come from a client/header or request metadata; ~inferred ids are grouped by the opening prompt.'
    />
  ),
  user: <HeaderLabel label='User' info='The user whose conversation this is.' />,
  models: <HeaderLabel label='Models' info='Distinct models the session used (a session can span a main + a helper model).' />,
  requests: <HeaderLabel label='Requests' info='Number of gateway requests in this session.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Total input / output tokens across the session.' />,
  cost: <HeaderLabel label='Cost' info='Total cost of the session.' />,
  started: <HeaderLabel label='Started' info='First request in the session (UTC).' />,
  last: <HeaderLabel label='Last active' secondary='(span)' info='Most recent request in the session (UTC), with the elapsed span below.' />,
};

const HEADERS = [
  { name: 'Session', width: '19%', component: H.session },
  { name: 'User', width: '14%', component: H.user },
  { name: 'Models', width: '17%', component: H.models },
  { name: 'Requests', width: '8%', align: 'right' as const, component: H.requests, sortEnabled: true },
  { name: 'Tokens', width: '11%', align: 'right' as const, component: H.tokens, sortEnabled: true },
  { name: 'Cost', width: '9%', align: 'right' as const, component: H.cost, sortEnabled: true },
  { name: 'Started', width: '11%', align: 'right' as const, component: H.started, sortEnabled: true },
  { name: 'Last active', width: '11%', align: 'right' as const, component: H.last, sortEnabled: true },
];

/** Small copy-to-clipboard icon button, reused for session id / user / models. */
function CopyButton({ text, label, testid }: { text: string; label: string; testid?: string }) {
  const [copied, setCopied] = React.useState(false);
  if (!text) return null;
  const copy = (e: React.MouseEvent) => {
    e.stopPropagation();
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };
  return (
    <Button
      tone='ghost'
      size='sm'
      composition='icon-only'
      icon={copied ? <CheckOutlinedIcon sx={{ fontSize: 13 }} /> : <ContentCopyOutlinedIcon sx={{ fontSize: 13 }} />}
      aria-label={`Copy ${label}`}
      tooltip={copied ? 'Copied' : `Copy ${label}`}
      onClick={copy}
      data-testid={testid}
    />
  );
}

/** Short, copyable session id with a source hint. */
function SessionCell({ s, onDrill }: { s: GatewaySession; onDrill: (id: string) => void }) {
  const exact = s.session_source === 'header' || s.session_source === 'metadata.session_id' || s.session_source === 'metadata.user_id';
  const short = s.session_id.length > 12 ? s.session_id.slice(0, 12) + '…' : s.session_id;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', minWidth: 0, gap: '2px' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
        <Box
          component='button'
          type='button'
          onClick={() => onDrill(s.session_id)}
          title={`${s.session_id} — click to view this session's requests`}
          sx={{
            padding: 0,
            border: 'none',
            background: 'none',
            cursor: 'pointer',
            fontFamily: 'var(--ds-font-mono, monospace)',
            fontSize: 'var(--ds-text-small)',
            fontVariantNumeric: 'tabular-nums',
            color: exact ? 'var(--ds-blue-600)' : 'var(--ds-gray-500)',
            fontStyle: exact ? 'normal' : 'italic',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            '&:hover': { textDecoration: 'underline' },
          }}
        >
          {exact ? '' : '~'}
          {short}
        </Box>
        <CopyButton text={s.session_id} label='session id' testid={`gateway-session-copy-${s.session_id}`} />
      </Box>
      {s.first_message && (
        <Box
          title={s.first_message}
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-500)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            maxWidth: '100%',
          }}
        >
          {s.first_message}
        </Box>
      )}
    </Box>
  );
}

/** Text cell with an inline copy button revealed alongside the value. */
function CopyableCell({ text, label, mono }: { text: string; label: string; mono?: boolean }) {
  if (!text) return <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>—</Box>;
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
      <Box
        title={text}
        sx={{
          fontSize: mono ? 'var(--ds-text-small)' : 'var(--ds-text-body)',
          color: mono ? 'var(--ds-gray-600)' : 'var(--ds-gray-700)',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {text}
      </Box>
      <CopyButton text={text} label={label} />
    </Box>
  );
}

function toRow(s: GatewaySession, onDrill: (id: string) => void) {
  const models = (s.models || []).join(', ');
  const spanMs = dayjs.utc(s.last_seen).diff(dayjs.utc(s.first_seen));
  return [
    { component: <SessionCell s={s} onDrill={onDrill} /> },
    { component: <CopyableCell text={s.user || ''} label='user' /> },
    { component: <CopyableCell text={models} label='models' mono />, data: models },
    { align: 'right' as const, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{s.requests}</Box> },
    {
      align: 'right' as const,
      component: (
        <Box sx={{ ...numCell, textAlign: 'right' }}>
          {fmtTokens(s.input_tokens)} / {fmtTokens(s.output_tokens)}
        </Box>
      ),
    },
    {
      align: 'right' as const,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <CostCallout value={s.cost_usd} size='sm' tone='neutral' fractionDigits={4} />
        </Box>
      ),
    },
    { align: 'right' as const, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{dayjs.utc(s.first_seen).format('DD MMM HH:mm')}</Box> },
    {
      align: 'right' as const,
      component: (
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end' }}>
          <Box sx={{ ...numCell }}>{dayjs.utc(s.last_seen).format('DD MMM HH:mm')}</Box>
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
            {spanMs > 0 ? fmtDuration(spanMs) : '—'}
          </Box>
        </Box>
      ),
    },
  ];
}

export function SessionsView({ filters, metrics, metricsLoading, userFilter, onChangeUser, onDrillSession }: SessionsViewProps) {
  const [offset, setOffset] = React.useState(0);
  const [limit, setLimit] = React.useState(LIMIT);
  const [searchInput, setSearchInput] = React.useState('');
  const [search, setSearch] = React.useState('');
  const [model, setModel] = React.useState('');
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: 'Last active', order: 'desc' });

  // Filter options from the shell's aggregate metrics (no extra fetch), mirroring Requests.
  const userOptions = React.useMemo(
    () => (metrics?.breakdowns.user ?? []).filter((g) => g.id).map((g) => ({ label: g.key, value: g.id as string })),
    [metrics]
  );
  const modelOptions = React.useMemo(() => (metrics?.breakdowns.model ?? []).map((g) => g.key).filter(Boolean), [metrics]);

  // Debounce the search box so we don't refetch on every keystroke.
  React.useEffect(() => {
    const t = setTimeout(() => setSearch(searchInput.trim()), 400);
    return () => clearTimeout(t);
  }, [searchInput]);

  // Reset paging when the scope changes — done during render (React's adjust-state-on-
  // prop pattern) rather than in an effect, so we avoid an extra render + a duplicate
  // (immediately-aborted) fetch with the stale offset.
  const scope = {
    start: filters.startDate,
    end: filters.endDate,
    search,
    model,
    userId: userFilter?.id ?? '',
    sort: sort.name,
    order: sort.order,
  };
  const [prevScope, setPrevScope] = React.useState(scope);
  if (JSON.stringify(prevScope) !== JSON.stringify(scope)) {
    setOffset(0);
    setPrevScope(scope);
  }

  const { loading, error, data } = useGatewaySessions(filters, {
    userId: userFilter?.id || undefined,
    search: search || undefined,
    model: model || undefined,
    sort: SORT_KEY[sort.name] ?? 'last_seen',
    order: sort.order,
    limit,
    offset,
  });

  const rows = data?.rows ?? [];
  const total = data?.total ?? 0;
  const tableData = React.useMemo(() => rows.map((s) => toRow(s, onDrillSession)), [rows, onDrillSession]);
  const showEmpty = !loading && !error && rows.length === 0;

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
            <FilterDropdown
              id='gateway-sessions-filter-user'
              label='User'
              options={userOptions}
              value={userFilter ? { label: userFilter.name, value: userFilter.id } : ''}
              isOptionsLoading={metricsLoading}
              onSelect={(e: { target: { value: string | null } }) => {
                const id = e?.target?.value ?? null;
                if (!id) return onChangeUser(null);
                const name = userOptions.find((o) => o.value === id)?.label ?? id;
                onChangeUser({ id, name });
              }}
            />
            <FilterDropdown
              id='gateway-sessions-filter-model'
              label='Model'
              clearable
              options={modelOptions}
              value={model}
              isOptionsLoading={metricsLoading}
              onSelect={(e: { target: { value: string | null } }) => setModel(e?.target?.value ?? '')}
            />
          </Box>
          <Box sx={{ width: 260 }}>
            <Input value={searchInput} onChange={(v) => setSearchInput(v)} placeholder='Search session id…' size='sm' id='gateway-sessions-search' />
          </Box>
        </Box>

        {error && <Banner tone='critical' title='Could not load sessions' message={error} />}

        {showEmpty ? (
          <EmptyState
            size='section'
            illustration='no-results'
            title='No sessions'
            description='Try widening the date range or clearing the filters.'
          />
        ) : (
          !error &&
          (loading && !rows.length ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
              <CircularProgress size={28} />
            </Box>
          ) : (
            <CustomTable2
              id='gateway-sessions-table'
              headers={HEADERS}
              tableData={tableData}
              loading={loading}
              totalRows={total}
              rowsPerPage={limit}
              pageNumber={Math.floor(offset / limit) + 1}
              sort={sort}
              onSortChange={(next: { name: string; order: 'asc' | 'desc' }) => {
                setOffset(0);
                setSort({ name: next.name, order: next.order });
              }}
              onPageChange={(page: number, lim: number) => {
                const nextLimit = lim || limit;
                setLimit(nextLimit);
                setOffset((Math.max(1, page) - 1) * nextLimit);
              }}
            />
          ))
        )}
      </Box>
    </Card>
  );
}

export default SessionsView;
