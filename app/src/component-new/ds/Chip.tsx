/**
 * Chip — DS V2.
 * Spec: app/design-system/primitives/action/chip.html
 *
 * Canonical port of @components1/common/Chip (V1) into the DS token system.
 * Every V1 variant, slot, state, and behavior is preserved; only the colour
 * source is swapped from raw hex to DS CSS variables (--ds-*) where a semantic
 * token exists. Categorical `hue` values intentionally remain as raw hex per
 * V1 — they form their own categorical-color system, distinct from tones.
 *
 * When to use (Chip vs Label/CustomLabels):
 *   - Chip   → interactive or categorical pill: filters, dismissible tags,
 *              counts, categorical hues, avatars, action menus.
 *   - Label  → read-only Status-axis pill in a table cell (`Active`/`Failed`/
 *              `Pending`). 5 tones, no click. Prefer `ds/Label` for new code;
 *              `common/CustomLabels` for legacy call sites that need
 *              auto-tone-from-text.
 *   Rule:   clickable or categorical → Chip. Just status text → Label.
 *
 * Variants:
 *   variant        = 'filter' | 'tag' | 'status' | 'input' | 'action' | 'count' | 'avatar'
 *                    Optional — when omitted, derived from prop combinations
 *                    (kept for backward compat with bare-prop call sites).
 *   size           = 'micro' | '2xs' | 'xs' | 'sm' | 'md'  (14 / 14 / 18 / 22 / 24px heights)
 *                    micro is the *secondary-signal* tier (9px / 14px) — used
 *                    inside dense cells (Comparison delta etc.). Don't use it
 *                    for the primary label of a row.
 *   tone           = 'neutral' | 'info' | 'success' | 'warning' | 'critical' | 'savings' | 'waste' | 'agent'
 *                    V1's `danger` → use `critical`; V1's `pending` → use `neutral`
 *                    with `dotVariant='hollow'` (auto-hollow when omitted).
 *   shape          = 'pill' | 'rect'
 *   hue            = 'slate' | 'green' | 'amber' | 'red' | 'blue' | 'violet' | 'pink' | 'teal'
 *                    Categorical, tag chips only. Use `hashHue(key)` for stable mapping.
 *   composition    = 'text-only' | 'icon+text' | 'icon+count' | 'dot+text' | 'count-first' | 'icon-only' | 'avatar+text'
 *   interactivity  = 'static' | 'clickable' | 'dismissible' | 'toggleable'
 *   solid          = boolean  (filled bg; reserve for P0 / critical headlines)
 *
 * Don't:
 *   - Don't introduce a new tone — pick one of the eight or raise it.
 *   - Don't combine `solid` with non-critical / non-warning tones.
 *   - Don't use icon-only at size='md' — that's a button.
 *   - Don't put `hue` on non-tag variants — use `tone`.
 *   - aria-label is required when composition resolves to icon-only.
 */
import * as React from 'react';
import { Box, ButtonBase, CircularProgress } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';

export type ChipSize = 'micro' | '2xs' | 'xs' | 'sm' | 'md';
export type ChipVariant = 'filter' | 'tag' | 'status' | 'input' | 'action' | 'count' | 'avatar';
export type ChipTone = 'neutral' | 'info' | 'success' | 'warning' | 'critical' | 'savings' | 'waste' | 'agent';
export type ChipShape = 'pill' | 'rect';
/** 8 categorical hues for tag chips. */
export type ChipHue = 'slate' | 'green' | 'amber' | 'red' | 'blue' | 'violet' | 'pink' | 'teal';
export type ChipDotVariant = 'filled' | 'hollow';

export interface ChipProps {
  /** When omitted, behavior is derived from prop combinations:
   *    - `pressed`/`selected` ⇒ filter
   *    - `onDismiss` ⇒ tag (with dismiss)
   *    - `onClick` ⇒ action
   *    - `dot` ⇒ status
   *    - else ⇒ tag (static tonal pill)
   */
  variant?: ChipVariant;
  size?: ChipSize;
  tone?: ChipTone;
  shape?: ChipShape;
  /** Categorical hue. Tag chips only — overrides `tone`. */
  hue?: ChipHue;

