/**
 * Button — DS V2. Replaces 11 legacy buttons.
 * Spec:        app/design-system/primitives/action/button.html
 * Variants:    size = 'xs' | 'sm' | 'md' | 'lg'
 *              tone = 'primary' | 'secondary' | 'ghost' | 'danger' | 'link'
 *              composition = 'text' | 'icon+text' | 'text+icon' | 'icon-only' (auto from props)
 *              state: default | hover | active | focus | loading | disabled
 *
 * Tone resolution (post 2026-05-09 brand-navy migration):
 *   - primary   — brand navy. rest=--ds-brand-600 (the brand colour exactly),
 *                 hover=--ds-brand-500, active=--ds-brand-700.
 *   - secondary — white bg + brand-600 text + brand-200 border. Hover tints to
 *                 brand-100 — secondary buttons feel like "lighter primary".
 *   - ghost     — transparent + brand-600 text. Per-row icon-only ghosts (Ask
 *                 Nubi, kebab, share, download) inherit this — they read as
 *                 brand-toned interactive marks.
 *   - danger    — stays on --ds-red-*. Destructive never absorbs brand.
 *   - link      — stays on --ds-blue-600 (per Q1 — links rely on convention).
 *   - focus     — outline uses --ds-yellow-500 (Nudgebee Yellow). Keyboard
 *                 signal is unmistakable on both navy and white surfaces.
 *
 * Trailing accent (added 2026-05-09):
 *   `<Button trailingAccent={<ArrowForward/>}>Optimize</Button>` renders a
 *   yellow-tile (--ds-yellow-500 bg, --ds-brand-700 ink) at the right edge.
 *   Reserved for *the* page CTA. Don't combine with composition='icon-only'
 *   or tone='link'. Don't use for ambient secondary actions.
 *
 * Migration:
 *   `import CustomButton from '@common/NewCustomButton'`        → `import { Button } from '@components1/ds/Button'`
 *   `import CustomIconButton from '@components1/CustomIconButton'`
 *                                                               → `<Button composition="icon-only" />`
 *   `import CopyButton from '@common/CopyButton'`               → `<Button tone="ghost" composition="icon-only" icon={<ContentCopy/>} aria-label="Copy" />`
 *   `import ShareButton from '@common/ShareButton'`             → `<Button tone="ghost" composition="icon-only" icon={<Share/>} aria-label="Share" />`
 *   `import InvestigateButton from '@common/InvestigateButton'` → `<Button tone="ghost" composition="icon-only" icon={<Search/>} aria-label="Investigate" />`
 *   `import ResolveButton from '@common/ResolveButton'`         → `<Button tone="ghost" composition="icon-only" icon={<CheckCircle/>} aria-label="Resolve" />`
 *   `import EditButton from '@k8s/common/EditButton'`           → `<Button tone="ghost" composition="icon-only" icon={<Edit/>} aria-label="Edit" />`
 *   `import DisableButton from '@k8s/common/DisableButton'`     → `<Button tone="danger" composition="icon-only" icon={<Block/>} aria-label="Disable" />`
 *   `import CreateTicketButton from '@common/CreateTicketButton'` → `<Button tone="secondary" icon={<Add/>}>Create ticket</Button>`
 *   `import DownloadButton from '@common/DownloadButton'`       → `<Button tone="ghost" composition="icon-only" icon={<Download/>} aria-label="Download" />`
 *   `import CustomBackButton from '@common/CustomBackButton'`   → `<Button tone="link" icon={<ArrowBack/>}>Back</Button>`
 *
 * Don't (per spec):
 *   - Don't put two primary buttons in the same form/dialog. One primary per surface.
 *   - Don't use `danger` for cancel. Cancel is `secondary` or `ghost`.
 *   - Don't introduce a "warning" tone. Escalate to `danger` with confirmation.
 *   - Don't ship `composition="icon-only"` without `aria-label`.
 *   - Don't use `link` tone for form-submit or destructive flows.
 */
import * as React from 'react';
import { Box, ButtonBase, CircularProgress } from '@mui/material';
import Tooltip from './Tooltip';

