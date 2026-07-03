import React from 'react';
import { type TextFieldProps, type SxProps, type Theme, TextField, Typography, Box } from '@mui/material';
import { ds } from 'src/utils/colors';

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
      color: ds.brand[500],
    },
    instructionText: {
      fontSize: 'var(--ds-text-small)',
      color: ds.gray[600],
      marginLeft: 'var(--ds-space-1)',
    },
    inputField: {
      fontSize: 'var(--ds-text-body-lg)',
      '& .MuiOutlinedInput-root': {
        borderRadius: 'var(--ds-radius-md)',
        fontSize: 'var(--ds-text-body-lg)',
        backgroundColor: 'white',
        color: ds.brand[500],
        mt: 'var(--ds-space-1)',
        transition: 'all 0.2s ease-in-out',
        '&.Mui-error fieldset': {
          borderColor: ds.red[500],
          borderWidth: '1px',
        },
        '& fieldset': {
          borderColor: ds.gray[300],
          transition: 'border-color 0.2s ease-in-out',
        },
        '&.Mui-disabled': {
          backgroundColor: 'var(--ds-background-300)',
          '& fieldset': {
            borderColor: ds.gray[300],
          },
          '& .MuiInputBase-input': {
            color: ds.gray[400],
          },
          '&:hover fieldset': {
            borderColor: ds.gray[300],
          },
        },
        '&:hover fieldset': {
          borderColor: ds.blue[400],
        },

        ...(showActiveState && {
          '&.Mui-focused fieldset': {
            borderColor: activeColor || ds.blue[500],
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
          color: ds.red[500],
        },
      },
    },
    errorText: {
      color: ds.red[500],
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
          {required && <span style={{ color: ds.red[500], marginLeft: 'var(--ds-space-1)' }}>*</span>}
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
