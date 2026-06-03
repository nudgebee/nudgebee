/**
 * FilterDropdown — DS V2 port of legacy FilterDropdownButton.
 * Spec:        app/design-system/primitives/forms/filter-dropdown.html
 * Variants:    multiple   = boolean        (single vs multi-select chips with limitTag)
 *              grouped    = boolean        (renders option groups with headers)
 *              freeSolo   = boolean        (allow values not in options list)
 *
 * Migration:   `import FilterDropdownButton from '@components1/common/FilterDropdownButton'`
 *           →  `import FilterDropdown from '@components1/ds/FilterDropdown'`
 *
 *   API surface preserved verbatim from the legacy component (see the test at
 *   __tests__/components1/common/FilterDropdownButton.test.jsx — covers this
 *   component too). Colors swapped from `colors.*` legacy palette to `--ds-*`
 *   tokens; everything else (popover, virtualized list, search, chip
 *   rendering) is structurally identical.
 *
 * Don't (per spec):
 *   - Don't use FilterDropdown for form inputs — use Select/MultiSelect.
 *     This component is shaped for toolbar filter rows.
 *   - Don't put > ~200 options without confirming virtualization works for
 *     your option shape (the threshold lives in this file).
 *   - Don't render a single-select FilterDropdown when the user must always
 *     pick exactly one — Select reads as "form field", FilterDropdown reads
 *     as "filter that can be cleared".
 */
import React, { useState, useRef, useEffect, useMemo, useCallback } from 'react';
import { Box, Popover, Typography, InputBase } from '@mui/material';
import { Label } from '@components1/ds/Label';
import CustomTooltip from '@components1/ds/Tooltip';
import { ds } from '@utils/colors';
import PropTypes from 'prop-types';
import { toKebabCase } from '@utils/common';
import { OverlayCheckbox } from './internal/Overlay';
import { Skeleton } from './Skeleton';

const VIRTUALIZATION_THRESHOLD = 200;
const OPTION_HEIGHT = 36;
const OVERSCAN_COUNT = 10;

// Field chrome scale — mirrors ds/Input so FilterDropdown triggers align
// with Input rows when placed in a form/toolbar together.
const SIZE_HEIGHT = { sm: '32px', md: '36px', lg: '40px' };
const SIZE_PAD_X = { sm: 'var(--ds-space-3)', md: 'var(--ds-space-3)', lg: 'var(--ds-space-4)' };

// Safe key helper: falls back to label when getValue returns a non-primitive
const getOptionKey = (option, fallback) => {
  const val = getValue(option);
  const valStr = val != null && typeof val !== 'object' ? val : getLabel(option);
  return `${getLabel(option)}-${valStr}-${fallback}`;
};

const getLabel = (option) => {
  if (typeof option === 'string') return option;
  if (option == null) return '';
  return option.label || option.name || String(option.value ?? '');
};

const getValue = (option) => {
  if (typeof option === 'string') return option;
  if (option == null) return '';
  return option.value ?? option;
};

const ChevronIcon = ({ open = false }) => (
  <svg
    width='12'
    height='12'
    viewBox='0 0 10 10'
    fill='none'
    style={{
      opacity: 0.3,
      transition: 'transform 0.2s ease',
      transform: open ? 'rotate(180deg)' : 'rotate(0deg)',
      flexShrink: 0,
    }}
  >
    <path d='M2 3.5L5 6.5L8 3.5' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' />
  </svg>
);

const SearchIcon = () => (
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

const PlusIcon = () => (
  <svg width='12' height='12' viewBox='0 0 12 12' fill='none' style={{ opacity: 0.4 }}>
    <line x1='6' y1='2' x2='6' y2='10' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
    <line x1='2' y1='6' x2='10' y2='6' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
  </svg>
);

/**
 * OptionLabel — option-row label that truncates with an ellipsis and shows a
 * tooltip with the full text *only when clipped*. Open state is controlled so we
 * can force-close on scroll: otherwise scrolling the option list moves the
 * hovered row away from the cursor while MUI keeps the tooltip open and the
 * Popper flips it up to stay in view — a tooltip left floating above the panel.
 * Overflow is read off the event target (not a ref) because CustomTooltip clones
 * its child and owns the child's ref. Mirrors OverlayItemLabel in internal/Overlay.
 */
function OptionLabel({ label }) {
  const [overflowing, setOverflowing] = useState(false);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!(open && overflowing)) return undefined;
    const close = () => setOpen(false);
    // Capture phase so a scroll inside the option list (an inner scroll
    // container, which doesn't bubble) triggers it too, not just window scroll.
    window.addEventListener('scroll', close, true);
    return () => window.removeEventListener('scroll', close, true);
  }, [open, overflowing]);

  return (
    <CustomTooltip
      title={overflowing ? label : ''}
      placement='top'
      open={open && overflowing}
      onOpen={() => setOpen(true)}
      onClose={() => setOpen(false)}
    >
      <span
        onMouseEnter={(e) => {
          const el = e.currentTarget;
          setOverflowing(el.scrollWidth > el.clientWidth);
        }}
        style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'inherit' }}
      >
        {label}
      </span>
    </CustomTooltip>
  );
}

