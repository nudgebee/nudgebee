import { ReactNode, MouseEvent, KeyboardEvent, useEffect } from 'react';
import { Box, CircularProgress, type SxProps, type Theme } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { colors } from 'src/utils/colors';

// Single source of truth for chip primitives. Variant overview, common patterns, and
// migration notes live in app/design-system.md (Chips section).

export type ChipVariant = 'filter' | 'tag' | 'status' | 'input' | 'action' | 'count' | 'avatar';
export type ChipSize = 'xs' | 'sm' | 'md';
export type ChipTone = 'neutral' | 'info' | 'success' | 'warning' | 'danger' | 'pending';
export type ChipShape = 'pill' | 'rect';
/** 8 categorical hues for tag chips. Use `hashHue(key)` for stable string-to-hue mapping. */
export type ChipHue = 'slate' | 'green' | 'amber' | 'red' | 'blue' | 'violet' | 'pink' | 'teal';
export type ChipDotVariant = 'filled' | 'hollow';

export interface ChipProps {
  variant: ChipVariant;
  size?: ChipSize;
  tone?: ChipTone;
  shape?: ChipShape;
  /** Categorical hue for tag chips. Overrides `tone` when variant='tag'. */
  hue?: ChipHue;

  /** Required for all variants except icon-only (md status chip with aria-label). */
  label?: ReactNode;

  // Leading slots — at most one of these should be set per variant.
  leadingIcon?: ReactNode;
  leadingDot?: boolean;
  /** Hollow vs filled dot. Defaults to 'hollow' when tone='pending', 'filled' otherwise. */
  dotVariant?: ChipDotVariant;
  leadingAvatar?: ReactNode;

  // Trailing slots — at most one of these should be set per variant.
  trailingIcon?: ReactNode;
  trailingChevron?: boolean;
  /** Renders the trailing × dismiss affordance. Calls handler with the chip's click event. */
  onDismiss?: (e: MouseEvent | KeyboardEvent) => void;

  // Interaction
  selected?: boolean;
  onClick?: () => void;
  loading?: boolean;
  disabled?: boolean;

  // a11y
  'aria-label'?: string;
  'aria-pressed'?: boolean;

  // Escape hatches
  className?: string;
  sx?: SxProps<Theme>;
  'data-testid'?: string;
}

// ─── Sizing tokens ─────────────────────────────────────────────────────────

interface SizeTokens {
  height: string;
  paddingX: string;
  fontSize: string;
  gap: string;
  iconSize: string;
  dotSize: string;
  trailingDismiss: string;
  trailingChevron: string;
  avatarSize: string;
  pillRadius: string;
  rectRadius: string;
}

// Sizes were reduced by ~30% from the original spec (xs 20→16, sm 26→20, md 32→24)
// to better fit Nudgebee's information-dense surfaces. The proportions between sizes
// and between slot dimensions within a size are preserved.
const SIZES: Record<ChipSize, SizeTokens> = {
  xs: {
    height: '16px',
    paddingX: '4px',
    fontSize: '9px',
    gap: '3px',
    iconSize: '10px',
    dotSize: '5px',
    trailingDismiss: '8px',
    trailingChevron: '6px',

    avatarSize: '12px',
    pillRadius: '999px',
    rectRadius: '3px',
  },
  sm: {
    height: '20px',
    paddingX: '8px',
    fontSize: '10px',
    gap: '4px',
    iconSize: '12px',
    dotSize: '6px',
    trailingDismiss: '10px',
    trailingChevron: '8px',
    avatarSize: '16px',
    pillRadius: '999px',
    rectRadius: '4px',
  },
  md: {
    height: '24px',
    paddingX: '10px',
    fontSize: '11px',
    gap: '4px',
    iconSize: '12px',
    dotSize: '6px',
    trailingDismiss: '12px',
    trailingChevron: '10px',
    avatarSize: '18px',
    pillRadius: '999px',
    rectRadius: '5px',
  },
};

// ─── Tone tokens (light mode) ──────────────────────────────────────────────

