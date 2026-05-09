import CustomButton from '@components1/common/NewCustomButton';
import VerticalStepNavigation from '@components1/common/NewVerticalStepper';
import { Box, Typography, Radio, RadioGroup, FormControlLabel, FormControl, Switch, Button } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import { FormCard, FormField } from '@components1/common/NewReusabeFormComponents';
import { getLlmIdentifierValidationMessage } from 'src/utils/common';

const CreateTool = ({ accountId, handleClose, allTools, editMode = false, toolData = null, preSelectedToolType = '' }) => {
  const [selectedToolType] = React.useState(preSelectedToolType || (editMode && toolData ? (toolData.executor_type || '').toLowerCase() : ''));

  // Existing state
  const [description, setDescription] = React.useState(editMode && toolData ? toolData.description : '');
  const [name, setName] = React.useState(editMode && toolData ? toolData.name : '');
  const [toolType] = React.useState(preSelectedToolType || (editMode && toolData ? (toolData.executor_type || '').toLowerCase() : 'mcp'));
  const [toolStatus, setToolStatus] = React.useState(editMode && toolData ? toolData.status : 'enabled');

  // New MCP fields
  const [category, _setCategory] = React.useState(editMode && toolData ? toolData.category || 'Monitoring' : 'Monitoring');
  const [connectionType, setConnectionType] = React.useState(
    editMode && toolData && toolData.config?.connection_type ? toolData.config.connection_type : 'Remote (HTTP/HTTPS)'
  );

  // Remote connection specific fields
  const [port, setPort] = React.useState(editMode && toolData && toolData.config?.port ? toolData.config.port : '443');
  const [mcpVersion, setMcpVersion] = React.useState(editMode && toolData && toolData.config?.mcp_version ? toolData.config.mcp_version : 'Auto');
  const [connectionPoolSize, setConnectionPoolSize] = React.useState(
    editMode && toolData && toolData.config?.connection_pool_size ? toolData.config.connection_pool_size : '8'
  );

  // Authentication & TLS fields
  const [authType, setAuthType] = React.useState(editMode && toolData && toolData.config?.auth_type ? toolData.config.auth_type : 'None');
  const [verifySSL, setVerifySSL] = React.useState(
    editMode && toolData && toolData.config?.verify_ssl !== undefined ? toolData.config.verify_ssl : true
  );

  // Conditional auth fields
  const [apiKey, setApiKey] = React.useState(editMode && toolData && toolData.config?.api_key ? toolData.config.api_key : '');
  const [apiHeaderName, setApiHeaderName] = React.useState(
    editMode && toolData && toolData.config?.api_header_name ? toolData.config.api_header_name : 'Authorization'
  );
  const [bearerToken, setBearerToken] = React.useState(editMode && toolData && toolData.config?.bearer_token ? toolData.config.bearer_token : '');

  // Runtime & Scope fields
  const [environmentVariables, setEnvironmentVariables] = React.useState(
    editMode && toolData && toolData.config?.environment_variables ? toolData.config.environment_variables : ''
  );
  const [_environmentFile, _setEnvironmentFile] = React.useState(null);
  const [_contextTags, _setContextTags] = React.useState(editMode && toolData && toolData.config?.context_tags ? toolData.config.context_tags : []);
  const [supportedClusters, setSupportedClusters] = React.useState(
    editMode && toolData && toolData.config?.supported_clusters ? toolData.config.supported_clusters : ['All']
  );
  const [agentAccess, setAgentAccess] = React.useState(
    editMode && toolData && toolData.config?.agent_access ? toolData.config.agent_access : ['Troubleshoot']
  );

  // Capabilities & Guardrails fields
  const [capabilities, setCapabilities] = React.useState(
    editMode && toolData && toolData.config?.capabilities ? toolData.config.capabilities : ['Read']
  );
  const [riskLevel, setRiskLevel] = React.useState(editMode && toolData && toolData.config?.risk_level ? toolData.config.risk_level : 'Medium');
  const [approvalRequired, setApprovalRequired] = React.useState(
    editMode && toolData && toolData.config?.approval_required !== undefined ? toolData.config.approval_required : false
  );
  const [auditLogging, setAuditLogging] = React.useState(
    editMode && toolData && toolData.config?.audit_logging !== undefined ? toolData.config.audit_logging : true
  );
  const [rateLimit, setRateLimit] = React.useState(editMode && toolData && toolData.config?.rate_limit ? toolData.config.rate_limit : '120');

  // Advanced Resilience fields
  const [retryPolicy, setRetryPolicy] = React.useState('{"max":3}');
  const [circuitBreaker, setCircuitBreaker] = React.useState(false);
  const [cachingPolicy, setCachingPolicy] = React.useState('None');
  const [_customHeaders, _setCustomHeaders] = React.useState('');
  const [_resourceLimits, _setResourceLimits] = React.useState('{"cpu":"200m"}');

  // Step navigation state for MCP
  const [activeStep, setActiveStep] = React.useState(1);

  // Refs for scrolling to each step
  const stepRefs = React.useRef([]);

  // Create individual refs for each step
  const step1Ref = React.useRef(null);
  const step2Ref = React.useRef(null);
  const step3Ref = React.useRef(null);
  const step4Ref = React.useRef(null);
  const step5Ref = React.useRef(null);
  const step6Ref = React.useRef(null);
  const step7Ref = React.useRef(null);

  // Update stepRefs array when component mounts or updates
  React.useEffect(() => {
    if (toolType === 'mcp') {
      stepRefs.current = [
        step1Ref.current,
        step2Ref.current,
        step3Ref.current,
        step4Ref.current,
        step5Ref.current,
        step6Ref.current,
        step7Ref.current,
      ];
    }
  }, [toolType]);

  // Common function to get active step styling
  const getActiveStepSx = (stepNumber) => {
    if (toolType === 'mcp' && activeStep === stepNumber) {
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
  React.useEffect(() => {
    if (toolType !== 'mcp') {
      return;
    }

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
  }, [toolType]);

  // Define MCP steps
  const mcpSteps = [
    { id: 'basic-details', title: 'Basic Details', description: 'Enter the basic information for your tool' },
    { id: 'mcp-configuration', title: 'MCP Configuration', description: 'Configure the MCP server connection settings' },
    { id: 'authentication-tls', title: 'Authentication & TLS', description: 'Configure security settings for MCP connection' },
    { id: 'runtime-scope', title: 'Runtime & Scope', description: 'Configure runtime and scope settings for MCP tool' },
    { id: 'capabilities-guardrails', title: 'Capabilities & Guardrails', description: 'Define operational capabilities and safety measures' },
    { id: 'test-validation', title: 'Test & Validation', description: 'Validate tool functionality and performance' },
    { id: 'advanced-resilience', title: 'Advanced Resilience', description: 'Configure advanced resilience settings for MCP tool' },
  ];

  // Step validation errors
  const getStepErrors = () => {
    return [
      !!(errors.name || errors.description || errors.category), // Step 1
      !!(errors.connectionType || errors.mcpHttpUrl || errors.mcpHttpHeaders), // Step 2
      false, // Step 3 - Auth & TLS (no required fields)
      false, // Step 4 - Runtime & Scope (no required fields)
      false, // Step 5 - Capabilities & Guardrails (no required fields)
      false, // Step 6 - Test & Validation (no required fields)
      false, // Step 7 - Advanced Resilience (no required fields)
    ];
  };

  const [mcpHttpUrl, setMcpHttpUrl] = React.useState(
    editMode && toolData && (toolData.executor_type || '').toLowerCase() === 'mcp' ? toolData.config?.mcp_http_url || '' : ''
  );
  const [mcpHttpHeaders, setMcpHttpHeaders] = React.useState(
    editMode && toolData && (toolData.executor_type || '').toLowerCase() === 'mcp' && toolData.config?.mcp_http_headers
      ? JSON.stringify(toolData.config.mcp_http_headers, null, 2)
      : ''
  );

  const [containerImage, setContainerImage] = React.useState(
    editMode && toolData && (toolData.executor_type || '').toLowerCase() === 'container' ? toolData.config?.image || '' : ''
  );
  const [containerCommand, setContainerCommand] = React.useState(
    editMode && toolData && (toolData.executor_type || '').toLowerCase() === 'container' && toolData.config?.command?.length
      ? toolData.config.command[0]
      : ''
  );
  const [containerArgs, setContainerArgs] = React.useState(
    editMode && toolData && (toolData.executor_type || '').toLowerCase() === 'container' && toolData.config?.args?.length
      ? toolData.config.args.join(' ')
      : ''
  );

  const [loading, setLoading] = React.useState(false);
  const [errors, setErrors] = React.useState({
    name: '',
    description: '',
    category: '',
    mcpHttpUrl: '',
    mcpHttpHeaders: '',
    containerImage: '',
    connectionType: '',
  });

  const mcpHttpUrlValidation = (url) => {
    if (toolType === 'mcp' && (connectionType === 'Remote (HTTP/HTTPS)' || connectionType === 'SSH Tunnel') && !url.trim()) {
      return connectionType === 'SSH Tunnel' ? 'Server endpoint cannot be empty' : 'HTTP URL cannot be empty for MCP tool';
    }
    if (url.trim() && connectionType === 'Remote (HTTP/HTTPS)' && !url.match(/^https?:\/\/.+/)) {
      return 'HTTP URL must be a valid URL starting with http:// or https://';
    }
    return '';
  };

  const validateHttpHeaders = (headers) => {
    if (!headers.trim()) {
      return '';
    }

    try {
      const parsedHeaders = JSON.parse(headers);
      if (typeof parsedHeaders !== 'object' || parsedHeaders === null) {
        return 'HTTP headers must be a valid JSON object';
      }
      return '';
    } catch {
      return 'Invalid JSON format for HTTP headers';
    }
  };

  const containerImageValidation = (image) => {
    if (toolType === 'container' && !image.trim()) {
      return 'Container image cannot be empty.';
    }
    return '';
  };

  const connectionTypeValidation = (connectionType) => {
    if (toolType === 'mcp' && (!connectionType || connectionType.trim() === '')) {
      return 'Connection type cannot be empty.';
    }
    return '';
  };

  const _containerCommandValidation = (_command) => {
    return ''; // Optional
  };

  const _containerArgsValidation = (_args) => {
    return ''; // Optional
  };

  const validateForm = () => {
    const newErrors = {
      name: nameValidation(name),
      description: descriptionValidation(description),
      category: '',
      mcpHttpUrl:
        toolType === 'mcp' && (connectionType === 'Remote (HTTP/HTTPS)' || connectionType === 'SSH Tunnel') ? mcpHttpUrlValidation(mcpHttpUrl) : '',
      mcpHttpHeaders: toolType === 'mcp' && connectionType === 'Remote (HTTP/HTTPS)' ? validateHttpHeaders(mcpHttpHeaders) : '',
      containerImage: toolType === 'container' ? containerImageValidation(containerImage) : '',
      connectionType: toolType === 'mcp' ? connectionTypeValidation(connectionType) : '',
    };

    setErrors(newErrors);

    // Check if at least one tool type is properly configured
    if (toolType === 'mcp' && (newErrors.mcpHttpUrl || newErrors.mcpHttpHeaders || newErrors.connectionType)) {
      return false;
    }
    if (toolType === 'container' && newErrors.containerImage) {
      return false;
    }

    return !(newErrors.name || newErrors.description || newErrors.category);
  };

  const descriptionValidation = (description) => {
    return !description.trim() ? 'Description cannot be empty.' : '';
  };

  const nameValidation = (name) => {
    // In edit mode, only check for duplicate if the name has changed
    if (
      (!editMode && allTools.some((tool) => tool.name === name)) ||
      (editMode && toolData && name !== toolData.name && allTools.some((tool) => tool.name === name))
    ) {
      return 'Tool name already exists';
    }

    const validationMessage = getLlmIdentifierValidationMessage(name);
    return validationMessage;
  };

  const handleSubmit = () => {
    if (!validateForm()) {
      return;
    }
    setLoading(true);

    const baseToolData = {
      description: description,
      name: name,
      category: category,
      schema: {},
    };

    if (editMode) {
      baseToolData.status = toolStatus;
    }

    let specificToolConfig = {};
    let executorType = '';

    if (toolType === 'mcp') {
      executorType = 'mcp';

      const config = {
        connection_type: connectionType,
      };

      if (connectionType === 'Remote (HTTP/HTTPS)') {
        config.mcp_server_type = 'http';
        config.mcp_http_url = mcpHttpUrl;

        if (mcpHttpHeaders.trim()) {
          let httpHeaders = {};
          try {
            httpHeaders = JSON.parse(mcpHttpHeaders);
            if (Object.keys(httpHeaders).length > 0) {
              config.mcp_http_headers = httpHeaders;
            }
          } catch {
            setErrors({ ...errors, mcpHttpHeaders: 'Invalid JSON format for HTTP headers' });
            setLoading(false);
            return;
          }
        }
      } else if (connectionType === 'SSH Tunnel') {
        config.server_endpoint = mcpHttpUrl;
      } else if (connectionType === 'Local (stdio)') {
        config.mcp_server_type = 'stdio';
      }

      specificToolConfig = { config };
    } else if (toolType === 'container') {
      executorType = 'container';
      specificToolConfig = {
        config: {
          image: containerImage,
          command: containerCommand ? [containerCommand.trim()] : [],
          args: containerArgs ? containerArgs.trim().split(/\s+/).filter(Boolean) : [],
        },
      };
    }

    const toolPayload = {
      account_id: accountId,
      tool: { ...baseToolData, executor_type: executorType, ...specificToolConfig },
    };

    if (editMode && toolData && toolData.id) {
      toolPayload.tool.id = toolData.id;
    }

    const apiCall = editMode ? apiAskNudgebee.updateTool(toolPayload) : apiAskNudgebee.createTool(toolPayload);

    apiCall
      .then((res) => {
        const apiResponseData = editMode ? res?.data?.data?.ai_update_tool?.data : res?.data?.data?.ai_create_tool?.data;

        if (res?.data?.errors) {
          snackbar.error(`Failed to ${editMode ? 'update' : 'create'} tool`);
          setLoading(false);
          return;
        }
        if (Object.keys(apiResponseData || {}).length > 0) {
          snackbar.success(`Tool ${editMode ? 'updated' : 'created'} successfully`);
          handleClose('success');
        } else {
          snackbar.error(`Failed to ${editMode ? 'update' : 'create'} tool`);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  };

  // Function to get the form title based on tool type
  const getFormTitle = () => {
    if (editMode) {
      return `Edit ${selectedToolType === 'mcp' ? 'MCP HTTP' : 'Container'} Tool`;
    }
    return `Create ${selectedToolType === 'mcp' ? 'MCP HTTP' : 'Container'} Tool`;
  };

  return (
    <Box sx={{ display: 'flex', height: '80vh', overflow: 'hidden', position: 'relative' }}>
      {/* Left Sidebar - Step Navigation (only for MCP) */}
      {toolType === 'mcp' && (
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
            steps={mcpSteps}
            title='MCP Setup Steps'
            activeStep={activeStep}
            onStepChange={scrollToStep}
            stepErrors={getStepErrors()}
          />
        </Box>
      )}

      {/* Main Content Area */}
      <Box
        sx={{
          flex: 1,
          overflow: 'auto',
          p: 4,
        }}
      >
        {/* Form Title */}
        <Box mb={3}>
          <Typography variant='h5' sx={{ fontWeight: 600, color: colors.text.secondary }}>
            {getFormTitle()}
          </Typography>
          <Typography variant='body2' sx={{ color: colors.text.secondaryDark }}>
            {selectedToolType === 'mcp' && 'Create a tool that connects to MCP HTTP servers'}
            {selectedToolType === 'container' && 'Create a tool that runs containerized applications'}
          </Typography>
        </Box>

        {/* Basic Details Card */}
        <Box ref={toolType === 'mcp' ? step1Ref : null}>
          <FormCard title='Basic Details' description='Enter the basic information for your tool' number={1} columns={1} sx={getActiveStepSx(1)}>
            <FormField
              label='Name'
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setErrors({ ...errors, name: nameValidation(e.target.value) });
              }}
              placeholder='Enter tool name'
              required={true}
              sx={{ width: '50%' }}
              error={errors.name}
              fieldType='textfield'
            />

            <FormField
              label='Description'
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
                setErrors({ ...errors, description: descriptionValidation(e.target.value) });
              }}
              placeholder='Describe what this tool does'
              required={true}
              fieldType='textfield'
              multiline={true}
              rows={3}
            />

            {/* <FormField
          label="Category"
          value={category}
          onChange={(e) => {
            setCategory(e.target.value);
            setErrors({ ...errors, category: categoryValidation(e.target.value) });
          }}
          error={errors.category}
          fieldType="dropdown"
          options={[
            { label: 'Monitoring', value: 'Monitoring' },
            { label: 'Database', value: 'Database' },
            { label: 'Cloud Provider', value: 'Cloud Provider' },
            { label: 'CI/CD', value: 'CI/CD' },
            { label: 'Security', value: 'Security' },
            { label: 'Networking', value: 'Networking' },
            { label: 'Storage', value: 'Storage' },
            { label: 'Custom', value: 'Custom' }
          ]}
        /> */}

            {editMode && (
              <FormField
                label='Status'
                value={toolStatus}
                onChange={(e) => setToolStatus(e.target.value)}
                required={true}
                fieldType='dropdown'
                options={[
                  { label: 'Enabled', value: 'enabled' },
                  { label: 'Disabled', value: 'disabled' },
                ]}
              />
            )}
          </FormCard>
        </Box>

        {/* Advanced Details Card - MCP */}
        {toolType === 'mcp' && (
          <Box ref={step2Ref}>
            <FormCard
              title='MCP Configuration'
              description='Configure the MCP server connection settings'
              number={2}
              columns={1}
              sx={getActiveStepSx(2)}
            >
              <FormField
                label='Connection Type'
                required={true}
                error={errors.connectionType}
                fieldType='custom'
                customRender={
                  <Box>
                    <FormControl>
                      <RadioGroup
                        value={connectionType}
                        row={true}
                        sx={{
                          '& .MuiFormControlLabel-label': {
                            fontSize: '13px',
                          },
                        }}
                        onChange={(e) => {
                          setConnectionType(e.target.value);
                          setErrors({ ...errors, connectionType: connectionTypeValidation(e.target.value) });
                        }}
                      >
                        <FormControlLabel value='Remote (HTTP/HTTPS)' control={<Radio />} label='Remote (HTTP/HTTPS)' />
                        <FormControlLabel value='Local (stdio)' control={<Radio />} label='Local (stdio)' disabled />
                        <FormControlLabel value='SSH Tunnel' control={<Radio />} label='SSH Tunnel' disabled />
                      </RadioGroup>
                    </FormControl>
                  </Box>
                }
              />

              {connectionType === 'Remote (HTTP/HTTPS)' && (
                <>
                  <FormField
                    label='Server URL'
                    description='Base URL to MCP server'
                    value={mcpHttpUrl}
                    onChange={(e) => {
                      setMcpHttpUrl(e.target.value);
                      setErrors({ ...errors, mcpHttpUrl: mcpHttpUrlValidation(e.target.value) });
                    }}
                    placeholder='https://'
                    required={true}
                    sx={{ width: '50%' }}
                    error={errors.mcpHttpUrl}
                    fieldType='textfield'
                  />

                  <FormField
                    label='HTTP Headers'
                    description='Enter headers as a JSON object'
                    value={mcpHttpHeaders}
                    onChange={(e) => setMcpHttpHeaders(e.target.value)}
                    placeholder='{"Authorization": "Bearer token", "Content-Type": "application/json"}'
                    error={errors.mcpHttpHeaders}
                    fieldType='textfield'
                    multiline={true}
                    rows={3}
                  />

                  <FormField
                    label='Port'
                    description='Optional explicit port'
                    value={port}
                    onChange={(e) => setPort(e.target.value)}
                    placeholder='443'
                    fieldType='textfield'
                    sx={{ width: '50%' }}
                    type='number'
                  />

                  <FormField
                    label='MCP Version'
                    description='Protocol version or auto'
                    value={mcpVersion}
                    onChange={(e) => setMcpVersion(e.target.value)}
                    fieldType='dropdown'
                    options={[
                      { label: 'Auto', value: 'Auto' },
                      { label: 'v1.0', value: 'v1.0' },
                      { label: 'v0.9', value: 'v0.9' },
                    ]}
                  />

                  <FormField
                    label='Connection Pool Size'
                    description='Parallel connections allowed'
                    value={connectionPoolSize}
                    onChange={(e) => setConnectionPoolSize(e.target.value)}
                    placeholder='8'
                    sx={{ width: '50%' }}
                    fieldType='textfield'
                    type='number'
                  />
                </>
              )}

              {connectionType === 'SSH Tunnel' && (
                <FormField
                  label='Server Endpoint'
                  description='Command path, URL, or SSH target'
                  value={mcpHttpUrl}
                  onChange={(e) => {
                    setMcpHttpUrl(e.target.value);
                    setErrors({ ...errors, mcpHttpUrl: mcpHttpUrlValidation(e.target.value) });
                  }}
                  placeholder='Enter server endpoint'
                  required={true}
                  error={errors.mcpHttpUrl}
                  fieldType='textfield'
                />
              )}
            </FormCard>
          </Box>
        )}

        {/* Authentication & TLS Card - MCP */}
        {toolType === 'mcp' && (
          <Box ref={step3Ref}>
            <FormCard
              title='Authentication & TLS'
              description='Configure security settings for MCP connection'
              number={3}
              columns={1}
              sx={getActiveStepSx(3)}
            >
              <FormField
                label='Auth Type'
                description='Security mechanism for server'
                value={authType}
                onChange={(e) => setAuthType(e.target.value)}
                fieldType='dropdown'
                options={[
                  { label: 'None', value: 'None' },
                  { label: 'API Key', value: 'API Key' },
                  { label: 'Bearer Token', value: 'Bearer Token' },
                  { label: 'Basic Auth', value: 'Basic Auth' },
                  { label: 'OAuth 2.0', value: 'OAuth 2.0' },
                  { label: 'mTLS', value: 'mTLS' },
                ]}
              />

              {/* Conditional Auth Fields */}
              {authType === 'API Key' && (
                <>
                  <FormField
                    label='API Key'
                    description='Secret key sent with requests'
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                    placeholder='Enter your API key'
                    required={true}
                    fieldType='textfield'
                    type='password'
                  />

                  <FormField
                    label='API Header Name'
                    description='Override header for key'
                    value={apiHeaderName}
                    onChange={(e) => setApiHeaderName(e.target.value)}
                    placeholder='Authorization'
                    fieldType='textfield'
                  />
                </>
              )}

              {authType === 'Bearer Token' && (
                <FormField
                  label='Bearer Token'
                  description='Token for Authorization header'
                  value={bearerToken}
                  onChange={(e) => setBearerToken(e.target.value)}
                  placeholder='Enter your bearer token'
                  required={true}
                  fieldType='textfield'
                  type='password'
                />
              )}

              <FormField
                label='Verify SSL'
                description='Enforce certificate verification'
                value={verifySSL}
                onChange={(e) => setVerifySSL(e.target.checked)}
                fieldType='custom'
                customRender={
                  <Box sx={{ display: 'flex', alignItems: 'center', mt: 1 }}>
                    <FormControlLabel
                      control={
                        <Switch
                          checked={verifySSL}
                          onChange={(e) => setVerifySSL(e.target.checked)}
                          sx={{
                            '& .MuiSwitch-switchBase.Mui-checked': {
                              color: colors.success,
                            },
                            '& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track': {
                              backgroundColor: colors.success,
                            },
                          }}
                        />
                      }
                      label={verifySSL ? 'On' : 'Off'}
                      sx={{
                        ml: 0,
                        '& .MuiFormControlLabel-label': {
                          fontSize: '14px',
                          color: colors.text.secondary,
                        },
                      }}
                    />
                  </Box>
                }
              />
            </FormCard>
          </Box>
        )}

        {/* Runtime & Scope Card - MCP */}
        {toolType === 'mcp' && (
          <Box ref={step4Ref}>
            <FormCard
              title='Runtime & Scope'
              description='Configure runtime environment and access scope'
              number={4}
              columns={1}
              sx={getActiveStepSx(4)}
            >
              <FormField
                label='Environment Variables'
                description='Key/value pairs for server'
                value={environmentVariables}
                onChange={(e) => setEnvironmentVariables(e.target.value)}
                fieldType='textfield'
                multiline={true}
                rows={3}
                placeholder='KEY1=value1\nKEY2=value2'
              />

              {/* <FormField
            label="Context Tags"
            description="Labels for routing/search"
            value={contextTags}
            onChange={(event, newValue) => setContextTags(newValue)}
            fieldType="autocomplete"
            multiple={true}
            options={[
              { label: 'k8s', value: 'k8s' },
              { label: 'aws', value: 'aws' },
              { label: 'finops', value: 'finops' }
            ]}
            placeholder="Select or type tags"
          /> */}

              <FormField
                label='Supported Clusters'
                description='Limit tool to clusters'
                value={supportedClusters}
                onChange={(event, newValue) => setSupportedClusters(newValue)}
                fieldType='autocomplete'
                multiple={true}
                expanded={true}
                options={[
                  { label: 'All', value: 'All' },
                  { label: 'aws-dev', value: 'aws-dev' },
                  { label: 'nudgebee-aws-prod', value: 'nudgebee-aws-prod' },
                  { label: 'pollux', value: 'pollux' },
                  { label: 'nudgebee-dev-new', value: 'nudgebee-dev-new' },
                  { label: 'nudgebee-gke-dev', value: 'nudgebee-gke-dev' },
                  { label: 'nudgebee-civo-dev', value: 'nudgebee-civo-dev' },
                  { label: 'aks-dev-cluster', value: 'aks-dev-cluster' },
                  { label: 'nudgebee-prod', value: 'nudgebee-prod' },
                ]}
                placeholder='Select clusters'
              />

              <FormField
                label='Agent Access'
                description='Agents allowed to invoke tool'
                value={agentAccess}
                onChange={(event, newValue) => setAgentAccess(newValue)}
                fieldType='autocomplete'
                multiple={true}
                expanded={true}
                options={[
                  { label: 'Troubleshoot', value: 'Troubleshoot' },
                  { label: 'CostOps', value: 'CostOps' },
                  { label: 'AutoOps', value: 'AutoOps' },
                  { label: 'Security', value: 'Security' },
                  { label: 'Custom', value: 'Custom' },
                ]}
                placeholder='Select agent access'
              />
            </FormCard>
          </Box>
        )}

        {/* Capabilities & Guardrails Card - MCP */}
        {toolType === 'mcp' && (
          <Box ref={step5Ref}>
            <FormCard
              title='Capabilities & Guardrails'
              description='Define operational capabilities and safety measures'
              number={5}
              columns={1}
              sx={getActiveStepSx(5)}
            >
              <FormField
                label='Capabilities'
                description='Allowed operations on system'
                value={capabilities}
                onChange={(event, newValue) => setCapabilities(newValue)}
                fieldType='autocomplete'
                multiple={true}
                options={[
                  { label: 'Read', value: 'Read' },
                  { label: 'Config Change', value: 'Config Change' },
                  { label: 'Create', value: 'Create' },
                  { label: 'Delete', value: 'Delete' },
                  { label: 'Sensitive Data', value: 'Sensitive Data' },
                ]}
                placeholder='Select capabilities'
              />

              <FormField
                label='Risk Level'
                description='Operational risk rating'
                value={riskLevel}
                onChange={(e) => setRiskLevel(e.target.value)}
                fieldType='custom'
                customRender={
                  <Box sx={{ mt: 1 }}>
                    <FormControl>
                      <RadioGroup value={riskLevel} onChange={(e) => setRiskLevel(e.target.value)} row>
                        <FormControlLabel value='Low' control={<Radio />} label='Low' />
                        <FormControlLabel value='Medium' control={<Radio />} label='Medium' />
                        <FormControlLabel value='High' control={<Radio />} label='High' />
                      </RadioGroup>
                    </FormControl>
                  </Box>
                }
              />

              <FormField
                label='Approval Required'
                description='Human approval before action'
                value={approvalRequired}
                onChange={(e) => setApprovalRequired(e.target.checked)}
                fieldType='custom'
                customRender={
                  <Box sx={{ display: 'flex', alignItems: 'center', mt: 1 }}>
                    <FormControlLabel
                      control={
                        <Switch
                          checked={approvalRequired}
                          onChange={(e) => setApprovalRequired(e.target.checked)}
                          sx={{
                            '& .MuiSwitch-switchBase.Mui-checked': {
                              color: colors.success,
                            },
                            '& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track': {
                              backgroundColor: colors.success,
                            },
                          }}
                        />
                      }
                      label={approvalRequired ? 'On' : 'Off*'}
                      sx={{
                        ml: 0,
                        '& .MuiFormControlLabel-label': {
                          fontSize: '14px',
                          color: colors.text.secondary,
                        },
                      }}
                    />
                  </Box>
                }
              />

              <FormField
                label='Audit Logging'
                description='Log tool invocations'
                value={auditLogging}
                onChange={(e) => setAuditLogging(e.target.checked)}
                fieldType='custom'
                customRender={
                  <Box sx={{ display: 'flex', alignItems: 'center', mt: 1 }}>
                    <FormControlLabel
                      control={
                        <Switch
                          checked={auditLogging}
                          onChange={(e) => setAuditLogging(e.target.checked)}
                          sx={{
                            '& .MuiSwitch-switchBase.Mui-checked': {
                              color: colors.success,
                            },
                            '& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track': {
                              backgroundColor: colors.success,
                            },
                          }}
                        />
                      }
                      label={auditLogging ? 'On' : 'Off'}
                      sx={{
                        ml: 0,
                        '& .MuiFormControlLabel-label': {
                          fontSize: '14px',
                          color: colors.text.secondary,
                        },
                      }}
                    />
                  </Box>
                }
              />

              <FormField
                label='Rate Limit (rpm)'
                description='Requests per minute cap'
                value={rateLimit}
                onChange={(e) => setRateLimit(e.target.value)}
                placeholder='120'
                sx={{ width: '50%' }}
                fieldType='textfield'
                type='number'
              />
            </FormCard>
          </Box>
        )}

        {/* Test & Validation Card - MCP */}
        {toolType === 'mcp' && (
          <Box ref={step6Ref}>
            <FormCard
              title='Test & Validation'
              description='Test connection and validate MCP server configuration'
              number={6}
              columns={1}
              expand={true}
              sx={getActiveStepSx(6)}
            >
              <FormField
                label='Health check endpoints'
                description='Endpoints to check MCP server health'
                value={mcpHttpUrl}
                onChange={(e) => {
                  setMcpHttpUrl(e.target.value);
                  setErrors({
                    ...errors,
                    mcpHttpUrl: mcpHttpUrlValidation(e.target.value),
                  });
                }}
                placeholder='https://mcp-server/health'
                required={true}
                error={errors.mcpHttpUrl}
                fieldType='textfield'
              />

              <FormField
                label='Sample Query'
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                  setErrors({
                    ...errors,
                    description: descriptionValidation(e.target.value),
                  });
                }}
                placeholder="e.g., 'Show me the list of pods in the default namespace'"
                required={true}
                error={errors.description}
                fieldType='textfield'
                multiline={true}
                rows={3}
              />

              <FormField
                label='Expected Response'
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                  setErrors({
                    ...errors,
                    description: descriptionValidation(e.target.value),
                  });
                }}
                placeholder="e.g., 'Here is the list of pods in the default namespace'"
                required={true}
                error={errors.description}
                fieldType='textfield'
                multiline={true}
                rows={3}
              />

              <FormField
                label='Connection Test'
                description='Verify MCP server connectivity and authentication'
                fieldType='custom'
                customRender={
                  <Box sx={{ mt: 1 }}>
                    <Button
                      variant='contained'
                      sx={{
                        backgroundColor: colors.success,
                        '&:hover': {
                          backgroundColor: colors.success,
                        },
                        textTransform: 'none',
                        fontWeight: 500,
                      }}
                    >
                      Test Connection
                    </Button>
                  </Box>
                }
              />
            </FormCard>
          </Box>
        )}

        {/* Advanced Resilience Card - MCP */}
        {toolType === 'mcp' && (
          <Box ref={step7Ref}>
            <FormCard
              title='Advanced Resilience'
              description='Configure retry policies, circuit breakers, and resource limits'
              number={7}
              columns={1}
              expand={true}
              sx={getActiveStepSx(7)}
            >
              <FormField
                label='Retry Policy'
                description='Backoff strategy for errors.'
                value={retryPolicy}
                onChange={(e) => setRetryPolicy(e.target.value)}
                placeholder='{"max":3}'
                fieldType='textfield'
                multiline={true}
                rows={2}
              />

              <FormField
                label='Circuit Breaker'
                description='Trip on consecutive failures.'
                value={circuitBreaker}
                onChange={(e) => setCircuitBreaker(e.target.checked)}
                fieldType='custom'
                customRender={
                  <Box sx={{ display: 'flex', alignItems: 'center', mt: 1 }}>
                    <FormControlLabel
                      control={
                        <Switch
                          checked={circuitBreaker}
                          onChange={(e) => setCircuitBreaker(e.target.checked)}
                          sx={{
                            '& .MuiSwitch-switchBase.Mui-checked': {
                              color: colors.success,
                            },
                            '& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track': {
                              backgroundColor: colors.success,
                            },
                          }}
                        />
                      }
                      label={circuitBreaker ? 'On' : 'Off'}
                      sx={{
                        ml: 0,
                        '& .MuiFormControlLabel-label': {
                          fontSize: '14px',
                          color: colors.text.secondary,
                        },
                      }}
                    />
                  </Box>
                }
              />

              <FormField
                label='Caching Policy'
                description='Cache server responses.'
                value={cachingPolicy}
                onChange={(e) => setCachingPolicy(e.target.value)}
                fieldType='dropdown'
                options={[
                  { label: 'None', value: 'None' },
                  { label: '5 min', value: '5min' },
                  { label: '15 min', value: '15min' },
                  { label: '1 h', value: '1h' },
                ]}
              />
            </FormCard>
          </Box>
        )}

        {/* Advanced Details Card - Container */}
        {toolType === 'container' && (
          <FormCard title='Container Configuration' description='Configure the container execution settings' number={2} columns={1}>
            <FormField
              label='Container Image'
              value={containerImage}
              onChange={(e) => {
                setContainerImage(e.target.value);
                setErrors({ ...errors, containerImage: containerImageValidation(e.target.value) });
              }}
              placeholder='e.g., alpine:latest or myrepo/myimage:tag'
              required={true}
              sx={{ width: '50%' }}
              error={errors.containerImage}
              fieldType='textfield'
            />

            <FormField
              label='Container Command'
              description='Optional command to override image ENTRYPOINT'
              value={containerCommand}
              onChange={(e) => setContainerCommand(e.target.value)}
              placeholder='e.g., /bin/sh or printenv'
              sx={{ width: '50%' }}
              fieldType='textfield'
            />

            <FormField
              label='Container Arguments'
              description='Optional space-separated arguments'
              value={containerArgs}
              onChange={(e) => setContainerArgs(e.target.value)}
              placeholder='e.g., -c "echo hello" or --verbose'
              sx={{ width: '50%' }}
              fieldType='textfield'
            />
          </FormCard>
        )}

        {/* Action Buttons */}
        <Box display='flex' alignItems='center' justifyContent='flex-end' gap='12px' pt='24px' sx={{ '& button': { minWidth: '140px' } }}>
          <CustomButton text='Cancel' variant='secondary' size='Medium' onClick={() => handleClose('')} disabled={loading} />
          <CustomButton text={editMode ? 'Update' : 'Create Tool'} size='Medium' onClick={() => handleSubmit()} loading={loading} />
        </Box>
      </Box>
    </Box>
  );
};

CreateTool.propTypes = {
  accountId: PropTypes.string,
  handleClose: PropTypes.func,
  allTools: PropTypes.arrayOf(PropTypes.object),
  editMode: PropTypes.bool,
  toolData: PropTypes.object,
  preSelectedToolType: PropTypes.string,
};

export default CreateTool;
