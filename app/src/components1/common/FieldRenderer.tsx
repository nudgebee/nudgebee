import React from 'react';
import { Box, Typography, IconButton } from '@mui/material';
import { ContentCopy, InfoOutlined } from '@mui/icons-material';
import Text from './format/Text';
import { colors } from 'src/utils/colors';
import JsonTreeView from '@components1/common/JsonTreeView';

interface FieldRendererProps {
  data: any;
  schema: any;
  taskType?: string;
  fieldType: 'input' | 'output';
  taskDefinitions: any[];
  copyToClipboard: (text: string, label: string) => void;
}

const FieldRenderer: React.FC<FieldRendererProps> = ({ data, schema, taskType, fieldType, taskDefinitions, copyToClipboard }) => {
  if (!taskType || !data || typeof data !== 'object') {
    return (
      <Box sx={{ textAlign: 'center', color: colors.tertiary, py: 4 }}>
        <Typography>No schema available for formatting</Typography>
      </Box>
    );
  }

  const taskDefinition = taskDefinitions.find((def) => def.name === taskType);
  const fieldSchema = schema || taskDefinition?.[`${fieldType}_schema`];

  if (!fieldSchema || Object.keys(fieldSchema).length === 0) {
    return (
      <Box sx={{ textAlign: 'center', color: colors.tertiary, py: 4 }}>
        <Typography>
          No {fieldType} schema defined for {taskType}
        </Typography>
      </Box>
    );
  }

  const renderField = (key: string, value: any, fieldSchemaObj: any) => {
    const fieldSchemaItem = fieldSchemaObj[key];
    if (!fieldSchemaItem) {
      return null;
    }

    const fieldSchemaType = fieldSchemaItem.type || 'string';

    const getDisplayValue = () => {
      if (value === null || value === undefined) {
        return 'N/A';
      }

      if (fieldSchemaType === 'object' || fieldSchemaType === 'array' || typeof value === 'object') {
        return JSON.stringify(value, null, 2);
      }

      return String(value);
    };

    const displayValue = getDisplayValue();

    return (
      <Box
        key={key}
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          flexDirection: 'column',
          gap: 0.75,
          padding: 'var(--ds-space-3) var(--ds-space-4)',
          backgroundColor: colors.background.white,
          border: `1px solid ${colors.border.secondaryLight}`,
          borderRadius: 'var(--ds-radius-md)',
        }}
      >
        <Box sx={{ flex: 1, width: '100%', display: 'flex', justifyContent: 'space-between', marginBottom: 'var(--ds-space-1)' }}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 0.5,
              fontFamily: 'Poppins, sans-serif',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: colors.text.secondary,
              fontSize: 'var(--ds-text-caption)',
            }}
          >
            {key.charAt(0).toUpperCase() + key.slice(1).replace(/_/g, ' ')}
            {fieldSchemaItem.required && (
              <Box
                component='span'
                sx={{
                  color: colors.text.secondaryDark,
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                }}
              >
                (required)
              </Box>
            )}
          </Box>

          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            <Box
              sx={{
                backgroundColor: colors.background.tertiaryLightest,
                color: colors.text.secondaryDark,
                px: 0.75,
                py: 0.25,
                borderRadius: 0.5,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                textTransform: 'lowercase',
              }}
            >
              <Text sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }} value={fieldSchemaType} />
            </Box>
            <IconButton
              size='small'
              onClick={() => copyToClipboard(displayValue, `${key} value`)}
              sx={{ color: colors.text.secondaryDark, padding: 'var(--ds-space-1)' }}
            >
              <ContentCopy sx={{ fontSize: 'var(--ds-text-small)' }} />
            </IconButton>
          </Box>
        </Box>
        {(() => {
          const isComplexType = fieldSchemaType === 'object' || fieldSchemaType === 'array' || typeof value === 'object';
          const isJsonString =
            typeof value === 'string' &&
            value.trim().length > 1 &&
            ((value.trim().startsWith('{') && value.trim().endsWith('}')) || (value.trim().startsWith('[') && value.trim().endsWith(']')));

          if (isComplexType || isJsonString) {
            return <JsonTreeView data={value} defaultExpanded={2} maxHeight='200px' fontSize='12px' />;
          }

          return (
            <Box
              sx={{
                color: colors.text.secondary,
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                wordBreak: 'break-word',
                whiteSpace: 'pre-wrap',
                lineHeight: 1.5,
              }}
            >
              {displayValue}
            </Box>
          );
        })()}
      </Box>
    );
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
        <InfoOutlined sx={{ fontSize: 'var(--ds-text-body-lg)', color: colors.text.secondaryDark }} />
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark }}>
          Formatted according to {taskType} {fieldType} schema
        </Typography>
      </Box>
      {Object.keys(fieldSchema).map((key) => {
        const value = data[key];
        return renderField(key, value, fieldSchema);
      })}
    </Box>
  );
};

export default FieldRenderer;
