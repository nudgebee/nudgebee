/**
 * ToolReliabilityBars — per-tool success/error composition, top tools by failure
 * count. A segmented horizontal bar per tool (success → in-progress → error →
 * other), so an unreliable tool is obvious at a glance and the error *rate* reads
 * directly off the red share. Box + DS tokens, no charting dependency (same
 * lightweight pattern as CostTreemap). Click a row to open its failures.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import type { ToolUsageRow, ToolStatusGroup } from '@api1/ai-cost';

interface ToolReliabilityBarsProps {
  rows: ToolUsageRow[];
  /** How many tools to show (by failure count). */
  topN?: number;
  /** Open a tool's drill-in (defaults to its failures). */
  onSelect?: (row: ToolUsageRow, status: ToolStatusGroup) => void;
  id?: string;
}

const SEGMENTS: { key: keyof ToolUsageRow; label: string; color: string }[] = [
  { key: 'success_count', label: 'success', color: 'var(--ds-green-400)' },
  { key: 'in_progress_count', label: 'in-progress', color: 'var(--ds-amber-300)' },
  { key: 'error_count', label: 'error', color: 'var(--ds-red-400)' },
];

export function ToolReliabilityBars({ rows, topN = 8, onSelect, id }: ToolReliabilityBarsProps) {
  const shown = React.useMemo(() => [...rows].sort((a, b) => b.error_count - a.error_count).slice(0, topN), [rows, topN]);

  if (!shown.length) return null;

  return (
    <Box id={id} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
      {shown.map((r) => {
        const known = r.success_count + r.in_progress_count + r.error_count;
        const other = Math.max(0, r.calls - known);
        const seg = (n: number) => (r.calls > 0 ? (n / r.calls) * 100 : 0);
        const clickable = !!onSelect;
        const title = `${
          r.tool_name
        }: ${r.success_count.toLocaleString()} ok · ${r.in_progress_count.toLocaleString()} in-progress · ${r.error_count.toLocaleString()} error of ${r.calls.toLocaleString()} (${r.error_rate_pct.toFixed(
          1
        )}% err)`;
        return (
          <Box
            key={r.tool_name}
            component={clickable ? 'button' : 'div'}
            type={clickable ? 'button' : undefined}
            onClick={clickable ? () => onSelect!(r, 'errors') : undefined}
            title={title}
            sx={{
              all: clickable ? 'unset' : undefined,
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--ds-space-3)',
              width: '100%',
              boxSizing: 'border-box',
              cursor: clickable ? 'pointer' : 'default',
              '&:hover .tool-rel-name': clickable ? { textDecoration: 'underline' } : undefined,
              '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: 2, borderRadius: 'var(--ds-radius-sm)' },
            }}
          >
            <Box
              className='tool-rel-name'
              sx={{
                width: 140,
                flexShrink: 0,
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
                flex: 1,
                minWidth: 0,
                display: 'flex',
                height: 12,
                borderRadius: 'var(--ds-radius-sm)',
                overflow: 'hidden',
                border: '1px solid var(--ds-gray-200)',
                backgroundColor: 'var(--ds-gray-100)',
              }}
            >
              {SEGMENTS.map((s) => {
                const pct = seg(r[s.key] as number);
                return pct > 0 ? <Box key={s.label} sx={{ width: `${pct}%`, height: '100%', backgroundColor: s.color }} /> : null;
              })}
              {other > 0 ? <Box sx={{ width: `${seg(other)}%`, height: '100%', backgroundColor: 'var(--ds-gray-300)' }} /> : null}
            </Box>
            <Box
              sx={{
                width: 64,
                flexShrink: 0,
                textAlign: 'right',
                fontSize: 'var(--ds-text-small)',
                fontVariantNumeric: 'tabular-nums',
                color: r.error_rate_pct > 0 ? 'var(--ds-red-600)' : 'var(--ds-gray-500)',
              }}
            >
              {r.error_rate_pct.toFixed(0)}% err
            </Box>
          </Box>
        );
      })}
    </Box>
  );
}

export default ToolReliabilityBars;
