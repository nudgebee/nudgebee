import React, { useState, useEffect } from 'react';
import {
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  TextField,
  Typography,
  Box,
  Button,
  CircularProgress,
  Alert,
  Checkbox,
  FormControlLabel,
} from '@mui/material';
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
    <Dialog open={open} onClose={handleClose} maxWidth='md' fullWidth>
      <DialogTitle
        sx={{
          fontSize: '20px',
          fontWeight: 600,
          color: colors.text.secondary,
          borderBottom: `1px solid ${colors.border.vertical}`,
          pb: 2,
        }}
      >
        Trigger Automation: {workflowName}
      </DialogTitle>

      <DialogContent sx={{ pt: 3 }}>
        <Box sx={{ mb: 3, mt: '8px' }}>
          <Typography
            sx={{
              fontSize: '14px',
              color: colors.text.secondaryDark,
              mb: 1,
            }}
          >
            <strong>Trigger Type:</strong> {triggerType?.charAt(0).toUpperCase() + triggerType?.slice(1)}
          </Typography>
          <Typography
            sx={{
              fontSize: '14px',
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
                fontSize: '14px',
                fontWeight: 500,
                color: colors.text.secondary,
                mb: 1,
              }}
            >
              Expected Input Variables
            </Typography>
            <Typography
              sx={{
                fontSize: '12px',
                color: colors.text.secondaryDark,
                mb: 2,
              }}
            >
              The following input variables are configured for this automation:
            </Typography>
            <Box
              sx={{
                border: '1px solid #e5e7eb',
                borderRadius: '8px',
                backgroundColor: '#f8fafc',
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
                    borderRadius: '6px',
                    border: '1px solid #e5e7eb',
                  }}
                >
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, minWidth: 0, flex: 1 }}>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                      <Typography
                        sx={{
                          fontSize: '13px',
                          fontWeight: 600,
                          color: colors.text.secondary,
                          fontFamily: 'monospace',
                        }}
                      >
                        {input.id}
                      </Typography>
                      <Box
                        sx={{
                          backgroundColor: '#e5e7eb',
                          color: '#374151',
                          px: 0.75,
                          py: 0.25,
                          borderRadius: '4px',
                          fontSize: '10px',
                          fontWeight: 500,
                          lineHeight: 1.2,
                          flexShrink: 0,
                        }}
                      >
                        {input.type}
                      </Box>
                    </Box>
                    <Typography
                      sx={{
                        fontSize: '11px',
                        color: colors.text.secondaryDark,
                      }}
                    >
                      {input.description}
                    </Typography>
                  </Box>
                  <Typography
                    sx={{
                      fontSize: '11px',
                      color: '#6b7280',
                      fontFamily: 'monospace',
                      backgroundColor: '#f3f4f6',
                      px: 1,
                      py: 0.5,
                      borderRadius: '4px',
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
                fontSize: '14px',
                fontWeight: 500,
                color: colors.text.secondary,
                mb: 1,
              }}
            >
              Input Variables
            </Typography>
            <Typography
              sx={{
                fontSize: '12px',
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
            sx={{ mb: 2, '& .MuiFormControlLabel-label': { fontSize: '13px', color: colors.text.secondary } }}
          />
        )}

        <Box sx={{ mb: 2 }}>
          <Typography
            sx={{
              fontSize: '14px',
              fontWeight: 500,
              color: colors.text.secondary,
              mb: 1,
            }}
          >
            Input Parameters (JSON)
          </Typography>
          <Typography
            sx={{
              fontSize: '12px',
              color: colors.text.secondaryDark,
              mb: 2,
            }}
          >
            Provide JSON input parameters that will be passed to the automation execution. Leave empty {'{}'} for no inputs.
          </Typography>

          <TextField
            fullWidth
            multiline
            rows={8}
            value={inputsJson}
            onChange={(e) => handleInputsChange(e.target.value)}
            placeholder={getPlaceholderText()}
            error={!!jsonError}
            helperText={jsonError}
            sx={{
              '& .MuiOutlinedInput-root': {
                fontFamily: 'monospace',
                fontSize: '13px',
                backgroundColor: colors.background.primaryLightest,
              },
            }}
          />
        </Box>

        {jsonError && (
          <Alert severity='error' sx={{ mb: 2 }}>
            {jsonError}
          </Alert>
        )}

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
              fontSize: '13px',
              fontWeight: 500,
              color: colors.text.secondary,
              mb: 1,
            }}
          >
            Examples:
          </Typography>
          <Typography
            sx={{
              fontSize: '12px',
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
      </DialogContent>

      <DialogActions
        sx={{
          borderTop: `1px solid ${colors.border.vertical}`,
          px: 3,
          py: 2,
          gap: 1,
        }}
      >
        <Button
          onClick={handleClose}
          variant='outlined'
          sx={{
            textTransform: 'none',
            borderColor: colors.border.vertical,
            color: colors.text.secondary,
          }}
          disabled={loading}
        >
          Cancel
        </Button>
        <Button
          onClick={handleTrigger}
          variant='contained'
          disabled={!!jsonError || loading}
          startIcon={loading ? <CircularProgress size={16} /> : null}
          sx={{
            textTransform: 'none',
            backgroundColor: colors.primary,
            '&:hover': {
              backgroundColor: colors.darkPrimary,
            },
            '&:disabled': {
              backgroundColor: colors.background.tertiaryLightest,
              color: colors.text.secondaryDark,
            },
          }}
        >
          {loading ? 'Triggering...' : 'Trigger Automation'}
        </Button>
      </DialogActions>
    </Dialog>
  );
};

export default TriggerWorkflowModal;
