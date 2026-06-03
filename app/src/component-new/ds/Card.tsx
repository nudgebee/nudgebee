/**
 * Card — DS V2 unified card surface primitive.
 * Spec: design-system/primitives/layout/card.html
 * Original proposal: design-system/proposals/card-consolidation.html
 *
 * Consolidates WidgetCard + CustomBorderCard into a single primitive with
 * three independent axes: `variant`, `size`, `elevation`. Visual baseline
 * (size="md" + elevation="raised") matches @common-new/WidgetCard exactly —
 * same border, radius, padding tokens, and shadow value.
 *
 * Distinct from:
 *   - CollapsableCard  — disclosure behavior; composes Card for the surface.
 *   - FormCard         — form-shaped semantic wrapper; composes Card.
 *   - Banner           — tinted background for messages; Card never tints bg.
 *   - DiffCard         — agentic specialised content.
 *
 * Variants:
 *   variant      = 'elevated' | 'outlined' | 'accent' | 'tinted'
 *                  elevated → border + shadow (default)
 *                  outlined → heavier border, no extra emphasis
 *                  accent   → 3px coloured left border driven by `tone`
 *                  tinted   → coloured background + matching border (no shadow),
 *                             driven by `tone`. Use for nested form panels,
 *                             callout containers grouping related fields, or
 *                             subtle visual grouping inside dense surfaces
 *                             (e.g. modal bodies). `tone='neutral'` is the
 *                             canonical "light-gray panel" look.
 *   size         = 'sm' | 'md' | 'lg'
 *                  Drives padding AND default shadow. md is today's WidgetCard.
 *   elevation    = 'raised' | 'flat'
 *                  Default 'raised' (all sizes ship with a shadow).
 *                  Use 'flat' when nested inside Modal / Inspector / Drawer
 *                  where stacking shadows would compound visual weight.
 *                  Tinted variant is always flat (shadow on tinted bg looks heavy).
 *   tone         = 'neutral' | 'info' | 'success' | 'warning' | 'danger'
 *                  Meaningful when variant='accent' (left-border colour) OR
 *                  variant='tinted' (bg + border colour). Dev-warns otherwise.
 *                  Tinted tone → bg mapping:
 *                    neutral → gray-100 (the gray-panel default)
 *                    info    → blue-100
 *                    success → green-100
 *                    warning → amber-100
 *                    danger  → red-100
 *   interactive  = boolean
 *                  Adds role="button", keyboard activation, focus-visible ring,
 *                  hover lift. Required when onClick is set; dev-warns otherwise.
 *   selected     = boolean
 *                  2px brand border for picker-row use. Interaction state,
 *                  not semantic colour.
 *
 * Slots (props):
 *   header   — ReactNode rendered above body with a 1px divider when children
 *              are present. Card does NOT impose flex layout on header content —
 *              consumers wrap in their own flex container if they need
 *              title-on-left / actions-on-right.
 *   footer   — ReactNode rendered below body with a 1px divider. Same layout
 *              rule as header — consumer controls alignment (left for "View all"
 *              links, right for form action buttons, etc.).
 *   children — body content.
 *
 * Don't:
 *   - Don't tint the background by hand (raw `sx={{ backgroundColor }}`) — use
 *     `variant='tinted'` + `tone`. For message-shaped surfaces (status + icon),
 *     reach for Banner instead of a tinted Card.
 *   - Don't nest Cards more than one level deep — the inner should be a Section header.
 *   - Don't use sx for padding/shadow overrides. Use size and elevation instead.
 *   - Don't pass tone without variant="accent" or variant="tinted".
 *   - Don't pass onClick without interactive=true (dev-warns; keyboard would be broken).
 */
import * as React from 'react';
import { Box, SxProps, Theme } from '@mui/material';

export type CardVariant = 'elevated' | 'outlined' | 'accent' | 'tinted';
export type CardSize = 'sm' | 'md' | 'lg';
export type CardElevation = 'raised' | 'flat';
export type CardTone = 'neutral' | 'info' | 'success' | 'warning' | 'danger';

export interface CardProps {
  variant?: CardVariant;
  size?: CardSize;
  elevation?: CardElevation;
  tone?: CardTone;
  interactive?: boolean;
  selected?: boolean;

  header?: React.ReactNode;
  footer?: React.ReactNode;
  children?: React.ReactNode;

  onClick?: (e: React.MouseEvent<HTMLDivElement>) => void;

  id?: string;
  className?: string;
  'data-testid'?: string;
  'aria-label'?: string;

  /** Escape hatch — do not use for variant-equivalent changes. */
  sx?: SxProps<Theme>;
}

/**
 * Padding scale anchored on the production WidgetCard (20px 24px) as `md`.
 * Each size keeps the vertical-narrower-than-horizontal rhythm.
 * 20px / 28px are literal (no matching --ds-space-* token).
 */
