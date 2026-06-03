import React, { useEffect, useRef, useState } from 'react';
import { Box, ToggleButtonGroup, ToggleButton, Typography } from '@mui/material';
import { ArrowDropDown, Code } from '@mui/icons-material';
import { FormField } from '@components1/common/NewReusabeFormComponents';
import CustomTooltip from '@components1/common/CustomTooltip';
import TemplateTextField from './TemplateTextField';

type FieldMode = 'select' | 'expression';

const isTemplateString = (v: any): boolean => typeof v === 'string' && (v.includes('{{') || v.includes('{%'));

const detectMode = (value: any): FieldMode => {
  if (isTemplateString(value)) return 'expression';
  return 'select';
};

// Match `{{ Configs.X }}` or `{{ Configs['X'] }}` / `{{ Configs["X"] }}`.
// Whitespace and the surrounding `{{ ... }}` are tolerant; anything more
// complex (filters, `or`, nested expressions) we deliberately don't try
// to resolve — leave the dropdown empty in that case rather than guess.
const CONFIGS_LOOKUP_RE = /^\s*\{\{\s*Configs(?:\.([A-Za-z_][\w]*)|\[\s*['"]([^'"]+)['"]\s*\])\s*\}\}\s*$/;

const resolveConfigsTemplate = (value: string, workflowConfigs: Array<{ key: string; value: string; type: string }>): string | null => {
  if (!isTemplateString(value)) return null;
  const m = CONFIGS_LOOKUP_RE.exec(value);
  if (!m) return null;
  const key = m[1] || m[2];
  if (!key) return null;
  const found = workflowConfigs?.find((c) => c?.key === key);
  return found?.value ?? null;
};

interface AccountFieldProps {
  fieldName: string;
  value: string;
  options: any[];
  description?: string;
  placeholder?: string;
  disabled?: boolean;
  required?: boolean;
  error?: string;
  defaultFormFieldProps?: Record<string, any>;
  onChange: (value: string) => void;
  /**
   * Updates the underlying value WITHOUT running the depends_on cascade in
   * the parent. Used when we resolve a Configs template to its UUID on a
   * Select-mode toggle — the field's logical value (which account this
   * task targets) hasn't changed, only its display form.
   */
  onResolveTemplate?: (value: string) => void;
  previousTasks?: any[];
  workflowInputs?: any[];
  workflowConfigs?: Array<{ key: string; value: string; type: string }>;
}

/**
 * AccountField wraps the standard account dropdown with a Select / Expression
 * mode toggle so users can either pick from cloudAccounts or author a Jinja
 * template (e.g. {{ Configs.k8s_dev_account_id }}). Mirrors HybridField but
 * preserves the account-specific rendering (groupByCloudProvider, defensive
 * sanitization of stale UUIDs).
 */
