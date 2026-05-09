import React, { useState, useEffect } from 'react';
import { Box, Typography, TextField, IconButton, Chip, Button } from '@mui/material';
import { Add, Close, GridView } from '@mui/icons-material';
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
              sx={{ height: 20, fontSize: '10px' }}
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
                <TextField
                  size='small'
                  label='Parameter Name'
                  value={entry.key}
                  onChange={(e) => handleKeyChange(entryIndex, e.target.value)}
                  disabled={disabled}
                  placeholder='e.g., region, environment'
                  sx={{ flex: 1 }}
                  InputProps={{ sx: { fontSize: '12px' } }}
                />
                <IconButton size='small' onClick={() => handleDeleteEntry(entryIndex)} disabled={disabled}>
                  <SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />
                </IconButton>
              </Box>

              <Typography sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.secondary, mb: 0.5 }}>
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
                    <TextField
                      size='small'
                      value={val}
                      onChange={(e) => handleValueChange(entryIndex, valueIndex, e.target.value)}
                      disabled={disabled}
                      placeholder='value'
                      variant='standard'
                      InputProps={{
                        disableUnderline: true,
                        sx: { fontSize: '11px', width: 80 },
                      }}
                    />
                    <IconButton
                      size='small'
                      onClick={() => handleDeleteValue(entryIndex, valueIndex)}
                      disabled={disabled || entry.values.length <= 1}
                      sx={{ p: 0.25 }}
                    >
                      <Close sx={{ fontSize: 12 }} />
                    </IconButton>
                  </Box>
                ))}
                <IconButton size='small' onClick={() => handleAddValue(entryIndex)} disabled={disabled} sx={{ p: 0.5 }}>
                  <Add sx={{ fontSize: 14 }} />
                </IconButton>
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
              <Button
                variant='outlined'
                size='small'
                startIcon={<Add sx={{ fontSize: 16 }} />}
                onClick={handleAddEntry}
                disabled={disabled}
                sx={emptyStateStyles.button}
              >
                Add Your First Parameter
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
              Add Parameter
            </Button>
          )}

          {entries.length > 0 && (
            <Typography sx={{ fontSize: '10px', color: colors.text.secondary, mt: 0.5 }}>
              Access values in task params using: <code style={{ fontSize: '10px' }}>{'{{ Matrix.<param_name> }}'}</code>
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
