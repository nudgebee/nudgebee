import CustomButton from '@components1/common/NewCustomButton';
import { Textarea } from '@components1/k8s/common/TextArea';
import { Box, TextField, Typography } from '@mui/material';
import CustomDropdown from '@components1/common/CustomDropdown';
import React, { useState } from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import { parseHttpResponseBodyMessage, getLlmIdentifierValidationMessage, getToolLabel } from 'src/utils/common';
import CustomAutocomplete from '@components1/common/CustomAutocomplete';

const styles = {
  label: {
    mb: 1,
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
    },
  },
  requiredField: {
    '& .MuiOutlinedInput-root': {
      '& fieldset': {
        borderColor: colors.border.error,
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
  },
};

const CreateAgent = ({ accountId, handleClose, allAgents, editMode, customizeMode, agentData }) => {
  const [description, setDescription] = React.useState('');
  const [name, setName] = React.useState('');
  const [instructions, setInstructions] = useState('');
  const [constraints, setConstraints] = useState('');
  const [toolUsage, setToolUsage] = useState('');
  const [examples, setExamples] = useState('');
  const [role, setRole] = useState('');
  const [status, setStatus] = useState(agentData?.status || '');

  const [toolOptions, setToolOptions] = React.useState([]);
  const [tools, setTools] = React.useState([]);
  const [loading, setLoading] = React.useState(false);
  const [errors, setErrors] = React.useState({
    name: '',
    description: '',
    role: '',
    tools: '',
    instructions: '',
    constraints: '',
    toolUsage: '',
    status: '',
  });
  const [ragData, setRagData] = useState([]);
  const [hasSubmitted, setHasSubmitted] = useState(false); // Track if form has been submitted

  React.useEffect(() => {
    setToolOptions([]);
    apiAskNudgebee.listTools({ accountId }).then((res) => {
      const listToolsResponse = res.data?.data?.ai_list_tools?.data ?? [];
      if (listToolsResponse.length > 0) {
        const GROUP_ORDER = { tool: 0, agent: 1, other: 2 };
        setToolOptions(
          listToolsResponse
            .map((g) => ({
              label: getToolLabel(g),
              value: g.name,
              group: g.nb_tool_type?.toLowerCase() || 'Other',
            }))
            .sort((a, b) => {
              const groupA = GROUP_ORDER[a.group] ?? 99;
              const groupB = GROUP_ORDER[b.group] ?? 99;

              if (groupA !== groupB) {
                return groupA - groupB;
              }
              return a.label.localeCompare(b.label); // sort within the group
            })
        );
      }
    });

    if (editMode && agentData) {
      setName(agentData.name);
      setDescription(agentData.description);
      setTools(agentData.tools || []);
      setRagData(agentData.rags || []);

      if (agentData.system_prompt) {
        try {
          const systemPrompt = JSON.parse(agentData.system_prompt);
          setRole(systemPrompt.role || '');
          setInstructions(systemPrompt.instructions?.join('\n') || '');
          setConstraints(systemPrompt.constraints?.join('\n') || '');
          setToolUsage(systemPrompt.tool_usage?.toolUsage || '');
          if (typeof systemPrompt.examples === 'string') {
            setExamples(systemPrompt.examples || '');
          } else {
            setExamples(JSON.stringify(systemPrompt.examples) || '');
          }
        } catch (error) {
          setInstructions(agentData.system_prompt);
          console.error('Error parsing system_prompt:', error);
          // Optionally, set default values or show an error message
        }
      }
    }

    if (customizeMode && agentData) {
      // For customization, pre-fill the name and description but leave other fields empty for user customization
      setName(agentData.name);
      setDescription(agentData.description);
      setTools([]);
      setRagData([]);
      setRole('');
      setInstructions('');
      setConstraints('');
      setToolUsage('');
      setExamples('');
    }
  }, [accountId, editMode, customizeMode, agentData]);

  const validateForm = () => {
    const newErrors = {
      name: nameValidation(name),
      description: descriptionValidation(description),
      instructions: instructionsValidation(instructions),
      status: editMode ? statusValidation(status) : '',
    };
    setErrors(newErrors);

    return !(newErrors.name || newErrors.description || newErrors.instructions || (editMode && newErrors.status));
  };

  const descriptionValidation = (description) => {
    return !description.trim() ? 'Description cannot be empty.' : '';
  };

  const instructionsValidation = (instructions) => {
    return !instructions.trim() ? 'Instructions cannot be empty.' : '';
  };

  const statusValidation = (status) => {
    return !status || status.trim() === '' || status.trim() === ' ' ? 'Status is required.' : '';
  };

  const nameValidation = (name) => {
    if ((editMode || customizeMode) && agentData && agentData.name.toLowerCase() === name.toLowerCase()) {
      return ''; // Allow current name in edit mode or customize mode
    }

    if (name && allAgents.some((existingName) => existingName.toLowerCase() === name.toLowerCase())) {
      return 'Agent name already exists';
    }

    // Use the new identifier validation
    const validationMessage = getLlmIdentifierValidationMessage(name);
    return validationMessage;
  };

  const getFieldStyle = (fieldName, _isRequired = false) => {
    const realTimeValidationFields = ['name'];
    const hasError = realTimeValidationFields.includes(fieldName) ? !!errors[fieldName] : hasSubmitted && !!errors[fieldName];

    return {
      ...styles.inputField,
      ...(hasError ? styles.requiredField : {}),
    };
  };

  const clearFieldError = (fieldName) => {
    if (errors[fieldName]) {
      setErrors({ ...errors, [fieldName]: '' });
    }
  };

  const handleSubmit = () => {
    setHasSubmitted(true);
    const isValid = validateForm();

    if (!isValid) {
      let errorMessage = 'Please fill the following fields:';
      if (errors.name) {
        errorMessage += `\n- ${errors.name}`;
      }
      if (errors.description) {
        errorMessage += `\n- ${errors.description}`;
      }
      if (errors.instructions) {
        errorMessage += `\n- ${errors.instructions}`;
      }
      if (editMode && errors.status) {
        errorMessage += `\n- ${errors.status}`;
      }

      snackbar.error(errorMessage);
      return;
    }
    setLoading(true);

    const agentPayload = {
      description: description,
      name: name,
      system_prompt: JSON.stringify({
        role: role,
        instructions: [instructions.trim()],
        constraints: [constraints.trim()],
        tool_usage: {
          toolUsage,
        },
        output_format: 'Markdown',
        examples: examples?.trim() ?? '',
      }),
      system_prompt_variables: [],
      tools: tools.map((f) => f?.value || f),
      config: {}, // Assuming config is not editable for now
      // Only include rags when creating a new agent, not when updating
      ...((!editMode || customizeMode) && { rags: ragData }),
    };

    if (editMode && !customizeMode) {
      apiAskNudgebee
        .updateAgent({
          account_id: accountId,
          agent: {
            status: status,
            id: agentData.id, // Include agent ID for update
            ...agentPayload,
          },
        })
        .then((res) => {
          const errors = res?.data?.errors || [];
          setLoading(false);
          if (errors.length > 0) {
            const errorMessage = `Failed to update Agent - ${parseHttpResponseBodyMessage(res?.data)}`;
            snackbar.error(errorMessage);
            return;
          }
          snackbar.success('Agent updated successfully');
          handleClose('success');
        })
        .catch((error) => {
          setLoading(false);
          snackbar.error('An error occurred while updating the agent');
          console.error('Error updating agent:', error);
        });
    } else {
      apiAskNudgebee
        .createAgent({
          account_id: accountId,
          agent: agentPayload,
          override_agent: customizeMode ? true : false,
        })
        .then((res) => {
          const errors = res?.data?.errors || [];
          setLoading(false);
          if (errors.length > 0) {
            const errorMessage = `Failed to create Agent - ${parseHttpResponseBodyMessage(res?.data)}`;
            snackbar.error(errorMessage);
            return;
          }
          snackbar.success(customizeMode ? 'Custom agent created successfully. It will override the original agent.' : 'Agent created successfully');
          handleClose('success');
        })
        .catch((error) => {
          setLoading(false);
          snackbar.error('An error occurred while creating the agent');
          console.error('Error creating agent:', error);
        });
    }
  };

  const handleFileChange = (e) => {
    const file = e.target.files[0];
    if (file) {
      const reader = new FileReader();
      reader.onload = (event) => {
        const csvData = event.target.result;
        const parseCSV = (csv) => {
          const lines = csv.split('\n');
          const result = [];
          const headers = lines[0].split(',');

          for (let i = 1; i < lines.length; i++) {
            const obj = {};
            const currentline = lines[i].split(',');

            for (let j = 0; j < headers.length; j++) {
              obj[headers[j]] = currentline[j];
            }
            result.push(obj);
          }
          return result;
        };
        const jsonData = parseCSV(csvData);
        setRagData((prevRagData) => [...prevRagData, { data: jsonData, format: 'json', filename: file.name }]);
      };
      reader.readAsText(file);
    }
  };

  return (
    <Box p={5}>
      <Box display='flex' flexDirection='column' width='100%' mb={3} gap='20px'>
        <Box>
          {agentData?.overridden && (
            <Box
              sx={{
                backgroundColor: '#fff3cd',
                border: '1px solid #ffeaa7',
                borderRadius: '6px',
                padding: '8px 12px',
                mb: 2,
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
              }}
            >
              <Typography
                sx={{
                  fontSize: '12px',
                  color: '#856404',
                  fontWeight: 500,
                }}
              >
                ⚠️ This system agent&apos;s prompt has been overridden and will use custom instructions instead of the default system prompt.
              </Typography>
            </Box>
          )}
          <Typography sx={styles.label}>
            Name <span style={styles.requiredStar}>*</span>
          </Typography>
          {customizeMode && (
            <Typography sx={{ fontSize: '12px', color: colors.text.secondary, mb: 1 }}>
              Creating a custom agent with the same name will override the original agent&apos;s behavior
            </Typography>
          )}
          <TextField
            value={name}
            required
            onChange={(e) => {
              if (!customizeMode) {
                setName(e.target.value);
                setErrors({ ...errors, name: nameValidation(e.target.value) });
              }
            }}
            variant='outlined'
            fullWidth
            placeholder='Enter agent name'
            sx={getFieldStyle('name', true)}
            error={!!errors.name}
            disabled={customizeMode}
          />
          {errors.name && <Typography sx={styles.errorText}>{errors.name}</Typography>}
        </Box>

        <Box>
          <Typography sx={styles.label}>
            Description <span style={styles.requiredStar}>*</span>
          </Typography>
          <TextField
            value={description}
            required
            onChange={(e) => {
              setDescription(e.target.value);
              clearFieldError('description');
            }}
            variant='outlined'
            fullWidth
            placeholder='Describe what this agent does'
            sx={{
              ...getFieldStyle('description', true),
              '& .MuiInputBase-root': {
                padding: 0,
              },
            }}
            error={hasSubmitted && !!errors.description}
            multiline
            rows={3}
          />
          {hasSubmitted && errors.description && <Typography sx={styles.errorText}>{errors.description}</Typography>}
        </Box>

        <Box>
          <Typography sx={styles.label}>Role</Typography>
          <Textarea
            value={role}
            onChange={(e) => {
              setRole(e.target.value);
              clearFieldError('role');
            }}
            placeholder={`You are a [role], responsible for [specific task].`}
            width='100%'
            minRows={1}
            maxRows={2}
            maxLength={1000}
            disabled={loading}
            sx={getFieldStyle('role')}
          />
          {hasSubmitted && errors.role && <Typography sx={styles.errorText}>{errors.role}</Typography>}
        </Box>
        <Box
          sx={{
            textarea: {
              border: hasSubmitted && errors.instructions && `1px solid ${colors.border.error}`,
            },
          }}
        >
          <Typography sx={styles.label}>
            Instructions <span style={styles.requiredStar}>*</span>
          </Typography>

          <Textarea
            value={instructions}
            required
            onChange={(e) => {
              setInstructions(e.target.value);
              clearFieldError('instructions');
            }}
            placeholder={`Key responsibilities:
1. [Primary responsibility]
2. [Secondary responsibility]
3. [Additional responsibility]

Follow these guidelines for accurate responses.`}
            width='100%'
            minRows={4}
            maxRows={8}
            maxLength={10000}
            disabled={loading}
            sx={getFieldStyle('instructions', true)}
            error={hasSubmitted && !!errors.instructions}
          />
          {hasSubmitted && errors.instructions && <Typography sx={styles.errorText}>{errors.instructions}</Typography>}
        </Box>

        <Box>
          <Typography sx={styles.label}>Constraints</Typography>
          <Textarea
            value={constraints}
            onChange={(e) => {
              setConstraints(e.target.value);
              clearFieldError('constraints');
            }}
            placeholder={`1. Always use appropriate tools for interactions
2. Do not expose sensitive information
3. Format responses in markdown
4. Ask for clarification when needed`}
            width='100%'
            minRows={2}
            maxRows={5}
            maxLength={5000}
            disabled={loading}
            sx={getFieldStyle('constraints')}
          />
          {hasSubmitted && errors.constraints && <Typography sx={styles.errorText}>{errors.constraints}</Typography>}
        </Box>

        <Box>
          <Typography sx={styles.label}>Tool Usage</Typography>
          <Textarea
            value={toolUsage}
            onChange={(e) => {
              setToolUsage(e.target.value);
              clearFieldError('toolUsage');
            }}
            placeholder={`Tool: [Tool Name]
Purpose: [What this tool does]
When to use: [Specific scenarios]
Input format: [Expected input]
Output format: [Expected output]

Example usage:
\`\`\`
[Example command or usage]
\`\`\``}
            width='100%'
            minRows={2}
            maxRows={5}
            maxLength={5000}
            disabled={loading}
            sx={getFieldStyle('toolUsage')}
          />
          {hasSubmitted && errors.toolUsage && <Typography sx={styles.errorText}>{errors.toolUsage}</Typography>}
        </Box>

        <Box>
          <Typography sx={styles.label}>Examples</Typography>
          <Textarea
            value={examples}
            onChange={(e) => setExamples(e.target.value)}
            placeholder={`Example 1:
Question: [Sample question]
Answer: [Sample answer]
Explanation: [Why this approach was taken]

Example 2:
Question: [Another question]
Answer: [Another answer]
Explanation: [Reasoning behind the answer]`}
            width='100%'
            minRows={2}
            maxRows={8}
            maxLength={5000}
            disabled={loading}
            sx={getFieldStyle('examples')}
          />
        </Box>

        {editMode && (
          <Box>
            <Typography sx={styles.label}>Status</Typography>
            <CustomDropdown
              value={status}
              onChange={(e) => {
                setStatus(e.target.value);
                clearFieldError('status');
              }}
              options={[
                { label: 'Enabled', value: 'enabled' },
                { label: 'Disabled', value: 'disabled' },
                { label: 'Draft', value: 'draft' },
              ]}
              minWidth='350px'
              isDisabled={loading}
              isRequired={true}
              error={hasSubmitted && !!errors.status}
              helperText={hasSubmitted && errors.status ? errors.status : ''}
            />
          </Box>
        )}

        <Box>
          <Typography sx={styles.label}>Tool/Agent</Typography>
          <CustomAutocomplete
            options={toolOptions || []}
            multiple={true}
            grouped={true}
            value={tools}
            onSelect={(event, value) => {
              setTools(value);
              clearFieldError('tools');
            }}
            label='Select Tool/Agent'
            isOptionsLoading={false}
            minWidth='300px'
            limitTags={3}
          />
          {hasSubmitted && errors.tools && <Typography sx={styles.errorText}>{errors.tools}</Typography>}
        </Box>

        {(!editMode || customizeMode) && (
          <Box>
            <Typography sx={styles.label}>Upload CSV for RAG Data</Typography>
            <input type='file' accept='.csv' onChange={handleFileChange} disabled={loading} style={{ marginTop: '8px' }} />
          </Box>
        )}
      </Box>
      <Box display='flex' alignItems='center' justifyContent='flex-end' gap='12px' pt='24px' sx={{ '& button': { minWidth: '140px' } }}>
        <CustomButton
          text='Cancel'
          variant='secondary'
          size='Medium'
          onClick={() => {
            setDescription('');
            setName('');
            setInstructions('');
            setConstraints('');
            setToolUsage('');
            setExamples('');
            setTools([]);
            setStatus('enabled');
            setErrors({
              name: '',
              description: '',
              role: '',
              tools: '',
              instructions: '',
              constraints: '',
              toolUsage: '',
              status: '',
            });
            setHasSubmitted(false);
            handleClose('');
          }}
          disabled={loading}
        />
        <CustomButton text='Submit' size='Medium' onClick={handleSubmit} loading={loading} />
      </Box>
    </Box>
  );
};

CreateAgent.propTypes = {
  accountId: PropTypes.string,
  handleClose: PropTypes.func,
  allAgents: PropTypes.arrayOf(PropTypes.string),
  editMode: PropTypes.bool,
  customizeMode: PropTypes.bool,
  agentData: PropTypes.object,
};

export default CreateAgent;
