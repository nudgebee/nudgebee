/**
 * Comparison — DS V2 primitive. Net new.
 * Spec: app/design-system/primitives/data-display/comparison.html
 *
 * Compact "before → after, with delta" presentation. Built for dense table
 * cells where 2-3 comparisons stack vertically (e.g. per-container CPU /
 * memory request vs recommended). Pairs naturally with `ComparisonGroup`
 * which adds spacing rhythm between adjacent Comparisons.
 *
 * Anatomy:
 *
 *     ml-k8s-server                       ← optional label (caption, italic, muted)
 *     1.2 Core  →  0.01 Core   [↓ 99%]    ← before [unit]  arrow  after [unit]   delta-pill
 *
 * The arrow indicates *transition*, not polarity. The delta is a *micro pill*
 * (Chip-style 14×9 px pastel rectangle, borderless) carrying the polarity —
 * colour comes from `polarity` + the direction of change:
 *
 *   polarity='lower-is-better' (default) — savings axis: down=green, up=red
 *   polarity='higher-is-better'          — capacity axis: up=green, down=red
 *   polarity='neutral'                   — informational only, gray
 *
 * Variants per spec:
 *   size        = 'sm' | 'md'                              (table-cell vs page body)
 *   layout      = 'stacked' | 'inline'                     (label position)
 *   polarity    = 'lower-is-better' | 'higher-is-better' | 'neutral'
 *   deltaFormat = 'percent' | 'absolute' | 'auto'          (auto picks % when meaningful)
 *
 * Don't (per spec):
 *   - Don't render Comparison without both `before.value` and `after.value`. If
 *     either is missing, the primitive renders the no-data form (em-dash) — but
 *     that's the *fallback*, not the intended use.
 *   - Don't pick the delta colour manually. Pass `polarity`; let the primitive
 *     resolve. Manual picks drift across surfaces.
 *   - Don't combine Comparison with a ProgressBar in the same cell. Pick one —
 *     Comparison if the comparison is the point, ProgressBar if utilisation
 *     against a known maximum is the point.
 *   - Don't use `size='md'` inside table cells; it's intended for page body
 *     copy where the comparison stands alone.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';

export type ComparisonSize = 'sm' | 'md';
export type ComparisonLayout = 'stacked' | 'inline';
export type ComparisonPolarity = 'lower-is-better' | 'higher-is-better' | 'neutral';
export type ComparisonDeltaFormat = 'percent' | 'absolute' | 'auto';
export type ComparisonTone = 'positive' | 'negative' | 'neutral';

export interface ComparisonValue {
  /** Numeric or pre-formatted string. Pass null when the value is unknown. */
  value: number | string | null | undefined;
  /** Optional unit caption rendered to the right of the value (smaller, lighter). */
  unit?: React.ReactNode;
}

export interface ComparisonProps {
  /** Optional label rendered above (stacked) or before (inline) the values. */
  label?: React.ReactNode;
  before: ComparisonValue;
  after: ComparisonValue;
  /** Sets which direction is "good". Default: lower-is-better (cost-axis). */
  polarity?: ComparisonPolarity;
  /** How to render the delta. Default: 'auto' (percent when finite + non-zero base). */
  deltaFormat?: ComparisonDeltaFormat;
  size?: ComparisonSize;
  layout?: ComparisonLayout;
  /** Pre-formatted delta override (e.g. "↓ 99% saved"). Takes precedence over computed delta. */
  deltaOverride?: React.ReactNode;
  /** Show the delta even when unchanged. Default: true (renders "0%" in neutral
   *  gray so the cell rhythm stays consistent across rows). Set to false to
   *  suppress entirely for changed-only views. */
  showZeroDelta?: boolean;
  className?: string;
  id?: string;
}

interface ComputedDelta {
  /** Signed change: after - before. */
  abs: number;
  /** Signed % change relative to before. null when before is 0. */
  pct: number | null;
}

const SIZE_TOKENS: Record<
  ComparisonSize,
  { dataSize: string; labelSize: string; unitSize: string; deltaSize: string; gap: string; lineHeight: number }
> = {
  sm: {
    dataSize: 'var(--ds-text-small)',
    labelSize: 'var(--ds-text-caption)',
    unitSize: '9px',
    deltaSize: 'var(--ds-text-caption)',
    gap: 'var(--ds-space-2)',
    lineHeight: 1.35,
  },
  md: {
    dataSize: 'var(--ds-text-body)',
    labelSize: 'var(--ds-text-small)',
    unitSize: 'var(--ds-text-caption)',
    deltaSize: 'var(--ds-text-small)',
    gap: 'var(--ds-space-2)',
    lineHeight: 1.4,
  },
};

