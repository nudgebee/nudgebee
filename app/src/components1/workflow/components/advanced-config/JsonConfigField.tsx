import React, { useState, useCallback, useEffect } from 'react';
import { Box, Typography, Menu, MenuItem, ListItemText, ListItemIcon } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { ContentCopy, Check, FormatAlignLeft, KeyboardArrowDown, AutoAwesome } from '@mui/icons-material';
import { Input } from '@components1/ds/Input';
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

  const handleChange = (newValue: string) => {
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
        <Typography
          variant='body2'
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}
        >
          {label}
        </Typography>
        <Box sx={{ display: 'flex', gap: 0.5 }}>
          {presets.length > 0 && (
            <>
              <Button
                composition='icon-only'
                tone='ghost'
                size='xs'
                tooltip='Apply preset'
                aria-label='Apply preset'
                disabled={disabled}
                onClick={handlePresetClick}
                icon={
                  <Box sx={{ display: 'inline-flex', alignItems: 'center' }}>
                    <AutoAwesome sx={{ fontSize: 16 }} />
                    <KeyboardArrowDown sx={{ fontSize: 14 }} />
                  </Box>
                }
              />
              <Menu anchorEl={anchorEl} open={Boolean(anchorEl)} onClose={handlePresetClose}>
                {presets.map((preset, index) => (
                  <MenuItem key={index} onClick={() => handlePresetSelect(preset)} sx={{ minWidth: 200 }}>
                    <ListItemIcon>
                      <AutoAwesome sx={{ fontSize: 16 }} />
                    </ListItemIcon>
                    <ListItemText
                      primary={preset.label}
                      secondary={preset.description}
                      primaryTypographyProps={{ fontSize: 'var(--ds-text-body)' }}
                      secondaryTypographyProps={{ fontSize: 'var(--ds-text-caption)' }}
                    />
                  </MenuItem>
                ))}
              </Menu>
            </>
          )}
          <Button
            composition='icon-only'
            tone='ghost'
            size='xs'
            tooltip='Format JSON'
            aria-label='Format JSON'
            disabled={disabled || !localValue.trim()}
            icon={<FormatAlignLeft sx={{ fontSize: 16 }} />}
            onClick={handleFormat}
          />
          <Button
            composition='icon-only'
            tone='ghost'
            size='xs'
            tooltip={copied ? 'Copied!' : 'Copy'}
            aria-label='Copy'
            disabled={!localValue.trim()}
            icon={copied ? <Check sx={{ fontSize: 16, color: 'success.main' }} /> : <ContentCopy sx={{ fontSize: 16 }} />}
            onClick={handleCopy}
          />
        </Box>
      </Box>
      <Input
        type='textarea'
        rows={rows}
        size='sm'
        value={localValue}
        onChange={handleChange}
        onBlur={handleBlur}
        placeholder={placeholder}
        disabled={disabled}
        error={!isValid ? errorMessage || 'Invalid JSON' : undefined}
        help={isValid ? helperText : undefined}
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
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'success.main' }}>Valid JSON</Typography>
        </Box>
      )}
    </Box>
  );
};

export default JsonConfigField;
