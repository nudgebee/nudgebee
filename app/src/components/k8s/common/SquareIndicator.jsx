import { Box } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';

const SquareIndicator = ({ color }) => {
  return <Box sx={{ backgroundColor: color, height: '8px', width: '8px', borderRadius: '2px' }} />;
};

export default SquareIndicator;

SquareIndicator.propTypes = {
  color: PropTypes.any,
};
