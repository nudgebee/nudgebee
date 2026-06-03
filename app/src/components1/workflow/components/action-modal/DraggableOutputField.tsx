import React from 'react';
import { Box, Typography, Tooltip } from '@mui/material';
import { DragIndicator } from '@mui/icons-material';
import { colors } from 'src/utils/colors';

interface DraggableOutputFieldProps {
  taskId: string;
  taskName: string;
  fieldName: string;
  fieldType: string;
  fieldPath?: string;
  value?: any;
  isInput?: boolean; // true for workflow inputs, false for task outputs
  isConfig?: boolean; // true for workflow configs
  isSecret?: boolean; // true for secrets (uses Secret['key'] template)
  description?: string; // Optional description for the field
}

const DraggableOutputField: React.FC<DraggableOutputFieldProps> = ({
  taskId,
  taskName,
  fieldName,
  fieldType,
  fieldPath,
  value,
  isInput = false,
  isConfig = false,
  isSecret = false,
  description,
}) => {
  // Generate the template expression based on whether it's an input, config, secret, or task output
  const getTemplateExpression = () => {
    if (isSecret) {
      return `{{ Secrets['${fieldName}'] }}`;
    }
    if (isConfig) {
      return `{{ Configs['${fieldName}'] }}`;
    }
    if (isInput) {
      return `{{ Inputs['${fieldName}'] }}`;
    }
    const path = fieldPath ? `.${fieldPath}` : '';
    return `{{ Tasks['${taskId}'].output${path || `.${fieldName}`} }}`;
  };

  const templateExpression = getTemplateExpression();

  const handleDragStart = (e: React.DragEvent<HTMLDivElement>) => {
    e.dataTransfer.setData('text/plain', templateExpression);
    e.dataTransfer.setData(
      'application/x-template-field',
      JSON.stringify({
        taskId,
        taskName,
        fieldName,
        fieldType,
        fieldPath,
        templateExpression,
        isInput,
        isConfig,
        isSecret,
      })
    );
    e.dataTransfer.effectAllowed = 'copy';

    // Set a custom drag image
    const dragElement = e.currentTarget.cloneNode(true) as HTMLElement;
    dragElement.style.position = 'absolute';
    dragElement.style.top = '-1000px';
    dragElement.style.opacity = '0.8';
    dragElement.style.backgroundColor = '#e3f2fd';
    document.body.appendChild(dragElement);
    e.dataTransfer.setDragImage(dragElement, 0, 0);

    setTimeout(() => {
      document.body.removeChild(dragElement);
    }, 0);
  };

  const handleDragEnd = () => {
    // Cleanup if needed
  };

  const hasValue = value !== undefined && value !== null;

  // Format value for display
  const formatValueForDisplay = (val: any): string => {
    if (val === undefined || val === null) {
      return '';
    }
    if (typeof val === 'object') {
      return JSON.stringify(val).slice(0, 30);
    }
    return String(val).slice(0, 30);
  };

  const tooltipContent = description
    ? `${description}\n\nDrag to insert: ${templateExpression}${hasValue ? `\nDefault: ${formatValueForDisplay(value)}` : ''}`
    : `Drag to insert: ${templateExpression}${hasValue ? `\nDefault: ${formatValueForDisplay(value)}` : ''}`;

  return (
    <Tooltip title={tooltipContent} placement='right'>
      <Box
        draggable
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          p: 0.75,
          border: '1px dashed',
          borderColor: hasValue ? '#a7f3d0' : '#d1d5db',
          borderRadius: 1,
          backgroundColor: hasValue ? '#f0fdf4' : '#f9fafb',
          cursor: 'grab',
          transition: 'all 0.2s ease',
          '&:hover': {
            borderColor: 'var(--ds-blue-400)',
            backgroundColor: 'var(--ds-blue-100)',
            boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
          },
          '&:active': {
            cursor: 'grabbing',
            opacity: 0.7,
          },
        }}
      >
        <DragIndicator sx={{ fontSize: 14, color: 'var(--ds-gray-400)' }} />

        <Box
          sx={{
            bgcolor: colors.text.secondary,
            color: 'white',
            px: 0.5,
            py: 0.125,
            borderRadius: 0.25,
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            minWidth: 36,
            textAlign: 'center',
            textTransform: 'lowercase',
          }}
        >
          {fieldType || 'any'}
        </Box>

        <Box sx={{ flex: 1, minWidth: 0, overflow: 'hidden' }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: colors.text.secondary,
              lineHeight: 1.2,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {fieldName}
          </Typography>

          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontFamily: 'monospace',
              color: 'var(--ds-blue-600)',
              lineHeight: 1.2,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {templateExpression}
          </Typography>
        </Box>

        {hasValue && (
          <Box
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-green-700)',
              bgcolor: 'var(--ds-green-100)',
              px: 0.5,
              py: 0.125,
              borderRadius: 0.25,
              maxWidth: 80,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
            title={`Default: ${formatValueForDisplay(value)}`}
          >
            {formatValueForDisplay(value)}
          </Box>
        )}
      </Box>
    </Tooltip>
  );
};

export default DraggableOutputField;
