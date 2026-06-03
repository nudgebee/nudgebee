import React from 'react';
import { Box, Typography } from '@mui/material';
import { Button as DsButton } from '@components1/ds/Button';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { formatNumber } from '@lib/formatter';

const InfoIcon = ({ tooltipContent, tooltipPosition }) => {
  return (
    <DsButton
      tone='ghost'
      size='sm'
      composition='icon-only'
      aria-label='Info'
      icon={<InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
      tooltip={tooltipContent || undefined}
      tooltipPlacement={tooltipPosition}
    />
  );
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
    ? {
        border: '1.5px solid',
        padding: 'var(--ds-space-1) var(--ds-space-4)',
        borderRadius: 'var(--ds-radius-lg)',
        gap: 'var(--ds-space-1)',
        minHeight: ds.space.mul(0, 28),
        mediaPadding: `${ds.space.mul(0, 3)} ${ds.space[3]} !important`,
      }
    : {
        border: '2px solid',
        padding: 'var(--ds-space-2) var(--ds-space-4)',
        borderRadius: 'var(--ds-radius-xl)',
        gap: 'var(--ds-space-2)',
        minHeight: ds.space.mul(0, 40),
        mediaPadding: `${ds.space[4]} !important`,
      };

  const titleFontStyles = isSmall
    ? { fontSize: 'var(--ds-text-caption)', lineHeight: '14px' }
    : { fontSize: 'var(--ds-text-body-lg)', lineHeight: '16px' };

  const valueFontStyles = isSmall
    ? { fontSize: 'var(--ds-text-heading)', lineHeight: '22px' }
    : { fontSize: 'var(--ds-text-display)', lineHeight: '28px' };

  return (
    <Box
      onClick={onClick}
      sx={{
        border: sizeStyles.border,
        borderColor: isSavings ? ds.green[200] : ds.purple[200],
        backgroundColor: ds.background[100],
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
            borderColor: isSavings ? ds.green[300] : ds.purple[300],
            boxShadow: isSavings
              ? '0px 2px 12px 0px #86EFAC'
              : '0px 4px 20px -1px rgba(200, 180, 255, 0.3), 0px 2px 10px 0px rgba(200, 180, 255, 0.5)',
          },
        }),
        ...sx,
      }}
    >
      {/* Title */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
          <Typography
            sx={{
              color: ds.gray[600],
              fontFamily: 'poppins',
              letterSpacing: '-0.01em',
              fontWeight: 'var(--ds-font-weight-regular)',
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
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-1)' }}>
        {React.isValidElement(value) ? (
          value
        ) : (
          <Typography
            sx={{
              color: ds.gray[700],
              fontWeight: 'var(--ds-font-weight-semibold)',
              ...valueFontStyles,
            }}
          >
            {typeof value === 'number' ? formatNumber(value, '-', 0, 0) : value}
          </Typography>
        )}
        {suffix && (
          <Typography
            sx={{
              color: 'var(--ds-brand-400)',
              fontSize: isSmall ? '12px' : '14px',
              fontWeight: 'var(--ds-font-weight-regular)',
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
