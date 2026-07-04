/**
 * ToolCallsModal — the Tools-tab drill-in: the recent invocations of ONE tool,
 * with a status toggle (All / Errors / In-progress) so it doubles as a per-tool
 * invocation explorer and a "view the failures" view.
 *
 * Backed by `ai_list_tool_calls`. Each row shows the conversation (a cross-link
 * that opens the full conversation detail via `onSelectRun`), the owning agent,
 * status, when, duration, and the output/error snippet needed to triage a failure
 * inline. Scoped by the same account + date + source as the leaderboard row it
 * opened from.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import CustomTable2 from '@shared/tables/CustomTable2';
import { Modal } from '@ui/Modal';
import { Banner } from '@ui/Banner';
import { Label } from '@ui/Label';
import { ToggleGroup } from '@ui/ToggleGroup';
import { EmptyState } from '@ui/EmptyState';
import { listToolCalls, toolStatusesFor, type ToolCallRow, type ToolStatusGroup } from '@api1/ai-cost';
import type { CostFilters } from '../types';

interface ToolCallsModalProps {
  open: boolean;
  toolName: string;
  toolType?: string;
  accountId?: string;
  filters: CostFilters;
  /** Status view to open on (defaults to 'errors' when the tool has failures). */
  initialStatus?: ToolStatusGroup;
  onClose: () => void;
  /** Open the full conversation detail, focused on this invocation's agent. */
  onSelectRun: (sessionId: string, accountId?: string, agentId?: string) => void;
}

const STATUS_OPTIONS: { value: ToolStatusGroup; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'errors', label: 'Errors' },
  { value: 'in_progress', label: 'In-progress' },
];

const STATUS_TONE: Record<string, 'success' | 'critical' | 'warning' | 'neutral'> = {
  success: 'success',
  completed: 'success',
  fail: 'critical',
  failed: 'critical',
  error: 'critical',
  terminated: 'critical',
  in_progress: 'warning',
  waiting: 'warning',
  waiting_for_client: 'warning',
};

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;
const secs = (s: number) => `${s.toFixed(1)}s`;

function relTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return 'just now';
  const m = s / 60;
  if (m < 60) return `${Math.floor(m)}m ago`;
  const h = m / 60;
  if (h < 24) return `${Math.floor(h)}h ago`;
  const d = h / 24;
  if (d < 30) return `${Math.floor(d)}d ago`;
  return new Date(t).toISOString().slice(0, 10);
}

const HEADERS = [
  { name: 'Conversation', width: '26%' },
  { name: 'Agent', width: '15%' },
  { name: 'Status', width: '11%' },
  { name: 'When', width: '10%', align: 'right' as const },
  { name: 'Dur', width: '8%', align: 'right' as const },
  { name: 'Output / error', width: '30%' },
];

function toRow(r: ToolCallRow, onOpen: (r: ToolCallRow) => void) {
  const title = r.conversation_title || r.conversation_id || '(conversation)';
  const detail = r.stderr || r.response || r.parameters || '';
  return [
    {
      component: (
        <Box
          component='button'
          type='button'
          title={title}
          onClick={() => onOpen(r)}
          id={`toolcall-link-${r.id}`}
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
      component: <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', overflowWrap: 'anywhere' }}>{r.agent_name || '—'}</Box>,
    },
    { component: <Label tone={STATUS_TONE[(r.status || '').toLowerCase()] ?? 'neutral'} text={r.status || '—'} /> },
    { align: 'right' as const, component: <Box sx={{ ...numCell, color: 'var(--ds-gray-500)' }}>{relTime(r.created_at)}</Box> },
    { align: 'right' as const, component: <Box sx={{ ...numCell, textAlign: 'right' }}>{secs(r.duration_seconds)}</Box> },
    {
      component: detail ? (
        <Box
          title={detail}
          sx={{
            fontFamily: 'var(--ds-font-mono, monospace)',
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-gray-600)',
            whiteSpace: 'pre-wrap',
            display: '-webkit-box',
            WebkitLineClamp: 3,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
            wordBreak: 'break-word',
          }}
        >
          {detail}
        </Box>
      ) : (
        <Box sx={{ ...numCell, color: 'var(--ds-gray-400)' }}>—</Box>
      ),
    },
  ];
}

export function ToolCallsModal({
  open,
  toolName,
  toolType,
  accountId,
  filters,
  initialStatus = 'errors',
  onClose,
  onSelectRun,
}: ToolCallsModalProps) {
  const [status, setStatus] = React.useState<ToolStatusGroup>(initialStatus);
  const [state, setState] = React.useState<{ loading: boolean; error: string | null; rows: ToolCallRow[] }>({
    loading: false,
    error: null,
    rows: [],
  });

  // Reset the toggle to the requested view each time the modal (re)opens for a tool.
  React.useEffect(() => {
    if (open) setStatus(initialStatus);
  }, [open, toolName, initialStatus]);

  const sourcesKey = (filters.sources ?? []).join(',');

  React.useEffect(() => {
    if (!open || !toolName) return;
    const controller = new AbortController();
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    listToolCalls(
      {
        accountIds: accountId ? [accountId] : [],
        startDate: `${filters.startDate}T00:00:00Z`,
        endDate: `${filters.endDate}T23:59:59Z`,
        sources: filters.sources ?? [],
        toolName,
        statuses: toolStatusesFor(status),
        limit: 200,
      },
      controller.signal
    )
      .then((d) => {
        if (!cancelled) setState({ loading: false, error: null, rows: d?.rows ?? [] });
      })
      .catch((e) => {
        if (!cancelled) setState({ loading: false, error: e instanceof Error ? e.message : 'Failed to load tool calls', rows: [] });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [open, toolName, accountId, filters.startDate, filters.endDate, sourcesKey, status]);

  const openRun = (r: ToolCallRow) => {
    onSelectRun(r.conversation_id, r.account_id, r.agent_id);
    onClose();
  };

  const tableData = React.useMemo(() => state.rows.map((r) => toRow(r, openRun)), [state.rows]);
  const showEmpty = !state.loading && !state.error && state.rows.length === 0;

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='lg'
      maxHeight='85vh'
      title={toolName || 'Tool'}
      subtitle={toolType ? `tool_type: ${toolType}` : undefined}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            {state.rows.length} invocation{state.rows.length === 1 ? '' : 's'} · newest first · click a conversation to open the full run
          </Box>
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Filter tool calls by status'
            value={status}
            onChange={(v: ToolStatusGroup) => setStatus(v)}
            options={STATUS_OPTIONS}
          />
        </Box>

        {state.error && <Banner tone='critical' title='Could not load tool calls' message={state.error} />}

        {showEmpty ? (
          <EmptyState
            size='section'
            illustration='no-results'
            title={status === 'errors' ? 'No failures' : 'No invocations'}
            description={status === 'errors' ? 'This tool had no failed calls in range.' : 'Try a different status or widen the date range.'}
          />
        ) : (
          !state.error && <CustomTable2 id='tool-calls-table' headers={HEADERS} tableData={tableData} loading={state.loading} />
        )}
      </Box>
    </Modal>
  );
}

export default ToolCallsModal;
