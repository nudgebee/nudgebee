import React, { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Box } from '@mui/material';
import CloseRoundedIcon from '@mui/icons-material/CloseRounded';
import CheckCircleOutlineRoundedIcon from '@mui/icons-material/CheckCircleOutlineRounded';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import WarningAmberRoundedIcon from '@mui/icons-material/WarningAmberRounded';
import ErrorOutlineRoundedIcon from '@mui/icons-material/ErrorOutlineRounded';

import { snackbar as newSnackbar, type SnackbarOptions, type SnackbarSeverity } from './snackbarService';
import { snackbar as legacySnackbar } from '@components1/common/snackbarService';
import { ds } from '@utils/colors';

const MAX_VISIBLE = 3;
const EXIT_ANIMATION_MS = 150;

const SEVERITY_DEFAULT_DURATION: Record<SnackbarSeverity, number> = {
  success: 3000,
  info: 4000,
  warning: 5000,
  error: 6000,
};

interface SeverityTokens {
  stripeColor: string;
  stripeWidthPx: number;
  cardBg: string;
  cardBorder: string;
  iconColor: string;
  textColor: string;
  closeColor: string;
  progressBar: string;
  shadow: string;
  hoverShadow: string;
  Icon: React.ComponentType<{ sx?: object }>;
}

const NEUTRAL_SHADOW = '0 8px 24px rgba(15, 23, 42, 0.10), 0 2px 4px rgba(15, 23, 42, 0.05)';
const NEUTRAL_HOVER_SHADOW = '0 12px 32px rgba(15, 23, 42, 0.14), 0 4px 8px rgba(15, 23, 42, 0.07)';

const SEVERITY_TOKENS: Record<SnackbarSeverity, SeverityTokens> = {
  // Success: green pastel card with green icon (severity identity) + blue-700 text & close (per design choice).
  success: {
    stripeColor: 'var(--ds-green-500)',
    stripeWidthPx: 3,
    cardBg: 'var(--ds-green-100)',
    cardBorder: 'var(--ds-green-200)',
    iconColor: 'var(--ds-green-700)',
    textColor: 'var(--ds-blue-700)',
    closeColor: 'var(--ds-blue-700)',
    progressBar: 'var(--ds-green-500)',
    shadow: NEUTRAL_SHADOW,
    hoverShadow: NEUTRAL_HOVER_SHADOW,
    Icon: CheckCircleOutlineRoundedIcon,
  },
  info: {
    stripeColor: 'var(--ds-blue-500)',
    stripeWidthPx: 3,
    cardBg: 'var(--ds-blue-100)',
    cardBorder: 'var(--ds-blue-200)',
    iconColor: 'var(--ds-blue-700)',
    textColor: 'var(--ds-blue-700)',
    closeColor: 'var(--ds-blue-700)',
    progressBar: 'var(--ds-blue-500)',
    shadow: NEUTRAL_SHADOW,
    hoverShadow: NEUTRAL_HOVER_SHADOW,
    Icon: InfoOutlinedIcon,
  },
  warning: {
    stripeColor: 'var(--ds-amber-500)',
    stripeWidthPx: 3,
    cardBg: 'var(--ds-amber-100)',
    cardBorder: 'var(--ds-amber-200)',
    iconColor: 'var(--ds-amber-700)',
    textColor: 'var(--ds-amber-700)',
    closeColor: 'var(--ds-amber-700)',
    progressBar: 'var(--ds-amber-500)',
    shadow: NEUTRAL_SHADOW,
    hoverShadow: NEUTRAL_HOVER_SHADOW,
    Icon: WarningAmberRoundedIcon,
  },
  error: {
    stripeColor: 'var(--ds-red-500)',
    stripeWidthPx: 5,
    cardBg: 'var(--ds-red-100)',
    cardBorder: 'var(--ds-red-200)',
    iconColor: 'var(--ds-red-700)',
    textColor: 'var(--ds-red-700)',
    closeColor: 'var(--ds-red-700)',
    progressBar: 'var(--ds-red-500)',
    shadow: '0 8px 24px rgba(220, 38, 38, 0.10), 0 2px 4px rgba(220, 38, 38, 0.05)',
    hoverShadow: '0 12px 32px rgba(220, 38, 38, 0.14), 0 4px 8px rgba(220, 38, 38, 0.07)',
    Icon: ErrorOutlineRoundedIcon,
  },
};

