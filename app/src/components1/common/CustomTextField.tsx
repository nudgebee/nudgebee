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
      fontSize: '14px',
      marginLeft: '2px',
      fontWeight: 500,
      color: colors.text.secondary,
    },
    instructionText: {
      fontSize: '12px',
      color: colors.text.tertiary,
      marginLeft: '2px',
    },
    inputField: {
      fontSize: '14px',
      '& .MuiOutlinedInput-root': {
        borderRadius: '6px',
        fontSize: '14px',
        backgroundColor: 'white',
        color: colors.text.secondary,
        mt: '6px',
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
          backgroundColor: '#F5F5F5',
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
        padding: '12px 16px',
        transition: 'background-color 0.2s ease-in-out',
        '&::placeholder': {
          fontSize: '14px',
          opacity: 0.4,
        },
        '&:focus': {
          outline: 'none',
        },
      },
      '& .MuiInputBase-inputMultiline': {
        padding: '4px 2px !important',
        '&::placeholder': {
          fontSize: '14px',
          opacity: 0.4,
        },
      },
      '& .MuiFormHelperText-root': {
        marginLeft: '0px',
        marginTop: '4px',
        fontSize: '12px',
        '&.Mui-error': {
          color: colors.border.error,
        },
      },
    },
    errorText: {
      color: colors.border.error,
      fontSize: '12px',
      fontWeight: 500,
      mt: 1,
    },
  };

  return (
    <Box>
      {label && (
        <Typography sx={styles.label}>
          {label}
          {required && <span style={{ color: colors.border.error, marginLeft: '4px' }}>*</span>}
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
