/**
 * Overlay primitives — shared popover/menu chrome.
 *
 * Composed by DropdownMenu, Select, MultiSelect, FilterDropdown, Autocomplete,
 * Popover. All visual chrome (radius, shadow, item padding, hover wash,
 * animation) lives in the --ds-overlay-* token group; these components just
 * plumb the tokens onto MUI Menu/MenuItem/Divider/Typography. Changing a
 * token retones every consumer at once.
 *
 * Exports:
 *   OverlaySurface  — the popover surface (chrome + positioning + animation)
 *   OverlayItem     — one row inside a surface (label + slots + states)
 *   OverlaySection  — uppercase section header
 *   OverlaySeparator — divider between groups of items
 *
 * Why MUI Menu (not Popover) under the hood: Menu wraps Popover and adds
 * keyboard navigation (arrow keys, typeahead, focus management), role="menu"
 * / role="listbox" semantics, and MenuItem integration. Value-pickers that
 * contain a search input pass `disableAutoFocusItem` to keep focus on the
 * input.
 *
 * Not for app code — only consumed by other ds/* components.
 */
import * as React from 'react';
import { Box, Divider, InputBase, Menu, MenuItem, Typography, type MenuProps } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import { ds } from '@utils/colors';
import { Skeleton } from '../Skeleton';

/* ════════════════════════════════════════════════════════════════════════
   OverlaySurface
   ════════════════════════════════════════════════════════════════════════ */

export type OverlayAlign = 'start' | 'end';
export type OverlaySide = 'bottom' | 'top' | 'left' | 'right';
export type OverlayRole = 'menu' | 'listbox';

const OverlayItemRoleContext = React.createContext<'menuitem' | 'option'>('menuitem');

export interface OverlaySurfaceProps {
  anchorEl: HTMLElement | null;
  open: boolean;
  onClose: () => void;
  side?: OverlaySide;
  align?: OverlayAlign;
  /** Min-width of the panel (auto-grows beyond). Default: 200. */
  minWidth?: string | number;
  /** Fixed width — overrides minWidth when set. */
  width?: string | number;
  role?: OverlayRole;
  disableAutoFocusItem?: boolean;
  disablePortal?: boolean;
  children: React.ReactNode;
  /** Escape hatch for forwarding additional MUI Menu slot props. */
  slotProps?: MenuProps['slotProps'];
}

function deriveAnchorOrigin(side: OverlaySide, align: OverlayAlign) {
  if (side === 'top') {
    return { vertical: 'top' as const, horizontal: align === 'end' ? ('right' as const) : ('left' as const) };
  }
  if (side === 'left') {
    return { vertical: align === 'end' ? ('bottom' as const) : ('top' as const), horizontal: 'left' as const };
  }
  if (side === 'right') {
    return { vertical: align === 'end' ? ('bottom' as const) : ('top' as const), horizontal: 'right' as const };
  }
  return { vertical: 'bottom' as const, horizontal: align === 'end' ? ('right' as const) : ('left' as const) };
}

function deriveTransformOrigin(side: OverlaySide, align: OverlayAlign) {
  if (side === 'top') {
    return { vertical: 'bottom' as const, horizontal: align === 'end' ? ('right' as const) : ('left' as const) };
  }
  if (side === 'left') {
    return { vertical: align === 'end' ? ('bottom' as const) : ('top' as const), horizontal: 'right' as const };
  }
  if (side === 'right') {
    return { vertical: align === 'end' ? ('bottom' as const) : ('top' as const), horizontal: 'left' as const };
  }
  return { vertical: 'top' as const, horizontal: align === 'end' ? ('right' as const) : ('left' as const) };
}

