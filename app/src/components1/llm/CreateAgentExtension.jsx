import VerticalStepNavigation from '@components1/common/NewVerticalStepper';
import { Box, Typography } from '@mui/material';
import React, { useState, useRef, useEffect } from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';
import { parseHttpResponseBodyMessage, getToolLabel } from 'src/utils/common';
import { FormCard, FormField } from '@components1/common/NewReusabeFormComponents';
import { colors } from 'src/utils/colors';

const CreateAgentExtension = ({ accountId, handleClose, agentData, existingExtension, editMode, triggerSubmit, onSubmitStart, onSubmitEnd }) => {
  const [prompt, setPrompt] = useState('');
  const [toolUsage, setToolUsage] = useState('');
  const [toolOptions, setToolOptions] = useState([]);
  const [tools, setTools] = useState([]);
  const [loading, setLoading] = useState(false);
  const [errors, setErrors] = useState({
    prompt: '',
    tools: '',
    toolUsage: '',
  });
  const [hasSubmitted, setHasSubmitted] = useState(false);

  // Step navigation state
  const [activeStep, setActiveStep] = useState(1);

  // Refs for scrolling to each step
  const stepRefs = useRef([]);

  // Create individual refs for each step
  const step1Ref = useRef(null);
  const step2Ref = useRef(null);
  const step3Ref = useRef(null);

  // Define extension steps
  const extensionSteps = [
    { id: 'agent-info', title: 'Agent Information', description: 'Basic agent information and settings' },
    { id: 'additional-instructions', title: 'Additional Instructions', description: 'Additional instructions and prompts' },
    { id: 'tools-config', title: 'Tools & Configuration', description: 'Configure tools and settings' },
  ];

  // Update stepRefs array when component mounts or updates
  useEffect(() => {
    stepRefs.current = [step1Ref.current, step2Ref.current, step3Ref.current];
  }, []);

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
    return [
      false, // Agent Information (no validation)
      !!errors.prompt, // Additional Instructions
      false, // Tools & Configuration (optional)
    ];
  };

  useEffect(() => {
    setToolOptions([]);

    // Load tools and existing extension data
    Promise.all([apiAskNudgebee.listTools({ accountId })])
      .then(([toolsRes]) => {
        // Handle tools
        const listToolsResponse = toolsRes.data?.data?.ai_list_tools?.data ?? [];
        if (listToolsResponse.length > 0) {
          const GROUP_ORDER = { tool: 0, agent: 1, other: 2 };
          const toolOptions = listToolsResponse
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
              return a.label.localeCompare(b.label);
            });
          setToolOptions(toolOptions);
        }

        // Handle existing extensions - pre-fill form if extension exists
        if (existingExtension) {
          // Parse structured prompt data
          let parsedPrompt = '';
          let parsedToolUsage = '';

          try {
            if (existingExtension.additional_instructions) {
              const promptData = JSON.parse(existingExtension.additional_instructions);
              parsedPrompt = promptData.additional_instructions || '';
              parsedToolUsage = promptData.tool_usage || '';
            }
          } catch {
            // Fallback to treating as plain string if not JSON
            parsedPrompt = existingExtension.additional_instructions || '';
          }

          setPrompt(parsedPrompt);
          setToolUsage(parsedToolUsage);

          const existingTools = (existingExtension.tools || []).map((toolName) => {
            const toolOption = listToolsResponse.find((t) => t.name === toolName);
            return toolOption
              ? {
                  label: getToolLabel(toolOption),
                  value: toolOption.name,
                  group: toolOption.nb_tool_type?.toLowerCase() || 'Other',
                }
              : { label: toolName, value: toolName, group: 'other' };
          });
          setTools(existingTools);
        }
      })
      .catch((error) => {
        console.error('Error loading data:', error);
        snackbar.error('Failed to load agent extension data');
      });
  }, [accountId, agentData, existingExtension]);

  const validateForm = () => {
    const newErrors = {
      prompt: promptValidation(prompt),
      tools: '',
      toolUsage: '',
    };
    setErrors(newErrors);

    return !(newErrors.prompt || newErrors.tools || newErrors.toolUsage);
  };

  const promptValidation = (prompt) => {
    return !prompt.trim() ? 'Additional prompt cannot be empty.' : '';
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
      if (errors.prompt) {
        errorMessage += `\n- ${errors.prompt}`;
      }
      if (errors.tools) {
        errorMessage += `\n- ${errors.tools}`;
      }
      if (errors.toolUsage) {
        errorMessage += `\n- ${errors.toolUsage}`;
      }

      snackbar.error(errorMessage);
      if (onSubmitEnd) {
        onSubmitEnd();
      }
      return;
    }
    setLoading(true);
    if (onSubmitStart) {
      onSubmitStart();
    }

    const extensionPayload = {
      account_id: accountId,
      agent: {
        agent_name: agentData.name,
        prompt: JSON.stringify({
          additional_instructions: prompt.trim(),
          tool_usage: toolUsage.trim(),
        }),
        tools: tools.map((f) => f?.value || f),
      },
    };

    if (editMode) {
      apiAskNudgebee
        .updateAgentExtension(extensionPayload)
        .then((res) => {
          const errors = res?.data?.errors || [];
          setLoading(false);
          if (onSubmitEnd) {
            onSubmitEnd();
          }
          if (errors.length > 0) {
            const errorMessage = `Failed to create Agent Extension - ${parseHttpResponseBodyMessage(res?.data)}`;
            snackbar.error(errorMessage);
            return;
          }
          snackbar.success('Agent extension updated successfully');
          handleClose('success');
        })
        .catch((error) => {
          setLoading(false);
          if (onSubmitEnd) {
            onSubmitEnd();
          }
          snackbar.error('An error occurred while updating the agent extension');
          console.error('Error creating agent extension:', error);
        });
    } else {
      apiAskNudgebee
        .createAgentExtension(extensionPayload)
        .then((res) => {
          const errors = res?.data?.errors || [];
          setLoading(false);
          if (onSubmitEnd) {
            onSubmitEnd();
          }
          if (errors.length > 0) {
            const errorMessage = `Failed to create Agent Extension - ${parseHttpResponseBodyMessage(res?.data)}`;
            snackbar.error(errorMessage);
            return;
          }
          snackbar.success('Agent extension created successfully');
          handleClose('success');
        })
        .catch((error) => {
          setLoading(false);
          if (onSubmitEnd) {
            onSubmitEnd();
          }
          snackbar.error(editMode ? 'An error occurred while updating the agent extension' : 'An error occurred while creating the agent extension');
          console.error('Error creating agent extension:', error);
        });
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
          steps={extensionSteps}
          title='Extension Steps'
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
          {/* Agent Information Card */}
          <Box ref={step1Ref}>
            <FormCard title='Agent Information' description='Overview of the agent being extended' number={1} columns={1} sx={getActiveStepSx(1)}>
              <Box sx={{ p: 2, backgroundColor: colors.background.primaryLightest, borderRadius: '8px' }}>
                <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.primary, mb: 1 }}>
                  Agent: {agentData?.aliases?.[0] || agentData?.name}
                </Typography>
                <Typography sx={{ fontSize: '12px', color: colors.text.secondary, mb: 2 }}>
                  {editMode
                    ? 'Update the additional prompt and tools for this system agent'
                    : "Add additional prompt and tools to extend this system agent's capabilities"}
                </Typography>
              </Box>
            </FormCard>
          </Box>

          {/* Additional Instructions Card */}
          <Box ref={step2Ref}>
            <FormCard
              title='Additional Instructions'
              description='Define Additional Prompts And Behaviors For The Agent Extension'
              number={2}
              columns={1}
              sx={getActiveStepSx(2)}
            >
              <FormField
                label='Additional Prompt'
                description='Add specific instructions that will be appended to the existing agent behavior'
                value={prompt}
                onChange={(e) => {
                  setPrompt(e.target.value);
                  clearFieldError('prompt');
                }}
                placeholder={`Add specific instructions for this agent:

1. [Additional behavior or capability]
2. [Specific task guidance]
3. [Context-specific instructions]

These instructions will be added to the existing system prompt.`}
                required={true}
                fieldType='textarea'
                minRows={6}
                maxRows={12}
                maxLength={5000}
                disabled={loading}
                error={hasSubmitted ? errors.prompt : ''}
              />

              <FormField
                label='Tool Usage Instructions'
                description='Explain how and when the agent should use the additional tools with examples'
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
                minRows={4}
                maxRows={8}
                maxLength={5000}
                disabled={loading}
                error={hasSubmitted ? errors.toolUsage : ''}
              />
            </FormCard>
          </Box>

          {/* Tools & Configuration Card */}
          <Box ref={step3Ref}>
            <FormCard
              title='Tools & Configuration'
              description='Configure Additional Tools Available To The Agent Extension'
              number={3}
              columns={1}
              sx={getActiveStepSx(3)}
            >
              <FormField
                label='Additional Tools'
                description='Select additional tools that this agent extension can use'
                value={tools}
                onSelect={(_, value) => {
                  setTools(value);
                  clearFieldError('tools');
                }}
                placeholder='Select Additional Tools'
                fieldType='autocomplete'
                expanded={true}
                showCategorization={true}
                options={toolOptions || []}
                multiple={true}
                grouped={true}
                isOptionsLoading={false}
                limitTags={3}
                minRows={2}
                maxRows={4}
                error={hasSubmitted ? errors.tools : ''}
              />
            </FormCard>
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

CreateAgentExtension.propTypes = {
  accountId: PropTypes.string,
  handleClose: PropTypes.func,
  agentData: PropTypes.object,
  existingExtension: PropTypes.object,
  editMode: PropTypes.bool,
  triggerSubmit: PropTypes.bool,
  onSubmitStart: PropTypes.func,
  onSubmitEnd: PropTypes.func,
};

export default CreateAgentExtension;
