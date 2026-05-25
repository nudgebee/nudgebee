import React, { useState, useEffect } from 'react';
import { Modal } from './modal';
import { Typography, TextField, Box } from '@mui/material';
import apiIntegrations from '@api1/integrations';
import { infoIcon } from '@assets';
import apiTicketIntegrations from '@api1/tickets';
import PropTypes from 'prop-types';
import CustomButton from './NewCustomButton';
import CustomTooltip from './CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';

// Pure display placeholder shown in edit mode to indicate a token is stored.
// The real token is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const TOKEN_PLACEHOLDER = '••••••••';

const GitlabAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [name, setName] = useState('');
  const [url, setUrl] = useState('https://gitlab.com');
  const [username, setUsername] = useState('');
  const [token, setToken] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [errors, setErrors] = useState({});

  // Prefill fields in edit mode
  useEffect(() => {
    if (isEdit && editConfig) {
      setName(editConfig.name || '');
      setUrl(editConfig.url || 'https://gitlab.com');
      setUsername(editConfig.username || '');
      setToken(TOKEN_PLACEHOLDER);
    } else {
      setName('');
      setUrl('https://gitlab.com');
      setUsername('');
      setToken('');
    }
    setErrors({});
    setIsSubmitting(false);
    setIsTesting(false);
  }, [isEdit, editConfig, openModal]);

  const handleCloseModal = (trigger = true) => {
    setName('');
    setUrl('https://gitlab.com');
    setUsername('');
    setToken('');
    setErrors({});
    setIsTesting(false);
    handleClose(trigger);
  };

  // Empty token, or unchanged placeholder in edit mode, both mean "keep stored value".
  const tokenForSubmit = () => (token && token !== TOKEN_PLACEHOLDER ? token.trim() : '');

  const handleTestConnection = async () => {
    if (!name.trim() || !username.trim()) {
      snackbar.error('Please fill name and username before testing');
      return;
    }
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: name.trim(),
        url: url.trim() || 'https://gitlab.com',
        username: username.trim(),
        password: tokenForSubmit(),
        tool: 'gitlab',
      });
      if (result?.success) {
        snackbar.success('GitLab connection successful');
      } else {
        snackbar.error(result?.error || 'GitLab connection test failed');
      }
    } catch {
      snackbar.error('Failed to test GitLab connection');
    } finally {
      setIsTesting(false);
    }
  };

  const validateFields = () => {
    const newErrors = {};
    const trimmedName = name.trim();
    const trimmedUsername = username.trim();
    const tokenEntered = tokenForSubmit();

    if (!trimmedName) {
      newErrors.name = 'Name is required';
    }
    if (!trimmedUsername) {
      newErrors.username = 'Username is required';
    }
    if (!isEdit && !tokenEntered) {
      newErrors.token = 'Token is required';
    } else if (isEdit && tokenEntered && tokenEntered.length < 8) {
      newErrors.token = 'Token must be at least 8 characters long.';
    }

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };

  const submitForm = async () => {
    if (!validateFields()) {
      return;
    }

    setIsSubmitting(true);
    const bodyData = {
      name: name.trim(),
      password: tokenForSubmit(),
      url: url.trim() || 'https://gitlab.com',
      tool: 'gitlab',
      username: username.trim(),
    };

    try {
      // Check for duplicate names
      const res = await apiIntegrations.listTicketConfigurationsByTool({ tool: 'gitlab' });
      const configList = res?.data || [];
      let duplicateExists = false;

      if (isEdit) {
        duplicateExists = configList.some((config) => config.name === bodyData.name && config.id !== editConfig.id);
      } else {
        duplicateExists = configList.some((config) => config.name === bodyData.name);
      }

      if (duplicateExists) {
        setErrors({ name: `${bodyData.name} already exists. Please choose a different name.` });
        setIsSubmitting(false);
        return;
      }

      if (isEdit) {
        // Update existing account
        const updateRes = await apiIntegrations.createTicketIntegration({
          id: editConfig.id,
          name: bodyData.name,
          url: bodyData.url,
          username: bodyData.username,
          password: bodyData.password || undefined,
          tool: 'gitlab',
        });

        const { data } = updateRes;
        if (data?.data?.ticket_integration_create_config?.id) {
          await apiTicketIntegrations.listTicketConfigurations({ tool: 'gitlab' }, true);
          snackbar.success('GitLab account updated successfully.');
          handleCloseModal(true);
        } else if (data?.data?.errors?.length > 0) {
          snackbar.error(data.data.errors[0]?.message || 'Failed to update GitLab account');
        } else {
          snackbar.error('Failed to update GitLab account');
        }
      } else {
        // Add new account
        const response = await apiIntegrations.createTicketIntegration(bodyData);

        if (response?.data?.data?.ticket_integration_create_config?.id) {
          await apiTicketIntegrations.listTicketConfigurations({}, true);
          snackbar.success('GitLab account added successfully.');
          handleCloseModal(true);
        } else if (response?.data?.errors?.length > 0) {
          snackbar.error(response.data.errors[0]?.message || 'Failed to Add GitLab Account');
        } else {
          handleCloseModal();
        }
      }
    } catch (error) {
      console.error('Error:', error);
      snackbar.error(`Failed to ${isEdit ? 'Update' : 'Add'} GitLab Account`);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={() => handleCloseModal()}
      title={isEdit ? 'Edit GitLab Account' : 'Add GitLab Account'}
      loader={isSubmitting}
    >
      <Box sx={{ minHeight: '200px', pt: 3, pb: 1 }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {/* Name Field */}
          <Box sx={{ mb: 0.5 }}>
            <Typography
              variant='body2'
              sx={{
                color: colors.text.secondaryDark,
                fontSize: '12px',
                lineHeight: 1.5,
                mb: 1,
                pl: 0.5,
              }}
            >
              A unique name to identify this GitLab account configuration
            </Typography>
            <TextField
              value={name}
              size='small'
              fullWidth
              id='gitlabName'
              label='Name'
              required
              onChange={(e) => {
                setName(e.target.value);
                if (errors.name) {
                  setErrors((prev) => ({ ...prev, name: '' }));
                }
              }}
              disabled={isSubmitting}
              error={!!errors.name}
              helperText={errors.name}
            />
          </Box>

          {/* URL Field */}
          <Box sx={{ mb: 0.5 }}>
            <Typography
              variant='body2'
              sx={{
                color: colors.text.secondaryDark,
                fontSize: '12px',
                lineHeight: 1.5,
                mb: 1,
                pl: 0.5,
              }}
            >
              GitLab instance URL (use https://gitlab.com for GitLab.com, or your self-hosted URL)
            </Typography>
            <TextField
              value={url}
              size='small'
              fullWidth
              id='gitlabUrl'
              label='GitLab URL'
              placeholder='https://gitlab.com'
              onChange={(e) => {
                setUrl(e.target.value);
              }}
              disabled={isSubmitting}
            />
          </Box>

          {/* Username Field */}
          <Box sx={{ mb: 0.5 }}>
            <Typography
              variant='body2'
              sx={{
                color: colors.text.secondaryDark,
                fontSize: '12px',
                lineHeight: 1.5,
                mb: 1,
                pl: 0.5,
              }}
            >
              Your GitLab username (not your email address)
            </Typography>
            <TextField
              value={username}
              size='small'
              fullWidth
              id='gitlabUsername'
              label='Username'
              required
              onChange={(e) => {
                setUsername(e.target.value);
                if (errors.username) {
                  setErrors((prev) => ({ ...prev, username: '' }));
                }
              }}
              disabled={isSubmitting}
              error={!!errors.username}
              helperText={errors.username}
            />
          </Box>

          {/* Token Field */}
          <Box sx={{ mb: 0.5 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', mb: 1, pl: 0.5 }}>
              <Typography
                variant='body2'
                sx={{
                  color: colors.text.secondaryDark,
                  fontSize: '12px',
                  lineHeight: 1.5,
                }}
              >
                {isEdit
                  ? 'A token is stored. Click the field to enter a new one, or leave unchanged to keep it.'
                  : 'Personal access token with api scope for authentication'}
              </Typography>
              {isEdit && (
                <CustomTooltip title='Stored token will be used if left unchanged'>
                  <Box ml={1} sx={{ cursor: 'pointer', display: 'flex', alignItems: 'center' }}>
                    <SafeIcon src={infoIcon} alt='info' width={16} height={16} />
                  </Box>
                </CustomTooltip>
              )}
            </Box>
            <TextField
              value={token}
              size='small'
              fullWidth
              id='gitlabToken'
              label='Personal Access Token'
              required={!isEdit}
              onFocus={() => {
                if (token === TOKEN_PLACEHOLDER) setToken('');
              }}
              onChange={(e) => {
                setToken(e.target.value);
                if (errors.token) {
                  setErrors((prev) => ({ ...prev, token: '' }));
                }
              }}
              type='password'
              disabled={isSubmitting || isTesting}
              error={!!errors.token}
              helperText={errors.token}
            />
          </Box>
        </Box>
      </Box>
      <Box
        sx={{
          display: 'flex',
          gap: '12px',
          justifyContent: 'flex-end',
          mt: 3,
          mb: 4,
          button: {
            minWidth: '140px',
          },
        }}
      >
        <CustomButton
          id='cancel-btn'
          text='Cancel'
          variant='secondary'
          size='Medium'
          onClick={() => handleCloseModal()}
          disabled={isSubmitting || isTesting}
        />
        <CustomButton
          id='test-gitlab-connection'
          text={isTesting ? 'Testing...' : 'Test Connection'}
          variant='secondary'
          size='Medium'
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        />
        <CustomButton
          size='Medium'
          id={isEdit ? 'update-gitlab-acc' : 'create-gitlab-acc'}
          text={isEdit ? 'Update' : 'Save'}
          disabled={isSubmitting || isTesting}
          onClick={submitForm}
        />
      </Box>
    </Modal>
  );
};

GitlabAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
    username: PropTypes.string,
  }),
};

export default GitlabAccountModal;
