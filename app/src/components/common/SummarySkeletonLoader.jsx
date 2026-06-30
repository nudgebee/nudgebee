import React from 'react';
import { Box, Skeleton } from '@mui/material';

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
          backgroundColor: 'rgba(255, 255, 255, 1)',
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          borderRadius: 'var(--ds-radius-lg)',
          boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
        }}
      >
        <Skeleton variant='text' width='60%' height={20} sx={{ mb: 2 }} />
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 2, mb: 2 }}>
          <Box>
            <Skeleton variant='text' width='80%' height={16} />
            <Skeleton variant='text' width='50%' height={28} />
          </Box>
          <Box>
            <Skeleton variant='text' width='70%' height={16} />
            <Skeleton variant='text' width='60%' height={28} />
          </Box>
          <Box>
            <Skeleton variant='text' width='85%' height={16} />
            <Skeleton variant='text' width='55%' height={28} />
          </Box>
        </Box>
      </Box>

      {/* Utilization & Health Skeleton */}
      <Box
        sx={{
          backgroundColor: 'rgba(255, 255, 255, 1)',
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          borderRadius: 'var(--ds-radius-lg)',
          boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
        }}
      >
        <Skeleton variant='text' width='40%' height={20} sx={{ mb: 2 }} />
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2, mb: 3 }}>
          <Box>
            <Skeleton variant='text' width='60%' height={16} />
            <Skeleton variant='text' width='80%' height={24} />
          </Box>
          <Box>
            <Skeleton variant='text' width='70%' height={16} />
            <Skeleton variant='text' width='75%' height={24} />
          </Box>
        </Box>
        <Skeleton variant='text' width='50%' height={20} sx={{ mb: 1 }} />
        <Skeleton variant='rectangular' height={120} />
      </Box>

      {/* Cost Summary Skeleton */}
      <Box
        sx={{
          backgroundColor: 'rgba(255, 255, 255, 1)',
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          borderRadius: 'var(--ds-radius-lg)',
          boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
        }}
      >
        <Skeleton variant='text' width='70%' height={20} sx={{ mb: 2 }} />
        <Box sx={{ mb: 2 }}>
          <Skeleton variant='text' width='50%' height={16} />
          <Skeleton variant='text' width='80%' height={24} />
          <Skeleton variant='text' width='60%' height={14} />
        </Box>
        <Box sx={{ mb: 2 }}>
          <Skeleton variant='text' width='60%' height={16} />
          <Skeleton variant='text' width='70%' height={24} />
          <Skeleton variant='text' width='55%' height={14} />
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
          <Skeleton variant='circular' width={60} height={60} />
          <Box>
            <Skeleton variant='text' width='40%' height={16} />
            <Skeleton variant='text' width='60%' height={20} />
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default SummarySkeletonLoader;
