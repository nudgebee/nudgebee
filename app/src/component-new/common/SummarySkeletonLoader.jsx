/**
 * SummarySkeletonLoader — domain composition for the cloud-account 3-card
 * summary loading state (Service / Utilization / Cost). Hand-crafted layout
 * tuned to its specific dashboard pattern — not a generic primitive.
 *
 * Previously deprecated 2026-05-07 → demoted to domain composition 2026-05-07.
 * The 3-card layout has no clean 1:1 V2 preset (Skeleton.Card is single-card);
 * inlining 21 skeleton instances at each of 7 call sites was rejected as
 * unreusable code duplication. Internals now use `ds/Skeleton` primitives.
 *
 * For generic placeholder use cases prefer `Skeleton` / `Skeleton.Card` from
 * `@components1/ds/Skeleton` directly.
 */
import React from 'react';
import { Box } from '@mui/material';
import { Skeleton } from '@components1/ds/Skeleton';

const cardSx = {
  backgroundColor: 'var(--ds-background-100)',
  padding: '18px 24px',
  borderRadius: 'var(--ds-radius-md)',
  boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
};

const SummarySkeletonLoader = () => {
  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: '1.5fr 2fr 0.7fr', columnGap: '15px', rowGap: '20px', mb: '25px' }}>
      {/* Service Summary Skeleton */}
      <Box sx={cardSx}>
        <Box sx={{ mb: 2 }}>
          <Skeleton shape='text' width='60%' height={20} />
        </Box>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 2, mb: 2 }}>
          <Box>
            <Skeleton shape='text' width='80%' height={16} />
            <Box sx={{ mt: 0.5 }}>
              <Skeleton shape='text' width='50%' height={28} />
            </Box>
          </Box>
          <Box>
            <Skeleton shape='text' width='70%' height={16} />
            <Box sx={{ mt: 0.5 }}>
              <Skeleton shape='text' width='60%' height={28} />
            </Box>
          </Box>
          <Box>
            <Skeleton shape='text' width='85%' height={16} />
            <Box sx={{ mt: 0.5 }}>
              <Skeleton shape='text' width='55%' height={28} />
            </Box>
          </Box>
        </Box>
      </Box>

      {/* Utilization & Health Skeleton */}
      <Box sx={cardSx}>
        <Box sx={{ mb: 2 }}>
          <Skeleton shape='text' width='40%' height={20} />
        </Box>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2, mb: 3 }}>
          <Box>
            <Skeleton shape='text' width='60%' height={16} />
            <Box sx={{ mt: 0.5 }}>
              <Skeleton shape='text' width='80%' height={24} />
            </Box>
          </Box>
          <Box>
            <Skeleton shape='text' width='70%' height={16} />
            <Box sx={{ mt: 0.5 }}>
              <Skeleton shape='text' width='75%' height={24} />
            </Box>
          </Box>
        </Box>
        <Box sx={{ mb: 1 }}>
          <Skeleton shape='text' width='50%' height={20} />
        </Box>
        <Skeleton shape='rect' width='100%' height={120} />
      </Box>

      {/* Cost Summary Skeleton */}
      <Box sx={cardSx}>
        <Box sx={{ mb: 2 }}>
          <Skeleton shape='text' width='70%' height={20} />
        </Box>
        <Box sx={{ mb: 2 }}>
          <Skeleton shape='text' width='50%' height={16} />
          <Box sx={{ mt: 0.5 }}>
            <Skeleton shape='text' width='80%' height={24} />
          </Box>
          <Box sx={{ mt: 0.5 }}>
            <Skeleton shape='text' width='60%' height={14} />
          </Box>
        </Box>
        <Box sx={{ mb: 2 }}>
          <Skeleton shape='text' width='60%' height={16} />
          <Box sx={{ mt: 0.5 }}>
            <Skeleton shape='text' width='70%' height={24} />
          </Box>
          <Box sx={{ mt: 0.5 }}>
            <Skeleton shape='text' width='55%' height={14} />
          </Box>
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
          <Skeleton shape='circle' width={60} height={60} />
          <Box>
            <Skeleton shape='text' width='40%' height={16} />
            <Box sx={{ mt: 0.5 }}>
              <Skeleton shape='text' width='60%' height={20} />
            </Box>
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default SummarySkeletonLoader;
