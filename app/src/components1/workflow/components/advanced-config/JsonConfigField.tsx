import React, { useState, useCallback, useEffect } from 'react';
import { Box, TextField, Typography, IconButton, Tooltip, Menu, MenuItem, ListItemText, ListItemIcon } from '@mui/material';
import { ContentCopy, Check, FormatAlignLeft, KeyboardArrowDown, AutoAwesome } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import { getPresetsForField, FIELD_HELPER_TEXT, FIELD_PLACEHOLDERS, type Preset } from './advancedConfigPresets';
import { useCopyToClipboard } from '@components1/workflow/hooks/useCopyToClipboard';

interface JsonConfigFieldProps {
  field: string;
  label: string;
  value: string | Record<string, unknown> | undefined;
  onChange: (value: Record<string, unknown> | undefined) => void;
  disabled?: boolean;
  rows?: number;
  customHelperText?: string;
  customPlaceholder?: string;
}

const JsonConfigField: React.FC<JsonConfigFieldProps> = ({
  field,
  label,
  value,
  onChange,
  disabled = false,
  rows = 3,
  customHelperText,
  customPlaceholder,
}) => {
  // Convert value to string for display
  const getDisplayValue = useCallback((val: string | Record<string, unknown> | undefined): string => {
    if (!val) {
      return '';
    }
    if (typeof val === 'string') {
      return val;
    }
    return JSON.stringify(val, null, 2);
  }, []);

  const [localValue, setLocalValue] = useState(() => getDisplayValue(value));
  const [isValid, setIsValid] = useState(true);
  const [errorMessage, setErrorMessage] = useState('');
  const { copied, copy } = useCopyToClipboard();
  const [anchorEl, setAnchorEl] = useState<null | HTMLElement>(null);

  const presets = getPresetsForField(field);
  const helperText = customHelperText || FIELD_HELPER_TEXT[field] || '';
  const placeholder = customPlaceholder || FIELD_PLACEHOLDERS[field] || '';

  // Sync with external value changes
  useEffect(() => {
    setLocalValue(getDisplayValue(value));
  }, [value, getDisplayValue]);

  const validateJson = useCallback((text: string): { valid: boolean; parsed?: Record<string, unknown>; error?: string } => {
    if (!text.trim()) {
      return { valid: true, parsed: undefined };
    }
    try {
      const parsed = JSON.parse(text);
      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
        return { valid: false, error: 'Must be a JSON object' };
      }
      return { valid: true, parsed };
    } catch (e) {
      return { valid: false, error: (e as Error).message };
    }
  }, []);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e.target.value;
    setLocalValue(newValue);

    const validation = validateJson(newValue);
    setIsValid(validation.valid);
    setErrorMessage(validation.error || '');

    // Only propagate valid JSON or empty value
    if (validation.valid) {
      onChange(validation.parsed);
    }
  };

  const handleBlur = () => {
    const validation = validateJson(localValue);
    setIsValid(validation.valid);
    setErrorMessage(validation.error || '');
  };

  const handleFormat = () => {
    if (!localValue.trim()) {
      return;
    }
    try {
      const parsed = JSON.parse(localValue);
      const formatted = JSON.stringify(parsed, null, 2);
      setLocalValue(formatted);
      setIsValid(true);
      setErrorMessage('');
    } catch {
      // Already invalid, don't change anything
    }
  };

  const handleCopy = async () => {
    await copy(localValue);
  };

  const handlePresetClick = (event: React.MouseEvent<HTMLElement>) => {
    setAnchorEl(event.currentTarget);
  };

  const handlePresetClose = () => {
    setAnchorEl(null);
  };

  const handlePresetSelect = (preset: Preset) => {
    const presetValue = typeof preset.value === 'string' ? preset.value : JSON.stringify(preset.value, null, 2);
    setLocalValue(presetValue);
    setIsValid(true);
    setErrorMessage('');

    if (typeof preset.value === 'object') {
      onChange(preset.value);
    }
    handlePresetClose();
  };

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
        <Typography variant='body2' sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>
          {label}
        </Typography>
        <Box sx={{ display: 'flex', gap: 0.5 }}>
          {presets.length > 0 && (
            <>
              <Tooltip title='Apply preset'>
                <IconButton size='small' onClick={handlePresetClick} disabled={disabled} sx={{ p: 0.5 }}>
                  <AutoAwesome sx={{ fontSize: 16 }} />
                  <KeyboardArrowDown sx={{ fontSize: 14 }} />
                </IconButton>
              </Tooltip>
              <Menu anchorEl={anchorEl} open={Boolean(anchorEl)} onClose={handlePresetClose}>
                {presets.map((preset, index) => (
                  <MenuItem key={index} onClick={() => handlePresetSelect(preset)} sx={{ minWidth: 200 }}>
                    <ListItemIcon>
                      <AutoAwesome sx={{ fontSize: 16 }} />
                    </ListItemIcon>
                    <ListItemText
                      primary={preset.label}
                      secondary={preset.description}
                      primaryTypographyProps={{ fontSize: '13px' }}
                      secondaryTypographyProps={{ fontSize: '11px' }}
                    />
                  </MenuItem>
                ))}
              </Menu>
            </>
          )}
          <Tooltip title='Format JSON'>
            <IconButton size='small' onClick={handleFormat} disabled={disabled || !localValue.trim()} sx={{ p: 0.5 }}>
              <FormatAlignLeft sx={{ fontSize: 16 }} />
            </IconButton>
          </Tooltip>
          <Tooltip title={copied ? 'Copied!' : 'Copy'}>
            <IconButton size='small' onClick={handleCopy} disabled={!localValue.trim()} sx={{ p: 0.5 }}>
              {copied ? <Check sx={{ fontSize: 16, color: 'success.main' }} /> : <ContentCopy sx={{ fontSize: 16 }} />}
            </IconButton>
          </Tooltip>
        </Box>
      </Box>
      <TextField
        fullWidth
        multiline
        rows={rows}
        size='small'
        value={localValue}
        onChange={handleChange}
        onBlur={handleBlur}
        placeholder={placeholder}
        disabled={disabled}
        error={!isValid}
        helperText={!isValid ? errorMessage : helperText}
        sx={{
          '& .MuiInputBase-input': {
            fontFamily: 'monospace',
            fontSize: '12px',
          },
          '& .MuiOutlinedInput-root': {
            borderColor: isValid ? undefined : 'error.main',
            '&.Mui-focused': {
              borderColor: isValid ? 'primary.main' : 'error.main',
            },
          },
        }}
        FormHelperTextProps={{
          sx: { fontSize: '11px' },
        }}
      />
      {isValid && localValue.trim() && (
        <Box
          sx={{
            mt: 0.5,
            px: 1,
            py: 0.25,
            bgcolor: colors.background.greenLabel,
            borderRadius: 0.5,
            display: 'inline-flex',
            alignItems: 'center',
            gap: 0.5,
          }}
        >
          <Check sx={{ fontSize: 12, color: 'success.main' }} />
          <Typography sx={{ fontSize: '10px', color: 'success.main' }}>Valid JSON</Typography>
        </Box>
      )}
    </Box>
  );
};

export default JsonConfigField;
