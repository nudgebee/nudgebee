/**
 * FilterGroup — DS V2 (kept name).
 * Spec: app/design-system/primitives/feedback/filter-group.html
 *
 * A row of removable filter Chips with a leading "Filters" affordance.
 * Composes Chip (dismissible) and DropdownMenu. Not a layout, not a shell —
 * the row itself.
 *
 * Variants per spec:
 *   composition = 'chips' | 'add+chips' | 'add+chips+clear'
 *                 (auto from `onAdd` + `onClear` + filter-count presence)
 *   overflow    = 'wrap' | 'more-menu'
 *   size        = 'sm' | 'md'
 *
 * Don't (per spec):
 *   - Don't show "Clear all" until at least one filter is applied — empty
 *     action is noise.
 *   - Don't combine FilterGroup with a SearchInput on the same row without a
 *     divider — see SearchInput → Don't.
 *
 * Migration:
 *   `import FilterGroup from '@components1/common/FilterGroup'`
 * → `import { FilterGroup } from '@components1/ds/FilterGroup'`
 *   Chip composition replaces ad-hoc buttons.
 */
import * as React from 'react';
import { Box, ButtonBase, Menu, MenuItem } from '@mui/material';
import FilterListIcon from '@mui/icons-material/FilterList';
import CloseIcon from '@mui/icons-material/Close';
import MoreHorizIcon from '@mui/icons-material/MoreHoriz';

export type FilterGroupSize = 'sm' | 'md';
export type FilterGroupOverflow = 'wrap' | 'more-menu';

export interface FilterGroupChip {
  /** Stable identity used for remove callbacks. */
  id: string;
  /** Display label, e.g. "severity = high". */
  label: React.ReactNode;
}

export interface FilterGroupAvailableFilter {
  id: string;
  label: React.ReactNode;
  /** Disabled in the menu (e.g. already applied). */
  disabled?: boolean;
}

export interface FilterGroupProps {
  filters: FilterGroupChip[];
  /** Fires when an unapplied filter is selected from the "Filters" menu. Required for `add+...` compositions. */
  onAdd?: (filter: FilterGroupAvailableFilter) => void;
  onRemove: (chip: FilterGroupChip) => void;
  /** Required for the `add+chips+clear` composition. Spec: don't show until ≥ 1 filter applied. */
  onClear?: () => void;
  /** Filter options surfaced by the leading "Filters" button. */
  availableFilters?: FilterGroupAvailableFilter[];
  /** wrap (default) flows chips to next line; more-menu collapses overflow into a "+N" menu. */
  overflow?: FilterGroupOverflow;
  /** Max chips to show inline before collapsing into the more-menu. Used when overflow='more-menu'. */
  maxInline?: number;
  size?: FilterGroupSize;
  className?: string;
  id?: string;
}

const SIZE_TOKENS: Record<FilterGroupSize, { chipHeight: string; fontSize: string; gap: string; padX: string; iconSize: number }> = {
  sm: { chipHeight: '20px', fontSize: 'var(--ds-text-caption)', gap: '6px', padX: '8px', iconSize: 10 },
  md: { chipHeight: '24px', fontSize: 'var(--ds-text-small)', gap: '8px', padX: '10px', iconSize: 12 },
};

