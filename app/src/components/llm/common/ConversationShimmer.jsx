import React from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';

const ConversationShimmer = () => {
  const shimmerStyle = {
    background: `linear-gradient(90deg, var(--ds-gray-100) 25%, var(--ds-background-200) 50%, var(--ds-gray-100) 75%)`,
    backgroundSize: '200% 100%',
    animation: 'shimmer 1.5s infinite',
  };

  return (
    <Box sx={{ px: 'var(--ds-space-6)', py: ds.space.mul(4, 5) }}>
      <style>
        {`
          @keyframes shimmer {
            0% { background-position: 200% 0; }
            100% { background-position: -200% 0; }
          }
        `}
      </style>
      {/* Question shimmer */}
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', mb: ds.space.mul(2, 5) }}>
        <Box
          sx={{
            height: ds.space.mul(1, 7),
            width: ds.space.mul(1, 7),
            borderRadius: '50%',
            mt: 'var(--ds-space-1)',
            ...shimmerStyle,
          }}
        />
        <Box sx={{ width: '100%' }}>
          <Box
            sx={{
              height: 'var(--ds-space-4)',
              width: '75%',
              borderRadius: 'var(--ds-radius-sm)',
              mb: 'var(--ds-space-2)',
              ...shimmerStyle,
            }}
          />
          <Box
            sx={{
              height: ds.space.mul(0, 7),
              width: '45%',
              borderRadius: 'var(--ds-radius-sm)',
              ...shimmerStyle,
            }}
          />
        </Box>
      </Box>
      {/* Task shimmer cards */}
      {[1, 2, 3, 4].map((index) => (
        <Box key={index} sx={{ display: 'flex', gap: 'var(--ds-space-3)', mb: 'var(--ds-space-5)' }}>
          <Box
            sx={{
              height: 'var(--ds-space-1)',
              width: 'var(--ds-space-1)',
              borderRadius: '50%',
              mt: 'var(--ds-space-3)',
              ...shimmerStyle,
            }}
          />
          <Box sx={{ width: '100%' }}>
            <Box
              sx={{
                height: 'var(--ds-space-3)',
                width: '60%',
                borderRadius: 'var(--ds-radius-sm)',
                mb: 'var(--ds-space-2)',
                ...shimmerStyle,
              }}
            />
            <Box
              sx={{
                height: ds.space.mul(0, 5),
                width: '30%',
                borderRadius: 'var(--ds-radius-sm)',
                mb: 'var(--ds-space-3)',
                ...shimmerStyle,
              }}
            />
            <Box
              sx={{
                height: ds.space.mul(4, 5),
                width: '100%',
                borderRadius: 'var(--ds-radius-lg)',
                ...shimmerStyle,
              }}
            />
          </Box>
        </Box>
      ))}
    </Box>
  );
};

export default ConversationShimmer;
