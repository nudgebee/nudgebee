import React, { useState, useEffect } from 'react';
import { Box, TextField, Typography, Chip, Tooltip } from '@mui/material';
import { Check, Warning } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import { DURATION_PRESETS, FIELD_HELPER_TEXT } from './advancedConfigPresets';

interface DurationFieldProps {
  label: string;
  value: string | undefined;
  onChange: (value: string) => void;
  disabled?: boolean;
  customHelperText?: string;
  warningMessage?: string;
}

// Validate Go duration format
const validateDuration = (value: string): { valid: boolean; error?: string } => {
  if (!value.trim()) {
    return { valid: true };
  }

  // Go duration regex: optional sign, then number+unit pairs
  // Valid units: ns, us, µs, ms, s, m, h
  const durationRegex = /^[-+]?(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;

  if (durationRegex.test(value)) {
    return { valid: true };
  }

  // Check for common mistakes
  if (/^\d+$/.test(value)) {
    return { valid: false, error: 'Missing unit. Use: s (seconds), m (minutes), or h (hours)' };
  }

  return { valid: false, error: 'Invalid duration format. Examples: 30s, 5m, 1h, 1h30m' };
};

const DurationField: React.FC<DurationFieldProps> = ({ label, value, onChange, disabled = false, customHelperText, warningMessage }) => {
  const [localValue, setLocalValue] = useState(value || '');
  const [isValid, setIsValid] = useState(true);
  const [errorMessage, setErrorMessage] = useState('');

  const helperText = customHelperText || FIELD_HELPER_TEXT.timeout || '';

  useEffect(() => {
    setLocalValue(value || '');
  }, [value]);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e.target.value;
    setLocalValue(newValue);

    const validation = validateDuration(newValue);
    setIsValid(validation.valid);
    setErrorMessage(validation.error || '');

    if (validation.valid) {
      onChange(newValue);
    }
  };

  const handlePresetClick = (presetValue: string) => {
    setLocalValue(presetValue);
    setIsValid(true);
    setErrorMessage('');
    onChange(presetValue);
  };

  return (
    <Box>
      <Typography variant='body2' sx={{ mb: 1, fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>
        {label}
      </Typography>
      <TextField
        fullWidth
        size='small'
        value={localValue}
        onChange={handleChange}
        placeholder='e.g., 5m, 300s, 1h'
        disabled={disabled}
        error={!isValid}
        helperText={!isValid ? errorMessage : helperText}
        InputProps={{
          endAdornment: localValue && (
            <Tooltip title={isValid ? 'Valid duration' : errorMessage}>
              {isValid ? (
                <Check sx={{ fontSize: 16, color: 'success.main', mr: 1 }} />
              ) : (
                <Warning sx={{ fontSize: 16, color: 'error.main', mr: 1 }} />
              )}
            </Tooltip>
          ),
        }}
        sx={{ fontSize: '13px' }}
        FormHelperTextProps={{
          sx: { fontSize: '11px' },
        }}
      />
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, mt: 1 }}>
        {DURATION_PRESETS.slice(0, 4).map((preset) => (
          <Tooltip key={preset.label} title={preset.description || ''}>
            <Chip
              label={preset.value as string}
              size='small'
              onClick={() => handlePresetClick(preset.value as string)}
              disabled={disabled}
              sx={{
                fontSize: '11px',
                height: 22,
                bgcolor: localValue === preset.value ? 'primary.light' : colors.lowestLight,
                color: localValue === preset.value ? 'primary.contrastText' : colors.text.secondary,
                '&:hover': {
                  bgcolor: localValue === preset.value ? 'primary.main' : colors.background.tertiaryLightest,
                },
              }}
            />
          </Tooltip>
        ))}
      </Box>
      {isValid && warningMessage && (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, mt: 0.5 }}>
          <Warning sx={{ fontSize: 14, color: 'warning.main' }} />
          <Typography variant='body2' sx={{ fontSize: '11px', color: 'warning.main' }}>
            {warningMessage}
          </Typography>
        </Box>
      )}
    </Box>
  );
};

export default DurationField;