const TONE_COLOR: Record<ComparisonTone, string> = {
  positive: 'var(--ds-green-600)',
  negative: 'var(--ds-red-600)',
  neutral: 'var(--ds-gray-500)',
};

/** Pastel pill background + text colours for the delta micro-pill. Mirrors the
 *  Chip primitive's `savings` / `waste` / `neutral` tone bg/fg pair, kept here
 *  rather than importing Chip itself so Comparison stays dependency-free and
 *  the borderless variant doesn't require a `borderless` prop on Chip. */
const PILL_BG: Record<ComparisonTone, string> = {
  positive: 'var(--ds-green-100)',
  negative: 'var(--ds-red-100)',
  neutral: 'var(--ds-gray-100)',
};
const PILL_FG: Record<ComparisonTone, string> = {
  positive: 'var(--ds-green-700)',
  negative: 'var(--ds-red-700)',
  neutral: 'var(--ds-gray-500)',
};

function isNumericFinite(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v);
}

function computeDelta(before: number | string | null | undefined, after: number | string | null | undefined): ComputedDelta | null {
  if (!isNumericFinite(before) || !isNumericFinite(after)) {
    return null;
  }
  const abs = after - before;
  const pct = before === 0 ? null : (abs / before) * 100;
  return { abs, pct };
}

function formatNumber(n: number): string {
  const a = Math.abs(n);
  if (a >= 1000) {
    return n.toLocaleString(undefined, { maximumFractionDigits: 0 });
  }
  if (a >= 100) {
    return n.toLocaleString(undefined, { maximumFractionDigits: 1 });
  }
  if (a >= 10) {
    return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  }
  return n.toLocaleString(undefined, { maximumFractionDigits: 3 });
}

function resolveDirection(absDelta: number): 'down' | 'up' | 'flat' {
  if (absDelta === 0) return 'flat';
  return absDelta < 0 ? 'down' : 'up';
}

function resolveTone(direction: 'down' | 'up' | 'flat', polarity: ComparisonPolarity): ComparisonTone {
  if (direction === 'flat') return 'neutral';
  if (polarity === 'neutral') return 'neutral';
  if (polarity === 'lower-is-better') return direction === 'down' ? 'positive' : 'negative';
  return direction === 'up' ? 'positive' : 'negative';
}

function ValueAtom({
  value,
  unit,
  emphasized,
  tone,
  unitSize,
}: {
  value: number | string | null | undefined;
  unit?: React.ReactNode;
  emphasized: boolean;
  tone: ComparisonTone;
  unitSize: string;
}) {
  if (value === null || value === undefined || value === '') {
    return (
      <Box component='span' sx={{ color: 'var(--ds-gray-400)', fontVariantNumeric: 'tabular-nums' }}>
        —
      </Box>
    );
  }
  const display = isNumericFinite(value) ? formatNumber(value) : String(value);
  return (
    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[0] }}>
      <Box
        component='span'
        sx={{
          color: emphasized && tone !== 'neutral' ? TONE_COLOR[tone] : 'var(--ds-gray-700)',
          fontWeight: emphasized ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-regular)',
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {display}
      </Box>
      {unit && (
        <Box component='span' sx={{ fontSize: unitSize, color: 'var(--ds-gray-400)', lineHeight: 1 }}>
          {unit}
        </Box>
      )}
    </Box>
  );
}