const AccountField: React.FC<AccountFieldProps> = ({
  fieldName,
  value,
  options,
  description,
  placeholder,
  disabled = false,
  required = false,
  error,
  defaultFormFieldProps,
  onChange,
  onResolveTemplate,
  previousTasks = [],
  workflowInputs = [],
  workflowConfigs = [],
}) => {
  const [mode, setMode] = useState<FieldMode>(() => detectMode(value));
  const userToggledRef = useRef(false);
  // Round-trip support: when we resolve a template to its UUID for the Select
  // view, stash the original template here. On the way back to Expression
  // mode we restore it — but only if the dropdown value still matches what
  // the stashed template resolved to. If the user picked a different
  // account in Select mode, the stash is cleared (real intentional change).
  const stashedTemplateRef = useRef<string | null>(null);
  const stashedResolvedRef = useRef<string | null>(null);

  // Keep mode in sync with the saved value until the user manually toggles.
  // After the user picks a mode for this mount we honour their choice.
  useEffect(() => {
    if (userToggledRef.current) return;
    const detected = detectMode(value);
    if (detected !== mode) setMode(detected);
  }, [value, mode]);

  // Invalidate the stash when the user picks a different account in Select
  // mode (or any other path mutates the value to something other than the
  // resolved UUID). Without this, switching to Expression after picking a
  // different option would surprise the user by restoring an unrelated
  // template.
  useEffect(() => {
    if (stashedResolvedRef.current && value !== stashedResolvedRef.current) {
      stashedTemplateRef.current = null;
      stashedResolvedRef.current = null;
    }
  }, [value]);

  const handleModeChange = (_e: React.MouseEvent<HTMLElement>, newMode: FieldMode | null) => {
    if (!newMode || newMode === mode) return;
    userToggledRef.current = true;

    // Expression → Select with a templated Configs.* lookup: resolve to the
    // underlying UUID when possible so the dropdown displays the correct
    // account. The resolution is logically the same value, so route it
    // through the no-cascade setter — otherwise dependent fields like
    // `command`, `namespace`, `kind`, `name` would get wiped by the
    // depends_on cleanup in the parent.
    if (newMode === 'select' && isTemplateString(value)) {
      const resolved = resolveConfigsTemplate(value, workflowConfigs);
      if (resolved && options.some((o: any) => o?.value === resolved)) {
        stashedTemplateRef.current = value;
        stashedResolvedRef.current = resolved;
        if (onResolveTemplate) onResolveTemplate(resolved);
        else onChange(resolved);
      }
      // If we couldn't resolve (unknown key, complex expression, no
      // matching account in the filtered list) we deliberately leave the
      // value alone. The dropdown will render empty via the defensive
      // sanitization below; the user can either pick an option (which
      // overwrites the template via the cascading onChange) or flip back
      // to Expression mode without losing their template.
    }

    // Select → Expression after a previous resolution: restore the original
    // template if the dropdown is still pointing at the resolved UUID. If
    // the user picked a different account, the stash was already cleared
    // by the value-watch effect above and we just toggle the mode.
    if (newMode === 'expression' && stashedTemplateRef.current && value === stashedResolvedRef.current) {
      const template = stashedTemplateRef.current;
      stashedTemplateRef.current = null;
      stashedResolvedRef.current = null;
      if (onResolveTemplate) onResolveTemplate(template);
      else onChange(template);
    }

    setMode(newMode);
  };

  // Defensive sanitization: if the saved value isn't a known UUID and isn't a
  // template, render the dropdown empty so the user sees a placeholder
  // instead of a raw UUID. The clearing effect upstream handles the cascade.
  const renderedDropdownValue = value && options.length > 0 && !options.some((o: any) => o?.value === value) ? '' : value;

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.75 }}>
        <ToggleButtonGroup value={mode} exclusive onChange={handleModeChange} size='small' disabled={disabled}>
          <ToggleButton
            value='select'
            sx={{
              px: 1.5,
              py: 0.25,
              fontSize: 'var(--ds-text-caption)',
              textTransform: 'none',
              borderColor: 'var(--ds-gray-300)',
              '&.Mui-selected': {
                backgroundColor: 'var(--ds-blue-200)',
                color: 'var(--ds-purple-600)',
                borderColor: 'var(--ds-brand-200)',
                '&:hover': { backgroundColor: 'var(--ds-brand-200)' },
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
              fontSize: 'var(--ds-text-caption)',
              textTransform: 'none',
              borderColor: 'var(--ds-gray-300)',
              '&.Mui-selected': {
                backgroundColor: 'var(--ds-red-100)',
                color: 'var(--ds-red-600)',
                borderColor: 'var(--ds-red-300)',
                '&:hover': { backgroundColor: 'var(--ds-red-200)' },
              },
            }}
          >
            <Code sx={{ fontSize: 14, mr: 0.5 }} />
            {'{{ }} Expression'}
          </ToggleButton>
        </ToggleButtonGroup>
      </Box>

      {mode === 'select' && (
        <FormField
          {...defaultFormFieldProps}
          description={description || ''}
          value={renderedDropdownValue}
          onChange={(e: any) => onChange(e.target.value)}
          placeholder={placeholder || `Select ${fieldName.replace(/_/g, ' ')}`}
          disabled={disabled}
          error={error || ''}
          fieldType='dropdown'
          options={options as any}
          required={required}
          minWidth='100%'
          maxLength={0}
          groupByCloudProvider
        />
      )}

      {mode === 'expression' && (
        <>
          <TemplateTextField
            value={value}
            onChange={onChange}
            placeholder='e.g. {{ Configs.k8s_dev_account_id }}'
            disabled={disabled}
            required={required}
            previousTasks={previousTasks}
            workflowInputs={workflowInputs}
            workflowConfigs={workflowConfigs}
            fullWidth
          />
          <ResolutionHint value={value} options={options} workflowConfigs={workflowConfigs} />
        </>
      )}
    </Box>
  );
};

interface ResolutionHintProps {
  value: string;
  options: any[];
  workflowConfigs: Array<{ key: string; value: string; type: string }>;
}

/**
 * Inline hint shown beneath the Expression-mode editor that tells the user
 * what their template currently resolves to. Three states:
 *   1. Empty / non-template — render nothing.
 *   2. Resolves to a known account UUID — show "Resolves to: <name>" with a
 *      tooltip carrying the underlying UUID.
 *   3. Resolves but the UUID isn't in the filtered options, OR the
 *      expression isn't a simple Configs.* lookup — show "Resolves at
 *      runtime" with an ErrorOutline so the user knows we couldn't preview.
 */
const ResolutionHint: React.FC<ResolutionHintProps> = ({ value, options, workflowConfigs }) => {
  if (!isTemplateString(value)) return null;

  const resolved = resolveConfigsTemplate(value, workflowConfigs);
  const matchedOption = resolved ? options.find((o: any) => o?.value === resolved) : null;

  if (matchedOption) {
    return (
      <CustomTooltip title={`Account: ${matchedOption.label}`} placement='top'>
        <Typography
          component='span'
          sx={{
            display: 'inline-block',
            mt: 0.75,
            fontSize: 'var(--ds-text-caption)',
            fontFamily: 'monospace',
            color: 'var(--ds-brand-400)',
            cursor: 'default',
          }}
        >
          {resolved}
        </Typography>
      </CustomTooltip>
    );
  }

  // Either the template referenced an unknown Configs key, or it's a more
  // complex expression we don't try to evaluate (filters, logic, nested
  // lookups). The renderer can't preview it but the runtime will still
  // resolve it.
  const previewLabel = resolved ? resolved : 'Resolves at runtime';
  const tooltipMsg = resolved
    ? 'No matching account in the current account list. Will be resolved at runtime.'
    : 'Could not preview this expression. It will be evaluated at runtime against the workflow Configs / Inputs / Tasks context.';

  return (
    <CustomTooltip title={tooltipMsg} placement='top'>
      <Typography
        component='span'
        sx={{
          display: 'inline-block',
          mt: 0.75,
          fontSize: 'var(--ds-text-caption)',
          fontFamily: resolved ? 'monospace' : 'inherit',
          color: 'var(--ds-gray-400)',
          cursor: 'default',
        }}
      >
        {previewLabel}
      </Typography>
    </CustomTooltip>
  );
};

export default AccountField;
