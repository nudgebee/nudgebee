/**
 * ConversationsTable — run/conversation list (spec §4d top-10, §5 explorer).
 *
 * Built on `@shared/tables/CustomTable`:
 *   - "Models" column lists every model with its call count + cost.
 *   - Sortable columns via the table's own header sort icons (Trigger, Cost,
 *     Tokens[by input], Duration, Latency, Calls). No separate sort control.
 *   - Each row expands to a "Tasks" panel — a nested table of the steps (adds a
 *     Wait-time column; sortable by cost / input tokens / wait / latency;
 *     default order is task sequence). This is in addition to the detail popup.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import FormatListBulletedIcon from '@mui/icons-material/FormatListBulleted';
import PaidOutlinedIcon from '@mui/icons-material/PaidOutlined';
import TimerOutlinedIcon from '@mui/icons-material/TimerOutlined';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import RepeatOutlinedIcon from '@mui/icons-material/RepeatOutlined';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import OpenInNewOutlinedIcon from '@mui/icons-material/OpenInNewOutlined';
import ArrowForwardRoundedIcon from '@mui/icons-material/ArrowForwardRounded';
import CustomTable2 from '@shared/tables/CustomTable2';
import { Button } from '@ui/Button';
import { Label } from '@ui/Label';
import { Chip } from '@ui/Chip';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import { ToggleGroup } from '@ui/ToggleGroup';
import Tooltip from '@ui/Tooltip';
import HeaderLabel from './HeaderLabel';
import { fmtCost, fmtDuration, fmtTokens, runCallCount, runModelBreakdown, stepModelBreakdown, triggerLabel, type ModelStat } from '../format';
import { makeSeverity, SeverityCell } from './severity';
import type { Run, RunStatus, Step } from '../types';

export type ConvSortKey = 'start' | 'trigger' | 'cost' | 'tokens' | 'duration' | 'latency' | 'calls';

type ConvView = 'all' | 'cost' | 'latency' | 'calls' | 'failed';

const CONV_VIEW_OPTIONS = [
  { value: 'all', label: 'All', icon: <FormatListBulletedIcon sx={{ fontSize: 14 }} /> },
  { value: 'cost', label: 'Top 5 by cost', icon: <PaidOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'latency', label: 'Top 5 by latency', icon: <TimerOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'calls', label: 'Top 5 by calls', icon: <RepeatOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'failed', label: 'Show failed', icon: <ErrorOutlineIcon sx={{ fontSize: 14 }} /> },
];

interface ConversationsTableProps {
  runs: Run[];
  /** account_id → display name, for the per-row account label beside the source. */
  accountNameById?: Record<string, string>;
  onSelectRun: (runId: string) => void;
  /** Open the detail modal straight on the Optimize tab and run/show the analysis. */
  onAnalyse?: (runId: string) => void;
  /** Compact = fewer columns (Overview top-10). */
  compact?: boolean;
  /** Initial sort (default differs per report). */
  defaultSort?: { key: ConvSortKey; order: 'asc' | 'desc' };
  id?: string;
  /** Caption rendered below the preset toggle row (e.g. "Showing top N of M…"). */
  caption?: React.ReactNode;
  /** Actions rendered at the top-right of the toggle row (e.g. Export CSV). */
  headerActions?: React.ReactNode;
  /** When true, the table renders its own skeleton rows (no external spinner). */
  loading?: boolean;
}

const STATUS_TONE: Record<RunStatus, 'success' | 'critical' | 'warning' | 'neutral'> = {
  completed: 'success',
  failed: 'critical',
  'awaiting-approval': 'warning',
  cancelled: 'neutral',
};

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

// Header name → sort key (only sortable headers appear here).
const HEADER_TO_KEY: Record<string, ConvSortKey> = {
  Trigger: 'trigger',
  Cost: 'cost',
  Tokens: 'tokens',
  Duration: 'duration',
  Latency: 'latency',
  Calls: 'calls',
};
const KEY_TO_HEADER: Record<ConvSortKey, string> = {
  start: 'Start time',
  trigger: 'Trigger',
  cost: 'Cost',
  tokens: 'Tokens',
  duration: 'Duration',
  latency: 'Latency',
  calls: 'Calls',
};

