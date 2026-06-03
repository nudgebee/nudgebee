/**
 * StatusIndicator — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/data-display/status-indicator.html
 *
 * Resource-state read-out: a dot (or icon) + primary text + optional subtext.
 * Inherits the Status semantic axis 1:1.
 *
 * Distinct from `ds/Label`: StatusIndicator is for "what is this resource doing
 * right now" with optional context (since / count / target), used in detail
 * headers, inspector panels, chat preambles, empty states. Label is the inline
 * cell tag.
 *
 * Variants:
 *   tone        = 'healthy' | 'degraded' | 'failed' | 'pending' | 'unknown'
 *   size        = 'sm' | 'md'
 *   composition = 'dot+text' | 'dot+text+subtext' | 'icon+text' | 'icon+text+subtext'
 *                 (auto from `icon` + `subtext` props presence)
 *
 * Don't (per spec):
 *   - Don't use StatusIndicator inside a table cell. That's `Label`; the
 *     indicator is for headers, drawers, and chat preambles.
 *   - Don't put actions in the subtext. Actions belong in the surrounding
 *     region, not the indicator.
 *   - Don't add a tone outside the Status axis. There is no "purple" status.
 *   - Don't combine icon+dot. Pick one.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type StatusTone = 'healthy' | 'degraded' | 'failed' | 'pending' | 'unknown';
export type StatusIndicatorSize = 'sm' | 'md';

export interface StatusIndicatorProps {
  tone: StatusTone;
  label: React.ReactNode;
  /** Optional secondary line ("12 nodes · last check 12s ago"). */
  subtext?: React.ReactNode;
  /** Optional left-aligned icon — incompatible with default dot. */
  icon?: React.ReactNode;
  size?: StatusIndicatorSize;
  className?: string;
  id?: string;
}

const TONE_DOT_COLOR: Record<StatusTone, string> = {
  healthy: 'var(--ds-green-700)',
  degraded: 'var(--ds-amber-700)',
  failed: 'var(--ds-red-700)',
  pending: 'var(--ds-blue-600)',
  unknown: 'var(--ds-gray-500)',
};

const SIZE_TOKENS: Record<StatusIndicatorSize, { dot: number; gap: string; mainSize: string; subSize: string }> = {
  sm: { dot: 8, gap: 'var(--ds-space-2)', mainSize: 'var(--ds-text-small)', subSize: 'var(--ds-text-caption)' },
  md: { dot: 10, gap: 'var(--ds-space-2)', mainSize: 'var(--ds-text-body)', subSize: 'var(--ds-text-small)' },
};

export function StatusIndicator({ tone, label, subtext, icon, size = 'md', className, id }: StatusIndicatorProps) {
  const tokens = SIZE_TOKENS[size];
  const dotColor = TONE_DOT_COLOR[tone];

  // Spec: don't combine icon+dot. If both passed, prefer icon.
  if (icon && process.env.NODE_ENV !== 'production') {
    // No-op — explicit icon overrides default dot. The "don't combine" rule
    // applies to consumers; we just pick icon when supplied.
  }

  const leadingNode = icon ? (
    <Box
      component='span'
      aria-hidden='true'
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
        color: dotColor,
        '& svg': { width: 14, height: 14 },
      }}
    >
      {icon}
    </Box>
  ) : (
    <Box
      component='span'
      aria-hidden='true'
      sx={{
        width: tokens.dot,
        height: tokens.dot,
        borderRadius: 'var(--ds-radius-pill)',
        backgroundColor: dotColor,
        flexShrink: 0,
      }}
    />
  );

  return (
    <Box
      id={id}
      className={className}
      sx={{
        display: 'inline-flex',
        alignItems: subtext ? 'flex-start' : 'center',
        gap: tokens.gap,
      }}
    >
      <Box sx={{ display: 'inline-flex', alignItems: 'center', height: subtext ? 18 : 'auto' }}>{leadingNode}</Box>
      <Box sx={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <Box
          component='span'
          sx={{
            fontSize: tokens.mainSize,
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-gray-700)',
            lineHeight: 1.3,
          }}
        >
          {label}
        </Box>
        {subtext !== undefined && (
          <Box
            component='span'
            sx={{
              fontSize: tokens.subSize,
              color: 'var(--ds-gray-500)',
              lineHeight: 1.4,
              mt: 0.25,
            }}
          >
            {subtext}
          </Box>
        )}
      </Box>
    </Box>
  );
}

export default StatusIndicator;
