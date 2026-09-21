import React, { useState, useRef, useCallback, useEffect, useMemo } from 'react';
import { Box, TextField, Popper, Paper, List, ListItem, ListItemText, Typography, ClickAwayListener, InputAdornment } from '@mui/material';
import { Button } from '@ui/Button';
import DataObjectIcon from '@mui/icons-material/DataObject';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';

interface PreviousTask {
  id: string;
  name: string;
  type: string;
  outputSchema?: any;
}

export type TemplateSuggestionCategory = 'task' | 'field' | 'input' | 'config' | 'secret' | 'builtin' | 'workflow' | 'loop';

export interface TemplateSuggestion {
  text: string;
  description: string;
  type: TemplateSuggestionCategory;
  insertText: string;
}

interface TemplateTextFieldProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  label?: string;
  disabled?: boolean;
  multiline?: boolean;
  rows?: number;
  maxRows?: number;
  previousTasks?: PreviousTask[];
  workflowInputs?: Array<{ id: string; type: string; description?: string }>;
  workflowConfigs?: Array<{ key: string; value: string; type: string }>;
  builtinVariables?: TemplateSuggestion[];
  error?: string;
  required?: boolean;
  fullWidth?: boolean;
  validationStatus?: 'valid' | 'invalid';
}

const CATEGORY_LABELS: Record<TemplateSuggestionCategory, string> = {
  loop: 'Loop Item',
  builtin: 'Date & Time',
  workflow: 'Workflow',
  task: 'Previous Steps',
  field: 'Previous Steps',
  input: 'Inputs',
  config: 'Configs',
  secret: 'Secrets',
};

const CATEGORY_ORDER: TemplateSuggestionCategory[] = ['loop', 'builtin', 'workflow', 'task', 'field', 'input', 'config', 'secret'];

// Template syntax patterns - matches Tasks, Inputs, Configs, Secrets
const PARTIAL_TEMPLATE_PATTERN =
  /\{\{\s*((?:Tasks(?:\[['"][^'"]*['"]\])?(?:\.output(?:\.[^}\s]*)?)?)|(?:Inputs(?:\.[^}\s]*)?)|(?:Configs(?:\.[^}\s]*)?)|(?:Secrets(?:\.[^}\s]*)?))?$/;

