/**
 * Stat — DS V2 of legacy SummaryWidget.
 * Spec:        app/design-system/primitives/data-display/stat.html
 * Variants:    size = 'sm' | 'md' | 'hero'   (hero uses --ds-text-display)
 *              composition = 'label+value' | '+delta' | '+sub' | 'icon+label+value' (auto from props)
 *              delta tone (cost-axis): 'high-savings' | 'savings' | 'neutral' | 'waste' | 'high-waste'
 *              align = 'start' | 'center'
 *
 * Migration:   `import SummaryWidget from '@components1/optimise/SummaryWidget'`
 *           →  `import { Stat } from '@components1/ds/Stat'`
 *
 *   V1 prop          →  V2 prop
 *   title            →  label
 *   value            →  value (string | number | ReactNode)
 *   variant='savings'→  Add a `delta={{ tone: 'savings', ... }}` instead of toning the container
 *                       (V2 spec: "Don't tone the value. Tone goes on the delta.")
 *   size='small'     →  size='sm'
 *   size='default'   →  size='md' (or 'hero' for the headline figure)
 *   showInfoIcon     →  pass `info={{ tooltip: '...' }}`
 *   onClick          →  onClick
 *   suffix           →  sub (rendered under value as caption)
 *   headerRight      →  headerRight
 *
 * Don't (per spec):
 *   - Don't use more than one `hero` Stat per page.
 *   - Don't tone the value. Tone goes on the delta.
 *   - Don't render a delta without a comparison anchor.
 *   - Don't combine Stat with a CostCallout for the same figure.
 */
import * as React from 'react';
import { Box, IconButton, Typography } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward';
import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward';
import { formatNumber } from '@lib/formatter';
import CustomTooltip from '@components1/common/CustomTooltip';

export type StatSize = 'sm' | 'md' | 'hero';
export type StatAlign = 'start' | 'center';
/** Cost-axis tones per D7 — green=good (savings), red=bad (waste), neutral=no semantic */
export type DeltaTone = 'high-savings' | 'savings' | 'neutral' | 'waste' | 'high-waste';
export type ValueFormat = 'plain' | 'percent' | 'currency';

export interface StatDelta {
  /** The change value (positive or negative number, or pre-formatted string). */
  value: number | string;
  /** The comparison anchor — "vs last month", "vs 1h ago". REQUIRED per spec. */
  period: string;
  /** Cost-axis tone of the delta. Up-arrow rendered for positive numeric values, down-arrow for negative. */
  tone?: DeltaTone;
  /** Override arrow direction; defaults to derived from sign of `value` if numeric. */
  direction?: 'up' | 'down' | 'flat';
}

export interface StatInfoSlot {
  tooltip: React.ReactNode;
  position?: 'top' | 'bottom' | 'left' | 'right';
}

export interface StatProps {
  label: string;
  value: string | number | React.ReactNode;
  size?: StatSize;
  align?: StatAlign;
  delta?: StatDelta;
  /** Caption rendered under value (e.g., "last 24h") */
  sub?: React.ReactNode;
  /** Optional left-aligned icon (size-scaled automatically) */
  icon?: React.ReactNode;
  /** Optional info hint with tooltip */
  info?: StatInfoSlot;
  /** Optional right-aligned content in the header row */
  headerRight?: React.ReactNode;
  /** Pre-format numeric value via the matching utility */
  format?: ValueFormat;
  onClick?: () => void;
  maxWidth?: string;
  id?: string;
  sx?: object;
}

const SIZE_TOKENS: Record<StatSize, { label: string; value: string; valueWeight: string; lineHeight: string }> = {
  sm: { label: 'var(--ds-text-caption)', value: 'var(--ds-text-title)', valueWeight: 'var(--ds-font-weight-semibold)', lineHeight: '1.2' },
  md: { label: 'var(--ds-text-small)', value: 'var(--ds-text-heading)', valueWeight: 'var(--ds-font-weight-semibold)', lineHeight: '1.2' },
  hero: { label: 'var(--ds-text-body-lg)', value: 'var(--ds-text-display)', valueWeight: 'var(--ds-font-weight-semibold)', lineHeight: '1.1' },
};

const DELTA_TONE_COLOR: Record<DeltaTone, string> = {
  'high-savings': 'var(--ds-green-700)',
  savings: 'var(--ds-green-600)',
  neutral: 'var(--ds-gray-500)',
  waste: 'var(--ds-red-600)',
  'high-waste': 'var(--ds-red-700)',
};