interface ToneSet {
  /** Filled background — used by tag/status/input/avatar/count and selected filter. */
  bg: string;
  /** Hover delta over the filled bg. */
  bgHover: string;
  /** Foreground text + leading icon color. */
  fg: string;
  /** Border for outline + filled chips. May be transparent. */
  border: string;
  /** Color for leading status dot. */
  dot: string;
}

// Canonical chip tones — verbatim hex from the chip design system. These are the chip
// primitive's own token values, NOT approximations of platform tokens elsewhere in the app.
// Light mode only. Hover bg uses the family's 100-shade (one step darker than 50-shade base).
const TONES: Record<ChipTone, ToneSet> = {
  neutral: {
    bg: '#F4F4F5',
    bgHover: '#E4E4E7',
    fg: '#3F3F46',
    border: '#E4E4E7',
    dot: '#A1A1AA',
  },
  info: {
    bg: '#EFF6FF',
    bgHover: '#DBEAFE',
    fg: '#1D4ED8',
    border: '#BFDBFE',
    dot: '#3B82F6',
  },
  success: {
    bg: '#ECFDF5',
    bgHover: '#D1FAE5',
    fg: '#047857',
    border: '#A7F3D0',
    dot: '#10B981',
  },
  warning: {
    bg: '#FFFBEB',
    bgHover: '#FEF3C7',
    fg: '#B45309',
    border: '#FDE68A',
    dot: '#F59E0B',
  },
  danger: {
    bg: '#FEF2F2',
    bgHover: '#FEE2E2',
    fg: '#B91C1C',
    border: '#FECACA',
    dot: '#EF4444',
  },
  pending: {
    bg: '#F4F4F5',
    bgHover: '#E4E4E7',
    fg: '#52525B',
    border: '#E4E4E7',
    dot: '#A1A1AA',
  },
};

// Filter chips have their own indigo selection palette — distinct from semantic tones,
// so a severity-tagged filter (red dot) still selects to indigo, never to red. Per spec.
const FILTER_SELECTED = {
  bg: '#EEF2FF',
  fg: '#3730A3',
  border: '#C7D2FE',
};

const FILTER_DEFAULT = {
  fg: '#3F3F46',
  border: '#E4E4E7',
  hover: '#F4F4F5',
};

// ─── Categorical hues (tag chips) ──────────────────────────────────────────
//
// 8 hues for tag chips. The hex values here ARE intentional new tokens — categorical hues
// for tag chips are part of the design system primitive; reusing semantic tokens
// (success/warning/etc.) for tags would conflate "category" with "state". Used only by tag
// chips — no other variant accepts `hue`.

interface HueSet {
  bg: string;
  fg: string;
  border: string;
}

const HUES: Record<ChipHue, HueSet> = {
  slate: { bg: '#F1F5F9', fg: '#334155', border: '#E2E8F0' },
  green: { bg: '#ECFDF5', fg: '#047857', border: '#A7F3D0' },
  amber: { bg: '#FFFBEB', fg: '#B45309', border: '#FDE68A' },
  red: { bg: '#FEF2F2', fg: '#B91C1C', border: '#FECACA' },
  blue: { bg: '#EFF6FF', fg: '#1D4ED8', border: '#BFDBFE' },
  violet: { bg: '#F5F3FF', fg: '#5B21B6', border: '#DDD6FE' },
  pink: { bg: '#FDF2F8', fg: '#9D174D', border: '#FBCFE8' },
  teal: { bg: '#F0FDFA', fg: '#0F766E', border: '#99F6E4' },
};

const HUE_KEYS: ChipHue[] = ['slate', 'green', 'amber', 'red', 'blue', 'violet', 'pink', 'teal'];

/** Stable hash from any string key to one of the 8 categorical hues. */
export const hashHue = (key: string): ChipHue => {
  let h = 0;
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0;
  return HUE_KEYS[h % HUE_KEYS.length];
};

// ─── Variant defaults ──────────────────────────────────────────────────────

