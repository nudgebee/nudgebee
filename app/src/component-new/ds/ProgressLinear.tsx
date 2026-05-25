/**
 * ProgressLinear — DS V2 of legacy LinearLoader.
 * Spec: app/design-system/primitives/feedback/progress-linear.html
 *
 * Indeterminate or determinate horizontal progress at the top of a region.
 * The "is something happening" indicator — distinct from:
 *   - `ProgressBar` (utilisation gauge with known max)
 *   - `Skeleton` (placeholder for content shape)
 *   - `StreamingIndicator` (chat-tail cursor / pulse trio)
 *
 * Variants per spec:
 *   mode    = 'indeterminate' | 'determinate'  (auto: if `value` provided → determinate)
 *   tone    = 'neutral' | 'info'
 *   surface = 'page-top' | 'section' | 'inline'  (controls thickness + radius)
 *
 * Don't (per spec):
 *   - Don't show ProgressLinear for actions that complete in < 200ms — flash
 *     without progress is just visual noise.
 *   - Don't combine indeterminate ProgressLinear with a Skeleton on the same
 *     region; pick one signal.
 *
 * Migration:
 *   `import LinearLoader from '@components1/k8s/common/LinearLoader'`
 * → `import { ProgressLinear } from '@components1/ds/ProgressLinear'`
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type ProgressLinearMode = 'indeterminate' | 'determinate';
export type ProgressLinearTone = 'neutral' | 'info';
export type ProgressLinearSurface = 'page-top' | 'section' | 'inline';

export interface ProgressLinearProps {
  mode?: ProgressLinearMode;
  /** Determinate progress value. If provided, mode auto-resolves to 'determinate'. */
  value?: number;
  /** Determinate progress maximum. Defaults to 100. */
  total?: number;
  tone?: ProgressLinearTone;
  surface?: ProgressLinearSurface;
  /** Aria label announced to screen readers; default "Loading". */
  'aria-label'?: string;
  className?: string;
  id?: string;
}

const TONE_FILL: Record<ProgressLinearTone, string> = {
  neutral: 'var(--ds-gray-500)',
  info: 'var(--ds-blue-500)',
};

const SURFACE_TOKENS: Record<ProgressLinearSurface, { height: string; radius: string }> = {
  'page-top': { height: '3px', radius: '0' },
  section: { height: '2px', radius: 'var(--ds-radius-pill)' },
  inline: { height: '2px', radius: 'var(--ds-radius-pill)' },
};

const KEYFRAMES = {
  '@keyframes ds-progress-linear-indeterminate': {
    '0%': { left: '-40%', width: '40%' },
    '50%': { left: '20%', width: '60%' },
    '100%': { left: '100%', width: '40%' },
  },
};

export function ProgressLinear({
  mode,
  value,
  total = 100,
  tone = 'info',
  surface = 'section',
  'aria-label': ariaLabel = 'Loading',
  className,
  id,
}: ProgressLinearProps) {
  const resolvedMode: ProgressLinearMode = mode ?? (value !== undefined ? 'determinate' : 'indeterminate');
  const tokens = SURFACE_TOKENS[surface];
  const fill = TONE_FILL[tone];

  // Determinate
  if (resolvedMode === 'determinate') {
    const safeValue = Math.min(Math.max(0, value ?? 0), total);
    const pct = (safeValue / total) * 100;
    return (
      <Box
        role='progressbar'
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={safeValue}
        aria-label={ariaLabel}
        id={id}
        className={className}
        sx={{
          width: '100%',
          height: tokens.height,
          backgroundColor: 'var(--ds-gray-200)',
          borderRadius: tokens.radius,
          overflow: 'hidden',
          position: 'relative',
        }}
      >
        <Box
          aria-hidden='true'
          sx={{
            width: `${pct}%`,
            height: '100%',
            backgroundColor: fill,
            borderRadius: tokens.radius,
            transition: 'width var(--ds-motion-medium) var(--ds-motion-ease)',
          }}
        />
      </Box>
    );
  }

  // Indeterminate
  return (
    <Box
      role='progressbar'
      aria-label={ariaLabel}
      aria-busy='true'
      id={id}
      className={className}
      sx={{
        width: '100%',
        height: tokens.height,
        backgroundColor: 'var(--ds-gray-200)',
        borderRadius: tokens.radius,
        overflow: 'hidden',
        position: 'relative',
        ...KEYFRAMES,
        '@media (prefers-reduced-motion: reduce)': {
          // Per accessibility: collapse to a static dim fill at 33% width
          '& > span': { animation: 'none', left: 0, width: '33%', opacity: 0.6 },
        },
      }}
    >
      <Box
        component='span'
        aria-hidden='true'
        sx={{
          position: 'absolute',
          top: 0,
          height: '100%',
          backgroundColor: fill,
          borderRadius: tokens.radius,
          animation: 'ds-progress-linear-indeterminate 1.4s ease-in-out infinite',
        }}
      />
    </Box>
  );
}

export default ProgressLinear;
