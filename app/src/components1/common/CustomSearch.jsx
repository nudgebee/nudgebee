import React, { useCallback, useEffect, useRef, useState } from 'react';
import { FormControl, TextField, InputAdornment, IconButton } from '@mui/material';
import { searchSvg } from '@assets';
import PropTypes from 'prop-types';
import ClearIcon from '@mui/icons-material/Clear';
import { colors } from 'src/utils/colors';
import SafeIcon from './SafeIcon';

const CustomSearch = ({
  label = '',
  minWidth = '150px',
  maxWidth = '260px',
  ml,
  mr,
  onChange,
  onEnterPress,
  sx,
  value,
  id,
  onClear,
  disabled = false,
  debounceMs = 300,
}) => {
  const [searchText, setSearchText] = useState(value ?? '');
  const [shouldTriggerFilter, setShouldTriggerFilter] = useState(false);
  const debounceTimerRef = useRef(null);

  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
    };
  }, []);

  const debouncedOnChange = useCallback(
    (newValue) => {
      if (!onChange) return;
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
      if (debounceMs <= 0) {
        onChange(newValue);
        return;
      }
      debounceTimerRef.current = setTimeout(() => {
        onChange(newValue);
      }, debounceMs);
    },
    [onChange, debounceMs]
  );

  useEffect(() => {
    if (shouldTriggerFilter && searchText === '' && onEnterPress) {
      onEnterPress();
      setShouldTriggerFilter(false);
    }
  }, [searchText, shouldTriggerFilter, onEnterPress]);

  const handleChange = (event) => {
    const newValue = event.target.value;
    setSearchText(newValue);
    debouncedOnChange(newValue);
  };

  const handleKeyDown = (event) => {
    if (event.key === 'Enter' && onEnterPress) {
      onEnterPress();
    }
  };

  const handleClear = () => {
    if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
    setShouldTriggerFilter(true);
    setSearchText('');
    if (onChange) {
      onChange('');
    }
    if (onClear) {
      onClear();
    }
  };

  return (
    <FormControl
      size='small'
      sx={{
        ...sx,
        borderRadius: '6px',
        maxWidth,
        minWidth,
        border: 'none',
        ml,
        mr,
        '&.css-1a4c7pq-MuiFormControl-root-MuiTextField-root': {
          mb: '3px',
          fontSize: '15px',
        },
      }}
    >
      <TextField
        id={id}
        type='search'
        value={searchText}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        placeholder={label}
        disabled={disabled}
        sx={{
          minWidth,
          maxWidth,
          '& input': {
            fontSize: '13px !important',
            fontWeight: 450,
            color: colors.text.secondary,
            lineHeight: 1,
            padding: '0px !important',
          },
          '& ::placeholder': {
            color: colors.text.secondaryDark,
            opacity: 1,
            fontSize: '12px',
            fontWeight: 400,
          },
          '& input::-webkit-search-cancel-button': {
            display: 'none',
            '-webkit-appearance': 'none',
          },
          '& input::-webkit-search-decoration': {
            display: 'none',
            '-webkit-appearance': 'none',
          },
          '& input[type="search"]::-moz-search-clear-button': {
            display: 'none',
          },
          '& input[type="search"]::-ms-clear': {
            display: 'none',
          },
          '& input[type="search"]::-ms-reveal': {
            display: 'none',
          },
        }}
        InputProps={{
          sx: {
            padding: '8px 10px !important',
            borderRadius: '6px',
            height: '34px !important',
            backgroundColor: colors.background.white,
            border: '1px solid #e2e2e2c4',
            boxShadow: '0 4px 4px rgba(0, 0, 0, 0.04)',
            transition: 'all 0.2s ease',
            '& .MuiOutlinedInput-notchedOutline': {
              border: 'none !important',
            },
            '&.Mui-focused': {
              backgroundColor: colors.background.tertiaryLightest,
              boxShadow: '0 0 0 2px rgba(59, 130, 246, 0.15)',
            },
            '&:hover:not(.Mui-focused)': {
              backgroundColor: colors.background.tertiaryLightest,
            },
          },
          startAdornment: (
            <InputAdornment position='start'>
              <SafeIcon src={searchSvg} alt={Date.now()} height={18} width={18} />
            </InputAdornment>
          ),
          endAdornment: (
            <InputAdornment position='end'>
              <IconButton
                aria-label='clear search'
                onClick={handleClear}
                edge='end'
                size='medium'
                sx={{
                  visibility: !searchText ? 'hidden' : 'visible',
                  paddingRight: '16px',
                  '&:hover': {
                    backgroundColor: 'transparent',
                  },
                }}
              >
                <ClearIcon sx={{ fontSize: '16px' }} />
              </IconButton>
            </InputAdornment>
          ),
        }}
      />
    </FormControl>
  );
};

CustomSearch.propTypes = {
  label: PropTypes.string,
  minWidth: PropTypes.string,
  maxWidth: PropTypes.string,
  ml: PropTypes.string,
  mr: PropTypes.string,
  onChange: PropTypes.func,
  onEnterPress: PropTypes.func,
  sx: PropTypes.object,
  value: PropTypes.string,
  id: PropTypes.string,
  onClear: PropTypes.func,
  disabled: PropTypes.bool,
  debounceMs: PropTypes.number,
};

export default CustomSearch;
