/**
 * ProgressBar — DS V2 of legacy ProgressBar.
 * Spec: app/design-system/primitives/feedback/progress-bar.html
 *
 * Utilisation gauge — fraction of a known maximum (CPU, memory, quota).
 * Tone is bound to the cost-axis convention (low utilisation = green, high =
 * red), but the consumer asserts the threshold via `thresholds` prop.
 *
 * Variants per spec:
 *   size        = 'sm' | 'md'
 *                 (sm=4px is appropriate for dense data surfaces too — KPI
 *                 tiles, util tables — where the bar sits under a number)
 *   tone        = 'neutral' | 'success' | 'warning' | 'critical'
 *                 (auto-resolved from `thresholds` + `value`)
 *   composition = 'bar' | 'bar+label' | 'label+bar+value'
 *                 (auto from `label` + `showValue` props)
 *
 * Don't (per spec):
 *   - Don't pick tone manually. Pass `thresholds` and let the primitive choose.
 *     Manual picks drift across surfaces. (Tone is provided here only as an
 *     explicit override for the rare "this isn't a utilisation gauge" case.)
 *   - Don't use ProgressBar for unknown maximums. That's `ProgressLinear`.
 *
 * Migration:
 *   `import ProgressBar from '@components1/common/widgets/ProgressBar'`
 * → `import { ProgressBar } from '@components1/ds/ProgressBar'`
 *   Tone selection moves from per-call to `thresholds` (per spec).
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type ProgressBarSize = 'sm' | 'md';
export type ProgressBarTone = 'neutral' | 'success' | 'warning' | 'critical';

export interface ProgressBarThresholds {
  /** Below this %, tone is `success`. */
  success?: number;
  /** Below this %, tone is `warning`. Above is `critical`. */
  warning?: number;
  /** Below this %, tone is `critical`. (Use sparingly; usually warning is enough.) */
  critical?: number;
}

export interface ProgressBarProps {
  /** Current value. */
  value: number;
  /** Maximum value. Defaults to 100. */
  max?: number;
  /** Optional left-aligned label. Triggers `bar+label` or `label+bar+value` composition. */
  label?: React.ReactNode;
  /** Show the right-aligned value/percent. Triggers `label+bar+value`. */
  showValue?: boolean;
  /** Format the displayed value (default: `${pct}%`). */
  formatValue?: (value: number, max: number, pct: number) => string;
  /**
   * Threshold percentages that drive tone resolution. Spec example:
   * `{ success: 60, warning: 80, critical: 95 }` →
   *   pct ≤ 60 → success, ≤ 80 → warning, > 80 → critical.
   */
  thresholds?: ProgressBarThresholds;
  /** Override auto-resolved tone. Use sparingly. */
  tone?: ProgressBarTone;
  size?: ProgressBarSize;
  className?: string;
  id?: string;
}

const TONE_FILL: Record<ProgressBarTone, string> = {
  neutral: 'var(--ds-gray-500)',
  success: 'var(--ds-green-500)',
  warning: 'var(--ds-amber-500)',
  critical: 'var(--ds-red-500)',
};

const SIZE_TOKENS: Record<ProgressBarSize, { trackHeight: string; fontSize: string; labelWidth: string; valueMinWidth: string; gap: string }> = {
  // 4px sm is intentional: dense data surfaces (KPI tiles, utilization
  // tables) need a bar that sits under a number without overpowering it,
  // and this primitive is non-interactive so the touch-target floor that
  // applies to Button/Chip doesn't apply here.
  sm: { trackHeight: '4px', fontSize: 'var(--ds-text-caption)', labelWidth: '60px', valueMinWidth: '40px', gap: 'var(--ds-space-2)' },
  md: { trackHeight: '8px', fontSize: 'var(--ds-text-small)', labelWidth: '80px', valueMinWidth: '48px', gap: 'var(--ds-space-3)' },
};

function deriveTone(pct: number, thresholds?: ProgressBarThresholds): ProgressBarTone {
  if (!thresholds) return 'neutral';
  // Spec example: thresholds={{ success: 60, warning: 80, critical: 95 }}
  // means ≤60 success, ≤80 warning, >80 critical
  const { success, warning, critical } = thresholds;
  if (success !== undefined && pct <= success) return 'success';
  if (warning !== undefined && pct <= warning) return 'warning';
  if (critical !== undefined && pct <= critical) return 'critical';
  // pct > all thresholds → critical (the worst-case bucket)
  return 'critical';
}

export function ProgressBar({
  value,
  max = 100,
  label,
  showValue,
  formatValue,
  thresholds,
  tone: toneOverride,
  size = 'sm',
  className,
  id,
}: ProgressBarProps) {
  const tokens = SIZE_TOKENS[size];
  const safeValue = Math.min(Math.max(0, value), max);
  const pct = (safeValue / max) * 100;
  const tone = toneOverride ?? deriveTone(pct, thresholds);
  const fill = TONE_FILL[tone];

  // Composition is auto-derived from props presence:
  //   no label → 'bar'
  //   label, no showValue → 'bar+label'
  //   label + showValue → 'label+bar+value'
  const showLabel = label !== undefined;
  const showValueText = showValue !== undefined ? showValue : !!thresholds; // sensible default: show value when thresholds are given

  const valueText = showValueText ? (formatValue ? formatValue(safeValue, max, pct) : `${Math.round(pct)} %`) : null;

  const trackBar = (
    <Box
      role='progressbar'
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={safeValue}
      aria-label={typeof label === 'string' ? label : undefined}
      sx={{
        flex: 1,
        height: tokens.trackHeight,
        backgroundColor: 'var(--ds-gray-200)',
        borderRadius: 'var(--ds-radius-pill)',
        overflow: 'hidden',
      }}
    >
      <Box
        aria-hidden='true'
        sx={{
          width: `${pct}%`,
          height: '100%',
          backgroundColor: fill,
          borderRadius: 'var(--ds-radius-pill)',
          transition: 'width var(--ds-motion-medium) var(--ds-motion-ease)',
        }}
      />
    </Box>
  );

  // Composition: 'bar'
  if (!showLabel) {
    return (
      <Box id={id} className={className} sx={{ width: '100%', display: 'flex', alignItems: 'center', gap: tokens.gap }}>
        {trackBar}
        {showValueText && valueText && (
          <Box
            component='span'
            sx={{
              fontSize: tokens.fontSize,
              color: 'var(--ds-gray-700)',
              fontVariantNumeric: 'tabular-nums',
              minWidth: tokens.valueMinWidth,
              textAlign: 'right',
              flexShrink: 0,
            }}
          >
            {valueText}
          </Box>
        )}
      </Box>
    );
  }

  // Composition: 'bar+label' or 'label+bar+value'
  return (
    <Box id={id} className={className} sx={{ display: 'flex', alignItems: 'center', gap: tokens.gap, width: '100%' }}>
      <Box
        component='span'
        sx={{
          width: tokens.labelWidth,
          fontSize: tokens.fontSize,
          color: 'var(--ds-gray-600)',
          flexShrink: 0,
        }}
      >
        {label}
      </Box>
      {trackBar}
      {showValueText && valueText && (
        <Box
          component='span'
          sx={{
            fontSize: tokens.fontSize,
            color: 'var(--ds-gray-700)',
            fontVariantNumeric: 'tabular-nums',
            minWidth: tokens.valueMinWidth,
            textAlign: 'right',
            flexShrink: 0,
          }}
        >
          {valueText}
        </Box>
      )}
    </Box>
  );
}

export default ProgressBar;
