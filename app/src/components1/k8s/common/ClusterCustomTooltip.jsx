import React from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { formatValueWithUnit } from 'src/utils/common';
import { ds } from '@utils/colors';

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
    <Box sx={{ display: 'flex', p: 'var(--ds-space-1)', justifyContent: 'space-between' }}>
      <Box display='flex' alignItems='center' gap={ds.space[2]}>
        <Typography
          sx={{
            color: 'var(--ds-gray-600)',
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            alignItems: 'end',
            minWidth: ds.space.mul(0, 28),
          }}
        >
          {label}
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', minWidth: ds.space.mul(0, 45), justifyContent: 'space-between' }}>
        <Box sx={{ marginRight: 'var(--ds-space-2)' }}>
          <Typography sx={{ color: 'var(--ds-brand-300)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)' }}>
            {calculatePercentage(rawValue, available)}
          </Typography>
        </Box>
        <Box sx={{ position: 'relative', display: 'flex', gap: 'var(--ds-space-1)' }}>
          <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)' }}>
            {formatted?.value ? formatted.value.toFixed(2) : '-'}
          </Typography>
          <Typography sx={{ color: 'var(--ds-brand-300)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)' }}>
            {formatted?.unit ?? ''}
          </Typography>
        </Box>
      </Box>
    </Box>
  );

  return (
    <Box
      sx={{
        display: showTooltip ? 'block' : 'none',
        position: 'absolute',
        backgroundColor: 'var(--ds-background-100)',
        border: '0.5px solid var(--ds-blue-400)',
        boxShadow: '0px 4px 10px 0px #89899340',
        borderRadius: 'var(--ds-radius-sm)',
        width: ds.space.mul(0, 95),
        p: 'var(--ds-space-1) var(--ds-space-1)',
        zIndex: 2,
        left: ds.space.mul(0, 82),
        top: ds.space.mul(0, 32),
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
