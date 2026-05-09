import CustomButton from '@components1/common/NewCustomButton';
import {
  Box,
  TextField,
  Typography,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Paper,
  Select,
  MenuItem,
  FormControl,
} from '@mui/material';
import React, { useState, useEffect } from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import { isVariableNameValid, getLlmIdentifierValidationMessage } from 'src/utils/common';
import PromptRewriteModal from './PromptRewriteModal';
import { Modal } from '@components1/common/modal';
import ConversationPopup from './ConversationPopup';
import { v4 as uuidv4 } from 'uuid';
import CustomLabels from '@components1/common/widgets/CustomLabels';

const styles = {
  label: {
    mb: '0px',
    fontSize: '14px',
    fontWeight: 500,
    color: colors.text.secondary,
  },
  inputField: {
    fontSize: '14px',
    '& .MuiOutlinedInput-root': {
      borderRadius: '8px',
      backgroundColor: 'white',
      '&.Mui-error fieldset': {
        borderColor: colors.border.error,
        borderWidth: '1px',
      },
      '& fieldset': {
        borderColor: colors.border.vertical,
      },
      '&:hover fieldset': {
        borderColor: colors.border.vertical,
      },
    },
    '& .MuiInputBase-input': {
      padding: '8px 12px',
      '&::placeholder': {
        fontSize: '14px',
        opacity: 0.4,
      },
    },
    '& .MuiInputBase-inputMultiline': {
      padding: '0px 0px !important',
      '&::placeholder': {
        fontSize: '14px',
        opacity: 0.4,
      },
    },
  },
  errorText: {
    color: colors.border.error,
    fontSize: '12px',
    fontWeight: 500,
    mt: 1,
  },
  requiredStar: {
    color: colors.border.error,
    marginBottom: '4px',
  },
  linkText: {
    color: colors.text.primary,
    textDecoration: 'underline',
    cursor: 'pointer',
    fontSize: '14px',
  },
  instructionText: {
    fontSize: '12px',
    color: colors.text.tertiary,
    mb: '8px',
  },
  promptContainer: {
    border: `1px solid ${colors.border.vertical}`,
    borderRadius: '8px',
    minHeight: '200px',
    padding: '12px',
    backgroundColor: 'white',
  },
  variableTable: {
    '& .MuiTableCell-root': {
      padding: '8px 12px',
      fontSize: '14px',
    },
    '& .MuiTableHead-root .MuiTableCell-root': {
      fontWeight: 600,
      backgroundColor: colors.background.ticketDescription,
    },
  },
};

