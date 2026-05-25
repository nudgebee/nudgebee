import React from 'react';
import { TextField } from '@mui/material';
import { jsonTextareaStyles } from './advancedConfigStyles';

interface JsonTextAreaProps {
  value: string;
  onChange: (value: string) => void;
  error?: string;
  helperText?: string;
  placeholder?: string;
  disabled?: boolean;
  minRows?: number;
  maxRows?: number;
}

const JsonTextArea: React.FC<JsonTextAreaProps> = ({
  value,
  onChange,
  error,
  helperText,
  placeholder,
  disabled = false,
  minRows = 3,
  maxRows = 12,
}) => (
  <TextField
    fullWidth
    multiline
    minRows={minRows}
    maxRows={maxRows}
    size='small'
    value={value}
    onChange={(e) => onChange(e.target.value)}
    placeholder={placeholder}
    disabled={disabled}
    error={!!error}
    helperText={error || helperText}
    sx={jsonTextareaStyles}
  />
);

export default JsonTextArea;
