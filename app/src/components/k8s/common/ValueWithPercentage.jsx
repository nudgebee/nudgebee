import { Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';

const ValueWithPercentage = ({ capacity = '', value = 0, noPercentage, makeValueRed = false, showParentheses = false }) => {
  return (
    <Typography
      sx={{
        fontWeight: 'var(--ds-font-weight-medium)',
        fontSize: 'var(--ds-text-caption)',
        lineHeight: '20px',
        color: 'var(--ds-gray-400)',
        '& .right-unit': {
          color: 'var(--ds-gray-400)',
        },
      }}
    >
      {capacity && (
        <span
          style={{
            fontWeight: 'var(--ds-font-weight-regular)',
            fontSize: 'var(--ds-text-small)',
            color: makeValueRed ? ds.red[400] : ds.gray[700],
            marginRight: 'var(--ds-space-1)',
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
