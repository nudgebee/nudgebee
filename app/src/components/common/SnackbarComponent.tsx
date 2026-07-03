import React, { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { Box } from '@mui/material';
import CloseRoundedIcon from '@mui/icons-material/CloseRounded';
import CheckCircleRoundedIcon from '@mui/icons-material/CheckCircleRounded';
import InfoRoundedIcon from '@mui/icons-material/InfoRounded';
import WarningRoundedIcon from '@mui/icons-material/WarningRounded';
import ErrorRoundedIcon from '@mui/icons-material/ErrorRounded';

import { snackbar as newSnackbar, type SnackbarAction, type SnackbarOptions, type SnackbarSeverity } from './snackbarService';
import { ds } from '@utils/colors';
import { Button } from '@ui/Button';

const MAX_VISIBLE = 6;
const EXIT_ANIMATION_MS = 380;
const REPOSITION_MS = 460;
const REPOSITION_EASE = 'cubic-bezier(0.16, 1, 0.3, 1)';

const prefersReducedMotion = () => typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

const SEVERITY_DEFAULT_DURATION: Record<SnackbarSeverity, number> = {
  default: 4000,
  success: 3000,
  info: 4000,
  warning: 5000,
  error: 8000,
};

interface SeverityTokens {
  cardBg: string;
  cardBorder: string;
  iconColor: string;
  textColor: string;
  closeColor: string;
  shadow: string;
  hoverShadow: string;
  Icon: React.ComponentType<{ sx?: object }> | null;
}

const NEUTRAL_SHADOW = `0 ${ds.space[2]} 24px color-mix(in srgb, ${ds.gray[700]} 10%, transparent), 0 ${ds.space[0]} ${ds.space[1]} color-mix(in srgb, ${ds.gray[700]} 5%, transparent)`;
const NEUTRAL_HOVER_SHADOW = `0 12px 32px color-mix(in srgb, ${ds.gray[700]} 14%, transparent), 0 ${ds.space[1]} ${ds.space[2]} color-mix(in srgb, ${ds.gray[700]} 7%, transparent)`;

const SEVERITY_TOKENS: Record<SnackbarSeverity, SeverityTokens> = {
  // Default: plain white card for neutral text — no severity icon.
  default: {
    cardBg: 'var(--ds-background-100)',
    cardBorder: 'var(--ds-gray-200)',
    iconColor: 'var(--ds-gray-500)',
    textColor: 'var(--ds-gray-700)',
    closeColor: 'var(--ds-gray-600)',
    shadow: NEUTRAL_SHADOW,
    hoverShadow: NEUTRAL_HOVER_SHADOW,
    Icon: null,
  },
  // Success: green pastel card with green icon (severity identity) + blue-700 text & close (per design choice).
  success: {
    cardBg: 'var(--ds-green-100)',
    cardBorder: 'var(--ds-green-300)',
    iconColor: 'var(--ds-green-600)',
    textColor: 'var(--ds-gray-700)',
    closeColor: 'var(--ds-gray-600)',
    shadow: NEUTRAL_SHADOW,
    hoverShadow: NEUTRAL_HOVER_SHADOW,
    Icon: CheckCircleRoundedIcon,
  },
  info: {
    cardBg: 'var(--ds-blue-100)',
    cardBorder: 'var(--ds-blue-300)',
    iconColor: 'var(--ds-blue-600)',
    textColor: 'var(--ds-gray-700)',
    closeColor: 'var(--ds-blue-700)',
    shadow: NEUTRAL_SHADOW,
    hoverShadow: NEUTRAL_HOVER_SHADOW,
    Icon: InfoRoundedIcon,
  },
  warning: {
    cardBg: 'var(--ds-amber-100)',
    cardBorder: 'var(--ds-amber-300)',
    iconColor: 'var(--ds-amber-700)',
    textColor: 'var(--ds-amber-700)',
    closeColor: 'var(--ds-amber-700)',
    shadow: NEUTRAL_SHADOW,
    hoverShadow: NEUTRAL_HOVER_SHADOW,
    Icon: WarningRoundedIcon,
  },
  error: {
    cardBg: 'var(--ds-red-100)',
    cardBorder: 'var(--ds-red-300)',
    iconColor: 'var(--ds-red-600)',
    textColor: 'var(--ds-red-700)',
    closeColor: 'var(--ds-red-700)',
    shadow: `0 ${ds.space[2]} 24px color-mix(in srgb, ${ds.red[500]} 10%, transparent), 0 ${ds.space[0]} ${ds.space[1]} color-mix(in srgb, ${ds.red[500]} 5%, transparent)`,
    hoverShadow: `0 12px 32px color-mix(in srgb, ${ds.red[500]} 14%, transparent), 0 ${ds.space[1]} ${ds.space[2]} color-mix(in srgb, ${ds.red[500]} 7%, transparent)`,
    Icon: ErrorRoundedIcon,
  },
};

interface ToastEntry {
  id: number;
  message: ReactNode;
  severity: SnackbarSeverity;
  duration: number;
  description?: ReactNode;
  action?: SnackbarAction;
}

let nextToastId = 1;

export function SnackbarComponent() {
  const [toasts, setToasts] = useState<ToastEntry[]>([]);
  // FLIP bookkeeping: the outer DOM node of each toast, and its last-known top.
  const itemRefs = useRef<Map<number, HTMLDivElement>>(new Map());
  const prevTops = useRef<Map<number, number>>(new Map());

  const addToast = useCallback((options: SnackbarOptions) => {
    const id = nextToastId++;
    const resolvedDuration = options.duration !== undefined ? options.duration : SEVERITY_DEFAULT_DURATION[options.severity];

    setToasts((prev) => {
      const next: ToastEntry[] = [
        {
          id,
          message: options.message,
          severity: options.severity,
          duration: resolvedDuration,
          description: options.description,
          action: options.action,
        },
        ...prev,
      ];
      return next.slice(0, MAX_VISIBLE);
    });
  }, []);

  const removeToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  useEffect(() => {
    const u1 = newSnackbar.subscribe(addToast);
    return () => {
      u1();
    };
  }, [addToast]);

  useLayoutEffect(() => {
    const reduce = prefersReducedMotion();
    const newTops = new Map<number, number>();
    itemRefs.current.forEach((el, id) => newTops.set(id, el.getBoundingClientRect().top));

    if (!reduce) {
      newTops.forEach((newTop, id) => {
        const oldTop = prevTops.current.get(id);
        if (oldTop === undefined) return; // newly mounted → handled by enter animation
        const delta = oldTop - newTop;
        if (!delta) return;
        const el = itemRefs.current.get(id);
        if (!el) return;
        el.style.transition = 'none';
        el.style.transform = `translateY(${delta}px)`;
        // Force the jump to commit before we animate back on the next frame.
        void el.getBoundingClientRect();
        requestAnimationFrame(() => {
          el.style.transition = `transform ${REPOSITION_MS}ms ${REPOSITION_EASE}`;
          el.style.transform = '';
        });
      });
    }
    prevTops.current = newTops;
  }, [toasts]);

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
        pointerEvents: 'none',
        alignItems: 'center',
      }}
    >
      {toasts.map((toast) => (
        <ToastItem
          key={toast.id}
          toast={toast}
          onDismiss={() => removeToast(toast.id)}
          registerRef={(el) => {
            if (el) itemRefs.current.set(toast.id, el);
            else itemRefs.current.delete(toast.id);
          }}
        />
      ))}
    </Box>
  );
}

