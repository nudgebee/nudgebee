import { Box, Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import SafeIcon from '@components1/common/SafeIcon';
import { ds } from 'src/utils/colors';

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
      return ds.text.caption;
    }
    if (forCostSummary) {
      return ds.text.small;
    }
    return ds.text.bodyLg;
  };
  const getCostSummaryFontSize = () => {
    if (forWorkload) {
      return ds.text.small;
    }
    if (forCostSummary) {
      return ds.text.title;
    }
    return ds.text.heading;
  };
  return (
    <Box display='flex' alignItems={updatedNode && 'center'}>
      {!!iconColor && <SafeIcon src={icon} alt='node icon' />}
      {updatedNode && (
        <Box display={'flex'} alignItems={'center'} gap={ds.space[3]} width={'max-content'}>
          <Typography
            sx={{
              fontSize: getFontSize(),
              lineHeight: 1.2,
              ml: 'var(--ds-space-1)',
              fontWeight: forWorkload || forCostSummary ? 400 : 600,
              ...(forWorkload || forCostSummary ? { color: 'var(--ds-gray-600)' } : {}),
              ...(iconColor ? {} : { color: 'var(--ds-gray-500)' }),
            }}
          >
            {heading}
          </Typography>
          <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: getCostSummaryFontSize() }}>
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
              fontSize: forWorkload ? ds.text.caption : forCostSummary ? ds.text.small : ds.text.bodyLg,
              lineHeight: 1.2,
              fontWeight: forWorkload || forCostSummary ? 400 : 600,
              ...(forWorkload || forCostSummary ? { color: 'var(--ds-gray-600)' } : {}),
              ...(iconColor ? {} : { color: 'var(--ds-gray-500)' }),
            }}
          >
            {heading}
          </Typography>
          <Typography
            sx={{
              fontWeight: 'var(--ds-font-weight-semibold)',
              fontSize: forWorkload ? ds.text.small : forCostSummary ? ds.text.title : ds.text.heading,
            }}
          >
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
