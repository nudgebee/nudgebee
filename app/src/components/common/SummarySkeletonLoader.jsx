import React from 'react';
import { Box } from '@mui/material';
import { Skeleton } from '@ui/Skeleton';
import { ds } from 'src/utils/colors';

// ds/Skeleton is inline-block; MUI's was block. These placeholders stack vertically,
// so keep them block-level to preserve the existing layout.
// MUI painted its text bars at 60% of their box, which left a gap between stacked
// lines; ds/Skeleton fills the box, so add that separation back explicitly.
const block = { display: 'block', marginBottom: ds.space[1] };

const SummarySkeletonLoader = () => {
  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: '1.5fr 2fr 0.7fr',
        columnGap: 'var(--ds-space-4)',
        rowGap: 'var(--ds-space-4)',
        mb: 'var(--ds-space-5)',
      }}
    >
      {/* Service Summary Skeleton */}
      <Box
        sx={{
          backgroundColor: ds.background[100],
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          borderRadius: 'var(--ds-radius-lg)',
          boxShadow: `0px ${ds.space[1]} ${ds.space.mul(0, 3)} -1px ${ds.gray.alpha[100]}, 0px ${ds.space[0]} ${ds.space[1]} -2px ${
            ds.gray.alpha[100]
          }`,
        }}
      >
        <Skeleton shape='text' width='60%' height={20} sx={{ ...block, mb: ds.space[4] }} />
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 2, mb: 2 }}>
          <Box>
            <Skeleton shape='text' width='80%' height={16} sx={block} />
            <Skeleton shape='text' width='50%' height={28} sx={block} />
          </Box>
          <Box>
            <Skeleton shape='text' width='70%' height={16} sx={block} />
            <Skeleton shape='text' width='60%' height={28} sx={block} />
          </Box>
          <Box>
            <Skeleton shape='text' width='85%' height={16} sx={block} />
            <Skeleton shape='text' width='55%' height={28} sx={block} />
          </Box>
        </Box>
      </Box>

      {/* Utilization & Health Skeleton */}
      <Box
        sx={{
          backgroundColor: ds.background[100],
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          borderRadius: 'var(--ds-radius-lg)',
          boxShadow: `0px ${ds.space[1]} ${ds.space.mul(0, 3)} -1px ${ds.gray.alpha[100]}, 0px ${ds.space[0]} ${ds.space[1]} -2px ${
            ds.gray.alpha[100]
          }`,
        }}
      >
        <Skeleton shape='text' width='40%' height={20} sx={{ ...block, mb: ds.space[4] }} />
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2, mb: 3 }}>
          <Box>
            <Skeleton shape='text' width='60%' height={16} sx={block} />
            <Skeleton shape='text' width='80%' height={24} sx={block} />
          </Box>
          <Box>
            <Skeleton shape='text' width='70%' height={16} sx={block} />
            <Skeleton shape='text' width='75%' height={24} sx={block} />
          </Box>
        </Box>
        <Skeleton shape='text' width='50%' height={20} sx={{ ...block, mb: ds.space[2] }} />
        <Skeleton shape='rect' height={120} sx={block} />
      </Box>

      {/* Cost Summary Skeleton */}
      <Box
        sx={{
          backgroundColor: ds.background[100],
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          borderRadius: 'var(--ds-radius-lg)',
          boxShadow: `0px ${ds.space[1]} ${ds.space.mul(0, 3)} -1px ${ds.gray.alpha[100]}, 0px ${ds.space[0]} ${ds.space[1]} -2px ${
            ds.gray.alpha[100]
          }`,
        }}
      >
        <Skeleton shape='text' width='70%' height={20} sx={{ ...block, mb: ds.space[4] }} />
        <Box sx={{ mb: 2 }}>
          <Skeleton shape='text' width='50%' height={16} sx={block} />
          <Skeleton shape='text' width='80%' height={24} sx={block} />
          <Skeleton shape='text' width='60%' height={14} sx={block} />
        </Box>
        <Box sx={{ mb: 2 }}>
          <Skeleton shape='text' width='60%' height={16} sx={block} />
          <Skeleton shape='text' width='70%' height={24} sx={block} />
          <Skeleton shape='text' width='55%' height={14} sx={block} />
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
          <Skeleton shape='circle' width={60} height={60} sx={block} />
          <Box>
            <Skeleton shape='text' width='40%' height={16} sx={block} />
            <Skeleton shape='text' width='60%' height={20} sx={block} />
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default SummarySkeletonLoader;
