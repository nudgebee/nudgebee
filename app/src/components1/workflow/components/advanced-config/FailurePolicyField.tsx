import React, { useState, useEffect } from 'react';
import { Box, Typography, TextField, FormControl, InputLabel, Select, MenuItem, IconButton, Chip, Switch, FormControlLabel } from '@mui/material';
import { Add, Close } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import { FAILURE_POLICY_PRESETS, FIELD_HELPER_TEXT } from './advancedConfigPresets';
import { useJsonViewMode } from '@components1/workflow/hooks/useJsonViewMode';
import FieldHeader from './FieldHeader';
import JsonTextArea from './JsonTextArea';

interface RetryConfig {
  maximum_attempts?: number;
  initial_interval?: string;
  maximum_interval?: string;
  backoff_coefficient?: number;
  non_retryable_error_types?: string[];
}

interface FailurePolicy {
  action?: 'continue' | 'fail';
  retry?: RetryConfig;
}

interface FailurePolicyFieldProps {
  value: FailurePolicy | undefined;
  onChange: (value: FailurePolicy | undefined) => void;
  disabled?: boolean;
}

const INTERVAL_PRESETS = ['1s', '5s', '10s', '30s', '1m', '5m'];

const FailurePolicyField: React.FC<FailurePolicyFieldProps> = ({ value, onChange, disabled = false }) => {
  const { viewMode, setViewMode, jsonValue, jsonError, copied, handleJsonChange, handleCopy } = useJsonViewMode({ value, onChange });

  // Local state for structured editing
  const [action, setAction] = useState<'continue' | 'fail'>(value?.action || 'fail');
  const [enableRetry, setEnableRetry] = useState(!!value?.retry);
  const [retry, setRetry] = useState<RetryConfig>(
    value?.retry || {
      maximum_attempts: 3,
      initial_interval: '1s',
      maximum_interval: '60s',
      backoff_coefficient: 2,
      non_retryable_error_types: [],
    }
  );
  const [errorTypesInput, setErrorTypesInput] = useState('');

  // Sync from external value
  useEffect(() => {
    if (value) {
      setAction(value.action || 'fail');
      setEnableRetry(!!value.retry);
      if (value.retry) {
        setRetry(value.retry);
      }
    } else {
      setAction('fail');
      setEnableRetry(false);
    }
  }, [value]);

  // Build and emit value from structured fields
  const emitStructuredValue = (newAction: string, newEnableRetry: boolean, newRetry: RetryConfig) => {
    const newValue: FailurePolicy = {
      action: newAction as 'continue' | 'fail',
    };
    if (newEnableRetry) {
      newValue.retry = newRetry;
    }
    onChange(newValue);
  };

  const handleActionChange = (newAction: 'continue' | 'fail') => {
    setAction(newAction);
    emitStructuredValue(newAction, enableRetry, retry);
  };

  const handleEnableRetryChange = (enabled: boolean) => {
    setEnableRetry(enabled);
    emitStructuredValue(action, enabled, retry);
  };

  const handleRetryChange = (field: keyof RetryConfig, fieldValue: string | number) => {
    const newRetry = { ...retry, [field]: fieldValue };
    setRetry(newRetry);
    emitStructuredValue(action, enableRetry, newRetry);
  };

  const handleAddErrorType = () => {
    const trimmed = errorTypesInput.trim();
    if (!trimmed) {
      return;
    }
    const current = retry.non_retryable_error_types || [];
    if (current.includes(trimmed)) {
      return;
    }
    const newRetry = { ...retry, non_retryable_error_types: [...current, trimmed] };
    setRetry(newRetry);
    setErrorTypesInput('');
    emitStructuredValue(action, enableRetry, newRetry);
  };

  const handleRemoveErrorType = (errorType: string) => {
    const current = retry.non_retryable_error_types || [];
    const newRetry = { ...retry, non_retryable_error_types: current.filter((e) => e !== errorType) };
    setRetry(newRetry);
    emitStructuredValue(action, enableRetry, newRetry);
  };

  return (
    <Box>
      <FieldHeader
        label='Failure Policy'
        viewMode={viewMode}
        onViewModeChange={setViewMode}
        copied={copied}
        onCopy={handleCopy}
        presets={FAILURE_POLICY_PRESETS}
        onPresetClick={(preset) => onChange(preset.value as FailurePolicy)}
        disabled={disabled}
      />

      {viewMode === 'structured' ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {/* Action */}
          <FormControl fullWidth size='small' disabled={disabled}>
            <InputLabel>Action on Failure</InputLabel>
            <Select value={action} label='Action on Failure' onChange={(e) => handleActionChange(e.target.value as 'continue' | 'fail')}>
              <MenuItem value='fail'>Fail automation</MenuItem>
              <MenuItem value='continue'>Continue automation</MenuItem>
            </Select>
          </FormControl>

          {/* Enable Retry Toggle */}
          <FormControlLabel
            control={<Switch checked={enableRetry} onChange={(e) => handleEnableRetryChange(e.target.checked)} disabled={disabled} size='small' />}
            label={<Typography sx={{ fontSize: '13px' }}>Enable retry on failure</Typography>}
          />

          {/* Retry Configuration */}
          {enableRetry && (
            <Box
              sx={{
                p: 2,
                border: `1px solid ${colors.lowestLight}`,
                borderRadius: 1,
                bgcolor: colors.background.tertiaryLightest,
              }}
            >
              <Typography sx={{ fontSize: '12px', fontWeight: 600, mb: 1.5, color: colors.text.secondary }}>Retry Configuration</Typography>
              <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2 }}>
                <TextField
                  size='small'
                  label='Max Attempts'
                  type='number'
                  value={retry.maximum_attempts || ''}
                  onChange={(e) => handleRetryChange('maximum_attempts', parseInt(e.target.value) || 0)}
                  disabled={disabled}
                  inputProps={{ min: 1, max: 10 }}
                />
                <TextField
                  size='small'
                  label='Backoff Coefficient'
                  type='number'
                  value={retry.backoff_coefficient || ''}
                  onChange={(e) => handleRetryChange('backoff_coefficient', parseFloat(e.target.value) || 1)}
                  disabled={disabled}
                  inputProps={{ min: 1, max: 10, step: 0.5 }}
                />
                <Box>
                  <TextField
                    size='small'
                    label='Initial Interval'
                    value={retry.initial_interval || ''}
                    onChange={(e) => handleRetryChange('initial_interval', e.target.value)}
                    disabled={disabled}
                    fullWidth
                    placeholder='e.g., 1s, 5s'
                  />
                  <Box sx={{ display: 'flex', gap: 0.5, mt: 0.5 }}>
                    {INTERVAL_PRESETS.slice(0, 3).map((preset) => (
                      <Chip
                        key={preset}
                        label={preset}
                        size='small'
                        onClick={() => handleRetryChange('initial_interval', preset)}
                        disabled={disabled}
                        sx={{
                          fontSize: '9px',
                          height: 18,
                          bgcolor: retry.initial_interval === preset ? 'primary.light' : colors.lowestLight,
                          color: retry.initial_interval === preset ? 'primary.contrastText' : colors.text.secondary,
                        }}
                      />
                    ))}
                  </Box>
                </Box>
                <Box>
                  <TextField
                    size='small'
                    label='Maximum Interval'
                    value={retry.maximum_interval || ''}
                    onChange={(e) => handleRetryChange('maximum_interval', e.target.value)}
                    disabled={disabled}
                    fullWidth
                    placeholder='e.g., 1m, 5m'
                  />
                  <Box sx={{ display: 'flex', gap: 0.5, mt: 0.5 }}>
                    {INTERVAL_PRESETS.slice(3).map((preset) => (
                      <Chip
                        key={preset}
                        label={preset}
                        size='small'
                        onClick={() => handleRetryChange('maximum_interval', preset)}
                        disabled={disabled}
                        sx={{
                          fontSize: '9px',
                          height: 18,
                          bgcolor: retry.maximum_interval === preset ? 'primary.light' : colors.lowestLight,
                          color: retry.maximum_interval === preset ? 'primary.contrastText' : colors.text.secondary,
                        }}
                      />
                    ))}
                  </Box>
                </Box>
              </Box>

              {/* Non-retryable Error Types */}
              <Box sx={{ mt: 2 }}>
                <Typography sx={{ fontSize: '11px', fontWeight: 600, mb: 1, color: colors.text.secondary }}>Non-retryable Error Types</Typography>
                <Box sx={{ display: 'flex', gap: 1, mb: 1 }}>
                  <TextField
                    size='small'
                    value={errorTypesInput}
                    onChange={(e) => setErrorTypesInput(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && handleAddErrorType()}
                    disabled={disabled}
                    placeholder='e.g., TimeoutError, ValidationError'
                    sx={{ flex: 1 }}
                    InputProps={{ sx: { fontSize: '12px' } }}
                  />
                  <IconButton size='small' onClick={handleAddErrorType} disabled={disabled || !errorTypesInput.trim()}>
                    <Add sx={{ fontSize: 16 }} />
                  </IconButton>
                </Box>
                <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
                  {(retry.non_retryable_error_types || []).map((errorType) => (
                    <Chip
                      key={errorType}
                      label={errorType}
                      size='small'
                      onDelete={() => handleRemoveErrorType(errorType)}
                      deleteIcon={<Close sx={{ fontSize: 12 }} />}
                      disabled={disabled}
                      sx={{ fontSize: '10px', height: 22 }}
                    />
                  ))}
                  {(retry.non_retryable_error_types || []).length === 0 && (
                    <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontStyle: 'italic' }}>
                      All error types will be retried
                    </Typography>
                  )}
                </Box>
              </Box>
            </Box>
          )}
        </Box>
      ) : (
        <JsonTextArea
          value={jsonValue}
          onChange={handleJsonChange}
          error={jsonError}
          helperText={FIELD_HELPER_TEXT.failure_policy}
          placeholder={JSON.stringify({ action: 'continue', retry: { maximum_attempts: 3 } }, null, 2)}
          disabled={disabled}
          minRows={4}
        />
      )}
    </Box>
  );
};

export default FailurePolicyField;
