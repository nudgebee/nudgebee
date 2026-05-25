import React from 'react';
import { Box, Typography } from '@mui/material';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { colors } from 'src/utils/colors';
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
  const displayedItems = severityData.filter((data) => data?.value && data?.value !== '-');
  return (
    <Box
      sx={{
        display: 'flex',
        background: colors.background.white,
        width: 'min-content',
        border: `0.5px solid ${colors.toDo}`,
        padding: ' 6px 18px',
        borderRadius: '4px',
        boxShadow: '0px 2px 7px 0px #EFF6FF',
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
                  border: `0.5px solid ${colors.border.secondary}`,
                  marginLeft: '8px',
                  marginRight: '8px',
                },
              }),
          }}
        >
          <Box sx={{ ml: '12px', mr: '8px' }}>
            <CustomLabels height='14px' text={data?.value.toString()} variant={getSeverityColor(data?.label?.toLowerCase())} />
          </Box>
          <Typography
            className='label'
            sx={{
              fontSize: '12px',
              fontWeight: 400,
              color: colors.border.quadrant,
              mr: '10px',
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