export function OverlaySurface({
  anchorEl,
  open,
  onClose,
  side = 'bottom',
  align = 'start',
  minWidth = 200,
  width,
  role = 'menu',
  disableAutoFocusItem,
  disablePortal = true,
  children,
  slotProps,
}: OverlaySurfaceProps) {
  const itemRole = role === 'listbox' ? 'option' : 'menuitem';
  return (
    <Menu
      anchorEl={anchorEl}
      open={open}
      onClose={onClose}
      disablePortal={disablePortal}
      anchorOrigin={deriveAnchorOrigin(side, align)}
      transformOrigin={deriveTransformOrigin(side, align)}
      disableAutoFocusItem={disableAutoFocusItem}
      // component='div' so non-item children (OverlaySearch, OverlayScrollBox)
      // are valid HTML — see OverlayItemRoleContext above for the trade-off.
      MenuListProps={{ role, component: 'div', sx: { padding: 'var(--ds-overlay-padding-y) 0' } }}
      slotProps={{
        ...slotProps,
        paper: {
          ...slotProps?.paper,
          sx: {
            ...(width !== undefined ? { width } : { minWidth }),
            backgroundColor: 'var(--ds-overlay-bg)',
            borderRadius: 'var(--ds-overlay-radius)',
            border: 'none',
            boxShadow: 'var(--ds-overlay-shadow)',
            overflow: 'hidden',
            maxHeight: 'none',
            mt: 'var(--ds-overlay-anchor-gap)',
            animation: 'overlaySurfaceEnter var(--ds-overlay-enter-duration) var(--ds-overlay-enter-easing)',
            '@keyframes overlaySurfaceEnter': {
              '0%': { opacity: 0, transform: 'scaleY(0.9) translateY(-8px)' },
              '100%': { opacity: 1, transform: 'scaleY(1) translateY(0)' },
            },
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            ...(((slotProps?.paper as any)?.sx as object | undefined) ?? {}),
          },
        },
      }}
    >
      <OverlayItemRoleContext.Provider value={itemRole}>{children}</OverlayItemRoleContext.Provider>
    </Menu>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlayItem — one row inside an OverlaySurface.
   Slots are named (icon / kbd / badge) rather than generic leading/trailing
   to match the legacy DropdownMenu item shape. The `selected` state is for
   value-pickers; action menus leave it false.
   ════════════════════════════════════════════════════════════════════════ */

export type OverlayItemSize = 'sm' | 'md';
export type OverlayItemTone = 'default' | 'danger';

const ITEM_FONT: Record<OverlayItemSize, string> = {
  sm: 'var(--ds-text-small)',
  md: 'var(--ds-text-body)',
};

const ITEM_PADDING: Record<OverlayItemSize, string> = {
  sm: 'var(--ds-overlay-item-padding-sm)',
  md: 'var(--ds-overlay-item-padding-md)',
};

const ITEM_TONE_COLOR: Record<OverlayItemTone, string> = {
  default: 'var(--ds-gray-700)',
  danger: 'var(--ds-red-600)',
};

export interface OverlayItemProps {
  /** Main label content. */
  children: React.ReactNode;
  size?: OverlayItemSize;
  tone?: OverlayItemTone;
  /** Selected state — value-pickers only (Select / MultiSelect / FilterDropdown). */
  selected?: boolean;
  disabled?: boolean;
  /** Leading icon slot. */
  icon?: React.ReactNode;
  /** Trailing keyboard shortcut hint (e.g. '⌘D'). */
  kbd?: React.ReactNode;
  /** Trailing badge (text or count). */
  badge?: React.ReactNode;
  onClick?: () => void;
  id?: string;
}

/**
 * OverlayItemLabel — string label that truncates with an ellipsis and shows a
 * tooltip carrying the full text *only when the text is actually clipped*.
 * Overflow is measured from the element on hover (scrollWidth > clientWidth)
 * rather than wrapping every row in a tooltip unconditionally. We read it off
 * the event target instead of a ref because Tooltip clones its child and owns
 * the child's ref. When not overflowing, `title=''` makes Tooltip a no-op.
 *
 * Open state is controlled so we can force-close on scroll: otherwise scrolling
 * the option list moves the hovered row away from the cursor while MUI keeps the
 * tooltip open and the Popper flips it up to stay in view — a tooltip left
 * floating above the panel detached from any row.
 */
function OverlayItemLabel({ children }: { children: string }) {
  const [overflowing, setOverflowing] = React.useState(false);
  const [open, setOpen] = React.useState(false);

  // While the tooltip is open, close it on any scroll. Capture phase so a
  // scroll inside the option list (an inner scroll container, which doesn't
  // bubble) triggers it too, not just window scroll.
  React.useEffect(() => {
    if (!(open && overflowing)) return;
    const close = () => setOpen(false);
    window.addEventListener('scroll', close, true);
    return () => window.removeEventListener('scroll', close, true);
  }, [open, overflowing]);

  return (
    <Tooltip
      title={overflowing ? children : ''}
      placement='top'
      open={open && overflowing}
      onOpen={() => setOpen(true)}
      onClose={() => setOpen(false)}
    >
      <Box
        component='span'
        onMouseEnter={(e) => {
          const el = e.currentTarget;
          setOverflowing(el.scrollWidth > el.clientWidth);
        }}
        sx={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
      >
        {children}
      </Box>
    </Tooltip>
  );
}

export function OverlayItem({
  children,
  size = 'md',
  tone = 'default',
  selected = false,
  disabled,
  icon,
  kbd,
  badge,
  onClick,
  id,
}: OverlayItemProps) {
  const hoverBg =
    tone === 'danger'
      ? 'var(--ds-overlay-item-danger-hover-bg)'
      : selected
      ? 'var(--ds-overlay-item-selected-bg)'
      : 'var(--ds-overlay-item-hover-bg)';
  const itemRole = React.useContext(OverlayItemRoleContext);

  return (
    <MenuItem
      id={id}
      // component='div' to match MenuList (also div). Role + aria-selected set
      // explicitly because the semantic <li> is gone.
      component='div'
      role={itemRole}
      aria-selected={itemRole === 'option' ? selected : undefined}
      disabled={disabled}
      onClick={onClick}
      sx={{
        padding: ITEM_PADDING[size],
        margin: '0 var(--ds-overlay-item-margin-x)',
        borderRadius: 'var(--ds-overlay-item-radius)',
        fontSize: ITEM_FONT[size],
        // Selected (value-picker) → blue text + medium weight. Danger always wins.
        color: tone === 'danger' ? ITEM_TONE_COLOR.danger : selected ? 'var(--ds-blue-600)' : ITEM_TONE_COLOR.default,
        fontWeight: selected ? 500 : 400,
        backgroundColor: selected ? 'var(--ds-overlay-item-selected-bg)' : 'transparent',
        gap: 'var(--ds-space-2)',
        display: 'flex',
        alignItems: 'center',
        transition: 'background var(--ds-motion-micro) var(--ds-motion-ease)',
        '&:hover': { backgroundColor: hoverBg },
      }}
    >
      {icon && (
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', color: 'inherit', flexShrink: 0 }}>
          {icon}
        </Box>
      )}
      {/* Label — truncates with an ellipsis instead of overflowing the panel
          width (which would force the scroll box's overflow-x to `auto` and
          show a horizontal scrollbar). String labels get a hover tooltip with
          the full text, but only when actually clipped (see OverlayItemLabel). */}
      {typeof children === 'string' ? (
        <OverlayItemLabel>{children}</OverlayItemLabel>
      ) : (
        <Box component='span' sx={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {children}
        </Box>
      )}
      {badge && (
        <Box
          component='span'
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            minWidth: '18px',
            height: '16px',
            padding: '0 5px',
            backgroundColor: 'var(--ds-gray-100)',
            color: 'var(--ds-gray-700)',
            fontSize: 'var(--ds-text-caption)',
            fontFamily: 'var(--ds-font-mono)',
            fontVariantNumeric: 'tabular-nums',
            borderRadius: '9px',
            flexShrink: 0,
          }}
        >
          {badge}
        </Box>
      )}
      {kbd && (
        <Box
          component='kbd'
          sx={{
            fontFamily: 'var(--ds-font-mono)',
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-500)',
            backgroundColor: 'var(--ds-gray-100)',
            border: '1px solid var(--ds-gray-200)',
            borderRadius: 'var(--ds-radius-sm)',
            padding: '1px 6px',
            flexShrink: 0,
          }}
        >
          {kbd}
        </Box>
      )}
    </MenuItem>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlaySection — uppercase header above a group of items.
   ════════════════════════════════════════════════════════════════════════ */

export interface OverlaySectionProps {
  children: React.ReactNode;
}

export function OverlaySection({ children }: OverlaySectionProps) {
  return (
    <Typography
      sx={{
        padding: 'var(--ds-space-2) 14px var(--ds-space-1)',
        fontSize: 'var(--ds-text-caption)',
        color: 'var(--ds-gray-700)',
        fontWeight: 'var(--ds-font-weight-semibold)',
        textTransform: 'uppercase',
        letterSpacing: '0.02em',
      }}
    >
      {children}
    </Typography>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlaySeparator — divider between groups.
   ════════════════════════════════════════════════════════════════════════ */

export function OverlaySeparator() {
  return (
    <Divider
      sx={{
        mx: 'calc(var(--ds-overlay-item-margin-x) * 2)',
        my: 'var(--ds-overlay-padding-y)',
        borderColor: 'var(--ds-gray-200)',
        borderBottomWidth: '0.5px',
      }}
    />
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlayCheckbox — small box-checkbox used by multi-select rows.
   16×16, blue-600 when checked, gray-300 1.5px border when unchecked.
   ════════════════════════════════════════════════════════════════════════ */

export function OverlayCheckbox({ checked }: { checked: boolean }) {
  return (
    <Box
      sx={{
        width: 16,
        height: 16,
        borderRadius: ds.radius.sm,
        border: checked ? 'none' : '1.5px solid var(--ds-gray-300)',
        backgroundColor: checked ? 'var(--ds-blue-600)' : 'transparent',
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
        transition: 'all 0.15s ease',
      }}
    >
      {checked && (
        <svg width='10' height='10' viewBox='0 0 10 10' fill='none'>
          <path d='M2 5L4.2 7L8 3' stroke='white' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' />
        </svg>
      )}
    </Box>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlayScrollBox — scroll container for an item list inside OverlaySurface.
   The Paper has overflow:hidden, so without this wrapper long lists clip
   instead of scroll. Default 260px max-height; styled 4px scrollbar.
   ════════════════════════════════════════════════════════════════════════ */

export interface OverlayScrollBoxProps {
  /** Max height before content scrolls. Default `'260px'`. */
  maxHeight?: string | number;
  children: React.ReactNode;
}

export function OverlayScrollBox({ maxHeight = '260px', children }: OverlayScrollBoxProps) {
  return (
    <Box
      sx={{
        maxHeight,
        overflowY: 'auto',
        paddingBottom: 'var(--ds-overlay-padding-y)',
        '&::-webkit-scrollbar': { width: '4px' },
        '&::-webkit-scrollbar-track': { background: 'transparent' },
        '&::-webkit-scrollbar-thumb': { background: 'var(--ds-gray-300)', borderRadius: ds.radius.sm },
        '&::-webkit-scrollbar-thumb:hover': { background: 'var(--ds-gray-400)' },
      }}
    >
      {children}
    </Box>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlayLoadingSkeleton — placeholder rows shown while items/options load.
   Mirrors OverlayItem geometry (padding + gap + optional leading checkbox
   slot) so the list doesn't shift when the real rows arrive. Spec caps
   skeleton lists at ~5 rows.
   ════════════════════════════════════════════════════════════════════════ */

const OVERLAY_SKELETON_WIDTHS = ['70%', '55%', '80%', '45%', '65%'];

export interface OverlayLoadingSkeletonProps {
  /** Number of placeholder rows. Default 5 (spec cap). */
  rows?: number;
  /** Matches OverlayItem size so the padding lines up. */
  size?: OverlayItemSize;
  /** Render a leading 16×16 box mirroring the multi-select checkbox slot. */
  showCheckbox?: boolean;
}

export function OverlayLoadingSkeleton({ rows = 5, size = 'md', showCheckbox = false }: OverlayLoadingSkeletonProps) {
  return (
    <Box role='status' aria-busy='true' aria-label='Loading'>
      {Array.from({ length: rows }).map((_, i) => (
        <Box
          key={i}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            padding: ITEM_PADDING[size],
            margin: '0 var(--ds-overlay-item-margin-x)',
          }}
        >
          {showCheckbox && <Skeleton shape='rect' width={16} height={16} />}
          <Skeleton shape='text' size='text' width={OVERLAY_SKELETON_WIDTHS[i % OVERLAY_SKELETON_WIDTHS.length]} />
        </Box>
      ))}
    </Box>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlaySearch — search input row pinned at the top of an OverlaySurface.
   Magnifier glyph absolutely positioned at the left; input is `--ds-gray-100`
   fill, focused state lifts to background-100 + blue-500 border + blue-100 halo.
   ════════════════════════════════════════════════════════════════════════ */

function SearchGlyph() {
  return (
    <svg
      width='12'
      height='12'
      viewBox='0 0 12 12'
      fill='none'
      style={{ opacity: 0.4, position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', pointerEvents: 'none' }}
    >
      <circle cx='5' cy='5' r='4' stroke='currentColor' strokeWidth='1.5' />
      <line x1='8' y1='8' x2='11' y2='11' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
    </svg>
  );
}

export interface OverlaySearchProps {
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  autoFocus?: boolean;
  onKeyDown?: React.KeyboardEventHandler<HTMLInputElement>;
}

export function OverlaySearch({ value, onChange, placeholder = 'Search…', autoFocus = true, onKeyDown }: OverlaySearchProps) {
  return (
    <Box sx={{ margin: '8px 10px 6px 10px', position: 'relative' }}>
      <SearchGlyph />
      <InputBase
        autoFocus={autoFocus}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        onKeyDown={onKeyDown}
        sx={{
          width: '100%',
          fontSize: 'var(--ds-text-body)',
          color: 'var(--ds-gray-700)',
          border: '1px solid var(--ds-gray-200)',
          backgroundColor: 'var(--ds-gray-100)',
          borderRadius: 'var(--ds-radius-md)',
          padding: '6px 10px 6px 28px',
          transition: 'all 0.15s ease',
          '&.Mui-focused': {
            backgroundColor: 'var(--ds-background-100)',
            borderColor: 'var(--ds-blue-500)',
            boxShadow: '0 0 0 3px var(--ds-blue-100)',
          },
          '& input::placeholder': { color: 'var(--ds-gray-500)', opacity: 1 },
          '& .MuiInputBase-input': { padding: 0 },
        }}
      />
    </Box>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   OverlaySelectAll — "Select All" / "Clear All" row for multi-select lists.
   Renders an OverlayCheckbox + label on the left and (optional) Clear All
   link on the right. Caller decides what "toggle" and "clear" mean.
   Followed by a thin divider.
   ════════════════════════════════════════════════════════════════════════ */

export interface OverlaySelectAllProps {
  checked: boolean;
  /** Called when the main checkbox / label is clicked. */
  onToggle: () => void;
  /** When true, render the right-side Clear All link. */
  showClear?: boolean;
  /** Called when the right-side Clear All link is clicked. */
  onClear?: () => void;
  /** Main label text. Default `'Select All'`. */
  label?: string;
}

export function OverlaySelectAll({ checked, onToggle, showClear, onClear, label = 'Select All' }: OverlaySelectAllProps) {
  return (
    <>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: 'var(--ds-overlay-item-padding-md)',
          margin: '0 var(--ds-overlay-item-margin-x)',
          borderRadius: 'var(--ds-overlay-item-radius)',
        }}
      >
        <Box
          component='button'
          type='button'
          onClick={onToggle}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            cursor: 'pointer',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-blue-600)',
            border: 'none',
            background: 'none',
            padding: 0,
          }}
        >
          <OverlayCheckbox checked={checked} />
          {label}
        </Box>
        {showClear && onClear && (
          <Box
            component='button'
            type='button'
            onClick={onClear}
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-blue-600)',
              border: 'none',
              background: 'none',
              cursor: 'pointer',
              padding: 0,
              '&:hover': { opacity: 0.7 },
            }}
          >
            Clear All
          </Box>
        )}
      </Box>
      <Box sx={{ borderBottom: '0.5px solid var(--ds-gray-200)', margin: '4px 10px' }} />
    </>
  );
}
