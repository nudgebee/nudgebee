/**
 * Distribution — "is this run normal for its type" (spec §6e / §8 level-3).
 * Mini distribution of a template's run costs with median, p90, and THIS run
 * marked. Box + DS tokens, no charting dependency.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { fmtCost, type TemplateStats } from '../format';

interface DistributionProps {
  stats: TemplateStats;
  thisRunCost: number;
}

export function Distribution({ stats, thisRunCost }: DistributionProps) {
  const { values, median, p90 } = stats;
  if (!values.length) {
    return <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No comparable runs for this template yet.</Box>;
  }
  const max = Math.max(...values, thisRunCost, p90) || 1;
  const pos = (v: number) => `${Math.min(100, (v / max) * 100)}%`;
  const multiple = median ? thisRunCost / median : 0;
  const hot = multiple >= 2;

  return (
    <Box id='cost-distribution' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
      <Box sx={{ position: 'relative', height: 44 }}>
        {/* baseline track */}
        <Box sx={{ position: 'absolute', top: 30, left: 0, right: 0, height: 2, backgroundColor: 'var(--ds-gray-200)' }} />
        {/* dots for each run */}
        {values.map((v, i) => (
          <Tooltip key={i} title={fmtCost(v)} placement='top'>
            <Box
              sx={{
                position: 'absolute',
                top: 26,
                left: pos(v),
                width: 8,
                height: 8,
                borderRadius: '50%',
                backgroundColor: 'var(--ds-gray-400)',
                transform: 'translateX(-50%)',
              }}
            />
          </Tooltip>
        ))}
        {/* median marker */}
        <Marker left={pos(median)} color='var(--ds-blue-500)' label='median' />
        {/* p90 marker */}
        <Marker left={pos(p90)} color='var(--ds-amber-500)' label='p90' />
        {/* this run */}
        <Box
          sx={{
            position: 'absolute',
            top: 22,
            left: pos(thisRunCost),
            width: 14,
            height: 14,
            borderRadius: '50%',
            backgroundColor: hot ? 'var(--ds-red-500)' : 'var(--ds-green-500)',
            border: '2px solid var(--ds-background-100)',
            transform: 'translateX(-50%)',
            zIndex: 2,
          }}
        />
      </Box>
      <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
        This run is{' '}
        <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: hot ? 'var(--ds-red-700)' : 'var(--ds-green-700)' }}>
          {multiple.toFixed(1)}×
        </Box>{' '}
        the median ({fmtCost(median)}) · p90 {fmtCost(p90)} · {values.length} comparable runs
      </Box>
    </Box>
  );
}

function Marker({ left, color, label }: { left: string; color: string; label: string }) {
  return (
    <Box sx={{ position: 'absolute', top: 0, left, transform: 'translateX(-50%)', display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
      <Box sx={{ fontSize: '9px', color, whiteSpace: 'nowrap' }}>{label}</Box>
      <Box sx={{ width: 2, height: 26, backgroundColor: color }} />
    </Box>
  );
}

export default Distribution;
