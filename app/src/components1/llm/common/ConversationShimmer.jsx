import React from 'react';
import { Box } from '@mui/material';
import { colors } from 'src/utils/colors';

const ConversationShimmer = () => {
  const shimmerStyle = {
    background: `linear-gradient(90deg, ${colors.background.shimmerBase} 25%, ${colors.background.shimmerHighlight} 50%, ${colors.background.shimmerBase} 75%)`,
    backgroundSize: '200% 100%',
    animation: 'shimmer 1.5s infinite',
    '@keyframes shimmer': {
      '0%': { backgroundPosition: '200% 0' },
      '100%': { backgroundPosition: '-200% 0' },
    },
  };

  return (
    <Box sx={{ px: '35px', py: '80px' }}>
      <style>
        {`
          @keyframes shimmer {
            0% { background-position: 200% 0; }
            100% { background-position: -200% 0; }
          }
        `}
      </style>
      {/* Question shimmer */}
      <Box sx={{ display: 'flex', gap: '12px', mb: '40px' }}>
        <Box
          sx={{
            height: '28px',
            width: '28px',
            borderRadius: '50%',
            mt: '5px',
            ...shimmerStyle,
          }}
        />
        <Box sx={{ width: '100%' }}>
          <Box
            sx={{
              height: '16px',
              width: '75%',
              borderRadius: '4px',
              mb: '8px',
              ...shimmerStyle,
            }}
          />
          <Box
            sx={{
              height: '14px',
              width: '45%',
              borderRadius: '4px',
              ...shimmerStyle,
            }}
          />
        </Box>
      </Box>
      {/* Task shimmer cards */}
      {[1, 2, 3, 4].map((index) => (
        <Box key={index} sx={{ display: 'flex', gap: '12px', mb: '24px' }}>
          <Box
            sx={{
              height: '4px',
              width: '4px',
              borderRadius: '50%',
              mt: '12px',
              ...shimmerStyle,
            }}
          />
          <Box sx={{ width: '100%' }}>
            <Box
              sx={{
                height: '12px',
                width: '60%',
                borderRadius: '4px',
                mb: '8px',
                ...shimmerStyle,
              }}
            />
            <Box
              sx={{
                height: '10px',
                width: '30%',
                borderRadius: '4px',
                mb: '12px',
                ...shimmerStyle,
              }}
            />
            <Box
              sx={{
                height: '80px',
                width: '100%',
                borderRadius: '8px',
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
