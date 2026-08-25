import React from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';

const ConversationShimmer = ({ popup = false }) => {
  const shimmerStyle = {
    background: `linear-gradient(90deg, var(--ds-gray-100) 25%, var(--ds-background-200) 50%, var(--ds-gray-100) 75%)`,
    backgroundSize: '200% 100%',
    animation: 'shimmer 1.5s infinite',
  };

  return (
    <Box
      sx={{
        px: popup ? 'var(--ds-space-1)' : 'var(--ds-space-6)',
        py: popup ? 'var(--ds-space-3)' : ds.space.mul(4, 5),
        width: '100%',
        minWidth: 0,
        boxSizing: 'border-box',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      <style>
        {`
          @keyframes shimmer {
            0% { background-position: 200% 0; }
            100% { background-position: -200% 0; }
          }
        `}
      </style>
      {/* Question shimmer */}
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', mb: popup ? 'var(--ds-space-3)' : ds.space.mul(2, 5), width: '100%', minWidth: 0 }}>
        <Box
          sx={{
            height: ds.space.mul(1, 5),
            width: ds.space.mul(1, 5),
            borderRadius: '50%',
            mt: 'var(--ds-space-1)',
            flexShrink: 0,
            ...shimmerStyle,
          }}
        />
        <Box sx={{ width: '100%' }}>
          <Box
            sx={{
              height: 'var(--ds-space-3)',
              width: popup ? '70%' : '75%',
              borderRadius: 'var(--ds-radius-sm)',
              mb: 'var(--ds-space-2)',
              ...shimmerStyle,
            }}
          />
          <Box
            sx={{
              height: ds.space.mul(0, 5),
              width: '40%',
              borderRadius: 'var(--ds-radius-sm)',
              ...shimmerStyle,
            }}
          />
        </Box>
      </Box>
      {/* Task shimmer cards */}
      {[1, 2].map((index) => (
        <Box
          key={index}
          sx={{ display: 'flex', gap: 'var(--ds-space-3)', mb: popup ? 'var(--ds-space-4)' : 'var(--ds-space-5)', width: '100%', minWidth: 0 }}
        >
          <Box
            sx={{
              height: 'var(--ds-space-1)',
              width: 'var(--ds-space-1)',
              borderRadius: '50%',
              mt: 'var(--ds-space-3)',
              flexShrink: 0,
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
                height: popup ? ds.space.mul(2, 5) : ds.space.mul(4, 5),
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
