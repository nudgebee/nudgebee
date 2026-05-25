import React from 'react';
import { Box, Divider, Typography } from '@mui/material';
import PropTypes from 'prop-types';

const AutoPilotInfoCard = ({ shadow, cardTag, title, button, children, _data = {}, border, height }) => {
  return (
    <Box sx={{ minHeight: '180px' }}>
      {cardTag && (
        <Box sx={{ marginBottom: '10px', marginLeft: '2px' }}>
          <Typography sx={{ color: '#374151', fontSize: '14px', fontWeight: 600 }}>{cardTag}</Typography>
          <Divider sx={{ background: '#60A5FA', padding: '1px', width: '28px' }} />
        </Box>
      )}
      <Box
        sx={{
          height: height ?? '190px',
          borderRadius: '8px',
          border: border || '1px solid #EBEBEB',
          display: 'flex',
          minWidth: '370px',
          padding: '16px 16px 9px 16px',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: '10px',
          boxShadow: shadow,
        }}
      >
        <Box sx={{ width: '100%', alignItem: 'cneter', display: 'flex', justifyContent: 'space-between' }}>
          <Box>
            <Typography sx={{ color: '374151', fontSize: '12px', fontWeight: 600 }}>{title}</Typography>
          </Box>
          {button && <Box>{button}</Box>}
        </Box>
        {children}
      </Box>
    </Box>
  );
};

export default AutoPilotInfoCard;

AutoPilotInfoCard.propTypes = {
  shadow: PropTypes.any,
  cardTag: PropTypes.any,
  title: PropTypes.any,
  button: PropTypes.any,
  children: PropTypes.any,
  data: PropTypes.any,
  border: PropTypes.any,
  height: PropTypes.any,
};
