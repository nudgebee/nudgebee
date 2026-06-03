import React from 'react';
import { Box, Typography } from '@mui/material';
import { Label } from '@components1/ds/Label';
import PropTypes from 'prop-types';

const SeverityInfographics = ({ severityData, customStyle }) => {
  const getSeverityColor = (value) => {
    switch (value) {
      case 'critical':
        return 'criticalRed';
      case 'high':
        return 'red';
      case 'medium':
        return 'yellow';
      case 'low':
        return 'blue';
    }
  };
  const displayedItems = (severityData || []).filter((data) => data?.value && data?.value !== '-');
  if (displayedItems.length === 0) {
    return null;
  }
  return (
    <Box
      sx={{
        display: 'flex',
        background: 'var(--ds-background-100)',
        width: 'min-content',
        border: '0.5px solid var(--ds-blue-300)',
        padding: 'var(--ds-space-2) var(--ds-space-4)',
        borderRadius: 'var(--ds-radius-sm)',
        boxShadow: '0px 2px 7px 0px var(--ds-blue-200)',
        ...customStyle,
      }}
    >
      {displayedItems.map((data, index) => (
        <Box
          display='flex'
          alignItems='center'
          key={data?.label}
          sx={{
            ...(displayedItems.length > 1 &&
              index !== displayedItems.length - 1 && {
                '&::after': {
                  content: '" "',
                  height: '16px',
                  border: '0.5px solid var(--ds-gray-300)',
                  marginLeft: 'var(--ds-space-2)',
                  marginRight: 'var(--ds-space-2)',
                },
              }),
          }}
        >
          <Box sx={{ ml: 'var(--ds-space-3)', mr: 'var(--ds-space-2)' }}>
            <Label height='14px' text={data?.value.toString()} variant={getSeverityColor(data?.label?.toLowerCase())} />
          </Box>
          <Typography
            className='label'
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: 'var(--ds-blue-600)',
              mr: 'var(--ds-space-2)',
            }}
          >
            {data?.label}
          </Typography>
        </Box>
      ))}
    </Box>
  );
};

SeverityInfographics.propTypes = {
  severityData: PropTypes.array,
};

export default SeverityInfographics;
