import { Box } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';

const CustomBorderCard = ({
  children,
  borderColor = '#BFDBFE',
  onClick,
  padding = '16px 25px 16px 16px',
  borderLeftColor,
  borderLeftWidth,
  showLeftBorder = true,
  sx = {},
}) => {
  return (
    <Box
      sx={{
        padding: padding,
        bgcolor: 'white',
        borderRadius: '6px',
        ...sx,
        cursor: onClick ? 'pointer' : 'default',
        borderBottom: `1px solid ${borderColor || 'transparent !important'}`,
        borderLeftColor: borderLeftColor,
        borderLeftWidth: borderLeftWidth,
        borderLeftStyle: showLeftBorder ? 'solid' : 'none',
      }}
      onClick={onClick}
    >
      {children}
    </Box>
  );
};

export default CustomBorderCard;

CustomBorderCard.propTypes = {
  sx: PropTypes.object,
  children: PropTypes.any,
  borderColor: PropTypes.any,
  borderLeftColor: PropTypes.any,
  onClick: PropTypes.any,
  showLeftBorder: PropTypes.bool,
  padding: PropTypes.any,
  borderLeftWidth: PropTypes.any,
  showBoxShadow: PropTypes.bool,
};
