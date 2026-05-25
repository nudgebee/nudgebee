import React, { useMemo, useState, useEffect, useRef, forwardRef, useCallback } from 'react';
import { Autocomplete, TextField, Chip, Box, CircularProgress, Paper, Checkbox, InputAdornment } from '@mui/material';
import CheckIcon from '@mui/icons-material/Check';
import SearchIcon from '@mui/icons-material/Search';
import { MenuArrowDownIcon } from '@assets';
import CustomTooltip from './CustomTooltip';
import Text from './format/Text';
import { inputCustomSx, inputSx } from '@data/themes/inputField';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { snakeToTitleCase, toKebabCase } from 'src/utils/common';
import Tooltip from '@mui/material/Tooltip';
import SafeIcon from './SafeIcon';
import ClickAwayListener from '@mui/material/ClickAwayListener';

const OPTION_HEIGHT = 40;
const LISTBOX_PADDING = 8;
const MAX_VISIBLE_OPTIONS = 8;
const OVERSCAN_COUNT = 5;
const VIRTUALIZATION_THRESHOLD = 200;

const VirtualizedListbox = forwardRef(function VirtualizedListbox(props, ref) {
  const { children, ...other } = props;
  const items = React.Children.toArray(children);
  const itemCount = items.length;
  const listHeight = Math.min(itemCount * OPTION_HEIGHT + LISTBOX_PADDING * 2, MAX_VISIBLE_OPTIONS * OPTION_HEIGHT + LISTBOX_PADDING * 2);

  const [scrollTop, setScrollTop] = useState(0);

  const handleScroll = useCallback((e) => {
    setScrollTop(e.currentTarget.scrollTop);
  }, []);

  const startIndex = Math.max(0, Math.floor(scrollTop / OPTION_HEIGHT) - OVERSCAN_COUNT);
  const endIndex = Math.min(itemCount, Math.ceil((scrollTop + listHeight) / OPTION_HEIGHT) + OVERSCAN_COUNT);

  const topSpacerHeight = startIndex * OPTION_HEIGHT + LISTBOX_PADDING;
  const bottomSpacerHeight = Math.max(0, itemCount - endIndex) * OPTION_HEIGHT + LISTBOX_PADDING;

  return (
    <ul
      ref={ref}
      {...other}
      onScroll={handleScroll}
      style={{
        ...other.style,
        maxHeight: `${listHeight}px`,
        overflow: 'auto',
        margin: 0,
        padding: 0,
      }}
    >
      <div style={{ height: topSpacerHeight }} aria-hidden='true' />
      {items.slice(startIndex, endIndex)}
      <div style={{ height: bottomSpacerHeight }} aria-hidden='true' />
    </ul>
  );
});

const createSelectHandler = (onSelect) => (_event, value) => {
  if (!onSelect) {
    return;
  }

  const newEventObj = {
    target: {
      value: value?.value || value,
    },
  };
  onSelect(newEventObj, value);
};

const createOptionComparator = (multiple) => (option, value) => {
  // 1. Safety Check: If value is empty, nothing matches
  if (value === '' || value === null || value === undefined) {
    return false;
  }

  // 2. Handle Strings (Primitive comparison)
  if (typeof option === 'string') {
    return option === value;
  }

  // 3. Check for 'id' (Fixes your specific Switch Tenant issue)
  // We strictly check if 'id' exists to avoid undefined === undefined
  if (option.id !== undefined && value.id !== undefined) {
    return option.id === value.id;
  }

  // 4. Check for 'value' property (Standard Autocomplete behavior)
  if (option.value !== undefined && value.value !== undefined) {
    return option.value === value.value;
  }

  // 5. Single Select specific legacy checks
  if (!multiple) {
    if (option.value === value) {
      return true;
    }
    if (option.label === value) {
      return true;
    }
  }

  // 6. Fallback: Reference Equality (The Safe Fix)
  // Instead of returning false, we check if they are the exact same object instance.
  // This prevents the "undefined === undefined" bug while allowing valid object matches.
  return option === value;
};

