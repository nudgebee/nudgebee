/**
 * ConversationUsagePanel — the detail "basic summary", backed by the legacy
 * `ai_get_conversation_usage_metrics` action.
 *
 * Shows the figures that action reports reliably and the tree doesn't expose
 * cleanly: success rate, cache hit / savings, the API-vs-tool time split, and a
 * per-model requests table. Per-model **cost** is intentionally omitted here —
 * that action's cost is inconsistent with the row/tree total, so cost lives in
 * the tree-backed sub-task breakdown instead.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import CustomTable2 from '@shared/tables/CustomTable2';
import { Chip } from '@ui/Chip';
import { fmtDuration, fmtTokens } from '../format';
import { MODEL_HUE } from './palette';
import type { ConversationUsageSummary } from '@api1/ai-cost';

interface ConversationUsagePanelProps {
  usage: ConversationUsageSummary;
}

const pct = (v: number | null | undefined): string => (v == null ? '—' : `${v.toFixed(1)}%`);
const numCell = {
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-700)',
  fontVariantNumeric: 'tabular-nums',
  textAlign: 'right' as const,
} as const;

function Stat({ label, value, sub }: { label: string; value: React.ReactNode; sub?: string }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px', minWidth: 120 }}>
      <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{label}</Box>
      <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)' }}>{value}</Box>
      {sub && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{sub}</Box>}
    </Box>
  );
}

export function ConversationUsagePanel({ usage }: ConversationUsagePanelProps) {
  const models = usage.model_usage ?? [];

  const headers = [
    { name: 'Model', width: '32%' },
    { name: 'Requests', width: '14%' },
    { name: 'Tokens', width: '22%', secondryText: '(in/out)' },
    { name: 'Cache hit', width: '16%' },
    { name: 'Success', width: '16%' },
  ];
  const tableData = models.map((m) => [
    {
      component: (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <Chip size='2xs' variant='tag' hue={MODEL_HUE[m.model_name] ?? 'slate'}>
            {m.model_name}
          </Chip>
          <Box component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            {m.model_provider}
          </Box>
        </Box>
      ),
    },
    { component: <Box sx={numCell}>{m.requests}</Box> },
    {
      component: (
        <Box sx={numCell}>
          {fmtTokens(m.input_tokens)} / {fmtTokens(m.output_tokens)}
        </Box>
      ),
    },
    { component: <Box sx={numCell}>{pct(m.cache_hit_rate_percentage)}</Box> },
    {
      component: (
        <Box sx={{ ...numCell, color: m.failed_requests > 0 ? 'var(--ds-amber-700)' : 'var(--ds-gray-700)' }}>{pct(m.success_rate_percentage)}</Box>
      ),
    },
  ]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-5)' }}>
        <Stat label='Requests' value={usage.total_requests} sub={`${usage.successful_requests} ok · ${usage.failed_requests} failed`} />
        <Stat label='Success rate' value={pct(usage.success_rate_percentage)} />
        <Stat label='Tool calls' value={usage.total_tool_calls} sub={`${usage.successful_tool_calls} ok`} />
        <Stat label='Cache hit rate' value={pct(usage.total_cache_hit_rate_percentage)} />
        <Stat label='Tokens saved by cache' value={fmtTokens(usage.cache_savings?.tokens_saved)} />
        <Stat label='Avg latency / call' value={fmtDuration((usage.average_latency_seconds ?? 0) * 1000)} />
        <Stat
          label='Time split'
          value={`${pct(usage.api_time_percentage)} API`}
          sub={`${pct(usage.tool_time_percentage)} tool · ${fmtDuration((usage.wall_time_seconds ?? 0) * 1000)} wall`}
        />
      </Box>

      {!!models.length && <CustomTable2 headers={headers} tableData={tableData} />}
    </Box>
  );
}

export default ConversationUsagePanel;