OptionLabel.propTypes = {
  label: PropTypes.string,
};

const OptionItem = React.memo(function OptionItem({ opt, selected, multiple, onToggle, style }) {
  const handleKeyDown = (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onToggle(opt);
    }
  };

  return (
    <Box
      role='option'
      tabIndex={0}
      aria-selected={selected}
      onClick={() => onToggle(opt)}
      onKeyDown={handleKeyDown}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: '10px',
        padding: 'var(--ds-overlay-item-padding-md)',
        margin: '0 var(--ds-overlay-item-margin-x)',
        borderRadius: 'var(--ds-overlay-item-radius)',
        cursor: 'pointer',
        fontSize: 'var(--ds-text-body)',
        fontWeight: selected ? 'var(--ds-font-weight-medium)' : 'var(--ds-font-weight-regular)',
        color: selected ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
        backgroundColor: selected ? 'var(--ds-overlay-item-selected-bg)' : 'transparent',
        transition: 'background var(--ds-motion-micro) var(--ds-motion-ease)',
        boxSizing: 'border-box',
        '&:hover': {
          backgroundColor: selected ? 'var(--ds-overlay-item-selected-bg)' : 'var(--ds-overlay-item-hover-bg)',
        },
        ...style,
      }}
    >
      {multiple && <OverlayCheckbox checked={selected} />}
      <OptionLabel label={getLabel(opt)} />
      {opt?.type && (
        <Box sx={{ ml: 'auto', flexShrink: 0 }}>
          <Label text={opt.type} />
        </Box>
      )}
    </Box>
  );
});

OptionItem.propTypes = {
  opt: PropTypes.any,
  selected: PropTypes.bool,
  multiple: PropTypes.bool,
  onToggle: PropTypes.func,
  style: PropTypes.object,
};

// Loading affordance: a short list of skeleton rows that mirror OptionItem's
// layout (optional checkbox + label) so the list doesn't shift when real
// options arrive. Spec caps skeleton lists at ~5 rows.
const SKELETON_ROW_COUNT = 5;
const SKELETON_LABEL_WIDTHS = ['70%', '55%', '80%', '45%', '65%'];

function OptionsLoadingSkeleton({ multiple }) {
  return (
    <Box aria-busy='true' aria-label='Loading options'>
      {Array.from({ length: SKELETON_ROW_COUNT }).map((_, idx) => (
        <Box
          key={idx}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '10px',
            padding: 'var(--ds-overlay-item-padding-md)',
            margin: '0 var(--ds-overlay-item-margin-x)',
            height: `${OPTION_HEIGHT}px`,
            boxSizing: 'border-box',
          }}
        >
          {multiple && <Skeleton shape='rect' width={16} height={16} />}
          <Skeleton shape='text' size='text' width={SKELETON_LABEL_WIDTHS[idx % SKELETON_LABEL_WIDTHS.length]} />
        </Box>
      ))}
    </Box>
  );
}

OptionsLoadingSkeleton.propTypes = {
  multiple: PropTypes.bool,
};

