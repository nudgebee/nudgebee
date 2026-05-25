import React from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import CustomTooltip from '@common/CustomTooltip';

/**
 * Ripple Severity Indicator
 *
 * A visual severity indicator that shows expanding ripple rings
 * around a central dot. More rings = higher severity.
 */

// Severity configuration mapping severity types to colors and ring counts
const SEVERITY_CONFIG = {
  info: {
    color: colors.info,
    bgColor: '#eef2ff',
    rings: 1,
  },
  lowest: {
    color: colors.lowest,
    bgColor: '#f0fdf4',
    rings: 1,
  },
  low: {
    color: colors.low,
    bgColor: '#eff6ff',
    rings: 1,
  },
  medium: {
    color: colors.medium,
    bgColor: '#fffbeb',
    rings: 3,
  },
  high: {
    color: colors.high,
    bgColor: '#fff7ed',
    rings: 3,
  },
  critical: {
    color: colors.critical,
    bgColor: '#fef2f2',
    rings: 4,
  },
  highest: {
    color: colors.highest,
    bgColor: '#fef2f2',
    rings: 4,
  },
  na: {
    color: colors.NA,
    bgColor: '#f5f5f5',
    rings: 1,
  },
  debug: {
    color: colors.NA,
    bgColor: '#f5f5f5',
    rings: 1,
  },
  ok: {
    color: colors.ok,
    bgColor: '#f0fdf4',
    rings: 2,
  },
  firing: {
    color: colors.firing,
    bgColor: '#fef2f2',
    rings: 5,
  },
  open: {
    color: colors.ok,
    bgColor: '#f0fdf4',
    rings: 2,
  },
  default: {
    color: colors.purple,
    bgColor: '#f5f3ff',
    rings: 1,
  },
};

function SeverityIcon({ severityType, size = 38, animated = false, showBackground = false }) {
  // Normalize severity type to lowercase for config lookup
  const normalizedSeverity = severityType ? severityType.toLowerCase() : '';
  const titleCasedSeverity = severityType ? severityType.charAt(0).toUpperCase() + severityType.slice(1).toLowerCase() : '';

  const config = SEVERITY_CONFIG[normalizedSeverity] || SEVERITY_CONFIG.default;
  const { color, bgColor, rings } = config;

  const centerDotSize = 8;
  const ringSpacing = 4;

  return (
    <Box display={'flex'} justifyContent={'center'}>
      <CustomTooltip title={titleCasedSeverity === 'Na' ? 'NA' : titleCasedSeverity}>
        <Box
          sx={{
            position: 'relative',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            width: size,
            height: size,
            backgroundColor: showBackground ? bgColor : 'transparent',
            borderRadius: showBackground ? '6px' : '0',
            '@keyframes ripple-pulse': {
              '0%, 100%': { transform: 'scale(1)', opacity: 0.7 },
              '50%': { transform: 'scale(1.1)', opacity: 0.4 },
            },
            '@keyframes ripple-dot-pulse': {
              '0%, 100%': { transform: 'scale(1)' },
              '50%': { transform: 'scale(1.2)' },
            },
          }}
        >
          {/* Ripple rings - opacity fades from inner to outer */}
          {[...Array(rings)].map((_, i) => {
            const opacity = 0.8 - i * 0.3;
            return (
              <Box
                key={i}
                sx={{
                  position: 'absolute',
                  borderRadius: '50%',
                  border: '1px solid',
                  borderColor: color,
                  width: centerDotSize + (i + 1) * ringSpacing * 2,
                  height: centerDotSize + (i + 1) * ringSpacing * 2,
                  opacity: Math.max(opacity, 0.1),
                  animation: animated ? `ripple-pulse 1.5s ease-in-out ${i * 0.2}s infinite` : 'none',
                }}
              />
            );
          })}

          {/* Center dot */}
          <Box
            sx={{
              borderRadius: '50%',
              zIndex: 10,
              width: centerDotSize,
              height: centerDotSize,
              backgroundColor: color,
              animation: animated ? 'ripple-dot-pulse 1.5s ease-in-out infinite' : 'none',
            }}
          />
        </Box>
      </CustomTooltip>
    </Box>
  );
}

SeverityIcon.propTypes = {
  severityType: PropTypes.string,
  size: PropTypes.number,
  animated: PropTypes.bool,
  showBackground: PropTypes.bool,
};

// React.memo prevents re-renders when props are unchanged — this component
// appears in every row of event/alert/recommendation tables (25+ consumers).
export default React.memo(SeverityIcon);
