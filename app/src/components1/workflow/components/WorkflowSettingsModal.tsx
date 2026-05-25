import React, { useState, useEffect, useMemo } from 'react';
import { Box, IconButton, Typography, TextField, Tooltip, Collapse } from '@mui/material';
import { Close, Add, DragIndicator, Edit as EditIcon } from '@mui/icons-material';
import { DurationField } from './advanced-config';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { FormCard, FormField } from '@components1/common/NewReusabeFormComponents';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { colors } from 'src/utils/colors';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import type { WorkflowSettings, WorkflowInput } from '@components1/workflow/types';
import { parseDurationToSeconds } from '@components1/workflow/utils/taskUtils';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

interface WorkflowSettingsModalProps {
  open: boolean;
  onClose: () => void;
  onSave: (settings: WorkflowSettings) => void;
  initialSettings?: WorkflowSettings;
  taskTimeouts?: string[];
}

const defaultSettings: WorkflowSettings = {
  timeout: '5m',
  maxInterval: '1m',
  retries: 3,
  inputs: [],
  outputs: {},
  tags: [],
  status: 'DRAFT',
};

const defaultInput: WorkflowInput = {
  id: '',
  type: 'string',
  description: '',
  default: '',
  customType: '',
  validation: { isValid: true },
};

// Validation functions for different input types
const validateInputValue = (type: WorkflowInput['type'], value: any): { isValid: boolean; error?: string } => {
  if (value === '' || value === null || value === undefined) {
    return { isValid: true }; // Allow empty values
  }

  switch (type) {
    case 'string':
      return { isValid: typeof value === 'string' };

    case 'int': {
      const num = Number(value);
      if (isNaN(num) || !Number.isInteger(num)) {
        return { isValid: false, error: 'Must be a valid integer' };
      }
      return { isValid: true };
    }

    case 'bool':
      if (typeof value === 'boolean') {
        return { isValid: true };
      }
      if (value === 'true' || value === 'false') {
        return { isValid: true };
      }
      return { isValid: false, error: 'Must be true or false' };

    case 'json':
      try {
        if (typeof value === 'string') {
          JSON.parse(value);
        }
        return { isValid: true };
      } catch {
        return { isValid: false, error: 'Must be valid JSON' };
      }

    case 'array':
      try {
        let parsed = value;
        if (typeof value === 'string') {
          parsed = JSON.parse(value);
        }
        if (!Array.isArray(parsed)) {
          return { isValid: false, error: 'Must be a valid array' };
        }
        return { isValid: true };
      } catch {
        return { isValid: false, error: 'Must be a valid array' };
      }

    default:
      return { isValid: true };
  }
};

