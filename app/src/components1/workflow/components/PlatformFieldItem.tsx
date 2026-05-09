import React from 'react';
import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import HybridField from './HybridField';
import TemplateTextField from './TemplateTextField';
import { TimestampPicker, MultiSelectChips } from './WorkflowFieldComponents';

export interface TicketFieldMeta {
  key: string;
  name: string;
  type: string;
  required?: boolean;
  allowedValues?: any[];
  autoCompleteUrl?: string;
}

type Option = { label: string; value: string };

interface PreviousTask {
  id: string;
  name: string;
  type: string;
  outputSchema?: any;
}

interface PlatformFieldItemProps {
  fieldKey: string;
  fieldMeta: TicketFieldMeta;
  localData: Record<string, any>;
  dynamicOptions: Option[];
  dynamicLoading: boolean;
  viewOnlyMode: boolean;
  previousTasks: PreviousTask[];
  workflowInputs: any[];
  workflowConfigs: any[];
  onDataChange: (field: string, value: any) => void;
  onSearchField: (fieldKey: string, query: string) => void;
}

const isUserFieldKey = (key: string, type: string): boolean => key === 'assignee' || type === 'user';

// Seed the field value from additional_fields; fall back to the legacy top-level key
// for user fields (saved workflows written before assignee moved into additional_fields).
const resolveDynamicValue = (fieldKey: string, localData: Record<string, any>, isUserField: boolean): any => {
  const additional = localData?.additional_fields;
  if (additional && typeof additional === 'object') {
    const v = additional[fieldKey];
    if (v !== undefined && v !== '') return v;
  }
  if (isUserField && typeof localData?.[fieldKey] === 'string' && localData[fieldKey]) {
    return localData[fieldKey];
  }
  return '';
};

// Select/user fields sometimes arrive as { id: "..." } — unwrap for display.
const resolveDisplayValue = (value: any, type: string, isUserField: boolean): any => {
  const wrapsId = (type === 'select' || isUserField) && typeof value === 'object' && value?.id;
  return wrapsId ? value.id : value;
};

// Async user fields fetch options per keystroke; a saved accountId may not be in the
// current cache. Synthesize a placeholder so the Autocomplete still renders the value.
const resolveDisplayOptions = (options: Option[], resolvedValue: any, isUserField: boolean): Option[] => {
  if (!isUserField || !resolvedValue) return options;
  const asString = String(resolvedValue);
  if (options.some((o) => o.value === asString)) return options;
  return [{ label: asString, value: asString }, ...options];
};

const labelForArrayValue = (value: any, options: Option[]): string => {
  const opt = options.find((o) => o.value === value || o.value === value?.id);
  return opt ? opt.label : String(value);
};

