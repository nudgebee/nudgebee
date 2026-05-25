/**
 * NBStatusBadge — DS V2 of legacy NBStatusBadge (kept name).
 * Spec: app/design-system/primitives/data-display/nb-status-badge.html
 *
 * Workflow-specific status badge — distinct from `Label` because it carries
 * workflow lifecycle semantics (queued / running / paused / completed / failed
 * / cancelled) and renders with a fixed glyph per state. Specialised, not generic.
 *
 * Variants per spec:
 *   state       = 'queued' | 'running' | 'paused' | 'completed' | 'failed' | 'cancelled'
 *   size        = 'sm' | 'md'
 *   composition = 'icon+label' | 'icon+label+timer' | 'icon-only'
 *                 (auto from `timer` + `iconOnly` props)
 *
 * Don't (per spec):
 *   - Don't reuse NBStatusBadge for non-workflow surfaces. Cluster status, pod
 *     status, ticket status — those are `Label`.
 *   - Don't add new states. The lifecycle is fixed by the workflow engine.
 *   - Tone derives from state. No `tone` override.
 *
 * Migration:
 *   `import NBStatusBadge from '@components1/common/NBStatusBadge'`
 * → `import { NBStatusBadge } from '@components1/ds/NBStatusBadge'`
 */
import * as React from 'react';
import { Box } from '@mui/material';
import AccessTimeIcon from '@mui/icons-material/AccessTime';
import AutorenewIcon from '@mui/icons-material/Autorenew';
import PauseIcon from '@mui/icons-material/Pause';
import CheckIcon from '@mui/icons-material/Check';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import BlockIcon from '@mui/icons-material/Block';

export type NBStatusBadgeState = 'queued' | 'running' | 'paused' | 'completed' | 'failed' | 'cancelled';

export type NBStatusBadgeSize = 'sm' | 'md';

export interface NBStatusBadgeProps {
  state: NBStatusBadgeState;
  /** Elapsed-time string for `running` / `paused` (e.g. "02:14"). Triggers the timer composition. */
  timer?: string;
  /** When true, the timer string auto-ticks every second from the moment the badge mounts. */
  live?: boolean;
  /** When true, omits the visible label; aria-label still announces the state. */
  iconOnly?: boolean;
  size?: NBStatusBadgeSize;
  className?: string;
  id?: string;
}

interface StateConfig {
  label: string;
  Icon: React.ElementType;
  bg: string;
  fg: string;
  border: string;
  spin?: boolean;
}

const STATE_CONFIG: Record<NBStatusBadgeState, StateConfig> = {
  queued: {
    label: 'Queued',
    Icon: AccessTimeIcon,
    bg: 'var(--ds-gray-100)',
    fg: 'var(--ds-gray-700)',
    border: 'var(--ds-gray-200)',
  },
  running: {
    label: 'Running',
    Icon: AutorenewIcon,
    bg: 'var(--ds-blue-100)',
    fg: 'var(--ds-blue-700)',
    border: 'var(--ds-blue-200)',
    spin: true,
  },
  paused: {
    label: 'Paused',
    Icon: PauseIcon,
    bg: 'var(--ds-amber-100)',
    fg: 'var(--ds-amber-700)',
    border: 'var(--ds-amber-200)',
  },
  completed: {
    label: 'Completed',
    Icon: CheckIcon,
    bg: 'var(--ds-green-100)',
    fg: 'var(--ds-green-700)',
    border: 'var(--ds-green-200)',
  },
  failed: {
    label: 'Failed',
    Icon: ErrorOutlineIcon,
    bg: 'var(--ds-red-100)',
    fg: 'var(--ds-red-700)',
    border: 'var(--ds-red-200)',
  },
  cancelled: {
    label: 'Cancelled',
    Icon: BlockIcon,
    bg: 'var(--ds-gray-100)',
    fg: 'var(--ds-gray-600)',
    border: 'var(--ds-gray-200)',
  },
};