interface VariantBehavior {
  /** Whether the chip body itself should be a button (interactive). */
  interactive: boolean;
  /** Whether to apply scale(0.97) on :active. */
  pressable: boolean;
  /** Whether the chip uses a filled bg by default (tag/status/input/avatar/count) or outline (filter unselected, action default). */
  filled: boolean;
}

const VARIANT_BEHAVIOR: Record<ChipVariant, VariantBehavior> = {
  filter: { interactive: true, pressable: true, filled: false },
  tag: { interactive: false, pressable: false, filled: true },
  status: { interactive: false, pressable: false, filled: true },
  input: { interactive: true, pressable: false, filled: true },
  action: { interactive: true, pressable: true, filled: false },
  count: { interactive: false, pressable: false, filled: true },
  avatar: { interactive: true, pressable: false, filled: true },
};

// ─── Dev-mode prop validation ──────────────────────────────────────────────
//
// Catches the most common variant/prop misuses at runtime in development builds; in
// production these checks are dead-code-eliminated.

// leadingDot is valid on status (canonical) and filter (filter-as-status pattern, e.g.
// severity facets). Forbidden elsewhere — a dot on a tag/action chip is meaningless.
const VALIDATION_RULES: ReadonlyArray<{ test: (p: ChipProps) => boolean; message: string }> = [
  { test: (p) => p.variant === 'status' && Boolean(p.onClick || p.onDismiss), message: `status chips are read-only; onClick/onDismiss not allowed.` },
  {
    test: (p) => p.variant === 'tag' && Boolean(p.onClick),
    message: `tag chips are read-only. Use variant='filter' or 'action' for clickable tags.`,
  },
  {
    test: (p) => Boolean(p.leadingDot) && p.variant !== 'status' && p.variant !== 'filter',
    message: `leadingDot is only valid on status or filter chips.`,
  },
  { test: (p) => Boolean(p.selected) && p.variant !== 'filter', message: `selected is only valid on filter chips.` },
  { test: (p) => Boolean(p.onDismiss && p.trailingChevron), message: `onDismiss and trailingChevron are mutually exclusive trailing slots.` },
  { test: (p) => p.variant === 'avatar' && !p.leadingAvatar, message: `avatar chips require leadingAvatar.` },
  {
    test: (p) => p.variant === 'count' && Boolean(p.leadingIcon || p.leadingAvatar),
    message: `count chips don't accept leading icon/avatar — they're number-first.`,
  },
  { test: (p) => Boolean(p.hue) && p.variant !== 'tag', message: `hue is only valid on tag chips. Other variants use 'tone' instead.` },
];

const validateProps = (props: ChipProps): void => {
  if (process.env.NODE_ENV === 'production') return;
  for (const rule of VALIDATION_RULES) {
    if (rule.test(props)) {
      // eslint-disable-next-line no-console
      console.warn(`[Chip] ${rule.message}`, props);
    }
  }
};

// ─── Color resolution ──────────────────────────────────────────────────────
//
// Filter chips have their own indigo selection palette — distinct from semantic tones,
// so a severity-tagged filter (red dot) still selects to indigo, never to red. The `tone`
// prop is still honored for the leading dot color via `dotColor` separately.

interface ResolvedColors {
  fill: string;
  fillHover: string;
  fg: string;
  border: string;
}

const resolveColors = (variant: ChipVariant, selected: boolean, hueSet: HueSet | null, toneSet: ToneSet): ResolvedColors => {
  if (variant === 'filter' && selected) {
    return { fill: FILTER_SELECTED.bg, fillHover: FILTER_SELECTED.bg, fg: FILTER_SELECTED.fg, border: FILTER_SELECTED.border };
  }
  if (variant === 'filter') {
    return { fill: 'transparent', fillHover: FILTER_DEFAULT.hover, fg: FILTER_DEFAULT.fg, border: FILTER_DEFAULT.border };
  }
  if (hueSet) {
    return { fill: hueSet.bg, fillHover: hueSet.bg, fg: hueSet.fg, border: hueSet.border };
  }
  return { fill: toneSet.bg, fillHover: toneSet.bgHover, fg: toneSet.fg, border: toneSet.border };
};

// ─── Slot renderers ────────────────────────────────────────────────────────

