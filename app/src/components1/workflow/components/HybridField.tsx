import React, { useState, useEffect, useRef } from 'react';
import { Box, Typography, ToggleButtonGroup, ToggleButton, Autocomplete, TextField, CircularProgress, Chip } from '@mui/material';
import { ArrowDropDown, Code } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import SafeIcon from '@common/SafeIcon';
import FilterDropdownButton from '@common/FilterDropdownButton';
import TemplateTextField from './TemplateTextField';

interface PreviousTask {
  id: string;
  name: string;
  type: string;
  outputSchema?: any;
}

interface TemplateSource {
  type: 'input' | 'config' | 'secret';
  key: string;
  description?: string;
}

interface HybridFieldProps {
  fieldName: string;
  value: string;
  onChange: (value: string) => void;
  label?: string;
  placeholder?: string;
  disabled?: boolean;
  error?: string;
  required?: boolean;
  options: { label: string; value: string; icon?: any; type?: string }[];
  optionsLoading?: boolean;
  previousTasks?: PreviousTask[];
  templateSources?: TemplateSource[];
  workflowInputs?: Array<{ id: string; type: string; description?: string }>;
  workflowConfigs?: Array<{ key: string; value: string; type: string }>;
  contextChips?: Array<{ label: string; value: string }>;
  onDrop?: (e: React.DragEvent) => void;
  onDragOver?: (e: React.DragEvent) => void;
  onDragLeave?: () => void;
  isDropTarget?: boolean;
  // When provided, the Autocomplete behaves as async typeahead: onSearch is invoked
  // (debounced internally) on keystrokes so parent can refetch options. Client-side
  // filtering is disabled in this mode — the server decides what to return.
  onSearch?: (query: string) => void;
}

type FieldMode = 'select' | 'expression';

type OptionLike = { value: string };

const detectMode = (value: string, options: OptionLike[], optionsLoading: boolean, isAsync: boolean): FieldMode => {
  if (!value) return 'select';
  const trimmed = value.trim();
  if (trimmed.includes('{{') && trimmed.includes('}}')) return 'expression';
  // Async fields fetch options on-demand — the current value may legitimately not
  // be in the cached option list, so don't auto-flip to expression mode.
  if (isAsync) return 'select';
  if (optionsLoading) return 'select';
  if (options.some((opt) => opt.value === trimmed)) return 'select';
  return 'expression';
};

