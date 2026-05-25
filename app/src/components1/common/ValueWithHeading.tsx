import { Box, Typography } from '@mui/material';
import React from 'react';
type ValueWithHeadingProps = {
  iconColor?: string;
  heading: string;
  value?: string | number;
  isRightAlign?: boolean;
  forCostSummary?: boolean;
  forWorkload?: boolean;
  hideLogo?: boolean;
};

const ValueWithHeading = ({
  iconColor,
  heading = '',
  value = '',
  isRightAlign,
  forCostSummary,
  forWorkload,
  hideLogo = false,
}: ValueWithHeadingProps) => {
  return (
    <Box display='flex' sx={{ justifyContent: 'space-between', gap: '20px' }}>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'row',
          alignItems: isRightAlign ? 'flex-end' : 'flex-start',
        }}
      >
        {!!iconColor && (
          <Box
            component='span'
            sx={{
              width: '8px',
              height: '8px',
              borderRadius: '2px',
              backgroundColor: iconColor,
              mr: forWorkload ? '6px' : forCostSummary ? '9px' : '14px',
              mt: forWorkload ? '3px' : forCostSummary ? '5px' : '6px',
            }}
          />
        )}
        <Typography
          sx={{
            fontSize: forWorkload ? '10px' : forCostSummary ? '12px' : '14px',
            lineHeight: 1.3,
            fontWeight: forWorkload || forCostSummary ? 400 : 600,
            ...(forWorkload || forCostSummary ? { color: '#737373' } : {}),
            ...(iconColor ? {} : { color: '#808896' }),
          }}
        >
          {heading}
        </Typography>
      </Box>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'row',
          gap: '30px',
          alignItems: isRightAlign ? 'flex-end' : 'flex-start',
        }}
      >
        {value && (
          <Typography sx={{ fontWeight: 600, fontSize: forWorkload ? '12px' : forCostSummary ? '16px' : '18px', lineHeight: 1 }}>
            {hideLogo ? '' : '$'}
            {value?.toLocaleString()}
          </Typography>
        )}
      </Box>
    </Box>
  );
};

export default ValueWithHeading;