export type ButtonSize = 'xs' | 'sm' | 'md' | 'lg';
export type ButtonTone = 'primary' | 'secondary' | 'ghost' | 'danger' | 'link';
export type ButtonComposition = 'text' | 'icon+text' | 'text+icon' | 'icon-only';
export type IconPlacement = 'start' | 'end';
export type ButtonTooltipPlacement = 'top' | 'bottom' | 'left' | 'right';

interface BaseButtonProps {
  tone?: ButtonTone;
  size?: ButtonSize;
  /** Force a composition. If omitted, derived from `children` and `icon` presence. */
  composition?: ButtonComposition;
  icon?: React.ReactNode;
  iconPlacement?: IconPlacement;
  /**
   * Trailing visual accent — renders the given icon inside a yellow-tile
   * (Nudgebee Yellow `--ds-yellow-500` bg, `--ds-brand-700` ink) at the right
   * edge of the button. Reserved for *the* primary CTA on a page (Apply,
   * Optimize, Continue). Don't combine with `composition='icon-only'` or
   * `tone='link'`. Don't use for ambient secondary actions — the yellow draws
   * the eye too aggressively to be ambient.
   */
  trailingAccent?: React.ReactNode;
  /**
   * Hover tooltip content. Renders a `ds/Tooltip` (re-export of CustomTooltip)
   * wrapping the button. Recommended for `composition='icon-only'` buttons so
   * sighted users see the same label `aria-label` announces. Omit on text
   * buttons where the label is already visible.
   */
  tooltip?: React.ReactNode;
  /** Tooltip placement. Default 'top'. */
  tooltipPlacement?: ButtonTooltipPlacement;
  /** Disable tooltip flip so it stays on the requested side and shifts instead. */
  tooltipDisableFlip?: boolean;
  loading?: boolean;
  disabled?: boolean;
  fullWidth?: boolean;
  type?: 'button' | 'submit' | 'reset';
  id?: string;
  className?: string;
  children?: React.ReactNode;
  onClick?: (e: React.MouseEvent<HTMLButtonElement>) => void;
  /** Required when composition='icon-only' */
  'aria-label'?: string;
  href?: string;
  target?: string;
}

export type ButtonProps = BaseButtonProps;

const SIZE_TOKENS: Record<
  ButtonSize,
  { height: string; padX: string; fontSize: string; gap: string; iconSize: number; accentSize: number; accentIconSize: number }
> = {
  xs: {
    height: '24px',
    padX: 'var(--ds-space-2)',
    fontSize: 'var(--ds-text-caption)',
    gap: 'var(--ds-space-1)',
    iconSize: 13,
    accentSize: 16,
    accentIconSize: 10,
  },
  sm: {
    height: '28px',
    padX: 'var(--ds-space-3)',
    fontSize: 'var(--ds-text-small)',
    gap: 'var(--ds-space-1)',
    iconSize: 14,
    accentSize: 20,
    accentIconSize: 12,
  },
  md: {
    height: '32px',
    padX: 'var(--ds-space-3)',
    fontSize: 'var(--ds-text-body)',
    gap: 'var(--ds-space-2)',
    iconSize: 16,
    accentSize: 24,
    accentIconSize: 14,
  },
  lg: {
    height: '40px',
    padX: 'var(--ds-space-4)',
    fontSize: 'var(--ds-text-body-lg)',
    gap: 'var(--ds-space-2)',
    iconSize: 18,
    accentSize: 30,
    accentIconSize: 18,
  },
};

interface TonePalette {
  bg: string;
  bgHover: string;
  bgActive: string;
  text: string;
  border: string;
  borderHover: string;
}

