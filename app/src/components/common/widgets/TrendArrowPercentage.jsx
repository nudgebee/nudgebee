import { Typography, Box } from '@mui/material';
import { formatNumber } from '@lib/formatter';
import React from 'react';
import PropTypes from 'prop-types';
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown';
import ArrowDropUpIcon from '@mui/icons-material/ArrowDropUp';
import { ds } from 'src/utils/colors';

const TrendArrowPercentage = ({ value, sign = 1, width = ds.space.mul(0, 25), size = 'default' }) => {
  const compact = size === 'sm';
  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        width: compact ? 'auto' : width,
        marginRight: compact ? '0px' : ds.space[2],
      }}
    >
      {value * sign > 0 ? (
        <ArrowDropDownIcon sx={{ color: ds.green[500], fontSize: compact ? 14 : undefined }} />
      ) : (
        <ArrowDropUpIcon sx={{ color: ds.red[500], fontSize: compact ? 14 : undefined }} />
      )}
      <Typography
        sx={{
          color: value * sign > 0 ? ds.green[500] : ds.red[500],
          fontSize: compact ? '10px' : ds.text.small,
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