const ROW_SX = { mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' } as const;
const CELL_SX = { flex: '1 1 300px', minWidth: '200px' } as const;
const LABEL_SX = {
  fontSize: '13px',
  fontWeight: 500,
  color: colors.text.secondary,
  minWidth: '120px',
  maxWidth: '120px',
  pt: 1,
} as const;
const REQUIRED_MARKER_STYLE = { color: colors.border.error };

const FieldLabel: React.FC<{ meta: TicketFieldMeta }> = ({ meta }) => (
  <Typography sx={LABEL_SX}>
    {meta.name}
    {meta.required && <span style={REQUIRED_MARKER_STYLE}> *</span>}
  </Typography>
);

type FieldKind = 'select' | 'array' | 'datetime' | 'text';

const resolveFieldKind = (type: string, isUserField: boolean): FieldKind => {
  if (type === 'select' || isUserField) return 'select';
  if (type === 'array' || type === 'multicheckboxes') return 'array';
  if (type === 'datepicker' || type === 'datetime') return 'datetime';
  return 'text';
};

const wrapStoredValue = (fieldKey: string, type: string, value: any): any => {
  // Jira custom fields expect the { id: value } shape on the wire.
  const wrapAsId = type === 'select' && fieldKey.startsWith('customfield_') && typeof value === 'string' && value;
  return wrapAsId ? { id: value } : value;
};

interface SelectFieldProps {
  fieldKey: string;
  fieldMeta: TicketFieldMeta;
  isUserField: boolean;
  resolvedValue: any;
  displayOptions: Option[];
  dynamicLoading: boolean;
  viewOnlyMode: boolean;
  previousTasks: PreviousTask[];
  workflowInputs: any[];
  workflowConfigs: any[];
  onChange: (value: any) => void;
  onSearchField: (fieldKey: string, query: string) => void;
}

const SelectField: React.FC<SelectFieldProps> = ({
  fieldKey,
  fieldMeta,
  isUserField,
  resolvedValue,
  displayOptions,
  dynamicLoading,
  viewOnlyMode,
  previousTasks,
  workflowInputs,
  workflowConfigs,
  onChange,
  onSearchField,
}) => (
  <Box sx={ROW_SX}>
    <FieldLabel meta={fieldMeta} />
    <Box sx={CELL_SX}>
      <HybridField
        fieldName={fieldKey}
        value={String(resolvedValue || '')}
        onChange={onChange}
        placeholder={isUserField ? `Search ${fieldMeta.name}` : `Select ${fieldMeta.name}`}
        disabled={viewOnlyMode}
        error={''}
        required={fieldMeta.required || false}
        options={displayOptions}
        optionsLoading={dynamicLoading}
        previousTasks={previousTasks}
        workflowInputs={workflowInputs}
        workflowConfigs={workflowConfigs}
        onSearch={isUserField ? (q: string) => onSearchField(fieldKey, q) : undefined}
      />
    </Box>
  </Box>
);

interface ArrayFieldProps {
  fieldMeta: TicketFieldMeta;
  dynamicValue: any;
  dynamicOptions: Option[];
  viewOnlyMode: boolean;
  previousTasks: PreviousTask[];
  workflowInputs: any[];
  workflowConfigs: any[];
  onChange: (value: any) => void;
  onArrayChange: (selectedLabels: string[]) => void;
}

const ArrayField: React.FC<ArrayFieldProps> = ({
  fieldMeta,
  dynamicValue,
  dynamicOptions,
  viewOnlyMode,
  previousTasks,
  workflowInputs,
  workflowConfigs,
  onChange,
  onArrayChange,
}) => {
  const arrayLabels = Array.isArray(dynamicValue) ? dynamicValue.map((v: any) => labelForArrayValue(v, dynamicOptions)) : [];
  const hasOptions = dynamicOptions.length > 0;
  return (
    <Box sx={ROW_SX}>
      <FieldLabel meta={fieldMeta} />
      <Box sx={CELL_SX}>
        {hasOptions ? (
          <MultiSelectChips options={dynamicOptions.map((o) => o.label)} value={arrayLabels} onChange={onArrayChange} disabled={viewOnlyMode} />
        ) : (
          <TemplateTextField
            value={typeof dynamicValue === 'string' ? dynamicValue : JSON.stringify(dynamicValue || [])}
            onChange={onChange}
            placeholder={`Enter ${fieldMeta.name} (JSON array or template expression)`}
            disabled={viewOnlyMode}
            previousTasks={previousTasks}
            workflowInputs={workflowInputs}
            workflowConfigs={workflowConfigs}
          />
        )}
      </Box>
    </Box>
  );
};

interface DateTimeFieldProps {
  fieldMeta: TicketFieldMeta;
  dynamicValue: any;
  viewOnlyMode: boolean;
  onChange: (value: any) => void;
}

const DateTimeField: React.FC<DateTimeFieldProps> = ({ fieldMeta, dynamicValue, viewOnlyMode, onChange }) => (
  <Box sx={ROW_SX}>
    <FieldLabel meta={fieldMeta} />
    <Box sx={CELL_SX}>
      <TimestampPicker
        value={String(dynamicValue || '')}
        onChange={onChange}
        disabled={viewOnlyMode}
        includeTime={fieldMeta.type === 'datetime'}
        label={fieldMeta.name}
      />
    </Box>
  </Box>
);

interface StringFieldProps {
  fieldMeta: TicketFieldMeta;
  dynamicValue: any;
  viewOnlyMode: boolean;
  previousTasks: PreviousTask[];
  workflowInputs: any[];
  workflowConfigs: any[];
  onChange: (value: any) => void;
}

const StringField: React.FC<StringFieldProps> = ({
  fieldMeta,
  dynamicValue,
  viewOnlyMode,
  previousTasks,
  workflowInputs,
  workflowConfigs,
  onChange,
}) => (
  <Box sx={ROW_SX}>
    <FieldLabel meta={fieldMeta} />
    <Box sx={CELL_SX}>
      <TemplateTextField
        value={String(dynamicValue || '')}
        onChange={onChange}
        placeholder={`Enter ${fieldMeta.name}`}
        disabled={viewOnlyMode}
        previousTasks={previousTasks}
        workflowInputs={workflowInputs}
        workflowConfigs={workflowConfigs}
      />
    </Box>
  </Box>
);

const getCurrentAdditionalFields = (localData: Record<string, any>): Record<string, any> => {
  const additional = localData?.additional_fields;
  return additional && typeof additional === 'object' ? additional : {};
};

const buildHandleChange =
  (
    fieldKey: string,
    fieldMeta: TicketFieldMeta,
    localData: Record<string, any>,
    isUserField: boolean,
    onDataChange: (field: string, value: any) => void
  ) =>
  (value: any) => {
    const currentAdditional = getCurrentAdditionalFields(localData);
    const storedValue = wrapStoredValue(fieldKey, fieldMeta.type, value);
    onDataChange('additional_fields', { ...currentAdditional, [fieldKey]: storedValue });
    // Editing a user field also clears the legacy top-level mirror so next save is clean.
    if (isUserField && localData?.[fieldKey] !== undefined) {
      onDataChange(fieldKey, '');
    }
  };

const labelToValue = (label: string, options: Option[]): string => {
  const opt = options.find((o) => o.label === label);
  return opt ? opt.value : label;
};

const buildHandleArrayChange = (dynamicOptions: Option[], handleChange: (v: any) => void) => (selectedLabels: string[]) => {
  handleChange(selectedLabels.map((label) => labelToValue(label, dynamicOptions)));
};

const PlatformFieldItem: React.FC<PlatformFieldItemProps> = ({
  fieldKey,
  fieldMeta,
  localData,
  dynamicOptions,
  dynamicLoading,
  viewOnlyMode,
  previousTasks,
  workflowInputs,
  workflowConfigs,
  onDataChange,
  onSearchField,
}) => {
  const isUserField = isUserFieldKey(fieldKey, fieldMeta.type);
  const dynamicValue = resolveDynamicValue(fieldKey, localData, isUserField);
  const resolvedValue = resolveDisplayValue(dynamicValue, fieldMeta.type, isUserField);
  const displayOptions = resolveDisplayOptions(dynamicOptions, resolvedValue, isUserField);
  const kind = resolveFieldKind(fieldMeta.type, isUserField);

  const handleChange = buildHandleChange(fieldKey, fieldMeta, localData, isUserField, onDataChange);
  const handleArrayChange = buildHandleArrayChange(dynamicOptions, handleChange);

  if (kind === 'select') {
    return (
      <SelectField
        fieldKey={fieldKey}
        fieldMeta={fieldMeta}
        isUserField={isUserField}
        resolvedValue={resolvedValue}
        displayOptions={displayOptions}
        dynamicLoading={dynamicLoading}
        viewOnlyMode={viewOnlyMode}
        previousTasks={previousTasks}
        workflowInputs={workflowInputs}
        workflowConfigs={workflowConfigs}
        onChange={handleChange}
        onSearchField={onSearchField}
      />
    );
  }
  if (kind === 'array') {
    return (
      <ArrayField
        fieldMeta={fieldMeta}
        dynamicValue={dynamicValue}
        dynamicOptions={dynamicOptions}
        viewOnlyMode={viewOnlyMode}
        previousTasks={previousTasks}
        workflowInputs={workflowInputs}
        workflowConfigs={workflowConfigs}
        onChange={handleChange}
        onArrayChange={handleArrayChange}
      />
    );
  }
  if (kind === 'datetime') {
    return <DateTimeField fieldMeta={fieldMeta} dynamicValue={dynamicValue} viewOnlyMode={viewOnlyMode} onChange={handleChange} />;
  }
  return (
    <StringField
      fieldMeta={fieldMeta}
      dynamicValue={dynamicValue}
      viewOnlyMode={viewOnlyMode}
      previousTasks={previousTasks}
      workflowInputs={workflowInputs}
      workflowConfigs={workflowConfigs}
      onChange={handleChange}
    />
  );
};

export default PlatformFieldItem;
