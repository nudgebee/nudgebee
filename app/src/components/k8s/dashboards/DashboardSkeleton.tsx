import React from 'react';
import { Box, Stack } from '@mui/material';
import { Skeleton } from '@ui/Skeleton';
import { ds } from '@utils/colors';

/**
 * The waiting state for a dashboard that is still loading.
 *
 * A single flat block was technically a skeleton and practically invisible:
 * the primitive fills with `--ds-gray-100`, which on a light page reads as
 * empty space, so a deep-linked dashboard looked like a blank screen for as
 * long as the account list and the dashboard took to arrive.
 *
 * This mirrors the shape of what is coming — toolbar, then a grid of panels
 * with their header rules — which is what the Skeleton spec asks for ("render
 * at dimensions that match the eventual content"). The panel BORDERS are what
 * make it visible; the fill alone never was.
 */

/** Enough to read as a grid without becoming a wall of placeholders (spec: 5 is plenty). */
const PANEL_COUNT = 4;

const PanelSkeleton: React.FC = () => (
  <Box
    sx={{
      border: `1px solid ${ds.gray[300]}`,
      borderRadius: '8px',
      background: ds.background[100],
      overflow: 'hidden',
    }}
  >
    {/* Header rule, matching DashboardPanel's own */}
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, px: 1.25, py: 1, borderBottom: `1px solid ${ds.gray[200]}` }}>
      <Skeleton shape='text' size='text' width='40%' />
      <Box sx={{ flex: 1 }} />
      <Skeleton shape='rect' width={54} height={16} />
    </Box>
    <Box sx={{ p: 1.25 }}>
      <Skeleton shape='rect' width='100%' height={160} />
    </Box>
  </Box>
);

const DashboardSkeleton: React.FC = () => (
  <Box sx={{ p: 3 }} data-testid='dashboard-skeleton'>
    <Stack direction='row' alignItems='center' gap={1.5} sx={{ mb: 2.5 }}>
      <Skeleton shape='text' size='title' width={220} />
      <Box sx={{ flex: 1 }} />
      <Skeleton shape='rect' width={150} height={32} />
      <Skeleton shape='rect' width={32} height={32} />
    </Stack>
    <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: '10px' }}>
      {Array.from({ length: PANEL_COUNT }, (_, i) => (
        <PanelSkeleton key={i} />
      ))}
    </Box>
  </Box>
);

export default DashboardSkeleton;
