/**
 * StatusBadge - Simple colored badge for displaying status/labels
 *
 * Usage:
 * <StatusBadge label="Active" variant="success" />
 * <StatusBadge label="Suppressed" variant="grey" />
 * <StatusBadge label="High" variant="error" size="small" />
 */

import React from 'react';
import { Box, Typography } from '@mui/material';

type BadgeVariant = 'success' | 'error' | 'warning' | 'info' | 'grey' | 'purple';
type BadgeSize = 'small' | 'medium';

interface StatusBadgeProps {
  label: string;
  variant?: BadgeVariant;
  size?: BadgeSize;
  dot?: boolean; // Show colored dot instead of full background
}

const VARIANT_COLORS: Record<BadgeVariant, { bg: string; text: string; dot: string }> = {
  success: { bg: '#E8F5E9', text: '#2E7D32', dot: '#2E7D32' },
  error: { bg: '#FFEBEE', text: '#C62828', dot: '#C62828' },
  warning: { bg: '#FFF8E1', text: '#F57F17', dot: '#F57F17' },
  info: { bg: '#E3F2FD', text: '#1565C0', dot: '#1565C0' },
  grey: { bg: '#F5F5F5', text: '#616161', dot: '#9E9E9E' },
  purple: { bg: '#F3E5F5', text: '#7B1FA2', dot: '#7B1FA2' },
};

const SIZE_CONFIG: Record<BadgeSize, { px: string; py: string; fontSize: string; dotSize: number }> = {
  small: { px: '6px', py: '1px', fontSize: '10px', dotSize: 6 },
  medium: { px: '10px', py: '2px', fontSize: '11px', dotSize: 8 },
};

const StatusBadge: React.FC<StatusBadgeProps> = ({ label, variant = 'grey', size = 'medium', dot = false }) => {
  const colors = VARIANT_COLORS[variant];
  const sizeConfig = SIZE_CONFIG[size];

  if (dot) {
    return (
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
        <Box
          sx={{
            width: sizeConfig.dotSize,
            height: sizeConfig.dotSize,
            borderRadius: '50%',
            backgroundColor: colors.dot,
            flexShrink: 0,
          }}
        />
        <Typography
          sx={{
            fontSize: sizeConfig.fontSize,
            fontWeight: 500,
            color: colors.text,
          }}
        >
          {label}
        </Typography>
      </Box>
    );
  }

  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        px: sizeConfig.px,
        py: sizeConfig.py,
        borderRadius: '4px',
        backgroundColor: colors.bg,
      }}
    >
      <Typography
        sx={{
          fontSize: sizeConfig.fontSize,
          fontWeight: 500,
          color: colors.text,
          whiteSpace: 'nowrap',
        }}
      >
        {label}
      </Typography>
    </Box>
  );
};

export default StatusBadge;
