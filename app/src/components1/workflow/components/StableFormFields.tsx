import React, { useState, useEffect, useCallback, memo, useRef } from 'react';
import { Box, TextField, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { FormField } from '@components1/common/NewReusabeFormComponents';

const DEFAULT_FORM_FIELD_PROPS = {
  limitTags: 0,
  minWidth: '',
  onSelect: () => {},
  customRender: null,
  rows: 1,
  maxRows: 1,
  minRows: 1,
  maxLength: 500,
};

interface StableTextFieldProps {
  fieldName: string;
  value: string;
  onChange: (fieldName: string, value: string) => void;
  label: string;
  isRequired: boolean;
  description?: string;
  placeholder?: string;
  disabled?: boolean;
  error?: string;
  isDropTarget?: boolean;
  onDrop?: (e: React.DragEvent) => void;
  onDragOver?: (e: React.DragEvent) => void;
  onDragLeave?: () => void;
}

/**
 * StableTextField - A text field component that manages its own local state
 * to prevent focus loss during parent re-renders. Only syncs to parent on blur
 * or after a debounce period.
 */
export const StableTextField = memo(function StableTextField({
  fieldName,
  value,
  onChange,
  label,
  isRequired,
  description = '',
  placeholder = '',
  disabled = false,
  error = '',
  isDropTarget = false,
  onDrop,
  onDragOver,
  onDragLeave,
}: StableTextFieldProps) {
  const [localValue, setLocalValue] = useState(value);
  const isUserTypingRef = useRef(false);

  // Sync from parent when value changes externally (not from user input)
  useEffect(() => {
    // Only sync if user is not currently typing
    if (!isUserTypingRef.current) {
      setLocalValue(value);
    }
  }, [value]);

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const newValue = e.target.value;
      isUserTypingRef.current = true;
      setLocalValue(newValue);
      // Immediately propagate to parent - the memoization prevents re-render issues
      onChange(fieldName, newValue);
      // Reset the typing flag after a short delay
      setTimeout(() => {
        isUserTypingRef.current = false;
      }, 100);
    },
    [fieldName, onChange]
  );

  const wrapperStyle = {
    transition: 'all 0.2s ease',
    borderRadius: 1,
    ...(isDropTarget && {
      outline: '2px dashed #60a5fa',
      outlineOffset: 2,
      backgroundColor: '#eff6ff',
    }),
  };

  return (
    <Box onDrop={onDrop} onDragOver={onDragOver} onDragLeave={onDragLeave} sx={wrapperStyle}>
      <Box sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
        <Typography
          sx={{
            fontSize: '13px',
            fontWeight: 500,
            color: colors.text.secondary,
            minWidth: '120px',
            pt: 1,
          }}
        >
          {label}
          {isRequired && <span style={{ color: colors.border.error }}> *</span>}
        </Typography>
        <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
          <FormField
            {...DEFAULT_FORM_FIELD_PROPS}
            description={description}
            value={localValue}
            onChange={handleChange}
            placeholder={placeholder}
            disabled={disabled}
            error={error}
            fieldType='textfield'
            required={isRequired}
          />
        </Box>
      </Box>
    </Box>
  );
});

interface StableTextareaProps {
  fieldName: string;
  value: string;
  onChange: (fieldName: string, value: string) => void;
  label: string;
  isRequired: boolean;
  description?: string;
  placeholder?: string;
  disabled?: boolean;
  error?: string;
  rows?: number;
  maxRows?: number;
  minRows?: number;
  maxLength?: number;
  isJsonField?: boolean;
}

/**
 * StableTextarea - A textarea component that manages its own local state
 * to prevent focus loss during parent re-renders.
 */
