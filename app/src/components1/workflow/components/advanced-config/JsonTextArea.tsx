import React from 'react';
import { Input } from '@components1/ds/Input';

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
  <Input
    type='textarea'
    minRows={minRows}
    maxRows={maxRows}
    size='sm'
    value={value}
    onChange={onChange}
    placeholder={placeholder}
    disabled={disabled}
    error={error || undefined}
    help={!error ? helperText : undefined}
  />
);

export default JsonTextArea;