interface LeadingSlotProps {
  loading: boolean;
  leadingDot?: boolean;
  isHollowDot: boolean;
  dotColor: string;
  leadingAvatar?: ReactNode;
  leadingIcon?: ReactNode;
  sizes: SizeTokens;
  fgColor: string;
}

const LeadingSlot = ({ loading, leadingDot, isHollowDot, dotColor, leadingAvatar, leadingIcon, sizes, fgColor }: LeadingSlotProps) => {
  if (loading) {
    return (
      <Box sx={{ width: sizes.iconSize, height: sizes.iconSize, display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}>
        <CircularProgress size={sizes.iconSize} thickness={5} sx={{ color: fgColor }} />
      </Box>
    );
  }
  if (leadingDot) {
    const dotSx = isHollowDot
      ? {
          width: sizes.dotSize,
          height: sizes.dotSize,
          borderRadius: '50%',
          border: `1px solid ${dotColor}`,
          backgroundColor: 'transparent',
          flexShrink: 0,
        }
      : { width: sizes.dotSize, height: sizes.dotSize, borderRadius: '50%', backgroundColor: dotColor, flexShrink: 0 };
    return <Box aria-hidden data-dot={isHollowDot ? 'hollow' : 'filled'} sx={dotSx} />;
  }
  if (leadingAvatar) {
    return (
      <Box
        aria-hidden
        sx={{
          width: sizes.avatarSize,
          height: sizes.avatarSize,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          borderRadius: '50%',
          overflow: 'hidden',
          flexShrink: 0,
          // Force passed-in img/avatar to fill the slot
          '& > *': { width: '100%', height: '100%' },
        }}
      >
        {leadingAvatar}
      </Box>
    );
  }
  if (leadingIcon) {
    return (
      <Box
        aria-hidden
        sx={{
          width: sizes.iconSize,
          height: sizes.iconSize,
          fontSize: sizes.iconSize,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          color: 'inherit',
          flexShrink: 0,
          // Resize most icon implementations (MUI svg, lucide svg, raw <svg>) to slot dims
          '& svg, & .MuiSvgIcon-root': { width: sizes.iconSize, height: sizes.iconSize, fontSize: sizes.iconSize },
        }}
      >
        {leadingIcon}
      </Box>
    );
  }
  return null;
};

interface TrailingSlotProps {
  onDismiss?: (e: MouseEvent | KeyboardEvent) => void;
  trailingChevron?: boolean;
  trailingIcon?: ReactNode;
  sizes: SizeTokens;
  fgColor: string;
  label?: ReactNode;
  testIdPrefix?: string;
  onDismissClick: (e: MouseEvent) => void;
}

const TrailingSlot = ({ onDismiss, trailingChevron, trailingIcon, sizes, fgColor, label, testIdPrefix, onDismissClick }: TrailingSlotProps) => {
  if (onDismiss) {
    return (
      <Box
        component='button'
        type='button'
        aria-label={`Remove ${typeof label === 'string' ? label : 'item'}`}
        data-testid={`${testIdPrefix || 'chip'}-dismiss`}
        onClick={onDismissClick}
        sx={{
          appearance: 'none',
          border: 'none',
          background: 'transparent',
          color: 'inherit',
          cursor: 'pointer',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: sizes.iconSize,
          height: sizes.iconSize,
          padding: 0,
          borderRadius: '50%',
          opacity: 0.7,
          flexShrink: 0,
          transition: 'opacity 120ms cubic-bezier(0.4, 0, 0.2, 1), background-color 120ms cubic-bezier(0.4, 0, 0.2, 1)',
          '&:hover': { opacity: 1, backgroundColor: `${fgColor}1a` },
          '&:focus-visible': { outline: `2px solid ${colors.primary}`, outlineOffset: '1px' },
        }}
      >
        <CloseIcon sx={{ fontSize: sizes.trailingDismiss }} />
      </Box>
    );
  }
  if (trailingChevron) {
    return (
      <Box aria-hidden sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', color: 'inherit', opacity: 0.7, flexShrink: 0 }}>
        <KeyboardArrowDownIcon sx={{ fontSize: sizes.trailingChevron }} />
      </Box>
    );
  }
  if (trailingIcon) {
    return (
      <Box
        aria-hidden
        sx={{
          width: sizes.iconSize,
          height: sizes.iconSize,
          fontSize: sizes.iconSize,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          color: 'inherit',
          flexShrink: 0,
          '& svg, & .MuiSvgIcon-root': { width: sizes.iconSize, height: sizes.iconSize, fontSize: sizes.iconSize },
        }}
      >
        {trailingIcon}
      </Box>
    );
  }
  return null;
};

// ─── Container style builder ───────────────────────────────────────────────

interface ContainerSxArgs {
  variant: ChipVariant;
  size: ChipSize;
  shape: ChipShape;
  sizes: SizeTokens;
  fillColor: string;
  fillHoverColor: string;
  fgColor: string;
  borderColor: string;
  showFill: boolean;
  showOutline: boolean;
  isBoldFontWeight: boolean;
  isInteractive: boolean;
  isPressable: boolean;
  disabled: boolean;
  loading: boolean;
  sx?: SxProps<Theme>;
}

const buildContainerSx = (a: ContainerSxArgs): SxProps<Theme> => {
  const hoverBg = a.showFill ? a.fillHoverColor : `${a.fgColor}0d`;
  const hoverBorder = a.showOutline ? a.borderColor : 'transparent';
  const isHoverable = a.isInteractive && !a.disabled;
  const hover = isHoverable ? { backgroundColor: hoverBg, borderColor: hoverBorder } : undefined;
  return {
    appearance: 'none',
    font: 'inherit',
    display: 'inline-flex',
    alignItems: 'center',
    gap: a.sizes.gap,
    height: a.sizes.height,
    px: a.sizes.paddingX,
    borderRadius: a.shape === 'pill' ? a.sizes.pillRadius : a.sizes.rectRadius,
    backgroundColor: a.showFill ? a.fillColor : 'transparent',
    color: a.fgColor,
    border: `1px solid ${a.showOutline ? a.borderColor : 'transparent'}`,
    fontSize: a.sizes.fontSize,
    fontWeight: a.isBoldFontWeight ? 600 : 500,
    lineHeight: 1,
    whiteSpace: 'nowrap',
    letterSpacing: a.size === 'md' ? '-0.1px' : '0',
    cursor: a.isInteractive ? 'pointer' : 'default',
    userSelect: 'none',
    textDecoration: 'none',
    opacity: a.disabled ? 0.4 : 1,
    pointerEvents: a.disabled || a.loading ? 'none' : 'auto',
    // Animation — deliberately conservative.
    transition:
      'background-color 120ms cubic-bezier(0.4, 0, 0.2, 1), border-color 120ms cubic-bezier(0.4, 0, 0.2, 1), color 120ms cubic-bezier(0.4, 0, 0.2, 1)',
    '&:hover': hover,
    // Pressed — Filter and Action chips only. Status/Tag must not scale (read-only chips).
    '&:active': a.isPressable ? { transform: 'scale(0.97)' } : undefined,
    '&:focus-visible': a.isInteractive ? { outline: `2px solid ${colors.primary}`, outlineOffset: '2px' } : undefined,
    '@media (prefers-reduced-motion: reduce)': { transition: 'none', transform: 'none', '&:active': { transform: 'none' } },
    ...(a.sx as object),
  };
};

// Keyboard handler — Enter/Space activate, Backspace/Delete dismiss when handler present.
const buildKeyDownHandler = (isInteractive: boolean, onClick?: () => void, onDismiss?: (e: KeyboardEvent) => void) => (e: KeyboardEvent) => {
  if (!isInteractive) return;
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    onClick?.();
    return;
  }
  if ((e.key === 'Backspace' || e.key === 'Delete') && onDismiss) {
    e.preventDefault();
    onDismiss(e);
  }
};

