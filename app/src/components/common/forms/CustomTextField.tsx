import React from 'react';
import { type TextFieldProps, type SxProps, type Theme, TextField, Typography, Box } from '@mui/material';
import { colors } from 'src/utils/colors';

interface CustomTextFieldProps {
  label?: string;
  instructionText?: string;
  placeholder?: string;
  value?: string;
  onChange?: (e: React.ChangeEvent<HTMLInputElement>) => void;
  error?: boolean;
  helperText?: string;
  multiline?: boolean;
  rows?: number;
  disabled?: boolean;
  fullWidth?: boolean;
  size?: 'small' | 'medium';
  variant?: 'outlined' | 'filled' | 'standard';
  type?: string;
  InputProps?: TextFieldProps['InputProps'];
  inputProps?: TextFieldProps['inputProps'];
  autoComplete?: string;
  sx?: SxProps<Theme>;
  required?: boolean;
  minRows?: number;
  maxRows?: number;
  onBlur?: (e: React.FocusEvent<HTMLInputElement>) => void;
  onFocus?: (e: React.FocusEvent<HTMLInputElement>) => void;
  name?: string;
  id?: string;
  showActiveState?: boolean;
  activeColor?: string;
}

const CustomTextField: React.FC<CustomTextFieldProps> = ({
  label,
  instructionText,
  placeholder,
  value,
  onChange,
  error = false,
  helperText,
  multiline = false,
  rows,
  disabled = false,
  fullWidth = true,
  size = 'small',
  variant = 'outlined',
  type,
  InputProps,
  sx = {},
  required = false,
  minRows,
  maxRows,
  onBlur,
  onFocus,
  inputProps,
  autoComplete,
  name,
  id,
  showActiveState = true,
  activeColor,
}) => {
  const styles = {
    label: {
      mb: '0px',
      fontSize: 'var(--ds-text-body-lg)',
      marginLeft: 'var(--ds-space-1)',
      fontWeight: 'var(--ds-font-weight-medium)',
      color: colors.text.secondary,
    },
    instructionText: {
      fontSize: 'var(--ds-text-small)',
      color: colors.text.tertiary,
      marginLeft: 'var(--ds-space-1)',
    },
    inputField: {
      fontSize: 'var(--ds-text-body-lg)',
      '& .MuiOutlinedInput-root': {
        borderRadius: 'var(--ds-radius-md)',
        fontSize: 'var(--ds-text-body-lg)',
        backgroundColor: 'white',
        color: colors.text.secondary,
        mt: 'var(--ds-space-1)',
        transition: 'all 0.2s ease-in-out',
        '&.Mui-error fieldset': {
          borderColor: colors.border.error,
          borderWidth: '1px',
        },
        '& fieldset': {
          borderColor: colors.border.secondaryLightest,
          transition: 'border-color 0.2s ease-in-out',
        },
        '&.Mui-disabled': {
          backgroundColor: 'var(--ds-background-300)',
          '& fieldset': {
            borderColor: colors.border.secondaryLightest,
          },
          '& .MuiInputBase-input': {
            color: colors.text.disabledInput,
          },
          '&:hover fieldset': {
            borderColor: colors.border.secondaryLightest,
          },
        },
        '&:hover fieldset': {
          borderColor: colors.border.primaryLightest,
        },

        ...(showActiveState && {
          '&.Mui-focused fieldset': {
            borderColor: activeColor || colors.border.primary || '#3B82F6',
            borderWidth: '2px',
          },
          '&.Mui-focused .MuiInputBase-input': {
            backgroundColor: 'transparent',
          },
        }),
      },
      '& .MuiInputBase-input': {
        padding: 'var(--ds-space-3) var(--ds-space-4)',
        transition: 'background-color 0.2s ease-in-out',
        '&::placeholder': {
          fontSize: 'var(--ds-text-body-lg)',
          opacity: 0.4,
        },
        '&:focus': {
          outline: 'none',
        },
      },
      '& .MuiInputBase-inputMultiline': {
        padding: 'var(--ds-space-1) var(--ds-space-1) !important',
        '&::placeholder': {
          fontSize: 'var(--ds-text-body-lg)',
          opacity: 0.4,
        },
      },
      '& .MuiFormHelperText-root': {
        marginLeft: '0px',
        marginTop: 'var(--ds-space-1)',
        fontSize: 'var(--ds-text-small)',
        '&.Mui-error': {
          color: colors.border.error,
        },
      },
    },
    errorText: {
      color: colors.border.error,
      fontSize: 'var(--ds-text-small)',
      fontWeight: 'var(--ds-font-weight-medium)',
      mt: 1,
    },
  };

  return (
    <Box>
      {label && (
        <Typography sx={styles.label}>
          {label}
          {required && <span style={{ color: colors.border.error, marginLeft: 'var(--ds-space-1)' }}>*</span>}
        </Typography>
      )}
      {instructionText && <Typography sx={styles.instructionText}>{instructionText}</Typography>}
      <TextField
        id={id}
        name={name}
        placeholder={placeholder}
        value={value}
        onChange={onChange}
        onBlur={onBlur}
        onFocus={onFocus}
        error={error}
        helperText={helperText}
        multiline={multiline}
        rows={rows}
        minRows={minRows}
        maxRows={maxRows}
        disabled={disabled}
        fullWidth={fullWidth}
        size={size}
        variant={variant}
        type={type}
        InputProps={InputProps}
        inputProps={inputProps}
        autoComplete={autoComplete}
        sx={{
          ...styles.inputField,
          ...sx,
        }}
      />
    </Box>
  );
};

export default CustomTextField;
