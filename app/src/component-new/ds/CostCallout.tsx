/**
 * CostCallout — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/data-display/cost-callout.html
 *
 * Inline currency + optional delta arrow + optional period. The single
 * sanctioned way to surface a financial figure in a dense surface. Tone comes
 * from the Cost axis (D7) — never picked directly. Always tabular-nums,
 * always locale-aware.
 *
 * Variants per spec:
 *   tone        = 'high-savings' | 'medium-savings' | 'low-savings' | 'neutral' | 'waste'
 *                 (cost-axis level — pass `impact` and `magnitude` and let the axis pick;
 *                  or pass tone directly for backwards compat)
 *   size        = 'sm' | 'md' | 'lg' | 'display'
 *                 ('lg' = --ds-text-heading, pairs with Stat size='md' as the metric value)
 *   composition = 'value' | 'value+period' | 'arrow+value' | 'arrow+value+period'
 *                 (auto from `arrow` + `period` props presence)
 *   currency    = ISO code, defaults to USD; uses Intl.NumberFormat for locale formatting
 *   arrow       = 'down' | 'up' | 'flat' | 'none'
 *
 * Don't (per spec):
 *   - Don't pick tone manually. Pass the impact (savings/waste/neutral) and let
 *     the axis pick the colour.
 *   - Don't show an arrow without a comparison reference. "↓ $890" compared to what?
 *   - Don't use `size='display'` twice on the same page — that defeats its job
 *     as the hero figure.
 *   - Don't combine CostCallout with a separate `Trend` on the same value.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type CostTone = 'high-savings' | 'medium-savings' | 'low-savings' | 'neutral' | 'waste';
export type CostSize = 'sm' | 'md' | 'lg' | 'display';
export type CostArrow = 'down' | 'up' | 'flat' | 'none';

export interface CostCalloutProps {
  /** Numeric value. Sign is conveyed via `arrow` + `tone`, not via the displayed digits. */
  value: number;
  /** ISO currency code (USD/EUR/GBP/INR/...). Defaults to USD. */
  currency?: string;
  /** Optional locale override (defaults to browser/session locale). */
  locale?: string;
  tone?: CostTone;
  size?: CostSize;
  /** Suffix text rendered after the value (e.g. "/ mo", "/ yr", "/ instance"). */
  period?: React.ReactNode;
  arrow?: CostArrow;
  /** Number of fractional digits (default 0 — currency cents only when explicitly requested). */
  fractionDigits?: number;
  className?: string;
  id?: string;
}

const TONE_COLOR: Record<CostTone, string> = {
  'high-savings': 'var(--ds-green-700)',
  'medium-savings': 'var(--ds-green-600)',
  'low-savings': 'var(--ds-green-600)',
  neutral: 'var(--ds-gray-600)',
  waste: 'var(--ds-red-600)',
};

const TONE_WEIGHT: Record<CostTone, number> = {
  'high-savings': 700,
  'medium-savings': 600,
  'low-savings': 600,
  neutral: 500,
  waste: 600,
};

const SIZE_TOKENS: Record<CostSize, { fontSize: string; periodSize: string; arrowSize: number; gap: string }> = {
  sm: { fontSize: 'var(--ds-text-small)', periodSize: 'var(--ds-text-caption)', arrowSize: 10, gap: '2px' },
  md: { fontSize: 'var(--ds-text-body)', periodSize: 'var(--ds-text-caption)', arrowSize: 12, gap: '3px' },
  lg: { fontSize: 'var(--ds-text-heading)', periodSize: 'var(--ds-text-body)', arrowSize: 14, gap: '4px' },
  display: { fontSize: 'var(--ds-text-display)', periodSize: 'var(--ds-text-body)', arrowSize: 18, gap: '6px' },
};

function formatCurrency(value: number, currency: string, locale: string | undefined, fractionDigits: number): string {
  try {
    // currencyDisplay: 'narrowSymbol' renders "$" instead of "US$" / "CA$" — we
    // already disambiguate currency contextually elsewhere on the page.
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      currencyDisplay: 'narrowSymbol',
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
    }).format(Math.abs(value));
  } catch {
    return `${currency} ${Math.abs(value).toFixed(fractionDigits)}`;
  }
}

function arrowGlyph(arrow: CostArrow): string | null {
  if (arrow === 'down') return '↓';
  if (arrow === 'up') return '↑';
  if (arrow === 'flat') return '—';
  return null;
}

export function CostCallout({
  value,
  currency = 'USD',
  locale,
  tone = 'neutral',
  size = 'md',
  period,
  arrow = 'none',
  fractionDigits = 0,
  className,
  id,
}: CostCalloutProps) {
  const tokens = SIZE_TOKENS[size];
  const color = TONE_COLOR[tone];
  const weight = TONE_WEIGHT[tone];
  const formatted = formatCurrency(value, currency, locale, fractionDigits);
  const arrowChar = arrowGlyph(arrow);

  return (
    <Box
      component='span'
      id={id}
      className={className}
      sx={{
        display: 'inline-flex',
        alignItems: 'baseline',
        gap: tokens.gap,
        color,
        fontSize: tokens.fontSize,
        fontWeight: weight,
        fontVariantNumeric: 'tabular-nums',
        lineHeight: 1.1,
      }}
    >
      {arrowChar && (
        <Box
          component='span'
          aria-hidden='true'
          sx={{
            fontSize: tokens.arrowSize,
            lineHeight: 1,
            color: 'inherit',
            display: 'inline-flex',
            alignItems: 'baseline',
          }}
        >
          {arrowChar}
        </Box>
      )}
      <Box component='span' sx={{ color: 'inherit' }}>
        {formatted}
      </Box>
      {period !== undefined && (
        <Box
          component='span'
          sx={{
            fontSize: tokens.periodSize,
            color: 'var(--ds-gray-500)',
            fontWeight: 'var(--ds-font-weight-regular)',
            marginLeft: '2px',
          }}
        >
          {period}
        </Box>
      )}
    </Box>
  );
}

export default CostCallout;