  children?: React.ReactNode;
  /** Leading icon. Combined with text → icon+text; alone → icon-only (xs/sm only). */
  icon?: React.ReactNode;
  /** Leading avatar slot — circular, sized to chip. Mutually exclusive with `icon`. */
  leadingAvatar?: React.ReactNode;
  /** Optional numeric count. Right-aligned by default; `count-first` when no icon and no children. */
  count?: number;
  /** Boolean shorthand for the dot+text composition. */
  dot?: boolean;
  /** Hollow vs filled dot. Defaults to hollow when tone='neutral' and consumer didn't set explicitly; filled otherwise. */
  dotVariant?: ChipDotVariant;
  /** Trailing icon slot. Mutually exclusive with `trailingChevron` and `onDismiss`. */
  trailingIcon?: React.ReactNode;
  /** Renders a trailing chevron-down (dropdown trigger affordance). */
  trailingChevron?: boolean;

  /** Filled-background variant — reserve for P0 / critical headlines. Spec restricts to critical/warning. */
  solid?: boolean;

  /** Click handler — presence ⇒ clickable. */
  onClick?: (e: React.MouseEvent<HTMLElement>) => void;
  /** Dismiss handler — presence ⇒ dismissible (renders × button). */
  onDismiss?: (e: React.MouseEvent<HTMLElement> | React.KeyboardEvent<HTMLElement>) => void;
  /** Toggleable: V2 alias for `selected`. Either works on filter/clickable chips. */
  pressed?: boolean;
  /** Selected state — V1 name. Filter chips use the indigo selection palette when true. */
  selected?: boolean;

  loading?: boolean;
  disabled?: boolean;

  'aria-label'?: string;
  'aria-pressed'?: boolean;

  className?: string;
  id?: string;
  'data-testid'?: string;
  /** Escape hatch — merged onto the root element. Prefer tone/variant/size; use sx only for one-off colour/typography tweaks. */
  sx?: import('@mui/system').SxProps<import('@mui/material/styles').Theme>;
}

// ─── Tone palette (DS tokens) ──────────────────────────────────────────────

interface TonePalette {
  bg: string;
  bgHover: string;
  text: string;
  border: string;
  dot: string;
}

const TONE_PALETTE: Record<ChipTone, TonePalette> = {
  neutral: {
    bg: 'var(--ds-gray-100)',
    bgHover: 'var(--ds-gray-200)',
    text: 'var(--ds-gray-700)',
    border: 'var(--ds-gray-200)',
    dot: 'var(--ds-gray-500)',
  },
  info: {
    bg: 'var(--ds-blue-100)',
    bgHover: 'var(--ds-blue-200)',
    text: 'var(--ds-blue-700)',
    border: 'var(--ds-blue-200)',
    dot: 'var(--ds-blue-500)',
  },
  success: {
    bg: 'var(--ds-green-100)',
    bgHover: 'var(--ds-green-200)',
    text: 'var(--ds-green-700)',
    border: 'var(--ds-green-200)',
    dot: 'var(--ds-green-500)',
  },
  warning: {
    bg: 'var(--ds-amber-100)',
    bgHover: 'var(--ds-amber-200)',
    text: 'var(--ds-amber-700)',
    border: 'var(--ds-amber-200)',
    dot: 'var(--ds-amber-500)',
  },
  critical: {
    bg: 'var(--ds-red-100)',
    bgHover: 'var(--ds-red-200)',
    text: 'var(--ds-red-700)',
    border: 'var(--ds-red-200)',
    dot: 'var(--ds-red-500)',
  },
  // Cost axis: savings = green; waste = red. Same families as success/critical, distinct semantics.
  savings: {
    bg: 'var(--ds-green-100)',
    bgHover: 'var(--ds-green-200)',
    text: 'var(--ds-green-700)',
    border: 'var(--ds-green-200)',
    dot: 'var(--ds-green-500)',
  },
  waste: {
    bg: 'var(--ds-red-100)',
    bgHover: 'var(--ds-red-200)',
    text: 'var(--ds-red-700)',
    border: 'var(--ds-red-200)',
    dot: 'var(--ds-red-500)',
  },
  agent: {
    bg: 'var(--ds-purple-100)',
    bgHover: 'var(--ds-purple-200)',
    text: 'var(--ds-purple-700)',
    border: 'var(--ds-purple-200)',
    dot: 'var(--ds-purple-500)',
  },
};

