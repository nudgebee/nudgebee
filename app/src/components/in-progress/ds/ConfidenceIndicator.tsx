/**
 * ConfidenceIndicator — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/agentic/confidence.html
 *
 * Three-dot trio (filled / outline) carrying agent-output confidence.
 * **Shape-encoded, never colour-encoded** — Confidence often co-locates with
 * Severity on the same row, and they would otherwise compete for the same hue.
 *
 * Variants per spec:
 *   level       = 'high' | 'medium' | 'low'   (3 / 2 / 1 filled dots)
 *   composition = 'dots' | 'dots+label' | 'dots+score'
 *                 (auto from `label` + `score` props presence)
 *   size        = 'sm' | 'md'
 *
 * Don't (per spec):
 *   - Don't colour the dots. Confidence is a shape; colour is reserved for axes
 *     the dots may sit beside (severity, status).
 *   - Don't render Confidence on every row of a table. Use it where it changes
 *     a decision (chat suggestions, diff cards, recommendations).
 *   - Don't show a numeric score without the dots. The dots are the muscle-memory
 *     anchor; "0.42" alone reads as latency or load average.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type ConfidenceLevel = 'high' | 'medium' | 'low';
export type ConfidenceSize = 'sm' | 'md';

export interface ConfidenceIndicatorProps {
  level: ConfidenceLevel;
  /** Optional label (e.g. "High confidence"). Renders to the right of the dots. */
  label?: React.ReactNode;
  /** Optional numeric score (0–1). Renders alongside the label, or alone if no label. */
  score?: number;
  size?: ConfidenceSize;
  className?: string;
  id?: string;
}

const LEVEL_FILLED: Record<ConfidenceLevel, number> = {
  high: 3,
  medium: 2,
  low: 1,
};

const SIZE_TOKENS: Record<ConfidenceSize, { dot: number; gap: string; fontSize: string; dotGap: string }> = {
  sm: { dot: 6, gap: '6px', fontSize: 'var(--ds-text-caption)', dotGap: '3px' },
  md: { dot: 8, gap: '8px', fontSize: 'var(--ds-text-small)', dotGap: '4px' },
};

export function ConfidenceIndicator({ level, label, score, size = 'sm', className, id }: ConfidenceIndicatorProps) {
  const tokens = SIZE_TOKENS[size];
  const filled = LEVEL_FILLED[level];

  const ariaLabelText =
    label !== undefined && typeof label === 'string'
      ? label
      : `${level.charAt(0).toUpperCase() + level.slice(1)} confidence${score !== undefined ? ` (${score.toFixed(2)})` : ''}`;

  return (
    <Box
      component='span'
      id={id}
      className={className}
      role='img'
      aria-label={ariaLabelText}
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: tokens.gap,
      }}
    >
      <Box
        component='span'
        aria-hidden='true'
        sx={{
          display: 'inline-flex',
          gap: tokens.dotGap,
        }}
      >
        {[0, 1, 2].map((i) => (
          <Box
            key={i}
            component='span'
            sx={{
              width: tokens.dot,
              height: tokens.dot,
              borderRadius: 'var(--ds-radius-pill)',
              // Shape-encoded — same neutral color for all dots, filled vs outline conveys level
              ...(i < filled
                ? { backgroundColor: 'var(--ds-gray-700)' }
                : { backgroundColor: 'transparent', border: '1px solid var(--ds-gray-400)' }),
              flexShrink: 0,
            }}
          />
        ))}
      </Box>
      {(label !== undefined || score !== undefined) && (
        <Box
          component='span'
          sx={{
            fontSize: tokens.fontSize,
            color: label !== undefined ? 'var(--ds-gray-700)' : 'var(--ds-gray-500)',
          }}
        >
          {label}
          {label !== undefined && score !== undefined && ' · '}
          {score !== undefined && (
            <Box
              component='span'
              sx={{
                fontVariantNumeric: 'tabular-nums',
                color: 'var(--ds-gray-500)',
              }}
            >
              {score.toFixed(2)}
            </Box>
          )}
        </Box>
      )}
    </Box>
  );
}

export default ConfidenceIndicator;