const getOptionLabelSafe = (option) => {
  if (typeof option === 'string') {
    return option;
  }
  if (option == null) {
    return '';
  }
  if (Array.isArray(option)) {
    return option.map(getOptionLabelSafe).join(', ');
  }
  if (typeof option === 'object') {
    if (typeof option.label === 'string') {
      return option.label;
    }
    if (typeof option.name === 'string') {
      return option.name;
    }
    if (typeof option.value === 'string' || typeof option.value === 'number') {
      return String(option.value);
    }
    if (option.value && typeof option.value === 'object') {
      if (typeof option.value.name === 'string') {
        return option.value.name;
      }
      if (typeof option.value.display_name === 'string') {
        return option.value.display_name;
      }
    }
    try {
      return String(option);
    } catch {
      return '';
    }
  }
  return String(option);
};

function MultipleValueRenderer({ tagValue, getTagProps, onSelect, limitTags = 1 }) {
  const safeLimitTags = Number.isInteger(limitTags) && limitTags >= 0 ? limitTags : 1;
  const visibleTagValue = tagValue.slice(0, safeLimitTags);
  const hiddenTagValue = tagValue.slice(safeLimitTags);

  const allLabels = hiddenTagValue.map((tag, index) => {
    const originalIndex = index + safeLimitTags;
    const chipLabel = getOptionLabelSafe(tag) || `Option ${originalIndex + 1}`;
    return (
      <Box key={chipLabel}>
        <Chip
          label={chipLabel}
          size='small'
          sx={{
            my: '3px',
            backgroundColor: `${colors.background.primaryLightest} !important`,
            color: `${colors.text.secondary} !important`,
            '& .MuiChip-deleteIcon': {
              color: `${colors.text.tertiary} !important`,
              '&:hover': {
                color: `${colors.text.tertiaryLight} !important`,
              },
            },
          }}
          onDelete={() => {
            const newValue = tagValue.filter((_, i) => i !== index + 1);
            onSelect({ target: { value: newValue } }, newValue);
          }}
        />
      </Box>
    );
  });

  return (
    <div style={{ display: 'flex', alignItems: 'center' }}>
      {visibleTagValue.map((tag, index) => {
        const label = getOptionLabelSafe(tag) || `Option ${index + 1}`;

        return (
          <Box sx={{ maxWidth: '120px !important' }} key={`${index}-${label}`}>
            <Tooltip title={label} arrow placement='top'>
              <Chip
                size='small'
                label={<Text value={label} showAutoEllipsis fontSize='11px !important' />}
                {...getTagProps({ index })}
                sx={{
                  backgroundColor: colors.background.primaryLightest,
                  color: '#ffffff',
                  '& .MuiChip-deleteIcon': {
                    color: colors.text.tertiary,
                    '&:hover': {
                      color: colors.text.tertiaryLight,
                    },
                  },
                }}
                onDelete={() => {
                  const newValue = tagValue.filter((_, i) => i !== index);
                  onSelect({ target: { value: newValue } }, newValue);
                }}
              />
            </Tooltip>
          </Box>
        );
      })}
      {hiddenTagValue.length > 0 && (
        <CustomTooltip placement='bottom' title={<div>{allLabels}</div>} arrow>
          <span style={{ marginLeft: '4px', cursor: 'pointer' }}>{`+${hiddenTagValue.length}`}</span>
        </CustomTooltip>
      )}
    </div>
  );
}

MultipleValueRenderer.propTypes = {
  tagValue: PropTypes.arrayOf(
    PropTypes.oneOfType([
      PropTypes.string,
      PropTypes.shape({
        label: PropTypes.string,
        value: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
      }),
    ])
  ),
  getTagProps: PropTypes.func,
  onSelect: PropTypes.func,
};

MultipleValueRenderer.displayName = 'MultipleValueRenderer';

function createMultipleValuesRenderer(onSelect, limitTags) {
  function WrappedMultipleValuesRenderer(tagValue, getTagProps) {
    return <MultipleValueRenderer tagValue={tagValue} getTagProps={getTagProps} onSelect={onSelect} limitTags={limitTags} />;
  }
  WrappedMultipleValuesRenderer.displayName = 'WrappedMultipleValuesRenderer';
  return WrappedMultipleValuesRenderer;
}

