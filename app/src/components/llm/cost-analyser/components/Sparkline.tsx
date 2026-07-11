/**
 * Sparkline — tiny inline SVG trend (table cells). Token-coloured, no chart lib.
 */
import * as React from 'react';
import { Box } from '@mui/material';

interface SparklineProps {
  values: number[];
  width?: number;
  height?: number;
  color?: string;
}

export function Sparkline({ values, width = 84, height = 22, color = 'var(--ds-blue-500)' }: SparklineProps) {
  if (!values.length)
    return (
      <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
        —
      </Box>
    );
  const max = Math.max(...values, 0.000001);
  const min = Math.min(...values, 0);
  const span = max - min || 1;
  const stepX = values.length > 1 ? width / (values.length - 1) : width;
  const points = values
    .map((v, i) => {
      const x = i * stepX;
      const y = height - ((v - min) / span) * (height - 2) - 1;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(' ');

  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden='true' style={{ display: 'block' }}>
      <polyline points={points} fill='none' stroke={color} strokeWidth='1.5' strokeLinejoin='round' strokeLinecap='round' />
    </svg>
  );
}

export default Sparkline;
