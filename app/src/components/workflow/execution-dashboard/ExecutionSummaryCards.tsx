import React from 'react';
import { Box, Typography } from '@mui/material';
import { Card } from '@ui/Card';
import { Stat } from '@ui/Stat';
import { Divider } from '@ui/Divider';
import { ProgressBar } from '@ui/ProgressBar';
import { Skeleton } from '@ui/Skeleton';
import type { ExecutionAggregateResponse } from '@api1/workflow/types';

interface ExecutionSummaryCardsProps {
  aggregate: ExecutionAggregateResponse | null;
  loading: boolean;
  /** Temporal namespace retention, which is the window these counts cover. */
  retentionDays: number;
}

interface ShareRowProps {
  label: string;
  count: number;
  total: number;
  tone: 'success' | 'warning';
  approximate?: boolean;
  id: string;
}

const TONE_INK: Record<ShareRowProps['tone'], string> = {
  success: 'var(--ds-green-700)',
  warning: 'var(--ds-amber-700)',
};

/**
 * One outcome's share of the window: count and percentage over a filled bar.
 *
 * The label/value line sits above the bar rather than using ProgressBar's own
 * `label` + `showValue` composition, which reserves a fixed 80px label column —
 * too narrow for these words and with no room for "33,361 · 71.3%".
 */
const ShareRow: React.FC<ShareRowProps> = ({ label, count, total, tone, approximate, id }) => {
  const pct = total > 0 ? (count / total) * 100 : 0;
  return (
    <Box id={id} data-testid={id} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{label}</Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontFamily: 'var(--ds-font-mono)',
            fontVariantNumeric: 'tabular-nums',
            color: TONE_INK[tone],
          }}
        >
          {`${approximate ? '≈ ' : ''}${count.toLocaleString()} · ${pct.toFixed(1)}%`}
        </Typography>
      </Box>
      {/* Explicit tone: these bars read an outcome on the status axis, not a
          utilisation level, so there is no threshold for the primitive to
          resolve against. */}
      <ProgressBar value={count} max={total || 1} tone={tone} size='sm' />
    </Box>
  );
};

/**
 * The window's failure headline: how many runs failed, out of how many, and how
 * the rest of the window split.
 *
 * These counts are deliberately static — one unfiltered call on page load that
 * does not move when the table below is filtered. The "of N total · last N
 * days" sub-line is what stops that reading as a bug, so don't drop it.
 *
 * Counts carry a "≈" prefix because Temporal documents its count API as
 * approximate; a bare number would overstate what we know.
 */
const ExecutionSummaryCards: React.FC<ExecutionSummaryCardsProps> = ({ aggregate, loading, retentionDays }) => {
  if (loading && !aggregate) {
    return (
      <Card variant='outlined' size='md' id='execution-dashboard-summary' sx={{ minWidth: 0 }}>
        <Skeleton shape='rect' width='100%' height={148} />
      </Card>
    );
  }

  const approximate = !!aggregate?.counts_are_approximate;
  const total = aggregate?.total ?? 0;
  const failed = aggregate?.failed ?? 0;
  const prefix = approximate ? '≈ ' : '';
  // Zero means the retention could not be read; say nothing rather than guess.
  const windowLabel = retentionDays > 0 ? ` · last ${retentionDays} days` : '';

  return (
    <Card
      variant='outlined'
      size='md'
      id='execution-dashboard-summary'
      data-testid='execution-dashboard-summary'
      // The grid row already stretches both cards to the same height (an
      // explicit height:100% would be added to the card's own padding, since
      // this surface is content-box, and overflow the row). The column here
      // just lets the shares settle on the bottom edge of whatever height the
      // row lands on.
      sx={{ minWidth: 0, display: 'flex', flexDirection: 'column' }}
    >
      <Stat
        size='hero'
        label='Failed executions'
        // The value carries the status axis here rather than a delta: the whole
        // point of this tile is that failures are the headline. Stat's "don't
        // tone the value" rule guards against decorative colour, not against a
        // status read-out.
        value={
          <Box component='span' sx={{ color: 'var(--ds-red-700)' }}>
            {`${prefix}${failed.toLocaleString()}`}
          </Box>
        }
        sub={`of ${prefix}${total.toLocaleString()} total${windowLabel}`}
        info={{ tooltip: 'Counts runs that failed, were terminated, or timed out. Cancelled runs are excluded.' }}
      />

      <Box sx={{ margin: 'var(--ds-space-4) 0 0', flex: 1, display: 'flex', alignItems: 'flex-end' }}>
        <Divider />
      </Box>

      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)', paddingTop: 'var(--ds-space-4)' }}>
        <ShareRow
          id='execution-dashboard-share-succeeded'
          label='Successful'
          count={aggregate?.succeeded ?? 0}
          total={total}
          tone='success'
          approximate={approximate}
        />
        {/* A subset of the failed count, not a peer of it: a run that ran too
            long is a different fix from one that threw. */}
        <ShareRow
          id='execution-dashboard-share-timed-out'
          label='Timed out'
          count={aggregate?.timed_out ?? 0}
          total={total}
          tone='warning'
          approximate={approximate}
        />
      </Box>
    </Card>
  );
};

export default ExecutionSummaryCards;
