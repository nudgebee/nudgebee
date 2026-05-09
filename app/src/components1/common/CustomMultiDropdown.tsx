import React, { useState, useRef, useEffect } from 'react';
import { InputLabel, Select, MenuItem, FormControl, Stack, Chip, Box, ListSubheader, TextField, CircularProgress } from '@mui/material';
import CheckIcon from '@mui/icons-material/Check';
import CancelIcon from '@mui/icons-material/Cancel';
import CustomDropdownIcon from '@components1/common/CustomDropdownIcon';
import CustomTooltip from '@components1/common/CustomTooltip';
import { colors } from 'src/utils/colors';
import { inputSx } from '@data/themes/inputField';

interface Option {
  value: string;
  label: string;
  id?: string;
}

type Value = string | Option;

interface CustomMultiDropdownProps {
  value: Value[];
  onChange: (event: React.ChangeEvent<{ value: unknown }>) => void;
  options: Value[];
  color?: string;
  label?: string;
  minWidth?: string;
  ml?: string;
  mr?: string;
  mt?: string;
  mb?: string;
  size?: any;
  minHeight?: string;
  handleCloseIcon: (updatedValue: Value[]) => void;
  inputLabelSx?: any;
  maxWidth?: string;
  id?: string;
  isRequired?: boolean;
  disabled?: boolean;
  enableSearch?: boolean;
  limitTags?: number;
  isLoading?: boolean;
}

