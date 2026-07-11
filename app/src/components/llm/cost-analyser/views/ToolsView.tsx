/**
 * Screen 5 — Tools leaderboard (cross-conversation).
 *
 * Backed by `ai_aggregate_tool_usage`: a per-tool rollup across conversations from
 * `llm_conversation_tool_calls`. One row per tool NAME, showing usage (calls),
 * reliability (success / error + error rate), latency (avg / p90 / max duration),
 * and reach (distinct agents / conversations).
 *
 * Tool calls carry NO LLM token cost — the only cost signal is "downstream": the
 * LLM cost of sub-agents a tool spawned (child_agent_id), and it's 0 for plain
 * tools (kubectl / shell / fetch_logs). The tool-calls table has no model /
 * provider / status columns, so only account + date + source from the shared bar
 * apply here; an inline note flags any inapplicable filter that's active.
 *
 * Same self-contained pattern as AgentsView (own fetch + loading/error), rendered
 * through `@shared/tables/CustomTable`. The rank ToggleGroup picks WHICH metric the
 * server ranks the top rows by; the column-header sort reorders those loaded rows.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import FormatListNumberedIcon from '@mui/icons-material/FormatListNumbered';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import TimerOutlinedIcon from '@mui/icons-material/TimerOutlined';
import PaidOutlinedIcon from '@mui/icons-material/PaidOutlined';
import CustomTable2 from '@shared/tables/CustomTable2';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import { ToggleGroup } from '@ui/ToggleGroup';
import LeaderboardOutlinedIcon from '@mui/icons-material/LeaderboardOutlined';
import VerifiedOutlinedIcon from '@mui/icons-material/VerifiedOutlined';
import HeaderLabel from '../components/HeaderLabel';
import SectionHeader from '../components/Section';
import CostTreemap from '../components/CostTreemap';
import ToolReliabilityBars from '../components/ToolReliabilityBars';
import ToolDurationBars from '../components/ToolDurationBars';
import ToolCallsModal from '../components/ToolCallsModal';
import { makeSeverity, SeverityCell, type Severity } from '../components/severity';
import { listToolUsage, type ToolUsageRow, type ToolSortBy, type ToolStatusGroup } from '@api1/ai-cost';
import type { CostFilters } from '../types';

interface ToolsViewProps {
  accountId?: string;
  filters: CostFilters;
  /** Cross-link: open a conversation's detail, focused on a specific agent. */
  onSelectRun: (sessionId: string, accountId?: string, agentId?: string) => void;
}

/** The tool a row opened the drill-in for, plus which status view to open on. */
interface ToolDrill {
  tool: string;
  type: string;
  status: ToolStatusGroup;
}

const SORT_OPTIONS: { value: ToolSortBy; label: string; icon: React.ReactNode }[] = [
  { value: 'calls', label: 'By calls', icon: <FormatListNumberedIcon sx={{ fontSize: 14 }} /> },
  { value: 'errors', label: 'By errors', icon: <ErrorOutlineIcon sx={{ fontSize: 14 }} /> },
  { value: 'duration', label: 'By duration', icon: <TimerOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'cost', label: 'By downstream cost', icon: <PaidOutlinedIcon sx={{ fontSize: 14 }} /> },
];

const secs = (s: number) => `${s.toFixed(1)}s`;
const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  tool: <HeaderLabel label='Tool' info='The tool name and its type. Aggregated across every conversation in range.' />,
  calls: <HeaderLabel label='Calls' info='Total invocations of this tool in range.' />,
  errors: <HeaderLabel label='Errors' secondary='(rate)' info='Settled failures (status fail / error / terminated) and the share of all calls.' />,
  avgDur: <HeaderLabel label='Avg dur' info='Average wall-clock duration per call (execution_duration_ms, else updated−created).' />,
  p90Dur: <HeaderLabel label='Duration' secondary='(p90 / max)' info='90th-percentile and slowest single call duration.' />,
  reach: <HeaderLabel label='Reach' secondary='(agents / convs)' info='Distinct agents and conversations that used this tool.' />,
  cost: (
    <HeaderLabel label='Downstream $' info='LLM cost of sub-agents this tool spawned (child_agent_id). Blank for tools that make no LLM calls.' />
  ),
};

const HEADERS = [
  { name: 'Tool', width: '24%', sortEnabled: true, component: H.tool },
  { name: 'Calls', width: '9%', align: 'right' as const, sortEnabled: true, component: H.calls },
  { name: 'Errors', width: '13%', align: 'right' as const, sortEnabled: true, component: H.errors },
  { name: 'Avg dur', width: '10%', align: 'right' as const, sortEnabled: true, component: H.avgDur },
  { name: 'Duration', width: '14%', align: 'right' as const, sortEnabled: true, component: H.p90Dur },
  { name: 'Reach', width: '15%', align: 'right' as const, sortEnabled: true, component: H.reach },
  { name: 'Downstream $', width: '15%', align: 'right' as const, sortEnabled: true, component: H.cost },
];