const HybridField: React.FC<HybridFieldProps> = ({
  fieldName,
  value,
  onChange,
  label,
  placeholder,
  disabled = false,
  error,
  required = false,
  options,
  optionsLoading = false,
  previousTasks = [],
  workflowInputs = [],
  workflowConfigs = [],
  contextChips = [],
  onDrop,
  onDragOver,
  onDragLeave,
  isDropTarget = false,
  onSearch,
}) => {
  const isAsync = !!onSearch;
  const [mode, setMode] = useState<FieldMode>(() => detectMode(value, options, optionsLoading, isAsync));
  const userToggledRef = useRef(false);
  const searchDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current);
    },
    []
  );

  // Keep mode in sync with the saved value and async-loaded options. Skipped
  // once the user has manually toggled modes in this mount so we don't yank
  // them back after an explicit choice.
  useEffect(() => {
    if (userToggledRef.current) return;
    const detected = detectMode(value, options, optionsLoading, isAsync);
    if (detected !== mode) {
      setMode(detected);
    }
  }, [value, options, optionsLoading, isAsync]);

  const handleModeChange = (_event: React.MouseEvent<HTMLElement>, newMode: FieldMode | null) => {
    if (newMode && newMode !== mode) {
      userToggledRef.current = true;
      setMode(newMode);
      // Clear value when switching modes to avoid invalid state
      if (newMode === 'select' && value?.includes('{{')) {
        onChange('');
      }
    }
  };

  // Handle drag events - auto-switch to expression mode
  const handleDrop = (e: React.DragEvent) => {
    const template = e.dataTransfer.getData('text/plain');
    if (template?.includes('{{')) {
      userToggledRef.current = true;
      setMode('expression');
    }
    onDrop?.(e);
  };

  const dropHandlers =
    !disabled && (onDrop || onDragOver)
      ? {
          onDrop: handleDrop,
          onDragOver,
          onDragLeave,
        }
      : {};

  return (
    <Box
      {...dropHandlers}
      sx={{
        width: '100%',
        transition: 'all 0.2s ease',
        borderRadius: 1,
        ...(isDropTarget && {
          outline: '2px dashed #60a5fa',
          outlineOffset: 2,
          backgroundColor: '#eff6ff',
        }),
      }}
    >
      {/* Label */}
      {label && (
        <Typography
          sx={{
            fontSize: '13px',
            fontWeight: 500,
            color: colors.text.secondary,
            mb: 0.5,
          }}
        >
          {label} {required && <span style={{ color: colors.border.error }}>*</span>}
        </Typography>
      )}

      {/* Context chips - show current Account/Namespace/Kind */}
      {contextChips.length > 0 && (
        <Box sx={{ display: 'flex', gap: 0.5, mb: 1, flexWrap: 'wrap' }}>
          {contextChips.map(
            (chip) =>
              chip.value && (
                <Chip
                  key={chip.label}
                  label={`${chip.label}: ${chip.value}`}
                  size='small'
                  sx={{
                    fontSize: '11px',
                    height: '22px',
                    backgroundColor: '#f0f4ff',
                    color: '#3b5998',
                    border: '1px solid #d4dff7',
                    '& .MuiChip-label': { px: 1 },
                  }}
                />
              )
          )}
        </Box>
      )}

      {/* Mode toggle */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.75 }}>
        <ToggleButtonGroup value={mode} exclusive onChange={handleModeChange} size='small' disabled={disabled}>
          <ToggleButton
            value='select'
            sx={{
              px: 1.5,
              py: 0.25,
              fontSize: '11px',
              textTransform: 'none',
              borderColor: '#e0e0e0',
              '&.Mui-selected': {
                backgroundColor: '#e8eaf6',
                color: '#3949ab',
                borderColor: '#c5cae9',
                '&:hover': { backgroundColor: '#c5cae9' },
              },
            }}
          >
            <ArrowDropDown sx={{ fontSize: 14, mr: 0.5 }} />
            Select
          </ToggleButton>
          <ToggleButton
            value='expression'
            sx={{
              px: 1.5,
              py: 0.25,
              fontSize: '11px',
              textTransform: 'none',
              borderColor: '#e0e0e0',
              '&.Mui-selected': {
                backgroundColor: '#fce4ec',
                color: '#c62828',
                borderColor: '#ef9a9a',
                '&:hover': { backgroundColor: '#ffcdd2' },
              },
            }}
          >
            <Code sx={{ fontSize: 14, mr: 0.5 }} />
            {'{{ }} Expression'}
          </ToggleButton>
        </ToggleButtonGroup>
      </Box>

      {/* Select mode - async typeahead keeps MUI Autocomplete since
          FilterDropdownButton has no async onSearch hook. */}
      {mode === 'select' && isAsync && (
        <Autocomplete
          value={options.find((opt) => opt.value === value) || null}
          onChange={(_event, newValue) => {
            onChange(newValue ? newValue.value : '');
          }}
          onInputChange={(_event, inputValue, reason) => {
            if (reason !== 'input') return;
            if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current);
            searchDebounceRef.current = setTimeout(() => onSearch!(inputValue), 250);
          }}
          filterOptions={(x) => x}
          options={options}
          getOptionLabel={(option) => option.label}
          loading={optionsLoading}
          disabled={disabled}
          size='small'
          freeSolo={false}
          renderOption={(props, option) => {
            const { key, ...liProps } = props as React.HTMLAttributes<HTMLLIElement> & { key?: React.Key };
            return (
              <Box component='li' key={key} {...liProps} sx={{ display: 'flex', alignItems: 'center', gap: 1, fontSize: '13px', py: 0.75 }}>
                {option.icon && (
                  <SafeIcon src={option.icon} alt={option.type ?? ''} style={{ width: 16, height: 16, flexShrink: 0, objectFit: 'contain' }} />
                )}
                <Box sx={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{option.label}</Box>
                {option.type && (
                  <Chip
                    label={option.type}
                    size='small'
                    sx={{
                      height: 18,
                      fontSize: 10,
                      bgcolor: '#f5f5f5',
                      color: colors.text.secondary,
                      flexShrink: 0,
                      '& .MuiChip-label': { px: 0.75 },
                    }}
                  />
                )}
              </Box>
            );
          }}
          renderInput={(params) => {
            const selected = options.find((opt) => opt.value === value);
            return (
              <TextField
                {...params}
                placeholder={
                  optionsLoading
                    ? 'Loading...'
                    : options.length === 0
                    ? 'Select dependencies first'
                    : placeholder || `Select ${fieldName.replace(/_/g, ' ')}`
                }
                error={!!error}
                InputProps={{
                  ...params.InputProps,
                  startAdornment: selected?.icon ? (
                    <SafeIcon
                      src={selected.icon}
                      alt={selected.type ?? ''}
                      style={{ width: 16, height: 16, marginLeft: 4, flexShrink: 0, objectFit: 'contain' }}
                    />
                  ) : null,
                  endAdornment: (
                    <>
                      {optionsLoading ? <CircularProgress color='inherit' size={18} /> : null}
                      {params.InputProps.endAdornment}
                    </>
                  ),
                }}
                sx={{
                  '& .MuiOutlinedInput-root': {
                    borderRadius: '6px',
                    backgroundColor: 'white',
                    fontSize: '13px',
                    '& fieldset': { borderColor: '#e0e0e0' },
                    '&:hover fieldset': { borderColor: '#90caf9' },
                    '&.Mui-focused fieldset': { borderColor: '#1976d2', borderWidth: '2px' },
                  },
                  '& .MuiInputBase-input': {
                    padding: '6px 12px',
                    '&::placeholder': { color: '#9e9e9e', fontSize: '12px', opacity: 1 },
                  },
                }}
              />
            );
          }}
          noOptionsText={optionsLoading ? 'Loading resources...' : 'No resources found'}
          sx={{ width: '100%' }}
        />
      )}

      {/* Select mode - FilterDropdownButton for standard (non-async) dropdowns.
          Note: per-option icon and type chip are not rendered here — the
          shared FilterDropdownButton renders text labels only. Callers that
          need icons (integration/ticket dropdowns) still get them via the
          async branch above when onSearch is wired. */}
      {mode === 'select' && !isAsync && (
        <Box sx={{ width: '100%' }}>
          <FilterDropdownButton
            id={fieldName}
            options={options}
            value={value || null}
            onSelect={(_event: any, selected: any) => {
              onChange(selected?.value ?? selected ?? '');
            }}
            disabled={disabled}
            isOptionsLoading={optionsLoading}
            required={required}
            placeholder={placeholder || `Select ${fieldName.replace(/_/g, ' ')}`}
            searchPlaceholder={placeholder || `Search ${fieldName.replace(/_/g, ' ')}`}
            sx={{
              width: '100%',
              ...(error && {
                border: `1px solid ${colors.border?.error || '#d32f2f'}`,
                boxShadow: 'none',
              }),
            }}
          />
        </Box>
      )}

      {/* Expression mode - Template text field (error text handled by HybridField below) */}
      {mode === 'expression' && (
        <TemplateTextField
          value={value}
          onChange={onChange}
          placeholder={placeholder || `e.g. {{ Tasks['task-id'].output.name }}`}
          disabled={disabled}
          required={required}
          previousTasks={previousTasks}
          workflowInputs={workflowInputs}
          workflowConfigs={workflowConfigs}
          fullWidth
        />
      )}

      {/* Error message */}
      {error && <Typography sx={{ color: colors.border?.error || '#d32f2f', fontSize: '12px', fontWeight: 500, mt: 0.5 }}>{error}</Typography>}
    </Box>
  );
};

export default HybridField;
