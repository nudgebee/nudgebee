import { Box, Skeleton } from '@mui/material';
import { colors } from 'src/utils/colors';

const ShimmerLoading = () => {
  return (
    <Box
      sx={{
        borderRadius: 'var(--ds-radius-sm)',
        width: '100%',
        display: 'grid',
        gridTemplateColumns: '35px 1fr',
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
      }}
    >
      <Box
        sx={{
          height: '36px',
          width: '36px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          backgroundColor: colors.background.tertiaryLightest,
          borderRadius: 'var(--ds-radius-sm)',
          position: 'relative',
          '&::before': {
            content: '""',
            position: 'absolute',
            width: '24px',
            height: '24px',
            borderRadius: '50%',
            border: `2px solid ${colors.text.yellowLabel}`,
            animation: 'ripple 2s ease-in-out infinite',
          },
          '&::after': {
            content: '""',
            position: 'absolute',
            width: '16px',
            height: '16px',
            borderRadius: '50%',
            backgroundColor: colors.nudgebeeMain,
            animation: 'pulse 2s ease-in-out infinite',
          },
          '@keyframes ripple': {
            '0%': {
              transform: 'scale(0.8)',
              opacity: 0.8,
            },
            '50%': {
              transform: 'scale(1)',
              opacity: 0.3,
            },
            '100%': {
              transform: 'scale(0.8)',
              opacity: 0.8,
            },
          },
          '@keyframes pulse': {
            '0%': {
              transform: 'scale(1)',
              opacity: 0.8,
            },
            '50%': {
              transform: 'scale(0.8)',
              opacity: 1,
            },
            '100%': {
              transform: 'scale(1)',
              opacity: 0.8,
            },
          },
        }}
      />
      <Skeleton animation='wave' height={'60px'} sx={{ bgcolor: colors.background.tertiaryLightest }} />
    </Box>
  );
};

export default ShimmerLoading;
