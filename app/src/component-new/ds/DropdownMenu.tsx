/**
 * DropdownMenu — DS V2 of legacy CustomDropdown + ThreeDotsMenu + ButtonMenu.
 * Spec:        app/design-system/primitives/action/dropdown-menu.html
 * Variants:    align = 'start' | 'end'
 *              side  = 'bottom' | 'top' | 'left' | 'right'
 *              size  = 'sm' | 'md'
 *              item.tone = 'default' | 'danger'
 *              composition: items | sections | +kbd | +icons (auto from item shape)
 *              searchable = boolean (optional sticky search header — geometry/styling
 *                lifted verbatim from `ds/FilterDropdown` so the two
 *                primitives stay visually identical)
 *              onRefresh  = callback (renders a refresh icon button next to
 *                the search input; works with or without `searchable`)
 *
 * Composes the shared overlay primitives (`OverlaySurface`, `OverlayItem`,
 * `OverlaySection`, `OverlaySeparator`, `OverlayScrollBox`) so the popover
 * surface and items are byte-identical to Select / MultiSelect /
 * FilterDropdown / Popover.
 *
 * Migration:   `import CustomDropdown from '@common/CustomDropdown'`
 *              `import ThreeDotsMenu from '@common/ThreeDotsMenu'`
 *              `import ButtonMenu from '@common/ButtonMenu'`
 *           →  `import { DropdownMenu } from '@components1/ds/DropdownMenu'`
 *
 *   Per spec, ThreeDotsMenu and ButtonMenu are NOT separate primitives — they're
 *   `<DropdownMenu trigger={<Button ... />}>` with different trigger Buttons:
 *     <DropdownMenu trigger={<IconButton><MoreVertIcon /></IconButton>} />   ← was ThreeDotsMenu
 *     <DropdownMenu trigger={<Button endIcon={<KeyboardArrowDownIcon />}>Label</Button>} />  ← was ButtonMenu
 *
 * Don't (per spec):
 *   - Don't put > 7 items in one menu without sections.
 *   - Don't put a multi-step action behind a menu item — open a Dialog instead.
 *   - Don't nest DropdownMenus more than one level deep.
 */
import * as React from 'react';
import { Box, InputBase } from '@mui/material';
import {
  OverlayItem,
  OverlayScrollBox,
  OverlaySection,
  OverlaySeparator,
  OverlaySurface,
  type OverlayAlign,
  type OverlayItemSize,
  type OverlayItemTone,
  type OverlaySide,
} from './internal/Overlay';

export type DropdownMenuAlign = OverlayAlign;
export type DropdownMenuSide = OverlaySide;
export type DropdownMenuSize = OverlayItemSize;
export type DropdownMenuItemTone = OverlayItemTone;

export interface DropdownMenuItemAction {
  type?: 'item';
  label: React.ReactNode;
  icon?: React.ReactNode;
  /** Keyboard shortcut hint (right-aligned). e.g. "⌘D" */
  kbd?: string;
  tone?: DropdownMenuItemTone;
  disabled?: boolean;
  onSelect: () => void;
  id?: string;
  /**
   * Plain-text used by the built-in search header when `searchable` is set.
   * Items without `searchText` are *always* shown (use this for non-row
   * decorations like a disabled "No automations" placeholder).
   */
  searchText?: string;
}

export interface DropdownMenuSeparator {
  type: 'separator';
}

export interface DropdownMenuSection {
  type: 'section';
  label: string;
}

export type DropdownMenuItem = DropdownMenuItemAction | DropdownMenuSeparator | DropdownMenuSection;

export interface DropdownMenuProps {
  trigger: React.ReactElement;
  items: DropdownMenuItem[];
  align?: DropdownMenuAlign;
  side?: DropdownMenuSide;
  size?: DropdownMenuSize;
  /** Min-width of the menu panel */
  minWidth?: string | number;
  /**
   * Max height of the scrollable items region. Default `'260px'`. The
   * OverlaySurface Paper has `overflow:hidden`, so without this scroll
   * wrapper long item lists clip silently. Match `OverlayScrollBox` default.
   */
  itemsMaxHeight?: string | number;
  /**
   * Render a sticky search input at the top of the panel. Items with a
   * `searchText` field are filtered by case-insensitive substring match;
   * items without `searchText` are unfiltered.
   */
  searchable?: boolean;
  searchPlaceholder?: string;
  /** Header text shown above the input. Defaults to "Search…" when omitted. */
  /**
   * Renders a small refresh icon button at the right of the search header.
   * Independent of `searchable` — when set without `searchable`, the
   * header still shows so the refresh stays anchored.
   */
  onRefresh?: () => void;
  /** Tooltip / aria-label for the refresh button. Defaults to "Refresh". */
  refreshLabel?: string;
  /** Called after any item.onSelect (or after dismissal) */
  onClose?: () => void;
}

/* ────────────────────────────────────────────────────────────────────────
   Inline SVGs — matched to FilterDropdown's search header so the two
   primitives render the same affordances at the same weights/sizes.
   ──────────────────────────────────────────────────────────────────────── */
