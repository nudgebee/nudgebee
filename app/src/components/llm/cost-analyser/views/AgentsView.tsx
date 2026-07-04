/**
 * Screen 4 — Agents leaderboard (cross-conversation).
 *
 * Backed by `ai_list_agent_costs`: the top agent INVOCATIONS across conversations,
 * ranked server-side by cost / latency / errors (each rank is a different result
 * set, so switching the toggle refetches). One row per invocation — the same agent
 * name recurs, each linked to its own conversation for cross-navigation.
 *
 * Rendered through `@shared/tables/CustomTable` (the cost-analyser table
 * primitive, same as ConversationsTable / ModelsView) so it inherits the shared
 * cell chrome, skeleton loading state, and empty handling. Two controls, two jobs:
 * the rank ToggleGroup picks WHICH rows are fetched (the server's top-100 by
 * cost / latency / errors); the column-header sort reorders those loaded rows
 * client-side (covers every column, including the text ones the server can't sort).
 *
 * Include / exclude agent filters are LOCAL to this tab (the global filter bar would
 * change every screen's numbers). By default the noisy infra agents are excluded.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import PaidOutlinedIcon from '@mui/icons-material/PaidOutlined';
import TimerOutlinedIcon from '@mui/icons-material/TimerOutlined';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import CustomTable2 from '@shared/tables/CustomTable2';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import { ToggleGroup } from '@ui/ToggleGroup';
import FilterDropdown from '@ui/FilterDropdown';
import HeaderLabel from '../components/HeaderLabel';
import SectionHeader from '../components/Section';
import AgentLatencyBars from '../components/AgentLatencyBars';
import { fmtTokens } from '../format';
import { makeSeverity, SeverityCell, type Severity } from '../components/severity';
import { listAgentCosts, type AgentCallRow, type AgentLatencyProfile, type AgentLatencyPercentile, type AgentSortBy } from '@api1/ai-cost';
import type { CostFilters } from '../types';

interface AgentsViewProps {
  accountId?: string;
  filters: CostFilters;
  /** Agent names available for the include/exclude dropdowns. */
  agentOptions?: string[];
  /** Cross-link: open a conversation's detail, focused on a specific agent. */
  onSelectRun: (sessionId: string, accountId?: string, agentId?: string) => void;
}

const SORT_OPTIONS: { value: AgentSortBy; label: string; icon: React.ReactNode }[] = [
  { value: 'cost', label: 'By cost', icon: <PaidOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'latency', label: 'By latency', icon: <TimerOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'errors', label: 'By errors', icon: <ErrorOutlineIcon sx={{ fontSize: 14 }} /> },
];

// The infra/cloud-debug agents dominate the leaderboard; exclude them by default so
// the app-level agents are visible. The user can clear/adjust these.
const DEFAULT_EXCLUDE = ['k8s_debug', 'aws', 'gcp', 'azure'];

// Latency-outlier filter: show only invocations whose total latency ≥ the chosen
// pXX (over the trailing-24h baseline). 'All' = no filter. Defaults to p90.
const LATENCY_OPTIONS = ['All', '≥ p80', '≥ p85', '≥ p90', '≥ p95', '≥ p99'];
const LABEL_TO_PCTILE: Record<string, AgentLatencyPercentile> = { All: 0, '≥ p80': 80, '≥ p85': 85, '≥ p90': 90, '≥ p95': 95, '≥ p99': 99 };
const PCTILE_TO_LABEL: Record<number, string> = { 0: 'All', 80: '≥ p80', 85: '≥ p85', 90: '≥ p90', 95: '≥ p95', 99: '≥ p99' };

type FDOption = string | { value?: string };
const toValues = (sel?: FDOption[]): string[] => (sel ?? []).map((o) => (typeof o === 'string' ? o : String(o?.value ?? '')));

const STATUS_TONE: Record<string, 'success' | 'critical' | 'warning' | 'neutral'> = {
  success: 'success',
  completed: 'success',
  fail: 'critical',
  failed: 'critical',
  terminated: 'critical',
};

const secs = (s: number) => `${s.toFixed(1)}s`;
const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

// Header cells (info tooltips, optional gray secondary), matching ConversationsTable.
const H = {
  agent: <HeaderLabel label='Agent' info='The agent invocation and its own direct model calls. The same agent recurs across conversations.' />,
  conversation: <HeaderLabel label='Conversation' info='The conversation this invocation ran in. Click to open it focused on this agent.' />,
  tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info="Input / output tokens across this invocation's model calls." />,
  cost: <HeaderLabel label='Cost' info="Direct cost of this invocation's own model calls (children are their own rows)." />,
  latencyTotal: <HeaderLabel label='Latency' secondary='(total)' info="Summed model time (not wall-clock) across this invocation's calls." />,
  latencyMax: <HeaderLabel label='Latency' secondary='(max)' info='The slowest single model call. The second line shows the median call.' />,
  calls: <HeaderLabel label='Calls' info='Model (LLM) API calls in this invocation.' />,
  errors: <HeaderLabel label='Errors' info='Failed model calls (request error or a populated error message).' />,
  status: <HeaderLabel label='Status' info="The agent invocation's final status." />,
};

