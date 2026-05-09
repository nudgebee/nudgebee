import CustomButton from '@components1/common/NewCustomButton';
import { Box, TextField, Typography, MenuItem } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import Link from 'next/link';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import { getLlmIdentifierValidationMessage } from 'src/utils/common';

const styles = {
  errorText: {
    color: colors.error,
    fontSize: '12px',
    fontWeight: 500,
    mt: 1,
  },
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
      padding: '0px !important',
    },
    '& .MuiInputBase-inputMultiline': {
      padding: '8px 12px !important', // This targets multiline input specifically
    },
  },
  requiredStar: {
    color: colors.border.error,
  },
};

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
    // In edit mode, only check for duplicate if the name has changed
    if (
      (!editMode && allTools.some((tool) => tool.name === name)) ||
      (editMode && toolData && name !== toolData.name && allTools.some((tool) => tool.name === name))
    ) {
      return 'Tool name already exists';
    }

    // Use the new identifier validation
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
    <Box p={5}>
      <Box display='flex' flexDirection='column' width='100%' mb={3} gap='20px'>
        <Box>
          <Typography sx={styles.label}>
            Name <span style={styles.requiredStar}>*</span>
          </Typography>
          <TextField
            value={name}
            required
            onChange={(e) => {
              setName(e.target.value);
              setErrors({ ...errors, name: nameValidation(e.target.value) });
            }}
            variant='outlined'
            fullWidth
            placeholder='Enter tool name'
            sx={styles.inputField}
            error={!!errors.name}
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
              setErrors({ ...errors, description: descriptionValidation(e.target.value) });
            }}
            variant='outlined'
            fullWidth
            placeholder='Describe what this tool does'
            sx={styles.inputField}
            error={!!errors.description}
          />
          {errors.description && <Typography sx={styles.errorText}>{errors.description}</Typography>}
        </Box>
        {editMode && (
          <Box>
            <Typography sx={styles.label}>
              Status <span style={styles.requiredStar}>*</span>
            </Typography>
            <TextField
              select
              value={toolStatus}
              required
              onChange={(e) => setToolStatus(e.target.value)}
              variant='outlined'
              fullWidth
              sx={styles.inputField}
            >
              <MenuItem value='enabled'>Enabled</MenuItem>
              <MenuItem value='disabled'>Disabled</MenuItem>
            </TextField>
          </Box>
        )}

        {toolType === 'container' && (
          <>
            <Box>
              <Typography sx={styles.label}>
                Container Image <span style={styles.requiredStar}>*</span>
              </Typography>
              <TextField
                value={containerImage}
                required
                onChange={(e) => {
                  setContainerImage(e.target.value);
                  setErrors({ ...errors, containerImage: containerImageValidation(e.target.value) });
                }}
                variant='outlined'
                fullWidth
                placeholder='e.g., alpine:latest or myrepo/myimage:tag'
                sx={styles.inputField}
                error={!!errors.containerImage}
              />
              {errors.containerImage && <Typography sx={styles.errorText}>{errors.containerImage}</Typography>}
            </Box>
            <Box>
              <Typography sx={styles.label}>Container Command (Optional)</Typography>
              <TextField
                value={containerCommand}
                onChange={(e) => {
                  setContainerCommand(e.target.value);
                  // Optional: Add validation if command has specific format requirements when provided
                  // setErrors({ ...errors, containerCommand: containerCommandValidation(e.target.value) });
                }}
                variant='outlined'
                fullWidth
                placeholder='e.g., /bin/sh or printenv (overrides image ENTRYPOINT)'
                sx={styles.inputField}
              />
            </Box>
            <Box>
              <Typography sx={styles.label}>Container Arguments (Optional, space-separated)</Typography>
              <TextField
                value={containerArgs}
                onChange={(e) => setContainerArgs(e.target.value)}
                variant='outlined'
                fullWidth
                placeholder='e.g., -c "echo hello" or --verbose'
                sx={styles.inputField}
              />
            </Box>
          </>
        )}
      </Box>
      {!editMode && (
        <Box
          sx={{
            p: 1.5,
            borderRadius: '6px',
            border: `1px solid ${colors.background.warningButtonHover}`,
            backgroundColor: colors.background.warningLight,
            mb: 3,
          }}
        >
          <Typography sx={{ fontSize: '13px', color: colors.text.warning }}>
            <strong>Note:</strong> To add an MCP integration, go to{' '}
            <Link href='/accounts/account-form?cloudProvider=mcp' style={{ color: colors.text.infoDark, fontWeight: 600 }}>
              Admin &gt; Integrations &gt; MCP
            </Link>
          </Typography>
        </Box>
      )}
      <Box display='flex' alignItems='center' justifyContent='flex-end' gap='12px' pt='24px' sx={{ '& button': { minWidth: '140px' } }}>
        <CustomButton
          text='Cancel'
          variant='secondary'
          size='Medium'
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
        />
        <CustomButton text={editMode ? 'Update' : 'Submit'} size='Medium' onClick={() => handleSubmit()} loading={loading} />
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
