import { Box } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';

const CustomBorderCard = ({
  children,
  borderColor = 'var(--ds-blue-300)',
  onClick,
  padding = 'var(--ds-space-4) 25px var(--ds-space-4) var(--ds-space-4)',
  borderLeftColor,
  borderLeftWidth,
  showLeftBorder = true,
  sx = {},
}) => {
  return (
    <Box
      sx={{
        padding: padding,
        bgcolor: 'var(--ds-background-100)',
        borderRadius: 'var(--ds-radius-md)',
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
