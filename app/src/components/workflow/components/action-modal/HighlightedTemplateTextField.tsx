import React, { useState, useRef, useCallback, useMemo, memo } from 'react';
import { Box, TextField, Popper, Paper, List, ListItem, ListItemText, Typography, Chip } from '@mui/material';
import { Code as CodeIcon, Close as CloseIcon } from '@mui/icons-material';
import { colors } from 'src/utils/colors';

interface PreviousTask {
  id: string;
  name: string;
  type: string;
  outputSchema?: any;
}

interface HighlightedTemplateTextFieldProps {
  value: string;
  onChange: (fieldName: string, value: string) => void;
  fieldName: string; // Required field name for onChange callback
  placeholder?: string;
  label?: string;
  disabled?: boolean;
  multiline?: boolean;
  rows?: number;
  maxRows?: number;
  previousTasks?: PreviousTask[];
  error?: string;
  required?: boolean;
  fullWidth?: boolean;
  showHighlightedPreview?: boolean; // Show visual template boxes above input
}

interface TemplateSuggestion {
  text: string;
  description: string;
  type: 'task' | 'field';
  insertText: string;
}

interface ParsedSegment {
  type: 'text' | 'template';
  content: string;
  taskId?: string;
  field?: string;
  startIndex: number;
  endIndex: number;
}

// Template syntax patterns
const PARTIAL_TEMPLATE_PATTERN = /\{\{\s*(Tasks(?:\[['"][^'"]*['"]\])?(?:\.output(?:\.[^}\s]*)?)?)?$/;
const FULL_TEMPLATE_PATTERN = /\{\{\s*(Tasks\[['"]([^'"]*)['"]\]\.output(?:\.([^}\s]*))?|Inputs\[['"]([^'"]*)['"]\])\s*\}\}/g;

/**
 * Parse text to extract template expressions and plain text segments
 */
const parseTemplateExpressions = (text: string): ParsedSegment[] => {
  if (!text) {
    return [];
  }

  const segments: ParsedSegment[] = [];
  let lastIndex = 0;
  let match;

  const pattern = new RegExp(FULL_TEMPLATE_PATTERN.source, 'g');

  while ((match = pattern.exec(text)) !== null) {
    // Add text before match
    if (match.index > lastIndex) {
      segments.push({
        type: 'text',
        content: text.slice(lastIndex, match.index),
        startIndex: lastIndex,
        endIndex: match.index,
      });
    }

    // Parse the template expression
    const fullMatch = match[0];
    const taskId = match[2]; // Tasks['taskId']
    const field = match[3]; // .output.field
    const inputId = match[4]; // Inputs['inputId']

    segments.push({
      type: 'template',
      content: fullMatch,
      taskId: taskId || inputId,
      field: field || (inputId ? 'input' : undefined),
      startIndex: match.index,
      endIndex: match.index + fullMatch.length,
    });

    lastIndex = match.index + fullMatch.length;
  }

  // Add remaining text
  if (lastIndex < text.length) {
    segments.push({
      type: 'text',
      content: text.slice(lastIndex),
      startIndex: lastIndex,
      endIndex: text.length,
    });
  }

  return segments;
};

/**
 * Get a short display label for a template expression
 */
const getTemplateLabel = (segment: ParsedSegment): string => {
  if (segment.content.includes('Inputs[')) {
    return `Inputs.${segment.taskId}`;
  }
  if (segment.field) {
    return `${segment.taskId}.${segment.field}`;
  }
  return `${segment.taskId}.output`;
};

// Memoized template chip component
const TemplateChip = memo(({ segment, disabled, onRemove }: { segment: ParsedSegment; disabled: boolean; onRemove: () => void }) => (
  <Chip
    label={getTemplateLabel(segment)}
    size='small'
    icon={<CodeIcon sx={{ fontSize: 'var(--ds-text-body-lg) !important' }} />}
    onDelete={disabled ? undefined : onRemove}
    deleteIcon={<CloseIcon sx={{ fontSize: 'var(--ds-text-body-lg) !important' }} />}
    sx={{
      height: 24,
      fontSize: 'var(--ds-text-caption)',
      fontFamily: 'monospace',
      backgroundColor: 'var(--ds-blue-200)',
      color: 'var(--ds-blue-700)',
      border: '1px solid var(--ds-blue-300)',
      '& .MuiChip-icon': {
        color: 'var(--ds-blue-500)',
      },
      '& .MuiChip-deleteIcon': {
        color: 'var(--ds-gray-600)',
        '&:hover': {
          color: 'var(--ds-red-600)',
        },
      },
    }}
  />
));

