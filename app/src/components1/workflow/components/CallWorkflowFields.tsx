import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Typography, Switch, FormControlLabel, Alert } from '@mui/material';
import { colors } from 'src/utils/colors';
import apiWorkflow from '@api1/workflow';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import TemplateTextField from './TemplateTextField';
import { JsonEditor } from './WorkflowFieldComponents';

interface PreviousTask {
  id: string;
  name: string;
  type: string;
  outputSchema?: any;
}

interface WorkflowInput {
  id: string;
  type: string;
  description?: string;
  default?: any;
}

interface ListedWorkflow {
  id: string;
  name: string;
  status?: string;
  definition?: {
    inputs?: WorkflowInput[];
    triggers?: any[];
  };
}

interface CallWorkflowFieldsProps {
  accountId?: string;
  taskData: any;
  onTaskDataChange: (taskData: any) => void;
  viewOnlyMode?: boolean;
  validationErrors?: Record<string, string>;
  previousTasks?: PreviousTask[];
  workflowInputs?: WorkflowInput[];
  workflowConfigs?: Array<{ key: string; value: string; type: string }>;
  currentWorkflowId?: string;
}

const LABEL_COL_SX = {
  fontSize: 'var(--ds-text-body)',
  fontWeight: 'var(--ds-font-weight-medium)',
  color: colors.text.secondary,
  minWidth: '120px',
  maxWidth: '120px',
  pt: 1,
};

const FIELD_COL_SX = { flex: '1 1 300px', minWidth: '200px' };

// Render a field that swaps between literal editor (per input type) and template text field.
// Workflow inputs commonly accept either a concrete value or a templated `{{ Inputs.x }}` reference,
// so we keep TemplateTextField as the primary editor (it accepts both) and only switch to JsonEditor
// for object-typed inputs.
const InputValueEditor: React.FC<{
  input: WorkflowInput;
  value: any;
  onChange: (v: any) => void;
  disabled?: boolean;
  previousTasks: PreviousTask[];
  workflowInputs: WorkflowInput[];
  workflowConfigs: Array<{ key: string; value: string; type: string }>;
  error?: string;
}> = ({ input, value, onChange, disabled, previousTasks, workflowInputs, workflowConfigs, error }) => {
  const type = (input.type || 'string').toLowerCase();
  const isObjectType = type === 'object' || type === 'map[string]any' || type === 'map[string]string' || type === 'array' || type === 'list';

  if (isObjectType && typeof value !== 'string') {
    return (
      <JsonEditor
        value={value ?? input.default ?? (type === 'array' || type === 'list' ? [] : {})}
        onChange={(v) => {
          if (disabled) return;
          onChange(v);
        }}
        error={error}
      />
    );
  }

  return (
    <TemplateTextField
      value={typeof value === 'string' ? value : value == null ? '' : String(value)}
      onChange={(v) => onChange(v)}
      placeholder={
        input.default !== undefined ? `default: ${typeof input.default === 'object' ? JSON.stringify(input.default) : String(input.default)}` : ''
      }
      disabled={disabled}
      previousTasks={previousTasks}
      workflowInputs={workflowInputs}
      workflowConfigs={workflowConfigs}
      multiline={isObjectType}
      rows={isObjectType ? 3 : 1}
      maxRows={isObjectType ? 6 : 1}
      error={error}
    />
  );
};