interface ToastEntry {
  id: number;
  message: ReactNode;
  severity: SnackbarSeverity;
  duration: number;
}

let nextToastId = 1;

export function SnackbarComponent() {
  const [toasts, setToasts] = useState<ToastEntry[]>([]);

  const addToast = useCallback((options: SnackbarOptions) => {
    const id = nextToastId++;
    const resolvedDuration = options.duration !== undefined ? options.duration : SEVERITY_DEFAULT_DURATION[options.severity];

    setToasts((prev) => {
      const next: ToastEntry[] = [{ id, message: options.message, severity: options.severity, duration: resolvedDuration }, ...prev];
      return next.slice(0, MAX_VISIBLE);
    });
  }, []);

  const removeToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  useEffect(() => {
    const u1 = newSnackbar.subscribe(addToast);
    const u2 = legacySnackbar.subscribe(addToast);
    return () => {
      u1();
      u2();
    };
  }, [addToast]);

  if (toasts.length === 0) return null;

  return (
    <Box
      role='region'
      aria-label='Notifications'
      sx={{
        position: 'fixed',
        top: 'var(--ds-space-5)',
        right: 'var(--ds-space-5)',
        zIndex: 1500,
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[3],
        pointerEvents: 'none',
        alignItems: 'center',
      }}
    >
      {toasts.map((toast) => (
        <ToastItem key={toast.id} toast={toast} onDismiss={() => removeToast(toast.id)} />
      ))}
    </Box>
  );
}

interface ToastItemProps {
  toast: ToastEntry;
  onDismiss: () => void;
}