// Custom Paper component for dynamic width dropdown

const CustomPaper = ({ children, onSearchChange, searchValue, onClickAway, showCustomSearch, ...props }) => {
  const inputRef = useRef(null);

  useEffect(() => {
    // focus search input when dropdown opens
    setTimeout(() => {
      inputRef.current?.focus();
    }, 0);
  }, []);
  return (
    <ClickAwayListener onClickAway={onClickAway}>
      <Paper
        {...props}
        sx={{
          ...props.sx,
          minWidth: 'fit-content',
          width: 'auto',
          maxWidth: '500px',
          marginTop: '4px',
          boxShadow: '0 4px 20px rgba(0, 0, 0, 0.08), 0 1px 3px rgba(0, 0, 0, 0.08)',
        }}
      >
        {showCustomSearch && (
          <Box
            sx={{
              p: 1,
              position: 'sticky',
              top: 0,
              backgroundColor: 'white',
              zIndex: 1,
              borderBottom: `1px solid ${colors.border.tertiary || '#eee'}`,
            }}
          >
            <TextField
              inputRef={inputRef}
              autoFocus
              size='small'
              fullWidth
              placeholder='Search...'
              value={searchValue}
              onChange={(e) => onSearchChange(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== 'Escape') {
                  e.stopPropagation();
                }
              }}
              InputProps={{
                startAdornment: (
                  <InputAdornment position='start'>
                    <SearchIcon fontSize='small' sx={{ color: colors.text.tertiary }} />
                  </InputAdornment>
                ),
              }}
              sx={{
                '& .MuiOutlinedInput-root': {
                  fontSize: '13px',
                  backgroundColor: colors.background.primaryLightest,
                  '& fieldset': { border: 'none' },
                },
              }}
            />
          </Box>
        )}
        {children}
      </Paper>
    </ClickAwayListener>
  );
};

/**
 * @typedef {string | number | { label: string, value: string | number }} AutocompleteOption
 */

/**
 * @typedef {Object} CustomAutocompleteProps
 * @property {string=} id
 * @property {boolean=} multiple
 * @property {string=} label
 * @property {AutocompleteOption | AutocompleteOption[] | null=} value
 * @property {AutocompleteOption[]=} options
 * @property {number|string=} width
 * @property {number|string=} minWidth
 * @property {boolean=} disabled
 * @property {number=} limitTags
 * @property {(event: { target: { value: any } }, value: any) => void=} onSelect
 * @property {boolean=} isOptionsLoading
 * @property {boolean=} grouped
 * @property {boolean=} isRequired
 * @property {Object=} customMenuItemStyles
 * @property {Object=} inputContainerSx
 */

/**
 * @param {CustomAutocompleteProps} props
 */