// Brand-navy primary + brand-toned secondary/ghost. Approved 2026-05-09.
//   primary: rest=brand-600 (the brand exact), hover=brand-500, active=brand-700.
//   secondary: white bg + brand-600 text + brand-200 border; hover bg=brand-100.
//   ghost:   transparent bg + brand-600 text; hover bg=brand-100, hover text=brand-700.
//   link:    STAYS on --ds-blue-600 (per Q1) — links rely on convention.
//   danger:  STAYS on --ds-red-* — destructive intent never absorbs brand.
const TONE_PALETTE: Record<ButtonTone, TonePalette> = {
  primary: {
    bg: 'var(--ds-brand-500)',
    bgHover: 'var(--ds-brand-500)',
    bgActive: 'var(--ds-brand-700)',
    text: 'var(--ds-background-100)',
    border: 'var(--ds-brand-600)',
    borderHover: 'var(--ds-brand-500)',
  },
  secondary: {
    bg: 'var(--ds-background-100)',
    bgHover: 'var(--ds-brand-100)',
    bgActive: 'var(--ds-brand-200)',
    text: 'var(--ds-brand-600)',
    border: 'var(--ds-brand-200)',
    borderHover: 'var(--ds-brand-300)',
  },
  ghost: {
    bg: 'transparent',
    bgHover: 'var(--ds-brand-100)',
    bgActive: 'var(--ds-brand-200)',
    text: 'var(--ds-brand-600)',
    border: 'transparent',
    borderHover: 'transparent',
  },
  danger: {
    bg: 'var(--ds-red-500)',
    bgHover: 'var(--ds-red-600)',
    bgActive: 'var(--ds-red-700)',
    text: 'var(--ds-background-100)',
    border: 'var(--ds-red-500)',
    borderHover: 'var(--ds-red-600)',
  },
  link: {
    bg: 'transparent',
    bgHover: 'transparent',
    bgActive: 'transparent',
    text: 'var(--ds-blue-600)',
    border: 'transparent',
    borderHover: 'transparent',
  },
};

function deriveComposition(
  composition: ButtonComposition | undefined,
  hasIcon: boolean,
  hasChildren: boolean,
  iconPlacement: IconPlacement
): ButtonComposition {
  if (composition) return composition;
  if (hasIcon && hasChildren) {
    return iconPlacement === 'end' ? 'text+icon' : 'icon+text';
  }
  if (hasIcon) return 'icon-only';
  return 'text';
}

