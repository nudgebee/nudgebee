import React, { useState, useEffect } from 'react';
import { Box, Typography, FormControl, InputLabel, Select, MenuItem, Chip, Switch, FormControlLabel } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Add, Close } from '@mui/icons-material';
import { Input } from '@components1/ds/Input';
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
            label={<Typography sx={{ fontSize: 'var(--ds-text-body)' }}>Enable retry on failure</Typography>}
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
              <Typography
                sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', mb: 1.5, color: colors.text.secondary }}
              >
                Retry Configuration
              </Typography>
              <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2 }}>
                <Input
                  size='sm'
                  label='Max Attempts'
                  type='number'
                  inputMode='numeric'
                  value={retry.maximum_attempts == null ? '' : String(retry.maximum_attempts)}
                  onChange={(next) => handleRetryChange('maximum_attempts', next === '' ? 0 : parseInt(next) || 0)}
                  disabled={disabled}
                />
                <Input
                  size='sm'
                  label='Backoff Coefficient'
                  type='number'
                  inputMode='decimal'
                  value={retry.backoff_coefficient == null ? '' : String(retry.backoff_coefficient)}
                  onChange={(next) => handleRetryChange('backoff_coefficient', next === '' ? 1 : parseFloat(next) || 1)}
                  disabled={disabled}
                />
                <Box>
                  <Input
                    size='sm'
                    label='Initial Interval'
                    value={retry.initial_interval ?? ''}
                    onChange={(next) => handleRetryChange('initial_interval', next)}
                    disabled={disabled}
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
                          fontSize: 'var(--ds-text-caption)',
                          height: 18,
                          bgcolor: retry.initial_interval === preset ? 'primary.light' : colors.lowestLight,
                          color: retry.initial_interval === preset ? 'primary.contrastText' : colors.text.secondary,
                        }}
                      />
                    ))}
                  </Box>
                </Box>
                <Box>
                  <Input
                    size='sm'
                    label='Maximum Interval'
                    value={retry.maximum_interval ?? ''}
                    onChange={(next) => handleRetryChange('maximum_interval', next)}
                    disabled={disabled}
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
                          fontSize: 'var(--ds-text-caption)',
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
                <Typography
                  sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', mb: 1, color: colors.text.secondary }}
                >
                  Non-retryable Error Types
                </Typography>
                <Box sx={{ display: 'flex', gap: 1, mb: 1, alignItems: 'flex-start' }}>
                  <Box sx={{ flex: 1 }}>
                    <Input
                      size='sm'
                      value={errorTypesInput}
                      onChange={setErrorTypesInput}
                      onKeyDown={(e) => e.key === 'Enter' && handleAddErrorType()}
                      disabled={disabled}
                      placeholder='e.g., TimeoutError, ValidationError'
                    />
                  </Box>
                  <Button
                    composition='icon-only'
                    tone='ghost'
                    size='sm'
                    aria-label='Add error type'
                    icon={<Add sx={{ fontSize: 16 }} />}
                    disabled={disabled || !errorTypesInput.trim()}
                    onClick={handleAddErrorType}
                  />
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
                      sx={{ fontSize: 'var(--ds-text-caption)', height: 22 }}
                    />
                  ))}
                  {(retry.non_retryable_error_types || []).length === 0 && (
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary, fontStyle: 'italic' }}>
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