// JSON Editor Component
const JsonEditor: React.FC<{
  value: any;
  onChange: (value: any) => void;
  error?: string;
}> = ({ value, onChange, error }) => {
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
          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: '6px',
          fontSize: '13px',
        }}
      />
      {error && (
        <Typography variant='body2' sx={{ color: '#ef4444', fontSize: '12px', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

// Array Editor Component
const ArrayEditor: React.FC<{
  value: any[];
  onChange: (value: any[]) => void;
  error?: string;
}> = ({ value, onChange, error }) => {
  const [items, setItems] = useState<string[]>(() => {
    return Array.isArray(value) ? value.map((item) => (typeof item === 'string' ? item : JSON.stringify(item))) : [];
  });

  const addItem = () => {
    const newItems = [...items, ''];
    setItems(newItems);
    updateValue(newItems);
  };

  const removeItem = (index: number) => {
    const newItems = items.filter((_, i) => i !== index);
    setItems(newItems);
    updateValue(newItems);
  };

  const updateItem = (index: number, newValue: string) => {
    const newItems = [...items];
    newItems[index] = newValue;
    setItems(newItems);
    updateValue(newItems);
  };

  const updateValue = (newItems: string[]) => {
    const parsedItems = newItems.map((item) => {
      try {
        return JSON.parse(item);
      } catch {
        return item;
      }
    });
    onChange(parsedItems);
  };

  return (
    <Box>
      <Box
        sx={{
          border: error ? '1px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: '6px',
          p: 1,
          backgroundColor: '#f9fafb',
        }}
      >
        {items.map((item, index) => (
          <Box key={index} sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
            <DragIndicator sx={{ color: '#9ca3af', cursor: 'grab' }} />
            <TextField
              size='small'
              value={item}
              onChange={(e) => updateItem(index, e.target.value)}
              placeholder={`Item ${index + 1}`}
              sx={{ flex: 1 }}
            />
            <IconButton id={`wf-settings-array-remove-${index}-btn`} size='small' onClick={() => removeItem(index)}>
              <SafeIcon src={DeleteIconRed} alt='delete' width={20} height={20} />
            </IconButton>
          </Box>
        ))}
        <CustomButton
          id='wf-settings-array-add-btn'
          onClick={addItem}
          text='Add Item'
          variant='secondary'
          size='Small'
          startIcon={<Add />}
          sx={{ mt: 1 }}
        />
      </Box>
      {error && (
        <Typography variant='body2' sx={{ color: '#ef4444', fontSize: '12px', mt: 0.5 }}>
          {error}
        </Typography>
      )}
    </Box>
  );
};

const WorkflowSettingsModal: React.FC<WorkflowSettingsModalProps> = ({ open, onClose, onSave, initialSettings = defaultSettings, taskTimeouts }) => {
  const [settings, setSettings] = useState<WorkflowSettings>(initialSettings);
  const [newInput, setNewInput] = useState<WorkflowInput>({ ...defaultInput });
  const [outputsJson, setOutputsJson] = useState('');
  const [newTag, setNewTag] = useState('');
  const [showAddForm, setShowAddForm] = useState(false);
  const [editingInputId, setEditingInputId] = useState<string | null>(null);
  const [editingInput, setEditingInput] = useState<WorkflowInput | null>(null);

  const exceedingTaskCount = useMemo(() => {
    if (!taskTimeouts || !settings.timeout) return 0;
    const workflowSec = parseDurationToSeconds(settings.timeout);
    if (isNaN(workflowSec)) return 0;
    return taskTimeouts.filter((t) => {
      const taskSec = parseDurationToSeconds(t);
      return !isNaN(taskSec) && taskSec > workflowSec;
    }).length;
  }, [taskTimeouts, settings.timeout]);

  const taskTimeoutSumExceedsWorkflow = useMemo(() => {
    if (!taskTimeouts || !taskTimeouts.length || !settings.timeout) return false;
    const workflowSec = parseDurationToSeconds(settings.timeout);
    if (isNaN(workflowSec) || workflowSec <= 0) return false;
    const totalTaskSec = taskTimeouts.reduce((sum, t) => {
      const sec = parseDurationToSeconds(t);
      return sum + (isNaN(sec) ? 0 : sec);
    }, 0);
    return totalTaskSec > 0 && totalTaskSec > workflowSec;
  }, [taskTimeouts, settings.timeout]);

  const RemoveButton: React.FC<{ onClick: () => void; size?: 'small' | 'medium' | 'large'; backgroundColor?: string }> = ({
    onClick,
    size = 'small',
    backgroundColor = 'rgba(0, 0, 0, 0.1)',
  }) => (
    <IconButton
      size={size}
      onClick={onClick}
      sx={{
        position: 'absolute',
        top: '-8px',
        right: '-8px',
        width: '16px',
        height: '16px',
        backgroundColor: backgroundColor,
        border: '1px solid rgba(0, 0, 0, 0.08)',
        '&:hover': {
          backgroundColor: 'rgba(0, 0, 0, 0.15)',
          transform: 'scale(1.1)',
        },
        '&:active': {
          transform: 'scale(0.95)',
        },
        transition: 'all 0.15s ease-in-out',
        '& .MuiSvgIcon-root': {
          fontSize: '12px',
          color: colors.text.secondary,
        },
      }}
    >
      <Close />
    </IconButton>
  );

  useEffect(() => {
    if (initialSettings) {
      // Re-validate inputs on load since validation is not persisted
      const settingsWithValidation = {
        ...initialSettings,
        inputs: initialSettings.inputs.map((input) => ({
          ...input,
          validation: input.validation ?? validateInputValue(input.type, input.default),
        })),
      };
      setSettings(settingsWithValidation);
      setOutputsJson(JSON.stringify(initialSettings.outputs, null, 2));
    }
  }, [initialSettings]);

  const handleSettingChange = (field: keyof WorkflowSettings, value: string | number | string[] | WorkflowInput[] | Record<string, string>) => {
    setSettings((prev) => ({
      ...prev,
      [field]: value,
    }));
  };

  const handleAddInput = () => {
    if (newInput.id.trim() && !settings.inputs.some((input) => input.id === newInput.id.trim())) {
      const validation = validateInputValue(newInput.type, newInput.default);
      const inputToAdd: WorkflowInput = {
        ...newInput,
        id: newInput.id.trim(),
        description: newInput.description.trim() || `Input parameter: ${newInput.id.trim()}`,
        validation,
      };
      handleSettingChange('inputs', [...settings.inputs, inputToAdd]);
      setNewInput({ ...defaultInput });
    }
  };

  // Handle input parameter value changes with real-time validation
  const handleInputValueChange = (field: keyof WorkflowInput, value: any) => {
    const updated = { ...newInput, [field]: value };

    if (field === 'default' || field === 'type') {
      const validation = validateInputValue(updated.type, updated.default);
      updated.validation = validation;
    }

    setNewInput(updated);
  };

  const handleRemoveInput = (inputId: string) => {
    handleSettingChange(
      'inputs',
      settings.inputs.filter((input) => input.id !== inputId)
    );
    if (editingInputId === inputId) {
      setEditingInputId(null);
      setEditingInput(null);
    }
  };

  const handleStartEdit = (input: WorkflowInput) => {
    setEditingInputId(input.id);
    setEditingInput({ ...input });
    setShowAddForm(false);
  };

  const handleEditValueChange = (field: keyof WorkflowInput, value: any) => {
    if (!editingInput) return;
    const updated = { ...editingInput, [field]: value };
    if (field === 'default' || field === 'type') {
      updated.validation = validateInputValue(updated.type, updated.default);
    }
    setEditingInput(updated);
  };

  const handleSaveEdit = () => {
    if (!editingInput || !editingInputId) return;
    const updatedInputs = settings.inputs.map((input) =>
      input.id === editingInputId
        ? {
            ...editingInput,
            id: editingInput.id.trim(),
            description: editingInput.description.trim() || `Input parameter: ${editingInput.id.trim()}`,
          }
        : input
    );
    handleSettingChange('inputs', updatedInputs);
    setEditingInputId(null);
    setEditingInput(null);
  };

  const handleCancelEdit = () => {
    setEditingInputId(null);
    setEditingInput(null);
  };

  const handleToggleAddForm = () => {
    setShowAddForm((prev) => !prev);
    setEditingInputId(null);
    setEditingInput(null);
    if (!showAddForm) {
      setNewInput({ ...defaultInput });
    }
  };

  // Handle outputs JSON changes with validation
  const handleOutputsJsonChange = (value: string) => {
    setOutputsJson(value);

    // Handle empty string case - reset to empty object
    if (!value.trim()) {
      handleSettingChange('outputs', {});
      return;
    }

    try {
      const parsed = JSON.parse(value);
      if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) {
        handleSettingChange('outputs', parsed);
      }
    } catch {
      // Invalid JSON - don't update settings, let validation show error
    }
  };

  const handleAddTag = () => {
    if (newTag.trim() && !settings.tags.includes(newTag.trim())) {
      handleSettingChange('tags', [...settings.tags, newTag.trim()]);
      setNewTag('');
    }
  };

  const handleRemoveTag = (tag: string) => {
    handleSettingChange(
      'tags',
      settings.tags.filter((t) => t !== tag)
    );
  };

  const handleSave = () => {
    // Filter out validation and customType fields before sending to parent
    const cleanedSettings = {
      ...settings,
      inputs: settings.inputs.map((input) => {
        const { validation: _validation, customType: _customType, ...cleanInput } = input;
        return cleanInput;
      }),
      // outputs is already in the correct format (Record<string, string>)
    };
    onSave(cleanedSettings);
    onClose();
  };

  const handleCancel = () => {
    setSettings(initialSettings);
    setNewInput({ ...defaultInput });
    setOutputsJson(JSON.stringify(initialSettings.outputs, null, 2));
    setNewTag('');
    onClose();
  };

  const actionButtons = (
    <Box sx={{ display: 'flex', gap: 2, p: 2 }}>
      <CustomButton id='wf-settings-cancel-btn' onClick={handleCancel} text='Cancel' variant='secondary' size='Medium' />
      <CustomButton
        id='wf-settings-apply-btn'
        onClick={handleSave}
        text='Apply Settings'
        variant='primary'
        size='Medium'
        disabled={taskTimeoutSumExceedsWorkflow}
      />
    </Box>
  );

  return (
    <Modal
      open={open}
      onClose={handleCancel}
      title='Automation Settings'
      subtitle='Configure automation execution parameters'
      width='md'
      actionButtons={actionButtons}
    >
      <Box sx={{ p: 3, display: 'flex', flexDirection: 'column', gap: 3 }}>
        <Typography variant='body2' sx={{ fontSize: '12px', color: colors.text.secondary, fontStyle: 'italic' }}>
          Settings are applied to the current editor session. Save the automation to persist changes.
        </Typography>
        {/* Execution Settings */}
        <FormCard title='Execution Settings' description='Configure automation execution parameters' number={1} columns={2} icon={null}>
          <DurationField
            label='Automation Timeout'
            value={settings.timeout}
            onChange={(value) => handleSettingChange('timeout', value)}
            customHelperText='Maximum time for the entire automation, including all tasks'
            warningMessage={
              taskTimeoutSumExceedsWorkflow
                ? 'Sum of all task timeouts exceeds the automation timeout. Reduce task timeouts or increase the automation timeout.'
                : exceedingTaskCount > 0
                ? `${exceedingTaskCount} task(s) have timeouts exceeding the automation timeout. These tasks will be terminated when the automation times out.`
                : undefined
            }
          />

          <DurationField
            label='Max Retry Interval'
            value={settings.maxInterval}
            onChange={(value) => handleSettingChange('maxInterval', value)}
            customHelperText='Maximum time between retry attempts'
          />

          <FormField
            label='Retries'
            description='Number of times to retry automation execution on failure'
            value={settings.retries}
            onChange={(e: any) => handleSettingChange('retries', e.target.value as number)}
            placeholder='Select retry count'
            fieldType='dropdown'
            options={
              [0, 1, 2, 3, 4, 5].map((retry) => ({
                label: `${retry} ${retry === 1 ? 'retry' : 'retries'}`,
                value: retry,
              })) as any
            }
            minWidth='100%'
            maxRows={1}
            minRows={1}
            maxLength={0}
            limitTags={0}
            onSelect={() => {}}
            customRender={null}
          />
        </FormCard>

        {/* Workflow Parameters */}
        <FormCard
          title='Automation Parameters'
          description='Define input, output, and tag parameters for the automation'
          number={2}
          columns={1}
          icon={null}
        >
          {/* Inputs */}
          <FormField
            label='Input Parameters'
            description='Define input parameters that can be passed to the automation with default values'
            value=''
            onChange={() => {}}
            placeholder=''
            fieldType='custom'
            maxRows={1}
            minRows={1}
            maxLength={0}
            limitTags={0}
            minWidth=''
            onSelect={() => {}}
            customRender={
              <Box>
                {/* Existing Input Parameters */}
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, mb: 2 }}>
                  {settings.inputs.length === 0 && !showAddForm && (
                    <Typography variant='body2' sx={{ color: '#9ca3af', fontStyle: 'italic', textAlign: 'center', py: 2 }}>
                      No input parameters defined yet
                    </Typography>
                  )}
                  {settings.inputs.map((input) => (
                    <Box key={input.id}>
                      {/* Collapsed view */}
                      {editingInputId !== input.id && (
                        <Box
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            position: 'relative',
                            p: 1.5,
                            border: '1px solid #e5e7eb',
                            borderRadius: '6px',
                            backgroundColor: 'white',
                            cursor: 'pointer',
                            '&:hover': {
                              backgroundColor: '#f9fafb',
                              borderColor: '#d1d5db',
                            },
                          }}
                          onClick={() => handleStartEdit(input)}
                        >
                          <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', gap: 1.5, minWidth: 0 }}>
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, flexShrink: 0 }}>
                              <CustomLabels text={input.id} textTransform='none' variant='blue' />
                              <CustomLabels text={input.type} textTransform='none' variant='grey' />
                              {!input.validation?.isValid && <CustomLabels text='Invalid' textTransform='none' variant='red' />}
                            </Box>
                            <Typography
                              variant='body2'
                              component='span'
                              sx={{
                                color: '#6b7280',
                                fontSize: '12px',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                                whiteSpace: 'nowrap',
                                flexShrink: 1,
                                fontFamily: 'monospace',
                              }}
                            >
                              <Typography component='span' sx={{ color: '#9ca3af', fontSize: '12px' }}>
                                value:{' '}
                              </Typography>
                              {input.type === 'json' || input.type === 'array'
                                ? (() => {
                                    const s = JSON.stringify(input.default ?? null);
                                    return s.length > 40 ? s.substring(0, 40) + '...' : s;
                                  })()
                                : String(input.default ?? '—')}
                            </Typography>
                            {input.description && !input.description.startsWith('Input parameter:') && (
                              <Typography
                                variant='body2'
                                sx={{
                                  color: '#9ca3af',
                                  fontSize: '12px',
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                  whiteSpace: 'nowrap',
                                  flex: 1,
                                  minWidth: 0,
                                }}
                              >
                                <Typography component='span' sx={{ color: '#b0b5bc', fontSize: '12px' }}>
                                  description:{' '}
                                </Typography>
                                {input.description}
                              </Typography>
                            )}
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexShrink: 0 }}>
                              <Tooltip title='Edit parameter'>
                                <IconButton
                                  size='small'
                                  sx={{ color: '#9ca3af', '&:hover': { color: colors.text.secondary } }}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    handleStartEdit(input);
                                  }}
                                >
                                  <EditIcon sx={{ fontSize: '16px' }} />
                                </IconButton>
                              </Tooltip>
                              <Tooltip title='Remove parameter'>
                                <IconButton
                                  size='small'
                                  sx={{ color: '#9ca3af', '&:hover': { color: '#ef4444' } }}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    handleRemoveInput(input.id);
                                  }}
                                >
                                  <Close sx={{ fontSize: '16px' }} />
                                </IconButton>
                              </Tooltip>
                            </Box>
                          </Box>
                        </Box>
                      )}

                      {/* Expanded edit view */}
                      {editingInputId === input.id && editingInput && (
                        <Box sx={{ border: '1px solid #3b82f6', borderRadius: '8px', p: 2, backgroundColor: '#f8faff' }}>
                          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
                            <Typography variant='body2' sx={{ color: '#374151', fontWeight: 600, fontSize: '13px' }}>
                              Editing: {input.id}
                            </Typography>
                            <Box sx={{ display: 'flex', gap: 1 }}>
                              <CustomButton onClick={handleCancelEdit} text='Cancel' variant='secondary' size='Small' />
                              <CustomButton
                                onClick={handleSaveEdit}
                                text='Save'
                                variant='primary'
                                size='Small'
                                disabled={!editingInput.validation?.isValid}
                              />
                            </Box>
                          </Box>
                          <Box sx={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 2, mb: 2, alignItems: 'start' }}>
                            <FormField
                              value={editingInput.id}
                              onChange={(e: any) => handleEditValueChange('id', e.target.value)}
                              placeholder='Parameter ID (e.g. slack_channel)'
                              fieldType='textfield'
                              label='Parameter ID'
                              description=''
                              maxRows={1}
                              minRows={1}
                              maxLength={0}
                              limitTags={0}
                              minWidth=''
                              onSelect={() => {}}
                              customRender={null}
                              disabled
                            />
                            <FormField
                              value={editingInput.type}
                              onChange={(e: any) => {
                                handleEditValueChange('type', e.target.value);
                                // Reset default value when type changes
                                const newType = e.target.value;
                                if (newType === 'bool') handleEditValueChange('default', false);
                                else if (newType === 'int') handleEditValueChange('default', 0);
                                else if (newType === 'json') handleEditValueChange('default', null);
                                else if (newType === 'array') handleEditValueChange('default', []);
                                else handleEditValueChange('default', '');
                              }}
                              placeholder='Type'
                              fieldType='dropdown'
                              label='Type'
                              description=''
                              options={
                                [
                                  { label: 'String', value: 'string' },
                                  { label: 'Integer', value: 'int' },
                                  { label: 'Boolean', value: 'bool' },
                                  { label: 'JSON', value: 'json' },
                                  { label: 'Array', value: 'array' },
                                ] as any
                              }
                              maxRows={1}
                              minRows={1}
                              maxLength={0}
                              limitTags={0}
                              minWidth='100%'
                              onSelect={() => {}}
                              customRender={null}
                              inputVariant='outlined'
                              customStyle={{
                                margin: '0px',
                                marginTop: '0px',
                                marginBottom: '0px',
                                '& .MuiFormControl-root': { margin: '0px', marginTop: '0px', marginBottom: '0px' },
                                '& .MuiTextField-root': { margin: '0px', marginTop: '2px', marginBottom: '0px' },
                                '& .MuiOutlinedInput-root': { minHeight: '56px' },
                              }}
                              componentsProps={{
                                textField: { margin: 'none', sx: { margin: '0px', marginTop: '0px', marginBottom: '0px' } },
                              }}
                            />
                          </Box>
                          <FormField
                            value={editingInput.description}
                            onChange={(e: any) => handleEditValueChange('description', e.target.value)}
                            placeholder='Description of this parameter'
                            fieldType='textfield'
                            label='Description'
                            description=''
                            maxRows={1}
                            minRows={1}
                            maxLength={0}
                            limitTags={0}
                            minWidth=''
                            onSelect={() => {}}
                            customRender={null}
                          />
                          <Box sx={{ mt: 2 }}>
                            <Typography variant='body2' sx={{ color: '#374151', fontWeight: 500, mb: 1 }}>
                              Default Value
                            </Typography>
                            {editingInput.type === 'json' && (
                              <JsonEditor
                                value={editingInput.default}
                                onChange={(value) => handleEditValueChange('default', value)}
                                error={editingInput.validation?.error}
                              />
                            )}
                            {editingInput.type === 'array' && (
                              <ArrayEditor
                                value={Array.isArray(editingInput.default) ? editingInput.default : []}
                                onChange={(value) => handleEditValueChange('default', value)}
                                error={editingInput.validation?.error}
                              />
                            )}
                            {editingInput.type === 'bool' && (
                              <FormField
                                value={String(editingInput.default)}
                                onChange={(e: any) => handleEditValueChange('default', e.target.value === 'true')}
                                placeholder='Select boolean value'
                                fieldType='dropdown'
                                label=''
                                description=''
                                options={
                                  [
                                    { label: 'True', value: 'true' },
                                    { label: 'False', value: 'false' },
                                  ] as any
                                }
                                maxRows={1}
                                minRows={1}
                                maxLength={0}
                                limitTags={0}
                                minWidth='100%'
                                onSelect={() => {}}
                                customRender={null}
                              />
                            )}
                            {editingInput.type === 'int' && (
                              <Box>
                                <FormField
                                  value={String(editingInput.default)}
                                  onChange={(e: any) => {
                                    const value = e.target.value;
                                    handleEditValueChange('default', value ? parseInt(value) : 0);
                                  }}
                                  placeholder='Enter integer value'
                                  fieldType='textfield'
                                  type='number'
                                  label=''
                                  description=''
                                  maxRows={1}
                                  minRows={1}
                                  maxLength={0}
                                  limitTags={0}
                                  minWidth=''
                                  onSelect={() => {}}
                                  customRender={null}
                                />
                                {editingInput.validation?.error && (
                                  <Typography variant='body2' sx={{ color: '#ef4444', fontSize: '12px', mt: 0.5 }}>
                                    {editingInput.validation.error}
                                  </Typography>
                                )}
                              </Box>
                            )}
                            {editingInput.type === 'string' && (
                              <FormField
                                value={String(editingInput.default)}
                                onChange={(e: any) => handleEditValueChange('default', e.target.value)}
                                placeholder='Enter string value'
                                fieldType='textfield'
                                label=''
                                description=''
                                maxRows={1}
                                minRows={1}
                                maxLength={0}
                                limitTags={0}
                                minWidth=''
                                onSelect={() => {}}
                                customRender={null}
                              />
                            )}
                          </Box>
                        </Box>
                      )}
                    </Box>
                  ))}
                </Box>

                {/* Collapsible Add Parameter Form */}
                <Collapse in={showAddForm}>
                  <Box sx={{ border: '1px solid #e5e7eb', borderRadius: '8px', p: 2, mb: 2, backgroundColor: '#f9fafb' }}>
                    <Box sx={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 2, mb: 2, alignItems: 'start' }}>
                      <FormField
                        value={newInput.id}
                        onChange={(e: any) => handleInputValueChange('id', e.target.value)}
                        placeholder='Parameter ID (e.g. slack_channel)'
                        fieldType='textfield'
                        label='Parameter ID'
                        description=''
                        maxRows={1}
                        minRows={1}
                        maxLength={0}
                        limitTags={0}
                        minWidth=''
                        onSelect={() => {}}
                        customRender={null}
                      />
                      <FormField
                        value={newInput.type}
                        onChange={(e: any) => handleInputValueChange('type', e.target.value)}
                        placeholder='Type'
                        fieldType='dropdown'
                        label='Type'
                        description=''
                        options={
                          [
                            { label: 'String', value: 'string' },
                            { label: 'Integer', value: 'int' },
                            { label: 'Boolean', value: 'bool' },
                            { label: 'JSON', value: 'json' },
                            { label: 'Array', value: 'array' },
                          ] as any
                        }
                        maxRows={1}
                        minRows={1}
                        maxLength={0}
                        limitTags={0}
                        minWidth='100%'
                        onSelect={() => {}}
                        customRender={null}
                        inputVariant='outlined'
                        customStyle={{
                          margin: '0px',
                          marginTop: '0px',
                          marginBottom: '0px',
                          '& .MuiFormControl-root': { margin: '0px', marginTop: '0px', marginBottom: '0px' },
                          '& .MuiTextField-root': { margin: '0px', marginTop: '2px', marginBottom: '0px' },
                          '& .MuiOutlinedInput-root': { minHeight: '56px' },
                        }}
                        componentsProps={{
                          textField: { margin: 'none', sx: { margin: '0px', marginTop: '0px', marginBottom: '0px' } },
                        }}
                      />
                    </Box>
                    <FormField
                      value={newInput.description}
                      onChange={(e: any) => handleInputValueChange('description', e.target.value)}
                      placeholder='Description of this parameter'
                      fieldType='textfield'
                      label='Description'
                      description=''
                      maxRows={1}
                      minRows={1}
                      maxLength={0}
                      limitTags={0}
                      minWidth=''
                      onSelect={() => {}}
                      customRender={null}
                    />
                    <Box sx={{ mt: 2 }}>
                      <Typography variant='body2' sx={{ color: '#374151', fontWeight: 500, mb: 1 }}>
                        Default Value
                      </Typography>
                      {newInput.type === 'json' && (
                        <JsonEditor
                          value={newInput.default}
                          onChange={(value) => handleInputValueChange('default', value)}
                          error={newInput.validation?.error}
                        />
                      )}
                      {newInput.type === 'array' && (
                        <ArrayEditor
                          value={Array.isArray(newInput.default) ? newInput.default : []}
                          onChange={(value) => handleInputValueChange('default', value)}
                          error={newInput.validation?.error}
                        />
                      )}
                      {newInput.type === 'bool' && (
                        <FormField
                          value={String(newInput.default)}
                          onChange={(e: any) => handleInputValueChange('default', e.target.value === 'true')}
                          placeholder='Select boolean value'
                          fieldType='dropdown'
                          label=''
                          description=''
                          options={
                            [
                              { label: 'True', value: 'true' },
                              { label: 'False', value: 'false' },
                            ] as any
                          }
                          maxRows={1}
                          minRows={1}
                          maxLength={0}
                          limitTags={0}
                          minWidth='100%'
                          onSelect={() => {}}
                          customRender={null}
                        />
                      )}
                      {newInput.type === 'int' && (
                        <Box>
                          <FormField
                            value={String(newInput.default)}
                            onChange={(e: any) => {
                              const value = e.target.value;
                              handleInputValueChange('default', value ? parseInt(value) : 0);
                            }}
                            placeholder='Enter integer value'
                            fieldType='textfield'
                            type='number'
                            label=''
                            description=''
                            maxRows={1}
                            minRows={1}
                            maxLength={0}
                            limitTags={0}
                            minWidth=''
                            onSelect={() => {}}
                            customRender={null}
                          />
                          {newInput.validation?.error && (
                            <Typography variant='body2' sx={{ color: '#ef4444', fontSize: '12px', mt: 0.5 }}>
                              {newInput.validation.error}
                            </Typography>
                          )}
                        </Box>
                      )}
                      {newInput.type === 'string' && (
                        <FormField
                          value={String(newInput.default)}
                          onChange={(e: any) => handleInputValueChange('default', e.target.value)}
                          placeholder='Enter string value'
                          fieldType='textfield'
                          label=''
                          description=''
                          maxRows={1}
                          minRows={1}
                          maxLength={0}
                          limitTags={0}
                          minWidth=''
                          onSelect={() => {}}
                          customRender={null}
                        />
                      )}
                    </Box>
                    <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, mt: 2 }}>
                      <CustomButton
                        onClick={() => {
                          setShowAddForm(false);
                          setNewInput({ ...defaultInput });
                        }}
                        text='Cancel'
                        variant='secondary'
                        size='Small'
                      />
                      <CustomButton
                        onClick={() => {
                          handleAddInput();
                          setShowAddForm(false);
                        }}
                        text='Add Parameter'
                        variant='primary'
                        size='Small'
                        disabled={
                          !newInput.id.trim() || settings.inputs.some((input) => input.id === newInput.id.trim()) || !newInput.validation?.isValid
                        }
                      />
                    </Box>
                  </Box>
                </Collapse>

                {/* Add Parameter Toggle Button */}
                {!showAddForm && (
                  <CustomButton onClick={handleToggleAddForm} text='Add Parameter' variant='secondary' size='Small' startIcon={<Add />} />
                )}
              </Box>
            }
          />

          {/* Outputs */}
          <FormField
            label='Output Parameters'
            description='Define output values as JSON object with key-value pairs (template expressions)'
            value=''
            onChange={() => {}}
            placeholder=''
            fieldType='custom'
            maxRows={1}
            minRows={1}
            maxLength={0}
            limitTags={0}
            minWidth=''
            onSelect={() => {}}
            customRender={
              <Box>
                <JsonEditor
                  value={outputsJson}
                  onChange={handleOutputsJsonChange}
                  error={(() => {
                    // Allow empty string (no error)
                    if (!outputsJson.trim()) {
                      return undefined;
                    }

                    try {
                      const parsed = JSON.parse(outputsJson);
                      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
                        return 'Must be a valid JSON object (not array or primitive)';
                      }
                      return undefined;
                    } catch {
                      return 'Invalid JSON syntax';
                    }
                  })()}
                />
                <Typography variant='body2' sx={{ color: '#6b7280', fontSize: '11px', mt: 1 }}>
                  Example: {`{"final_message": "Processed {{ Task.output.count }} items", "status": "{{ Task.output.result }}"}`}
                </Typography>
              </Box>
            }
          />

          {/* Tags */}
          <FormField
            label='Tags'
            description='Add tags to categorize and organize automations'
            value=''
            onChange={() => {}}
            placeholder=''
            fieldType='custom'
            maxRows={1}
            minRows={1}
            maxLength={0}
            limitTags={0}
            minWidth=''
            onSelect={() => {}}
            customRender={
              <Box>
                <Box sx={{ display: 'flex', gap: 1, mb: 1 }}>
                  <FormField
                    value={newTag}
                    onChange={(e: any) => setNewTag(e.target.value)}
                    placeholder='Add tag'
                    fieldType='textfield'
                    sx={{ flex: 1 }}
                    label=''
                    description=''
                    maxRows={1}
                    minRows={1}
                    maxLength={0}
                    limitTags={0}
                    minWidth=''
                    onSelect={() => {}}
                    customRender={null}
                  />
                  <CustomButton
                    onClick={handleAddTag}
                    text='Add'
                    variant='secondary'
                    size='Small'
                    disabled={!newTag.trim() || settings.tags.includes(newTag.trim())}
                  />
                </Box>
                <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, mt: 1 }}>
                  {settings.tags
                    .filter((tag) => !tag.startsWith('ai_session_id:'))
                    .map((tag) => (
                      <Box key={tag} sx={{ display: 'flex', alignItems: 'center', position: 'relative' }}>
                        <CustomLabels text={tag} textTransform='none' variant='grey' />
                        <RemoveButton onClick={() => handleRemoveTag(tag)} backgroundColor='rgba(255, 255, 255, 0.8)' />
                      </Box>
                    ))}
                </Box>
              </Box>
            }
          />
        </FormCard>
      </Box>
    </Modal>
  );
};

export default WorkflowSettingsModal;