// ─── Component ─────────────────────────────────────────────────────────────

const Chip = (props: ChipProps) => {
  useEffect(() => {
    validateProps(props);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.variant, props.selected, props.leadingDot]);

  const {
    variant,
    size = 'sm',
    tone = 'neutral',
    shape = 'pill',
    hue,
    label,
    leadingIcon,
    leadingDot,
    dotVariant,
    leadingAvatar,
    trailingIcon,
    trailingChevron,
    onDismiss,
    selected = false,
    onClick,
    loading = false,
    disabled = false,
    className,
    sx,
  } = props;

  const sizes = SIZES[size];
  // Tag chips with `hue` use the categorical palette; everything else uses semantic tones.
  const hueSet = variant === 'tag' && hue ? HUES[hue] : null;
  const toneSet = TONES[tone];
  const isFilter = variant === 'filter';
  const { fill: fillColor, fillHover: fillHoverColor, fg: fgColor, border: borderColor } = resolveColors(variant, selected, hueSet, toneSet);

  // Dot follows the tone the consumer passed — never the filter selection state.
  const dotColor = toneSet.dot;
  // Hollow dot defaults: explicit dotVariant wins; auto-hollow for pending tone.
  const isHollowDot = dotVariant === 'hollow' || (dotVariant === undefined && tone === 'pending');
  const behavior = VARIANT_BEHAVIOR[variant];

  // Filter chips render filled when selected (indigo bg) and outlined when unselected.
  // Action chips are always outlined; everything else is filled by behavior.
  const showFill = behavior.filled || (isFilter && selected);
  const showOutline = !showFill || variant === 'action' || isFilter;
  const isBoldFontWeight = (isFilter && selected) || variant === 'count';

  const isInteractive = behavior.interactive && !disabled && !loading;
  const isPressable = behavior.pressable && isInteractive;

  const handleClick = (e: MouseEvent) => {
    if (!isInteractive) return;
    e.stopPropagation();
    onClick?.();
  };

  const handleDismissClick = (e: MouseEvent) => {
    e.stopPropagation();
    onDismiss?.(e);
  };

  const handleKeyDown = buildKeyDownHandler(isInteractive, onClick, onDismiss);

  const containerSx = buildContainerSx({
    variant,
    size,
    shape,
    sizes,
    fillColor,
    fillHoverColor,
    fgColor,
    borderColor,
    showFill,
    showOutline,
    isBoldFontWeight,
    isInteractive,
    isPressable,
    disabled,
    loading,
    sx,
  });

  const ariaPressed = variant === 'filter' ? props['aria-pressed'] ?? selected : undefined;
  const inner = (
    <>
      <LeadingSlot
        loading={loading}
        leadingDot={leadingDot}
        isHollowDot={isHollowDot}
        dotColor={dotColor}
        leadingAvatar={leadingAvatar}
        leadingIcon={leadingIcon}
        sizes={sizes}
        fgColor={fgColor}
      />
      {label != null && <Box component='span'>{label}</Box>}
      <TrailingSlot
        onDismiss={onDismiss}
        trailingChevron={trailingChevron}
        trailingIcon={trailingIcon}
        sizes={sizes}
        fgColor={fgColor}
        label={label}
        testIdPrefix={props['data-testid']}
        onDismissClick={handleDismissClick}
      />
    </>
  );

  // Two render paths so element-typed props (`type`, `disabled`) only land on the button form.
  if (isInteractive) {
    return (
      <Box
        component='button'
        type='button'
        onClick={handleClick}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        data-testid={props['data-testid']}
        data-variant={variant}
        data-tone={tone}
        data-size={size}
        data-selected={selected || undefined}
        className={className}
        aria-pressed={ariaPressed}
        aria-label={props['aria-label']}
        sx={containerSx}
      >
        {inner}
      </Box>
    );
  }

  return (
    <Box
      component='span'
      data-testid={props['data-testid']}
      data-variant={variant}
      data-tone={tone}
      data-size={size}
      data-selected={selected || undefined}
      className={className}
      aria-label={props['aria-label']}
      aria-pressed={ariaPressed}
      sx={containerSx}
    >
      {inner}
    </Box>
  );
};

export default Chip;