const CallWorkflowFields: React.FC<CallWorkflowFieldsProps> = ({
  accountId,
  taskData,
  onTaskDataChange,
  viewOnlyMode = false,
  validationErrors = {},
  previousTasks = [],
  workflowInputs = [],
  workflowConfigs = [],
  currentWorkflowId,
}) => {
  const [workflows, setWorkflows] = useState<ListedWorkflow[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string>('');
  const [advancedInputs, setAdvancedInputs] = useState(false);

  const workflowName: string = taskData?.workflow_name ?? '';
  const rawInputs: any = taskData?.inputs;
  const inputsObject: Record<string, any> = useMemo(() => {
    if (rawInputs && typeof rawInputs === 'object' && !Array.isArray(rawInputs)) return rawInputs;
    return {};
  }, [rawInputs]);

  // Stable ref to taskData to merge without dropping unrelated fields.
  const taskDataRef = useRef(taskData);
  taskDataRef.current = taskData;

  // Fetch the workflow list once an account is known. The picker is bound to the current account
  // and filters out the workflow we are editing (a workflow cannot call itself — that would loop).
  useEffect(() => {
    if (!accountId) return;
    let cancelled = false;
    setLoading(true);
    setLoadError('');
    (async () => {
      try {
        const result: any = await apiWorkflow.listWorkflows(accountId);
        if (cancelled) return;
        const list = result?.data?.workflow_list?.workflows ?? [];
        setWorkflows(list);
      } catch (err: any) {
        if (cancelled) return;
        setLoadError(err?.message || 'Failed to load workflows');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [accountId]);

  // Build picker options for FilterDropdownButton. Each option exposes the workflow name as the
  // value (matches backend `workflow_name` lookup), the input list inline in the label so users
  // can pick the right workflow at a glance, and the status chip via the `type` slot.
  const pickerOptions = useMemo(
    () =>
      workflows
        .filter((w) => !currentWorkflowId || w.id !== currentWorkflowId)
        .map((w) => {
          const inputs = w.definition?.inputs ?? [];
          const inputSummary = inputs.length > 0 ? ` — inputs: ${inputs.map((i) => i.id).join(', ')}` : '';
          return {
            label: `${w.name}${inputSummary}`,
            value: w.name,
            type: w.status,
          };
        }),
    [workflows, currentWorkflowId]
  );

  // Resolve the workflow object matching the configured name so we can derive its inputs.
  // We match on name (the backend `core.call-workflow` task looks up by name too); when the
  // value contains template syntax there is no concrete workflow to resolve.
  const isTemplated = /\{\{|\{%/.test(workflowName);
  const selectedWorkflow = useMemo(() => {
    if (!workflowName || isTemplated) return undefined;
    return workflows.find((w) => w.name === workflowName);
  }, [workflows, workflowName, isTemplated]);

  const targetInputs: WorkflowInput[] = selectedWorkflow?.definition?.inputs ?? [];

  const setWorkflowName = useCallback(
    (next: string) => {
      const merged = { ...(taskDataRef.current || {}), workflow_name: next };
      onTaskDataChange(merged);
    },
    [onTaskDataChange]
  );

  const setSingleInput = useCallback(
    (key: string, value: any) => {
      const current = taskDataRef.current?.inputs;
      const base = current && typeof current === 'object' && !Array.isArray(current) ? current : {};
      const nextInputs: Record<string, any> = { ...base };
      // Empty string is treated as "field cleared"; keep the key out of the payload so the
      // called workflow falls back to its own default.
      if (value === '' || value === undefined) {
        delete nextInputs[key];
      } else {
        nextInputs[key] = value;
      }
      const merged = {
        ...(taskDataRef.current || {}),
        inputs: Object.keys(nextInputs).length > 0 ? nextInputs : undefined,
      };
      onTaskDataChange(merged);
    },
    [onTaskDataChange]
  );

  const setInputsRaw = useCallback(
    (raw: any) => {
      const merged = { ...(taskDataRef.current || {}), inputs: raw };
      onTaskDataChange(merged);
    },
    [onTaskDataChange]
  );

  const nameError = validationErrors['workflow_name'];

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, mb: 1 }}>
        Run another workflow by name and return its result. Inputs are forwarded to the called workflow; outputs are available as{' '}
        <code>Tasks[&apos;this-task-id&apos;].output</code>.
      </Typography>

      {/* Workflow picker */}
      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
        <Typography sx={LABEL_COL_SX}>
          Workflow<span style={{ color: colors.border.error }}> *</span>
        </Typography>
        <Box sx={FIELD_COL_SX}>
          <FilterDropdownButton
            id='call-workflow-name-picker'
            freeSolo
            disabled={viewOnlyMode}
            isOptionsLoading={loading}
            required
            options={pickerOptions}
            value={workflowName || ''}
            onSelect={(_e: any, next: any) => {
              const raw = next && typeof next === 'object' ? next.value ?? '' : next ?? '';
              setWorkflowName(typeof raw === 'string' ? raw : '');
            }}
            placeholder='Select a workflow or type a name / {{ template }}'
            searchPlaceholder='Search workflows...'
            sx={{ width: '100%' }}
          />
          {(() => {
            if (nameError) {
              return <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.border.error, mt: 0.5 }}>{nameError}</Typography>;
            }
            const helper = isTemplated
              ? 'Templated workflow name — inputs cannot be auto-detected; use raw JSON below.'
              : selectedWorkflow
              ? `${targetInputs.length} input${targetInputs.length === 1 ? '' : 's'}`
              : workflowName
              ? 'No workflow with this name in the current account.'
              : '';
            if (!helper) return null;
            return <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary, mt: 0.5 }}>{helper}</Typography>;
          })()}
          {loadError ? (
            <Alert severity='warning' sx={{ mt: 1, py: 0 }}>
              {loadError}
            </Alert>
          ) : null}
        </Box>
      </Box>

      {/* Inputs */}
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
            Inputs
          </Typography>
          <FormControlLabel
            control={
              <Switch
                size='small'
                checked={advancedInputs}
                onChange={(e) => setAdvancedInputs(e.target.checked)}
                disabled={viewOnlyMode}
                data-testid='call-workflow-advanced-toggle'
              />
            }
            label={<Typography sx={{ fontSize: 'var(--ds-text-small)' }}>Raw JSON</Typography>}
            sx={{ m: 0 }}
          />
        </Box>

        {advancedInputs || isTemplated || !selectedWorkflow ? (
          <>
            {!selectedWorkflow && !isTemplated && workflowName ? (
              <Alert severity='info' sx={{ py: 0 }}>
                Pick an existing workflow above to get a per-input editor. You can still pass inputs as a JSON object.
              </Alert>
            ) : null}
            <JsonEditor
              value={rawInputs ?? {}}
              onChange={(v) => {
                if (viewOnlyMode) return;
                setInputsRaw(v);
              }}
            />
          </>
        ) : targetInputs.length === 0 ? (
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontStyle: 'italic' }}>
            This workflow takes no inputs.
          </Typography>
        ) : (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
            {targetInputs.map((input) => {
              const fieldErr = validationErrors[`inputs.${input.id}`];
              return (
                <Box key={input.id} sx={{ display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
                  <Typography sx={LABEL_COL_SX}>
                    {input.id}
                    {input.default === undefined ? <span style={{ color: colors.border.error }}> *</span> : null}
                    <Typography component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark, ml: 0.5 }}>
                      ({input.type || 'string'})
                    </Typography>
                  </Typography>
                  <Box sx={FIELD_COL_SX}>
                    <InputValueEditor
                      input={input}
                      value={inputsObject[input.id]}
                      onChange={(v) => setSingleInput(input.id, v)}
                      disabled={viewOnlyMode}
                      previousTasks={previousTasks}
                      workflowInputs={workflowInputs}
                      workflowConfigs={workflowConfigs}
                      error={fieldErr}
                    />
                    {input.description ? (
                      <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                        {input.description}
                      </Typography>
                    ) : null}
                  </Box>
                </Box>
              );
            })}
          </Box>
        )}
      </Box>
    </Box>
  );
};

export default CallWorkflowFields;