const HEADERS = [
  { name: 'Agent', width: '17%', sortEnabled: true, component: H.agent },
  { name: 'Conversation', width: '22%', sortEnabled: true, component: H.conversation },
  { name: 'Tokens', width: '11%', sortEnabled: true, component: H.tokens },
  { name: 'Cost', width: '9%', align: 'right' as const, sortEnabled: true, component: H.cost },
  { name: 'Latency total', width: '8%', align: 'right' as const, sortEnabled: true, component: H.latencyTotal },
  { name: 'Latency max', width: '11%', align: 'right' as const, sortEnabled: true, component: H.latencyMax },
  { name: 'Calls', width: '6%', align: 'right' as const, sortEnabled: true, component: H.calls },
  { name: 'Errors', width: '6%', align: 'right' as const, sortEnabled: true, component: H.errors },
  { name: 'Status', width: '10%', sortEnabled: true, component: H.status },
];

// Header name → the value a column sorts on. Sorting is client-side over the loaded
// rows (the server already picked the top-100 for the rank metric); this reorders
// what's shown. The max column sorts by the slowest call (its headline number).
const SORT_VALUE: Record<string, (r: AgentCallRow) => number | string> = {
  Agent: (r) => r.agent_name || '',
  Conversation: (r) => r.conversation_title || r.conversation_id || '',
  Tokens: (r) => r.input_tokens,
  Cost: (r) => r.cost_usd,
  'Latency total': (r) => r.latency_sum_seconds,
  'Latency max': (r) => r.latency_max_seconds,
  Calls: (r) => r.llm_call_count,
  Errors: (r) => r.error_count,
  Status: (r) => r.status || '',
};

function toRow(r: AgentCallRow, onSelectRun: AgentsViewProps['onSelectRun'], costSev: (v: number) => Severity, latSev: (v: number) => Severity) {
  const title = r.conversation_title || r.conversation_id;
  return [
    // Agent — plain text (no chip); name can wrap.
    {
      data: r.agent_name,
      component: (
        <Box
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-gray-700)',
            overflowWrap: 'anywhere',
            minWidth: 0,
          }}
        >
          {r.agent_name || 'agent'}
        </Box>
      ),
    },
    // Conversation — clickable text that wraps to 2 lines then ellipsises.
    {
      data: title,
      component: (
        <Box
          component='button'
          type='button'
          title={title}
          onClick={() => onSelectRun(r.conversation_id, r.account_id, r.agent_id)}
          id={`agent-link-${r.agent_id}`}
          sx={{
            all: 'unset',
            cursor: 'pointer',
            boxSizing: 'border-box',
            width: '100%',
            textAlign: 'left',
            color: 'var(--ds-blue-600)',
            fontSize: 'var(--ds-text-body)',
            display: '-webkit-box',
            WebkitLineClamp: 2,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
            wordBreak: 'break-word',
            '&:hover': { textDecoration: 'underline' },
            '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: 2, borderRadius: 'var(--ds-radius-sm)' },
          }}
        >
          {title}
        </Box>
      ),
    },
    {
      data: r.input_tokens,
      component: (
        <Box sx={numCell}>
          {fmtTokens(r.input_tokens)} / {fmtTokens(r.output_tokens)}
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
      data: r.latency_sum_seconds,
      component: (
        <Box sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%' }}>
          <SeverityCell severity={latSev(r.latency_sum_seconds)} metric='latency'>
            <Box sx={{ ...numCell, textAlign: 'right' }}>{secs(r.latency_sum_seconds)}</Box>
          </SeverityCell>
        </Box>
      ),
    },
    {
      align: 'right' as const,
      data: r.latency_max_seconds,
      component: (
        <Box sx={{ lineHeight: 1.3, textAlign: 'right' }}>
          <Box sx={numCell}>{secs(r.latency_max_seconds)}</Box>
          <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
            med {secs(r.latency_median_seconds)}
          </Box>
        </Box>
      ),
    },
    { align: 'right' as const, data: r.llm_call_count, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{r.llm_call_count}</Box> },
    {
      align: 'right' as const,
      data: r.error_count,
      component:
        r.error_count > 0 ? (
          <Chip size='2xs' tone='waste' variant='tag'>
            {r.error_count}
          </Chip>
        ) : (
          <Box sx={{ ...numCell, textAlign: 'right' }}>0</Box>
        ),
    },
    { data: r.status, component: <Label tone={STATUS_TONE[r.status] ?? 'neutral'} text={r.status || '—'} /> },
  ];
}

