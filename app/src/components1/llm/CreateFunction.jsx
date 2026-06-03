import { Box, Typography } from '@mui/material';
import SearchIcon from '@mui/icons-material/Search';
import React, { useState, useEffect } from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import CustomTable from '@common-new/tables/CustomTable2';
import { toast as snackbar } from '@components1/ds/Toast';
import { Modal } from '@components1/ds/Modal';
import { ds } from '@utils/colors';
import { isVariableNameValid, getLlmIdentifierValidationMessage } from 'src/utils/common';
import PromptRewriteModal from './PromptRewriteModal';
import ConversationPopup from './ConversationPopup';
import { v4 as uuidv4 } from 'uuid';

const styles = {
  instructionText: {
    fontSize: 'var(--ds-text-small)',
    color: 'var(--ds-gray-500)',
    mb: 'var(--ds-space-2)',
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

  useEffect(() => {
    loadAgents();
  }, [accountId]);

  useEffect(() => {
    if (editMode && functionData) {
      try {
        let variables = [];
        if (functionData.variables) {
          if (Array.isArray(functionData.variables)) {
            variables = functionData.variables;
          } else if (typeof functionData.variables === 'string') {
            try {
              variables = JSON.parse(functionData.variables);
              if (!Array.isArray(variables)) {
                variables = [];
              }
            } catch {
              variables = functionData.variables
                .split(',')
                .map((v) => v.trim())
                .filter((v) => v.length > 0);
            }
          }
        }

        let defaults = {};
        if (functionData.variable_defaults) {
          if (
            typeof functionData.variable_defaults === 'object' &&
            functionData.variable_defaults !== null &&
            !Array.isArray(functionData.variable_defaults)
          ) {
            defaults = functionData.variable_defaults;
          } else if (typeof functionData.variable_defaults === 'string') {
            try {
              defaults = JSON.parse(functionData.variable_defaults);
              if (typeof defaults !== 'object' || defaults === null || Array.isArray(defaults)) {
                defaults = {};
              }
            } catch {
              defaults = {};
            }
          }
        }
        setDetectedVariables(variables);
        setVariableDefaults(defaults);
      } catch (error) {
        console.error('Error parsing function data:', error);
        setDetectedVariables([]);
        setVariableDefaults({});
      }
    }
  }, [editMode, functionData]);

  useEffect(() => {
    if (triggerSubmit) {
      handleSubmit();
    }
  }, [triggerSubmit]);

  useEffect(() => {
    const variableRegex = /<([^>]+)>/g;
    const matches = [...prompt.matchAll(variableRegex)];
    const variables = [...new Set(matches.map((match) => match[1]))];

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

  useEffect(() => {
    if (detectedVariables.length > 0) {
      setVariableDefaults((prevDefaults) => {
        const newDefaults = {};
        detectedVariables.forEach((variable) => {
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
    const isDuplicateName = functionList.some((func) => {
      const funcName = func.name.toLowerCase().trim();
      const currentName = functionName.toLowerCase().trim();
      if (editMode && functionData) {
        return funcName === currentName && func.id !== functionData.id;
      }
      return funcName === currentName;
    });

    const newErrors = {
      functionName: isDuplicateName ? 'A function with this name already exists' : getLlmIdentifierValidationMessage(functionName),
      description:
        description.trim() === '' ? 'Description is required' : description.split(' ').length > 200 ? 'Description should be max 200 words' : '',
      prompt: prompt.trim() === '' ? 'Prompt is required' : '',
      variables: errors.variables,
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

        if (!editMode && !isModal) {
          clearAllFields();
        }

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

  const getDetectedAgents = () => {
    if (!prompt) {
      return [];
    }
    const agentMatches = prompt.match(/@\w+/g) || [];
    const agentNames = agentMatches.map((match) => match.substring(1));
    const validAgents = agentNames.filter((agentName) => agentList.some((agent) => agent.name === agentName));
    return [...new Set(validAgents)];
  };

  const handleTestPrompt = () => {
    if (prompt.trim() === '') {
      snackbar.error('Please enter a prompt to test');
      return;
    }
    const newSessionId = uuidv4();
    setTestSessionId(newSessionId);
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
          <Box sx={{ p: `${ds.space[4]} ${ds.space[5]}`, backgroundColor: 'var(--ds-blue-100)', borderRadius: ds.radius.lg, mt: ds.space[4] }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography
                variant='h5'
                sx={{
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-gray-700)',
                  fontSize: 'var(--ds-text-heading)',
                  fontFamily: 'var(--ds-font-display)',
                }}
              >
                {editMode ? 'Edit LLM Function' : 'Create New LLM Function'}
              </Typography>
              <Button tone='secondary' size='md' onClick={() => setShowFunctionList(true)}>
                Show Existing Prompt Functions
              </Button>
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
            p: isModal ? 0 : ds.space[5],
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            marginBottom: isModal ? 0 : ds.space.mul(1, 5),
            ...(isModal
              ? {
                  maxHeight: '70vh',
                  overflowY: 'auto',
                  overflowX: 'hidden',
                }
              : {}),
          }}
        >
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: isModal ? ds.space[5] : ds.space[6], flex: 1 }}>
            {/* Function Name */}
            <Box sx={{ width: '40%' }}>
              <Input
                label='Function Name'
                required
                instructionText='Use lowercase letters, digits, and underscores (3-50 characters). Must start with a letter and end with a letter or digit.'
                value={functionName}
                onChange={(next) => {
                  setFunctionName(next);
                  const isDuplicateName = functionList.some((func) => {
                    const funcName = func.name.toLowerCase().trim();
                    const currentName = next.toLowerCase().trim();
                    if (editMode && functionData) {
                      return funcName === currentName && func.id !== functionData.id;
                    }
                    return funcName === currentName;
                  });
                  const nameError = isDuplicateName ? 'A function with this name already exists' : getLlmIdentifierValidationMessage(next);
                  setErrors({ ...errors, functionName: nameError });
                }}
                placeholder='e.g., get_user_data, process_payment'
                error={errors.functionName || undefined}
              />
            </Box>

            {/* Status */}
            <Box>
              <Box sx={{ width: ds.space.mul(0, 120) }}>
                <Select
                  id='function-status'
                  label='Status'
                  value={status}
                  onChange={(v) => setStatus(v)}
                  options={[
                    { value: 'active', label: 'Active' },
                    { value: 'draft', label: 'Draft' },
                  ]}
                  size='sm'
                />
              </Box>
              <Typography sx={{ ...styles.instructionText, mt: ds.space[2] }}>
                Set the function status — Draft functions can be tested but not used in production.
              </Typography>
            </Box>

            {/* Description */}
            <Input
              label='Description'
              required
              instructionText='Describe what this function does and its expected behavior (maximum 200 words)'
              type='textarea'
              rows={3}
              value={description}
              onChange={(next) => {
                setDescription(next);
                setErrors({ ...errors, description: '' });
              }}
              placeholder='What is this function supposed to do?'
              error={errors.description || undefined}
            />

            {/* Enter Prompt */}
            <Box>
              <Input
                label='Enter your prompt here'
                required
                type='textarea'
                rows={isModal ? 12 : 20}
                value={prompt}
                onChange={(next) => {
                  setPrompt(next);
                  setErrors({ ...errors, prompt: '' });
                }}
                placeholder='Write your prompt here...'
                error={errors.prompt || errors.variables || undefined}
              />
              <Box sx={{ mt: ds.space[2] }}>
                <Typography sx={{ ...styles.instructionText, marginBottom: 0 }}>
                  • use{' '}
                  <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                    &lt;Variable_Name&gt;
                  </Box>{' '}
                  format for introducing Variables. Variable names can only contain letters, numbers, and underscores (no hyphens or spaces).
                </Typography>
                <Typography sx={styles.instructionText}>
                  • use{' '}
                  <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                    @AgentName
                  </Box>{' '}
                  to reference NudgeBee Agents in your prompt.
                </Typography>
                <Box sx={{ display: 'flex', gap: ds.space[4], mt: ds.space[2] }}>
                  <Button tone='secondary' size='xs' onClick={() => setShowAgentList(true)}>
                    View Agent List
                  </Button>
                </Box>
              </Box>
              <Typography sx={{ ...styles.instructionText, mt: ds.space[2] }}>
                Use rewrite to get AI-powered improvements and suggestions for enhancing your prompt structure and effectiveness.
              </Typography>

              {/* Prompt Action Buttons */}
              <Box sx={{ display: 'flex', gap: ds.space[4], mt: ds.space[4] }}>
                <Button tone='secondary' size='md' onClick={handleValidatePrompt} loading={loading}>
                  Rewrite Prompt
                </Button>
                <Button tone='secondary' size='md' onClick={handleTestPrompt} loading={loading} disabled>
                  Test Prompt with AI
                </Button>
              </Box>

              {/* Visual indicators for detected variables and agents */}
              {(detectedVariables.length > 0 || getDetectedAgents().length > 0) && (
                <Box sx={{ mt: ds.space[2], display: 'flex', flexWrap: 'wrap', gap: ds.space[2] }}>
                  {detectedVariables.map((variable, index) => {
                    const isValidVariable = isVariableNameValid(variable);
                    return (
                      <Box
                        key={`var-${index}`}
                        sx={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          backgroundColor: isValidVariable ? 'var(--ds-background-200)' : 'var(--ds-red-100)',
                          color: isValidVariable ? 'var(--ds-blue-700)' : 'var(--ds-red-600)',
                          padding: `${ds.space[0]} ${ds.space.mul(0, 3)}`,
                          borderRadius: ds.radius.xl,
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          border: isValidVariable ? 'none' : `1px solid ${'var(--ds-red-100)'}`,
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
                        backgroundColor: 'var(--ds-background-200)',
                        color: 'var(--ds-green-600)',
                        padding: `${ds.space[0]} ${ds.space.mul(0, 3)}`,
                        borderRadius: ds.radius.xl,
                        fontSize: 'var(--ds-text-small)',
                        fontWeight: 'var(--ds-font-weight-medium)',
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
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-700)',
                    fontFamily: 'var(--ds-font-display)',
                  }}
                >
                  Variable Default Values
                </Typography>
                <Typography sx={styles.instructionText}>
                  Set default values for variables detected in your prompt. These will be used when no specific values are provided.
                </Typography>
                <Box sx={{ mt: ds.space[4] }}>
                  <CustomTable
                    id='variable-defaults'
                    headers={[
                      { name: 'Variable Name', width: '40%' },
                      { name: 'Default Value', width: '60%' },
                    ]}
                    tableData={detectedVariables.map((variable) => [
                      { text: `$${variable}` },
                      {
                        component: (
                          <Input
                            size='sm'
                            value={variableDefaults[variable] || ''}
                            onChange={(next) => {
                              setVariableDefaults({
                                ...variableDefaults,
                                [variable]: next,
                              });
                            }}
                            placeholder={`Default value for ${variable}`}
                          />
                        ),
                      },
                    ])}
                    rowsPerPage={detectedVariables.length}
                    totalRows={detectedVariables.length}
                    loading={false}
                  />
                </Box>
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
        <Box sx={{ p: ds.space[3] }}>
          <Box sx={{ mb: ds.space[3] }}>
            <Input
              size='sm'
              value={searchAgent}
              onChange={(next) => setSearchAgent(next)}
              placeholder='Search agents...'
              leadingIcon={<SearchIcon fontSize='small' />}
            />
          </Box>
          <Box sx={{ maxHeight: ds.space.mul(0, 200), overflowY: 'auto' }}>
            {filteredAgents.map((agent, index) => (
              <Box key={index} sx={{ p: ds.space[4], borderBottom: `1px solid var(--ds-gray-200)` }}>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16 }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Typography
                      variant='subtitle2'
                      sx={{
                        fontWeight: 'var(--ds-font-weight-medium)',
                        fontSize: 'var(--ds-text-body-lg)',
                        lineHeight: 1.2,
                        color: 'var(--ds-gray-700)',
                      }}
                    >
                      {agent.name}
                    </Typography>
                    <Typography sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', lineHeight: 1.3, mt: ds.space[1] }}>
                      {agent.description}
                    </Typography>
                  </Box>
                  <Button tone='secondary' size='xs' onClick={() => copyToClipboard(`@${agent.name}`)}>
                    Copy
                  </Button>
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
        <Box sx={{ p: ds.space[3] }}>
          <Box sx={{ mb: ds.space[3] }}>
            <Input
              size='sm'
              value={searchFunction}
              onChange={(next) => setSearchFunction(next)}
              placeholder='Search functions...'
              leadingIcon={<SearchIcon fontSize='small' />}
            />
          </Box>
          <Box sx={{ maxHeight: ds.space.mul(0, 175), overflowY: 'auto' }}>
            {filteredFunctions.map((func, index) => (
              <Box key={index} sx={{ p: ds.space[3], border: `1px solid var(--ds-gray-200)`, borderRadius: ds.radius.md, mb: ds.space[2] }}>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: ds.space[2] }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Typography
                      variant='subtitle2'
                      sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body-lg)', lineHeight: 1.2 }}
                    >
                      {func.name}
                    </Typography>
                    <Typography sx={{ color: 'var(--ds-gray-700)', fontSize: 'var(--ds-text-small)', lineHeight: 1.3, mt: ds.space[1] }}>
                      {func.description}
                    </Typography>
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
