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
  success: { bg: 'var(--ds-green-100)', text: 'var(--ds-green-600)', dot: 'var(--ds-green-600)' },
  error: { bg: 'var(--ds-red-100)', text: 'var(--ds-red-600)', dot: 'var(--ds-red-600)' },
  warning: { bg: 'var(--ds-amber-100)', text: 'var(--ds-amber-500)', dot: 'var(--ds-amber-500)' },
  info: { bg: 'var(--ds-blue-200)', text: 'var(--ds-blue-600)', dot: 'var(--ds-blue-600)' },
  grey: { bg: 'var(--ds-background-300)', text: 'var(--ds-gray-600)', dot: 'var(--ds-gray-400)' },
  purple: { bg: 'var(--ds-brand-100)', text: 'var(--ds-purple-600)', dot: 'var(--ds-purple-600)' },
};

const SIZE_CONFIG: Record<BadgeSize, { px: string; py: string; fontSize: string; dotSize: number }> = {
  small: { px: 'var(--ds-space-1)', py: 'var(--ds-space-1)', fontSize: 'var(--ds-text-caption)', dotSize: 6 },
  medium: { px: 'var(--ds-space-2)', py: 'var(--ds-space-1)', fontSize: 'var(--ds-text-caption)', dotSize: 8 },
};

const StatusBadge: React.FC<StatusBadgeProps> = ({ label, variant = 'grey', size = 'medium', dot = false }) => {
  const colors = VARIANT_COLORS[variant];
  const sizeConfig = SIZE_CONFIG[size];

  if (dot) {
    return (
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
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
            fontWeight: 'var(--ds-font-weight-medium)',
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
        borderRadius: 'var(--ds-radius-sm)',
        backgroundColor: colors.bg,
      }}
    >
      <Typography
        sx={{
          fontSize: sizeConfig.fontSize,
          fontWeight: 'var(--ds-font-weight-medium)',
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