export function FilterGroup({
  filters,
  onAdd,
  onRemove,
  onClear,
  availableFilters,
  overflow = 'wrap',
  maxInline = 6,
  size = 'md',
  className,
  id,
}: FilterGroupProps) {
  const tokens = SIZE_TOKENS[size];
  const [addAnchor, setAddAnchor] = React.useState<HTMLElement | null>(null);
  const [moreAnchor, setMoreAnchor] = React.useState<HTMLElement | null>(null);

  const showAdd = onAdd !== undefined && availableFilters && availableFilters.length > 0;
  // Spec: don't show "Clear all" until ≥ 1 filter applied.
  const showClear = onClear !== undefined && filters.length > 0;

  const inlineChips = overflow === 'more-menu' ? filters.slice(0, maxInline) : filters;
  const overflowChips = overflow === 'more-menu' ? filters.slice(maxInline) : [];

  return (
    <Box
      id={id}
      className={className}
      sx={{
        display: 'flex',
        alignItems: 'center',
        flexWrap: overflow === 'wrap' ? 'wrap' : 'nowrap',
        gap: tokens.gap,
        width: '100%',
      }}
    >
      {showAdd && (
        <ButtonBase
          onClick={(e) => setAddAnchor(e.currentTarget)}
          aria-haspopup='menu'
          aria-expanded={Boolean(addAnchor)}
          sx={{
            height: tokens.chipHeight,
            padding: `0 ${tokens.padX}`,
            fontSize: tokens.fontSize,
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-gray-700)',
            backgroundColor: 'var(--ds-background-100)',
            border: '1px solid var(--ds-gray-300)',
            borderRadius: 'var(--ds-radius-sm)',
            display: 'inline-flex',
            alignItems: 'center',
            gap: '4px',
            flexShrink: 0,
            '&:hover': { borderColor: 'var(--ds-gray-400)', backgroundColor: 'var(--ds-gray-100)' },
            '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
          }}
        >
          <FilterListIcon sx={{ fontSize: tokens.iconSize }} />
          Filters
        </ButtonBase>
      )}

      {inlineChips.map((chip) => (
        <Box
          key={chip.id}
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '4px',
            height: tokens.chipHeight,
            padding: `0 4px 0 ${tokens.padX}`,
            fontSize: tokens.fontSize,
            color: 'var(--ds-blue-700)',
            backgroundColor: 'var(--ds-blue-100)',
            border: '1px solid var(--ds-blue-200)',
            borderRadius: 'var(--ds-radius-pill)',
            flexShrink: 0,
          }}
        >
          <Box component='span'>{chip.label}</Box>
          <ButtonBase
            aria-label={`Remove filter ${typeof chip.label === 'string' ? chip.label : chip.id}`}
            onClick={() => onRemove(chip)}
            sx={{
              width: '16px',
              height: '16px',
              borderRadius: '50%',
              color: 'var(--ds-blue-700)',
              '&:hover': { backgroundColor: 'var(--ds-blue-200)' },
              '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
            }}
          >
            <CloseIcon sx={{ fontSize: tokens.iconSize }} />
          </ButtonBase>
        </Box>
      ))}

      {overflowChips.length > 0 && (
        <>
          <ButtonBase
            onClick={(e) => setMoreAnchor(e.currentTarget)}
            aria-haspopup='menu'
            aria-expanded={Boolean(moreAnchor)}
            sx={{
              height: tokens.chipHeight,
              padding: `0 ${tokens.padX}`,
              fontSize: tokens.fontSize,
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-700)',
              backgroundColor: 'var(--ds-background-100)',
              border: '1px solid var(--ds-gray-300)',
              borderRadius: 'var(--ds-radius-pill)',
              display: 'inline-flex',
              alignItems: 'center',
              gap: '4px',
              flexShrink: 0,
              '&:hover': { borderColor: 'var(--ds-gray-400)', backgroundColor: 'var(--ds-gray-100)' },
              '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
            }}
          >
            <MoreHorizIcon sx={{ fontSize: tokens.iconSize }} />+{overflowChips.length}
          </ButtonBase>
          <Menu
            anchorEl={moreAnchor}
            open={Boolean(moreAnchor)}
            onClose={() => setMoreAnchor(null)}
            anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
            transformOrigin={{ vertical: 'top', horizontal: 'left' }}
          >
            {overflowChips.map((chip) => (
              <MenuItem
                key={chip.id}
                onClick={() => {
                  onRemove(chip);
                  setMoreAnchor(null);
                }}
                sx={{ fontSize: tokens.fontSize, gap: '8px' }}
              >
                <Box component='span' sx={{ flex: 1 }}>
                  {chip.label}
                </Box>
                <CloseIcon sx={{ fontSize: tokens.iconSize, color: 'var(--ds-gray-500)' }} />
              </MenuItem>
            ))}
          </Menu>
        </>
      )}

      {showClear && (
        <ButtonBase
          onClick={onClear}
          sx={{
            marginLeft: 'auto',
            height: tokens.chipHeight,
            padding: `0 ${tokens.padX}`,
            fontSize: tokens.fontSize,
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-blue-600)',
            borderRadius: 'var(--ds-radius-sm)',
            '&:hover': { color: 'var(--ds-blue-700)', backgroundColor: 'var(--ds-blue-100)' },
            '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
          }}
        >
          Clear all
        </ButtonBase>
      )}

      {showAdd && (
        <Menu
          anchorEl={addAnchor}
          open={Boolean(addAnchor)}
          onClose={() => setAddAnchor(null)}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
          transformOrigin={{ vertical: 'top', horizontal: 'left' }}
          slotProps={{
            paper: {
              sx: {
                minWidth: '180px',
                borderRadius: 'var(--ds-radius-md)',
                border: '1px solid var(--ds-gray-200)',
                boxShadow: '0px 4px 20px var(--ds-gray-alpha-200)',
                marginTop: '4px',
              },
            },
          }}
        >
          {availableFilters!.map((f) => (
            <MenuItem
              key={f.id}
              disabled={f.disabled}
              onClick={() => {
                onAdd!(f);
                setAddAnchor(null);
              }}
              sx={{ fontSize: tokens.fontSize, color: 'var(--ds-gray-700)' }}
            >
              {f.label}
            </MenuItem>
          ))}
        </Menu>
      )}
    </Box>
  );
}

export default FilterGroup;