const SIZE_TOKENS: Record<NBStatusBadgeSize, { height: string; fontSize: string; padX: string; iconSize: number; gap: string }> = {
  sm: { height: '20px', fontSize: 'var(--ds-text-caption)', padX: '6px', iconSize: 10, gap: '4px' },
  md: { height: '24px', fontSize: 'var(--ds-text-small)', padX: '8px', iconSize: 12, gap: '6px' },
};

const SPIN_KEYFRAMES = {
  '@keyframes nb-status-badge-spin': {
    from: { transform: 'rotate(0deg)' },
    to: { transform: 'rotate(360deg)' },
  },
};

function formatElapsed(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const hh = Math.floor(s / 3600);
  const mm = Math.floor((s % 3600) / 60);
  const ss = s % 60;
  const pad = (n: number) => String(n).padStart(2, '0');
  return hh > 0 ? `${hh}:${pad(mm)}:${pad(ss)}` : `${pad(mm)}:${pad(ss)}`;
}

export function NBStatusBadge({ state, timer, live, iconOnly, size = 'md', className, id }: NBStatusBadgeProps) {
  const cfg = STATE_CONFIG[state];
  const tokens = SIZE_TOKENS[size];
  const [, forceTick] = React.useState(0);
  const mountedAtRef = React.useRef<number>(Date.now());
  const baseSecondsRef = React.useRef<number>(parseTimerToSeconds(timer));

  // Reset baseline whenever the user-supplied `timer` changes (the controlling event).
  React.useEffect(() => {
    baseSecondsRef.current = parseTimerToSeconds(timer);
    mountedAtRef.current = Date.now();
  }, [timer]);

  // Spec: `live` keeps the timer ticking on a 1s interval. Only meaningful for running/paused.
  React.useEffect(() => {
    if (!live) return;
    if (state !== 'running' && state !== 'paused') return;
    const i = setInterval(() => forceTick((n) => n + 1), 1000);
    return () => clearInterval(i);
  }, [live, state]);

  const showTimer = (state === 'running' || state === 'paused') && timer !== undefined;
  const elapsedText = showTimer
    ? live
      ? formatElapsed(baseSecondsRef.current + (Date.now() - mountedAtRef.current) / 1000)
      : (timer as string)
    : null;

  const label = iconOnly ? undefined : cfg.label;

  return (
    <Box
      id={id}
      className={className}
      role='status'
      aria-label={iconOnly ? cfg.label : undefined}
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: tokens.gap,
        height: tokens.height,
        padding: iconOnly ? 0 : `0 ${tokens.padX}`,
        width: iconOnly ? tokens.height : undefined,
        justifyContent: 'center',
        fontSize: tokens.fontSize,
        fontWeight: 'var(--ds-font-weight-medium)',
        color: cfg.fg,
        backgroundColor: cfg.bg,
        border: `1px solid ${cfg.border}`,
        borderRadius: 'var(--ds-radius-pill)',
        whiteSpace: 'nowrap',
        ...(cfg.spin ? SPIN_KEYFRAMES : {}),
      }}
    >
      <Box
        component={cfg.Icon as React.ElementType}
        aria-hidden='true'
        sx={{
          fontSize: tokens.iconSize,
          ...(cfg.spin
            ? {
                animation: 'nb-status-badge-spin 1.4s linear infinite',
                '@media (prefers-reduced-motion: reduce)': { animation: 'none' },
              }
            : {}),
        }}
      />
      {label && <Box component='span'>{label}</Box>}
      {elapsedText && (
        <Box component='span' sx={{ fontVariantNumeric: 'tabular-nums', opacity: 0.85 }}>
          · {elapsedText}
        </Box>
      )}
    </Box>
  );
}

function parseTimerToSeconds(timer: string | undefined): number {
  if (!timer) return 0;
  const parts = timer.split(':').map((p) => Number(p));
  if (parts.some((n) => Number.isNaN(n))) return 0;
  if (parts.length === 3) return parts[0] * 3600 + parts[1] * 60 + parts[2];
  if (parts.length === 2) return parts[0] * 60 + parts[1];
  return parts[0] || 0;
}

export default NBStatusBadge;
