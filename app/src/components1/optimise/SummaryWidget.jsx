import React from 'react';
import { Box, Typography, IconButton } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { formatNumber } from '@lib/formatter';
import CustomTooltip from 'src/components1/common/CustomTooltip';

const InfoIcon = ({ tooltipContent, tooltipPosition }) => {
  const iconButton = (
    <IconButton
      size='small'
      sx={{
        padding: '4px',
        color: colors.text.secondaryDark,
        '&:hover': {
          backgroundColor: 'rgba(0, 0, 0, 0.04)',
        },
      }}
    >
      <InfoOutlinedIcon sx={{ fontSize: '14px' }} />
    </IconButton>
  );

  if (tooltipContent) {
    return (
      <CustomTooltip title={tooltipContent} placement={tooltipPosition}>
        {iconButton}
      </CustomTooltip>
    );
  }
  return iconButton;
};

InfoIcon.propTypes = {
  tooltipContent: PropTypes.oneOfType([PropTypes.string, PropTypes.node]),
  tooltipPosition: PropTypes.oneOf(['top', 'bottom', 'left', 'right']),
};

const SummaryWidget = ({
  title,
  value,
  variant = 'default',
  size = 'default',
  maxWidth = '100%',
  showInfoIcon = false,
  tooltipContent,
  sx = {},
  tooltipPosition = 'top',
  onClick,
  suffix,
  headerRight,
}) => {
  const isSmall = size === 'small';
  const isSavings = variant === 'savings';

  const sizeStyles = isSmall
    ? { border: '1.5px solid', padding: '6px 16px', borderRadius: '10px', gap: '2px', minHeight: '56px', mediaPadding: '6px 12px !important' }
    : { border: '2px solid', padding: '8px 20px', borderRadius: '12px', gap: '8px', minHeight: '80px', mediaPadding: '16px !important' };

  const titleFontStyles = isSmall ? { fontSize: '11px', lineHeight: '14px' } : { fontSize: '14px', lineHeight: '16px' };

  const valueFontStyles = isSmall ? { fontSize: '20px', lineHeight: '22px' } : { fontSize: '28px', lineHeight: '28px' };

  return (
    <Box
      onClick={onClick}
      sx={{
        border: sizeStyles.border,
        borderColor: isSavings ? '#BBF7D0' : '#d7c9ff',
        backgroundColor: colors.background.white,
        boxShadow: isSavings ? '0px 2px 10px 0px #BBF7D0' : '0px 4px 20px -1px rgba(229, 229, 229, 0.15), 0px 2px 10px 0px rgba(233, 233, 233, 0.5)',
        padding: sizeStyles.padding,
        borderRadius: sizeStyles.borderRadius,
        display: 'flex',
        flexDirection: 'column',
        gap: sizeStyles.gap,
        minHeight: sizeStyles.minHeight,
        justifyContent: 'center',
        maxWidth: maxWidth,
        '@media(max-width: 1170px)': {
          padding: sizeStyles.mediaPadding,
        },
        ...(onClick && {
          cursor: 'pointer',
          transition: 'border-color 0.15s ease, box-shadow 0.15s ease',
          '&:hover': {
            borderColor: isSavings ? '#86EFAC' : '#b8a4f0',
            boxShadow: isSavings
              ? '0px 2px 12px 0px #86EFAC'
              : '0px 4px 20px -1px rgba(200, 180, 255, 0.3), 0px 2px 10px 0px rgba(200, 180, 255, 0.5)',
          },
        }),
        ...sx,
      }}
    >
      {/* Title */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '8px' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '2px', minWidth: 0 }}>
          <Typography
            sx={{
              color: colors.text.greyDark,
              fontFamily: 'poppins',
              letterSpacing: '-0.01em',
              fontWeight: 400,
              ...titleFontStyles,
            }}
          >
            {title}
          </Typography>
          {showInfoIcon && <InfoIcon tooltipContent={tooltipContent} tooltipPosition={tooltipPosition} />}
        </Box>
        {headerRight && <Box sx={{ flexShrink: 0 }}>{headerRight}</Box>}
      </Box>

      {/* Value */}
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '4px' }}>
        {React.isValidElement(value) ? (
          value
        ) : (
          <Typography
            sx={{
              color: colors.text.secondary,
              fontWeight: 600,
              ...valueFontStyles,
            }}
          >
            {typeof value === 'number' ? formatNumber(value, '-', 0, 0) : value}
          </Typography>
        )}
        {suffix && (
          <Typography
            sx={{
              color: '#717886',
              fontSize: isSmall ? '12px' : '14px',
              fontWeight: 400,
            }}
          >
            {suffix}
          </Typography>
        )}
      </Box>
    </Box>
  );
};

SummaryWidget.propTypes = {
  title: PropTypes.string.isRequired,
  value: PropTypes.oneOfType([PropTypes.string, PropTypes.number, PropTypes.node]).isRequired,
  variant: PropTypes.oneOf(['default', 'savings']),
  size: PropTypes.oneOf(['default', 'small']),
  maxWidth: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  showInfoIcon: PropTypes.bool,
  tooltipContent: PropTypes.oneOfType([PropTypes.string, PropTypes.node]),
  sx: PropTypes.object,
  tooltipPosition: PropTypes.oneOf(['top', 'bottom', 'left', 'right']),
  onClick: PropTypes.func,
  suffix: PropTypes.string,
  headerRight: PropTypes.node,
};

SummaryWidget.defaultProps = {
  tooltipPosition: 'top',
};

export default SummaryWidget;
