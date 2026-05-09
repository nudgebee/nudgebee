import React, { useState, useEffect } from 'react';
import { Box, TextField, IconButton, Chip, Button, Typography } from '@mui/material';
import { Add, DataObject } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import { SET_STATE_PRESETS, SET_VARS_PRESETS, FIELD_HELPER_TEXT } from './advancedConfigPresets';
import { emptyStateStyles } from './advancedConfigStyles';
import { useJsonViewMode } from '@components1/workflow/hooks/useJsonViewMode';
import FieldHeader from './FieldHeader';
import JsonTextArea from './JsonTextArea';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

interface KeyValueEntry {
  key: string;
  value: string;
  ttl?: string;
}

interface KeyValueFieldProps {
  label: string;
  field: 'set_state' | 'set_vars';
  value: Record<string, unknown> | undefined;
  onChange: (value: Record<string, unknown> | undefined) => void;
  disabled?: boolean;
  showTtl?: boolean;
}

const TTL_PRESETS = ['1h', '6h', '24h', '7d', '30d'];

const KeyValueField: React.FC<KeyValueFieldProps> = ({ label, field, value, onChange, disabled = false, showTtl = false }) => {
  const { viewMode, setViewMode, jsonValue, jsonError, copied, handleJsonChange, handleCopy } = useJsonViewMode({ value, onChange });
  const [entries, setEntries] = useState<KeyValueEntry[]>([]);

  const presets = field === 'set_state' ? SET_STATE_PRESETS : SET_VARS_PRESETS;
  const helperText = FIELD_HELPER_TEXT[field];

  // Parse value into entries
  useEffect(() => {
    if (value && typeof value === 'object') {
      const parsed: KeyValueEntry[] = Object.entries(value).map(([key, val]) => {
        if (typeof val === 'object' && val !== null && 'value' in val) {
          const obj = val as { value: unknown; ttl?: string };
          return {
            key,
            value: typeof obj.value === 'string' ? obj.value : JSON.stringify(obj.value),
            ttl: obj.ttl,
          };
        }
        return {
          key,
          value: typeof val === 'string' ? val : JSON.stringify(val),
        };
      });
      setEntries(parsed);
    } else {
      setEntries([]);
    }
  }, [value]);

  // Build value from entries
  const buildValue = (newEntries: KeyValueEntry[]): Record<string, unknown> | undefined => {
    if (newEntries.length === 0) {
      return undefined;
    }

    const result: Record<string, unknown> = {};
    for (const entry of newEntries) {
      if (!entry.key.trim()) {
        continue;
      }

      if (showTtl && entry.ttl) {
        result[entry.key] = {
          value: entry.value,
          ttl: entry.ttl,
        };
      } else if (showTtl) {
        result[entry.key] = {
          value: entry.value,
        };
      } else {
        result[entry.key] = {
          value: entry.value,
        };
      }
    }
    return Object.keys(result).length > 0 ? result : undefined;
  };

  const handleAddEntry = () => {
    const newEntries = [...entries, { key: '', value: '', ttl: showTtl ? '24h' : undefined }];
    setEntries(newEntries);
  };

  const handleEntryChange = (index: number, entryField: keyof KeyValueEntry, fieldValue: string) => {
    const newEntries = [...entries];
    newEntries[index] = { ...newEntries[index], [entryField]: fieldValue };
    setEntries(newEntries);
    onChange(buildValue(newEntries));
  };

  const handleDeleteEntry = (index: number) => {
    const newEntries = entries.filter((_, i) => i !== index);
    setEntries(newEntries);
    onChange(buildValue(newEntries));
  };

  return (
    <Box>
      <FieldHeader
        label={label}
        viewMode={viewMode}
        onViewModeChange={setViewMode}
        copied={copied}
        onCopy={handleCopy}
        presets={presets}
        onPresetClick={(preset) => onChange(preset.value as Record<string, unknown>)}
        disabled={disabled}
      />

      {viewMode === 'structured' ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          {entries.map((entry, index) => (
            <Box
              key={index}
              sx={{
                p: 1.5,
                border: `1px solid ${colors.lowestLight}`,
                borderRadius: 1,
                bgcolor: colors.background.tertiaryLightest,
              }}
            >
              <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start' }}>
                <TextField
                  size='small'
                  label='Key'
                  value={entry.key}
                  onChange={(e) => handleEntryChange(index, 'key', e.target.value)}
                  disabled={disabled}
                  placeholder='variable_name'
                  sx={{ flex: 1 }}
                  InputProps={{ sx: { fontSize: '12px' } }}
                />
                {showTtl && (
                  <Box sx={{ width: 120 }}>
                    <TextField
                      size='small'
                      label='TTL'
                      value={entry.ttl || ''}
                      onChange={(e) => handleEntryChange(index, 'ttl', e.target.value)}
                      disabled={disabled}
                      placeholder='e.g., 24h'
                      InputProps={{ sx: { fontSize: '12px' } }}
                    />
                    <Box sx={{ display: 'flex', gap: 0.25, mt: 0.5, flexWrap: 'wrap' }}>
                      {TTL_PRESETS.slice(0, 3).map((preset) => (
                        <Chip
                          key={preset}
                          label={preset}
                          size='small'
                          onClick={() => handleEntryChange(index, 'ttl', preset)}
                          disabled={disabled}
                          sx={{
                            fontSize: '8px',
                            height: 16,
                            bgcolor: entry.ttl === preset ? 'primary.light' : colors.lowestLight,
                            color: entry.ttl === preset ? 'primary.contrastText' : colors.text.secondary,
                          }}
                        />
                      ))}
                    </Box>
                  </Box>
                )}
                <IconButton size='small' onClick={() => handleDeleteEntry(index)} disabled={disabled} sx={{ mt: 0.5 }}>
                  <SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />
                </IconButton>
              </Box>
              <TextField
                size='small'
                label='Value (template expression)'
                value={entry.value}
                onChange={(e) => handleEntryChange(index, 'value', e.target.value)}
                disabled={disabled}
                placeholder='e.g., {{ Self.output.data }} or {{ .Result }}'
                fullWidth
                sx={{ mt: 1 }}
                InputProps={{ sx: { fontSize: '12px', fontFamily: 'monospace' } }}
              />
            </Box>
          ))}

          {entries.length === 0 ? (
            <Box sx={emptyStateStyles.container}>
              <DataObject sx={emptyStateStyles.icon} />
              <Typography sx={emptyStateStyles.text}>
                No {field === 'set_state' ? 'state variables' : 'variables'} configured yet.
                <br />
                Add entries to {field === 'set_state' ? 'persist data across automation runs' : 'share data between tasks'}.
              </Typography>
              <Button
                variant='outlined'
                size='small'
                startIcon={<Add sx={{ fontSize: 16 }} />}
                onClick={handleAddEntry}
                disabled={disabled}
                sx={emptyStateStyles.button}
              >
                Add Your First Entry
              </Button>
            </Box>
          ) : (
            <Button
              size='small'
              startIcon={<Add sx={{ fontSize: 14 }} />}
              onClick={handleAddEntry}
              disabled={disabled}
              sx={{ alignSelf: 'flex-start', fontSize: '11px' }}
            >
              Add Entry
            </Button>
          )}
        </Box>
      ) : (
        <JsonTextArea
          value={jsonValue}
          onChange={handleJsonChange}
          error={jsonError}
          helperText={helperText}
          placeholder={
            showTtl ? '{\n  "key": {\n    "value": "{{ .Result }}",\n    "ttl": "24h"\n  }\n}' : '{\n  "key": {\n    "value": "{{ .Result }}"\n  }\n}'
          }
          disabled={disabled}
        />
      )}
    </Box>
  );
};

export default KeyValueField;