interface ToastItemProps {
  toast: ToastEntry;
  onDismiss: () => void;
  registerRef: (el: HTMLDivElement | null) => void;
}

function ToastItem({ toast, onDismiss, registerRef }: ToastItemProps) {
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

  // Run the action, then dismiss the toast — an action button resolves the toast.
  const handleAction = () => {
    toast.action?.onClick();
    handleDismiss();
  };

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
    <Box ref={registerRef} sx={{ pointerEvents: 'none', marginBottom: ds.space[3] }}>
      <Box
        role={isError ? 'alert' : 'status'}
        aria-live={isError ? 'assertive' : 'polite'}
        onMouseEnter={() => setPaused(true)}
        onMouseLeave={() => setPaused(false)}
        sx={{
          pointerEvents: 'auto',
          position: 'relative',
          width: ds.space.mul(0, 160),
          backgroundColor: tokens.cardBg,
          border: `1px solid ${tokens.cardBorder}`,
          borderRadius: 'var(--ds-radius-xl)',
          boxShadow: tokens.shadow,
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--ds-space-2)',
          padding: 'var(--ds-space-4) var(--ds-space-4)',
          opacity: exiting ? 0 : 1,
          transform: exiting ? 'translateY(-40px)' : 'translateY(0)',
          animation: exiting ? 'none' : 'ds-toast-enter 520ms cubic-bezier(0.16, 1, 0.3, 1)',
          transition: exiting
            ? `opacity ${EXIT_ANIMATION_MS}ms ease, transform ${EXIT_ANIMATION_MS}ms cubic-bezier(0.7, 0, 0.84, 1)`
            : 'box-shadow 200ms ease',
          '&:hover': {
            boxShadow: tokens.hoverShadow,
          },
          '@keyframes ds-toast-enter': {
            '0%': { opacity: 0, transform: 'translateY(-40px) scale(0.8)' },
            '100%': { opacity: 1, transform: 'translateY(0) scale(1)' },
          },
          '@media (prefers-reduced-motion: reduce)': {
            animation: 'none',
            transition: 'opacity 120ms',
            transform: 'none',
          },
        }}
      >
        {/* Severity icon glyph — omitted for the plain `default` variant. */}
        {Icon && (
          <Icon
            aria-hidden='true'
            sx={{
              fontSize: 20,
              color: tokens.iconColor,
              flexShrink: 0,
              marginTop: 'var(--ds-space-1)',
            }}
          />
        )}

        {/* Message + optional description */}
        <Box sx={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
          <Box
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-small)',
              color: tokens.textColor,
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: 1.7,
              wordBreak: 'break-word',
              paddingTop: 'var(--ds-space-1)',
              paddingBottom: toast.description ? 0 : 'var(--ds-space-1)',
            }}
          >
            {toast.message}
          </Box>

          {/* Description — one size smaller (caption), gray, regular weight. */}
          {toast.description && (
            <Box
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-500)',
                fontWeight: 'var(--ds-font-weight-regular)',
                lineHeight: 1.4,
                wordBreak: 'break-word',
                paddingBottom: 'var(--ds-space-1)',
              }}
            >
              {toast.description}
            </Box>
          )}
        </Box>

        {/* Action button (secondary tone), rendered before close. */}
        {toast.action && (
          <Box sx={{ flexShrink: 0, alignSelf: 'center' }}>
            <Button tone='secondary' size='sm' onClick={handleAction}>
              {toast.action.label}
            </Button>
          </Box>
        )}

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
              backgroundColor: ds.gray.alpha[100],
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
      </Box>
    </Box>
  );
}

export default SnackbarComponent;