TemplateChip.displayName = 'TemplateChip';

// Memoized preview component
const TemplatePreview = memo(
  ({
    segments,
    disabled,
    onRemoveTemplate,
  }: {
    segments: ParsedSegment[];
    disabled: boolean;
    onRemoveTemplate: (segment: ParsedSegment) => void;
  }) => {
    const templateSegments = segments.filter((s) => s.type === 'template');

    if (templateSegments.length === 0) {
      return null;
    }

    return (
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: 0.5,
          p: 1,
          mb: 1,
          border: '1px dashed',
          borderColor: 'var(--ds-blue-300)',
          borderRadius: 1,
          backgroundColor: 'var(--ds-blue-100)',
          minHeight: 36,
          alignItems: 'center',
        }}
      >
        {templateSegments.map((segment, idx) => (
          <TemplateChip key={`${segment.startIndex}-${idx}`} segment={segment} disabled={disabled} onRemove={() => onRemoveTemplate(segment)} />
        ))}
      </Box>
    );
  }
);

TemplatePreview.displayName = 'TemplatePreview';

// Static styles - defined outside component to prevent recreation
const fieldStyleBase = {
  fontSize: 'var(--ds-text-small)',
  '& .MuiOutlinedInput-root': {
    borderRadius: 'var(--ds-radius-md)',
    backgroundColor: 'white',
    fontSize: 'var(--ds-text-body-lg)',
    '& fieldset': {
      borderColor: colors.border?.vertical || '#e0e0e0',
    },
    '&:hover fieldset': {
      borderColor: colors.border?.primaryLightest || '#1976d2',
    },
    '&.Mui-focused fieldset': {
      borderColor: colors.border?.primary || '#1976d2',
      borderWidth: '2px',
    },
  },
  '& .MuiInputBase-input': {
    padding: 'var(--ds-space-2) var(--ds-space-3)',
    '&::placeholder': {
      color: colors.text?.tertiarymedium || '#9e9e9e',
      fontWeight: 'var(--ds-font-weight-regular)',
      fontSize: 'var(--ds-text-small)',
      opacity: 1,
    },
  },
};

const fieldStyleWithError = {
  ...fieldStyleBase,
  '& .MuiOutlinedInput-root': {
    ...fieldStyleBase['& .MuiOutlinedInput-root'],
    '&.Mui-error fieldset': {
      borderColor: colors.border?.error || '#d32f2f',
      borderWidth: '1px',
    },
  },
};

const labelStyle = {
  fontSize: 'var(--ds-text-body)',
  fontWeight: 'var(--ds-font-weight-medium)',
  color: colors.text?.secondary || '#424242',
  mb: 0.5,
};

const errorTextStyle = {
  color: colors.border?.error || '#d32f2f',
  fontSize: 'var(--ds-text-small)',
  fontWeight: 'var(--ds-font-weight-medium)',
  mt: 1,
};

