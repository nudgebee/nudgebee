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
 *     fold the other into it, or — if it concerns one field — move it next to that
 *     field as a Callout.
 *   - Don't make a Banner dismissible if dismissing it loses the only signal
 *     that something is wrong. Critical-tone Banners are usually non-dismissible.
 *   - Don't put more than two actions in a Banner. Long action lists belong in a Modal.
 *   - Don't use Banner for transient confirmations ("Saved!"). That's a `Toast`.
 */
import * as React from 'react';
import { Box, ButtonBase } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import { Button } from './Button';
import { FEEDBACK_TONES, feedbackRole, resolveFeedbackTone, type FeedbackTone } from './internal/feedbackTones';

export type BannerTone = FeedbackTone;
export type BannerSurface = 'page' | 'section';

export interface BannerAction {
  /** Rendered as the button's `id` and `data-testid`, for automation. */
  id?: string;
  label: React.ReactNode;
  onClick: () => void;
  /** `link` renders as a borderless text button; `primary` is for the banner's one main CTA. Default is a secondary DS `Button`. */
  tone?: 'primary' | 'secondary' | 'link';
}

export type BannerActionsPlacement = 'inline' | 'below';

export interface BannerProps {
  tone: BannerTone;
  /** Optional title rendered in semibold above the message. */
  title?: React.ReactNode;
  message: React.ReactNode;
  /** Up to 2 actions per spec. Three or more renders an internal warning in dev. */
  actions?: BannerAction[];
  /**
   * `below` (default) stacks actions under the message in the content column.
   * `inline` pulls them out as a row-level sibling on the right — use for
   * single-CTA banners where the action reads more naturally beside the text.
   */
  actionsPlacement?: BannerActionsPlacement;
  dismissible?: boolean;
  onDismiss?: () => void;
  surface?: BannerSurface;
  className?: string;
  id?: string;
}

const SURFACE_TOKENS: Record<BannerSurface, { padding: string; radius: string; marginBottom: string }> = {
  page: { padding: 'var(--ds-space-3) var(--ds-space-4)', radius: 'var(--ds-radius-xl)', marginBottom: 'var(--ds-space-4)' },
  section: { padding: 'var(--ds-space-2) var(--ds-space-3)', radius: 'var(--ds-radius-lg)', marginBottom: 'var(--ds-space-3)' },
};

// Deliberately not FEEDBACK_TONES[tone].textColor: that field is tone-colored
// (blue-700/green-700/amber-700/red-700) for Callout's use. On a Banner, a
// tone-colored title/message read as a second severity signal stacked on top
// of the icon and border — the same signal twice. Banner keeps copy neutral
// and leaves tone to the icon/border alone; Callout is unaffected.
const TITLE_COLOR = 'var(--ds-gray-700)';
const MESSAGE_COLOR = 'var(--ds-gray-600)';

export function Banner({
  tone,
  title,
  message,
  actions,
  actionsPlacement = 'below',
  dismissible,
  onDismiss,
  surface = 'page',
  className,
  id,
}: BannerProps) {
  const safeTone = resolveFeedbackTone(tone, 'Banner');
  const cfg = FEEDBACK_TONES[safeTone];
  const surf = SURFACE_TOKENS[surface];
  const Icon = cfg.Icon;

  // Spec: don't put more than two actions. Surface a dev-mode warning, but render anyway.
  if (process.env.NODE_ENV !== 'production' && actions && actions.length > 2) {
    // eslint-disable-next-line no-console
    console.warn('Banner: spec violation — more than 2 actions; long action lists belong in a Modal.');
  }

  const role = feedbackRole(safeTone);

  const actionsNode =
    actions && actions.length > 0 ? (
      <Box
        sx={{
          display: 'flex',
          gap: 'var(--ds-space-2)',
          flexShrink: 0,
          ...(actionsPlacement === 'below' ? { marginTop: 'var(--ds-space-2)' } : null),
        }}
      >
        {actions.map((a, i) => (
          <Button key={i} id={a.id} data-testid={a.id} size='xs' tone={a.tone ?? 'secondary'} onClick={a.onClick}>
            {a.label}
          </Button>
        ))}
      </Box>
    ) : null;

  // Inline actions must not crush the message on a narrow bar: give the content a
  // basis and let the row wrap, so the actions drop to their own line instead.
  const wrapsInlineActions = !!actionsNode && actionsPlacement === 'inline';

  return (
    <Box
      id={id}
      className={className}
      role={role}
      sx={{
        display: 'flex',
        ...(wrapsInlineActions ? { flexWrap: 'wrap' } : null),
        gap: 'var(--ds-space-3)',
        padding: surf.padding,
        backgroundColor: cfg.bg,
        border: `1px solid ${cfg.border}`,
        borderRadius: surf.radius,
        color: MESSAGE_COLOR,
      }}
    >
      <Box component={Icon as React.ElementType} aria-hidden='true' sx={{ fontSize: 18, color: cfg.iconColor, flexShrink: 0 }} />
      <Box sx={{ flex: wrapsInlineActions ? '1 1 240px' : 1, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
        {title && (
          <Box
            component='div'
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontFamily: 'var(--ds-font-display)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: TITLE_COLOR,
              lineHeight: 1.4,
            }}
          >
            {title}
          </Box>
        )}
        <Box component='span' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.2 }}>
          {message}
        </Box>
        {actionsPlacement === 'below' && actionsNode}
      </Box>
      {actionsPlacement === 'inline' && actionsNode}
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
