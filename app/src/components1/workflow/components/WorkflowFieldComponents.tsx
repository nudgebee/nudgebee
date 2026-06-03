import React, { useEffect, useRef, useState } from 'react';
import {
  Box,
  Typography,
  TextField,
  Select,
  MenuItem,
  FormControl,
  Chip,
  Switch,
  FormControlLabel,
  ToggleButtonGroup,
  ToggleButton,
} from '@mui/material';
import { Add, Delete, DragIndicator, Visibility, VisibilityOff, ExpandMore, ExpandLess, Code, ViewList } from '@mui/icons-material';
import { Input } from '@components1/ds/Input';
import CollapsableCard from '@components1/ds/CollapsableCard';
import TemplateTextField from './TemplateTextField';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import dayjs, { type Dayjs } from 'dayjs';
import utc from 'dayjs/plugin/utc';
import timezone from 'dayjs/plugin/timezone';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { sql } from '@codemirror/lang-sql';
import { javascript } from '@codemirror/lang-javascript';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { StreamLanguage } from '@codemirror/language';
import { Button } from '@components1/ds/Button';
import { FormField } from '@components1/common/NewReusabeFormComponents';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import ReorderableList from '@components1/common/ReorderableList';
import { colors } from '@utils/colors';

// Extend dayjs with UTC and timezone support
dayjs.extend(utc);
dayjs.extend(timezone);

/**
 * TimestampPicker Component
 *
 * Renders a datetime picker that outputs ISO3339 formatted timestamps (YYYY-MM-DDThh:mm:ssTZD)
 * Supports both date-only and full datetime modes
 */
interface TimestampPickerProps {
  value: string; // ISO3339 string
  onChange: (value: string) => void;
  error?: string;
  disabled?: boolean;
  includeTime?: boolean; // false = date only, true = datetime
  label?: string;
}

export const TimestampPicker: React.FC<TimestampPickerProps> = ({
  value,
  onChange,
  error,
  disabled = false,
  includeTime = true,
  label = 'Timestamp',
}) => {
  // Parse ISO3339 string to Dayjs object
  const dayjsValue = value ? dayjs(value) : null;

  const handleChange = (newValue: unknown) => {
    const dayjsNewValue = newValue as Dayjs | null;
    if (dayjsNewValue?.isValid()) {
      // Format as ISO3339 with timezone
      const iso3339String = dayjsNewValue.toISOString();
      onChange(iso3339String);
    } else if (dayjsNewValue === null) {
      onChange('');
    }
  };

  return (
    <Box>
      <LocalizationProvider dateAdapter={AdapterDayjs}>
        <DateTimePicker
          label={label}
          value={dayjsValue}
          onChange={handleChange}
          disabled={disabled}
          views={includeTime ? ['year', 'month', 'day', 'hours', 'minutes'] : ['year', 'month', 'day']}
          renderInput={(params) => (
            <TextField
              {...params}
              fullWidth
              size='small'
              error={!!error}
              helperText={error || 'Format: MM/DD/YYYY hh:mm AM/PM'}
              sx={{
                '& .MuiInputBase-root': {
                  height: '42px',
                },
              }}
            />
          )}
        />
      </LocalizationProvider>
    </Box>
  );
};

/**
 * JsonEditor Component
 *
 * Renders a CodeMirror editor for JSON objects with syntax highlighting and validation
 */
interface JsonEditorProps {
  value: any;
  onChange: (value: any) => void;
  error?: string;
}

export const JsonEditor: React.FC<JsonEditorProps> = ({ value, onChange, error }) => {
  const [jsonString, setJsonString] = useState(() => {
    try {
      return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
    } catch {
      return '';
    }
  });

  const handleChange = (val: string) => {
    setJsonString(val);
    try {
      const parsed = JSON.parse(val);
      onChange(parsed);
    } catch {
      onChange(val); // Keep as string if invalid JSON for validation to catch
    }
  };

  return (
    <Box>
      <CodeMirror
        value={jsonString}
        height='120px'
        extensions={[json()]}
        onChange={handleChange}
        theme={undefined}
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          dropCursor: false,
          allowMultipleSelections: false,
          indentOnInput: true,
          bracketMatching: true,
          closeBrackets: true,
        }}
        style={{
          maxWidth: '500px',
          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: 'var(--ds-radius-md)',
          fontSize: 'var(--ds-text-body)',
        }}
      />
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * ArrayEditor Component
 *
 * Renders an array editor with add/remove functionality
 * Supports both string items and schema-based structured items
 */
interface ArrayItemSchema {
  type: string;
  description?: string;
  required?: boolean;
  default?: any;
  options?: string[];
  enum?: string[];
  title?: string;
  schema?: {
    properties?: Record<string, ArrayItemSchema>;
  };
}

interface ArrayEditorProps {
  value: any[];
  onChange: (value: any[]) => void;
  error?: string;
  itemSchema?: Record<string, ArrayItemSchema>;
}

