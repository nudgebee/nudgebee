/**
 * Banner — DS V2 (canonical / new).
 * Spec: app/design-system/primitives/feedback/banner.html
 *
 * Page- or section-level persistent message. One per page, max. Stays visible
 * until the underlying condition clears (or the user dismisses, when permitted).
 * Distinct from `Toast` (transient) and `Callout` (inline-in-flow).
 *
 * Variants per spec:
 *   tone        = 'info' | 'success' | 'warning' | 'critical'
 *   composition = 'icon+message' | 'icon+title+message' | 'icon+title+message+actions'
 *                 | 'icon+message+dismiss'
 *                 (auto from `title` + `actions` + `dismissible` props)
 *   surface     = 'page' | 'section'
 *   dismissible = false | true
 *
 * Don't (per spec):
 *   - Don't render two banners stacked on the same surface. The more severe wins;
 *     the other becomes a Callout in the page body.
 *   - Don't make a Banner dismissible if dismissing it loses the only signal
 *     that something is wrong. Critical-tone Banners are usually non-dismissible.
 *   - Don't put more than two actions in a Banner. Long action lists belong in a Modal.
 *   - Don't use Banner for transient confirmations ("Saved!"). That's a `Toast`.
 */
import * as React from 'react';
import { Box, ButtonBase } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import CloseIcon from '@mui/icons-material/Close';

export type BannerTone = 'info' | 'success' | 'warning' | 'critical';
export type BannerSurface = 'page' | 'section';

export interface BannerAction {
  label: React.ReactNode;
  onClick: () => void;
  /** `link` renders as a borderless text button. Default is secondary-style button. */
  tone?: 'secondary' | 'link';
}

export interface BannerProps {
  tone: BannerTone;
  /** Optional title rendered in semibold above the message. */
  title?: React.ReactNode;
  message: React.ReactNode;
  /** Up to 2 actions per spec. Three or more renders an internal warning in dev. */
  actions?: BannerAction[];
  dismissible?: boolean;
  onDismiss?: () => void;
  surface?: BannerSurface;
  className?: string;
  id?: string;
}

interface ToneTokens {
  bg: string;
  border: string;
  iconColor: string;
  titleColor: string;
  messageColor: string;
  Icon: React.ElementType;
}

const TONE_TOKENS: Record<BannerTone, ToneTokens> = {
  info: {
    bg: 'var(--ds-blue-100)',
    border: 'var(--ds-blue-200)',
    iconColor: 'var(--ds-blue-600)',
    titleColor: 'var(--ds-blue-800)',
    messageColor: 'var(--ds-blue-800)',
    Icon: InfoOutlinedIcon,
  },
  success: {
    bg: 'var(--ds-green-100)',
    border: 'var(--ds-green-200)',
    iconColor: 'var(--ds-green-600)',
    titleColor: 'var(--ds-green-800)',
    messageColor: 'var(--ds-green-800)',
    Icon: CheckCircleOutlineIcon,
  },
  warning: {
    bg: 'var(--ds-amber-100)',
    border: 'var(--ds-amber-200)',
    iconColor: 'var(--ds-amber-700)',
    titleColor: 'var(--ds-amber-800)',
    messageColor: 'var(--ds-amber-800)',
    Icon: WarningAmberIcon,
  },
  critical: {
    bg: 'var(--ds-red-100)',
    border: 'var(--ds-red-200)',
    iconColor: 'var(--ds-red-600)',
    titleColor: 'var(--ds-red-800)',
    messageColor: 'var(--ds-red-800)',
    Icon: ErrorOutlineIcon,
  },
};

const SURFACE_TOKENS: Record<BannerSurface, { padding: string; radius: string; marginBottom: string }> = {
  page: { padding: 'var(--ds-space-3) var(--ds-space-4)', radius: 'var(--ds-radius-md)', marginBottom: 'var(--ds-space-4)' },
  section: { padding: 'var(--ds-space-2) var(--ds-space-3)', radius: 'var(--ds-radius-sm)', marginBottom: 'var(--ds-space-3)' },
};

export function Banner({ tone, title, message, actions, dismissible, onDismiss, surface = 'page', className, id }: BannerProps) {
  const cfg = TONE_TOKENS[tone];
  const surf = SURFACE_TOKENS[surface];
  const Icon = cfg.Icon;

  // Spec: don't put more than two actions. Surface a dev-mode warning, but render anyway.
  if (process.env.NODE_ENV !== 'production' && actions && actions.length > 2) {
    // eslint-disable-next-line no-console
    console.warn('Banner: spec violation — more than 2 actions; long action lists belong in a Modal.');
  }

  const role = tone === 'critical' || tone === 'warning' ? 'alert' : 'status';

  return (
    <Box
      id={id}
      className={className}
      role={role}
      aria-live={tone === 'critical' ? 'assertive' : 'polite'}
      sx={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 'var(--ds-space-3)',
        padding: surf.padding,
        marginBottom: surf.marginBottom,
        backgroundColor: cfg.bg,
        border: `1px solid ${cfg.border}`,
        borderRadius: surf.radius,
        color: cfg.messageColor,
      }}
    >
      <Box component={Icon as React.ElementType} aria-hidden='true' sx={{ fontSize: 18, color: cfg.iconColor, flexShrink: 0, marginTop: '2px' }} />
      <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
        {title && (
          <Box
            component='div'
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: cfg.titleColor,
            }}
          >
            {title}
          </Box>
        )}
        <Box component='span' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.5 }}>
          {message}
        </Box>
        {actions && actions.length > 0 && (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', marginTop: 'var(--ds-space-2)' }}>
            {actions.map((a, i) => (
              <ButtonBase
                key={i}
                type='button'
                onClick={a.onClick}
                sx={
                  a.tone === 'link'
                    ? {
                        fontSize: 'var(--ds-text-small)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: cfg.titleColor,
                        textDecoration: 'underline',
                        padding: 0,
                        '&:hover': { textDecoration: 'none' },
                        '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '2px' },
                      }
                    : {
                        height: '24px',
                        padding: '0 10px',
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: cfg.titleColor,
                        backgroundColor: 'var(--ds-background-100)',
                        border: `1px solid ${cfg.border}`,
                        borderRadius: 'var(--ds-radius-sm)',
                        '&:hover': { backgroundColor: 'var(--ds-background-200)' },
                        '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
                      }
                }
              >
                {a.label}
              </ButtonBase>
            ))}
          </Box>
        )}
      </Box>
      {dismissible && (
        <ButtonBase
          aria-label='Dismiss'
          onClick={onDismiss}
          sx={{
            width: '20px',
            height: '20px',
            borderRadius: 'var(--ds-radius-sm)',
            color: cfg.iconColor,
            flexShrink: 0,
            marginTop: '1px',
            '&:hover': { backgroundColor: 'var(--ds-gray-alpha-200)' },
            '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
          }}
        >
          <CloseIcon sx={{ fontSize: 14 }} />
        </ButtonBase>
      )}
    </Box>
  );
}

export default Banner;