function OptionsList({ isOptionsLoading, filteredOptions, multiple, isSelected, handleToggle, handleClear, handleSelectAll }) {
  const scrollRef = useRef(null);
  const [scrollTop, setScrollTop] = useState(0);

  const handleScroll = useCallback((e) => {
    setScrollTop(e.currentTarget.scrollTop);
  }, []);

  // Reset scroll when options change (e.g., search)
  useEffect(() => {
    setScrollTop(0);
    if (scrollRef.current) {
      scrollRef.current.scrollTop = 0;
    }
  }, [filteredOptions]);

  const { selectedOptions, unselectedOptions } = useMemo(() => {
    const sel = filteredOptions.filter((opt) => isSelected(opt));
    const unsel = filteredOptions.filter((opt) => !isSelected(opt));
    return { selectedOptions: sel, unselectedOptions: unsel };
  }, [filteredOptions, isSelected]);

  const useVirtualization = unselectedOptions.length > VIRTUALIZATION_THRESHOLD;

  // Header height: "Selected" label (28px) + selected items + divider (18px)
  const selectedSectionHeight =
    selectedOptions.length > 0 ? 28 + selectedOptions.length * OPTION_HEIGHT + (unselectedOptions.length > 0 ? 18 : 0) : 0;

  // For virtualized mode, compute which unselected items are visible
  const virtualizedContent = useMemo(() => {
    if (!useVirtualization) {
      return null;
    }

    const totalHeight = selectedSectionHeight + unselectedOptions.length * OPTION_HEIGHT;
    const viewportHeight = 260;

    // Compute visible range of unselected items based on scroll position
    const unselectedScrollOffset = Math.max(0, scrollTop - selectedSectionHeight);
    const startIndex = Math.max(0, Math.floor(unselectedScrollOffset / OPTION_HEIGHT) - OVERSCAN_COUNT);
    const endIndex = Math.min(unselectedOptions.length, Math.ceil((unselectedScrollOffset + viewportHeight) / OPTION_HEIGHT) + OVERSCAN_COUNT);

    const topSpacerHeight = startIndex * OPTION_HEIGHT;
    const bottomSpacerHeight = Math.max(0, (unselectedOptions.length - endIndex) * OPTION_HEIGHT);

    return { totalHeight, startIndex, endIndex, topSpacerHeight, bottomSpacerHeight };
  }, [useVirtualization, scrollTop, selectedSectionHeight, unselectedOptions.length]);

  const scrollboxSx = {
    maxHeight: '260px',
    overflowY: 'auto',
    padding: 'var(--ds-overlay-padding-y) 0',
    '&::-webkit-scrollbar': { width: '4px' },
    '&::-webkit-scrollbar-track': { background: 'transparent' },
    '&::-webkit-scrollbar-thumb': { background: 'var(--ds-gray-300)', borderRadius: ds.radius.sm },
    '&::-webkit-scrollbar-thumb:hover': { background: 'var(--ds-gray-400)' },
  };

  if (isOptionsLoading) {
    return (
      <Box sx={scrollboxSx}>
        <OptionsLoadingSkeleton multiple={multiple} />
      </Box>
    );
  }

  if (filteredOptions.length === 0) {
    return (
      <Box sx={scrollboxSx}>
        <Typography sx={{ padding: '16px 14px', fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)', textAlign: 'center' }}>
          No results found
        </Typography>
      </Box>
    );
  }

  return (
    <Box ref={scrollRef} onScroll={handleScroll} sx={scrollboxSx}>
      {/* Select All — shown as a checkbox row in multiple mode */}
      {multiple && filteredOptions.length > 0 && (
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
              onClick={filteredOptions.every(isSelected) ? handleClear : handleSelectAll}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  filteredOptions.every(isSelected) ? handleClear(e) : handleSelectAll(e);
                }
              }}
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: '10px',
                cursor: 'pointer',
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-600)',
                transition: 'background 0.1s ease',
                border: 'none',
                background: 'none',
                padding: 0,
              }}
            >
              <OverlayCheckbox checked={filteredOptions.every(isSelected)} />
              Select All
            </Box>
            {selectedOptions.length > 0 && (
              <Typography
                role='button'
                tabIndex={0}
                onClick={handleClear}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleClear(e);
                  }
                }}
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-blue-600)',
                  cursor: 'pointer',
                  '&:hover': { opacity: 0.7 },
                }}
              >
                Clear All
              </Typography>
            )}
          </Box>
          <Box sx={{ borderBottom: '0.5px solid var(--ds-gray-200)', margin: '6px 10px' }} />
        </>
      )}

      {/* Selected section — always fully rendered (small count) */}
      {selectedOptions.length > 0 &&
        selectedOptions.map((opt, idx) => (
          <OptionItem
            key={`sel-${getOptionKey(opt, idx)}`}
            opt={opt}
            selected
            multiple={multiple}
            onToggle={handleToggle}
            style={{ height: `${OPTION_HEIGHT}px` }}
          />
        ))}

      {/* Unselected section — virtualized when large */}
      {useVirtualization ? (
        <>
          <div style={{ height: virtualizedContent.topSpacerHeight }} aria-hidden='true' />
          {unselectedOptions.slice(virtualizedContent.startIndex, virtualizedContent.endIndex).map((opt, i) => {
            const idx = virtualizedContent.startIndex + i;
            return (
              <OptionItem
                key={`unsel-${getOptionKey(opt, idx)}`}
                opt={opt}
                selected={false}
                multiple={multiple}
                onToggle={handleToggle}
                style={{ height: `${OPTION_HEIGHT}px` }}
              />
            );
          })}
          <div style={{ height: virtualizedContent.bottomSpacerHeight }} aria-hidden='true' />
        </>
      ) : (
        unselectedOptions.map((opt, idx) => (
          <OptionItem key={`unsel-${getOptionKey(opt, idx)}`} opt={opt} selected={false} multiple={multiple} onToggle={handleToggle} />
        ))
      )}
    </Box>
  );
}

OptionsList.propTypes = {
  isOptionsLoading: PropTypes.bool,
  filteredOptions: PropTypes.array,
  multiple: PropTypes.bool,
  isSelected: PropTypes.func,
  handleToggle: PropTypes.func,
  handleClear: PropTypes.func,
  handleSelectAll: PropTypes.func,
};

