/**
 * SessionsView — the AI Gateway's Sessions sub-tab.
 *
 * A paginated, most-recently-active-first list of conversations aggregated per
 * `session_id` (via `useGatewaySessions`): user · models · request count ·
 * in/out tokens · total cost · last active. A search box filters by session id;
 * each row's id can be copied; clicking a row drills into the Requests tab scoped
 * to that session (reusing the shell's session filter).
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
import HeaderLabel from '@components/llm/cost-analyser/components/HeaderLabel';
import { fmtTokens } from '@components/llm/cost-analyser/format';
import { useGatewaySessions } from '../useGatewaySessions';
import type { GatewayFilters } from '../useGatewayData';
import type { GatewaySession } from '@api1/gateway-usage';

dayjs.extend(utc);

interface SessionsViewProps {
  filters: GatewayFilters;
  /** Drill into one session → the Requests tab scoped to it. */
  onDrillSession: (id: string) => void;
}

const LIMIT = 50;
const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  session: (
    <HeaderLabel
      label='Session'
      info='One conversation. Exact ids come from a client/header or request metadata; ~inferred ids are grouped by the opening prompt.'
    />
  ),
  user: <HeaderLabel label='User' info='The user whose conversation this is.' />,
  models: <HeaderLabel label='Models' info='Distinct models the session used (a session can span a main + a helper model).' />,
  requests: <HeaderLabel label='Requests' info='Number of gateway requests in this session.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Total input / output tokens across the session.' />,
  cost: <HeaderLabel label='Cost' info='Total cost of the session.' />,
  last: <HeaderLabel label='Last active' info='Most recent request in the session (UTC).' />,
};

const HEADERS = [
  { name: 'Session', width: '22%', component: H.session },
  { name: 'User', width: '15%', component: H.user },
  { name: 'Models', width: '21%', component: H.models },
  { name: 'Requests', width: '9%', align: 'right' as const, component: H.requests },
  { name: 'Tokens', width: '13%', align: 'right' as const, component: H.tokens },
  { name: 'Cost', width: '10%', align: 'right' as const, component: H.cost },
  { name: 'Last active', width: '10%', align: 'right' as const, component: H.last },
];

/** Short, copyable session id with a source hint. */
function SessionCell({ s, onDrill }: { s: GatewaySession; onDrill: (id: string) => void }) {
  const [copied, setCopied] = React.useState(false);
  const exact = s.session_source === 'header' || s.session_source === 'metadata.session_id' || s.session_source === 'metadata.user_id';
  const short = s.session_id.length > 12 ? s.session_id.slice(0, 12) + '…' : s.session_id;
  const copy = (e: React.MouseEvent) => {
    e.stopPropagation();
    navigator.clipboard?.writeText(s.session_id).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };
  return (
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
      <Button
        tone='ghost'
        size='sm'
        composition='icon-only'
        icon={copied ? <CheckOutlinedIcon sx={{ fontSize: 13 }} /> : <ContentCopyOutlinedIcon sx={{ fontSize: 13 }} />}
        aria-label='Copy session id'
        tooltip={copied ? 'Copied' : 'Copy session id'}
        onClick={copy}
        data-testid={`gateway-session-copy-${s.session_id}`}
      />
    </Box>
  );
}

function toRow(s: GatewaySession, onDrill: (id: string) => void) {
  return [
    { component: <SessionCell s={s} onDrill={onDrill} /> },
    { component: <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', overflowWrap: 'anywhere' }}>{s.user || '—'}</Box> },
    {
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', overflowWrap: 'anywhere' }}>
          {(s.models || []).join(', ') || '—'}
        </Box>
      ),
      data: (s.models || []).join(', '),
    },
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
    { align: 'right' as const, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{dayjs.utc(s.last_seen).format('DD MMM HH:mm')}</Box> },
  ];
}

export function SessionsView({ filters, onDrillSession }: SessionsViewProps) {
  const [offset, setOffset] = React.useState(0);
  const [limit, setLimit] = React.useState(LIMIT);
  const [searchInput, setSearchInput] = React.useState('');
  const [search, setSearch] = React.useState('');

  // Debounce the search box so we don't refetch on every keystroke.
  React.useEffect(() => {
    const t = setTimeout(() => setSearch(searchInput.trim()), 400);
    return () => clearTimeout(t);
  }, [searchInput]);

  // Reset paging when the scope changes — done during render (React's adjust-state-on-
  // prop pattern) rather than in an effect, so we avoid an extra render + a duplicate
  // (immediately-aborted) fetch with the stale offset.
  const [prevScope, setPrevScope] = React.useState({ start: filters.startDate, end: filters.endDate, search });
  if (prevScope.start !== filters.startDate || prevScope.end !== filters.endDate || prevScope.search !== search) {
    setOffset(0);
    setPrevScope({ start: filters.startDate, end: filters.endDate, search });
  }

  const { loading, error, data } = useGatewaySessions(filters, { search: search || undefined, limit, offset });

  const rows = data?.rows ?? [];
  const total = data?.total ?? 0;
  const tableData = React.useMemo(() => rows.map((s) => toRow(s, onDrillSession)), [rows, onDrillSession]);
  const showEmpty = !loading && !error && rows.length === 0;

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Most recently active first</Box>
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
            description='Try widening the date range or clearing the search.'
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