export function Button({
  tone = 'primary',
  size = 'md',
  composition,
  icon,
  iconPlacement = 'start',
  trailingAccent,
  tooltip,
  tooltipPlacement = 'top',
  tooltipDisableFlip = false,
  loading = false,
  disabled = false,
  fullWidth = false,
  type = 'button',
  id,
  className,
  children,
  onClick,
  'aria-label': ariaLabel,
  href,
  target,
}: ButtonProps) {
  const isInteractionDisabled = disabled || loading;
  const tokens = SIZE_TOKENS[size];
  const palette = TONE_PALETTE[tone];

  const resolvedComposition = deriveComposition(composition, !!icon, !!children, iconPlacement);
  const isIconOnly = resolvedComposition === 'icon-only';

  // Spec: icon-only requires aria-label
  if (isIconOnly && !ariaLabel && process.env.NODE_ENV !== 'production') {
    // eslint-disable-next-line no-console
    console.warn('[Button] composition="icon-only" requires `aria-label` (per spec).');
  }

  // Spec: trailingAccent isn't valid for icon-only or link buttons.
  if (trailingAccent && (isIconOnly || tone === 'link') && process.env.NODE_ENV !== 'production') {
    // eslint-disable-next-line no-console
    console.warn(`[Button] trailingAccent is invalid with composition="icon-only" or tone="link".`);
  }

  const isLink = tone === 'link';

  const iconNode = loading ? (
    <CircularProgress size={tokens.iconSize} sx={{ color: 'inherit' }} thickness={5} />
  ) : icon ? (
    <Box
      component='span'
      aria-hidden='true'
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        '& svg': { width: tokens.iconSize, height: tokens.iconSize },
        flexShrink: 0,
      }}
    >
      {icon}
    </Box>
  ) : null;

  // Trailing accent — yellow tile + dark icon, rendered after the children at
  // the right edge of the button. Reserved for the page's primary CTA. The
  // accent uses brand-yellow regardless of tone, so it reads consistently
  // whether the button is primary (navy bg) or secondary (white bg).
  const accentNode =
    trailingAccent && !isIconOnly && !isLink ? (
      <Box
        component='span'
        aria-hidden='true'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: `${tokens.accentSize}px`,
          height: `${tokens.accentSize}px`,
          backgroundColor: 'var(--ds-yellow-500)',
          color: 'var(--ds-brand-700)',
          borderRadius: 'var(--ds-radius-sm)',
          flexShrink: 0,
          '& svg, & .MuiSvgIcon-root': {
            width: `${tokens.accentIconSize}px`,
            height: `${tokens.accentIconSize}px`,
            fontSize: `${tokens.accentIconSize}px`,
          },
        }}
      >
        {trailingAccent}
      </Box>
    ) : null;

  const content = isIconOnly ? (
    iconNode
  ) : (
    <>
      {(resolvedComposition === 'icon+text' || (loading && iconPlacement !== 'end')) && iconNode}
      <Box component='span' sx={{ display: 'inline-block' }}>
        {children}
      </Box>
      {(resolvedComposition === 'text+icon' || (loading && iconPlacement === 'end')) && iconNode}
      {accentNode}
    </>
  );

  const buttonNode = (
    <ButtonBase
      id={id}
      className={className}
      type={type}
      disabled={isInteractionDisabled}
      onClick={onClick}
      aria-label={ariaLabel}
      aria-busy={loading || undefined}
      // Pass `component='a' + href + target` only when this button renders as a link.
      // MUI's ButtonBase typings only accept `href` when `component='a'` is set.
      {...(href ? { component: 'a' as const, href, target } : {})}
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: tokens.gap,
        // Button labels are interactive headings, not body text — use the
        // display family (Poppins). Matches Input/Select/FilterDropdown labels.
        fontFamily: 'var(--ds-font-display)',
        fontSize: tokens.fontSize,
        fontWeight: 'var(--ds-font-weight-medium)',
        lineHeight: 1,
        minHeight: tokens.height,
        height: tokens.height,
        // icon-only is square; link has no padding;
        // when a trailingAccent is present, tighten the right padding so the
        // yellow tile sits with a small inset (4px) rather than floating in
        // the middle of the standard padX.
        paddingLeft: isLink ? 0 : isIconOnly ? 0 : tokens.padX,
        paddingRight: isLink ? 0 : isIconOnly ? 0 : accentNode ? 'var(--ds-space-1)' : tokens.padX,
        width: fullWidth ? '100%' : isIconOnly ? tokens.height : 'auto',
        backgroundColor: palette.bg,
        color: palette.text,
        border: isLink ? 'none' : `1px solid ${palette.border}`,
        borderRadius: isLink ? 0 : 'var(--ds-radius-md)',
        cursor: isInteractionDisabled ? 'default' : 'pointer',
        whiteSpace: 'nowrap',
        textDecoration: isLink ? 'none' : undefined,
        transition:
          'background-color var(--ds-motion-micro) var(--ds-motion-ease), color var(--ds-motion-micro) var(--ds-motion-ease), border-color var(--ds-motion-micro) var(--ds-motion-ease)',
        '&:hover': isInteractionDisabled
          ? undefined
          : {
              backgroundColor: palette.bgHover,
              borderColor: palette.borderHover,
              ...(isLink && { textDecoration: 'underline' }),
            },
        '&:active': isInteractionDisabled
          ? undefined
          : {
              backgroundColor: palette.bgActive,
            },
        // Focus ring uses the brand-yellow accent (Nudgebee Yellow #FACF39) so
        // the keyboard-focus signal is unmistakable on both navy and white
        // surfaces and stays inside the brand palette. Approved 2026-05-09.
        '&.Mui-focusVisible': {
          outline: '2px solid var(--ds-yellow-500)',
          outlineOffset: '2px',
        },
        '&.Mui-disabled': {
          opacity: 0.5,
          color: palette.text,
          backgroundColor: palette.bg,
          borderColor: palette.border,
        },
      }}
    >
      {content}
    </ButtonBase>
  );

  // Wrap in Tooltip when caller provided one. Tooltip is a re-export of the
  // legacy CustomTooltip; the `<span>` wrapper is required so the tooltip
  // anchors correctly even when the button is disabled (a disabled <button>
  // doesn't fire MUI's hover events on its own).
  if (tooltip) {
    return (
      <Tooltip title={tooltip} placement={tooltipPlacement} disableFlip={tooltipDisableFlip}>
        <Box component='span' sx={{ display: 'inline-flex' }}>
          {buttonNode}
        </Box>
      </Tooltip>
    );
  }

  return buttonNode;
}

export default Button;
