import React, { useState, useEffect } from 'react';
import { Box, Typography, TextField, Chip } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Add, Close, GridView } from '@mui/icons-material';
import { Input } from '@components1/ds/Input';
import { colors } from 'src/utils/colors';
import { MATRIX_PRESETS, FIELD_HELPER_TEXT } from './advancedConfigPresets';
import { emptyStateStyles } from './advancedConfigStyles';
import { useJsonViewMode } from '@components1/workflow/hooks/useJsonViewMode';
import FieldHeader from './FieldHeader';
import JsonTextArea from './JsonTextArea';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

interface MatrixEntry {
  key: string;
  values: string[];
}

interface MatrixFieldProps {
  value: Record<string, unknown> | undefined;
  onChange: (value: Record<string, unknown> | undefined) => void;
  disabled?: boolean;
}

const MatrixField: React.FC<MatrixFieldProps> = ({ value, onChange, disabled = false }) => {
  const { viewMode, setViewMode, jsonValue, jsonError, copied, handleJsonChange, handleCopy } = useJsonViewMode({ value, onChange });
  const [entries, setEntries] = useState<MatrixEntry[]>([]);

  // Parse value into entries
  useEffect(() => {
    if (value && typeof value === 'object') {
      const parsed: MatrixEntry[] = Object.entries(value).map(([key, val]) => ({
        key,
        values: Array.isArray(val) ? val.map((v) => (typeof v === 'string' ? v : JSON.stringify(v))) : [String(val)],
      }));
      setEntries(parsed);
    } else {
      setEntries([]);
    }
  }, [value]);

  // Build value from entries
  const buildValue = (newEntries: MatrixEntry[]): Record<string, unknown> | undefined => {
    if (newEntries.length === 0) {
      return undefined;
    }

    const result: Record<string, string[]> = {};
    for (const entry of newEntries) {
      if (!entry.key.trim()) {
        continue;
      }
      result[entry.key] = entry.values.filter((v) => v.trim());
    }
    return Object.keys(result).length > 0 ? result : undefined;
  };

  const handleAddEntry = () => {
    const newEntries = [...entries, { key: '', values: [''] }];
    setEntries(newEntries);
  };

  const handleDeleteEntry = (index: number) => {
    const newEntries = entries.filter((_, i) => i !== index);
    setEntries(newEntries);
    onChange(buildValue(newEntries));
  };

  const handleKeyChange = (index: number, key: string) => {
    const newEntries = [...entries];
    newEntries[index] = { ...newEntries[index], key };
    setEntries(newEntries);
    onChange(buildValue(newEntries));
  };

  const handleAddValue = (entryIndex: number) => {
    const newEntries = [...entries];
    newEntries[entryIndex] = {
      ...newEntries[entryIndex],
      values: [...newEntries[entryIndex].values, ''],
    };
    setEntries(newEntries);
  };

  const handleValueChange = (entryIndex: number, valueIndex: number, newValue: string) => {
    const newEntries = [...entries];
    const newValues = [...newEntries[entryIndex].values];
    newValues[valueIndex] = newValue;
    newEntries[entryIndex] = { ...newEntries[entryIndex], values: newValues };
    setEntries(newEntries);
    onChange(buildValue(newEntries));
  };

  const handleDeleteValue = (entryIndex: number, valueIndex: number) => {
    const newEntries = [...entries];
    newEntries[entryIndex] = {
      ...newEntries[entryIndex],
      values: newEntries[entryIndex].values.filter((_, i) => i !== valueIndex),
    };
    setEntries(newEntries);
    onChange(buildValue(newEntries));
  };

  // Calculate total combinations
  const totalCombinations = entries.reduce(
    (acc, entry) => {
      const validValues = entry.values.filter((v) => v.trim());
      return acc * (validValues.length || 1);
    },
    entries.length > 0 ? 1 : 0
  );

  return (
    <Box>
      <FieldHeader
        label='Matrix Execution'
        viewMode={viewMode}
        onViewModeChange={setViewMode}
        copied={copied}
        onCopy={handleCopy}
        presets={MATRIX_PRESETS}
        onPresetClick={(preset) => onChange(preset.value as Record<string, unknown>)}
        disabled={disabled}
        labelExtra={
          totalCombinations > 0 ? (
            <Chip
              label={`${totalCombinations} combination${totalCombinations > 1 ? 's' : ''}`}
              size='small'
              color='primary'
              sx={{ height: 20, fontSize: 'var(--ds-text-caption)' }}
            />
          ) : undefined
        }
      />

      {viewMode === 'structured' ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
          {entries.map((entry, entryIndex) => (
            <Box
              key={entryIndex}
              sx={{
                p: 1.5,
                border: `1px solid ${colors.lowestLight}`,
                borderRadius: 1,
                bgcolor: colors.background.tertiaryLightest,
              }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
                <Box sx={{ flex: 1 }}>
                  <Input
                    size='sm'
                    label='Parameter Name'
                    value={entry.key}
                    onChange={(next) => handleKeyChange(entryIndex, next)}
                    disabled={disabled}
                    placeholder='e.g., region, environment'
                  />
                </Box>
                <Button
                  composition='icon-only'
                  tone='ghost'
                  size='sm'
                  aria-label='Delete parameter'
                  icon={<SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />}
                  disabled={disabled}
                  onClick={() => handleDeleteEntry(entryIndex)}
                />
              </Box>

              <Typography
                sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary, mb: 0.5 }}
              >
                Values ({entry.values.filter((v) => v.trim()).length})
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, alignItems: 'center' }}>
                {entry.values.map((val, valueIndex) => (
                  <Box
                    key={valueIndex}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      bgcolor: 'white',
                      border: `1px solid ${colors.lowestLight}`,
                      borderRadius: 1,
                      px: 0.5,
                    }}
                  >
                    {/* Inline borderless input inside a chip-shell. DS Input has no `variant='standard'` /
                        `disableUnderline` equivalent, so this stays MUI by design. */}
                    <TextField
                      size='small'
                      value={val}
                      onChange={(e) => handleValueChange(entryIndex, valueIndex, e.target.value)}
                      disabled={disabled}
                      placeholder='value'
                      variant='standard'
                      InputProps={{
                        disableUnderline: true,
                        sx: { fontSize: 'var(--ds-text-caption)', width: 80 },
                      }}
                    />
                    <Button
                      composition='icon-only'
                      tone='ghost'
                      size='xs'
                      aria-label='Delete value'
                      icon={<Close sx={{ fontSize: 12 }} />}
                      disabled={disabled || entry.values.length <= 1}
                      onClick={() => handleDeleteValue(entryIndex, valueIndex)}
                    />
                  </Box>
                ))}
                <Button
                  composition='icon-only'
                  tone='ghost'
                  size='xs'
                  aria-label='Add value'
                  icon={<Add sx={{ fontSize: 14 }} />}
                  disabled={disabled}
                  onClick={() => handleAddValue(entryIndex)}
                />
              </Box>
            </Box>
          ))}

          {entries.length === 0 ? (
            <Box sx={emptyStateStyles.container}>
              <GridView sx={emptyStateStyles.icon} />
              <Typography sx={emptyStateStyles.text}>
                No matrix parameters configured yet.
                <br />
                Create parallel executions with different parameter combinations.
              </Typography>
              <Button tone='secondary' size='sm' icon={<Add sx={{ fontSize: 16 }} />} disabled={disabled} onClick={handleAddEntry}>
                Add Your First Parameter
              </Button>
            </Box>
          ) : (
            <Box sx={{ alignSelf: 'flex-start' }}>
              <Button tone='ghost' size='sm' icon={<Add sx={{ fontSize: 14 }} />} disabled={disabled} onClick={handleAddEntry}>
                Add Parameter
              </Button>
            </Box>
          )}

          {entries.length > 0 && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary, mt: 0.5 }}>
              Access values in task params using: <code style={{ fontSize: 'var(--ds-text-caption)' }}>{'{{ Matrix.<param_name> }}'}</code>
            </Typography>
          )}
        </Box>
      ) : (
        <JsonTextArea
          value={jsonValue}
          onChange={handleJsonChange}
          error={jsonError}
          helperText={FIELD_HELPER_TEXT.matrix}
          placeholder={'{\n  "region": ["us-east-1", "us-west-2"],\n  "environment": ["dev", "prod"]\n}'}
          disabled={disabled}
        />
      )}
    </Box>
  );
};

export default MatrixField;
