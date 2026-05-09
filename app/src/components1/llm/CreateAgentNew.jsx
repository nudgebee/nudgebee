import VerticalStepNavigation from '@components1/common/NewVerticalStepper';
import { Box, Typography } from '@mui/material';
import React, { useState, useRef, useEffect } from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';
import { getLlmIdentifierValidationMessage, parseHttpResponseBodyMessage, getToolLabel } from 'src/utils/common';
import { FormCard, FormField } from '@components1/common/NewReusabeFormComponents';
import { colors } from 'src/utils/colors';

const CreateAgentNew = ({ accountId, handleClose, allAgents, editMode, agentData, triggerSubmit, onSubmitStart, onSubmitEnd, customizeMode }) => {
  const [description, setDescription] = useState('');
  const [name, setName] = useState('');
  const [instructions, setInstructions] = useState('');
  const [constraints, setConstraints] = useState('');
  const [toolUsage, setToolUsage] = useState('');
  const [examples, setExamples] = useState('');
  const [role, setRole] = useState('');
  const [status, setStatus] = useState(agentData?.status || '');

  const [toolOptions, setToolOptions] = useState([]);
  const [tools, setTools] = useState([]);
  const [loading, setLoading] = useState(false);
  const [errors, setErrors] = useState({
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
  const [hasSubmitted, setHasSubmitted] = useState(false);

  // Step navigation state
  const [activeStep, setActiveStep] = useState(1);

  // Refs for scrolling to each step
  const stepRefs = useRef([]);

  // Create individual refs for each step
  const statusRef = useRef(null); // For status card in edit mode
  const step1Ref = useRef(null);
  const step2Ref = useRef(null);
  const step3Ref = useRef(null);
  const step4Ref = useRef(null);

  // Define agent creation steps
  const agentSteps = editMode
    ? [
        { id: 'agent-status', title: 'Agent Status', description: 'Configure agent status and settings' },
        { id: 'agent-identity', title: 'Agent Identity', description: 'Define agent name and identity' },
        { id: 'behavior-guidelines', title: 'Behavior & Guidelines', description: 'Set agent behavior and guidelines' },
        { id: 'tool-selection', title: 'Tool/Agent Selection', description: 'Select tools and agents' },
        { id: 'knowledge-examples', title: 'Knowledge & Examples', description: 'Add knowledge base and examples' },
      ]
    : [
        { id: 'agent-identity', title: 'Agent Identity', description: 'Define agent name and identity' },
        { id: 'behavior-guidelines', title: 'Behavior & Guidelines', description: 'Set agent behavior and guidelines' },
        { id: 'tool-selection', title: 'Tool/Agent Selection', description: 'Select tools and agents' },
        { id: 'knowledge-examples', title: 'Knowledge & Examples', description: 'Add knowledge base and examples' },
      ];

  // Update stepRefs array when component mounts or updates
  useEffect(() => {
    stepRefs.current = editMode
      ? [statusRef.current, step1Ref.current, step2Ref.current, step3Ref.current, step4Ref.current]
      : [step1Ref.current, step2Ref.current, step3Ref.current, step4Ref.current];
  }, [editMode]);

  // Common function to get active step styling
  const getActiveStepSx = (stepNumber) => {
    if (activeStep === stepNumber) {
      return {
        border: `1px solid ${colors.border.primary}`,
        boxShadow: '0 4px 12px rgba(59, 130, 246, 0.15)',
      };
    }
    return {};
  };

  const scrollToStep = (stepNumber) => {
    setActiveStep(stepNumber);
    const stepIndex = stepNumber - 1;
    if (stepRefs.current[stepIndex]) {
      stepRefs.current[stepIndex].scrollIntoView({
        behavior: 'smooth',
        block: 'start',
        inline: 'nearest',
      });
    }
  };

  // Intersection Observer for scroll-based step detection
  useEffect(() => {
    // Wait for refs to be populated
    const timeoutId = setTimeout(() => {
      const validRefs = stepRefs.current.filter((ref) => ref !== null);
      if (validRefs.length === 0) {
        return;
      }

      const observer = new IntersectionObserver(
        (entries) => {
          // Find the entry with the highest intersection ratio that's actually intersecting
          let mostVisibleEntry = null;
          let highestRatio = 0;

          entries.forEach((entry) => {
            if (entry.isIntersecting && entry.intersectionRatio > highestRatio) {
              mostVisibleEntry = entry;
              highestRatio = entry.intersectionRatio;
            }
          });

          if (mostVisibleEntry) {
            const stepIndex = stepRefs.current.findIndex((ref) => ref === mostVisibleEntry.target);
            if (stepIndex !== -1) {
              setActiveStep(stepIndex + 1);
            }
          }
        },
        {
          root: null,
          rootMargin: '-20% 0px -60% 0px', // Trigger when card is prominently in view
          threshold: [0.1, 0.3, 0.5, 0.7], // Multiple thresholds for better detection
        }
      );

      validRefs.forEach((ref) => {
        if (ref) {
          observer.observe(ref);
        }
      });

      return () => {
        validRefs.forEach((ref) => {
          if (ref) {
            observer.unobserve(ref);
          }
        });
      };
    }, 100); // Small delay to ensure refs are populated

    return () => clearTimeout(timeoutId);
  }, []);

  // Step validation errors
  const getStepErrors = () => {
    return editMode
      ? [
          !!errors.status, // Status step (edit mode only)
          !!(errors.name || errors.description), // Agent Identity
          !!errors.instructions, // Behavior & Guidelines
          !!errors.tools, // Tool/Agent Selection
          false, // Knowledge & Examples
        ]
      : [
          !!(errors.name || errors.description), // Agent Identity
          !!errors.instructions, // Behavior & Guidelines
          !!errors.tools, // Tool/Agent Selection
          false, // Knowledge & Examples
        ];
  };

  useEffect(() => {
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
      return ''; // Allow current name in edit mode
    }

    if (!name.trim()) {
      return 'Name is required';
    }

    if (name && allAgents.some((existingName) => existingName.toLowerCase() === name.toLowerCase())) {
      return 'Agent name already exists';
    }

    const validationMessage = getLlmIdentifierValidationMessage(name);
    return validationMessage;
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
      if (onSubmitEnd) {
        onSubmitEnd();
      } // Reset loading state
      return;
    }
    setLoading(true);
    if (onSubmitStart) {
      onSubmitStart();
    }

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
      // Update existing custom agent
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
          if (onSubmitEnd) {
            onSubmitEnd();
          }
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
          if (onSubmitEnd) {
            onSubmitEnd();
          }
          snackbar.error('An error occurred while updating the agent');
          console.error('Error updating agent:', error);
        });
    } else {
      // Create new agent or customize/override system agent
      apiAskNudgebee
        .createAgent({
          account_id: accountId,
          agent: agentPayload,
          override_agent: customizeMode ? true : false,
        })
        .then((res) => {
          const errors = res?.data?.errors || [];
          setLoading(false);
          if (onSubmitEnd) {
            onSubmitEnd();
          }
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
          if (onSubmitEnd) {
            onSubmitEnd();
          }
          snackbar.error('An error occurred while creating the agent');
          console.error('Error creating agent:', error);
        });
    }
  };

  const handleFileChange = (e) => {
    const file = e.target.files[0];
    if (file) {
      const fileExtension = file.name.split('.').pop().toLowerCase();
      const reader = new FileReader();

      reader.onload = (event) => {
        const fileContent = event.target.result;
        let processedData;
        let format;

        try {
          switch (fileExtension) {
            case 'csv': {
              // Parse CSV and convert to JSON
              const parseCSV = (csv) => {
                const lines = csv.split('\n').filter((line) => line.trim() !== '');
                if (lines.length === 0) {
                  return [];
                }

                const headers = lines[0].split(',').map((h) => h.trim());
                const result = [];

                for (let i = 1; i < lines.length; i++) {
                  const obj = {};
                  const currentline = lines[i].split(',');

                  for (let j = 0; j < headers.length; j++) {
                    obj[headers[j]] = currentline[j] ? currentline[j].trim() : '';
                  }
                  result.push(obj);
                }
                return result;
              };
              processedData = parseCSV(fileContent);
              format = 'json'; // Convert CSV to JSON format for processing
              break;
            }

            case 'json':
              // Validate and parse JSON
              processedData = JSON.parse(fileContent);
              format = 'json';
              break;

            case 'xml':
              // Keep XML as text format
              processedData = fileContent;
              format = 'xml';
              break;

            case 'txt':
              // Keep text as is
              processedData = fileContent;
              format = 'text';
              break;

            default:
              snackbar.error('Unsupported file format. Please upload CSV, JSON, XML, or TXT files.');
              return;
          }

          setRagData((prevRagData) => [
            ...prevRagData,
            {
              data: processedData,
              format: format,
              filename: file.name,
            },
          ]);

          snackbar.success(`File "${file.name}" uploaded successfully`);
        } catch (error) {
          console.error('Error processing file:', error);
          snackbar.error(`Error processing ${fileExtension.toUpperCase()} file: ${error.message}`);
        }
      };

      reader.readAsText(file);
    }
  };

  // Watch for triggerSubmit prop to execute handleSubmit
  useEffect(() => {
    if (triggerSubmit) {
      handleSubmit();
    }
  }, [triggerSubmit]);

  return (
    <Box sx={{ display: 'flex', height: '80vh', overflow: 'hidden', position: 'relative' }}>
      {/* Left Sidebar - Step Navigation */}
      <Box
        sx={{
          width: '260px',
          flexShrink: 0,
          position: 'sticky',
          top: 0,
          height: '80vh',
          backgroundColor: 'white',
          marginTop: '20px',
          marginBottom: '20px !important',
          borderRadius: '8px',
          overflow: 'visible',
          boxShadow: '0px 10px 15px -6px rgba(0, 0, 0, 0.1), 0px 6px 14px -5px rgba(50, 37, 93, 0.1)',
          zIndex: 1,
        }}
      >
        <VerticalStepNavigation
          steps={agentSteps}
          title='Agent Setup Steps'
          activeStep={activeStep}
          onStepChange={scrollToStep}
          stepErrors={getStepErrors()}
        />
      </Box>

      {/* Main Content Area */}
      <Box
        sx={{
          flex: 1,
          overflow: 'auto',
          p: 4,
        }}
      >
        <Box display='flex' flexDirection='column' width='100%' mb={3} gap='20px'>
          {/* Agent Status Card - Only in edit mode */}
          {editMode && (
            <Box ref={statusRef}>
              <FormCard title='Agent Status' number={1} columns={1} sx={getActiveStepSx(1)}>
                <FormField
                  label='Status'
                  description='Set the current operational status of the agent'
                  value={status}
                  onChange={(e) => {
                    setStatus(e.target.value);
                    clearFieldError('status');
                  }}
                  fieldType='dropdown'
                  options={[
                    { label: 'Enabled', value: 'enabled' },
                    { label: 'Disabled', value: 'disabled' },
                    { label: 'Draft', value: 'draft' },
                  ]}
                  minWidth='350px'
                  disabled={loading}
                  required={true}
                  error={hasSubmitted ? errors.status : ''}
                />
              </FormCard>
            </Box>
          )}

          {/* Agent Identity Card */}
          <Box ref={step1Ref}>
            <FormCard
              title='Agent Identity'
              description='Enter A Unique Name For Your Agent Using Letters And Underscores Only'
              number={editMode ? 2 : 1}
              columns={1}
              sx={getActiveStepSx(editMode ? 2 : 1)}
            >
              <FormField
                label='Name'
                description='Enter a unique name for your agent using letters and underscores only'
                value={name}
                sx={{ width: '50%' }}
                onChange={(e) => {
                  setName(e.target.value);
                  setErrors({ ...errors, name: nameValidation(e.target.value) });
                }}
                placeholder='Enter agent name'
                required={true}
                error={errors.name}
                fieldType='textfield'
                disabled={customizeMode || loading}
              />

              <FormField
                label='Description'
                description='Provide a clear description of what this agent does and its purpose'
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                  clearFieldError('description');
                }}
                placeholder='Describe what this agent does'
                required={true}
                error={hasSubmitted ? errors.description : ''}
                fieldType='textarea'
                rows={4}
              />
            </FormCard>
          </Box>

          {/* Behavior & Guidelines Card */}
          <Box ref={step2Ref}>
            <FormCard
              title='Behavior & Guidelines'
              description='Define How Your Agent Should Behave And Act In Different Situations'
              number={editMode ? 3 : 2}
              columns={1}
              sx={getActiveStepSx(editMode ? 3 : 2)}
            >
              <FormField
                label='Role'
                description="Define the agent's role and responsibilities in a professional manner"
                value={role}
                onChange={(e) => {
                  setRole(e.target.value);
                  clearFieldError('role');
                }}
                placeholder='You are a [role], responsible for [specific task].'
                fieldType='textarea'
                rows={4}
                maxRows={6}
                maxLength={1000}
                disabled={loading}
                error={hasSubmitted ? errors.role : ''}
              />
              <FormField
                label='Instructions'
                description='Provide step-by-step instructions on how the agent should behave and act'
                value={instructions}
                onChange={(e) => {
                  setInstructions(e.target.value);
                  clearFieldError('instructions');
                }}
                placeholder={`Key responsibilities:
1. [Primary responsibility]
2. [Secondary responsibility]
3. [Additional responsibility]`}
                required={true}
                fieldType='textarea'
                minRows={7}
                maxRows={16}
                maxLength={10000}
                disabled={loading}
                error={hasSubmitted ? errors.instructions : ''}
              />

              <FormField
                label='Constraints'
                description='Define limitations, rules, and boundaries the agent must follow'
                value={constraints}
                onChange={(e) => {
                  setConstraints(e.target.value);
                  clearFieldError('constraints');
                }}
                placeholder={`1. Always use appropriate tools for interactions
2. Do not expose sensitive information
3. Format responses in markdown
4. Ask for clarification when needed`}
                fieldType='textarea'
                minRows={6}
                maxRows={16}
                maxLength={5000}
                disabled={loading}
                error={hasSubmitted ? errors.constraints : ''}
              />
            </FormCard>
          </Box>

          {/* Tool Selection Card */}
          <Box ref={step3Ref}>
            <FormCard
              title='Tool/Agent Selection'
              description='Configure Tools And Resources Available To Your Agent'
              number={editMode ? 4 : 3}
              columns={1}
              sx={getActiveStepSx(editMode ? 4 : 3)}
            >
              <FormField
                label='Tool/Agent'
                description='Select the tools and agents this agent can use for its tasks'
                value={tools}
                onSelect={(_, value) => {
                  setTools(value);
                  clearFieldError('tools');
                }}
                placeholder='Select Tool/Agent'
                fieldType='autocomplete'
                expanded={true}
                showCategorization={true}
                options={toolOptions || []}
                multiple={true}
                grouped={true}
                isOptionsLoading={false}
                minWidth={'65%'}
                limitTags={3}
                minRows={2}
                maxRows={5}
                error={hasSubmitted ? errors.tools : ''}
              />

              <FormField
                label='Tool Usage'
                description='Explain how and when the agent should use specific tools with examples'
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
                fieldType='textarea'
                minRows={7}
                maxRows={16}
                maxLength={5000}
                disabled={loading}
                error={hasSubmitted ? errors.toolUsage : ''}
              />
            </FormCard>
          </Box>

          {/* Knowledge & Examples Card */}
          <Box ref={step4Ref}>
            <FormCard
              title='Knowledge & Examples'
              description='Provide Training Data And Examples For Better Agent Performance'
              number={editMode ? 5 : 4}
              columns={1}
              sx={getActiveStepSx(editMode ? 5 : 4)}
            >
              <FormField
                label='Examples'
                description='Provide sample questions and answers to train the agent on interactions'
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
                fieldType='textarea'
                minRows={7}
                maxRows={12}
                maxLength={5000}
                disabled={loading}
              />

              {!editMode && !customizeMode && (
                <>
                  <FormField
                    label='Upload RAG Data Files'
                    description='Upload CSV, JSON, XML, or TXT files with data the agent can reference for answering questions'
                    fieldType='custom'
                    customRender={
                      <input type='file' accept='.csv,.json,.xml,.txt' onChange={handleFileChange} disabled={loading} style={{ marginTop: '8px' }} />
                    }
                  />

                  {ragData.length > 0 && (
                    <Box sx={{ mt: 2, p: 2, backgroundColor: colors.background.primaryLightest, borderRadius: '8px' }}>
                      <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.primary, mb: 1 }}>
                        Uploaded Files ({ragData.length}):
                      </Typography>
                      {ragData.map((file, index) => (
                        <Box key={index} sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.5 }}>
                          <Typography sx={{ fontSize: '11px', color: colors.text.secondary }}>• {file.filename}</Typography>
                          <Box
                            sx={{
                              fontSize: '10px',
                              color: colors.info.dark,
                              backgroundColor: colors.info.light,
                              px: 1,
                              py: 0.25,
                              borderRadius: '4px',
                              textTransform: 'uppercase',
                            }}
                          >
                            {file.original_format || file.format}
                          </Box>
                        </Box>
                      ))}
                    </Box>
                  )}
                </>
              )}
            </FormCard>
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

CreateAgentNew.propTypes = {
  accountId: PropTypes.string,
  handleClose: PropTypes.func,
  allAgents: PropTypes.arrayOf(PropTypes.string),
  editMode: PropTypes.bool,
  customizeMode: PropTypes.bool,
  agentData: PropTypes.object,
  triggerSubmit: PropTypes.bool,
  onSubmitStart: PropTypes.func,
  onSubmitEnd: PropTypes.func,
};

export default CreateAgentNew;