function formatValue(value: string | number | React.ReactNode, format: ValueFormat): React.ReactNode {
  if (React.isValidElement(value)) return value;
  if (typeof value === 'number') {
    if (format === 'currency') return `$${formatNumber(value, '-', 0, 2)}`;
    if (format === 'percent') return `${formatNumber(value, '-', 0, 2)}%`;
    return formatNumber(value, '-', 0, 0);
  }
  return value;
}

function deriveDirection(d: StatDelta): 'up' | 'down' | 'flat' {
  if (d.direction) return d.direction;
  if (typeof d.value === 'number') {
    if (d.value > 0) return 'up';
    if (d.value < 0) return 'down';
  }
  return 'flat';
}

function DeltaPill({ delta, size }: { delta: StatDelta; size: StatSize }) {
  const tone = delta.tone ?? 'neutral';
  const color = DELTA_TONE_COLOR[tone];
  const dir = deriveDirection(delta);
  const Arrow = dir === 'up' ? ArrowUpwardIcon : dir === 'down' ? ArrowDownwardIcon : null;
  const fontSize = size === 'hero' ? 'var(--ds-text-body)' : 'var(--ds-text-caption)';

  let formattedValue: React.ReactNode = delta.value;
  if (typeof delta.value === 'number') {
    formattedValue = `${delta.value > 0 ? '+' : ''}${formatNumber(delta.value, '-', 0, 2)}`;
  }

  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5, mt: 0.5, color, fontSize }}>
      {Arrow && <Arrow sx={{ fontSize: size === 'hero' ? 14 : 12 }} />}
      <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
        {formattedValue}
      </Box>
      <Box component='span' sx={{ color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-regular)' }}>
        {delta.period}
      </Box>
    </Box>
  );
}

export function Stat({
  label,
  value,
  size = 'md',
  align = 'start',
  delta,
  sub,
  icon,
  info,
  headerRight,
  format = 'plain',
  onClick,
  maxWidth,
  id,
  sx,
}: StatProps) {
  const tokens = SIZE_TOKENS[size];
  const isHorizontal = !!icon;

  return (
    <Box
      id={id}
      onClick={onClick}
      role={onClick ? 'button' : undefined}
      tabIndex={onClick ? 0 : undefined}
      sx={{
        display: 'flex',
        flexDirection: isHorizontal ? 'row' : 'column',
        alignItems: align === 'center' ? 'center' : isHorizontal ? 'center' : 'flex-start',
        gap: isHorizontal ? 'var(--ds-space-3)' : 'var(--ds-space-1)',
        textAlign: align === 'center' ? 'center' : 'left',
        maxWidth,
        cursor: onClick ? 'pointer' : 'default',
        transition: onClick ? 'background-color var(--ds-motion-micro) var(--ds-motion-ease)' : undefined,
        '&:hover': onClick ? { backgroundColor: 'var(--ds-gray-100)' } : undefined,
        ...sx,
      }}
    >
      {icon && (
        <Box
          aria-hidden='true'
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--ds-gray-600)',
            flexShrink: 0,
          }}
        >
          {icon}
        </Box>
      )}
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', minWidth: 0, flex: 1 }}>
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 'var(--ds-space-2)',
          }}
        >
          <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5, minWidth: 0 }}>
            <Typography
              component='span'
              sx={{
                fontSize: tokens.label,
                fontWeight: 'var(--ds-font-weight-regular)',
                color: 'var(--ds-gray-600)',
                lineHeight: 1.3,
              }}
            >
              {label}
            </Typography>
            {info && (
              <CustomTooltip title={info.tooltip} placement={info.position ?? 'top'}>
                <IconButton size='small' sx={{ p: 0, color: 'var(--ds-gray-500)' }} aria-label={`Info about ${label}`}>
                  <InfoOutlinedIcon sx={{ fontSize: 14 }} />
                </IconButton>
              </CustomTooltip>
            )}
          </Box>
          {headerRight && <Box sx={{ flexShrink: 0 }}>{headerRight}</Box>}
        </Box>
        <Typography
          component='div'
          sx={{
            fontSize: tokens.value,
            fontWeight: tokens.valueWeight,
            color: 'var(--ds-gray-700)',
            lineHeight: tokens.lineHeight,
            wordBreak: 'break-word',
          }}
        >
          {formatValue(value, format)}
        </Typography>
        {sub !== undefined && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mt: 0.25 }}>{sub}</Box>}
        {delta && <DeltaPill delta={delta} size={size} />}
      </Box>
    </Box>
  );
}

export default Stat;