export const StableTextarea = memo(function StableTextarea({
  fieldName,
  value,
  onChange,
  label,
  isRequired,
  description = '',
  placeholder = '',
  disabled = false,
  error = '',
  rows = 4,
  maxRows = 8,
  minRows = 3,
  maxLength = 2000,
  isJsonField = false,
}: StableTextareaProps) {
  // For JSON fields, convert object to string for display
  const getDisplayValue = useCallback(
    (val: any): string => {
      if (isJsonField && typeof val === 'object' && val !== null) {
        return JSON.stringify(val, null, 2);
      }
      return val || '';
    },
    [isJsonField]
  );

  const [localValue, setLocalValue] = useState(() => getDisplayValue(value));
  const isUserTypingRef = useRef(false);

  // Sync from parent when value changes externally (not from user input)
  useEffect(() => {
    // Only sync if user is not currently typing
    if (!isUserTypingRef.current) {
      setLocalValue(getDisplayValue(value));
    }
  }, [value, getDisplayValue]);

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const newValue = e.target.value;
      isUserTypingRef.current = true;
      setLocalValue(newValue);

      if (isJsonField) {
        try {
          const parsedValue = JSON.parse(newValue);
          onChange(fieldName, parsedValue);
        } catch {
          // If JSON parsing fails, store as string (for partial editing)
          onChange(fieldName, newValue);
        }
      } else {
        onChange(fieldName, newValue);
      }

      // Reset the typing flag after a short delay
      setTimeout(() => {
        isUserTypingRef.current = false;
      }, 100);
    },
    [fieldName, onChange, isJsonField]
  );

  return (
    <Box sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
      <Typography
        sx={{
          fontSize: '13px',
          fontWeight: 500,
          color: colors.text.secondary,
          minWidth: '120px',
          pt: 1,
        }}
      >
        {label}
        {isRequired && <span style={{ color: colors.border.error }}> *</span>}
      </Typography>
      <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
        <FormField
          {...DEFAULT_FORM_FIELD_PROPS}
          description={description}
          value={localValue}
          onChange={handleChange}
          placeholder={placeholder}
          disabled={disabled}
          error={error}
          fieldType='textarea'
          rows={rows}
          maxRows={maxRows}
          minRows={minRows}
          maxLength={maxLength}
          required={isRequired}
          minWidth=''
        />
        <Typography
          sx={{
            fontSize: '11px',
            color: localValue.length >= maxLength * 0.9 ? colors.text.warning : colors.text.secondary,
            textAlign: 'right',
            mt: 0.25,
          }}
          data-testid={`${fieldName}-char-counter`}
        >
          {localValue.length.toLocaleString()} / {maxLength.toLocaleString()}
        </Typography>
      </Box>
    </Box>
  );
});

interface StableNumberFieldProps {
  fieldName: string;
  value: number | string;
  onChange: (fieldName: string, value: number) => void;
  label: string;
  isRequired: boolean;
  description?: string;
  disabled?: boolean;
  error?: string;
  isInteger?: boolean;
}

/**
 * StableNumberField - A number input that manages its own local state
 */
export const StableNumberField = memo(function StableNumberField({
  fieldName,
  value,
  onChange,
  label,
  isRequired,
  description = '',
  disabled = false,
  error = '',
  isInteger = false,
}: StableNumberFieldProps) {
  const [localValue, setLocalValue] = useState(value);
  const isUserTypingRef = useRef(false);

  // Sync from parent when value changes externally (not from user input)
  useEffect(() => {
    if (!isUserTypingRef.current) {
      setLocalValue(value);
    }
  }, [value]);

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const newValue = e.target.value;
      isUserTypingRef.current = true;
      setLocalValue(newValue);

      if (newValue === '') {
        // Field was cleared - propagate undefined to remove from saved data
        onChange(fieldName, undefined as unknown as number);
      } else {
        const parsedValue = isInteger ? Number.parseInt(newValue, 10) : Number.parseFloat(newValue);
        if (!Number.isNaN(parsedValue)) {
          onChange(fieldName, parsedValue);
        }
      }

      // Reset the typing flag after a short delay
      setTimeout(() => {
        isUserTypingRef.current = false;
      }, 100);
    },
    [fieldName, onChange, isInteger]
  );

  return (
    <Box sx={{ mb: 2, display: 'flex', alignItems: 'center', gap: 2, flexWrap: 'wrap' }}>
      <Typography
        sx={{
          fontSize: '13px',
          fontWeight: 500,
          color: colors.text.secondary,
          minWidth: '120px',
        }}
      >
        {label}
        {isRequired && <span style={{ color: colors.border.error }}> *</span>}
      </Typography>
      <Box sx={{ flex: '0 0 auto' }}>
        <TextField
          type='number'
          value={localValue}
          onChange={handleChange}
          disabled={disabled}
          error={!!error}
          size='small'
          sx={{
            width: '80px',
            '& .MuiOutlinedInput-root': {
              borderRadius: '6px',
              backgroundColor: 'white',
              fontSize: '14px',
              '&.Mui-error fieldset': {
                borderColor: colors.border.error,
                borderWidth: '1px',
              },
              '& fieldset': {
                borderColor: colors.border.vertical,
              },
              '&:hover fieldset': {
                borderColor: colors.border.primaryLightest,
              },
              '&.Mui-focused fieldset': {
                borderColor: colors.border.primary,
                borderWidth: '2px',
              },
            },
            '& .MuiInputBase-input': {
              padding: '6px 8px',
              textAlign: 'left',
              height: 'auto',
            },
          }}
        />
        {(description || error) && (
          <Typography
            sx={{
              fontSize: '11px',
              color: error ? colors.border.error : colors.text.secondaryDark,
              fontWeight: error ? 500 : 300,
              mt: 0.5,
            }}
          >
            {error || description}
          </Typography>
        )}
      </Box>
    </Box>
  );
});