const CreateFunction = ({
  accountId,
  _handleClose,
  editMode = false,
  functionData = null,
  triggerSubmit = false,
  onSubmitStart,
  onSubmitEnd,
  isModal = false,
  functionList = [],
}) => {
  const [functionName, setFunctionName] = useState(editMode && functionData ? functionData.name : '');
  const [description, setDescription] = useState(editMode && functionData ? functionData.description : '');
  const [prompt, setPrompt] = useState(editMode && functionData ? functionData.prompt : '');
  const [status, setStatus] = useState(editMode && functionData ? functionData.status || 'active' : 'active');
  const [detectedVariables, setDetectedVariables] = useState([]);
  const [variableDefaults, setVariableDefaults] = useState({});
  const [loading, setLoading] = useState(false);
  const [showAgentList, setShowAgentList] = useState(false);
  const [showFunctionList, setShowFunctionList] = useState(false);
  const [showPromptRewriteModal, setShowPromptRewriteModal] = useState(false);
  const [showConversationPopup, setShowConversationPopup] = useState(false);
  const [testSessionId, setTestSessionId] = useState('');
  const [agentList, setAgentList] = useState([]);
  const [searchAgent, setSearchAgent] = useState('');
  const [searchFunction, setSearchFunction] = useState('');
  const [errors, setErrors] = useState({
    functionName: '',
    description: '',
    prompt: '',
    variables: '',
  });

  // Load agents and functions on component mount
  useEffect(() => {
    loadAgents();
  }, [accountId]);

  // Initialize data for edit mode
  useEffect(() => {
    if (editMode && functionData) {
      try {
        // Handle variables - could be JSON array, comma-separated string, or already an array
        let variables = [];
        if (functionData.variables) {
          // If it's already an array, use it directly
          if (Array.isArray(functionData.variables)) {
            variables = functionData.variables;
          } else if (typeof functionData.variables === 'string') {
            try {
              // Try parsing as JSON first
              variables = JSON.parse(functionData.variables);
              // Ensure it's an array
              if (!Array.isArray(variables)) {
                variables = [];
              }
            } catch {
              // If JSON parsing fails, treat as comma-separated string
              variables = functionData.variables
                .split(',')
                .map((v) => v.trim())
                .filter((v) => v.length > 0);
            }
          }
        }

        // Handle variable defaults - could be JSON object or already an object
        let defaults = {};
        if (functionData.variable_defaults) {
          // If it's already an object, use it directly
          if (
            typeof functionData.variable_defaults === 'object' &&
            functionData.variable_defaults !== null &&
            !Array.isArray(functionData.variable_defaults)
          ) {
            defaults = functionData.variable_defaults;
          } else if (typeof functionData.variable_defaults === 'string') {
            try {
              defaults = JSON.parse(functionData.variable_defaults);
              // Ensure it's an object
              if (typeof defaults !== 'object' || defaults === null || Array.isArray(defaults)) {
                defaults = {};
              }
            } catch {
              // If parsing fails, start with empty object
              defaults = {};
            }
          }
        }
        setDetectedVariables(variables);
        setVariableDefaults(defaults);
      } catch (error) {
        console.error('Error parsing function data:', error);
        // Set safe defaults
        setDetectedVariables([]);
        setVariableDefaults({});
      }
    }
  }, [editMode, functionData]);

  // Handle external submit trigger
  useEffect(() => {
    if (triggerSubmit) {
      handleSubmit();
    }
  }, [triggerSubmit]);

  // Detect variables in prompt and validate them
  useEffect(() => {
    const variableRegex = /<([^>]+)>/g;
    const matches = [...prompt.matchAll(variableRegex)];
    const variables = [...new Set(matches.map((match) => match[1]))];

    // Validate variable names
    const invalidVariables = variables.filter((variable) => !isVariableNameValid(variable));
    if (invalidVariables.length > 0) {
      setErrors((prev) => ({
        ...prev,
        variables: `Invalid variable names: ${invalidVariables.join(
          ', '
        )}. Variables can only contain letters, numbers, and underscores. Must start with a letter.`,
      }));
    } else {
      setErrors((prev) => ({
        ...prev,
        variables: '',
      }));
    }

    setDetectedVariables(variables);
  }, [prompt]);

  // Update defaults when detected variables change, but preserve existing values
  useEffect(() => {
    if (detectedVariables.length > 0) {
      setVariableDefaults((prevDefaults) => {
        const newDefaults = {};
        detectedVariables.forEach((variable) => {
          // Keep existing value if it exists, otherwise set to empty string
          newDefaults[variable] = prevDefaults[variable] || '';
        });
        return newDefaults;
      });
    }
  }, [detectedVariables]);

  const loadAgents = async () => {
    try {
      const response = await apiAskNudgebee.listAgents({ accountId });
      const agents = response?.data?.data?.ai_list_agents?.data || [];
      setAgentList(agents);
    } catch (error) {
      console.error('Failed to load agents:', error);
    }
  };

  const clearAllFields = () => {
    setFunctionName('');
    setDescription('');
    setPrompt('');
    setDetectedVariables([]);
    setVariableDefaults({});
    setErrors({
      functionName: '',
      description: '',
      prompt: '',
      variables: '',
    });
  };

  const validateForm = () => {
    // Check for duplicate function name - allow same name only if it's the current function being edited
    const isDuplicateName = functionList.some((func) => {
      const funcName = func.name.toLowerCase().trim();
      const currentName = functionName.toLowerCase().trim();

      // In edit mode, allow the same name only if it's the current function
      if (editMode && functionData) {
        return funcName === currentName && func.id !== functionData.id;
      }
      // In create mode, any duplicate is not allowed
      return funcName === currentName;
    });

    const newErrors = {
      functionName: isDuplicateName ? 'A function with this name already exists' : getLlmIdentifierValidationMessage(functionName),
      description:
        description.trim() === '' ? 'Description is required' : description.split(' ').length > 200 ? 'Description should be max 200 words' : '',
      prompt: prompt.trim() === '' ? 'Prompt is required' : '',
      variables: errors.variables, // Keep existing variable validation errors
    };

    setErrors(newErrors);
    return !Object.values(newErrors).some((error) => error !== '');
  };

  const handleSubmit = async () => {
    if (!validateForm()) {
      return;
    }

    if (onSubmitStart) {
      onSubmitStart();
    }

    setLoading(true);
    try {
      const payload = {
        name: functionName.trim(),
        description: description.trim(),
        prompt: prompt.trim(),
        variables: detectedVariables,
        variable_defaults: variableDefaults,
        status: status,
      };

      let response;
      if (editMode) {
        response = await apiAskNudgebee.updateFunction({ accountId: accountId, functionId: functionData.id, data: payload });
      } else {
        response = await apiAskNudgebee.createAiFunction(payload, accountId);
      }

      if (response.success) {
        snackbar.success(editMode ? 'Function updated successfully' : 'Function created successfully');

        // If not in edit mode and not modal, clear all fields and refresh function list
        if (!editMode && !isModal) {
          clearAllFields();
        }

        // If it's a modal, close it and notify parent
        if (isModal && _handleClose) {
          _handleClose('success');
        }
      } else {
        snackbar.error(response.error || 'Unknown error occurred');
      }
    } catch (error) {
      console.error('Error saving function:', error);
      snackbar.error('Failed to save function');
    } finally {
      setLoading(false);
      if (onSubmitEnd) {
        onSubmitEnd();
      }
    }
  };

  const handleValidatePrompt = () => {
    if (prompt.trim() === '') {
      snackbar.error('Please enter a prompt to validate');
      return;
    }

    setShowPromptRewriteModal(true);
  };

  const handlePromptUpdate = (newPrompt) => {
    setPrompt(newPrompt);
    setErrors({ ...errors, prompt: '' });
  };

  // Function to detect agent names in the prompt
  const getDetectedAgents = () => {
    if (!prompt) {
      return [];
    }

    // Find all @AgentName patterns in the prompt
    const agentMatches = prompt.match(/@\w+/g) || [];

    // Remove @ symbol and get unique agent names
    const agentNames = agentMatches.map((match) => match.substring(1));

    // Filter to only include agents that exist in the agent list
    const validAgents = agentNames.filter((agentName) => agentList.some((agent) => agent.name === agentName));

    // Return unique valid agents
    return [...new Set(validAgents)];
  };

  const handleTestPrompt = () => {
    if (prompt.trim() === '') {
      snackbar.error('Please enter a prompt to test');
      return;
    }

    // Check if all variables have default values

    // Generate a random session ID
    const newSessionId = uuidv4();
    setTestSessionId(newSessionId);

    // Open the conversation popup
    setShowConversationPopup(true);
  };

  const handleCloseConversationPopup = () => {
    setShowConversationPopup(false);
    setTestSessionId('');
  };

  const copyToClipboard = (text) => {
    navigator.clipboard.writeText(text);
    snackbar.success('Copied to clipboard');
  };

  const filteredAgents = agentList.filter(
    (agent) => agent.name.toLowerCase().includes(searchAgent.toLowerCase()) || agent.description?.toLowerCase().includes(searchAgent.toLowerCase())
  );

  const filteredFunctions = functionList.filter(
    (func) => func.name.toLowerCase().includes(searchFunction.toLowerCase()) || func.description?.toLowerCase().includes(searchFunction.toLowerCase())
  );

  return (
    <Box>
      <Box sx={{ display: 'flex', flexDirection: 'column', ...(isModal ? {} : { height: '100vh' }) }}>
        {!isModal && (
          <Box sx={{ p: '16px 24px', backgroundColor: colors.background.primaryLightest, borderRadius: '8px', mt: '16px' }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography variant='h5' sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '20px' }}>
                {editMode ? 'Edit LLM Function' : 'Create New LLM Function'}
              </Typography>
              <CustomButton text='Show Existing Prompt Functions' variant='secondary' size='Medium' onClick={() => setShowFunctionList(true)} />
            </Box>

            <Typography sx={{ ...styles.instructionText }}>
              {editMode
                ? 'Modify your existing prompt-based function with dynamic variables and agent integrations. You can update:'
                : 'Create custom prompt-based functions with dynamic variables and integrate existing agents. You can:'}
              <br />
              • Define variables with default values for flexible prompts
              <br />• Reference existing agents and functions within your prompt
            </Typography>
          </Box>
        )}

        <Box
          sx={{
            p: isModal ? 2 : 3,
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            marginBottom: isModal ? '0px' : '20px',
            ...(isModal
              ? {
                  maxHeight: '70vh',
                  overflowY: 'auto',
                  overflowX: 'hidden',
                }
              : {}),
          }}
        >
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: isModal ? 3 : 4, flex: 1 }}>
            {/* Function Name */}
            <Box>
              <Typography sx={styles.label}>
                Function Name <span style={styles.requiredStar}>*</span>
              </Typography>
              <Typography sx={styles.instructionText}>
                Use lowercase letters, digits, and underscores (3-50 characters). Must start with a letter and end with a letter or digit.
              </Typography>
              <TextField
                value={functionName}
                onChange={(e) => {
                  const newName = e.target.value;
                  setFunctionName(newName);

                  // Real-time validation for duplicate names and format
                  const isDuplicateName = functionList.some((func) => {
                    const funcName = func.name.toLowerCase().trim();
                    const currentName = newName.toLowerCase().trim();

                    // In edit mode, allow the same name only if it's the current function
                    if (editMode && functionData) {
                      return funcName === currentName && func.id !== functionData.id;
                    }
                    // In create mode, any duplicate is not allowed
                    return funcName === currentName;
                  });

                  const nameError = isDuplicateName ? 'A function with this name already exists' : getLlmIdentifierValidationMessage(newName);

                  setErrors({ ...errors, functionName: nameError });
                }}
                variant='outlined'
                placeholder='e.g., get_user_data, process_payment'
                sx={{
                  ...styles.inputField,
                  width: '40%',
                }}
                error={!!errors.functionName}
              />
              {errors.functionName && <Typography sx={styles.errorText}>{errors.functionName}</Typography>}
            </Box>

            <Box>
              <Typography sx={styles.label}>
                Status <span style={styles.requiredStar}>*</span>
              </Typography>
              <Typography sx={styles.instructionText}>Set the function status - Draft functions can be tested but not used in production</Typography>
              <FormControl sx={{ width: '100px' }}>
                <Select
                  value={status}
                  onChange={(e) => setStatus(e.target.value)}
                  displayEmpty
                  sx={{
                    ...styles.inputField,
                    '& .MuiSelect-select': {
                      padding: '8px 14px',
                    },
                  }}
                >
                  <MenuItem value='active'>
                    <Box sx={{ justifyContent: 'center' }} />
                    <CustomLabels text={'Active'} size='small' />
                  </MenuItem>
                  <MenuItem value='draft'>
                    <CustomLabels text={'Draft'} size='small' />
                  </MenuItem>
                </Select>
              </FormControl>
            </Box>

            {/* Brief Description */}
            <Box>
              <Typography sx={styles.label}>
                Description <span style={styles.requiredStar}>*</span>
              </Typography>
              <Typography sx={styles.instructionText}>Describe what this function does and its expected behavior (maximum 200 words)</Typography>
              <TextField
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                  setErrors({ ...errors, description: '' });
                }}
                variant='outlined'
                fullWidth
                multiline
                rows={3}
                placeholder='What is this function supposed to do?'
                sx={{
                  ...styles.inputField,
                }}
                error={!!errors.description}
              />
              {errors.description && <Typography sx={styles.errorText}>{errors.description}</Typography>}
            </Box>

            {/* Enter Prompt */}
            <Box>
              <Typography sx={styles.label}>
                Enter your prompt here <span style={styles.requiredStar}>*</span>
              </Typography>
              <Box sx={{ mb: 2 }}>
                <Typography sx={{ ...styles.instructionText, marginBottom: '0px' }}>
                  • use{' '}
                  <Box component='span' sx={{ fontWeight: 500 }}>
                    &lt;Variable_Name&gt;
                  </Box>{' '}
                  format for introducing Variables. Variable names can only contain letters, numbers, and underscores (no hyphens or spaces).
                </Typography>
                <Typography sx={styles.instructionText}>
                  • use <Box component='span' sx={{ fontWeight: 500 }} /> to use NudgeBee existing Agents in your prompt.
                </Typography>
                <Box sx={{ display: 'flex', gap: 2, mt: 1 }}>
                  <CustomButton text='View Agent List' variant='secondary' size='xSmall' onClick={() => setShowAgentList(true)} />
                </Box>
              </Box>
              <TextField
                value={prompt}
                onChange={(e) => {
                  setPrompt(e.target.value);
                  setErrors({ ...errors, prompt: '' });
                }}
                multiline
                rows={isModal ? 12 : 20}
                placeholder='Write your prompt here...'
                variant='outlined'
                fullWidth
                error={!!errors.prompt}
                sx={{
                  ...styles.inputField,
                  '& .MuiInputBase-inputMultiline': {
                    padding: '0px !important',
                    fontSize: '14px',
                    maxHeight: isModal ? '200px' : '280px',
                    overflowY: 'auto',
                    '&::placeholder': {
                      color: colors.text.tertiary,
                      opacity: 0.4,
                      fontSize: '14px',
                    },
                  },
                }}
              />
              {errors.prompt && <Typography sx={styles.errorText}>{errors.prompt}</Typography>}
              {errors.variables && <Typography sx={styles.errorText}>{errors.variables}</Typography>}
              <Typography sx={{ ...styles.instructionText, mt: 1 }}>
                Use rewrite to get AI-powered improvements and suggestions for enhancing your prompt structure and effectiveness.
              </Typography>

              {/* Prompt Action Buttons */}
              <Box sx={{ display: 'flex', gap: 2, mt: 2 }}>
                <CustomButton text='Rewrite Prompt' variant='secondary' size='Medium' onClick={handleValidatePrompt} loading={loading} />
                <CustomButton text='Test Prompt with AI' variant='secondary' size='Medium' onClick={handleTestPrompt} loading={loading} disabled />
              </Box>

              {/* Visual indicators for detected variables and agents */}
              {(detectedVariables.length > 0 || getDetectedAgents().length > 0) && (
                <Box sx={{ mt: 1, display: 'flex', flexWrap: 'wrap', gap: 1 }}>
                  {detectedVariables.map((variable, index) => {
                    const isValidVariable = isVariableNameValid(variable);
                    return (
                      <Box
                        key={`var-${index}`}
                        sx={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          backgroundColor: isValidVariable ? colors.background.codeMirror : colors.background.medium,
                          color: isValidVariable ? colors.darkPrimary : colors.error,
                          padding: '2px 6px',
                          borderRadius: '12px',
                          fontSize: '12px',
                          fontWeight: 500,
                          border: isValidVariable ? 'none' : `1px solid ${colors.background.errorLight}`,
                        }}
                      >
                        &lt;{variable}&gt;
                      </Box>
                    );
                  })}
                  {getDetectedAgents().map((agent, index) => (
                    <Box
                      key={`agent-${index}`}
                      sx={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        backgroundColor: colors.background.codeMirror,
                        color: colors.success,
                        padding: '2px 6px',
                        borderRadius: '12px',
                        fontSize: '12px',
                        fontWeight: 500,
                      }}
                    >
                      @{agent}
                    </Box>
                  ))}
                </Box>
              )}
            </Box>

            {/* Variables Table */}
            {detectedVariables.length > 0 && (
              <Box>
                <Typography sx={styles.label}>Variable Default Values</Typography>
                <Typography sx={styles.instructionText}>
                  Set default values for variables detected in your prompt. These will be used when no specific values are provided.
                </Typography>
                <TableContainer component={Paper} sx={{ mt: 2 }}>
                  <Table sx={styles.variableTable}>
                    <TableHead>
                      <TableRow>
                        <TableCell>Variable Name</TableCell>
                        <TableCell>Default Value</TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {detectedVariables.map((variable, index) => (
                        <TableRow key={index}>
                          <TableCell>${variable}</TableCell>
                          <TableCell>
                            <TextField
                              value={variableDefaults[variable] || ''}
                              onChange={(e) => {
                                setVariableDefaults({
                                  ...variableDefaults,
                                  [variable]: e.target.value,
                                });
                              }}
                              variant='outlined'
                              fullWidth
                              size='small'
                              placeholder={`Default value for ${variable}`}
                              sx={{
                                ...styles.inputField,
                              }}
                            />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </TableContainer>
              </Box>
            )}
          </Box>
        </Box>
      </Box>

      {/* Agent List Modal */}
      <Modal
        width='md'
        title='Available Agents'
        open={showAgentList}
        handleClose={() => setShowAgentList(false)}
        onClose={() => setShowAgentList(false)}
      >
        <Box sx={{ p: 1.5 }}>
          <TextField
            value={searchAgent}
            onChange={(e) => setSearchAgent(e.target.value)}
            variant='outlined'
            fullWidth
            placeholder='Search agents...'
            size='small'
            sx={{ ...styles.inputField, mb: 1.5 }}
          />
          <Box sx={{ maxHeight: '400px', overflowY: 'auto' }}>
            {filteredAgents.map((agent, index) => (
              <Box key={index} sx={{ p: 2, borderBottom: `1px solid ${colors.border.vertical}` }}>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16 }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Typography variant='subtitle2' sx={{ fontWeight: 500, fontSize: '14px', lineHeight: 1.2, color: colors.text.secondary }}>
                      {agent.name}
                    </Typography>
                    <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', lineHeight: 1.3, mt: 0.5 }}>{agent.description}</Typography>
                  </Box>
                  <CustomButton text='Copy' variant='secondary' size='xSmall' onClick={() => copyToClipboard(`@${agent.name}`)} />
                </Box>
              </Box>
            ))}
          </Box>
        </Box>
      </Modal>

      {/* Function List Modal */}
      <Modal
        width='md'
        title='Existing Prompt Functions'
        open={showFunctionList}
        handleClose={() => setShowFunctionList(false)}
        onClose={() => setShowFunctionList(false)}
      >
        <Box sx={{ p: 1.5 }}>
          <TextField
            value={searchFunction}
            onChange={(e) => setSearchFunction(e.target.value)}
            variant='outlined'
            fullWidth
            placeholder='Search functions...'
            size='small'
            sx={{ ...styles.inputField, mb: 1.5 }}
          />
          <Box sx={{ maxHeight: '350px', overflowY: 'auto' }}>
            {filteredFunctions.map((func, index) => (
              <Box key={index} sx={{ p: 1.5, border: `1px solid ${colors.border.vertical}`, borderRadius: '6px', mb: 1 }}>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 1 }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Typography variant='subtitle2' sx={{ fontWeight: 600, fontSize: '14px', lineHeight: 1.2 }}>
                      {func.name}
                    </Typography>
                    <Typography sx={{ color: colors.text.secondary, fontSize: '12px', lineHeight: 1.3, mt: 0.5 }}>{func.description}</Typography>
                  </Box>
                </Box>
              </Box>
            ))}
          </Box>
        </Box>
      </Modal>

      {/* Prompt Rewrite Modal */}
      <PromptRewriteModal
        open={showPromptRewriteModal}
        onClose={() => setShowPromptRewriteModal(false)}
        functionName={functionName}
        currentPrompt={prompt}
        onPromptUpdate={handlePromptUpdate}
        accountId={accountId}
      />

      {/* Test Prompt Conversation Popup */}
      <ConversationPopup
        open={showConversationPopup}
        handleClose={handleCloseConversationPopup}
        query={prompt}
        sessionId={testSessionId}
        accountId={accountId}
        source='prompt_test'
        variableNames={detectedVariables}
        variableDefaults={variableDefaults}
      />
    </Box>
  );
};

CreateFunction.propTypes = {
  accountId: PropTypes.string,
  _handleClose: PropTypes.func,
  editMode: PropTypes.bool,
  functionData: PropTypes.object,
  triggerSubmit: PropTypes.bool,
  onSubmitStart: PropTypes.func,
  onSubmitEnd: PropTypes.func,
  isModal: PropTypes.bool,
};

export default CreateFunction;