// Solid only valid for critical / warning per spec.
const SOLID_PALETTE: Partial<Record<ChipTone, { bg: string; text: string }>> = {
  critical: { bg: 'var(--ds-red-600)', text: 'var(--ds-background-100)' },
  warning: { bg: 'var(--ds-amber-600)', text: 'var(--ds-background-100)' },
};

// Filter selection palette — distinct from tones so a tone-tagged filter still
// selects to a single accent. Mapped to DS blue (V1's indigo equivalent).
const FILTER_SELECTED = {
  bg: 'var(--ds-blue-100)',
  bgHover: 'var(--ds-blue-200)',
  text: 'var(--ds-blue-700)',
  border: 'var(--ds-blue-200)',
};

const FILTER_DEFAULT = {
  bg: 'transparent',
  bgHover: 'var(--ds-gray-100)',
  text: 'var(--ds-gray-600)',
  border: 'var(--ds-gray-200)',
};

// ─── Categorical hues (tag chips only) ─────────────────────────────────────
//
// These hex values are intentional new tokens — categorical hu.es are part of
// the chip primitive and exist precisely so they don't collide with semantic
// tones. Reusing tone tokens for categorisation would conflate "category"
// with "state".

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

// ─── Size tokens ───────────────────────────────────────────────────────────

interface SizeTokens {
  height: string;
  padX: string;
  fontSize: string;
  gap: string;
  iconSize: number;
  dotSize: number;
  trailingDismiss: number;
  trailingChevron: number;
  avatarSize: number;
  pillRadius: string;
  rectRadius: string;
}

const SIZE_TOKENS: Record<ChipSize, SizeTokens> = {
  // micro is the smallest tier — for *secondary* signals inside dense cells
  // (Comparison delta, ProgressBar caption, inline meta on a row that already
  // has a primary value). 9px font, 14px height, soft-rect radius. The label
  // should not be the primary read — pair with an emphasized neighbour.
  // Don't use for icon-only or interactive chips at this size.
  micro: {
    height: '14px',
    padX: '5px',
    fontSize: '9px',
    gap: '2px',
    iconSize: 7,
    dotSize: 3,
    trailingDismiss: 6,
    trailingChevron: 5,
    avatarSize: 8,
    pillRadius: 'var(--ds-radius-sm)',
    rectRadius: 'var(--ds-radius-sm)',
  },
  // 2xs is the densest tier — for table cells, account-card row metadata, anywhere a
  // 16px chip still feels heavy. Hardcoded 10px font (no DS token below caption).
  '2xs': {
    height: '14px',
    padX: '3px',
    fontSize: '10px',
    gap: '2px',
    iconSize: 8,
    dotSize: 4,
    trailingDismiss: 7,
    trailingChevron: 5,
    avatarSize: 10,
    pillRadius: 'var(--ds-radius-pill)',
    rectRadius: 'var(--ds-radius-sm)',
  },
  // xs is the table-row tier — small label inside a comfortable pill.
  // 10px text in an 18px chip lets the label recede so the pill reads
  // as a tag rather than dominating the cell.
  xs: {
    height: '18px',
    padX: '6px',
    fontSize: '10px',
    gap: '4px',
    iconSize: 10,
    dotSize: 5,
    trailingDismiss: 8,
    trailingChevron: 6,
    avatarSize: 12,
    pillRadius: 'var(--ds-radius-pill)',
    rectRadius: 'var(--ds-radius-sm)',
  },
  // sm + md mirror V1's @components1/common/Chip tokens so the redesigned
  // chip renders at the same proportions as the legacy primitive — same
  // padding, same dot/font ratios. Do not drift from V1 without coordinating.
  sm: {
    height: '22px',
    padX: '10px',
    fontSize: '10px',
    gap: '4px',
    iconSize: 12,
    dotSize: 6,
    trailingDismiss: 10,
    trailingChevron: 8,
    avatarSize: 16,
    pillRadius: 'var(--ds-radius-pill)',
    rectRadius: 'var(--ds-radius-sm)',
  },
  md: {
    height: '24px',
    padX: '10px',
    fontSize: 'var(--ds-text-caption)',
    gap: '4px',
    iconSize: 12,
    dotSize: 6,
    trailingDismiss: 12,
    trailingChevron: 10,
    avatarSize: 18,
    pillRadius: 'var(--ds-radius-pill)',
    rectRadius: 'var(--ds-radius-sm)',
  },
};

