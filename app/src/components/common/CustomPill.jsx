import { Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import CustomTooltip from './CustomTooltip';

const CustomPill = ({
  bgColor = colors.background.primaryLightest,
  borderRadius = '4px',
  padding = '2px 4px',
  font = '12px',
  fontWeight = 400,
  showBorder = false,
  color = colors.text.primaryLight,
  value,
  sx = {},
  tooltip = '',
}) => {
  return (
    <CustomTooltip placement='top' title={tooltip}>
      <Typography
        sx={{
          backgroundColor: showBorder ? 'transparent' : bgColor,
          borderRadius: borderRadius,
          padding: padding,
          fontSize: font,
          fontWeight: fontWeight,
          color: showBorder ? colors.text.tertiary : color,
          border: showBorder && `0.5px solid ${colors.border.secondary}`,
          height: '16px',
          ...sx,
        }}
      >
        {value > 99 ? '99+' : value}
      </Typography>
    </CustomTooltip>
  );
};

CustomPill.propTypes = {
  bgColor: PropTypes.string,
  borderRadius: PropTypes.string,
  padding: PropTypes.string,
  font: PropTypes.string,
  fontWeight: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  showBorder: PropTypes.bool,
  color: PropTypes.string,
  value: PropTypes.node.isRequired,
  sx: PropTypes.object,
  tooltip: PropTypes.string,
};

export default CustomPill;
