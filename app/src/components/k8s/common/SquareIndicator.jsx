import { Box } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';

const SquareIndicator = ({ color }) => {
  return (
    <Box sx={{ backgroundColor: color, height: 'var(--ds-space-2)', width: 'var(--ds-space-2)', borderRadius: 'calc(var(--ds-radius-sm) / 2)' }} />
  );
};

export default SquareIndicator;

SquareIndicator.propTypes = {
  color: PropTypes.any,
};
