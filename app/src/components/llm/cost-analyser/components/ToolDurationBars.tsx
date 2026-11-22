/**
 * ToolDurationBars — per-tool latency, top tools by p90 duration. A horizontal bar
 * per tool: the solid part is p90, the lighter tail extends to the slowest (max)
 * call, all scaled to the slowest max across the shown tools — so the slow tools
 * and their tail risk stand out. Box + DS tokens, no charting dependency (same
 * lightweight pattern as CostTreemap). Click a row to open its invocations.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import type { ToolUsageRow, ToolStatusGroup } from '@api1/ai-cost';

interface ToolDurationBarsProps {
  rows: ToolUsageRow[];
  /** How many tools to show (by p90 duration). */
  topN?: number;
  /** Open a tool's drill-in (defaults to all invocations). */
  onSelect?: (row: ToolUsageRow, status: ToolStatusGroup) => void;
  id?: string;
}

const secs = (s: number) => `${s.toFixed(1)}s`;

export function ToolDurationBars({ rows, topN = 8, onSelect, id }: ToolDurationBarsProps) {
  const shown = React.useMemo(() => [...rows].sort((a, b) => b.p90_duration_seconds - a.p90_duration_seconds).slice(0, topN), [rows, topN]);
  // Scale every bar to the slowest max across the shown tools (min 0.001 guards /0).
  const scaleMax = React.useMemo(() => Math.max(0.001, ...shown.map((r) => r.max_duration_seconds)), [shown]);

  if (!shown.length) return null;

  return (
    // One shared grid: the label column sizes to the widest "p90 · max" text, so every row's
    // bar starts and ends at the same x instead of shrinking with its own label.
    <Box
      id={id}
      sx={{
        display: 'grid',
        gridTemplateColumns: '140px minmax(0, 1fr) max-content',
        columnGap: 'var(--ds-space-3)',
        rowGap: 'var(--ds-space-2)',
      }}
    >
      {shown.map((r) => {
        const p90Pct = Math.min(100, (r.p90_duration_seconds / scaleMax) * 100);
        const tailPct = Math.min(100 - p90Pct, (Math.max(0, r.max_duration_seconds - r.p90_duration_seconds) / scaleMax) * 100);
        const clickable = !!onSelect;
        return (
          <Box
            key={r.tool_name}
            component={clickable ? 'button' : 'div'}
            type={clickable ? 'button' : undefined}
            onClick={clickable ? () => onSelect!(r, 'all') : undefined}
            title={`${r.tool_name}: avg ${secs(r.avg_duration_seconds)} · p90 ${secs(r.p90_duration_seconds)} · max ${secs(r.max_duration_seconds)}`}
            sx={{
              all: clickable ? 'unset' : undefined,
              display: 'grid',
              gridColumn: '1 / -1',
              gridTemplateColumns: 'subgrid',
              alignItems: 'center',
              boxSizing: 'border-box',
              cursor: clickable ? 'pointer' : 'default',
              '&:hover .tool-dur-name': clickable ? { textDecoration: 'underline' } : undefined,
              '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: 2, borderRadius: 'var(--ds-radius-sm)' },
            }}
          >
            <Box
              className='tool-dur-name'
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: clickable ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
                textAlign: 'left',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {r.tool_name}
            </Box>
            <Box
              sx={{
                minWidth: 0,
                display: 'flex',
                height: 12,
                borderRadius: 'var(--ds-radius-sm)',
                overflow: 'hidden',
                border: '1px solid var(--ds-gray-200)',
                backgroundColor: 'var(--ds-gray-100)',
              }}
            >
              {p90Pct > 0 ? <Box sx={{ width: `${p90Pct}%`, height: '100%', backgroundColor: 'var(--ds-blue-400)' }} /> : null}
              {tailPct > 0 ? <Box sx={{ width: `${tailPct}%`, height: '100%', backgroundColor: 'var(--ds-blue-200)' }} /> : null}
            </Box>
            <Box
              sx={{
                whiteSpace: 'nowrap',
                textAlign: 'left',
                fontSize: 'var(--ds-text-small)',
                fontVariantNumeric: 'tabular-nums',
                color: 'var(--ds-gray-600)',
              }}
            >
              p90 {secs(r.p90_duration_seconds)} · max {secs(r.max_duration_seconds)}
            </Box>
          </Box>
        );
      })}
    </Box>
  );
}

export default ToolDurationBars;