export function AgentsView({ accountId, filters, agentOptions = [], onSelectRun }: AgentsViewProps) {
  const [sortBy, setSortBy] = React.useState<AgentSortBy>('cost');
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: '', order: 'asc' });
  const [includeAgents, setIncludeAgents] = React.useState<string[]>([]);
  const [excludeAgents, setExcludeAgents] = React.useState<string[]>(DEFAULT_EXCLUDE);
  const [latencyPctile, setLatencyPctile] = React.useState<AgentLatencyPercentile>(90);
  const [state, setState] = React.useState<{
    loading: boolean;
    error: string | null;
    rows: AgentCallRow[];
    profiles: AgentLatencyProfile[];
    thresholdSec: number;
    appliedPctile: number;
  }>({
    loading: false,
    error: null,
    rows: [],
    profiles: [],
    thresholdSec: 0,
    appliedPctile: 0,
  });

  const includeKey = includeAgents.join(',');
  const excludeKey = excludeAgents.join(',');

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    listAgentCosts(
      {
        accountIds: accountId ? [accountId] : [],
        startDate: `${filters.startDate}T00:00:00Z`,
        endDate: `${filters.endDate}T23:59:59Z`,
        sources: filters.sources ?? [],
        models: filters.models,
        providers: filters.providers,
        statuses: filters.statuses,
        agents: includeAgents,
        agentsExclude: excludeAgents,
        sortBy,
        limit: 100,
        latencyPercentile: latencyPctile,
      },
      controller.signal
    )
      .then((d) => {
        if (!cancelled)
          setState({
            loading: false,
            error: null,
            rows: d?.rows ?? [],
            profiles: d?.latency_by_agent ?? [],
            thresholdSec: d?.latency_threshold_seconds ?? 0,
            appliedPctile: d?.latency_percentile ?? 0,
          });
      })
      .catch((e) => {
        if (!cancelled)
          setState({
            loading: false,
            error: e instanceof Error ? e.message : 'Failed to load agents',
            rows: [],
            profiles: [],
            thresholdSec: 0,
            appliedPctile: 0,
          });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    accountId,
    filters.startDate,
    filters.endDate,
    filters.sources,
    filters.models,
    filters.providers,
    filters.statuses,
    sortBy,
    includeKey,
    excludeKey,
    latencyPctile,
  ]);

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

  // Relative outlier highlighting: rank cost / total-latency within the rows shown.
  const costSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.cost_usd)), [sortedRows]);
  const latSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.latency_sum_seconds)), [sortedRows]);
  const tableData = React.useMemo(() => sortedRows.map((r) => toRow(r, onSelectRun, costSev, latSev)), [sortedRows, onSelectRun, costSev, latSev]);
  const showEmpty = !state.loading && !state.error && state.rows.length === 0;

  return (
    <Card>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        {/* Local include / exclude agent filters (left) and the rank toggle (right)
            share one row; the summary caption sits below them. */}
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', flexWrap: 'wrap', alignItems: 'center' }}>
            <FilterDropdown
              id='agents-filter-include'
              label='Agents (include)'
              multiple
              options={agentOptions}
              value={includeAgents}
              onSelect={(_e: unknown, sel: FDOption[]) => setIncludeAgents(toValues(sel))}
            />
            <FilterDropdown
              id='agents-filter-exclude'
              label='Agents (exclude)'
              multiple
              options={agentOptions}
              value={excludeAgents}
              onSelect={(_e: unknown, sel: FDOption[]) => setExcludeAgents(toValues(sel))}
            />
            <FilterDropdown
              id='agents-filter-latency'
              label='Latency'
              options={LATENCY_OPTIONS}
              value={PCTILE_TO_LABEL[latencyPctile]}
              onSelect={(e: { target: { value: string } }) => setLatencyPctile(LABEL_TO_PCTILE[e?.target?.value] ?? 0)}
            />
          </Box>
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Rank agents by'
            value={sortBy}
            onChange={(v: AgentSortBy) => setSortBy(v)}
            options={SORT_OPTIONS}
          />
        </Box>

        <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          Top {state.rows.length} agent invocations by {sortBy} · each links to its conversation · latency is summed model time (not wall-clock)
          {latencyPctile !== 0 &&
            (state.thresholdSec > 0
              ? ` · latency ≥ p${state.appliedPctile} (${secs(state.thresholdSec)}, last-24h baseline)`
              : ` · latency ≥ p${latencyPctile} (no last-24h baseline — showing all)`)}
        </Box>

        {state.error && <Banner tone='critical' title='Could not load agents' message={state.error} />}

        {!state.error && state.profiles.length > 0 && (
          <Box>
            <SectionHeader
              title='Agent latency profile'
              icon={<TimerOutlinedIcon />}
              subtitle='p50 / p90 / p99 of total invocation latency per agent. Dashed line = selected pXX threshold (24h baseline). Click a bar to filter the table.'
            />
            <AgentLatencyBars
              id='agent-latency-bars'
              profiles={state.profiles}
              thresholdSeconds={state.thresholdSec}
              percentile={state.appliedPctile}
              onSelectAgent={(name) => setIncludeAgents(name ? [name] : [])}
            />
          </Box>
        )}

        {showEmpty ? (
          <EmptyState
            size='section'
            illustration='no-results'
            title='No agent invocations'
            description='Try widening the date range or clearing a filter.'
          />
        ) : (
          !state.error && (
            <CustomTable2
              id='agents-leaderboard'
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

export default AgentsView;
