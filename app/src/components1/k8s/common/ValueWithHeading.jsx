import { Box, Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import SafeIcon from '@components1/common/SafeIcon';

const ValueWithHeading = ({
  iconColor,
  heading = '',
  value = 0,
  isRightAlign,
  forCostSummary,
  forWorkload,
  hideLogo = false,
  _clusterSummary,
  updatedNode = false,
  _marginRight = '',
  _marginTop = '',
  icon,
}) => {
  const getFontSize = () => {
    if (forWorkload) {
      return '10px';
    }
    if (forCostSummary) {
      return '12px';
    }
    return '14px';
  };
  const getCostSummaryFontSize = () => {
    if (forWorkload) {
      return '12px';
    }
    if (forCostSummary) {
      return '16px';
    }
    return '18px';
  };
  return (
    <Box display='flex' alignItems={updatedNode && 'center'}>
      {!!iconColor && <SafeIcon src={icon} alt='node icon' />}
      {updatedNode && (
        <Box display={'flex'} alignItems={'center'} gap={'12px'} width={'max-content'}>
          <Typography
            sx={{
              fontSize: getFontSize(),
              lineHeight: 1.2,
              ml: '5px',
              fontWeight: forWorkload || forCostSummary ? 400 : 600,
              ...(forWorkload || forCostSummary ? { color: '#737373' } : {}),
              ...(iconColor ? {} : { color: '#808896' }),
            }}
          >
            {heading}
          </Typography>
          <Typography sx={{ fontWeight: 600, fontSize: getCostSummaryFontSize() }}>
            {hideLogo ? '' : '$'}
            {value?.toLocaleString()}
          </Typography>
        </Box>
      )}

      {!updatedNode && (
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: isRightAlign ? 'flex-end' : 'flex-start',
          }}
        >
          <Typography
            sx={{
              fontSize: forWorkload ? '10px' : forCostSummary ? '12px' : '14px',
              lineHeight: 1.2,
              fontWeight: forWorkload || forCostSummary ? 400 : 600,
              ...(forWorkload || forCostSummary ? { color: '#737373' } : {}),
              ...(iconColor ? {} : { color: '#808896' }),
            }}
          >
            {heading}
          </Typography>
          <Typography sx={{ fontWeight: 600, fontSize: forWorkload ? '12px' : forCostSummary ? '16px' : '18px' }}>
            {hideLogo ? '' : '$'}
            {value?.toLocaleString()}
          </Typography>
        </Box>
      )}
    </Box>
  );
};

export default ValueWithHeading;

ValueWithHeading.propTypes = {
  iconColor: PropTypes.any,
  heading: PropTypes.any,
  value: PropTypes.any,
  isRightAlign: PropTypes.any,
  forCostSummary: PropTypes.any,
  forWorkload: PropTypes.any,
  hideLogo: PropTypes.bool,
  clusterSummary: PropTypes.any,
  updatedNode: PropTypes.bool,
  marginRight: PropTypes.string,
  marginTop: PropTypes.string,
  icon: PropTypes.any,
};
