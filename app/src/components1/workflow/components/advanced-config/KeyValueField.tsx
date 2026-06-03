import React, { useState, useEffect } from 'react';
import { Box, Chip, Typography } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Add, DataObject } from '@mui/icons-material';
import { Input } from '@components1/ds/Input';
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
                <Box sx={{ flex: 1 }}>
                  <Input
                    size='sm'
                    label='Key'
                    value={entry.key}
                    onChange={(next) => handleEntryChange(index, 'key', next)}
                    disabled={disabled}
                    placeholder='variable_name'
                  />
                </Box>
                {showTtl && (
                  <Box sx={{ width: 120 }}>
                    <Input
                      size='sm'
                      label='TTL'
                      value={entry.ttl ?? ''}
                      onChange={(next) => handleEntryChange(index, 'ttl', next)}
                      disabled={disabled}
                      placeholder='e.g., 24h'
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
                            fontSize: 'var(--ds-text-caption)',
                            height: 16,
                            bgcolor: entry.ttl === preset ? 'primary.light' : colors.lowestLight,
                            color: entry.ttl === preset ? 'primary.contrastText' : colors.text.secondary,
                          }}
                        />
                      ))}
                    </Box>
                  </Box>
                )}
                <Box sx={{ mt: 0.5 }}>
                  <Button
                    composition='icon-only'
                    tone='ghost'
                    size='sm'
                    aria-label='Delete entry'
                    icon={<SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />}
                    disabled={disabled}
                    onClick={() => handleDeleteEntry(index)}
                  />
                </Box>
              </Box>
              <Box sx={{ mt: 1 }}>
                <Input
                  size='sm'
                  label='Value (template expression)'
                  value={entry.value}
                  onChange={(next) => handleEntryChange(index, 'value', next)}
                  disabled={disabled}
                  placeholder='e.g., {{ Self.output.data }} or {{ .Result }}'
                />
              </Box>
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
              <Button tone='secondary' size='sm' icon={<Add sx={{ fontSize: 16 }} />} disabled={disabled} onClick={handleAddEntry}>
                Add Your First Entry
              </Button>
            </Box>
          ) : (
            <Box sx={{ alignSelf: 'flex-start' }}>
              <Button tone='ghost' size='sm' icon={<Add sx={{ fontSize: 14 }} />} disabled={disabled} onClick={handleAddEntry}>
                Add Entry
              </Button>
            </Box>
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