const SIZE_TOKENS: Record<CardSize, { padding: string; shadow: string }> = {
  sm: {
    padding: 'var(--ds-space-3) var(--ds-space-4)', // 12px 16px
    shadow: '0 1px 6px rgba(0, 0, 0, 0.06)',
  },
  md: {
    padding: '20px var(--ds-space-5)', // 20px 24px — matches WidgetCard
    shadow: '0 1px 10px rgba(0, 0, 0, 0.08)',
  },
  lg: {
    padding: '28px var(--ds-space-6)', // 28px 32px
    shadow: '0 2px 16px rgba(0, 0, 0, 0.10)',
  },
};

const ACCENT_TONE_COLOR: Record<CardTone, string> = {
  neutral: 'var(--ds-gray-300)',
  info: 'var(--ds-blue-500)',
  success: 'var(--ds-green-500)',
  warning: 'var(--ds-amber-500)',
  danger: 'var(--ds-red-500)',
};

// Background + border for variant='tinted'. Mirrors the soft-100/200 bg+border
// pairs used by Chip and Banner so a tinted Card visually relates to a same-tone
// chip/banner placed near it.
const TINTED_BG: Record<CardTone, string> = {
  neutral: 'var(--ds-background-300)',
  info: 'var(--ds-blue-100)',
  success: 'var(--ds-green-100)',
  warning: 'var(--ds-amber-100)',
  danger: 'var(--ds-red-100)',
};

const TINTED_BORDER: Record<CardTone, string> = {
  neutral: 'var(--ds-gray-200)',
  info: 'var(--ds-blue-200)',
  success: 'var(--ds-green-200)',
  warning: 'var(--ds-amber-200)',
  danger: 'var(--ds-red-200)',
};

const DIVIDER = '1px solid var(--ds-gray-200)';

export const Card: React.FC<CardProps> = ({
  variant = 'elevated',
  size = 'md',
  elevation = 'raised',
  tone = 'neutral',
  interactive = false,
  selected = false,
  header,
  footer,
  children,
  onClick,
  id,
  className,
  sx,
  ...rest
}) => {
  if (process.env.NODE_ENV !== 'production') {
    if (tone !== 'neutral' && variant !== 'accent' && variant !== 'tinted') {
      // eslint-disable-next-line no-console
      console.warn(`[Card] tone="${tone}" is only meaningful when variant="accent" or variant="tinted" (got variant="${variant}"). Ignoring tone.`);
    }
    if (onClick && !interactive) {
      // eslint-disable-next-line no-console
      console.warn('[Card] onClick provided without interactive=true. Set interactive to add role="button" and keyboard support.');
    }
  }

  const { padding, shadow } = SIZE_TOKENS[size];

  const isOutlined = variant === 'outlined';
  const isAccent = variant === 'accent';
  const isTinted = variant === 'tinted';

  const showShadow = elevation === 'raised' && !isTinted;

  const borderWidth = selected ? 2 : isOutlined ? 1 : 1;
  const defaultBorderColor = selected ? 'var(--ds-blue-500)' : isTinted ? TINTED_BORDER[tone] : 'var(--ds-gray-200)';
  const borderColor = defaultBorderColor;

  const handleKeyDown =
    interactive && onClick
      ? (e: React.KeyboardEvent<HTMLDivElement>) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onClick(e as unknown as React.MouseEvent<HTMLDivElement>);
          }
        }
      : undefined;

  return (
    <Box
      id={id}
      className={className}
      onClick={interactive ? onClick : undefined}
      onKeyDown={handleKeyDown}
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      sx={{
        backgroundColor: isTinted ? TINTED_BG[tone] : 'var(--ds-background-100)',
        border: `${borderWidth}px solid ${borderColor}`,
        borderRadius: 'var(--ds-radius-xl)',
        padding,
        boxShadow: showShadow ? shadow : 'none',
        ...(isAccent && {
          borderLeft: `3px solid ${ACCENT_TONE_COLOR[tone]}`,
        }),
        ...(isOutlined && {
          borderColor: selected ? 'var(--ds-blue-500)' : 'var(--ds-gray-300)',
        }),
        ...(interactive && {
          cursor: 'pointer',
          transition: 'box-shadow 120ms ease, transform 120ms ease, border-color 120ms ease',
          '&:hover': {
            boxShadow: '0 4px 18px rgba(0, 0, 0, 0.10)',
            transform: 'translateY(-1px)',
            borderColor: selected ? 'var(--ds-blue-500)' : 'var(--ds-gray-300)',
          },
          '&:focus-visible': {
            outline: '2px solid var(--ds-blue-500)',
            outlineOffset: 2,
          },
        }),
        ...sx,
      }}
      {...rest}
    >
      {header && (
        <Box
          sx={{
            ...(children != null && {
              paddingBottom: 'var(--ds-space-3)',
              borderBottom: DIVIDER,
              marginBottom: 'var(--ds-space-3)',
            }),
          }}
        >
          {header}
        </Box>
      )}
      {children}
      {footer && (
        <Box
          sx={{
            paddingTop: 'var(--ds-space-3)',
            borderTop: DIVIDER,
            marginTop: 'var(--ds-space-3)',
          }}
        >
          {footer}
        </Box>
      )}
    </Box>
  );
};

export default Card;
