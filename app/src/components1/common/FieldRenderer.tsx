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
          padding: '14px 16px',
          backgroundColor: colors.background.white,
          border: `1px solid ${colors.border.secondaryLight}`,
          borderRadius: '6px',
        }}
      >
        <Box sx={{ flex: 1, width: '100%', display: 'flex', justifyContent: 'space-between', marginBottom: '4px' }}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 0.5,
              fontFamily: 'Poppins, sans-serif',
              fontWeight: 600,
              color: colors.text.secondary,
              fontSize: '11px',
            }}
          >
            {key.charAt(0).toUpperCase() + key.slice(1).replace(/_/g, ' ')}
            {fieldSchemaItem.required && (
              <Box
                component='span'
                sx={{
                  color: colors.text.secondaryDark,
                  fontSize: '10px',
                  fontWeight: 400,
                }}
              >
                (required)
              </Box>
            )}
          </Box>

          <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
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
              <Text sx={{ fontSize: '10px', fontWeight: 400 }} value={fieldSchemaType} />
            </Box>
            <IconButton
              size='small'
              onClick={() => copyToClipboard(displayValue, `${key} value`)}
              sx={{ color: colors.text.secondaryDark, padding: '2px' }}
            >
              <ContentCopy sx={{ fontSize: '12px' }} />
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
                fontSize: '12px',
                fontWeight: 400,
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
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
        <InfoOutlined sx={{ fontSize: '14px', color: colors.text.secondaryDark }} />
        <Typography sx={{ fontSize: '11px', color: colors.text.secondaryDark }}>
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