export const ArrayEditor: React.FC<ArrayEditorProps> = ({ value, onChange, error, itemSchema }) => {
  const [expandedItems, setExpandedItems] = useState<Set<number>>(() => new Set(value.map((_, i) => i)));

  // For simple arrays (no itemSchema), use string representation
  const [simpleItems, setSimpleItems] = useState<string[]>(() => {
    if (itemSchema) {
      return [];
    }
    return Array.isArray(value) ? value.map((item) => (typeof item === 'string' ? item : JSON.stringify(item))) : [];
  });

  // For schema-based arrays, use the actual objects
  const items = itemSchema ? (Array.isArray(value) ? value : []) : simpleItems;

  const toggleExpand = (index: number) => {
    const newExpanded = new Set(expandedItems);
    if (newExpanded.has(index)) {
      newExpanded.delete(index);
    } else {
      newExpanded.add(index);
    }
    setExpandedItems(newExpanded);
  };

  const addItem = () => {
    if (itemSchema) {
      // Create new item with defaults from schema
      const newItem: Record<string, any> = {};
      Object.entries(itemSchema).forEach(([key, schema]) => {
        if (schema.default !== undefined) {
          newItem[key] = schema.default;
        }
      });
      const newItems = [...items, newItem];
      onChange(newItems);
      // Auto-expand new item
      setExpandedItems(new Set([...expandedItems, newItems.length - 1]));
    } else {
      const newItems = [...simpleItems, ''];
      setSimpleItems(newItems);
      updateSimpleValue(newItems);
    }
  };

  const removeItem = (index: number) => {
    if (itemSchema) {
      const newItems = items.filter((_, i) => i !== index);
      onChange(newItems);
      // Update expanded indices
      const newExpanded = new Set<number>();
      expandedItems.forEach((i) => {
        if (i < index) {
          newExpanded.add(i);
        } else if (i > index) {
          newExpanded.add(i - 1);
        }
      });
      setExpandedItems(newExpanded);
    } else {
      const newItems = simpleItems.filter((_, i) => i !== index);
      setSimpleItems(newItems);
      updateSimpleValue(newItems);
    }
  };

  const updateSimpleItem = (index: number, newValue: string) => {
    const newItems = [...simpleItems];
    newItems[index] = newValue;
    setSimpleItems(newItems);
    updateSimpleValue(newItems);
  };

  const updateSchemaItem = (index: number, newValue: Record<string, any>) => {
    const newItems = [...items];
    newItems[index] = newValue;
    onChange(newItems);
  };

  const updateSimpleValue = (newItems: string[]) => {
    const parsedItems = newItems.map((item) => {
      try {
        return JSON.parse(item);
      } catch {
        return item;
      }
    });
    onChange(parsedItems);
  };

  // Maps an old index to its new index after a single move from `from` to
  // `insertAt`. Used to keep the schema-mode `expandedItems` Set lined up
  // with the reordered array so a previously-expanded card stays expanded
  // after drag-drop.
  const remapIndexAfterMove = (idx: number, from: number, insertAt: number): number => {
    if (idx === from) return insertAt;
    if (from < idx && idx <= insertAt) return idx - 1;
    if (insertAt <= idx && idx < from) return idx + 1;
    return idx;
  };

  const handleReorderSchemaItems = (next: any[], from: number, insertAt: number) => {
    onChange(next);
    setExpandedItems((prev) => {
      const remapped = new Set<number>();
      prev.forEach((idx) => remapped.add(remapIndexAfterMove(idx, from, insertAt)));
      return remapped;
    });
  };

  const handleReorderSimpleItems = (next: string[]) => {
    setSimpleItems(next);
    updateSimpleValue(next);
  };

  // Build a short drag-preview label from a schema item: prefer the first
  // field's value (Switch case → "Approved"), fall back to the field's
  // title prefix when the value is empty (e.g. "Case Value: …"), final
  // fallback is "Item N". Rendered as a small chip following the cursor.
  const getSchemaItemDragLabel = (item: Record<string, any>, index: number): string => {
    if (!itemSchema) return `Item ${index + 1}`;
    const fieldNames = Object.keys(itemSchema);
    for (const fieldName of fieldNames) {
      const raw = item?.[fieldName];
      if (raw === undefined || raw === null || raw === '') continue;
      const text = typeof raw === 'string' ? raw : JSON.stringify(raw);
      const fieldTitle = itemSchema[fieldName]?.title || fieldName;
      const trimmed = text.length > 32 ? `${text.slice(0, 32)}…` : text;
      return `${fieldTitle}: ${trimmed}`;
    }
    return `Item ${index + 1}`;
  };

  const getSimpleItemDragLabel = (item: string, index: number): string => {
    const text = item ?? '';
    if (!text) return `Item ${index + 1}`;
    return text.length > 40 ? `${text.slice(0, 40)}…` : text;
  };

  const renderSchemaItem = (item: Record<string, any>, index: number, dragHandleProps: any) => {
    const isExpanded = expandedItems.has(index);
    const configuredCount = Object.keys(item || {}).filter((k) => item[k] !== undefined && item[k] !== '').length;

    return (
      <Box
        sx={{
          border: '1px solid var(--ds-brand-150)',
          borderRadius: 'var(--ds-radius-md)',
          mb: 1,
          backgroundColor: 'var(--ds-background-100)',
          overflow: 'hidden',
        }}
      >
        {/* Item Header */}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            p: 1,
            backgroundColor: 'var(--ds-background-200)',
            borderBottom: isExpanded ? '1px solid #e5e7eb' : 'none',
          }}
        >
          <Box {...dragHandleProps} aria-label='Drag to reorder' sx={{ display: 'flex', alignItems: 'center', mr: 0.5 }}>
            <DragIndicator sx={{ color: 'var(--ds-gray-400)', fontSize: 18 }} />
          </Box>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flex: 1, cursor: 'pointer' }} onClick={() => toggleExpand(index)}>
            <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' }}>
              Item {index + 1}
            </Typography>
            {configuredCount > 0 && (
              <Chip label={`${configuredCount} fields`} size='small' variant='outlined' sx={{ height: '18px', fontSize: 'var(--ds-text-caption)' }} />
            )}
            {isExpanded ? (
              <ExpandLess sx={{ fontSize: 18, color: 'var(--ds-gray-600)', ml: 'auto' }} />
            ) : (
              <ExpandMore sx={{ fontSize: 18, color: 'var(--ds-gray-600)', ml: 'auto' }} />
            )}
          </Box>
          <Box sx={{ ml: 1 }}>
            <Button
              composition='icon-only'
              tone='ghost'
              size='sm'
              aria-label='Remove item'
              icon={<Delete fontSize='small' sx={{ color: 'var(--ds-red-500)' }} />}
              onClick={() => removeItem(index)}
            />
          </Box>
        </Box>

        {/* Item Content */}
        {isExpanded && itemSchema && (
          <Box sx={{ p: 1.5 }}>
            {Object.entries(itemSchema).map(([fieldName, fieldSchema]) => (
              <Box key={fieldName} sx={{ mb: 1.5 }}>
                <Typography
                  sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)', mb: 0.5 }}
                >
                  {fieldSchema.title || fieldName}
                  {fieldSchema.required && <span style={{ color: colors.border.error }}> *</span>}
                </Typography>
                {fieldSchema.description && (
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', mb: 0.5 }}>{fieldSchema.description}</Typography>
                )}
                {renderItemField(fieldName, fieldSchema, item, (val) => updateSchemaItem(index, { ...item, [fieldName]: val }))}
              </Box>
            ))}
          </Box>
        )}
      </Box>
    );
  };

  const renderItemField = (fieldName: string, fieldSchema: ArrayItemSchema, item: Record<string, any>, onFieldChange: (val: any) => void) => {
    const fieldValue = item?.[fieldName];
    const options = fieldSchema.options || fieldSchema.enum;

    // Boolean type
    if (fieldSchema.type === 'boolean') {
      return <Switch checked={fieldValue === true || fieldValue === 'true'} onChange={(e) => onFieldChange(e.target.checked)} size='small' />;
    }

    // Options/enum - render dropdown
    if (options && options.length > 0) {
      return (
        <Select size='small' fullWidth value={fieldValue ?? fieldSchema.default ?? ''} onChange={(e) => onFieldChange(e.target.value)} displayEmpty>
          <MenuItem value=''>
            <em>Select {fieldSchema.title || fieldName}</em>
          </MenuItem>
          {options.map((option) => (
            <MenuItem key={option} value={option}>
              {option}
            </MenuItem>
          ))}
        </Select>
      );
    }

    // Object type
    if (fieldSchema.type === 'object') {
      return <JsonEditor value={fieldValue ?? {}} onChange={onFieldChange} />;
    }

    // Number type
    if (fieldSchema.type === 'number' || fieldSchema.type === 'integer') {
      const numValue = fieldValue ?? fieldSchema.default ?? '';
      return (
        <Input
          size='sm'
          type='number'
          inputMode={fieldSchema.type === 'integer' ? 'numeric' : 'decimal'}
          value={numValue === '' ? '' : String(numValue)}
          onChange={(next) => onFieldChange(next === '' ? '' : Number(next))}
          placeholder={fieldSchema.default !== undefined ? `Default: ${fieldSchema.default}` : `Enter ${fieldName}`}
        />
      );
    }

    // Default: string field
    return (
      <Input
        size='sm'
        value={fieldValue ?? fieldSchema.default ?? ''}
        onChange={onFieldChange}
        placeholder={fieldSchema.default !== undefined ? `Default: ${fieldSchema.default}` : `Enter ${fieldName}`}
      />
    );
  };

  return (
    <Box>
      <Box
        sx={{
          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: 'var(--ds-radius-md)',
          p: 1,
          backgroundColor: 'var(--ds-background-200)',
        }}
      >
        {itemSchema ? (
          // Schema-based array items — drag-to-reorder via ReorderableList.
          <ReorderableList
            items={items as Record<string, any>[]}
            onReorder={handleReorderSchemaItems}
            getItemKey={(_, i) => i}
            getDragLabel={getSchemaItemDragLabel}
            renderItem={(item, index, dragHandleProps) => renderSchemaItem(item, index, dragHandleProps)}
          />
        ) : (
          // Simple string array items — same wrapper, lighter row layout.
          <ReorderableList
            items={simpleItems}
            onReorder={handleReorderSimpleItems}
            getItemKey={(_, i) => i}
            getDragLabel={getSimpleItemDragLabel}
            renderItem={(item, index, dragHandleProps) => (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
                <Box {...dragHandleProps} aria-label='Drag to reorder' sx={{ display: 'flex', alignItems: 'center' }}>
                  <DragIndicator sx={{ color: 'var(--ds-gray-400)', fontSize: 18 }} />
                </Box>
                <Box sx={{ flex: 1 }}>
                  <Input size='sm' value={item} onChange={(next) => updateSimpleItem(index, next)} placeholder={`Item ${index + 1}`} />
                </Box>
                <Button
                  composition='icon-only'
                  tone='ghost'
                  size='sm'
                  aria-label='Remove item'
                  icon={<Delete sx={{ color: 'var(--ds-red-500)' }} />}
                  onClick={() => removeItem(index)}
                />
              </Box>
            )}
          />
        )}
        <Box sx={{ mt: 1 }}>
          <Button tone='secondary' size='sm' icon={<Add />} onClick={addItem}>
            Add Item
          </Button>
        </Box>
      </Box>
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * ScriptEditor Component
 *
 * Renders a CodeMirror editor for shell/bash scripts with syntax highlighting
 * Perfect for script, command, and expression fields
 */
interface ScriptEditorProps {
  value: string;
  onChange: (value: string) => void;
  error?: string;
  disabled?: boolean;
  placeholder?: string;
  height?: string;
}

export const ScriptEditor: React.FC<ScriptEditorProps> = ({ value, onChange, error, disabled = false, placeholder = '', height = '200px' }) => {
  return (
    <Box>
      <CodeMirror
        value={value || ''}
        height={height}
        extensions={[StreamLanguage.define(shell)]}
        onChange={onChange}
        editable={!disabled}
        placeholder={placeholder}
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          dropCursor: false,
          allowMultipleSelections: false,
          indentOnInput: true,
          bracketMatching: true,
          closeBrackets: true,
          highlightActiveLine: true,
          highlightActiveLineGutter: true,
        }}
        style={{
          maxWidth: '500px',

          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: 'var(--ds-radius-md)',
          fontSize: 'var(--ds-text-body)',
          backgroundColor: disabled ? '#f5f5f5' : 'white',
          opacity: disabled ? 0.6 : 1,
        }}
      />
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * PasswordField Component
 *
 * Renders a password input with show/hide toggle for encrypted/sensitive fields
 * Use for fields marked with IsEncrypted: true in schema
 */
interface PasswordFieldProps {
  value: string;
  onChange: (value: string) => void;
  error?: string;
  disabled?: boolean;
  placeholder?: string;
  label?: string;
}

export const PasswordField: React.FC<PasswordFieldProps> = ({
  value,
  onChange,
  error,
  disabled = false,
  placeholder = 'Enter secret value',
  label,
}) => {
  const [showPassword, setShowPassword] = useState(false);

  return (
    <Box>
      <Input
        type={showPassword ? 'text' : 'password'}
        value={value ?? ''}
        onChange={onChange}
        disabled={disabled}
        placeholder={placeholder}
        error={error || undefined}
        size='sm'
        label={label}
        trailingIcon={
          <Button
            composition='icon-only'
            tone='ghost'
            size='sm'
            disabled={disabled}
            aria-label={showPassword ? 'Hide password' : 'Show password'}
            icon={showPassword ? <VisibilityOff fontSize='small' /> : <Visibility fontSize='small' />}
            onClick={() => setShowPassword(!showPassword)}
          />
        }
      />
    </Box>
  );
};

/**
 * DurationInput Component
 *
 * Renders a duration input with number and unit selector (s, m, h, d)
 * Outputs duration strings like "10s", "5m", "1h"
 */
interface DurationInputProps {
  value: string;
  onChange: (value: string) => void;
  error?: string;
  disabled?: boolean;
  placeholder?: string;
}

const DURATION_UNITS = [
  { label: 'Seconds', value: 's' },
  { label: 'Minutes', value: 'm' },
  { label: 'Hours', value: 'h' },
  { label: 'Days', value: 'd' },
];

export const DurationInput: React.FC<DurationInputProps> = ({ value, onChange, error, disabled = false, placeholder = '30' }) => {
  // Parse existing value (e.g., "30s" -> { number: 30, unit: 's' })
  const parseValue = (val: string): { number: string; unit: string } => {
    if (!val) {
      return { number: '', unit: 's' };
    }
    const match = /^(\d+)([smhd])?$/.exec(val);
    if (match) {
      return { number: match[1], unit: match[2] || 's' };
    }
    return { number: val, unit: 's' };
  };

  const [numberValue, setNumberValue] = useState('');
  const [unit, setUnit] = useState('s');

  // Sync internal state when value prop changes
  React.useEffect(() => {
    const parsed = parseValue(value);
    setNumberValue(parsed.number);
    setUnit(parsed.unit);
  }, [value]);

  const handleNumberChange = (newNumber: string) => {
    setNumberValue(newNumber);
    if (newNumber) {
      onChange(`${newNumber}${unit}`);
    } else {
      onChange('');
    }
  };

  const handleUnitChange = (newUnit: string) => {
    setUnit(newUnit);
    if (numberValue) {
      onChange(`${numberValue}${newUnit}`);
    }
  };

  return (
    <Box>
      <Box sx={{ display: 'flex', gap: 1 }}>
        <Box sx={{ width: '100px' }}>
          <Input
            type='number'
            inputMode='numeric'
            value={numberValue}
            onChange={handleNumberChange}
            disabled={disabled}
            placeholder={placeholder}
            error={error || undefined}
            size='sm'
          />
        </Box>
        <FormControl size='small' sx={{ minWidth: '100px' }}>
          <Select
            value={unit}
            onChange={(e) => handleUnitChange(e.target.value)}
            disabled={disabled}
            sx={{
              borderRadius: 'var(--ds-radius-md)',
              backgroundColor: 'white',
              fontSize: 'var(--ds-text-body-lg)',
              '& fieldset': {
                borderColor: 'var(--ds-brand-200)',
              },
            }}
          >
            {DURATION_UNITS.map((u) => (
              <MenuItem key={u.value} value={u.value}>
                {u.label}
              </MenuItem>
            ))}
          </Select>
        </FormControl>
      </Box>
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * KeyValueEditor Component
 *
 * Renders a key-value pair editor for objects like headers, env variables
 * Better UX than raw JSON for simple string maps
 * Uses internal array-based state with unique IDs to handle duplicate/empty keys during editing
 */
interface KeyValueEditorProps {
  value: Record<string, string>;
  onChange: (value: Record<string, string>) => void;
  error?: string;
  disabled?: boolean;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}

interface KeyValueEntry {
  id: string;
  key: string;
  value: string;
}

// Generate unique ID for entries
let entryIdCounter = 0;
const generateEntryId = () => `kv-entry-${++entryIdCounter}`;

export const KeyValueEditor: React.FC<KeyValueEditorProps> = ({
  value,
  onChange,
  error,
  disabled = false,
  keyPlaceholder = 'Key',
  valuePlaceholder = 'Value',
}) => {
  // Convert Record to array of entries with unique IDs for internal management
  const [entries, setEntries] = useState<KeyValueEntry[]>(() =>
    Object.entries(value || {}).map(([k, v]) => ({
      id: generateEntryId(),
      key: k,
      value: v,
    }))
  );

  // Sync internal state when value prop changes externally
  // Using functional update to avoid stale closure issues
  React.useEffect(() => {
    const externalEntries = Object.entries(value || {});

    setEntries((currentEntries) => {
      // Build record from current entries
      const currentRecord: Record<string, string> = {};
      for (const entry of currentEntries) {
        if (entry.key.trim() !== '') {
          currentRecord[entry.key] = entry.value;
        }
      }

      // Only update if the external value has changed structurally
      const isSame = Object.keys(currentRecord).length === externalEntries.length && externalEntries.every(([k, v]) => currentRecord[k] === v);

      if (isSame) {
        return currentEntries;
      }

      return externalEntries.map(([k, v]) => ({
        id: generateEntryId(),
        key: k,
        value: v,
      }));
    });
  }, [value]);

  // Convert entries array back to Record, handling duplicates by using last occurrence
  const entriesToRecord = (entryList: KeyValueEntry[]): Record<string, string> => {
    const result: Record<string, string> = {};
    for (const entry of entryList) {
      // Only include entries with non-empty keys
      if (entry.key.trim() !== '') {
        result[entry.key] = entry.value;
      }
    }
    return result;
  };

  const addEntry = () => {
    const newEntries = [...entries, { id: generateEntryId(), key: '', value: '' }];
    setEntries(newEntries);
    // Don't call onChange for empty entries - they'll be included when key is filled
  };

  const removeEntry = (id: string) => {
    const newEntries = entries.filter((e) => e.id !== id);
    setEntries(newEntries);
    onChange(entriesToRecord(newEntries));
  };

  const updateEntry = (id: string, newKey: string, newVal: string) => {
    const newEntries = entries.map((e) => (e.id === id ? { ...e, key: newKey, value: newVal } : e));
    setEntries(newEntries);
    onChange(entriesToRecord(newEntries));
  };

  // Check for duplicate keys (excluding empty keys)
  const getDuplicateKeys = (): Set<string> => {
    const keyCounts: Record<string, number> = {};
    for (const entry of entries) {
      if (entry.key.trim() !== '') {
        keyCounts[entry.key] = (keyCounts[entry.key] || 0) + 1;
      }
    }
    return new Set(Object.keys(keyCounts).filter((k) => keyCounts[k] > 1));
  };

  const duplicateKeys = getDuplicateKeys();

  return (
    <Box>
      <Box
        sx={{
          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: 'var(--ds-radius-md)',
          p: 1,
          backgroundColor: 'var(--ds-background-200)',
        }}
      >
        {entries.length === 0 && (
          <Typography variant='body2' sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-body)', mb: 1, textAlign: 'center' }}>
            No entries. Click "Add Entry" to add key-value pairs.
          </Typography>
        )}
        {entries.map((entry) => {
          const isDuplicate = duplicateKeys.has(entry.key);
          return (
            <Box key={entry.id} sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
              <Box sx={{ flex: 1 }}>
                <Input
                  size='sm'
                  value={entry.key}
                  onChange={(next) => updateEntry(entry.id, next, entry.value)}
                  placeholder={keyPlaceholder}
                  disabled={disabled}
                  error={isDuplicate ? 'Duplicate key' : undefined}
                />
              </Box>
              <Typography sx={{ color: 'var(--ds-gray-400)' }}>:</Typography>
              <Box sx={{ flex: 2 }}>
                <Input
                  size='sm'
                  value={entry.value}
                  onChange={(next) => updateEntry(entry.id, entry.key, next)}
                  placeholder={valuePlaceholder}
                  disabled={disabled}
                />
              </Box>
              <Button
                composition='icon-only'
                tone='ghost'
                size='sm'
                disabled={disabled}
                aria-label='Remove entry'
                icon={<Delete fontSize='small' sx={{ color: 'var(--ds-red-500)' }} />}
                onClick={() => removeEntry(entry.id)}
              />
            </Box>
          );
        })}
        {duplicateKeys.size > 0 && (
          <Typography variant='body2' sx={{ color: 'var(--ds-amber-400)', fontSize: 'var(--ds-text-caption)', mb: 1 }}>
            Warning: Duplicate keys detected. Only the last value for each key will be saved.
          </Typography>
        )}
        <Box sx={{ mt: 1 }}>
          <Button tone='secondary' size='sm' icon={<Add />} disabled={disabled} onClick={addEntry}>
            Add Entry
          </Button>
        </Box>
      </Box>
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * MultiSelectChips Component
 *
 * Renders a multi-select input with chips for array fields with predefined options
 */
interface MultiSelectChipsProps {
  value: string[];
  options: string[];
  onChange: (value: string[]) => void;
  error?: string;
  disabled?: boolean;
  placeholder?: string;
}

export const MultiSelectChips: React.FC<MultiSelectChipsProps> = ({
  value,
  options,
  onChange,
  error,
  disabled = false,
  placeholder = 'Select options',
}) => {
  const handleToggle = (option: string) => {
    if (value.includes(option)) {
      onChange(value.filter((v) => v !== option));
    } else {
      onChange([...value, option]);
    }
  };

  return (
    <Box>
      <Box
        sx={{
          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: 'var(--ds-radius-md)',
          p: 1,
          backgroundColor: 'var(--ds-background-200)',
          display: 'flex',
          flexWrap: 'wrap',
          gap: 0.5,
          minHeight: '42px',
        }}
      >
        {options.map((option) => (
          <Chip
            key={option}
            label={option}
            onClick={() => !disabled && handleToggle(option)}
            color={value.includes(option) ? 'primary' : 'default'}
            variant={value.includes(option) ? 'filled' : 'outlined'}
            size='small'
            disabled={disabled}
            sx={{
              cursor: disabled ? 'default' : 'pointer',
              '&:hover': {
                backgroundColor: value.includes(option) ? undefined : 'var(--ds-brand-150)',
              },
            }}
          />
        ))}
        {options.length === 0 && (
          <Typography variant='body2' sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-body)' }}>
            {placeholder}
          </Typography>
        )}
      </Box>
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * CodeEditorWithLanguage Component
 *
 * Renders a CodeMirror editor with syntax highlighting based on language/subType
 */
interface CodeEditorWithLanguageProps {
  value: string;
  onChange: (value: string) => void;
  language?: 'bash' | 'javascript' | 'sql' | 'json' | 'jsonata';
  error?: string;
  disabled?: boolean;
  placeholder?: string;
  height?: string;
}

export const CodeEditorWithLanguage: React.FC<CodeEditorWithLanguageProps> = ({
  value,
  onChange,
  language = 'bash',
  error,
  disabled = false,
  placeholder = '',
  height = '200px',
}) => {
  // Get the appropriate language extension
  const getLanguageExtension = () => {
    switch (language) {
      case 'javascript':
      case 'jsonata':
        return [javascript()];
      case 'sql':
        return [sql()];
      case 'json':
        return [json()];
      case 'bash':
      default:
        return [StreamLanguage.define(shell)];
    }
  };

  return (
    <Box>
      <CodeMirror
        value={value || ''}
        height={height}
        extensions={getLanguageExtension()}
        onChange={onChange}
        editable={!disabled}
        placeholder={placeholder}
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          dropCursor: false,
          allowMultipleSelections: false,
          indentOnInput: true,
          bracketMatching: true,
          closeBrackets: true,
          highlightActiveLine: true,
          highlightActiveLineGutter: true,
        }}
        style={{
          maxWidth: '500px',
          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: 'var(--ds-radius-md)',
          fontSize: 'var(--ds-text-body)',
          backgroundColor: disabled ? '#f5f5f5' : 'white',
          opacity: disabled ? 0.6 : 1,
        }}
      />
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * NestedSchemaEditor Component
 *
 * Renders a collapsible form for nested schema objects with support for all field types
 */
interface NestedSchemaProperty {
  type: string;
  sub_type?: string;
  description?: string;
  required?: boolean;
  required_when?: { field: string; value: string[] };
  visible_when?: { field: string; value: string[] };
  default?: any;
  options?: string[];
  enum?: string[];
  title?: string;
  schema?: {
    properties?: Record<string, NestedSchemaProperty>;
  };
}

interface NestedSchemaEditorProps {
  value: Record<string, any>;
  schema: Record<string, NestedSchemaProperty>;
  onChange: (value: Record<string, any>) => void;
  error?: string;
  disabled?: boolean;
  title?: string;
  cloudAccounts?: { label: string; value: string; cloud_provider?: string; account_type?: string }[];
  ticketConfigurations?: { label: string; value: string; tool?: string; projects?: { name?: string; key?: string }[]; icon?: any }[];
}

export const NestedSchemaEditor: React.FC<NestedSchemaEditorProps> = ({
  value,
  schema,
  onChange,
  error,
  disabled = false,
  title = 'Configuration',
  cloudAccounts,
  ticketConfigurations,
}) => {
  const handleFieldChange = (fieldName: string, fieldValue: any) => {
    onChange({ ...value, [fieldName]: fieldValue });
  };

  const configuredCount = Object.keys(value || {}).filter((k) => value[k] !== undefined && value[k] !== '').length;

  const renderField = (fieldName: string, fieldSchema: NestedSchemaProperty) => {
    const fieldValue = value?.[fieldName];
    const options = fieldSchema.options || fieldSchema.enum;

    // Boolean type - render Switch
    if (fieldSchema.type === 'boolean') {
      return (
        <FormControlLabel
          control={
            <Switch
              checked={fieldValue === true || fieldValue === 'true'}
              onChange={(e) => handleFieldChange(fieldName, e.target.checked)}
              disabled={disabled}
              size='small'
            />
          }
          label=''
          sx={{ ml: 0 }}
        />
      );
    }

    // Ticket integration dropdown — used by gitops_config.integration_id to
    // pick a configured ticket integration (filtered by sub_type when set,
    // e.g. sub_type='github' shows only github ticket configs).
    if (fieldSchema.type === 'ticket') {
      const all = ticketConfigurations || [];
      const opts = fieldSchema.sub_type ? all.filter((o: any) => (o.tool || '').toLowerCase() === fieldSchema.sub_type) : all;
      const label = fieldSchema.sub_type
        ? opts.length === 0
          ? `No ${fieldSchema.sub_type} integrations configured`
          : `Select ${fieldSchema.sub_type} config`
        : 'Select ticket config';
      return (
        <FilterDropdownButton
          id={fieldName}
          options={opts}
          value={fieldValue ?? ''}
          onSelect={(_e: any, opt: any) => handleFieldChange(fieldName, opt?.value ?? opt ?? '')}
          disabled={disabled}
          placeholder={label}
          searchPlaceholder={label}
          sx={{ width: '100%' }}
        />
      );
    }

    // GitHub repository — sourced from the sibling integration_id's
    // ticket-configuration projects (same data the Create Ticket popup uses).
    // Falls back to a text field when the user hasn't picked an integration
    // yet or the chosen integration exposes no projects.
    if (fieldSchema.sub_type === 'github_repository') {
      const selectedIntegrationId = (value?.integration_id ?? value?.integration_name ?? '') as string;
      const selectedConfig = (ticketConfigurations || []).find((c) => c.value === selectedIntegrationId);
      const projects = selectedConfig?.projects || [];
      if (selectedIntegrationId && projects.length > 0) {
        const repoOptions = projects.map((p) => ({ label: p.name || p.key || '', value: p.key || p.name || '' })).filter((o) => !!o.value);
        return (
          <FilterDropdownButton
            id={fieldName}
            options={repoOptions}
            value={fieldValue ?? ''}
            onSelect={(_e: any, opt: any) => handleFieldChange(fieldName, opt?.value ?? opt ?? '')}
            disabled={disabled}
            placeholder='Select GitHub repository'
            searchPlaceholder='Search repositories'
            sx={{ width: '100%' }}
          />
        );
      }
      return (
        <Input
          size='sm'
          value={fieldValue ?? ''}
          onChange={(next) => handleFieldChange(fieldName, next)}
          disabled={disabled}
          placeholder={selectedIntegrationId ? 'org/repo or https://github.com/org/repo' : 'Select a GitHub config first'}
        />
      );
    }

    // Account type - render cloud accounts dropdown with provider grouping
    if (fieldSchema.type === 'account' && cloudAccounts && cloudAccounts.length > 0) {
      return (
        <FormField
          fieldType='dropdown'
          options={cloudAccounts}
          groupByCloudProvider
          value={fieldValue ?? ''}
          onChange={(e: any) => handleFieldChange(fieldName, e.target.value)}
          placeholder='Select account'
          disabled={disabled}
          minWidth='100%'
          limitTags={0}
          onSelect={() => {}}
          customRender={null}
          rows={1}
          maxRows={1}
          minRows={1}
          maxLength={0}
        />
      );
    }

    // Options/enum - render dropdown
    if (options && options.length > 0) {
      return (
        <Select
          size='small'
          fullWidth
          value={fieldValue ?? fieldSchema.default ?? ''}
          onChange={(e) => handleFieldChange(fieldName, e.target.value)}
          disabled={disabled}
          displayEmpty
        >
          <MenuItem value=''>
            <em>Select {fieldSchema.title || fieldName}</em>
          </MenuItem>
          {options.map((option) => (
            <MenuItem key={option} value={option}>
              {option}
            </MenuItem>
          ))}
        </Select>
      );
    }

    // Timestamp type - render TimestampPicker
    if (fieldSchema.type === 'timestamp') {
      return (
        <TimestampPicker
          value={fieldValue ?? ''}
          onChange={(val) => handleFieldChange(fieldName, val)}
          disabled={disabled}
          label={fieldSchema.title || fieldName}
        />
      );
    }

    // Nested object with schema - recursive NestedSchemaEditor
    if (fieldSchema.type === 'object' && fieldSchema.schema?.properties) {
      return (
        <NestedSchemaEditor
          value={typeof fieldValue === 'object' && fieldValue !== null ? fieldValue : {}}
          schema={fieldSchema.schema.properties}
          onChange={(val) => handleFieldChange(fieldName, val)}
          disabled={disabled}
          cloudAccounts={cloudAccounts}
          ticketConfigurations={ticketConfigurations}
          title={fieldSchema.title || fieldName}
        />
      );
    }

    // Object without schema - render JsonEditor
    if (fieldSchema.type === 'object') {
      return <JsonEditor value={fieldValue ?? {}} onChange={(val) => handleFieldChange(fieldName, val)} />;
    }

    // Array type - render ArrayEditor
    if (fieldSchema.type === 'array') {
      return (
        <ArrayEditor
          value={Array.isArray(fieldValue) ? fieldValue : []}
          onChange={(val) => handleFieldChange(fieldName, val)}
          itemSchema={fieldSchema.schema?.properties}
        />
      );
    }

    // Number/integer type
    if (fieldSchema.type === 'number' || fieldSchema.type === 'integer') {
      const numValue = fieldValue ?? fieldSchema.default ?? '';
      return (
        <Input
          size='sm'
          type='number'
          inputMode={fieldSchema.type === 'integer' ? 'numeric' : 'decimal'}
          value={numValue === '' ? '' : String(numValue)}
          onChange={(next) => handleFieldChange(fieldName, next === '' ? '' : Number(next))}
          disabled={disabled}
          placeholder={fieldSchema.default !== undefined ? `Default: ${fieldSchema.default}` : `Enter ${fieldName}`}
        />
      );
    }

    // Default: string/text field
    return (
      <Input
        size='sm'
        value={fieldValue ?? fieldSchema.default ?? ''}
        onChange={(next) => handleFieldChange(fieldName, next)}
        disabled={disabled}
        placeholder={fieldSchema.default !== undefined ? `Default: ${fieldSchema.default}` : `Enter ${fieldName}`}
      />
    );
  };

  return (
    <Box>
      <CollapsableCard
        composition='header+meta+body'
        elevation='flat'
        defaultOpen={false}
        header={
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' }}>
            {title}
          </Typography>
        }
        meta={
          configuredCount > 0 ? (
            <Chip
              label={`${configuredCount} configured`}
              size='small'
              color='primary'
              variant='outlined'
              sx={{ height: '20px', fontSize: 'var(--ds-text-caption)' }}
            />
          ) : undefined
        }
        sx={error ? { border: '1px solid var(--ds-red-500)' } : undefined}
      >
        {Object.entries(schema).map(([fieldName, fieldSchema]) => {
          const matchesRule = (rule?: { field: string; value: string[] }) => {
            if (!rule) return null;
            const sibling = value?.[rule.field];
            const siblingStr = typeof sibling === 'boolean' ? String(sibling) : (sibling ?? '').toString();
            return rule.value.includes(siblingStr);
          };
          const visible = matchesRule(fieldSchema.visible_when);
          if (visible === false) return null;
          // sub_type 'github_repository' is sourced from the sibling
          // integration_id's projects — hide it until that's picked so
          // we don't show an unusable repository field with no options.
          if (fieldSchema.sub_type === 'github_repository' && !value?.integration_id) return null;
          const required = !!fieldSchema.required || matchesRule(fieldSchema.required_when) === true;
          return (
            <Box key={fieldName} sx={{ mb: 1.5 }}>
              <Typography
                sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)', mb: 0.5 }}
              >
                {fieldSchema.title || fieldName}
                {required && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              {fieldSchema.description && (
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', mb: 0.5 }}>{fieldSchema.description}</Typography>
              )}
              {renderField(fieldName, fieldSchema)}
            </Box>
          );
        })}
      </CollapsableCard>
      {error && (
        <Typography variant='body2' sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

/**
 * KeyValueHybridField
 *
 * Dual-mode editor for object-typed workflow fields (headers, env, labels, etc.).
 * - "Key/Value" mode renders the structured KeyValueEditor for literal maps.
 * - "Expression" mode renders a TemplateTextField so the whole field can be a
 *   template reference like `{{ workflow.secrets.mcp_headers }}`, which the
 *   backend resolves to a map at runtime (ProcessValue's simple-variable path).
 *
 * Mode auto-detects from the stored value shape and sticks once the user has
 * explicitly toggled, mirroring the select/expression pattern in HybridField.
 */
type KeyValueFieldMode = 'keyvalue' | 'expression';

interface KeyValuePreviousTask {
  id: string;
  name: string;
  type: string;
  outputSchema?: any;
}

interface KeyValueHybridFieldProps {
  value: Record<string, string> | string;
  onChange: (value: Record<string, string> | string) => void;
  error?: string;
  disabled?: boolean;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  previousTasks?: KeyValuePreviousTask[];
  workflowInputs?: Array<{ id: string; type: string; description?: string }>;
  workflowConfigs?: Array<{ key: string; value: string; type: string }>;
  expressionPlaceholder?: string;
}

const detectKeyValueMode = (value: KeyValueHybridFieldProps['value']): KeyValueFieldMode =>
  typeof value === 'string' && /\{\{|\{%/.test(value) ? 'expression' : 'keyvalue';

export const KeyValueHybridField: React.FC<KeyValueHybridFieldProps> = ({
  value,
  onChange,
  error,
  disabled = false,
  keyPlaceholder,
  valuePlaceholder,
  previousTasks = [],
  workflowInputs = [],
  workflowConfigs = [],
  expressionPlaceholder = '{{ workflow.inputs.my_value }}',
}) => {
  const [mode, setMode] = useState<KeyValueFieldMode>(() => detectKeyValueMode(value));
  const userToggledRef = useRef(false);

  useEffect(() => {
    if (userToggledRef.current) return;
    const detected = detectKeyValueMode(value);
    if (detected !== mode) {
      setMode(detected);
    }
  }, [value, mode]);

  const handleModeChange = (_event: React.MouseEvent<HTMLElement>, newMode: KeyValueFieldMode | null) => {
    if (!newMode || newMode === mode) return;
    userToggledRef.current = true;
    setMode(newMode);
    // Clear value when switching modes to avoid mixing shapes. Storing {} in keyvalue mode
    // matches the schema's object type; '' in expression mode lets the user type a fresh
    // template without a stale map lingering on the node config.
    if (newMode === 'keyvalue' && typeof value === 'string') {
      onChange({});
    } else if (newMode === 'expression' && typeof value === 'object') {
      onChange('');
    }
  };

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.75 }}>
        <ToggleButtonGroup value={mode} exclusive onChange={handleModeChange} size='small' disabled={disabled}>
          <ToggleButton
            value='keyvalue'
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
            <ViewList sx={{ fontSize: 14, mr: 0.5 }} />
            Key / Value
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

      {mode === 'keyvalue' ? (
        <KeyValueEditor
          value={typeof value === 'object' && value !== null ? value : {}}
          onChange={onChange as (v: Record<string, string>) => void}
          error={error}
          disabled={disabled}
          keyPlaceholder={keyPlaceholder}
          valuePlaceholder={valuePlaceholder}
        />
      ) : (
        <TemplateTextField
          value={typeof value === 'string' ? value : ''}
          onChange={onChange as (v: string) => void}
          placeholder={expressionPlaceholder}
          disabled={disabled}
          error={error}
          previousTasks={previousTasks}
          workflowInputs={workflowInputs}
          workflowConfigs={workflowConfigs}
        />
      )}
    </Box>
  );
};