function CustomAutocomplete({
  id = '',
  multiple = false,
  label,
  value,
  options = [],
  width = 220,
  minWidth,
  disabled = false,
  limitTags = 1,
  onSelect,
  isOptionsLoading = false,
  grouped = false,
  isRequired = false,
  customMenuItemStyles = {},
  inputContainerSx = {},
  freeSolo = false,
  noOptionsText = 'No options available',
  renderOption: renderOptionProp,
  onInputChange,
}) {
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearchQuery, setDebouncedSearchQuery] = useState('');
  const [open, setOpen] = useState(false);
  const debounceTimerRef = useRef(null);

  const handleSearchChange = useCallback((value) => {
    setSearchQuery(value);
    if (debounceTimerRef.current) {
      clearTimeout(debounceTimerRef.current);
    }
    debounceTimerRef.current = setTimeout(() => {
      setDebouncedSearchQuery(value);
    }, 150);
  }, []);

  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current);
      }
    };
  }, []);

  const handleChange = useMemo(() => createSelectHandler(onSelect), [onSelect]);
  const isOptionEqualToValue = useMemo(() => createOptionComparator(multiple), [multiple]);
  const renderMultipleValues = useMemo(
    () => (multiple ? createMultipleValuesRenderer(onSelect, limitTags) : undefined),
    [multiple, onSelect, limitTags]
  );

  const filteredOptions = useMemo(() => {
    if (!debouncedSearchQuery) return options;
    const query = debouncedSearchQuery.toLowerCase();
    return options.filter((option) => getOptionLabelSafe(option).toLowerCase().includes(query));
  }, [options, debouncedSearchQuery]);

  const groupBy = grouped
    ? (option) => {
        if (typeof option === 'object' && option.group) {
          return snakeToTitleCase(option.group);
        }
        return 'Other';
      }
    : undefined;

  const inputId = toKebabCase(id || label || '');
  const rootRef = useRef(null);

  const useVirtualization = filteredOptions.length > VIRTUALIZATION_THRESHOLD;

  const paperDependencies = useRef();
  paperDependencies.current = {
    searchValue: searchQuery,
    onSearchChange: handleSearchChange,
    onClickAway: (event) => {
      if (rootRef.current?.contains(event.target)) return;
      setOpen(false);
      setSearchQuery('');
      setDebouncedSearchQuery('');
    },
    showCustomSearch: multiple && !freeSolo,
  };

  const MemoPaperComponent = useMemo(() => {
    return function PaperWrapper(paperProps) {
      return <CustomPaper {...paperProps} {...paperDependencies.current} />;
    };
  }, []);

  return (
    <Autocomplete
      ref={rootRef}
      open={open}
      onOpen={() => setOpen(true)}
      onClose={(event, reason) => {
        if (reason === 'blur') {
          if (event?.relatedTarget?.closest?.('.MuiAutocomplete-popper')) {
            return;
          }
        }
        setOpen(false);
        setSearchQuery('');
        setDebouncedSearchQuery('');
      }}
      size='small'
      id={inputId ? `auto-complete-${inputId}` : 'auto-complete'}
      multiple={multiple}
      value={multiple ? value ?? [] : value ?? null}
      options={filteredOptions}
      freeSolo={freeSolo}
      disabled={disabled}
      noOptionsText={noOptionsText}
      sx={{
        ...inputCustomSx,
        width: width,
        minWidth: minWidth,
        '& .MuiOutlinedInput-root': {
          ...(multiple && {
            padding: '4px 14px !important',
            height: '40px',
            flexWrap: 'nowrap',
            alignItems: 'center',
          }),
          ...inputContainerSx,
        },
      }}
      popupIcon={
        <SafeIcon
          src={MenuArrowDownIcon}
          alt='dropdown arrow'
          className='custom-dropdown-icon'
          style={{
            height: '18px',
            width: '18px',
            opacity: '60%',
          }}
        />
      }
      onChange={handleChange}
      onInputChange={onInputChange}
      blurOnSelect={false}
      selectOnFocus={false}
      disableCloseOnSelect={multiple}
      autoHighlight={false}
      autoSelect={false}
      handleHomeEndKeys={false}
      groupBy={groupBy}
      limitTags={limitTags}
      inputValue={multiple && !freeSolo ? '' : undefined}
      renderInput={(params) => {
        const hasValue = multiple ? Array.isArray(value) && value.length > 0 : Boolean(value);

        return (
          <TextField
            {...params}
            label={label}
            sx={inputSx}
            size='small'
            required={isRequired}
            InputLabelProps={{
              ...params.InputLabelProps,
              shrink: open || hasValue,
            }}
            InputProps={{
              ...params.InputProps,
              readOnly: multiple && !freeSolo,
              style: multiple && !freeSolo ? { caretColor: 'transparent' } : undefined,
              endAdornment: (
                <React.Fragment>
                  {isOptionsLoading ? <CircularProgress color='inherit' size={20} /> : null}
                  {params.InputProps.endAdornment}
                </React.Fragment>
              ),
            }}
          />
        );
      }}
      PaperComponent={MemoPaperComponent}
      {...(useVirtualization && { ListboxComponent: VirtualizedListbox })}
      ListboxProps={{
        sx: {
          fontSize: '13px',
          color: colors.text.secondary,
          padding: '8px 8px',
          '& .MuiAutocomplete-option': {
            whiteSpace: 'nowrap', // Prevent text wrapping
            padding: '6px 10px',
            borderRadius: '4px',
            ...(useVirtualization
              ? {
                  height: `${OPTION_HEIGHT}px`,
                  minHeight: `${OPTION_HEIGHT}px`,
                  boxSizing: 'border-box',
                  margin: 0,
                }
              : {
                  margin: '2px 0px', // Add gap between options
                }),
            '&:last-child': {
              borderBottom: 'none',
            },
            // Hover effect
            '&:hover': {
              backgroundColor: colors.background.hover,
              fontWeight: 500,
            },
            // Focused option styling
            '&.Mui-focused': {
              backgroundColor: colors.background.primaryLightest,
              color: colors.text.secondary,
              fontWeight: 500,
            },
            // Selected option styling - higher specificity to override focus
            '&[aria-selected="true"]': {
              backgroundColor: colors.background.primaryLightest,
              color: colors.text.primary,
              fontWeight: 500,
              '&:hover': {
                backgroundColor: colors.background.primaryLightest,
              },
              '&.Mui-focused': {
                backgroundColor: colors.background.primaryLightest,
                color: colors.text.primary,
                fontWeight: 500,
              },
            },
            // Merge custom styles
            ...customMenuItemStyles,
          },
        },
      }}
      renderOption={
        multiple
          ? (props, option, { selected }) => {
              // multiple-select: checkbox rendering (unchanged)
              const { key, ...restProps } = props;
              const uniqueKey = typeof option === 'string' ? option : option.value || option.label || key;
              return (
                <li {...restProps} key={uniqueKey} style={{ ...props.style, display: 'flex', alignItems: 'center' }}>
                  <Checkbox
                    icon={<Box sx={{ width: 18, height: 18, border: `1px solid ${colors.border.tertiary}`, borderRadius: '3px' }} />}
                    checkedIcon={
                      <CheckIcon
                        sx={{
                          width: 18,
                          height: 18,
                          color: colors.white,
                          backgroundColor: colors.background.primary || '#1976d2',
                          borderRadius: '3px',
                        }}
                      />
                    }
                    style={{ marginRight: 8, padding: 0 }}
                    checked={selected}
                  />
                  {typeof option === 'string' ? option : option.label || option.value}
                </li>
              );
            }
          : renderOptionProp || undefined
      }
      getOptionKey={(option) => (typeof option === 'object' && option?.id != null ? option.id : getOptionLabelSafe(option))}
      isOptionEqualToValue={isOptionEqualToValue}
      renderTags={renderMultipleValues}
      getOptionLabel={(option) => getOptionLabelSafe(option)}
    />
  );
}