// Header name → the value a column sorts on (client-side reorder of loaded rows).
const SORT_VALUE: Record<string, (r: ToolUsageRow) => number | string> = {
  Tool: (r) => r.tool_name || '',
  Calls: (r) => r.calls,
  Errors: (r) => r.error_count,
  'Avg dur': (r) => r.avg_duration_seconds,
  Duration: (r) => r.p90_duration_seconds,
  Reach: (r) => r.distinct_conversations,
  'Downstream $': (r) => r.downstream_cost_usd,
};

function toRow(
  r: ToolUsageRow,
  errSev: (v: number) => Severity,
  durSev: (v: number) => Severity,
  costSev: (v: number) => Severity,
  onOpen: (r: ToolUsageRow, status: ToolStatusGroup) => void
) {
  return [
    {
      data: r.tool_name,
      // The tool name opens the invocation explorer (all calls); the error cell
      // below opens it pre-filtered to failures.
      component: (
        <Box
          component='button'
          type='button'
          title={`View ${r.tool_name || 'tool'} invocations`}
          onClick={() => onOpen(r, 'all')}
          id={`tool-link-${r.tool_name}`}
          sx={{
            all: 'unset',
            cursor: 'pointer',
            boxSizing: 'border-box',
            width: '100%',
            lineHeight: 1.3,
            minWidth: 0,
            '&:hover .tool-name': { textDecoration: 'underline' },
            '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: 2, borderRadius: 'var(--ds-radius-sm)' },
          }}
        >
          <Box
            className='tool-name'
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-blue-600)',
              overflowWrap: 'anywhere',
            }}
          >
            {r.tool_name || 'tool'}
          </Box>
          {r.tool_type ? <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>{r.tool_type}</Box> : null}
        </Box>
      ),
    },
    { align: 'right' as const, data: r.calls, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{r.calls}</Box> },
    {
      align: 'right' as const,
      data: r.error_count,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <SeverityCell severity={errSev(r.error_rate_pct)} metric='cost'>
            {r.error_count > 0 ? (
              <Box
                component='button'
                type='button'
                title={`${r.error_count} of ${r.calls} calls failed (${r.error_rate_pct.toFixed(1)}%) — click to view`}
                onClick={() => onOpen(r, 'errors')}
                id={`tool-errors-${r.tool_name}`}
                sx={{
                  all: 'unset',
                  cursor: 'pointer',
                  textAlign: 'right',
                  lineHeight: 1.25,
                  '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: 2, borderRadius: 'var(--ds-radius-sm)' },
                }}
              >
                {/* Rate is the headline (comparable across tools); the raw count
                    is the secondary line so the two can't be misread as one number. */}
                <Chip size='2xs' tone='waste' variant='tag'>
                  {r.error_rate_pct.toFixed(0)}%
                </Chip>
                <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
                  {r.error_count.toLocaleString()} of {r.calls.toLocaleString()}
                </Box>
              </Box>
            ) : (
              <Box sx={{ ...numCell, textAlign: 'right' }}>0</Box>
            )}
          </SeverityCell>
        </Box>
      ),
    },
    {
      align: 'right' as const,
      data: r.avg_duration_seconds,
      component: <Box sx={{ ...numCell, textAlign: 'right' }}>{secs(r.avg_duration_seconds)}</Box>,
    },
    {
      align: 'right' as const,
      data: r.p90_duration_seconds,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <SeverityCell severity={durSev(r.p90_duration_seconds)} metric='latency'>
            <Box sx={{ lineHeight: 1.3, textAlign: 'right' }}>
              <Box sx={numCell}>{secs(r.p90_duration_seconds)}</Box>
              <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
                max {secs(r.max_duration_seconds)}
              </Box>
            </Box>
          </SeverityCell>
        </Box>
      ),
    },
    {
      align: 'right' as const,
      data: r.distinct_conversations,
      component: (
        <Box sx={{ ...numCell, textAlign: 'right' }}>
          {r.distinct_agents} / {r.distinct_conversations}
        </Box>
      ),
    },
    {
      align: 'right' as const,
      data: r.downstream_cost_usd,
      component:
        r.downstream_cost_usd > 0 ? (
          <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
            <SeverityCell severity={costSev(r.downstream_cost_usd)} metric='cost'>
              <CostCallout value={r.downstream_cost_usd} size='sm' tone='neutral' fractionDigits={3} />
            </SeverityCell>
          </Box>
        ) : (
          <Box sx={{ ...numCell, textAlign: 'right', color: 'var(--ds-gray-400)' }}>—</Box>
        ),
    },
  ];
}

