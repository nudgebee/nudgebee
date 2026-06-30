import { Typography, Box } from '@mui/material';
import { formatNumber } from '@lib/formatter';
import React from 'react';
import PropTypes from 'prop-types';
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown';
import ArrowDropUpIcon from '@mui/icons-material/ArrowDropUp';

const TrendArrowPercentage = ({ value, sign = 1, width = '50px', size = 'default' }) => {
  const compact = size === 'sm';
  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        width: compact ? 'auto' : width,
        marginRight: compact ? '0px' : '8px',
      }}
    >
      {value * sign > 0 ? (
        <ArrowDropDownIcon sx={{ color: 'green', fontSize: compact ? 14 : undefined }} />
      ) : (
        <ArrowDropUpIcon sx={{ color: 'red', fontSize: compact ? 14 : undefined }} />
      )}
      <Typography
        sx={{
          color: value * sign > 0 ? 'green' : '#EF4444',
          fontSize: compact ? '10px' : '12px',
          fontWeight: compact ? 400 : 500,
          opacity: compact ? 0.75 : 1,
        }}
      >
        {formatNumber(value)}%
      </Typography>
    </Box>
  );
};

TrendArrowPercentage.propTypes = {
  value: PropTypes.number,
  sign: PropTypes.number,
  width: PropTypes.string,
  size: PropTypes.string,
};

export default TrendArrowPercentage;
