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

/* ════════════════════════════════════════════════════════════════════════
   OverlaySurface
   ════════════════════════════════════════════════════════════════════════ */

export type OverlayAlign = 'start' | 'end';
export type OverlaySide = 'bottom' | 'top' | 'left' | 'right';
export type OverlayRole = 'menu' | 'listbox';

/**
 * Item-role context — MenuList/MenuItem render as <div> (not the MUI default
 * <ul>/<li>) so non-item children like OverlaySearch / OverlayScrollBox are
 * valid HTML. With the semantic tags gone, ARIA roles have to be set
 * explicitly: 'menu' container → 'menuitem' children, 'listbox' container →
 * 'option' children. OverlaySurface provides; OverlayItem consumes.
 */
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
  /**
   * ARIA role for the underlying list.
   *   - 'menu'    → action menus (DropdownMenu) — items trigger callbacks
   *   - 'listbox' → value-pickers (Select / MultiSelect / FilterDropdown)
   */
  role?: OverlayRole;
  /**
   * Keep focus where the caller put it (e.g. on a search input above the
   * options list) instead of MUI's default of auto-focusing the first item.
   */
  disableAutoFocusItem?: boolean;
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
  children,
  slotProps,
}: OverlaySurfaceProps) {
  const itemRole = role === 'listbox' ? 'option' : 'menuitem';
  return (
    <Menu
      anchorEl={anchorEl}
      open={open}
      onClose={onClose}
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
      <Box component='span' sx={{ flex: 1 }}>
        {children}
      </Box>
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
        fontSize: '11px',
        color: 'var(--ds-gray-700)',
        fontWeight: 600,
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
        borderRadius: '4px',
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
        '&::-webkit-scrollbar': { width: '4px' },
        '&::-webkit-scrollbar-track': { background: 'transparent' },
        '&::-webkit-scrollbar-thumb': { background: 'var(--ds-gray-300)', borderRadius: '4px' },
        '&::-webkit-scrollbar-thumb:hover': { background: 'var(--ds-gray-400)' },
      }}
    >
      {children}
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
            fontWeight: 500,
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
              fontWeight: 500,
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
