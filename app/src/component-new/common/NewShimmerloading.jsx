/**
 * NewShimmerloading — domain composition for "Nudgebee LLM is thinking" state.
 * Uses the brand ripple/pulse icon (kept as-is — it's brand identity, not a
 * generic loader) plus a `ds/Skeleton` wave for the response placeholder.
 *
 * NOT a generic skeleton — for placeholder use cases prefer
 * `Skeleton` / `Skeleton.Card` from `@components1/ds/Skeleton` directly.
 *
 * Previously deprecated 2026-05-07 → demoted to domain composition 2026-05-07
 * after V2 review: the ripple icon has no DS-primitive equivalent and the
 * single LLM-response call site relies on the branded gesture.
 */
import { Box } from '@mui/material';
import { Skeleton } from '@components1/ds/Skeleton';
import { colors } from 'src/utils/colors';

const NewShimmerloading = () => {
  return (
    <Box
      sx={{
        borderRadius: '4px',
        width: '100%',
        display: 'grid',
        gridTemplateColumns: '35px 1fr',
        alignItems: 'center',
        gap: '12px',
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
          borderRadius: '4px',
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
      <Skeleton shape='rect' height={60} width='100%' />
    </Box>
  );
};

export default NewShimmerloading;