const CustomMultiDropdown: React.FC<CustomMultiDropdownProps> = ({
  value,
  onChange,
  options,
  color,
  label = '',
  minWidth = '150px',
  ml,
  mr,
  mt,
  mb,
  size = 'small',
  minHeight = '33px',
  inputLabelSx = { color: colors.text.secondary, fontSize: '12px', fontWeight: 500 },
  handleCloseIcon,
  maxWidth = '800px',
  id = '',
  isRequired = false,
  disabled = false,
  enableSearch = false,
  limitTags = 1,
  isLoading = false,
}) => {
  const searchInputRef = useRef<HTMLInputElement>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [isMenuOpen, setIsMenuOpen] = useState(false);
  const selectRef = useRef<any>(null);

  const isObject = (item: any): item is Option => typeof item === 'object' && item !== null;

  const filteredOptions = enableSearch
    ? options.filter((opt) => {
        const optLabel = isObject(opt) ? opt.label : String(opt);
        return optLabel.toLowerCase().includes(searchQuery.toLowerCase());
      })
    : options;

  // Focus management
  useEffect(() => {
    if (isMenuOpen && enableSearch && searchInputRef.current) {
      const input = searchInputRef.current;
      requestAnimationFrame(() => {
        input.focus();
      });
    }
  }, [isMenuOpen, enableSearch, searchQuery]);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (selectRef.current && !selectRef.current.contains(event.target)) {
        if (searchInputRef.current && searchInputRef.current.contains(event.target as Node)) {
          return;
        }
        setIsMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
    };
  }, []);

  return (
    <FormControl size={size} sx={{ borderRadius: '20px', minWidth, ml, mr, mb, mt, position: 'relative', minHeight }} ref={selectRef}>
      <InputLabel sx={inputLabelSx} id={id ? `multi-select-input-${id}` : 'demo-simple-select-label'} required={isRequired}>
        {label}
      </InputLabel>
      <Select
        labelId={id ? `multi-select-input-${id}` : 'demo-simple-select-label'}
        id={id ? `multi-select-${id}` : 'demo-simple-select'}
        multiple
        value={value.map((e) => (isObject(e) ? e.value : e))}
        label={label}
        onChange={onChange as any}
        size={size}
        open={isMenuOpen}
        onOpen={() => {
          setIsMenuOpen(true);
          setSearchQuery('');
        }}
        onClose={() => {
          setIsMenuOpen(false);
          setSearchQuery('');
        }}
        MenuProps={{
          autoFocus: false,
          keepMounted: true,
          disablePortal: true,
          anchorOrigin: {
            vertical: 'bottom',
            horizontal: 'left',
          },
          transformOrigin: {
            vertical: 'top',
            horizontal: 'left',
          },
          TransitionProps: {
            onEntered: () => {
              if (enableSearch && searchInputRef.current) {
                searchInputRef.current.focus();
              }
            },
          },
        }}
        disabled={options.length === 0 || disabled}
        IconComponent={(props) => <CustomDropdownIcon color={color ?? colors.text.secondary} props={props} />}
        sx={{
          backgroundColor: color ?? '',
          color: colors.text.secondary,
          fontWeight: 500,
          '.MuiOutlinedInput-notchedOutline': {
            border: `0.5px solid ${colors.border.secondary}`,
            borderColor: colors.border.secondary,
            borderRadius: '6px',
          },
          '.MuiOutlinedInput-input': { padding: '6px 15px' },
          maxWidth: maxWidth,
          minHeight: minHeight,
        }}
        renderValue={(selected: string[]) => {
          const visibleSelected = selected.slice(0, limitTags);
          const hiddenSelected = selected.slice(limitTags);

          const hiddenChips = hiddenSelected.map((selectedValue) => {
            const originalOption = options.find((opt) => (isObject(opt) ? opt.value === selectedValue : String(opt) === selectedValue));
            const displayLabel = originalOption ? (isObject(originalOption) ? originalOption.label : String(originalOption)) : selectedValue;

            return (
              <Box key={selectedValue} sx={{ mb: 1 }}>
                <Chip
                  size='small'
                  label={displayLabel}
                  onDelete={() => {
                    const newValue = value.filter((item) => (isObject(item) ? item.value !== selectedValue : String(item) !== selectedValue));
                    handleCloseIcon(newValue);
                  }}
                  deleteIcon={<CancelIcon onMouseDown={(event) => event.stopPropagation()} />}
                  sx={{
                    height: '28px !important',
                    backgroundColor: `${colors.background.primaryLightest} !important`,
                    color: `${colors.text.secondary} !important`,
                    '& .MuiChip-deleteIcon': {
                      width: '20px !important',
                      height: '20px !important',
                      color: `${colors.text.tertiary} !important`,
                      '&:hover': {
                        color: `${colors.text.tertiaryLight} !important`,
                      },
                    },
                  }}
                />
              </Box>
            );
          });

          return (
            <Stack gap={1} direction='row' flexWrap='wrap' alignItems='center' sx={{ pr: value.length > 0 && !disabled ? '40px' : '0px' }}>
              {visibleSelected.map((selectedValue) => {
                const originalOption = options.find((opt) => (isObject(opt) ? opt.value === selectedValue : String(opt) === selectedValue));
                const displayLabel = originalOption ? (isObject(originalOption) ? originalOption.label : String(originalOption)) : selectedValue;

                return (
                  <Chip
                    disabled={disabled}
                    key={selectedValue}
                    label={displayLabel}
                    onDelete={() => {
                      const newValue = value.filter((item) => (isObject(item) ? item.value !== selectedValue : String(item) !== selectedValue));
                      handleCloseIcon(newValue);
                    }}
                    deleteIcon={<CancelIcon onMouseDown={(event) => event.stopPropagation()} />}
                    sx={{
                      height: '28px !important',
                      backgroundColor: `${colors.background.primaryLightest} !important`,
                      color: `${colors.text.secondary} !important`,
                      '& .MuiChip-deleteIcon': {
                        width: '20px !important',
                        height: '20px !important',
                        color: `${colors.text.tertiary} !important`,
                        '&:hover': {
                          color: `${colors.text.tertiaryLight} !important`,
                        },
                      },
                    }}
                  />
                );
              })}
              {hiddenSelected.length > 0 && (
                <CustomTooltip placement='bottom' title={<div>{hiddenChips}</div>} arrow>
                  <span style={{ marginLeft: '4px', cursor: 'pointer', color: colors.text.secondary, fontSize: '14px' }}>
                    {`+${hiddenSelected.length}`}
                  </span>
                </CustomTooltip>
              )}
            </Stack>
          );
        }}
        endAdornment={
          <>
            {isLoading && (
              <Box
                sx={{
                  position: 'absolute',
                  display: 'flex',
                  right: value.length > 0 && !disabled ? '65px' : '35px',
                  top: '50%',
                  transform: 'translateY(-50%)',
                  zIndex: 1,
                }}
              >
                <CircularProgress size={20} color='inherit' />
              </Box>
            )}
            {value.length > 0 && !disabled && (
              <Box
                sx={{
                  position: 'absolute',
                  display: 'flex',
                  right: '35px',
                  top: '50%',
                  transform: 'translateY(-50%)',
                  cursor: 'pointer',
                  zIndex: 1,
                }}
              >
                <CancelIcon
                  onClick={(e) => {
                    e.stopPropagation();
                    handleCloseIcon([]);
                  }}
                  onMouseDown={(event) => event.stopPropagation()}
                  sx={{ cursor: 'pointer' }}
                />
              </Box>
            )}
          </>
        }
      >
        {enableSearch && (
          <ListSubheader sx={{ p: 0, position: 'sticky', top: 0, zIndex: 1, backgroundColor: 'background.paper' }}>
            <TextField
              inputRef={searchInputRef}
              size='small'
              fullWidth
              placeholder='Type to search...'
              value={searchQuery}
              onChange={(e) => {
                setSearchQuery(e.target.value);
                if (searchInputRef.current) {
                  searchInputRef.current.focus();
                }
              }}
              onKeyDown={(e) => {
                e.stopPropagation();
                if (e.key === 'Escape') {
                  e.preventDefault();
                }
              }}
              sx={{
                ...inputSx,
                margin: '8px',
                boxSizing: 'border-box',
                width: 'calc(100% - 16px)',
                padding: '0px !important',
                '&.MuiOutlinedInput-root': {
                  padding: '0px !important',
                },
                input: {
                  padding: '0px !important',
                },
              }}
            />
          </ListSubheader>
        )}
        {enableSearch && filteredOptions.length === 0 && searchQuery ? (
          <MenuItem disabled sx={{ justifyContent: 'center' }}>
            No options found
          </MenuItem>
        ) : (
          (enableSearch ? filteredOptions : options).map((option) => {
            const optionValue = isObject(option) ? option.value : String(option);
            const optionLabel = isObject(option) ? option.label : String(option);
            const optionKey = isObject(option) ? option.id || option.value : String(option);
            const isSelected = value.some((item) => (isObject(item) ? item.value === optionValue : String(item) === optionValue));

            return (
              <MenuItem key={optionKey} value={optionValue} sx={{ justifyContent: 'space-between' }} onClick={(e) => e.stopPropagation()}>
                {optionLabel}
                {isSelected && <CheckIcon color='info' />}
              </MenuItem>
            );
          })
        )}
        {options.length === 0 && (
          <MenuItem disabled sx={{ fontSize: '12px', justifyContent: 'center' }}>
            No options available
          </MenuItem>
        )}
      </Select>
    </FormControl>
  );
};

export default CustomMultiDropdown;
