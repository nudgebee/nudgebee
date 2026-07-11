/**
 * Section primitives — a consistent, sharp section header (icon + title +
 * optional subtitle + right slot) and a surface card. Used across every screen
 * so the analyser reads as one designed product rather than stacked widgets.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { Card } from '@ui/Card';

interface SectionHeaderProps {
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  icon?: React.ReactNode;
  right?: React.ReactNode;
  dense?: boolean;
}

export function SectionHeader({ title, subtitle, icon, right, dense }: SectionHeaderProps) {
  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--ds-space-3)',
        mb: dense ? 'var(--ds-space-2)' : 'var(--ds-space-3)',
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
        {icon && (
          <Box
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: 'var(--ds-gray-500)',
              '& svg': { fontSize: dense ? 16 : 18 },
            }}
          >
            {icon}
          </Box>
        )}
        <Box sx={{ minWidth: 0 }}>
          <Box
            sx={{
              fontSize: dense ? 'var(--ds-text-body)' : 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              lineHeight: 1.2,
            }}
          >
            {title}
          </Box>
          {subtitle && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mt: '2px' }}>{subtitle}</Box>}
        </Box>
      </Box>
      {right && <Box sx={{ flexShrink: 0 }}>{right}</Box>}
    </Box>
  );
}

/** Card surface with an optional header row — keeps panels visually uniform. */
export function Panel({
  title,
  subtitle,
  icon,
  right,
  children,
  sx,
}: {
  title?: React.ReactNode;
  subtitle?: React.ReactNode;
  icon?: React.ReactNode;
  right?: React.ReactNode;
  children: React.ReactNode;
  sx?: object;
}) {
  return (
    <Card sx={sx}>
      {title && <SectionHeader title={title} subtitle={subtitle} icon={icon} right={right} />}
      {children}
    </Card>
  );
}

export default SectionHeader;