function snakeToTitleCase(str) {
  return str
    .split('_')
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

function GroupedOptionsList({
  isOptionsLoading,
  filteredOptions,
  multiple,
  isSelected,
  handleToggle,
  value,
  onSelect,
  groupIcon,
  selectionWithinGroup,
}) {
  const groups = useMemo(() => {
    const groupMap = new Map();
    filteredOptions.forEach((opt) => {
      const rawGroup = typeof opt === 'object' && opt.group ? opt.group : 'Other';
      const groupName = snakeToTitleCase(rawGroup);
      if (!groupMap.has(groupName)) groupMap.set(groupName, { options: [], rawGroup });
      groupMap.get(groupName).options.push(opt);
    });
    return Array.from(groupMap.entries()).map(([name, data]) => [name, data.options, data.rawGroup]);
  }, [filteredOptions]);

  const [openGroups, setOpenGroups] = useState({});

  const toggleGroup = useCallback((groupName) => {
    setOpenGroups((prev) => ({ ...prev, [groupName]: !prev[groupName] }));
  }, []);

  const handleGroupClear = useCallback(
    (groupOptions) => (e) => {
      e.stopPropagation();
      if (!onSelect) return;
      if (multiple) {
        const groupVals = new Set(groupOptions.map(getValue));
        const currentArr = Array.isArray(value) ? value : [];
        const newValue = currentArr.filter((v) => !groupVals.has(getValue(v)));
        onSelect({ target: { value: newValue } }, newValue);
      } else {
        onSelect({ target: { value: null } }, null);
      }
    },
    [value, onSelect, multiple]
  );

  const handleGroupSelectAll = useCallback(
    (groupOptions) => (e) => {
      e.stopPropagation();
      if (!onSelect || !multiple) return;
      if (selectionWithinGroup) {
        onSelect({ target: { value: groupOptions } }, groupOptions);
      } else {
        const currentArr = Array.isArray(value) ? value : [];
        const currentVals = new Set(currentArr.map(getValue));
        const toAdd = groupOptions.filter((opt) => !currentVals.has(getValue(opt)));
        onSelect({ target: { value: [...currentArr, ...toAdd] } }, [...currentArr, ...toAdd]);
      }
    },
    [value, onSelect, multiple, selectionWithinGroup]
  );

  const scrollboxSx = {
    maxHeight: '260px',
    overflowY: 'auto',
    paddingBottom: ds.space[1],
    '&::-webkit-scrollbar': { width: '4px' },
    '&::-webkit-scrollbar-track': { background: 'transparent' },
    '&::-webkit-scrollbar-thumb': { background: 'var(--ds-gray-300)', borderRadius: ds.radius.sm },
    '&::-webkit-scrollbar-thumb:hover': { background: 'var(--ds-gray-400)' },
  };

  if (isOptionsLoading) {
    return (
      <Box sx={scrollboxSx}>
        <OptionsLoadingSkeleton multiple={multiple} />
      </Box>
    );
  }

  if (filteredOptions.length === 0) {
    return (
      <Box sx={scrollboxSx}>
        <Typography sx={{ padding: '16px 14px', fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)', textAlign: 'center' }}>
          No results found
        </Typography>
      </Box>
    );
  }

  return (
    <Box sx={scrollboxSx}>
      {groups.map(([groupName, groupOptions, rawGroup]) => {
        const selectedInGroup = groupOptions.filter(isSelected);
        const unselectedInGroup = groupOptions.filter((opt) => !isSelected(opt));
        const hasGroupSelection = selectedInGroup.length > 0;
        const isOpen = !!openGroups[groupName];

        return (
          <Box key={groupName}>
            {/* Sticky group header with per-group actions */}
            <Box
              role='button'
              tabIndex={0}
              onClick={() => toggleGroup(groupName)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  toggleGroup(groupName);
                }
              }}
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '0 14px',
                height: `${OPTION_HEIGHT}px`,
                position: 'sticky',
                top: 0,
                zIndex: 10,
                backgroundColor: 'var(--ds-background-100)',
                cursor: 'pointer',
                '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
              }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                {groupIcon && typeof groupIcon === 'function' && groupIcon(rawGroup)}
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: 'var(--ds-gray-700)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.02em',
                  }}
                >
                  {groupName}
                </Typography>
                {hasGroupSelection && (
                  <Box
                    component='span'
                    sx={{
                      backgroundColor: 'var(--ds-blue-600)',
                      color: 'var(--ds-background-100)',
                      borderRadius: ds.radius.sm,
                      minWidth: '10px',
                      height: '18px',
                      padding: `0 ${ds.space[1]}`,
                      fontSize: '10px',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      display: 'inline-flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      cursor: 'default',
                    }}
                  >
                    {selectedInGroup.length}
                  </Box>
                )}
              </Box>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                {isOpen &&
                  (hasGroupSelection ? (
                    <Typography
                      role='button'
                      tabIndex={0}
                      onClick={handleGroupClear(groupOptions)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          handleGroupClear(groupOptions)(e);
                        }
                      }}
                      sx={{
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: 'var(--ds-blue-600)',
                        cursor: 'pointer',
                        '&:hover': { opacity: 0.7 },
                      }}
                    >
                      {multiple ? 'Clear All' : 'Clear'}
                    </Typography>
                  ) : (
                    multiple &&
                    unselectedInGroup.length > 0 && (
                      <Typography
                        role='button'
                        tabIndex={0}
                        onClick={handleGroupSelectAll(groupOptions)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            handleGroupSelectAll(groupOptions)(e);
                          }
                        }}
                        sx={{
                          fontSize: 'var(--ds-text-caption)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          color: 'var(--ds-blue-600)',
                          cursor: 'pointer',
                          '&:hover': { opacity: 0.7 },
                        }}
                      >
                        Select All
                      </Typography>
                    )
                  ))}
                <ChevronIcon open={isOpen} />
              </Box>
            </Box>

            {isOpen && (
              <Box sx={{ paddingLeft: ds.space[3] }}>
                {/* Selected items first */}
                {selectedInGroup.map((opt, idx) => (
                  <OptionItem key={`sel-${groupName}-${getOptionKey(opt, idx)}`} opt={opt} selected multiple={multiple} onToggle={handleToggle} />
                ))}

                {/* Divider between selected and unselected */}
                {selectedInGroup.length > 0 && unselectedInGroup.length > 0 && (
                  <Box sx={{ borderBottom: '0.5px solid var(--ds-gray-200)', margin: '6px 10px' }} />
                )}

                {/* Unselected items */}
                {unselectedInGroup.map((opt, idx) => (
                  <OptionItem
                    key={`unsel-${groupName}-${getOptionKey(opt, idx)}`}
                    opt={opt}
                    selected={false}
                    multiple={multiple}
                    onToggle={handleToggle}
                  />
                ))}
              </Box>
            )}
          </Box>
        );
      })}
    </Box>
  );
}