// ─── Variant behaviour ─────────────────────────────────────────────────────

interface VariantBehavior {
  interactive: boolean;
  pressable: boolean;
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

const deriveVariant = (
  props: Pick<ChipProps, 'variant' | 'pressed' | 'selected' | 'onClick' | 'onDismiss' | 'dot' | 'count' | 'leadingAvatar'>
): ChipVariant => {
  if (props.variant) return props.variant;
  if (props.pressed !== undefined || props.selected !== undefined) return 'filter';
  if (props.leadingAvatar) return 'avatar';
  if (props.onDismiss) return 'tag';
  if (props.onClick) return 'action';
  if (props.dot) return 'status';
  if (props.count !== undefined) return 'count';
  return 'tag';
};

// ─── Dev-mode validation ───────────────────────────────────────────────────

const VALIDATION_RULES: ReadonlyArray<{ test: (p: ChipProps & { _variant: ChipVariant; _isIconOnly: boolean }) => boolean; message: string }> = [
  {
    test: (p) => p._variant === 'status' && Boolean(p.onClick || p.onDismiss),
    message: `status chips are read-only; onClick/onDismiss not allowed.`,
  },
  {
    test: (p) => p._variant === 'tag' && Boolean(p.onClick),
    message: `tag chips are read-only. Use variant='filter' or 'action' for clickable tags.`,
  },
  { test: (p) => Boolean(p.dot) && p._variant !== 'status' && p._variant !== 'filter', message: `dot is only valid on status or filter chips.` },
  {
    test: (p) => (p.selected !== undefined || p.pressed !== undefined) && p._variant !== 'filter' && p._variant !== 'action',
    message: `selected/pressed is only valid on filter (or action) chips.`,
  },
  { test: (p) => Boolean(p.onDismiss && p.trailingChevron), message: `onDismiss and trailingChevron are mutually exclusive trailing slots.` },
  { test: (p) => Boolean(p.onDismiss && p.trailingIcon), message: `onDismiss and trailingIcon are mutually exclusive trailing slots.` },
  { test: (p) => p._variant === 'avatar' && !p.leadingAvatar, message: `avatar chips require leadingAvatar.` },
  {
    test: (p) => p._variant === 'count' && Boolean(p.icon || p.leadingAvatar),
    message: `count chips don't accept leading icon/avatar — they're number-first.`,
  },
  { test: (p) => Boolean(p.hue) && p._variant !== 'tag', message: `hue is only valid on tag chips. Other variants use 'tone'.` },
  {
    test: (p) => Boolean(p.solid) && p.tone !== 'critical' && p.tone !== 'warning',
    message: `solid is reserved for critical/warning tones (per spec).`,
  },
  { test: (p) => p._isIconOnly && !p['aria-label'], message: `icon-only composition requires aria-label.` },
  { test: (p) => p._isIconOnly && p.size === 'md', message: `icon-only at size='md' is reserved for buttons, not chips.` },
  { test: (p) => p._isIconOnly && p.size === 'micro', message: `icon-only at size='micro' is illegible. Use size='2xs' or pair with text.` },
  {
    test: (p) => p.size === 'micro' && p._variant !== 'tag' && p._variant !== 'count' && p._variant !== 'status',
    message: `micro is the secondary-signal tier — only tag / count / status variants. Don't use for filter / action / input.`,
  },
];

const validateProps = (props: ChipProps, variant: ChipVariant, isIconOnly: boolean): void => {
  if (process.env.NODE_ENV === 'production') return;
  for (const rule of VALIDATION_RULES) {
    if (rule.test({ ...props, _variant: variant, _isIconOnly: isIconOnly })) {
      // eslint-disable-next-line no-console
      console.warn(`[Chip] ${rule.message}`, props);
    }
  }
};

// ─── Resolved colours ──────────────────────────────────────────────────────

interface ResolvedColors {
  bg: string;
  bgHover: string;
  text: string;
  border: string;
}

const resolveColors = (
  variant: ChipVariant,
  selected: boolean,
  hueSet: HueSet | null,
  palette: TonePalette,
  solidPalette?: { bg: string; text: string }
): ResolvedColors => {
  if (solidPalette) {
    return { bg: solidPalette.bg, bgHover: solidPalette.bg, text: solidPalette.text, border: 'transparent' };
  }
  if (variant === 'filter' && selected) {
    return { bg: FILTER_SELECTED.bg, bgHover: FILTER_SELECTED.bgHover, text: FILTER_SELECTED.text, border: FILTER_SELECTED.border };
  }
  if (variant === 'filter') {
    return { bg: FILTER_DEFAULT.bg, bgHover: FILTER_DEFAULT.bgHover, text: FILTER_DEFAULT.text, border: FILTER_DEFAULT.border };
  }
  if (variant === 'action') {
    return { bg: 'transparent', bgHover: 'var(--ds-gray-100)', text: palette.text, border: palette.border };
  }
  if (hueSet) {
    return { bg: hueSet.bg, bgHover: hueSet.bg, text: hueSet.fg, border: hueSet.border };
  }
  return { bg: palette.bg, bgHover: palette.bgHover, text: palette.text, border: palette.border };
};

// ─── Slot renderers ────────────────────────────────────────────────────────

interface LeadingSlotProps {
  loading: boolean;
  dot: boolean;
  isHollowDot: boolean;
  dotColor: string;
  leadingAvatar?: React.ReactNode;
  icon?: React.ReactNode;
  tokens: SizeTokens;
  fgColor: string;
}

const LeadingSlot = ({ loading, dot, isHollowDot, dotColor, leadingAvatar, icon, tokens, fgColor }: LeadingSlotProps) => {
  if (loading) {
    return (
      <Box
        sx={{
          width: tokens.iconSize,
          height: tokens.iconSize,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        <CircularProgress size={tokens.iconSize} thickness={5} sx={{ color: fgColor }} />
      </Box>
    );
  }
  if (dot) {
    return (
      <Box
        component='span'
        aria-hidden='true'
        data-dot={isHollowDot ? 'hollow' : 'filled'}
        sx={{
          width: tokens.dotSize,
          height: tokens.dotSize,
          borderRadius: '50%',
          flexShrink: 0,
          ...(isHollowDot ? { border: `1px solid ${dotColor}`, backgroundColor: 'transparent' } : { backgroundColor: dotColor }),
        }}
      />
    );
  }
  if (leadingAvatar) {
    return (
      <Box
        aria-hidden='true'
        sx={{
          width: tokens.avatarSize,
          height: tokens.avatarSize,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          borderRadius: '50%',
          overflow: 'hidden',
          flexShrink: 0,
          '& > *': { width: '100%', height: '100%' },
        }}
      >
        {leadingAvatar}
      </Box>
    );
  }
  if (icon) {
    return (
      <Box
        component='span'
        aria-hidden='true'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
          color: 'inherit',
          '& svg, & .MuiSvgIcon-root': { width: tokens.iconSize, height: tokens.iconSize, fontSize: tokens.iconSize },
        }}
      >
        {icon}
      </Box>
    );
  }
  return null;
};

interface TrailingSlotProps {
  onDismiss?: (e: React.MouseEvent<HTMLElement> | React.KeyboardEvent<HTMLElement>) => void;
  trailingChevron: boolean;
  trailingIcon?: React.ReactNode;
  tokens: SizeTokens;
  fgColor: string;
  label?: React.ReactNode;
  testIdPrefix?: string;
}

const TrailingSlot = ({ onDismiss, trailingChevron, trailingIcon, tokens, fgColor, label, testIdPrefix }: TrailingSlotProps) => {
  if (onDismiss) {
    return (
      <Box
        component='button'
        type='button'
        aria-label={`Remove ${typeof label === 'string' ? label : 'item'}`}
        data-testid={`${testIdPrefix || 'chip'}-dismiss`}
        onClick={(e) => {
          e.stopPropagation();
          onDismiss(e);
        }}
        sx={{
          appearance: 'none',
          border: 'none',
          background: 'transparent',
          color: 'inherit',
          cursor: 'pointer',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: tokens.iconSize,
          height: tokens.iconSize,
          padding: 0,
          borderRadius: '50%',
          opacity: 0.7,
          flexShrink: 0,
          transition: 'opacity 120ms cubic-bezier(0.4, 0, 0.2, 1), background-color 120ms cubic-bezier(0.4, 0, 0.2, 1)',
          '&:hover': { opacity: 1, backgroundColor: `${fgColor}1a` },
          '&:focus-visible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
        }}
      >
        <CloseIcon sx={{ fontSize: tokens.trailingDismiss }} />
      </Box>
    );
  }
  if (trailingChevron) {
    return (
      <Box
        aria-hidden='true'
        sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', color: 'inherit', opacity: 0.7, flexShrink: 0 }}
      >
        <KeyboardArrowDownIcon sx={{ fontSize: tokens.trailingChevron }} />
      </Box>
    );
  }
  if (trailingIcon) {
    return (
      <Box
        component='span'
        aria-hidden='true'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
          color: 'inherit',
          '& svg, & .MuiSvgIcon-root': { width: tokens.iconSize, height: tokens.iconSize, fontSize: tokens.iconSize },
        }}
      >
        {trailingIcon}
      </Box>
    );
  }
  return null;
};