export function ToolsView({ accountId, filters, onSelectRun }: ToolsViewProps) {
  const [sortBy, setSortBy] = React.useState<ToolSortBy>('calls');
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: '', order: 'asc' });
  const [drill, setDrill] = React.useState<ToolDrill | null>(null);
  const [state, setState] = React.useState<{ loading: boolean; error: string | null; rows: ToolUsageRow[] }>({
    loading: false,
    error: null,
    rows: [],
  });

  const sourcesKey = (filters.sources ?? []).join(',');

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    listToolUsage(
      {
        accountIds: accountId ? [accountId] : [],
        startDate: `${filters.startDate}T00:00:00Z`,
        endDate: `${filters.endDate}T23:59:59Z`,
        sources: filters.sources ?? [],
        sortBy,
        limit: 100,
      },
      controller.signal
    )
      .then((d) => {
        if (!cancelled) setState({ loading: false, error: null, rows: d?.rows ?? [] });
      })
      .catch((e) => {
        if (!cancelled) setState({ loading: false, error: e instanceof Error ? e.message : 'Failed to load tools', rows: [] });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, filters.startDate, filters.endDate, sourcesKey, sortBy]);

  // Client-side reorder of the loaded rows. Empty sort.name = keep server order.
  const sortedRows = React.useMemo(() => {
    const get = sort.name ? SORT_VALUE[sort.name] : undefined;
    if (!get) return state.rows;
    const arr = [...state.rows].sort((a, b) => {
      const av = get(a);
      const bv = get(b);
      return typeof av === 'string' ? String(av).localeCompare(String(bv)) : (av as number) - (bv as number);
    });
    return sort.order === 'desc' ? arr.reverse() : arr;
  }, [state.rows, sort]);

  // Relative outlier highlighting across the rows shown.
  const errSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.error_rate_pct)), [sortedRows]);
  const durSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.p90_duration_seconds)), [sortedRows]);
  const costSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.downstream_cost_usd)), [sortedRows]);
  const openDrill = React.useCallback((r: ToolUsageRow, status: ToolStatusGroup) => setDrill({ tool: r.tool_name, type: r.tool_type, status }), []);
  const tableData = React.useMemo(
    () => sortedRows.map((r) => toRow(r, errSev, durSev, costSev, openDrill)),
    [sortedRows, errSev, durSev, costSev, openDrill]
  );

  // Volume treemap: top tools by calls + an "Other" bucket so the legend stays legible.
  const volumeSlices = React.useMemo(() => {
    const sorted = [...state.rows].sort((a, b) => b.calls - a.calls);
    const top = sorted.slice(0, 10).map((r) => ({ key: r.tool_name, cost: r.calls }));
    const otherCalls = sorted.slice(10).reduce((a, r) => a + r.calls, 0);
    return otherCalls > 0 ? [...top, { key: 'Other', cost: otherCalls }] : top;
  }, [state.rows]);

  const showEmpty = !state.loading && !state.error && state.rows.length === 0;
  const inapplicable = (filters.models?.length ?? 0) + (filters.providers?.length ?? 0) + (filters.statuses?.length ?? 0) > 0;
  const hasCharts = !state.error && state.rows.length > 0;
  const totalCalls = React.useMemo(() => state.rows.reduce((a, r) => a + r.calls, 0), [state.rows]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
      {hasCharts && (
        <>
          <Card>
            <SectionHeader
              title='Call volume by tool'
              icon={<LeaderboardOutlinedIcon />}
              subtitle='Share of all tool calls in range (top 10 + Other)'
            />
            <CostTreemap slices={volumeSlices} total={totalCalls} formatValue={(n) => n.toLocaleString()} />
          </Card>

          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-5)' }}>
            <Card sx={{ flex: '1 1 360px', minWidth: 0 }}>
              <SectionHeader
                title='Reliability'
                icon={<VerifiedOutlinedIcon />}
                subtitle='Success / error split for the tools with the most failures — click a row to view them'
              />
              <ToolReliabilityBars id='tool-reliability-bars' rows={state.rows} onSelect={openDrill} />
            </Card>
            <Card sx={{ flex: '1 1 360px', minWidth: 0 }}>
              <SectionHeader
                title='Slowest tools (p90)'
                icon={<TimerOutlinedIcon />}
                subtitle='Solid = p90 duration, light tail = slowest call — click a row to inspect'
              />
              <ToolDurationBars id='tool-duration-bars' rows={state.rows} onSelect={openDrill} />
            </Card>
          </Box>
        </>
      )}

      <Card>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              Top {state.rows.length} tools by {sortBy} · scoped by date + source only (model / provider / status don&rsquo;t apply to tools)
            </Box>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Rank tools by'
              value={sortBy}
              onChange={(v: ToolSortBy) => setSortBy(v)}
              options={SORT_OPTIONS}
            />
          </Box>

          {inapplicable && (
            <Banner
              tone='info'
              title='Some filters don&rsquo;t apply to tools'
              message='Model, provider, and status are LLM-call concepts — tool calls have none of them, so this tab honours only the date range and source filter.'
            />
          )}

          {state.error && <Banner tone='critical' title='Could not load tools' message={state.error} />}

          {showEmpty ? (
            <EmptyState
              size='section'
              illustration='no-results'
              title='No tool calls'
              description='Try widening the date range or clearing the source filter.'
            />
          ) : (
            !state.error && (
              <CustomTable2
                id='tools-leaderboard'
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

      <ToolCallsModal
        open={!!drill}
        toolName={drill?.tool ?? ''}
        toolType={drill?.type}
        accountId={accountId}
        filters={filters}
        initialStatus={drill?.status ?? 'all'}
        onClose={() => setDrill(null)}
        onSelectRun={onSelectRun}
      />
    </Box>
  );
}

export default ToolsView;
