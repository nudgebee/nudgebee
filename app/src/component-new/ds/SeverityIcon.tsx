/**
 * SeverityIcon — DS V2 (canonical / new).
 * Spec: app/design-system/primitives/data-display/severity-icon.html
 *
 * Letter-badge severity marker. Used inline in tables, chat preambles, list
 * rows, and icon-only legends. Letter + colour are fixed per level —
 * consumers pick a level, never the glyph. Always pair with an accessible
 * label (visible or `aria-label`).
 *
 * Variants per spec:
 *   level   = 'critical' | 'high' | 'medium' | 'low' | 'info'
 *   variant = 'bar'     (default — left colour bar + soft tint)
 *           | 'square'  (rounded square + soft tint, no bar)
 *   size    = 12 | 14 | 16 | 20
 *
 * Letter map (fixed per spec — do not override):
 *   critical → C
 *   high     → H
 *   medium   → M
 *   low      → L
 *   info     → I
 *
 * Don't (per spec):
 *   - Don't override the letter or colour — the level → letter/colour
 *     mapping is fixed for muscle-memory recognition.
 *   - Don't use a Severity icon to communicate Status. Severity is "how bad",
 *     Status is "what's it doing right now".
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';
import Tooltip from './Tooltip';

export type SeverityLevel = 'critical' | 'high' | 'medium' | 'low' | 'info';
export type SeverityVariant = 'bar' | 'square';
export type SeveritySize = 12 | 14 | 16 | 20;

export interface SeverityIconProps {
  level: SeverityLevel;
  variant?: SeverityVariant;
  size?: SeveritySize;
  /** Optional visible label — composition becomes `icon+text` or `icon+text+count`. */
  label?: React.ReactNode;
  /** Optional count — composition becomes `icon+count` or `icon+text+count`. */
  count?: number;
  /** Required when composition is `icon-only` (no label). */
  'aria-label'?: string;
  className?: string;
  id?: string;
}

const LEVEL_LETTER: Record<SeverityLevel, string> = {
  critical: 'C',
  high: 'H',
  medium: 'M',
  low: 'L',
  info: 'I',
};

const LEVEL_DEFAULT_LABEL: Record<SeverityLevel, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
  info: 'Info',
};

const LEVEL_PALETTE: Record<SeverityLevel, { bg: string; bar: string; text: string }> = {
  critical: { bg: 'var(--ds-red-200)', bar: 'var(--ds-red-700)', text: 'var(--ds-red-700)' },
  high: { bg: 'var(--ds-red-100)', bar: 'var(--ds-red-500)', text: 'var(--ds-red-600)' },
  medium: { bg: 'var(--ds-amber-100)', bar: 'var(--ds-amber-500)', text: 'var(--ds-amber-700)' },
  low: { bg: 'var(--ds-blue-100)', bar: 'var(--ds-blue-500)', text: 'var(--ds-blue-700)' },
  info: { bg: 'var(--ds-gray-100)', bar: 'var(--ds-gray-400)', text: 'var(--ds-gray-600)' },
};

const BADGE_DIMS: Record<SeveritySize, { height: number; minWidth: number; fontSize: number; barWidth: number; radius: number }> = {
  12: { height: 16, minWidth: 18, fontSize: 10, barWidth: 3, radius: 4 },
  14: { height: 18, minWidth: 20, fontSize: 11, barWidth: 3, radius: 4 },
  16: { height: 20, minWidth: 22, fontSize: 12, barWidth: 4, radius: 5 },
  20: { height: 24, minWidth: 26, fontSize: 14, barWidth: 4, radius: 6 },
};

/** SVG "I" with prominent top and bottom serifs that scale with the badge. */
function ILetterIcon({ fontSize }: { fontSize: number }) {
  const w = Math.round(fontSize * 0.65);
  const h = Math.round(fontSize * 0.9);
  const barH = Math.max(2, Math.round(h * 0.18));
  const barW = Math.round(w * 0.75);
  const barX = (w - barW) / 2;
  const stemW = Math.max(2, Math.round(w * 0.22));
  const stemX = (w - stemW) / 2;
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} fill='currentColor' aria-hidden='true'>
      {/* top bar */}
      <rect x={barX} y={0} width={barW} height={barH} rx={1} />
      {/* stem */}
      <rect x={stemX} y={barH} width={stemW} height={h - barH * 2} />
      {/* bottom bar */}
      <rect x={barX} y={h - barH} width={barW} height={barH} rx={1} />
    </svg>
  );
}

function Badge({ level, variant, size }: { level: SeverityLevel; variant: SeverityVariant; size: SeveritySize }) {
  const palette = LEVEL_PALETTE[level];
  const dims = BADGE_DIMS[size];
  const letter = LEVEL_LETTER[level];

  return (
    <Box
      aria-hidden='true'
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        position: 'relative',
        height: `${dims.height}px`,
        minWidth: variant === 'bar' ? `${dims.minWidth + dims.barWidth + 4}px` : `${dims.minWidth}px`,
        paddingLeft: variant === 'bar' ? `${dims.barWidth + 4}px` : 0,
        paddingRight: variant === 'bar' ? '4px' : 0,
        background: palette.bg,
        color: palette.text,
        borderRadius: `${dims.radius}px`,
        fontSize: `${dims.fontSize}px`,
        fontWeight: 'var(--ds-font-weight-semibold)',
        fontFamily: 'var(--ds-font-display)',
        lineHeight: 1,
        flex: '0 0 auto',
        '&::before':
          variant === 'bar'
            ? {
                content: '""',
                position: 'absolute',
                left: 0,
                top: 0,
                bottom: 0,
                width: `${dims.barWidth}px`,
                background: palette.bar,
                borderTopLeftRadius: 'inherit',
                borderBottomLeftRadius: 'inherit',
              }
            : undefined,
      }}
    >
      {letter === 'I' ? <ILetterIcon fontSize={dims.fontSize} /> : letter}
    </Box>
  );
}

export function SeverityIcon({ level, variant = 'bar', size = 14, label, count, 'aria-label': ariaLabel, className, id }: SeverityIconProps) {
  const hasLabel = label !== undefined;
  const hasCount = count !== undefined;
  const composition: 'icon-only' | 'icon+text' | 'icon+count' | 'icon+text+count' =
    hasLabel && hasCount ? 'icon+text+count' : hasLabel ? 'icon+text' : hasCount ? 'icon+count' : 'icon-only';

  const accessibleName = composition === 'icon-only' ? ariaLabel ?? LEVEL_DEFAULT_LABEL[level] : undefined;
  const tooltipText = ariaLabel ?? LEVEL_DEFAULT_LABEL[level];

  return (
    <Tooltip title={tooltipText}>
      <Box
        id={id}
        className={className}
        aria-label={accessibleName}
        role={composition === 'icon-only' ? 'img' : undefined}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: composition === 'icon-only' ? 0 : ds.space.mul(0, 3),
          color: LEVEL_PALETTE[level].text,
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-medium)',
          lineHeight: 1,
        }}
      >
        <Badge level={level} variant={variant} size={size} />
        {hasLabel && <Box component='span'>{label}</Box>}
        {hasCount && (
          <Box component='span' sx={{ fontVariantNumeric: 'tabular-nums', marginLeft: composition === 'icon+text+count' ? ds.space[1] : 0 }}>
            {composition === 'icon+text+count' ? '· ' : ''}
            {count}
          </Box>
        )}
      </Box>
    </Tooltip>
  );
}

export default SeverityIcon;
