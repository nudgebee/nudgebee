import { Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';

const ValueWithPercentage = ({ capacity = '', value = 0, noPercentage, makeValueRed = false, showParentheses = false }) => {
  return (
    <Typography
      sx={{
        fontWeight: 500,
        fontSize: '11px',
        lineHeight: '20px',
        color: '#9F9F9F',
        '& .right-unit': {
          color: '#9F9F9F',
        },
      }}
    >
      {capacity && (
        <span
          style={{
            fontWeight: 400,
            fontSize: '12px',
            color: makeValueRed ? '#F87171' : '#374151',
            marginRight: '4px',
          }}
        >
          {capacity}
        </span>
      )}
      {showParentheses ? `(${value}%)` : value}
      {!noPercentage && !showParentheses && '%'}
    </Typography>
  );
};
ValueWithPercentage.propTypes = {
  capacity: PropTypes.string,
  value: PropTypes.number,
  noPercentage: PropTypes.bool,
  makeValueRed: PropTypes.bool,
  showParentheses: PropTypes.bool,
};

ValueWithPercentage.propTypes = {
  value: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  noPercentage: PropTypes.bool,
};

export default ValueWithPercentage;