// ─── Component ─────────────────────────────────────────────────────────────

export function Chip(props: ChipProps) {
  const {
    size = 'sm',
    tone = 'neutral',
    shape = 'pill',
    hue,
    children,
    icon,
    leadingAvatar,
    count,
    dot = false,
    dotVariant,
    trailingIcon,
    trailingChevron = false,
    solid = false,
    onClick,
    onDismiss,
    pressed,
    selected,
    loading = false,
    disabled = false,
    'aria-label': ariaLabel,
    'aria-pressed': ariaPressedProp,
    className,
    id,
    sx: sxOverride,
  } = props;

  const variant = deriveVariant(props);
  const tokens = SIZE_TOKENS[size];
  const palette = TONE_PALETTE[tone];
  const hueSet = variant === 'tag' && hue ? HUES[hue] : null;
  const behavior = VARIANT_BEHAVIOR[variant];

  const hasChildren = children !== undefined && children !== null && children !== '';
  const isIconOnly = !!icon && !hasChildren && count === undefined && !dot && !leadingAvatar;
  const isSelected = selected ?? pressed ?? false;
  const isInteractive = (behavior.interactive || !!onClick || !!onDismiss) && !disabled && !loading;
  const isPressable = behavior.pressable && isInteractive;
  const isToggleable = pressed !== undefined || selected !== undefined;

  // Validation runs on every render in dev; cheap.
  validateProps(props, variant, isIconOnly);

  const solidPalette = solid ? SOLID_PALETTE[tone] : undefined;
  const colors = resolveColors(variant, isSelected, hueSet, palette, solidPalette);

  // Hollow dot defaults: explicit > auto-hollow for neutral tone (V1's `pending`).
  const isHollowDot = dotVariant === 'hollow' || (dotVariant === undefined && tone === 'neutral' && dot && variant === 'status');

  // Filter-selected and count chips render bolder.
  const isBoldWeight = (variant === 'filter' && isSelected) || variant === 'count';

  // count-first: count BEFORE children when no icon/avatar/dot.
  const isCountFirst = count !== undefined && !icon && !leadingAvatar && !dot && hasChildren;

  const handleKeyDown = (e: React.KeyboardEvent<HTMLElement>) => {
    if (!isInteractive) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onClick?.(e as unknown as React.MouseEvent<HTMLElement>);
      return;
    }
    if ((e.key === 'Backspace' || e.key === 'Delete') && onDismiss) {
      e.preventDefault();
      onDismiss(e);
    }
  };

  const baseSx = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: tokens.gap,
    height: tokens.height,
    paddingLeft: tokens.padX,
    paddingRight: tokens.padX,
    fontSize: tokens.fontSize,
    fontWeight: isBoldWeight ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
    lineHeight: 1,
    color: colors.text,
    backgroundColor: colors.bg,
    border: `1px solid ${colors.border}`,
    borderRadius: shape === 'pill' ? tokens.pillRadius : tokens.rectRadius,
    whiteSpace: 'nowrap',
    verticalAlign: 'middle',
    letterSpacing: size === 'md' ? '-0.1px' : '0',
    cursor: isInteractive ? 'pointer' : 'default',
    userSelect: 'none' as const,
    opacity: disabled ? 0.4 : 1,
    pointerEvents: (disabled || loading ? 'none' : 'auto') as React.CSSProperties['pointerEvents'],
    transition: isInteractive
      ? 'background-color 120ms cubic-bezier(0.4, 0, 0.2, 1), border-color 120ms cubic-bezier(0.4, 0, 0.2, 1), color 120ms cubic-bezier(0.4, 0, 0.2, 1), transform 120ms cubic-bezier(0.4, 0, 0.2, 1)'
      : undefined,
    '&:hover': isInteractive ? { backgroundColor: colors.bgHover } : undefined,
    '&:active': isPressable ? { transform: 'scale(0.97)' } : undefined,
    '&.Mui-focusVisible, &:focus-visible': isInteractive ? { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' } : undefined,
    '@media (prefers-reduced-motion: reduce)': {
      transition: 'none',
      '&:active': { transform: 'none' },
    },
  };

  const dotColor = solidPalette ? colors.text : palette.dot;

  const dotNode = dot ? (
    <LeadingSlot
      loading={false}
      dot={true}
      isHollowDot={isHollowDot}
      dotColor={dotColor}
      icon={undefined}
      leadingAvatar={undefined}
      tokens={tokens}
      fgColor={colors.text}
    />
  ) : null;

  const leadingNode = !dot ? (
    <LeadingSlot
      loading={loading}
      dot={false}
      isHollowDot={false}
      dotColor={dotColor}
      icon={icon}
      leadingAvatar={leadingAvatar}
      tokens={tokens}
      fgColor={colors.text}
    />
  ) : null;

  const countNode =
    count !== undefined ? (
      <Box
        component='span'
        sx={{
          fontVariantNumeric: 'tabular-nums',
          // Counts ride alongside the chip label; rendering them at the
          // chip's own weight keeps the *label* the loudest token. Bumping
          // counts to semibold made them dominate (e.g. "Critical 483"
          // reading as the number first), which is the wrong hierarchy
          // for filter/top-issues chips.
          fontWeight: 'var(--ds-font-weight-medium)',
          opacity: 0.75,
        }}
      >
        {count}
      </Box>
    ) : null;

  const trailingNode = (
    <TrailingSlot
      onDismiss={onDismiss}
      trailingChevron={trailingChevron}
      trailingIcon={trailingIcon}
      tokens={tokens}
      fgColor={colors.text}
      label={children}
      testIdPrefix={props['data-testid']}
    />
  );

  const content = (
    <>
      {dotNode}
      {leadingNode}
      {isCountFirst && countNode}
      {hasChildren && <Box component='span'>{children}</Box>}
      {!isCountFirst && countNode}
      {trailingNode}
    </>
  );

  const dataAttrs = {
    'data-variant': variant,
    'data-tone': tone,
    'data-size': size,
    'data-selected': isSelected || undefined,
    'data-testid': props['data-testid'],
  };

  const ariaPressed = isToggleable ? ariaPressedProp ?? isSelected : undefined;

  if (isInteractive) {
    return (
      <ButtonBase
        id={id}
        className={className}
        onClick={onClick}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        aria-label={ariaLabel}
        aria-pressed={ariaPressed}
        aria-disabled={disabled || undefined}
        aria-busy={loading || undefined}
        sx={sxOverride ? [baseSx, ...(Array.isArray(sxOverride) ? sxOverride : [sxOverride])] : baseSx}
        {...dataAttrs}
      >
        {content}
      </ButtonBase>
    );
  }

  return (
    <Box
      component='span'
      id={id}
      className={className}
      aria-label={ariaLabel}
      aria-pressed={ariaPressed}
      sx={sxOverride ? [baseSx, ...(Array.isArray(sxOverride) ? sxOverride : [sxOverride])] : baseSx}
      {...dataAttrs}
    >
      {content}
    </Box>
  );
}

export default Chip;