function runValue(r: Run, key: ConvSortKey): number | string {
  switch (key) {
    case 'trigger':
      return r.triggerType;
    case 'cost':
      return r.totalCost;
    case 'tokens':
      return r.totalInputTokens;
    case 'duration':
      return r.wallClockMs;
    case 'latency':
      return r.totalModelLatencyMs;
    case 'calls':
      return runCallCount(r);
    case 'start':
    default:
      return Date.parse(r.startedAt);
  }
}

function StartTime({ iso }: { iso: string }) {
  const d = new Date(iso);
  const hhmm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  const ddmmm = `${String(d.getDate()).padStart(2, '0')}-${MONTHS[d.getMonth()]}`;
  return (
    <Box sx={{ lineHeight: 1.2 }}>
      <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' }}>{hhmm}</Box>
      <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>{ddmmm}</Box>
    </Box>
  );
}

function ModelBadges({ models, dense }: { models: ModelStat[]; dense?: boolean }) {
  if (!models.length)
    return (
      <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
        —
      </Box>
    );
  return (
    <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
      {models.map((m) => (
        <Chip key={m.model} size={dense ? '2xs' : 'xs'} variant='tag' tone='subtle'>
          {m.model}
          {/* List rows carry no per-model call/cost split — show just the name then. */}
          {(m.calls > 0 || m.cost > 0) && (
            <Box component='span' sx={{ ml: '3px', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-regular)' }}>
              ({m.calls} · {fmtCost(m.cost)})
            </Box>
          )}
        </Chip>
      ))}
    </Box>
  );
}

/* ── Nested tasks table (expanded-row content) ──────────────────────────── */

type TaskSortKey = 'seq' | 'cost' | 'tokens' | 'wait' | 'latency';
const TASK_HEADER_TO_KEY: Record<string, TaskSortKey> = { Cost: 'cost', Tokens: 'tokens', 'Wait time': 'wait', Latency: 'latency' };

function taskValue(s: Step, key: TaskSortKey): number {
  switch (key) {
    case 'cost':
      return s.stepCost;
    case 'tokens':
      return s.stepInputTokens;
    case 'wait':
      return s.waitTimeMs;
    case 'latency':
      return s.stepLatencyMs;
    case 'seq':
    default:
      return s.sequence;
  }
}

function TaskTable({ run }: { run: Run }) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: '', order: 'asc' });
  const runCost = run.totalCost || 1;

  const steps = React.useMemo(() => {
    const key = TASK_HEADER_TO_KEY[sort.name] ?? 'seq';
    const sorted = [...run.steps].sort((a, b) => taskValue(a, key) - taskValue(b, key));
    return sort.name && sort.order === 'desc' ? sorted.reverse() : sorted;
  }, [run.steps, sort]);

  const headers = [
    { name: 'Task', width: '24%' },
    { name: 'Models (calls · cost)', width: '30%' },
    { name: 'Latency', width: '11%', sortEnabled: true },
    { name: 'Wait time', width: '10%', sortEnabled: true },
    { name: 'Tokens', width: '13%', sortEnabled: true, secondryText: '(in/out)' },
    { name: 'Cost', width: '7%', sortEnabled: true },
    { name: '% run', width: '5%' },
  ];
  const tableData = steps.map((step) => [
    {
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>
          {step.sequence}. {step.agent}
        </Box>
      ),
    },
    { component: <ModelBadges models={stepModelBreakdown(step)} dense /> },
    { component: <Box sx={numCell}>{fmtDuration(step.stepLatencyMs)}</Box> },
    {
      component: (
        <Box sx={{ ...numCell, color: step.waitTimeMs ? 'var(--ds-amber-700)' : 'var(--ds-gray-400)' }}>
          {step.waitTimeMs ? fmtDuration(step.waitTimeMs) : '—'}
        </Box>
      ),
    },
    {
      component: (
        <Box sx={numCell}>
          {fmtTokens(step.stepInputTokens)} / {fmtTokens(step.stepOutputTokens)}
        </Box>
      ),
    },
    { component: <CostCallout value={step.stepCost} size='sm' tone='neutral' fractionDigits={2} /> },
    { component: <Box sx={{ ...numCell, color: 'var(--ds-gray-500)' }}>{Math.round((step.stepCost / runCost) * 100)}%</Box> },
  ]);

  return (
    <CustomTable2 headers={headers} tableData={tableData} sort={sort} onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)} />
  );
}

