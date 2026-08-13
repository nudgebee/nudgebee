import React, { useState } from 'react';
import { Box, Switch, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import TemplateTextField, { TemplateSuggestion } from '../TemplateTextField';
import HybridField from '../HybridField';
import { StableNumberField } from '../StableFormFields';
import {
  JsonEditor,
  ArrayEditor,
  PasswordField,
  DurationInput,
  KeyValueHybridField,
  MultiSelectChips,
  CodeEditorWithLanguage,
} from '../WorkflowFieldComponents';
import {
  DBMS_OPTIONS,
  FIELD_PLACEHOLDERS,
  formatFieldLabel,
  getCodeLanguage,
  getDropdownOptionsForField,
  isTemplateString,
  resolveFieldType,
  type SchemaProperty,
} from '../../utils/fieldTypeUtils';
import { useTaskFormData } from '../../hooks/data-fetchers/useTaskFormData';
import type { PreviousTask } from '../../utils/templateUtils';

interface SubTaskParamFormProps {
  // Task definition ({ name, input_schema }) for the sub-task's selected type
  taskDefinition: any;
  values: Record<string, any>;
  onChange: (fieldName: string, value: any) => void;
  // De-prefixed error slice for this sub-task's params (plain field-name keys)
  errors: Record<string, string>;
  disabled?: boolean;
  // Container-scoped template suggestions surfaced in every template field
  // (e.g. `{{ LoopItem.<itemVar> }}` for foreach; empty for group).
  extraSuggestions?: TemplateSuggestion[];
  previousTasks: PreviousTask[];
  workflowInputs: Array<{ id: string; type: string; description?: string }>;
  workflowConfigs: Array<{ key: string; value: string; type: string }>;
}

const LABEL_SX = {
  fontSize: 'var(--ds-text-small)',
  fontWeight: 'var(--ds-font-weight-medium)',
  color: ds.gray[700],
  minWidth: '110px',
  maxWidth: '110px',
  pt: 1,
} as const;

/**
 * Schema-driven parameter form for a single container sub-task (foreach / group).
 * A lean re-composition of ActionDetailsSidebar's generic renderField path: same
 * field-type resolution (resolveFieldType) and the same standalone field
 * components, without the sidebar-only branches (ticket dynamic fields,
 * channel pickers, resource cascades) — those degrade to template text
 * fields, which is the accepted v1 fidelity gap.
 */
const SubTaskParamForm: React.FC<SubTaskParamFormProps> = ({
  taskDefinition,
  values,
  onChange,
  errors,
  disabled = false,
  extraSuggestions = [],
  previousTasks,
  workflowInputs,
  workflowConfigs,
}) => {
  const [draggingOverField, setDraggingOverField] = useState<string | null>(null);

  const inputSchema: Record<string, SchemaProperty> = taskDefinition?.input_schema || {};

  // Fetch dropdown option sources for THIS sub-task's schema — the sidebar's
  // own useTaskFormData call is keyed to the container type (no account/
  // integration/notification/ticket fields), so it never fires the fetches
  // sub-task dropdowns need. Passing `values` keeps the account → namespace /
  // kind cascades working per sub-task.
  const { cloudAccounts, integrations, namespaces, notifications, ticketConfigurations, resourceTypes, workloadKinds } = useTaskFormData(
    taskDefinition,
    taskDefinition?.name ?? null,
    values
  );
  const dropdownData = {
    cloudAccounts,
    integrations,
    notifications,
    ticketConfigurations,
    namespaces,
    resourceTypes,
    workloadKinds,
    dbmsOptions: DBMS_OPTIONS,
  };

  const handleDrop = (e: React.DragEvent, fieldName: string, currentValue: any) => {
    e.preventDefault();
    e.stopPropagation();
    setDraggingOverField(null);
    const template = e.dataTransfer.getData('text/plain');
    if (template) {
      const base = typeof currentValue === 'string' ? currentValue : '';
      onChange(fieldName, base ? `${base} ${template}` : template);
    }
  };

  const handleDragOver = (e: React.DragEvent, fieldName: string) => {
    e.preventDefault();
    e.stopPropagation();
    setDraggingOverField(fieldName);
  };

  const handleDragLeave = () => setDraggingOverField(null);

  const isFieldRequired = (fieldName: string, fieldSchema: SchemaProperty): boolean => {
    if (fieldSchema.required) return true;
    if (fieldSchema.required_when) {
      const { field, value } = fieldSchema.required_when;
      const currentValue = values?.[field] ?? inputSchema[field]?.default;
      if (currentValue !== undefined && currentValue !== null && value.includes(currentValue)) {
        return true;
      }
    }
    return false;
  };

  const renderField = (fieldName: string, fieldSchema: SchemaProperty) => {
    // Mirror the sidebar's visible_when gate so conditional fields hide the same way
    if (fieldSchema.visible_when) {
      const { field, value } = fieldSchema.visible_when;
      const currentValue = values?.[field] ?? inputSchema[field]?.default;
      if (currentValue === undefined || currentValue === null || !value.includes(currentValue)) {
        return null;
      }
    }

    const isRequired = isFieldRequired(fieldName, fieldSchema);
    const isObjectType = fieldSchema.type === 'object' || fieldSchema.type === 'map[string]string' || fieldSchema.type === 'map[string]any';
    const rawValue = values[fieldName] ?? fieldSchema.default ?? (isObjectType ? {} : '');
    // Coerce string values for object-type fields; template strings pass through
    // so KeyValueHybridField can flip to expression mode (same as the sidebar).
    let fieldValue = rawValue;
    if (isObjectType && typeof rawValue === 'string') {
      if (isTemplateString(rawValue)) {
        fieldValue = rawValue;
      } else if (rawValue.trim()) {
        try {
          fieldValue = JSON.parse(rawValue);
        } catch {
          fieldValue = {};
        }
      } else {
        fieldValue = {};
      }
    }

    const error = errors[fieldName] || '';
    const label = fieldSchema.title || formatFieldLabel(fieldName);
    const placeholder = FIELD_PLACEHOLDERS[fieldName] || fieldSchema.description || `Enter ${fieldName.replace(/_/g, ' ')}`;
    // The sub-task form skips the sidebar's resource-name cascade heuristic
    // (no hasResourceNameField opt-in) — `name` renders as a template field.
    const fieldType = resolveFieldType(fieldName, fieldSchema);

    const labelEl = (
      <Typography sx={LABEL_SX}>
        {label}
        {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
      </Typography>
    );

    const description = fieldSchema.description ? (
      <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
        {fieldSchema.description}
      </Typography>
    ) : null;

    const row = (control: React.ReactNode, withDescription = true) => (
      <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 1.5, flexWrap: 'wrap' }}>
        {labelEl}
        <Box sx={{ flex: '1 1 240px', minWidth: '200px' }}>
          {control}
          {withDescription && description}
        </Box>
      </Box>
    );

    if (fieldType === 'switch') {
      return row(
        <Switch
          id={`subtask-bool-${fieldName}-switch`}
          checked={fieldValue || false}
          onChange={(e) => onChange(fieldName, e.target.checked)}
          disabled={disabled}
          sx={{ alignSelf: 'flex-start', ml: -1 }}
        />
      );
    }

    if (fieldType === 'password') {
      return row(
        <PasswordField
          value={fieldValue}
          onChange={(value) => onChange(fieldName, value)}
          error={error}
          disabled={disabled}
          placeholder={placeholder}
        />
      );
    }

    if (fieldType === 'number') {
      return (
        <Box key={fieldName} sx={{ mb: 2 }}>
          <StableNumberField
            fieldName={fieldName}
            value={fieldValue}
            onChange={onChange}
            label={label}
            isRequired={isRequired}
            description={fieldSchema.description || ''}
            disabled={disabled}
            error={error}
            isInteger={fieldSchema.type === 'integer'}
          />
        </Box>
      );
    }

    if (fieldType === 'duration') {
      return row(<DurationInput value={fieldValue} onChange={(value) => onChange(fieldName, value)} error={error} disabled={disabled} />);
    }

    if (fieldType === 'keyvalue') {
      return row(
        <KeyValueHybridField
          value={fieldValue as Record<string, string> | string}
          onChange={(value) => onChange(fieldName, value)}
          error={error}
          disabled={disabled}
          previousTasks={previousTasks}
          workflowInputs={workflowInputs}
          workflowConfigs={workflowConfigs}
        />
      );
    }

    if (fieldType === 'multiselect') {
      const options = fieldSchema.options || fieldSchema.enum || [];
      const selected = Array.isArray(fieldValue) ? fieldValue : [];
      return row(
        <MultiSelectChips value={selected} options={options} onChange={(value) => onChange(fieldName, value)} error={error} disabled={disabled} />
      );
    }

    if (fieldType === 'dropdown') {
      const options = getDropdownOptionsForField(fieldName, fieldSchema, dropdownData);
      return row(
        <HybridField
          fieldName={fieldName}
          value={typeof fieldValue === 'string' ? fieldValue : ''}
          onChange={(newValue: string) => onChange(fieldName, newValue)}
          placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
          disabled={disabled}
          error={error}
          required={isRequired}
          options={options}
          previousTasks={previousTasks}
          workflowInputs={workflowInputs}
          workflowConfigs={workflowConfigs}
          onDrop={disabled ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
          onDragOver={disabled ? undefined : (e) => handleDragOver(e, fieldName)}
          onDragLeave={disabled ? undefined : handleDragLeave}
          isDropTarget={draggingOverField === fieldName}
        />,
        false
      );
    }

    if (fieldType === 'script') {
      return row(
        <CodeEditorWithLanguage
          value={typeof fieldValue === 'string' ? fieldValue : ''}
          onChange={(value) => onChange(fieldName, value)}
          language={getCodeLanguage(fieldName, fieldSchema.sub_type)}
          error={error}
          disabled={disabled}
          placeholder={placeholder}
          height='160px'
        />
      );
    }

    if (fieldType === 'array') {
      // Templates resolve to arrays at runtime — keep them editable as text
      if (typeof fieldValue === 'string' && isTemplateString(fieldValue)) {
        return row(
          <TemplateTextField
            value={fieldValue}
            onChange={(newValue: string) => onChange(fieldName, newValue)}
            placeholder={placeholder}
            disabled={disabled}
            error={error}
            required={isRequired}
            previousTasks={previousTasks}
            workflowInputs={workflowInputs}
            workflowConfigs={workflowConfigs}
            builtinVariables={extraSuggestions}
            fullWidth
          />
        );
      }
      const arrayValue = Array.isArray(fieldValue) ? fieldValue : [];
      return row(<ArrayEditor value={arrayValue} onChange={(value) => onChange(fieldName, value)} error={error} />);
    }

    if (fieldType === 'json' || fieldType === 'nested_schema') {
      return row(<JsonEditor value={fieldValue} onChange={(value) => onChange(fieldName, value)} error={error} />);
    }

    // textarea / timestamp / resource_dropdown / textfield → template-aware
    // text input with container suggestions and Available Data drag-drop.
    const isMultiline = fieldType === 'textarea';
    const isDropTarget = draggingOverField === fieldName;
    const dropHandlers = !disabled
      ? {
          onDrop: (e: React.DragEvent) => handleDrop(e, fieldName, fieldValue),
          onDragOver: (e: React.DragEvent) => handleDragOver(e, fieldName),
          onDragLeave: handleDragLeave,
        }
      : {};

    return (
      <Box
        key={fieldName}
        {...dropHandlers}
        sx={{
          transition: 'all 0.2s ease',
          borderRadius: ds.radius.sm,
          ...(isDropTarget && {
            outline: '2px dashed var(--ds-blue-400)',
            outlineOffset: 2,
            backgroundColor: 'var(--ds-blue-100)',
          }),
        }}
      >
        <Box sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 1.5, flexWrap: 'wrap' }}>
          {labelEl}
          <Box sx={{ flex: '1 1 240px', minWidth: '200px' }}>
            <TemplateTextField
              value={typeof fieldValue === 'string' ? fieldValue : String(fieldValue ?? '')}
              onChange={(newValue: string) => onChange(fieldName, newValue)}
              placeholder={placeholder}
              disabled={disabled}
              error={error}
              required={isRequired}
              previousTasks={previousTasks}
              workflowInputs={workflowInputs}
              workflowConfigs={workflowConfigs}
              builtinVariables={extraSuggestions}
              multiline={isMultiline}
              rows={isMultiline ? 5 : undefined}
              maxRows={isMultiline ? 10 : undefined}
              fullWidth
            />
          </Box>
        </Box>
      </Box>
    );
  };

  // Same ordering as the sidebar's parameters tab: schema order, then required first
  const fields = Object.entries(inputSchema)
    .filter(([, fieldSchema]) => !(fieldSchema as SchemaProperty).hidden)
    .sort(([, schemaA], [, schemaB]) => {
      const sA = schemaA as SchemaProperty;
      const sB = schemaB as SchemaProperty;
      const orderA = sA.order ?? 0;
      const orderB = sB.order ?? 0;
      if (orderA !== orderB) return orderA - orderB;
      const requiredA = sA.required === true;
      const requiredB = sB.required === true;
      if (requiredA !== requiredB) return requiredA ? -1 : 1;
      return 0;
    });

  if (fields.length === 0) {
    return <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], mb: 1 }}>This action has no configurable parameters.</Typography>;
  }

  return <Box>{fields.map(([fieldName, fieldSchema]) => renderField(fieldName, fieldSchema as SchemaProperty))}</Box>;
};

export default SubTaskParamForm;
