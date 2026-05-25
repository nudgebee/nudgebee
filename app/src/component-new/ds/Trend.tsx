/**
 * Trend — DS V2 of legacy TrendArrowPercentage.
 * Spec: app/design-system/primitives/data-display/trend.html
 *
 * Prop API:
 *   - value:   number (the percentage value)
 *   - sign:    1 | -1 (multiplier — flip semantic direction without changing display sign)
 *   - width:   string (container width, ignored when size='sm'/'xs' or variant='chip')
 *   - size:    'xs' | 'sm' | 'md' | 'lg' — DS-standard scale (matches Button/Chip).
 *              `'default'` accepted as a back-compat alias for `'md'` (pre-2026-05-20 callers).
 *   - variant: 'inline' (default) | 'chip'
 *              `inline` — arrow + tinted text only, no chrome
 *              `chip`   — oval pill with tinted background + 200-shade outline
 *                         (per axis-pill DS pattern, padding+radius+bg from SIZE_TOKENS)
 *
 * Convention: positive (value*sign > 0) renders DOWN arrow + green (improvement),
 * negative renders UP arrow + red (regression). Callers control polarity via `sign`.
 */
import * as React from 'react';
import { Typography, Box } from '@mui/material';
import SouthIcon from '@mui/icons-material/South';
import NorthIcon from '@mui/icons-material/North';
import { formatNumber } from '@lib/formatter';

export type TrendVariant = 'inline' | 'chip';
export type TrendSize = 'xs' | 'sm' | 'md' | 'lg';
/** Accepts the DS-standard scale plus `'default'` as a back-compat alias for `'md'`. */
export type TrendSizeInput = TrendSize | 'default';

export interface TrendProps {
  value: number;
  sign?: 1 | -1;
  width?: string;
  size?: TrendSizeInput;
  variant?: TrendVariant;
}

interface SizeTokens {
  iconSize: number;
  textSize: string;
  weight: number | string;
  /** Opacity dimming — only `sm` carries the legacy 0.75 dim for back-compat with V1 consumers. */
  opacity: number;
  chipPadding: string;
}

const SIZE_TOKENS: Record<TrendSize, SizeTokens> = {
  xs: { iconSize: 8, textSize: '10px', weight: 400, opacity: 1, chipPadding: '1px 4px' },
  sm: { iconSize: 10, textSize: '10px', weight: 400, opacity: 0.75, chipPadding: '2px 6px' },
  md: { iconSize: 12, textSize: 'var(--ds-text-small)', weight: 500, opacity: 1, chipPadding: '3px 8px' },
  lg: { iconSize: 14, textSize: 'var(--ds-text-body)', weight: 500, opacity: 1, chipPadding: '4px 10px' },
};

const normalizeSize = (size: TrendSizeInput): TrendSize => (size === 'default' ? 'md' : size);

export function Trend({ value, sign = 1, width = '50px', size = 'md', variant = 'inline' }: TrendProps) {
  const sizeKey = normalizeSize(size);
  const tokens = SIZE_TOKENS[sizeKey];
  const isDown = value * sign > 0;
  const isChip = variant === 'chip';
  const isCompact = sizeKey === 'xs' || sizeKey === 'sm';

  // Inline uses 600-shade on transparent; chip uses 700-shade text on 100-shade bg
  // with a 200-shade outline — standard "soft tinted pill" DS pattern (see
  // primitive-helpers.css `.axis-pill`, where the same 100/200/700 stack appears).
  const inlineColor = isDown ? 'var(--ds-green-600)' : 'var(--ds-red-600)';
  const chipTextColor = isDown ? 'var(--ds-green-700)' : 'var(--ds-red-700)';
  const chipBg = isDown ? 'var(--ds-green-100)' : 'var(--ds-red-100)';
  const chipBorder = isDown ? 'var(--ds-green-200)' : 'var(--ds-red-200)';

  const color = isChip ? chipTextColor : inlineColor;

  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        width: isChip || isCompact ? 'auto' : width,
        marginRight: isChip || isCompact ? '0px' : '8px',
        ...(isChip && {
          backgroundColor: chipBg,
          border: `1px solid ${chipBorder}`,
          borderRadius: 'var(--ds-radius-pill)',
          padding: tokens.chipPadding,
          gap: '2px',
        }),
      }}
    >
      {isDown ? <SouthIcon sx={{ color, fontSize: tokens.iconSize }} /> : <NorthIcon sx={{ color, fontSize: tokens.iconSize }} />}
      <Typography
        sx={{
          color,
          fontSize: tokens.textSize,
          fontWeight: isChip ? 'var(--ds-font-weight-medium)' : tokens.weight,
          opacity: isChip ? 1 : tokens.opacity,
          lineHeight: 1,
        }}
      >
        {formatNumber(value)}%
      </Typography>
    </Box>
  );
}

export default Trend;
