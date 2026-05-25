import React from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { formatValueWithUnit } from 'src/utils/common';

const ClusterCustomTooltip = ({ showTooltip = false, usage = 0, available = 0, limit = 0, request = 0, title = '' }) => {
  const formattedUsage = usage > 0 ? formatValueWithUnit(usage, title) : null;
  const formattedLimit = limit > 0 ? formatValueWithUnit(limit, title) : null;
  const formattedRequest = request > 0 ? formatValueWithUnit(request, title) : null;

  const calculatePercentage = (value, total) => {
    if (typeof value !== 'number' || typeof total !== 'number' || total <= 0 || value <= 0) {
      return '-';
    }
    return `(${((value / total) * 100).toFixed(0)}%)`;
  };

  const renderRow = (label, formatted, rawValue) => (
    <Box sx={{ display: 'flex', p: '4px', justifyContent: 'space-between' }}>
      <Box display='flex' alignItems='center' gap={1}>
        <Typography
          sx={{
            color: '#737373',
            fontSize: '11px',
            fontWeight: 500,
            alignItems: 'end',
            minWidth: '56px',
          }}
        >
          {label}
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', minWidth: '90px', justifyContent: 'space-between' }}>
        <Box sx={{ marginRight: '8px' }}>
          <Typography sx={{ color: '#B9B9B9', fontSize: '11px', fontWeight: 500 }}>{calculatePercentage(rawValue, available)}</Typography>
        </Box>
        <Box sx={{ position: 'relative', display: 'flex', gap: '4px' }}>
          <Typography sx={{ color: '#374151', fontSize: '11px', fontWeight: 500 }}>{formatted?.value ? formatted.value.toFixed(2) : '-'}</Typography>
          <Typography sx={{ color: '#B9B9B9', fontSize: '11px', fontWeight: 500 }}>{formatted?.unit ?? ''}</Typography>
        </Box>
      </Box>
    </Box>
  );

  return (
    <Box
      sx={{
        display: showTooltip ? 'block' : 'none',
        position: 'absolute',
        backgroundColor: '#fff',
        border: '0.5px solid #60A5FA',
        boxShadow: '0px 4px 10px 0px #89899340',
        borderRadius: '4px',
        width: '190px',
        p: '4px 6px',
        zIndex: 2,
        left: '165px',
        top: '65px',
      }}
    >
      {renderRow('Usage:', formattedUsage, usage)}
      {renderRow('Limit:', formattedLimit, limit)}
      {renderRow('Request:', formattedRequest, request)}
    </Box>
  );
};

ClusterCustomTooltip.propTypes = {
  showTooltip: PropTypes.bool,
  usage: PropTypes.number,
  available: PropTypes.number,
  limit: PropTypes.number,
  request: PropTypes.number,
  title: PropTypes.string,
};

export default ClusterCustomTooltip;
