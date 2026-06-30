import React from 'react';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';

const Heights = {
  xs: ds.space[5],
  sm: ds.space.mul(0, 14),
  md: ds.space[6],
  lg: ds.space.mul(0, 20),
};

const CustomDateTimePicker = ({
  id,
  label,
  value,
  onChange,
  views = ['day', 'hours', 'minutes'],
  format = 'MM/DD/YYYY hh:mm A',
  error = '',
  helperText = '',
  onBlur = undefined,
  required = false,
  disabled = false,
  minDate,
  maxDateTime,
  componentsProps,
  size = 'md',
  width = '100%',
  /** Blocks keyboard/paste/drop so the user must interact via the calendar popup. */
  preventDirectInput = false,
}) => {
  const hasError = typeof error === 'string' ? error.length > 0 : !!error;
  const errorMessage = typeof error === 'string' && error.length > 0 ? error : helperText;

  return (
    <LocalizationProvider dateAdapter={AdapterDayjs}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', width }}>
        {/* Label — identical markup and tokens to ds/Input */}
        {label !== undefined && (
          <Box
            component='label'
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-700)',
              fontWeight: 'var(--ds-font-weight-medium)',
            }}
          >
            {label}
            {required && (
              <Box component='span' aria-hidden='true' sx={{ color: 'var(--ds-red-500)', marginLeft: 'var(--ds-space-1)' }}>
                *
              </Box>
            )}
          </Box>
        )}

        <DateTimePicker
          value={value}
          onChange={(newValue) => onChange?.(newValue)}
          views={views}
          inputFormat={format}
          disabled={disabled}
          minDate={minDate}
          maxDateTime={maxDateTime}
          componentsProps={componentsProps}
          renderInput={({ inputRef, inputProps, InputProps }) => (
            // Outer wrapper: position-relative shell for the trailing icon,
            // same pattern as ds/Input's inputWithIcons block.
            <Box sx={{ position: 'relative', display: 'flex', width: '100%' }}>
              {/* Input — same Box component='input' + inputBaseSx as ds/Input sm */}
              <Box
                component='input'
                {...inputProps}
                ref={inputRef}
                id={id ?? inputProps?.id}
                onBlur={(e) => {
                  inputProps?.onBlur?.(e);
                  onBlur?.(e);
                }}
                onKeyDown={preventDirectInput ? (e) => e.preventDefault() : inputProps?.onKeyDown}
                onPaste={preventDirectInput ? (e) => e.preventDefault() : undefined}
                onDrop={preventDirectInput ? (e) => e.preventDefault() : undefined}
                sx={{
                  width: '100%',
                  height: Heights[size] || ds.space.mul(0, 18),
                  padding: `0 calc(var(--ds-space-3) + ${Heights[size] || ds.space.mul(0, 18)} + var(--ds-space-3)) 0 var(--ds-space-3)`,
                  fontSize: 'var(--ds-text-body)',
                  lineHeight: 1.4,
                  color: 'var(--ds-gray-700)',
                  backgroundColor: disabled ? 'var(--ds-background-200)' : 'var(--ds-background-100)',
                  border: `1px solid ${hasError ? 'var(--ds-red-500)' : 'var(--ds-gray-300)'}`,
                  borderRadius: 'var(--ds-radius-md)',
                  outline: 'none',
                  boxSizing: 'border-box',
                  transition: `border-color var(--ds-motion-micro), box-shadow var(--ds-motion-micro)`,
                  '&:hover': disabled ? undefined : { borderColor: hasError ? 'var(--ds-red-600)' : 'var(--ds-gray-400)' },
                  '&:focus': {
                    borderColor: hasError ? 'var(--ds-red-500)' : 'var(--ds-blue-500)',
                    boxShadow: `0 0 0 3px ${hasError ? 'var(--ds-red-100)' : 'var(--ds-blue-100)'}`,
                  },
                  '&:disabled': { color: 'var(--ds-gray-500)', cursor: 'not-allowed', WebkitTextFillColor: 'var(--ds-gray-500)' },
                  '&::placeholder': { color: 'var(--ds-gray-500)', opacity: 1 },
                }}
              />
              {/* Calendar icon — same absolute positioning as ds/Input trailingIcon */}
              <Box
                aria-hidden='true'
                sx={{
                  position: 'absolute',
                  right: 'var(--ds-space-3)',
                  top: '50%',
                  transform: 'translateY(-50%)',
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  color: 'var(--ds-gray-500)',
                  '& .MuiInputAdornment-root': { margin: 0 },
                  '& .MuiIconButton-root': {
                    height: Heights[size] || ds.space.mul(0, 18),
                    width: Heights[size] || ds.space.mul(0, 18),
                    color: 'var(--ds-brand-600)',
                    backgroundColor: 'var(--ds-background-100)',
                    border: '1px solid var(--ds-brand-200)',
                    borderRadius: 'var(--ds-radius-md)',
                    transition: `border-color var(--ds-motion-micro), background-color var(--ds-motion-micro)`,
                    '&:hover': {
                      color: 'var(--ds-brand-600)',
                      backgroundColor: 'var(--ds-brand-100)',
                      borderColor: 'var(--ds-brand-300)',
                    },
                    '&:active': {
                      backgroundColor: 'var(--ds-brand-200)',
                      borderColor: 'var(--ds-brand-300)',
                    },
                  },
                }}
              >
                {InputProps?.endAdornment}
              </Box>
            </Box>
          )}
        />

        {/* Error / help — identical markup and tokens to ds/Input */}
        {hasError && errorMessage && (
          <Box component='span' role='alert' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-red-600)' }}>
            {errorMessage}
          </Box>
        )}
        {!hasError && helperText && (
          <Box component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            {helperText}
          </Box>
        )}
      </Box>
    </LocalizationProvider>
  );
};

export default CustomDateTimePicker;
