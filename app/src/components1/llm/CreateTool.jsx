import { Box, Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import { Link } from '@components1/ds/Link';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { toast as snackbar } from '@components1/ds/Toast';
import { getLlmIdentifierValidationMessage } from 'src/utils/common';
import { ds } from '@utils/colors';

const STATUS_OPTIONS = [
  { value: 'enabled', label: 'Enabled' },
  { value: 'disabled', label: 'Disabled' },
];

const CreateTool = ({ accountId, handleClose, allTools, editMode = false, toolData = null }) => {
  const [description, setDescription] = React.useState(editMode && toolData ? toolData.description : '');
  const [name, setName] = React.useState(editMode && toolData ? toolData.name : '');
  const [toolType] = React.useState(editMode && toolData ? (toolData.executor_type || '').toLowerCase() : 'container');
  const [toolStatus, setToolStatus] = React.useState(editMode && toolData ? toolData.status : 'enabled');

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
    containerImage: '',
  });

  const containerImageValidation = (image) => {
    if (toolType === 'container' && !image.trim()) {
      return 'Container image cannot be empty.';
    }
    return '';
  };

  const validateForm = () => {
    const newErrors = {
      name: nameValidation(name),
      description: descriptionValidation(description),
      containerImage: toolType === 'container' ? containerImageValidation(containerImage) : '',
    };

    setErrors(newErrors);

    if (toolType === 'container' && newErrors.containerImage) {
      return false;
    }

    return !(newErrors.name || newErrors.description);
  };

  const descriptionValidation = (description) => {
    return !description.trim() ? 'Description cannot be empty.' : '';
  };

  const nameValidation = (name) => {
    if (
      (!editMode && allTools.some((tool) => tool.name === name)) ||
      (editMode && toolData && name !== toolData.name && allTools.some((tool) => tool.name === name))
    ) {
      return 'Tool name already exists';
    }
    return getLlmIdentifierValidationMessage(name);
  };

  const handleSubmit = () => {
    if (!validateForm()) {
      return;
    }
    setLoading(true);

    const baseToolData = {
      description: description,
      name: name,
      schema: {},
    };

    if (editMode) {
      baseToolData.status = toolStatus;
    }

    let specificToolConfig = editMode && toolData?.config ? { config: toolData.config } : {};
    let executorType = toolType;

    if (toolType === 'container') {
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

  return (
    <Box>
      <Box display='flex' flexDirection='column' width='100%' mb='var(--ds-space-3)' gap='var(--ds-space-4)'>
        <Input
          label='Name'
          required
          value={name}
          onChange={(next) => {
            setName(next);
            setErrors({ ...errors, name: nameValidation(next) });
          }}
          placeholder='Enter tool name'
          error={errors.name || undefined}
        />

        <Input
          label='Description'
          required
          value={description}
          onChange={(next) => {
            setDescription(next);
            setErrors({ ...errors, description: descriptionValidation(next) });
          }}
          placeholder='Describe what this tool does'
          error={errors.description || undefined}
        />

        {editMode && (
          <Box sx={{ width: ds.space.mul(5, 10) }}>
            <Select id='tool-status' label='Status' value={toolStatus} onChange={(next) => setToolStatus(next)} options={STATUS_OPTIONS} />
          </Box>
        )}

        {toolType === 'container' && (
          <>
            <Input
              label='Container Image'
              required
              value={containerImage}
              onChange={(next) => {
                setContainerImage(next);
                setErrors({ ...errors, containerImage: containerImageValidation(next) });
              }}
              placeholder='e.g., alpine:latest or myrepo/myimage:tag'
              error={errors.containerImage || undefined}
            />
            <Input
              label='Container Command (Optional)'
              value={containerCommand}
              onChange={(next) => setContainerCommand(next)}
              placeholder='e.g., /bin/sh or printenv (overrides image ENTRYPOINT)'
            />
            <Input
              label='Container Arguments (Optional, space-separated)'
              value={containerArgs}
              onChange={(next) => setContainerArgs(next)}
              placeholder='e.g., -c "echo hello" or --verbose'
            />
          </>
        )}
      </Box>
      {!editMode && (
        <Box
          sx={{
            p: 'var(--ds-space-3)',
            borderRadius: 'var(--ds-radius-md)',
            border: '1px solid var(--ds-amber-200)',
            backgroundColor: 'var(--ds-amber-100)',
            mb: 'var(--ds-space-3)',
          }}
        >
          <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 'var(--ds-text-body)', color: 'var(--ds-amber-700)' }}>
            <strong>Note:</strong> To add an MCP integration, go to{' '}
            <Link href='/accounts/account-form?cloudProvider=mcp' style={{ fontWeight: 'var(--ds-font-weight-semibold)' }} openInNew>
              Admin &gt; Integrations &gt; MCP
            </Link>
          </Typography>
        </Box>
      )}
      <Box display='flex' alignItems='center' justifyContent='flex-end' gap='var(--ds-space-3)' pt='var(--ds-space-4)'>
        <Button
          tone='secondary'
          size='md'
          onClick={() => {
            setDescription('');
            setName('');
            setContainerImage('');
            setContainerCommand('');
            setContainerArgs('');
            handleClose('');
            setErrors({
              name: '',
              description: '',
              containerImage: '',
            });
          }}
          disabled={loading}
        >
          Cancel
        </Button>
        <Button tone='primary' size='md' onClick={() => handleSubmit()} loading={loading}>
          {editMode ? 'Update' : 'Submit'}
        </Button>
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
};

export default CreateTool;