const TemplateTextField: React.FC<TemplateTextFieldProps> = ({
  value,
  onChange,
  placeholder,
  label,
  disabled = false,
  multiline = false,
  rows = 1,
  maxRows = 4,
  previousTasks = [],
  workflowInputs = [],
  workflowConfigs = [],
  builtinVariables = [],
  error,
  required = false,
  fullWidth = true,
  validationStatus,
}) => {
  // Local state to prevent focus loss during parent re-renders
  const [localValue, setLocalValue] = useState(value);
  const isUserTypingRef = useRef(false);

  const [showSuggestions, setShowSuggestions] = useState(false);
  const [suggestions, setSuggestions] = useState<TemplateSuggestion[]>([]);
  const [cursorPosition, setCursorPosition] = useState(0);
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null);
  // True when popper was opened by the +Variable button (no `{{` partial to replace)
  const [isPickerMode, setIsPickerMode] = useState(false);
  const textFieldRef = useRef<HTMLInputElement | HTMLTextAreaElement>(null);
  const pickerButtonRef = useRef<HTMLElement | null>(null);

  // Full catalog used by the +Variable button (no partial filtering)
  const allSuggestions: TemplateSuggestion[] = useMemo(() => {
    const list: TemplateSuggestion[] = [];

    builtinVariables.forEach((v) => list.push(v));

    previousTasks.forEach((task) => {
      list.push({
        text: `Tasks['${task.id}'].output`,
        description: `Output from ${task.name || task.type}`,
        type: 'task',
        insertText: `{{ Tasks['${task.id}'].output }}`,
      });
      if (task.outputSchema) {
        Object.keys(task.outputSchema).forEach((fieldName) => {
          list.push({
            text: `Tasks['${task.id}'].output.${fieldName}`,
            description: task.outputSchema[fieldName]?.description || `Field from ${task.name || task.type}`,
            type: 'field',
            insertText: `{{ Tasks['${task.id}'].output.${fieldName} }}`,
          });
        });
      }
    });

    workflowInputs.forEach((input) => {
      list.push({
        text: `Inputs.${input.id}`,
        description: input.description || `Workflow input (${input.type})`,
        type: 'input',
        insertText: `{{ Inputs.${input.id} }}`,
      });
    });

    workflowConfigs.forEach((config) => {
      const isSecret = config.type === 'secret';
      list.push({
        text: isSecret ? `Secrets.${config.key}` : `Configs.${config.key}`,
        description: isSecret ? 'Encrypted secret' : 'Config value',
        type: isSecret ? 'secret' : 'config',
        insertText: isSecret ? `{{ Secrets.${config.key} }}` : `{{ Configs.${config.key} }}`,
      });
    });

    return list;
  }, [builtinVariables, previousTasks, workflowInputs, workflowConfigs]);

  // Group suggestions by category for the picker popper rendering
  const groupedSuggestions = useMemo(() => {
    const groups = new Map<TemplateSuggestionCategory, TemplateSuggestion[]>();
    suggestions.forEach((s) => {
      const existing = groups.get(s.type) || [];
      existing.push(s);
      groups.set(s.type, existing);
    });
    return CATEGORY_ORDER.filter((c) => groups.has(c)).map((c) => ({
      category: c,
      label: CATEGORY_LABELS[c],
      items: groups.get(c) || [],
    }));
  }, [suggestions]);

  // Sync from parent when value changes externally (not from user input)
  useEffect(() => {
    if (!isUserTypingRef.current) {
      setLocalValue(value);
    }
  }, [value]);

  // Generate suggestions based on current input
  const generateSuggestions = useCallback(
    (inputValue: string, position: number) => {
      const textBeforeCursor = inputValue.slice(0, position);
      const partialMatch = PARTIAL_TEMPLATE_PATTERN.exec(textBeforeCursor);

      if (!partialMatch) {
        return [];
      }

      const [, partial] = partialMatch;
      const newSuggestions: TemplateSuggestion[] = [];

      // If user just typed "{{", suggest all available sources
      if (!partial) {
        // Built-in dynamic variables (date, time, workflow.*, etc.)
        builtinVariables.forEach((v) => newSuggestions.push(v));
        // Task outputs
        previousTasks.forEach((task) => {
          newSuggestions.push({
            text: `Tasks['${task.id}'].output`,
            description: `Output from ${task.name || task.type}`,
            type: 'task',
            insertText: `{{ Tasks['${task.id}'].output }}`,
          });
        });
        // Workflow inputs
        workflowInputs.forEach((input) => {
          newSuggestions.push({
            text: `Inputs.${input.id}`,
            description: input.description || `Workflow input (${input.type})`,
            type: 'input',
            insertText: `{{ Inputs.${input.id} }}`,
          });
        });
        // Configs and secrets
        workflowConfigs.forEach((config) => {
          const isSecret = config.type === 'secret';
          newSuggestions.push({
            text: isSecret ? `Secrets.${config.key}` : `Configs.${config.key}`,
            description: isSecret ? 'Encrypted secret' : `Config value`,
            type: isSecret ? 'secret' : 'config',
            insertText: isSecret ? `{{ Secrets.${config.key} }}` : `{{ Configs.${config.key} }}`,
          });
        });
        return newSuggestions;
      }

      // Tasks suggestions
      if (partial === 'Tasks') {
        previousTasks.forEach((task) => {
          newSuggestions.push({
            text: `Tasks['${task.id}'].output`,
            description: `Output from ${task.name || task.type}`,
            type: 'task',
            insertText: `{{ Tasks['${task.id}'].output }}`,
          });
        });
      } else if (partial.includes('Tasks[') && partial.includes('].output')) {
        const taskIdMatch = /Tasks\[['"]([^'"]*)['"]\]\.output(?:\.([^.]*))?/.exec(partial);
        if (taskIdMatch) {
          const [, taskId, fieldPrefix] = taskIdMatch;
          const task = previousTasks.find((t) => t.id === taskId);

          if (task?.outputSchema) {
            Object.keys(task.outputSchema).forEach((fieldName) => {
              if (!fieldPrefix || fieldName.startsWith(fieldPrefix)) {
                newSuggestions.push({
                  text: `Tasks['${taskId}'].output.${fieldName}`,
                  description: task.outputSchema[fieldName]?.description || `Field from ${task.name || task.type}`,
                  type: 'field',
                  insertText: `{{ Tasks['${taskId}'].output.${fieldName} }}`,
                });
              }
            });
          }
        }
      }

      // Inputs suggestions
      if (partial.startsWith('Inputs')) {
        const inputPrefix = partial.replace('Inputs.', '').replace('Inputs', '');
        workflowInputs.forEach((input) => {
          if (!inputPrefix || input.id.startsWith(inputPrefix)) {
            newSuggestions.push({
              text: `Inputs.${input.id}`,
              description: input.description || `Workflow input (${input.type})`,
              type: 'input',
              insertText: `{{ Inputs.${input.id} }}`,
            });
          }
        });
      }

      // Configs suggestions
      if (partial.startsWith('Configs')) {
        const configPrefix = partial.replace('Configs.', '').replace('Configs', '');
        workflowConfigs
          .filter((c) => c.type !== 'secret')
          .forEach((config) => {
            if (!configPrefix || config.key.startsWith(configPrefix)) {
              newSuggestions.push({
                text: `Configs.${config.key}`,
                description: 'Config value',
                type: 'config',
                insertText: `{{ Configs.${config.key} }}`,
              });
            }
          });
      }

      // Secrets suggestions
      if (partial.startsWith('Secrets')) {
        const secretPrefix = partial.replace('Secrets.', '').replace('Secrets', '');
        workflowConfigs
          .filter((c) => c.type === 'secret')
          .forEach((config) => {
            if (!secretPrefix || config.key.startsWith(secretPrefix)) {
              newSuggestions.push({
                text: `Secrets.${config.key}`,
                description: 'Encrypted secret',
                type: 'secret',
                insertText: `{{ Secrets.${config.key} }}`,
              });
            }
          });
      }

      return newSuggestions;
    },
    [builtinVariables, previousTasks, workflowInputs, workflowConfigs]
  );

  // Handle input changes
  const handleChange = useCallback(
    (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      const newValue = event.target.value;
      const position = event.target.selectionStart || 0;

      // Mark as user typing to prevent sync loop
      isUserTypingRef.current = true;
      setLocalValue(newValue);
      onChange(newValue);
      setCursorPosition(position);

      // Reset typing flag after a short delay
      setTimeout(() => {
        isUserTypingRef.current = false;
      }, 100);

      // Generate suggestions
      const newSuggestions = generateSuggestions(newValue, position);
      setSuggestions(newSuggestions);
      setShowSuggestions(newSuggestions.length > 0);

      if (newSuggestions.length > 0) {
        setAnchorEl(event.target);
      } else {
        setShowSuggestions(false);
      }
    },
    [onChange, generateSuggestions]
  );

  // Handle key events
  const handleKeyDown = useCallback((event: React.KeyboardEvent) => {
    if (event.key === 'Escape') {
      setShowSuggestions(false);
    }
  }, []);

  // Handle suggestion selection. Two modes:
  //   - Inline: triggered by `{{` typing, replaces the partial match.
  //   - Picker: triggered by the +Variable button, inserts at cursor.
  const handleSuggestionClick = useCallback(
    (suggestion: TemplateSuggestion) => {
      const textBeforeCursor = localValue.slice(0, cursorPosition);
      const textAfterCursor = localValue.slice(cursorPosition);
      const partialMatch = !isPickerMode ? PARTIAL_TEMPLATE_PATTERN.exec(textBeforeCursor) : null;

      let newValue: string;
      let newCursorPos: number;

      if (partialMatch) {
        const matchStart = partialMatch.index!;
        newValue = textBeforeCursor.slice(0, matchStart) + suggestion.insertText + textAfterCursor;
        newCursorPos = matchStart + suggestion.insertText.length;
      } else {
        newValue = textBeforeCursor + suggestion.insertText + textAfterCursor;
        newCursorPos = cursorPosition + suggestion.insertText.length;
      }

      setLocalValue(newValue);
      onChange(newValue);
      setShowSuggestions(false);
      setIsPickerMode(false);

      // Focus back to input
      setTimeout(() => {
        if (textFieldRef.current) {
          textFieldRef.current.focus();
          textFieldRef.current.setSelectionRange(newCursorPos, newCursorPos);
        }
      }, 0);
    },
    [localValue, cursorPosition, isPickerMode, onChange]
  );

  // Open the popper from the +Variable button with the full catalog
  const handlePickerOpen = useCallback(() => {
    if (allSuggestions.length === 0) {
      return;
    }
    // Snapshot the current cursor position so insertion lands at the right spot
    const pos = textFieldRef.current?.selectionStart ?? localValue.length;
    setCursorPosition(pos);
    setSuggestions(allSuggestions);
    setIsPickerMode(true);
    setShowSuggestions(true);
    // Anchor the popper to the input so it lines up underneath
    setAnchorEl(textFieldRef.current ?? pickerButtonRef.current);
  }, [allSuggestions, localValue.length]);

  const handlePopperClose = useCallback(() => {
    setShowSuggestions(false);
    setIsPickerMode(false);
  }, []);

  const getFieldStyle = () => {
    const hasError = !!error;
    return {
      fontSize: 'var(--ds-text-small)',
      '& .MuiOutlinedInput-root': {
        borderRadius: 'var(--ds-radius-md)',
        backgroundColor: 'white',
        fontSize: 'var(--ds-text-body-lg)',
        ...(hasError && {
          '&.Mui-error fieldset': {
            borderColor: 'var(--ds-red-500)',
            borderWidth: '1px',
          },
        }),
        '& fieldset': {
          borderColor: 'var(--ds-gray-200)',
        },
        '&:hover fieldset': {
          borderColor: 'var(--ds-blue-400)',
        },
        '&.Mui-focused fieldset': {
          borderColor: 'var(--ds-blue-500)',
          borderWidth: '2px',
        },
      },
      '& .MuiInputBase-input': {
        padding: 'var(--ds-space-2) var(--ds-space-3)',
        '&::placeholder': {
          color: 'var(--ds-brand-200)',
          fontWeight: 'var(--ds-font-weight-regular)',
          fontSize: 'var(--ds-text-small)',
          opacity: 1,
        },
      },
    };
  };

  const fieldContainer = {
    display: 'flex',
    flexDirection: 'column',
    width: '100%',
  };

  const labelStyle = {
    fontSize: 'var(--ds-text-body)',
    fontWeight: 'var(--ds-font-weight-medium)',
    color: 'var(--ds-brand-500)',
    mb: 0.5,
  };

  const inputProps = useMemo(() => {
    if (multiline || disabled || !validationStatus) return undefined;
    if (validationStatus === 'valid') {
      return {
        endAdornment: (
          <InputAdornment position='end'>
            <CheckIcon sx={{ color: 'var(--ds-green-500)', fontSize: 18 }} data-testid='url-valid-icon' />
          </InputAdornment>
        ),
      };
    }
    if (validationStatus === 'invalid') {
      return {
        endAdornment: (
          <InputAdornment position='end'>
            <CloseIcon sx={{ color: 'var(--ds-red-500)', fontSize: 18 }} data-testid='url-invalid-icon' />
          </InputAdornment>
        ),
      };
    }
    return undefined;
  }, [multiline, disabled, validationStatus]);

  const errorTextStyle = {
    color: 'var(--ds-red-500)',
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-medium)',
    mt: 1,
  };

  return (
    <Box sx={{ ...fieldContainer, position: 'relative', width: fullWidth ? '100%' : 'auto' }}>
      {label && (
        <Typography sx={labelStyle}>
          {label} {required && <span style={{ color: 'var(--ds-red-500)' }}>*</span>}
        </Typography>
      )}

      <Box sx={{ display: 'flex', alignItems: multiline ? 'flex-start' : 'center', gap: 0.5, width: '100%' }}>
        <TextField
          inputRef={textFieldRef}
          value={localValue}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          disabled={disabled}
          multiline={multiline}
          rows={rows}
          maxRows={maxRows}
          error={!!error}
          required={required}
          fullWidth={fullWidth}
          variant='outlined'
          InputProps={inputProps}
          sx={getFieldStyle()}
        />
        {allSuggestions.length > 0 && !disabled && (
          <Box ref={pickerButtonRef} sx={{ mt: multiline ? '4px' : 0 }}>
            <Button
              composition='icon-only'
              tone='secondary'
              size='sm'
              tooltip='Insert variable'
              aria-label='Insert variable'
              icon={<DataObjectIcon sx={{ fontSize: 'var(--ds-text-title)' }} />}
              onClick={handlePickerOpen}
              id='template-variable-picker-button'
            />
          </Box>
        )}
      </Box>

      {error && <Typography sx={errorTextStyle}>{error}</Typography>}

      {/* Suggestions Popper */}
      <Popper open={showSuggestions} anchorEl={anchorEl} placement='bottom-start' sx={{ zIndex: 1300 }}>
        <ClickAwayListener onClickAway={handlePopperClose}>
          <Paper
            sx={{
              minWidth: 320,
              maxWidth: 440,
              maxHeight: 360,
              overflow: 'auto',
              border: '1px solid var(--ds-gray-300)',
              boxShadow: '0 var(--ds-space-1) var(--ds-space-3) var(--ds-gray-alpha-300)',
            }}
          >
            <List dense disablePadding>
              {groupedSuggestions.map((group) => (
                <React.Fragment key={group.category}>
                  <Box
                    sx={{
                      px: 2,
                      pt: 1,
                      pb: 0.5,
                      backgroundColor: 'var(--ds-background-200)',
                      borderTop: '1px solid var(--ds-gray-100)',
                      position: 'sticky',
                      top: 0,
                    }}
                  >
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        textTransform: 'uppercase',
                        letterSpacing: '0.5px',
                        color: 'var(--ds-brand-500)',
                      }}
                    >
                      {group.label}
                    </Typography>
                  </Box>
                  {group.items.map((suggestion, index) => (
                    <ListItem
                      key={`${group.category}-${index}`}
                      onClick={() => handleSuggestionClick(suggestion)}
                      sx={{
                        cursor: 'pointer',
                        '&:hover': {
                          backgroundColor: 'var(--ds-background-300)',
                        },
                      }}
                    >
                      <ListItemText
                        primary={
                          <Typography sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-small)', color: 'var(--ds-blue-600)' }}>
                            {suggestion.insertText}
                          </Typography>
                        }
                        secondary={
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>{suggestion.description}</Typography>
                        }
                      />
                    </ListItem>
                  ))}
                </React.Fragment>
              ))}
            </List>
          </Paper>
        </ClickAwayListener>
      </Popper>
    </Box>
  );
};

export default TemplateTextField;