GroupedOptionsList.propTypes = {
  isOptionsLoading: PropTypes.bool,
  filteredOptions: PropTypes.array,
  multiple: PropTypes.bool,
  isSelected: PropTypes.func,
  handleToggle: PropTypes.func,
  value: PropTypes.any,
  onSelect: PropTypes.func,
  groupIcon: PropTypes.func,
  selectionWithinGroup: PropTypes.bool,
};

function TruncatedLabel({ label, maxWidth = '90px', color, fontWeight = 500, placement = 'top' }) {
  const spanRef = useRef(null);
  const [isOverflowing, setIsOverflowing] = useState(false);

  useEffect(() => {
    const el = spanRef.current;
    if (el) {
      setIsOverflowing(el.scrollWidth > el.clientWidth);
    }
  }, [label]);

  return (
    <CustomTooltip title={isOverflowing ? label : ''} placement={placement}>
      <span
        ref={spanRef}
        style={{
          color: color ?? 'var(--ds-blue-600)',
          fontWeight,
          maxWidth,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          display: 'inline-block',
          verticalAlign: 'middle',
          whiteSpace: 'nowrap',
        }}
      >
        {label}
      </span>
    </CustomTooltip>
  );
}

TruncatedLabel.propTypes = {
  label: PropTypes.string,
  maxWidth: PropTypes.string,
  color: PropTypes.string,
  fontWeight: PropTypes.number,
  placement: PropTypes.string,
};