const SearchIcon: React.FC = () => (
  <svg
    width='12'
    height='12'
    viewBox='0 0 12 12'
    fill='none'
    style={{ opacity: 0.35, position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)' }}
  >
    <circle cx='5' cy='5' r='4' stroke='currentColor' strokeWidth='1.5' />
    <line x1='8' y1='8' x2='11' y2='11' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
  </svg>
);

const RefreshIconSvg: React.FC = () => (
  <svg width='12' height='12' viewBox='0 0 12 12' fill='none'>
    <path d='M10 6a4 4 0 1 1-1.17-2.83' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' fill='none' />
    <path d='M10 1.5V4H7.5' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' fill='none' />
  </svg>
);

export function DropdownMenu({
  trigger,
  items,
  align = 'start',
  side = 'bottom',
  size = 'md',
  minWidth = 200,
  itemsMaxHeight = '260px',
  searchable = false,
  searchPlaceholder = 'Search…',
  onRefresh,
  refreshLabel = 'Refresh',
  onClose,
}: DropdownMenuProps) {
  const [anchorEl, setAnchorEl] = React.useState<HTMLElement | null>(null);
  const [search, setSearch] = React.useState('');
  const open = Boolean(anchorEl);
  const showHeader = searchable || !!onRefresh;

  const close = () => {
    setAnchorEl(null);
    setSearch('');
    onClose?.();
  };

  const handleSelect = (item: DropdownMenuItemAction) => {
    if (item.disabled) return;
    item.onSelect();
    close();
  };

  const enhancedTrigger = React.cloneElement(trigger, {
    onClick: (e: React.MouseEvent<HTMLElement>) => {
      const existingOnClick = (trigger.props as { onClick?: (e: React.MouseEvent<HTMLElement>) => void }).onClick;
      existingOnClick?.(e);
      setAnchorEl(e.currentTarget);
    },
    'aria-haspopup': 'menu',
    'aria-expanded': open,
  });

  // Filter items by search when `searchable`. Items without `searchText`
  // (separators, sections, placeholder labels) pass through unchanged.
  const visibleItems = React.useMemo(() => {
    if (!searchable) return items;
    const q = search.trim().toLowerCase();
    if (!q) return items;
    return items.filter((item) => {
      if (item.type === 'separator' || item.type === 'section') return true;
      if (item.searchText == null) return true;
      return item.searchText.toLowerCase().includes(q);
    });
  }, [items, search, searchable]);

  return (
    <>
      {enhancedTrigger}
      <OverlaySurface anchorEl={anchorEl} open={open} onClose={close} align={align} side={side} minWidth={minWidth} role='menu'>
        {showHeader && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              margin: '10px 10px 6px 10px',
            }}
          >
            {searchable && (
              <Box sx={{ position: 'relative', flex: 1 }}>
                <SearchIcon />
                <InputBase
                  autoFocus
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder={searchPlaceholder}
                  // Stop MenuList / parent-Menu type-to-select from eating keystrokes
                  // bound for the input. Esc still bubbles through Popover handling.
                  onKeyDown={(e) => {
                    if (e.key !== 'Escape') e.stopPropagation();
                  }}
                  sx={{
                    width: '100%',
                    fontSize: '13px',
                    color: 'var(--ds-gray-700)',
                    boxShadow: '0 0 0 1px rgba(59, 130, 246, 0.15)',
                    border: '1px solid var(--ds-gray-200)',
                    backgroundColor: 'var(--ds-gray-100)',
                    borderRadius: '6px',
                    padding: '7px 10px 7px 28px',
                    transition: 'all 0.15s ease',
                    '&.Mui-focused': {
                      backgroundColor: 'var(--ds-background-100)',
                      boxShadow: '0 0 0 2px rgba(59, 130, 246, 0.3)',
                    },
                    '& input::placeholder': {
                      color: 'var(--ds-gray-500)',
                      opacity: 1,
                    },
                    '& .MuiInputBase-input': {
                      padding: 0,
                    },
                  }}
                />
              </Box>
            )}
            {onRefresh && (
              <Box
                component='button'
                type='button'
                onClick={(e) => {
                  e.stopPropagation();
                  onRefresh();
                }}
                aria-label={refreshLabel}
                title={refreshLabel}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  width: '28px',
                  height: '28px',
                  flexShrink: 0,
                  cursor: 'pointer',
                  color: 'var(--ds-gray-600)',
                  border: '1px solid var(--ds-gray-200)',
                  borderRadius: '6px',
                  background: 'var(--ds-background-100)',
                  transition: 'all 0.15s ease',
                  '&:hover': {
                    color: 'var(--ds-blue-600)',
                    borderColor: 'var(--ds-blue-300)',
                    backgroundColor: 'var(--ds-blue-50, var(--ds-gray-100))',
                  },
                }}
              >
                <RefreshIconSvg />
              </Box>
            )}
          </Box>
        )}
        <OverlayScrollBox maxHeight={itemsMaxHeight}>
          {visibleItems.map((item, i) => {
            if (item.type === 'separator') {
              return <OverlaySeparator key={`sep-${i}`} />;
            }
            if (item.type === 'section') {
              return <OverlaySection key={`section-${i}-${item.label}`}>{item.label}</OverlaySection>;
            }
            return (
              <OverlayItem
                key={item.id ?? `item-${i}`}
                size={size}
                tone={item.tone}
                disabled={item.disabled}
                icon={item.icon}
                kbd={item.kbd}
                id={item.id}
                onClick={() => handleSelect(item)}
              >
                {item.label}
              </OverlayItem>
            );
          })}
        </OverlayScrollBox>
      </OverlaySurface>
    </>
  );
}

export default DropdownMenu;