const HighlightedTemplateTextField: React.FC<HighlightedTemplateTextFieldProps> = ({
  value,
  onChange,
  fieldName,
  placeholder,
  label,
  disabled = false,
  multiline = false,
  rows = 1,
  maxRows = 4,
  previousTasks = [],
  error,
  required = false,
  fullWidth = true,
  showHighlightedPreview = true,
}) => {
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [suggestions, setSuggestions] = useState<TemplateSuggestion[]>([]);
  const [cursorPosition, setCursorPosition] = useState(0);
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null);
  const textFieldRef = useRef<HTMLInputElement | HTMLTextAreaElement>(null);

  // Parse the current value into segments - memoized
  const parsedSegments = useMemo(() => parseTemplateExpressions(value || ''), [value]);

  // Get the appropriate field style based on error state
  const fieldStyle = useMemo(() => (error ? fieldStyleWithError : fieldStyleBase), [error]);

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

      // If user just typed "{{", suggest Tasks syntax
      if (!partial || partial === 'Tasks') {
        previousTasks.forEach((task) => {
          newSuggestions.push({
            text: `Tasks['${task.id}'].output`,
            description: `Access output from ${task.name || task.type}`,
            type: 'task',
            insertText: `{{ Tasks['${task.id}'].output }}`,
          });
        });
      }

      // If user is typing a specific task reference
      else if (partial.includes('Tasks[') && partial.includes('].output')) {
        const taskIdMatch = /Tasks\[['"]([^'"]*)['"]\]\.output(?:\.([^.]*))?/.exec(partial);
        if (taskIdMatch) {
          const [, taskId, fieldPrefix] = taskIdMatch;
          const task = previousTasks.find((t) => t.id === taskId);

          if (task?.outputSchema) {
            // Suggest available output fields
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

      return newSuggestions;
    },
    [previousTasks]
  );

  // Handle input changes
  const handleChange = useCallback(
    (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      const newValue = event.target.value;
      const position = event.target.selectionStart || 0;

      onChange(fieldName, newValue);
      setCursorPosition(position);

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
    [onChange, fieldName, generateSuggestions]
  );

  // Handle key events
  const handleKeyDown = useCallback((event: React.KeyboardEvent) => {
    if (event.key === 'Escape') {
      setShowSuggestions(false);
    }
  }, []);

  // Handle suggestion selection
  const handleSuggestionClick = useCallback(
    (suggestion: TemplateSuggestion) => {
      const textBeforeCursor = value.slice(0, cursorPosition);
      const textAfterCursor = value.slice(cursorPosition);
      const partialMatch = PARTIAL_TEMPLATE_PATTERN.exec(textBeforeCursor);

      if (partialMatch) {
        const matchStart = partialMatch.index!;
        const newValue = textBeforeCursor.slice(0, matchStart) + suggestion.insertText + textAfterCursor;

        onChange(fieldName, newValue);
        setShowSuggestions(false);

        // Focus back to input
        setTimeout(() => {
          if (textFieldRef.current) {
            const newCursorPos = matchStart + suggestion.insertText.length;
            textFieldRef.current.focus();
            textFieldRef.current.setSelectionRange(newCursorPos, newCursorPos);
          }
        }, 0);
      }
    },
    [value, cursorPosition, onChange, fieldName]
  );

  // Handle removing a template expression - memoized callback
  const handleRemoveTemplate = useCallback(
    (segment: ParsedSegment) => {
      const newValue = value.slice(0, segment.startIndex) + value.slice(segment.endIndex);
      onChange(fieldName, newValue);
    },
    [value, onChange, fieldName]
  );

  // Container style
  const containerStyle = useMemo(
    () => ({
      display: 'flex',
      flexDirection: 'column' as const,
      position: 'relative' as const,
      width: fullWidth ? '100%' : 'auto',
    }),
    [fullWidth]
  );

  return (
    <Box sx={containerStyle}>
      {label && (
        <Typography sx={labelStyle}>
          {label} {required && <span style={{ color: colors.border.error }}>*</span>}
        </Typography>
      )}

      {/* Highlighted Template Preview - only show templates, not text */}
      {showHighlightedPreview && <TemplatePreview segments={parsedSegments} disabled={disabled} onRemoveTemplate={handleRemoveTemplate} />}

      <TextField
        inputRef={textFieldRef}
        value={value}
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
        sx={fieldStyle}
      />

      {error && <Typography sx={errorTextStyle}>{error}</Typography>}

      {/* Suggestions Popper */}
      <Popper open={showSuggestions} anchorEl={anchorEl} placement='bottom-start' sx={{ zIndex: 1300 }}>
        <Paper
          sx={{
            maxWidth: 400,
            maxHeight: 200,
            overflow: 'auto',
            border: '1px solid var(--ds-gray-300)',
            boxShadow: '0 4px 12px rgba(0,0,0,0.1)',
          }}
        >
          <List dense>
            {suggestions.map((suggestion, index) => (
              <ListItem
                key={index}
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
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark }}>{suggestion.description}</Typography>
                  }
                />
              </ListItem>
            ))}
          </List>
        </Paper>
      </Popper>
    </Box>
  );
};

export default memo(HighlightedTemplateTextField);