function FilterDropdownButton({
  id,
  label,
  placeholder,
  options = [],
  value,
  multiple = false,
  grouped = false,
  groupIcon,
  selectionWithinGroup = false,
  freeSolo = false,
  popoverWidth,
  popoverAlign = 'left',
  onSelect,
  disabled = false,
  isOptionsLoading = false,
  limitTag = 1,
  sx = {},
  searchPlaceholder,
  required = false,
  size = 'sm',
}) {
  const [anchorEl, setAnchorEl] = useState(null);
  const [search, setSearch] = useState('');
  const searchRef = useRef(null);
  const open = Boolean(anchorEl);

  // Compute selected count and display text
  const selectedCount = useMemo(() => {
    if (multiple) {
      return Array.isArray(value) ? value.length : 0;
    }
    return value != null && value !== '' ? 1 : 0;
  }, [value, multiple]);

  const hasSelection = selectedCount > 0;

  // Pre-index options into a Map for O(1) value→label lookups.
  // Avoids O(N*M) linear scans during render when many items are selected.
  const optionLabelMap = useMemo(() => {
    const map = new Map();
    options.forEach((opt) => map.set(getValue(opt), getLabel(opt)));
    return map;
  }, [options]);

  // Resolve a raw value (e.g. an ID string) back to its display label
  // using the pre-indexed map. Falls back to getLabel(v) when the value
  // is already an option object or no matching option is found.
  const resolveLabel = useCallback(
    (v) => {
      if (v != null && typeof v !== 'object') {
        return optionLabelMap.get(v) ?? getLabel(v);
      }
      return getLabel(v);
    },
    [optionLabelMap]
  );

  // Build display values: max limitTag for multi, 1 for single
  const selectedDisplayText = useMemo(() => {
    if (!hasSelection) return null;
    if (multiple && Array.isArray(value)) {
      const visible = value.slice(0, limitTag).map((v) => resolveLabel(v));
      const hidden = value.slice(limitTag).map((v) => resolveLabel(v));
      return { labels: visible, extra: hidden.length, hiddenLabels: hidden };
    }
    return { labels: [resolveLabel(value)], extra: 0, hiddenLabels: [] };
  }, [value, multiple, hasSelection, limitTag, resolveLabel]);

  // Filter options by search.
  // Supports glob wildcards `*` (any sequence) and `?` (single char) — useful
  // for long index/label lists like `fluentk8s-prod-2026.04.03`. Plain queries
  // keep their existing case-insensitive substring semantics.
  const filteredOptions = useMemo(() => {
    const q = search.trim();
    if (!q) return options;
    const hasWildcard = /[*?]/.test(q);
    if (hasWildcard) {
      // Escape regex specials, then translate glob → regex.
      const escaped = q
        .replace(/[.+^${}()|[\]\\]/g, '\\$&')
        .replace(/\*/g, '.*')
        .replace(/\?/g, '.');
      try {
        const re = new RegExp(escaped, 'i');
        return options.filter((opt) => re.test(getLabel(opt)));
      } catch {
        // Fall through to substring match on regex compile failure.
      }
    }
    const lower = q.toLowerCase();
    return options.filter((opt) => getLabel(opt).toLowerCase().includes(lower));
  }, [options, search]);

  // Check if an option is selected
  const isSelected = useCallback(
    (opt) => {
      const optVal = getValue(opt);
      if (multiple && Array.isArray(value)) {
        return value.some((v) => getValue(v) === optVal);
      }
      if (!multiple && value != null) {
        const currentVal = typeof value === 'object' ? getValue(value) : value;
        return optVal === currentVal;
      }
      return false;
    },
    [multiple, value]
  );

  const handleToggle = (opt) => {
    if (!onSelect) return;

    if (multiple) {
      const optVal = getValue(opt);
      const currentArr = Array.isArray(value) ? value : [];
      let newValue;
      if (currentArr.some((v) => getValue(v) === optVal)) {
        newValue = currentArr.filter((v) => getValue(v) !== optVal);
      } else if (selectionWithinGroup && grouped) {
        const optGroup = opt?.group || 'Other';
        const sameGroupItems = currentArr.filter((v) => (v?.group || 'Other') === optGroup);
        newValue = [...sameGroupItems, opt];
      } else {
        newValue = [...currentArr, opt];
      }
      onSelect({ target: { value: newValue } }, newValue);
    } else {
      const newVal = opt?.value ?? opt;
      onSelect({ target: { value: newVal } }, opt);
      setAnchorEl(null);
    }
  };

  const handleClear = (e) => {
    e.stopPropagation();
    if (!onSelect) return;
    if (multiple) {
      onSelect({ target: { value: [] } }, []);
    } else {
      onSelect({ target: { value: null } }, null);
    }
  };

  const handleClearFiltered = (e) => {
    e.stopPropagation();
    if (!onSelect) return;
    if (multiple) {
      const currentArr = Array.isArray(value) ? value : [];
      const filteredVals = new Set(filteredOptions.map(getValue));
      const newValue = currentArr.filter((v) => !filteredVals.has(getValue(v)));
      onSelect({ target: { value: newValue } }, newValue);
    } else {
      onSelect({ target: { value: null } }, null);
    }
  };

  const handleSelectAllFiltered = (e) => {
    e.stopPropagation();
    if (!onSelect || !multiple) return;
    const currentArr = Array.isArray(value) ? value : [];
    const currentVals = new Set(currentArr.map(getValue));
    const toAdd = filteredOptions.filter((opt) => !currentVals.has(getValue(opt)));
    onSelect({ target: { value: [...currentArr, ...toAdd] } }, [...currentArr, ...toAdd]);
  };

  // freeSolo: check if search text can be added as a new custom value
  const canAddCustom = useMemo(() => {
    if (!freeSolo || !search.trim()) return false;
    const q = search.trim().toLowerCase();
    // Don't show "Add" if it already exists in options or selected values
    const existsInOptions = options.some((opt) => getLabel(opt).toLowerCase() === q);
    const existsInValue = multiple && Array.isArray(value) && value.some((v) => getLabel(v).toLowerCase() === q);
    return !existsInOptions && !existsInValue;
  }, [freeSolo, search, options, value, multiple]);

  const handleAddCustom = () => {
    if (!onSelect || !canAddCustom) return;
    const customVal = search.trim();
    if (multiple) {
      const currentArr = Array.isArray(value) ? value : [];
      const newValue = [...currentArr, customVal];
      onSelect({ target: { value: newValue } }, newValue);
    } else {
      onSelect({ target: { value: customVal } }, customVal);
      setAnchorEl(null);
    }
    setSearch('');
  };

  useEffect(() => {
    if (!open) setSearch('');
  }, [open]);

  const handleKeyDown = (e) => {
    if (e.key === 'Escape') {
      setAnchorEl(null);
    }
  };

  const inputId = toKebabCase(id || label || '');

  return (
    <>
      <Box
        component='button'
        type='button'
        id={inputId ? `auto-complete-${inputId}` : 'auto-complete'}
        onClick={(e) => !disabled && setAnchorEl(e.currentTarget)}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--ds-space-2)',
          minWidth: '150px',
          height: SIZE_HEIGHT[size],
          padding: `0 ${SIZE_PAD_X[size]}`,
          fontFamily: 'inherit',
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-regular)',
          lineHeight: 1.4,
          color: 'var(--ds-gray-700)',
          backgroundColor: disabled ? 'var(--ds-background-200)' : 'var(--ds-background-100)',
          border: `1px solid ${hasSelection ? 'var(--ds-blue-300)' : 'var(--ds-gray-300)'}`,
          borderRadius: 'var(--ds-radius-md)',
          boxShadow: hasSelection ? '0 0 0 3px var(--ds-blue-100)' : 'none',
          outline: 'none',
          cursor: disabled ? 'not-allowed' : 'pointer',
          transition: 'border-color 120ms ease, box-shadow 120ms ease, background-color 120ms ease',
          whiteSpace: 'nowrap',
          boxSizing: 'border-box',
          '&:hover': disabled
            ? undefined
            : {
                borderColor: hasSelection ? 'var(--ds-blue-500)' : 'var(--ds-gray-400)',
                backgroundColor: 'var(--ds-background-200)',
              },
          '&:focus-visible': {
            borderColor: 'var(--ds-blue-500)',
            boxShadow: '0 0 0 3px var(--ds-blue-100)',
          },
          ...(open && {
            borderColor: 'var(--ds-blue-500)',
            boxShadow: '0 0 0 3px var(--ds-blue-100)',
          }),
          ...sx,
        }}
      >
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', flex: 1, overflow: 'hidden', minWidth: 0 }}>
          {(label || (!hasSelection && placeholder) || isOptionsLoading) && (
            <span
              style={{
                color: 'var(--ds-gray-600)',
                fontWeight: 'var(--ds-font-weight-regular)',
                flexShrink: 0,
              }}
            >
              {label || (!hasSelection ? placeholder : '')}
              {required && (label || !hasSelection) && <span style={{ color: 'var(--ds-red-500)' }}> *</span>}
              {isOptionsLoading && (
                <span style={{ marginLeft: 'var(--ds-space-1)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontWeight: 400 }}>
                  ...
                </span>
              )}
            </span>
          )}
          {hasSelection && selectedDisplayText && (
            <>
              {selectedDisplayText.labels.map((lbl, idx) => (
                <React.Fragment key={lbl}>
                  {idx > 0 && <span style={{ color: 'var(--ds-gray-700)', fontWeight: 400 }}>, </span>}
                  <TruncatedLabel label={lbl} color='var(--ds-blue-500)' fontWeight={600} />
                </React.Fragment>
              ))}
              {selectedDisplayText.extra > 0 && (
                <CustomTooltip
                  variant='interactive'
                  title={
                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                      {selectedDisplayText.hiddenLabels.map((lbl) => (
                        <TruncatedLabel key={lbl} label={lbl} maxWidth='220px' color={'var(--ds-gray-700)'} fontWeight={400} placement='top' />
                      ))}
                    </Box>
                  }
                  placement='top'
                >
                  <Box
                    component='span'
                    sx={{
                      backgroundColor: 'var(--ds-gray-100)',
                      color: 'var(--ds-gray-700)',
                      border: '1px solid var(--ds-gray-200)',
                      borderRadius: ds.radius.sm,
                      padding: '0 5px',
                      fontSize: '10px',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      lineHeight: '16px',
                      display: 'inline-block',
                      cursor: 'default',
                    }}
                  >
                    +{selectedDisplayText.extra}
                  </Box>
                </CustomTooltip>
              )}
            </>
          )}
        </span>
        {hasSelection ? (
          <svg
            width='14'
            height='14'
            viewBox='0 0 12 12'
            fill='none'
            onClick={handleClear}
            style={{ opacity: 0.4, cursor: 'pointer', flexShrink: 0 }}
          >
            <line x1='3' y1='3' x2='9' y2='9' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
            <line x1='9' y1='3' x2='3' y2='9' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
          </svg>
        ) : (
          <ChevronIcon open={open} />
        )}
      </Box>

      <Popover
        open={open}
        anchorEl={anchorEl}
        onClose={() => setAnchorEl(null)}
        disablePortal
        anchorOrigin={{ vertical: 'bottom', horizontal: popoverAlign }}
        transformOrigin={{ vertical: 'top', horizontal: popoverAlign }}
        slotProps={{
          paper: {
            sx: {
              // Surface chrome shared via --ds-overlay-* tokens with DropdownMenu
              // (and Select / MultiSelect / Popover once those land).
              mt: 'var(--ds-overlay-anchor-gap)',
              backgroundColor: 'var(--ds-overlay-bg)',
              borderRadius: 'var(--ds-overlay-radius)',
              border: 'none',
              boxShadow: 'var(--ds-overlay-shadow)',
              width: popoverWidth ?? '220px',
              overflow: 'hidden',
              transformOrigin: 'top left',
              animation: 'filterDropdownSlideIn var(--ds-overlay-enter-duration) var(--ds-overlay-enter-easing)',
              '@keyframes filterDropdownSlideIn': {
                '0%': { opacity: 0, transform: 'scaleY(0.9) translateY(-8px)' },
                '100%': { opacity: 1, transform: 'scaleY(1) translateY(0)' },
              },
            },
          },
        }}
        onKeyDown={handleKeyDown}
      >
        {/* Search - show when more than 8 options, or always when freeSolo */}
        {(options.length > 8 || freeSolo) && (
          <Box sx={{ margin: '10px 10px 6px 10px', position: 'relative' }}>
            <SearchIcon />
            <InputBase
              inputRef={searchRef}
              autoFocus
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={searchPlaceholder || (freeSolo ? 'Search or type to add...' : 'Search...')}
              onKeyDown={(e) => {
                handleKeyDown(e);
                if (e.key === 'Enter') {
                  e.preventDefault();
                  if (canAddCustom) {
                    handleAddCustom();
                  } else if (filteredOptions.length > 0) {
                    // Select exact match first, otherwise select if only one result
                    const q = search.trim().toLowerCase();
                    const exactMatch = filteredOptions.find((opt) => getLabel(opt).toLowerCase() === q);
                    if (exactMatch) {
                      handleToggle(exactMatch);
                    } else if (filteredOptions.length === 1) {
                      handleToggle(filteredOptions[0]);
                    }
                  }
                }
              }}
              sx={{
                width: '100%',
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-gray-700)',
                boxShadow: '0 0 0 1px rgba(59, 130, 246, 0.15)',
                border: '1px solid var(--ds-gray-200)',
                backgroundColor: 'var(--ds-background-100)',
                borderRadius: ds.radius.md,
                padding: '7px 10px 7px 12px',
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

        {/* freeSolo: Add custom value option */}
        {canAddCustom && (
          <Box
            role='option'
            tabIndex={0}
            onClick={handleAddCustom}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                handleAddCustom();
              }
            }}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[2],
              padding: '8px 14px',
              cursor: 'pointer',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-blue-600)',
              borderBottom: '0.5px solid var(--ds-gray-200)',
              '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
            }}
          >
            <PlusIcon />
            <span>Add &ldquo;{search.trim()}&rdquo;</span>
          </Box>
        )}

        {/* Options */}
        {grouped ? (
          <GroupedOptionsList
            isOptionsLoading={isOptionsLoading}
            filteredOptions={filteredOptions}
            multiple={multiple}
            isSelected={isSelected}
            handleToggle={handleToggle}
            value={value}
            onSelect={onSelect}
            groupIcon={groupIcon}
            selectionWithinGroup={selectionWithinGroup}
          />
        ) : (
          <OptionsList
            isOptionsLoading={isOptionsLoading}
            filteredOptions={filteredOptions}
            multiple={multiple}
            isSelected={isSelected}
            handleToggle={handleToggle}
            handleClear={handleClearFiltered}
            handleSelectAll={handleSelectAllFiltered}
          />
        )}
      </Popover>
    </>
  );
}