// Row expansion only applies to mock runs (they carry the in-memory task tree).
// API-backed rows show per-model calls+cost inline in the Models column, so they
// don't expand.
const TASK_TAB = [
  { text: 'Tasks', value: 0, key: 'tasks', componentFn: (_o: unknown, q: { run?: Run }) => (q?.run ? <TaskTable run={q.run} /> : <></>) },
];

/* ── Conversations table ────────────────────────────────────────────────── */

export function ConversationsTable({
  runs,
  accountNameById,
  onSelectRun,
  onAnalyse,
  compact = false,
  defaultSort = { key: 'start', order: 'desc' },
  id,
  caption,
  headerActions,
  loading = false,
}: ConversationsTableProps) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: KEY_TO_HEADER[defaultSort.key], order: defaultSort.order });
  const [view, setView] = React.useState<ConvView>('all');

  // Picking a "Top 5 by X" preset syncs the column sort to the same metric, so
  // the visible order matches the preset. Without this, the default cost sort
  // would re-order e.g. the top-5-by-calls set by cost (the inconsistency the
  // user flagged). 'all' / 'failed' restore the table's default sort.
  const handleViewChange = (v: string) => {
    const next = (v as ConvView) || 'all';
    setView(next);
    if (next === 'cost' || next === 'latency' || next === 'calls') {
      setSort({ name: KEY_TO_HEADER[next], order: 'desc' });
    } else {
      setSort({ name: KEY_TO_HEADER[defaultSort.key], order: defaultSort.order });
    }
  };

  // Quick-filter presets narrow the set; the column sort then orders what's shown.
  const viewRuns = React.useMemo(() => {
    if (view === 'cost') return [...runs].sort((a, b) => b.totalCost - a.totalCost).slice(0, 5);
    if (view === 'latency') return [...runs].sort((a, b) => b.totalModelLatencyMs - a.totalModelLatencyMs).slice(0, 5);
    if (view === 'calls') return [...runs].sort((a, b) => runCallCount(b) - runCallCount(a)).slice(0, 5);
    if (view === 'failed') return runs.filter((r) => r.status === 'failed');
    return runs;
  }, [runs, view]);

  const sortedRuns = React.useMemo(() => {
    const key = HEADER_TO_KEY[sort.name] ?? defaultSort.key;
    const sorted = [...viewRuns].sort((a, b) => {
      const av = runValue(a, key);
      const bv = runValue(b, key);
      return typeof av === 'string' ? String(av).localeCompare(String(bv)) : (av as number) - (bv as number);
    });
    return sort.order === 'desc' ? sorted.reverse() : sorted;
  }, [viewRuns, sort, defaultSort.key]);

  // While loading, fall through to the table so it can render its own skeleton
  // rows; only show the empty state once a load has finished with no rows.
  if (!runs.length && !loading) {
    return (
      <EmptyState
        size='section'
        illustration='no-results'
        title='No conversations match these filters'
        description='Try widening the date range or clearing a filter.'
      />
    );
  }

  const H = {
    conversation: <HeaderLabel label='Conversation' info='The conversation / run. The chip shows its source (what triggered it).' />,
    models: <HeaderLabel label='Models' secondary='(calls · cost)' info='Models used in this conversation, with per-model calls · cost.' />,
    start: <HeaderLabel label='Start time' info='When the conversation started.' />,
    duration: <HeaderLabel label='Duration' info='Wall-clock time from start to end.' />,
    latency: <HeaderLabel label='Latency' info='Total time spent waiting on model responses.' />,
    calls: <HeaderLabel label='Calls' info='Total model (LLM) API calls in the conversation.' />,
    cost: <HeaderLabel label='Cost' info='Total cost of the conversation.' />,
    tokens: <HeaderLabel label='Tokens' secondary='(in/out)' info='Input / output tokens across the conversation.' />,
    status: <HeaderLabel label='Status' info='Final state of the conversation.' />,
  };
  const headers = compact
    ? [
        { name: 'Conversation', width: '24%', component: H.conversation },
        { name: 'Models (calls · cost)', width: '23%', component: H.models },
        { name: 'Start time', width: '8%', component: H.start },
        { name: 'Duration', width: '9%', sortEnabled: true, component: H.duration },
        { name: 'Latency', width: '10%', sortEnabled: true, component: H.latency },
        { name: 'Calls', width: '7%', sortEnabled: true, component: H.calls },
        { name: 'Cost', width: '8%', sortEnabled: true, component: H.cost },
        { name: 'Tokens', width: '11%', sortEnabled: true, component: H.tokens },
      ]
    : // Fixed layout: widths must sum to ~100%. Conversation takes the bulk;
      // Models keeps enough room (~16%) that its nowrap chips never overflow.
      [
        { name: 'Conversation', width: '24%', component: H.conversation },
        { name: 'Models (calls · cost)', width: '10%', component: H.models },
        { name: 'Start time', width: '6%', component: H.start },
        { name: 'Duration', width: '4%', sortEnabled: true, component: H.duration },
        { name: 'Latency', width: '6%', sortEnabled: true, component: H.latency },
        { name: 'Calls', width: '5%', sortEnabled: true, component: H.calls },
        { name: 'Cost', width: '4%', sortEnabled: true, component: H.cost },
        { name: 'Tokens', width: '6%', sortEnabled: true, component: H.tokens },
        { name: 'Status', width: '6%', component: H.status },
        { name: 'Actions', width: '4%' },
      ];

  // Relative outlier highlighting: rank cost / model-latency within the rows shown.
  const costSev = React.useMemo(() => makeSeverity(sortedRuns.map((r) => r.totalCost)), [sortedRuns]);
  const latSev = React.useMemo(() => makeSeverity(sortedRuns.map((r) => r.totalModelLatencyMs)), [sortedRuns]);

  const tableData = sortedRuns.map((run) => {
    const models = runModelBreakdown(run);
    const titleCell = {
      drilldownQuery: { run },
      component: (
        // The table runs in fixed layout (see the `& table` sx below), so the
        // header `width` percentages are honored exactly — no minWidth floor
        // needed here (a floor would only overflow the cell on narrow widths).
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', minWidth: 0, alignItems: 'flex-start', width: '100%' }}>
          {/* Plain title text — the whole row is clickable (opens the detail
              popup), so the title is no longer a separate link. Normal text,
              medium (500) weight, clamped to 2 lines, full title in a DS Tooltip. */}
          <Tooltip title={run.title} placement='top'>
            <Box
              sx={{
                width: '100%',
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-gray-700)',
                display: '-webkit-box',
                WebkitLineClamp: 2,
                WebkitBoxOrient: 'vertical',
                overflow: 'hidden',
                wordBreak: 'break-word',
              }}
            >
              {run.title}
            </Box>
          </Tooltip>
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', flexWrap: 'wrap', alignItems: 'center' }}>
            <Chip size='xs' variant='tag' hue='violet'>
              {run.source ?? triggerLabel[run.triggerType]}
            </Chip>
            {run.accountId && accountNameById?.[run.accountId] && (
              <Chip size='xs' variant='tag' hue='teal'>
                {accountNameById[run.accountId]}
              </Chip>
            )}
          </Box>
        </Box>
      ),
    };
    const modelsCell = { component: <ModelBadges models={models} /> };
    const start = { component: <StartTime iso={run.startedAt} /> };
    const duration = { component: <Box sx={numCell}>{fmtDuration(run.wallClockMs)}</Box> };
    const llmLatency = {
      component: (
        <SeverityCell severity={latSev(run.totalModelLatencyMs)} metric='latency'>
          <Box sx={numCell}>{fmtDuration(run.totalModelLatencyMs)}</Box>
        </SeverityCell>
      ),
    };
    const calls = { component: <Box sx={numCell}>{runCallCount(run)}</Box> };
    // Cost rendered as normal dark numeric text (matches the other metric
    // columns) rather than CostCallout's muted gray cost-axis neutral tone.
    const cost = {
      component: (
        <SeverityCell severity={costSev(run.totalCost)} metric='cost'>
          <Box sx={{ ...numCell, fontWeight: 'var(--ds-font-weight-semibold)' }}>{fmtCost(run.totalCost)}</Box>
        </SeverityCell>
      ),
    };
    const tokens = {
      component: (
        <Box sx={numCell}>
          {fmtTokens(run.totalInputTokens)} / {fmtTokens(run.totalOutputTokens)}
        </Box>
      ),
    };

    if (compact) {
      return [titleCell, modelsCell, start, duration, llmLatency, calls, cost, tokens];
    }
    return [
      titleCell,
      modelsCell,
      start,
      duration,
      llmLatency,
      calls,
      cost,
      tokens,
      { component: <Label tone={STATUS_TONE[run.status]} text={run.status} /> },
      {
        component: (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', alignItems: 'center' }}>
            {/* Compact icon-only View details (the row itself also opens it). */}
            <Button
              tone='secondary'
              size='xs'
              composition='icon-only'
              icon={<VisibilityOutlinedIcon />}
              aria-label='View details'
              tooltip='View details'
              onClick={() => onSelectRun(run.runId)}
              id={`view-run-${run.runId}`}
            />
            {/* Deep-link to the original conversation in the Ask-Nubi chat (new tab). */}
            <Button
              tone='secondary'
              size='xs'
              composition='icon-only'
              icon={<OpenInNewOutlinedIcon />}
              aria-label='Go to conversation'
              tooltip='Go to conversation'
              onClick={() =>
                window.open(
                  `/ask-nudgebee?accountId=${run.accountId ?? ''}&session_id=${run.sessionId ?? run.runId}`,
                  '_blank',
                  'noopener,noreferrer'
                )
              }
              id={`goto-run-${run.runId}`}
            />
            {onAnalyse && (
              <Button
                tone='secondary'
                size='xs'
                trailingAccent={<ArrowForwardRoundedIcon />}
                onClick={() => onAnalyse(run.runId)}
                id={`analyse-run-${run.runId}`}
              >
                Analyse
              </Button>
            )}
          </Box>
        ),
      },
    ];
  });

  // Only mock runs carry a per-task tree to expand; API rows show per-model
  // calls+cost inline (no expansion).
  const expandable = sortedRuns.some((r) => r.steps.length > 0) ? { tabs: TASK_TAB } : undefined;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      {/* Preset toggles sit top-left; the "Showing top N…" caption sits just
          below them, with optional actions (Export CSV) at the top-right. */}
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Filter conversations'
            value={view}
            onChange={handleViewChange}
            options={CONV_VIEW_OPTIONS}
          />
          {headerActions}
        </Box>
        {caption && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{caption}</Box>}
      </Box>
      <CustomTable2
        id={id}
        headers={headers}
        tableData={tableData}
        expandable={expandable}
        loading={loading}
        sort={sort}
        onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
        onRowClick={(q: { run?: Run }) => q?.run && onSelectRun(q.run.runId)}
      />
    </Box>
  );
}

export default ConversationsTable;