export function Comparison({
  label,
  before,
  after,
  polarity = 'lower-is-better',
  deltaFormat = 'auto',
  size = 'sm',
  layout = 'stacked',
  deltaOverride,
  showZeroDelta = true,
  className,
  id,
}: ComparisonProps) {
  const tokens = SIZE_TOKENS[size];
  const delta = computeDelta(before.value, after.value);
  const direction = delta ? resolveDirection(delta.abs) : 'flat';
  const tone = resolveTone(direction, polarity);
  const arrowGlyph = direction === 'flat' ? '=' : '→';
  const arrowColor = direction === 'flat' ? 'var(--ds-gray-400)' : 'var(--ds-green-500)';
  const isAfterEmphasized = direction !== 'flat';

  // Resolve delta display
  const deltaNode = (() => {
    if (deltaOverride !== undefined) return deltaOverride;
    if (!delta) return null;
    if (direction === 'flat' && !showZeroDelta) return null;

    const dirGlyph = direction === 'down' ? '↓' : direction === 'up' ? '↑' : '·';

    let valueText: string;
    const usePercent = deltaFormat === 'percent' || (deltaFormat === 'auto' && delta.pct !== null);
    if (usePercent && delta.pct !== null) {
      const p = Math.abs(delta.pct);
      valueText = p >= 10 ? `${p.toFixed(0)}%` : `${p.toFixed(1)}%`;
    } else {
      valueText = formatNumber(Math.abs(delta.abs));
    }

    // Micro pill — borderless pastel rectangle. Pastel bg + saturated text
    // recedes below the after-value (which already carries polarity colour),
    // so the delta reads as a discrete secondary signal rather than competing
    // with the numeric values. Sized to the Chip 'micro' tier.
    return (
      <Box
        component='span'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          alignSelf: 'center',
          gap: ds.space[0],
          height: '14px',
          paddingLeft: '5px',
          paddingRight: '5px',
          borderRadius: 'var(--ds-radius-sm)',
          fontSize: '9px',
          fontWeight: 'var(--ds-font-weight-medium)',
          fontVariantNumeric: 'tabular-nums',
          lineHeight: 1,
          whiteSpace: 'nowrap',
          letterSpacing: '0.1px',
          backgroundColor: PILL_BG[tone],
          color: PILL_FG[tone],
        }}
      >
        <Box component='span' sx={{ lineHeight: 1 }}>
          {dirGlyph}
        </Box>
        <Box component='span' sx={{ lineHeight: 1 }}>
          {valueText}
        </Box>
      </Box>
    );
  })();

  const labelNode = label ? (
    <Box
      component='span'
      sx={{
        fontSize: tokens.labelSize,
        color: 'var(--ds-gray-500)',
        fontStyle: 'italic',
        lineHeight: 1.2,
        whiteSpace: 'nowrap',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        maxWidth: layout === 'inline' ? '100px' : undefined,
        flex: layout === 'inline' ? '0 1 auto' : undefined,
      }}
    >
      {label}
    </Box>
  ) : null;

  const valueRow = (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'baseline',
        // Tighter gap between atoms so the whole row fits on one line in
        // narrow table cells. The delta sits inline right after the after-value
        // (no `ml: auto` push), so it travels with the row when content wraps.
        columnGap: tokens.gap,
        rowGap: ds.space[0],
        fontSize: tokens.dataSize,
        lineHeight: tokens.lineHeight,
        flexWrap: 'wrap',
        minWidth: 0,
      }}
    >
      <ValueAtom value={before.value} unit={before.unit} emphasized={false} tone='neutral' unitSize={tokens.unitSize} />
      <Box
        component='span'
        sx={{
          color: arrowColor,
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-medium)',
          lineHeight: 1,
        }}
        aria-hidden='true'
      >
        {arrowGlyph}
      </Box>
      <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: tokens.gap, whiteSpace: 'nowrap' }}>
        <ValueAtom value={after.value} unit={after.unit} emphasized={isAfterEmphasized} tone={tone} unitSize={tokens.unitSize} />
        {deltaNode}
      </Box>
    </Box>
  );

  if (layout === 'inline') {
    return (
      <Box
        id={id}
        className={className}
        sx={{
          display: 'flex',
          alignItems: 'baseline',
          gap: tokens.gap,
          py: '1px',
          minWidth: 0,
        }}
      >
        {labelNode}
        {valueRow}
      </Box>
    );
  }

  // Stacked (default)
  return (
    <Box
      id={id}
      className={className}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        py: ds.space[0],
        gap: ds.space[1],
        minWidth: 0,
      }}
    >
      {labelNode}
      {valueRow}
    </Box>
  );
}

export interface ComparisonGroupProps {
  children: React.ReactNode;
  /** Vertical spacing between Comparisons. Default: 'sm'. */
  spacing?: 'xs' | 'sm' | 'md';
  /** Render dividers between items. Default: false. */
  dividers?: boolean;
  className?: string;
}

const SPACING_TOKEN: Record<NonNullable<ComparisonGroupProps['spacing']>, string> = {
  xs: 'var(--ds-space-1)',
  sm: 'var(--ds-space-2)',
  md: 'var(--ds-space-3)',
};

/** Stacks multiple Comparison rows with consistent rhythm. Cap at 4 per cell. */
export function ComparisonGroup({ children, spacing = 'sm', dividers = false, className }: ComparisonGroupProps) {
  const items = React.Children.toArray(children);
  return (
    <Box
      className={className}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: SPACING_TOKEN[spacing],
        minWidth: 0,
      }}
    >
      {items.map((child, i) => (
        <React.Fragment key={i}>
          {child}
          {dividers && i < items.length - 1 && <Box sx={{ height: '1px', background: 'var(--ds-gray-200)' }} />}
        </React.Fragment>
      ))}
    </Box>
  );
}

export default Comparison;