CustomAutocomplete.propTypes = {
  id: PropTypes.string,
  multiple: PropTypes.bool,
  label: PropTypes.string,
  value: PropTypes.oneOfType([
    PropTypes.string,
    PropTypes.number,
    PropTypes.shape({
      label: PropTypes.string,
      value: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
    }),
    PropTypes.arrayOf(
      PropTypes.oneOfType([
        PropTypes.string,
        PropTypes.number,
        PropTypes.shape({
          label: PropTypes.string,
          value: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
        }),
      ])
    ),
  ]),
  options: PropTypes.arrayOf(
    PropTypes.oneOfType([
      PropTypes.string,
      PropTypes.number,
      PropTypes.shape({
        label: PropTypes.string,
        value: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
      }),
    ])
  ),
  width: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  minWidth: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  disabled: PropTypes.bool,
  limitTags: PropTypes.number,
  onSelect: PropTypes.func,
  isOptionsLoading: PropTypes.bool,
  grouped: PropTypes.bool,
  customMenuItemStyles: PropTypes.object,
  inputContainerSx: PropTypes.object,
  freeSolo: PropTypes.bool,
  noOptionsText: PropTypes.string,
  renderOption: PropTypes.func,
  onInputChange: PropTypes.func,
};

CustomAutocomplete.displayName = 'CustomAutocomplete';

export default CustomAutocomplete;