/**
 * Styled "+N more filters" button per the design spec
 */
export function MoreFiltersButton({ count, expanded, onClick }) {
  return (
    <Box
      component='button'
      onClick={onClick}
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: ds.space[1],
        background: 'transparent',
        border: 'none',
        padding: `${ds.space[1]} ${ds.space[0]}`,
        fontSize: 'var(--ds-text-small)',
        fontWeight: 450,
        color: 'var(--ds-gray-500)',
        cursor: 'pointer',
        transition: 'color 0.15s ease',
        whiteSpace: 'nowrap',
        outline: 'none',
        '&:hover': {
          color: 'var(--ds-blue-600)',
          '& svg': { opacity: 0.9 },
        },
      }}
    >
      {!expanded && <PlusIcon />}
      {expanded ? 'Show less' : `${count} more filters`}
    </Box>
  );
}

ChevronIcon.propTypes = {
  open: PropTypes.bool,
};

FilterDropdownButton.propTypes = {
  id: PropTypes.string,
  label: PropTypes.string,
  placeholder: PropTypes.string,
  options: PropTypes.array,
  value: PropTypes.any,
  multiple: PropTypes.bool,
  grouped: PropTypes.bool,
  groupIcon: PropTypes.func,
  selectionWithinGroup: PropTypes.bool,
  freeSolo: PropTypes.bool,
  onSelect: PropTypes.func,
  disabled: PropTypes.bool,
  isOptionsLoading: PropTypes.bool,
  limitTag: PropTypes.number,
  sx: PropTypes.object,
  searchPlaceholder: PropTypes.string,
  required: PropTypes.bool,
  size: PropTypes.oneOf(['sm', 'md', 'lg']),
};
FilterDropdownButton.displayName = 'FilterDropdownButton';

MoreFiltersButton.propTypes = {
  count: PropTypes.number,
  expanded: PropTypes.bool,
  onClick: PropTypes.func,
};
MoreFiltersButton.displayName = 'MoreFiltersButton';

export default FilterDropdownButton;
