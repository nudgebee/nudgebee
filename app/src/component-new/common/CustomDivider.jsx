import { Box } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';

const CustomDivider = ({ margin, borderWidth = '0.5px', borderType = 'solid', maxWidth, borderColor = 'var(--ds-gray-200)' }) => {
  return <Box sx={{ border: `${borderWidth} ${borderType} ${borderColor}`, m: margin || '10px 0px', maxWidth: maxWidth || 'auto' }} />;
};
CustomDivider.propTypes = {
  margin: PropTypes.string,
  maxWidth: PropTypes.string,
  borderColor: PropTypes.string,
  borderType: PropTypes.string,
  borderWidth: PropTypes.string,
};
export default CustomDivider;