function ToastItem({ toast, onDismiss }: ToastItemProps) {
  const tokens = SEVERITY_TOKENS[toast.severity];
  const isError = toast.severity === 'error';
  const Icon = tokens.Icon;

  const [paused, setPaused] = useState(false);
  const [exiting, setExiting] = useState(false);

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const remainingRef = useRef<number>(toast.duration);
  const lastResumeRef = useRef<number>(Date.now());

  const handleDismiss = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    setExiting(true);
    setTimeout(onDismiss, EXIT_ANIMATION_MS);
  }, [onDismiss]);

  // Keep a ref so the timer effect can call the latest handleDismiss without
  // needing it as a dependency (parent re-renders create new onDismiss lambdas
  // which would otherwise reset every toast's timer on each state change).
  const handleDismissRef = useRef(handleDismiss);
  handleDismissRef.current = handleDismiss;

  useEffect(() => {
    if (paused) {
      // Pausing: cancel timer and decrement remaining.
      if (timerRef.current) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
        remainingRef.current = Math.max(0, remainingRef.current - (Date.now() - lastResumeRef.current));
      }
      return;
    }
    // Running: schedule timer for remaining time.
    if (remainingRef.current > 0) {
      lastResumeRef.current = Date.now();
      timerRef.current = setTimeout(() => handleDismissRef.current(), remainingRef.current);
    }
    return () => {
      if (timerRef.current) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [paused]); // handleDismiss intentionally omitted — accessed via ref

  return (
    <Box
      role={isError ? 'alert' : 'status'}
      aria-live={isError ? 'assertive' : 'polite'}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      sx={{
        pointerEvents: 'auto',
        position: 'relative',
        minWidth: ds.space.mul(0, 160),
        maxWidth: ds.space.mul(0, 220),
        backgroundColor: tokens.cardBg,
        border: `1px solid ${tokens.cardBorder}`,
        borderLeft: `${tokens.stripeWidthPx}px solid ${tokens.stripeColor}`,
        borderBottom: 'none',
        borderRadius: 'var(--ds-radius-xl)',
        boxShadow: tokens.shadow,
        overflow: 'hidden',
        display: 'grid',
        gridTemplateColumns: 'auto 1fr auto',
        alignItems: 'flex-start',
        columnGap: 'var(--ds-space-3)',
        padding: 'var(--ds-space-3) var(--ds-space-3)',
        opacity: exiting ? 0 : 1,
        transform: exiting ? 'translateX(20px)' : 'translateX(0)',
        animation: exiting ? 'none' : 'ds-toast-enter 200ms cubic-bezier(0.16, 1, 0.3, 1)',
        transition: 'opacity 150ms ease-in, transform 150ms ease-in, box-shadow 200ms ease',
        '&:hover': {
          boxShadow: tokens.hoverShadow,
        },
        '@keyframes ds-toast-enter': {
          from: { opacity: 0, transform: 'translateY(-8px) scale(0.96)' },
          to: { opacity: 1, transform: 'translateY(0) scale(1)' },
        },
        '@media (prefers-reduced-motion: reduce)': {
          animation: 'none',
          transition: 'opacity 120ms',
          transform: 'none',
        },
      }}
    >
      {/* Bare icon glyph (no surrounding circle — card itself is now tinted) */}
      <Icon
        aria-hidden='true'
        sx={{
          fontSize: 22,
          color: tokens.iconColor,
          flexShrink: 0,
          marginTop: 'var(--ds-space-1)',
        }}
      />

      {/* Message */}
      <Box
        sx={{
          fontSize: 'var(--ds-text-body)',
          color: tokens.textColor,
          fontWeight: 'var(--ds-font-weight-semibold)',
          lineHeight: 1.45,
          wordBreak: 'break-word',
          paddingTop: 'var(--ds-space-1)',
          paddingBottom: 'var(--ds-space-1)',
        }}
      >
        {toast.message}
      </Box>

      {/* Close (always required) */}
      <Box
        component='button'
        type='button'
        onClick={handleDismiss}
        aria-label='Dismiss notification'
        sx={{
          width: ds.space.mul(0, 11),
          height: ds.space.mul(0, 11),
          padding: 0,
          background: 'transparent',
          border: 0,
          borderRadius: 'var(--ds-radius-sm)',
          color: tokens.closeColor,
          cursor: 'pointer',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          opacity: 0.7,
          transition: 'all 120ms ease',
          flexShrink: 0,
          marginTop: 'var(--ds-space-1)',
          '&:hover': {
            opacity: 1,
            backgroundColor: 'rgba(0, 0, 0, 0.06)',
          },
          '&:focus-visible': {
            outline: `2px solid ${tokens.closeColor}`,
            outlineOffset: '1px',
            opacity: 1,
          },
        }}
      >
        <CloseRoundedIcon sx={{ fontSize: 16 }} />
      </Box>

      {/* Progress bar (non-error, non-persistent only) */}
      {toast.duration > 0 && (
        <Box
          aria-hidden='true'
          sx={{
            position: 'absolute',
            left: 0,
            right: 0,
            bottom: 0,
            height: ds.space[0],
            backgroundColor: tokens.progressBar,
            transformOrigin: 'left',
            animation: `ds-toast-progress ${toast.duration}ms linear forwards`,
            animationPlayState: paused ? 'paused' : 'running',
            '@keyframes ds-toast-progress': {
              from: { transform: 'scaleX(1)' },
              to: { transform: 'scaleX(0)' },
            },
            '@media (prefers-reduced-motion: reduce)': {
              animation: 'none',
              transform: 'scaleX(0)',
            },
          }}
        />
      )}
    </Box>
  );
}

export default SnackbarComponent;
