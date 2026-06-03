import React, { useState, useEffect } from 'react';
import { Typography, Box, Checkbox, FormControlLabel } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import { Modal } from '@components1/ds/Modal';
import { colors } from 'src/utils/colors';

interface TriggerWorkflowModalProps {
  open: boolean;
  onClose: () => void;
  workflowName: string;
  triggerType: string;
  defaultInputs?: any;
  inputSchema?: Array<{
    id: string;
    type: 'string' | 'int' | 'bool' | 'json' | 'array';
    description: string;
    default: any;
  }>;
  onTrigger: (inputs: any) => Promise<void>;
  loading?: boolean;
}

const TriggerWorkflowModal: React.FC<TriggerWorkflowModalProps> = ({
  open,
  onClose,
  workflowName,
  triggerType,
  defaultInputs = {},
  inputSchema = [],
  onTrigger,
  loading = false,
}) => {
  const [inputsJson, setInputsJson] = useState<string>('{}');
  const [jsonError, setJsonError] = useState<string>('');
  const [useDefaults, setUseDefaults] = useState<boolean>(true);

  const hasDefaults = inputSchema.some((input) => input.default !== undefined && input.default !== null && input.default !== '');

  // Initialize inputs when modal opens or defaultInputs change
  useEffect(() => {
    if (open) {
      try {
        let initialInputs = {};

        // First try to use defaultInputs if available
        if (defaultInputs && Object.keys(defaultInputs).length > 0) {
          initialInputs = defaultInputs;
        }
        // If no defaultInputs but we have inputSchema, generate initial structure from schema
        else if (inputSchema && inputSchema.length > 0) {
          initialInputs = inputSchema.reduce((acc, input) => {
            acc[input.id] = input.default;
            return acc;
          }, {} as any);
        }

        setInputsJson(JSON.stringify(initialInputs, null, 2));
        setJsonError('');
      } catch (_error) {
        console.error(_error);
        setInputsJson('{}');
        setJsonError('');
      }
    }
  }, [open, defaultInputs, inputSchema]);

  const validateJson = (jsonString: string): boolean => {
    if (!jsonString.trim()) {
      setJsonError('');
      return true; // Empty is allowed
    }

    try {
      const parsed = JSON.parse(jsonString);
      if (typeof parsed !== 'object' || Array.isArray(parsed)) {
        setJsonError('Input must be a valid JSON object');
        return false;
      }
      setJsonError('');
      return true;
    } catch (_error) {
      console.error('JSON parse error:', _error);
      setJsonError('Invalid JSON format. Please provide a valid JSON object.');
      return false;
    }
  };

  const handleInputsChange = (value: string) => {
    setInputsJson(value);
    validateJson(value);
  };

  const handleTrigger = async () => {
    if (!validateJson(inputsJson)) {
      return;
    }

    try {
      const inputs = inputsJson.trim() ? JSON.parse(inputsJson) : {};
      await onTrigger(inputs);
      onClose(); // Close modal after successful trigger
    } catch (error) {
      console.error('Error triggering workflow:', error);
      setJsonError('Failed to parse JSON inputs');
    }
  };

  const handleClose = () => {
    setInputsJson('{}');
    setJsonError('');
    onClose();
  };

  const getTriggerDescription = () => {
    switch (triggerType) {
      case 'manual':
        return 'Trigger this automation manually with custom input parameters.';
      case 'schedule':
        return 'Trigger this scheduled automation immediately, bypassing the schedule.';
      case 'webhook':
        return 'Trigger this webhook automation manually with custom payload.';
      case 'event':
        return 'Trigger this event-driven automation manually with custom event data.';
      default:
        return 'Trigger this automation manually.';
    }
  };

  const getPlaceholderText = () => {
    switch (triggerType) {
      case 'webhook':
        return '{\n  "payload": {\n    "action": "test",\n    "data": "value"\n  }\n}';
      case 'event':
        return '{\n  "event": {\n    "type": "test_event",\n    "severity": "info"\n  }\n}';
      default:
        return '{\n  "param1": "value1",\n  "param2": 42,\n  "environment": "test"\n}';
    }
  };

  return (
    <Modal
      open={open}
      handleClose={handleClose}
      width='md'
      title={`Trigger Automation: ${workflowName}`}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, px: 3, py: 2 }}>
          <Button tone='secondary' size='md' onClick={handleClose} disabled={loading}>
            Cancel
          </Button>
          <Button tone='primary' size='md' onClick={handleTrigger} disabled={!!jsonError || loading} loading={loading}>
            {loading ? 'Triggering...' : 'Trigger Automation'}
          </Button>
        </Box>
      }
    >
      <Box sx={{ pt: 3 }}>
        <Box sx={{ mb: 3, mt: 'var(--ds-space-2)' }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              color: colors.text.secondaryDark,
              mb: 1,
            }}
          >
            <strong>Trigger Type:</strong> {triggerType?.charAt(0).toUpperCase() + triggerType?.slice(1)}
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              color: colors.text.secondaryDark,
              mb: 2,
            }}
          >
            {getTriggerDescription()}
          </Typography>
        </Box>

        {/* Input Schema Display */}
        {inputSchema && inputSchema.length > 0 ? (
          <Box sx={{ mb: 3 }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                mb: 1,
              }}
            >
              Expected Input Variables
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: colors.text.secondaryDark,
                mb: 2,
              }}
            >
              The following input variables are configured for this automation:
            </Typography>
            <Box
              sx={{
                border: '1px solid var(--ds-brand-150)',
                borderRadius: 'var(--ds-radius-lg)',
                backgroundColor: 'var(--ds-background-200)',
                p: 2,
                display: 'flex',
                flexDirection: 'column',
                gap: 1,
              }}
            >
              {inputSchema.map((input) => (
                <Box
                  key={input.id}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    px: 1.5,
                    py: 1.25,
                    backgroundColor: 'white',
                    borderRadius: 'var(--ds-radius-md)',
                    border: '1px solid var(--ds-brand-150)',
                  }}
                >
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, minWidth: 0, flex: 1 }}>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                      <Typography
                        sx={{
                          fontSize: 'var(--ds-text-body)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: colors.text.secondary,
                          fontFamily: 'monospace',
                        }}
                      >
                        {input.id}
                      </Typography>
                      <Box
                        sx={{
                          backgroundColor: 'var(--ds-brand-150)',
                          color: 'var(--ds-brand-500)',
                          px: 0.75,
                          py: 0.25,
                          borderRadius: 'var(--ds-radius-sm)',
                          fontSize: 'var(--ds-text-caption)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          lineHeight: 1.2,
                          flexShrink: 0,
                        }}
                      >
                        {input.type}
                      </Box>
                    </Box>
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-caption)',
                        color: colors.text.secondaryDark,
                      }}
                    >
                      {input.description}
                    </Typography>
                  </Box>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      color: 'var(--ds-gray-600)',
                      fontFamily: 'monospace',
                      backgroundColor: 'var(--ds-background-300)',
                      px: 1,
                      py: 0.5,
                      borderRadius: 'var(--ds-radius-sm)',
                      ml: 2,
                      overflowWrap: 'anywhere',
                      flexShrink: 0,
                      maxWidth: '40%',
                    }}
                  >
                    {input.type === 'json' || input.type === 'array' ? JSON.stringify(input.default) : String(input.default) || '""'}
                  </Typography>
                </Box>
              ))}
            </Box>
          </Box>
        ) : (
          // Show a message when no input schema is configured
          <Box sx={{ mb: 3 }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                mb: 1,
              }}
            >
              Input Variables
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: colors.text.secondaryDark,
                mb: 2,
                fontStyle: 'italic',
              }}
            >
              No input variables are configured for this automation. You can provide custom JSON input below or leave it empty.
            </Typography>
          </Box>
        )}

        {/* Use default values checkbox */}
        {hasDefaults && (
          <FormControlLabel
            control={
              <Checkbox
                checked={useDefaults}
                onChange={(e) => {
                  setUseDefaults(e.target.checked);
                  if (e.target.checked) {
                    const defaults = inputSchema.reduce((acc, input) => {
                      acc[input.id] = input.default;
                      return acc;
                    }, {} as any);
                    setInputsJson(JSON.stringify(defaults, null, 2));
                    setJsonError('');
                  } else {
                    setInputsJson('{}');
                    setJsonError('');
                  }
                }}
                size='small'
              />
            }
            label='Use default values'
            sx={{ mb: 2, '& .MuiFormControlLabel-label': { fontSize: 'var(--ds-text-body)', color: colors.text.secondary } }}
          />
        )}

        <Box sx={{ mb: 2 }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: colors.text.secondary,
              mb: 1,
            }}
          >
            Input Parameters (JSON)
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: colors.text.secondaryDark,
              mb: 2,
            }}
          >
            Provide JSON input parameters that will be passed to the automation execution. Leave empty {'{}'} for no inputs.
          </Typography>

          <Input
            type='textarea'
            rows={8}
            value={inputsJson}
            onChange={handleInputsChange}
            placeholder={getPlaceholderText()}
            error={jsonError || undefined}
          />
        </Box>

        <Box
          sx={{
            mt: 2,
            p: 2,
            backgroundColor: colors.background.primaryLightest,
            borderRadius: 1,
            border: `1px solid ${colors.border.vertical}`,
          }}
        >
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: colors.text.secondary,
              mb: 1,
            }}
          >
            Examples:
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: colors.text.secondaryDark,
              lineHeight: 1.5,
              fontFamily: 'monospace',
            }}
            component='div'
          >
            • Simple parameters: <code>{`{"env": "test", "debug": true}`}</code>
            <br />• Complex data: <code>{`{"user": {"id": 123, "name": "test"}, "config": {"timeout": 30}}`}</code>
            <br />• Empty inputs: <code>{`{}`}</code>
          </Typography>
        </Box>
      </Box>
    </Modal>
  );
};

export default TriggerWorkflowModal;
