import React, { useState, useEffect, useRef } from 'react';
import { Modal } from '@components1/ds/Modal';
import { Typography, Box } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import apiIntegrations from '@api1/integrations';
import { infoIcon } from '@assets';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import apiTicketIntegrations from '@api1/tickets';
import PropTypes from 'prop-types';
import CustomTooltip from './CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';
import { getAppBaseUrl } from '@lib/externalUrls';

const HELPER_TEXT_SX = {
  color: 'var(--ds-gray-500)',
  fontSize: 'var(--ds-text-caption)',
  lineHeight: 1.5,
};

// Pure display placeholder shown in edit mode to indicate a token is stored.
// The real token is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const TOKEN_PLACEHOLDER = '••••••••';

const GithubAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const [githubAppName, setGithubAppName] = useState('nudgebee');
  const isEdit = !!editConfig;
  const [githubName, setGithubName] = useState('');
  const [githubToken, setGithubToken] = useState('');
  const [githubUserName, setGithubUserName] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [errors, setErrors] = useState({});
  const [authType, setAuthType] = useState('github-app');
  const popupCheckIntervalRef = useRef(null);
  const successTimeoutRef = useRef(null);

  const githubUrl = 'api.github.com';

  useEffect(() => {
    // NEXT_PUBLIC_* env vars are inlined into the client bundle at build time,
    // so we can read directly without a network round-trip.
    setGithubAppName(process.env.NEXT_PUBLIC_GITHUB_APP_NAME || 'nudgebee');
  }, []);

  // Prefill fields in edit mode
  useEffect(() => {
    if (isEdit && editConfig) {
      setGithubName(editConfig.name || '');
      setGithubToken(TOKEN_PLACEHOLDER);
      setGithubUserName(editConfig.username || '');
    } else {
      setGithubName('');
      setGithubToken('');
      setGithubUserName('');
    }
    setErrors({});
    setIsSubmitting(false);
    setIsTesting(false);
  }, [isEdit, editConfig, openModal]);

  // Empty token, or unchanged placeholder in edit mode, both mean "keep stored value".
  // Trim guards against pasted tokens with leading/trailing whitespace.
  const tokenForSubmit = () => {
    const trimmed = githubToken.trim();
    return trimmed && trimmed !== TOKEN_PLACEHOLDER ? trimmed : '';
  };

  const handleTestConnection = async () => {
    if (!githubName.trim() || !githubUserName.trim()) {
      snackbar.error('Please fill name and username before testing');
      return;
    }
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: githubName.trim(),
        url: githubUrl,
        username: githubUserName.trim(),
        password: tokenForSubmit(),
        tool: 'github',
      });
      if (result?.success) {
        snackbar.success('Github connection successful');
      } else {
        snackbar.error(result?.error || 'Github connection test failed');
      }
    } catch {
      snackbar.error('Failed to test Github connection');
    } finally {
      setIsTesting(false);
    }
  };

  // Listen for popup messages
  useEffect(() => {
    const handleMessage = (event) => {
      if (event.origin !== window.location.origin) {
        return;
      }

      if (!openModal) {
        return;
      }

      if (event.data?.type === 'GITHUB_AUTH_SUCCESS') {
        // Clear the popup check interval to prevent race condition
        if (popupCheckIntervalRef.current) {
          clearInterval(popupCheckIntervalRef.current);
          popupCheckIntervalRef.current = null;
        }
        setIsSubmitting(false);

        // Refresh the listing
        apiTicketIntegrations.listTicketConfigurations({ tool: 'github' }, true);

        // Clear any existing success timeout
        if (successTimeoutRef.current) {
          clearTimeout(successTimeoutRef.current);
        }

        // Close modal and show success snackbar
        successTimeoutRef.current = setTimeout(() => {
          handleCloseGithubModal(true);
          snackbar.success('Github account added successfully.');
          successTimeoutRef.current = null;
        }, 1000);
      } else if (event.data?.type === 'GITHUB_AUTH_ERROR') {
        // Clear the popup check interval to prevent race condition
        if (popupCheckIntervalRef.current) {
          clearInterval(popupCheckIntervalRef.current);
          popupCheckIntervalRef.current = null;
        }
        setIsSubmitting(false);
        snackbar.error('Failed to add Github account');
        handleCloseGithubModal();
      }
    };

    window.addEventListener('message', handleMessage);
    return () => {
      window.removeEventListener('message', handleMessage);
      // Clean up any pending timeouts/intervals on unmount
      if (successTimeoutRef.current) {
        clearTimeout(successTimeoutRef.current);
        successTimeoutRef.current = null;
      }
      if (popupCheckIntervalRef.current) {
        clearInterval(popupCheckIntervalRef.current);
        popupCheckIntervalRef.current = null;
      }
    };
  }, [openModal]);

  const handleCloseGithubModal = (trigger = true) => {
    setGithubName('');
    setGithubToken('');
    setGithubUserName('');
    setAuthType('github-app');
    setErrors({});
    handleClose(trigger);
  };

  const validateFields = () => {
    const newErrors = {};

    if (authType === 'user-token') {
      const trimmedName = githubName.trim();
      const tokenEntered = githubToken.trim() === TOKEN_PLACEHOLDER ? '' : githubToken.trim();
      const trimmedUserName = githubUserName.trim();

      if (!trimmedName) {
        newErrors.githubName = 'Name is required';
      }

      if (!isEdit && !tokenEntered) {
        newErrors.githubToken = 'Token is required';
      } else if (isEdit && tokenEntered && tokenEntered.length < 8) {
        newErrors.githubToken = 'Token must be at least 8 characters long.';
      }
      if (!trimmedUserName) {
        newErrors.githubUserName = 'User Name is required';
      }
    }

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };

  const submitForm = async (data, cloud_provider) => {
    if (!validateFields()) {
      return;
    }

    setIsSubmitting(true);
    const bodyData = {
      name: data.name,
      password: tokenForSubmit(),
      url: data.url,
      tool: 'github',
      username: data.username,
    };

    try {
      // Check for duplicate names
      const res = await apiIntegrations.listTicketConfigurationsByTool({ tool: 'github' });
      const githubConfList = res?.data || [];
      let duplicateExists = false;

      if (isEdit) {
        duplicateExists = githubConfList.some((config) => config.name === bodyData.name && config.id !== editConfig.id);
      } else {
        duplicateExists = githubConfList.some((config) => config.name === bodyData.name);
      }

      if (duplicateExists) {
        setErrors({ githubName: `${bodyData.name} already exists. Please choose a different name.` });
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
          tool: 'github',
        });

        const { data } = updateRes;
        if (data?.data?.ticket_integration_create_config?.id) {
          await apiTicketIntegrations.listTicketConfigurations({ tool: 'github' }, true);
          snackbar.success('Github account updated successfully.');
          handleCloseGithubModal(true);
        } else if (data?.data?.errors?.length > 0) {
          snackbar.error(data.data.errors[0]?.message || 'Failed to update Github account');
        } else {
          snackbar.error('Failed to update Github account');
        }
      } else {
        // Add new account
        const response = await apiIntegrations.createTicketIntegration(bodyData);

        if (response?.data?.data?.ticket_integration_create_config?.id) {
          const message = getAccountCreationSuccessMsg(cloud_provider);
          await apiTicketIntegrations.listTicketConfigurations({}, true);
          snackbar.success(message);
          handleCloseGithubModal(true);
        } else if (response?.data?.errors?.length > 0) {
          snackbar.error(response.data.errors[0]?.message || 'Failed to Add Github Account');
        } else {
          handleCloseGithubModal();
        }
      }
    } catch (error) {
      console.error('Error:', error);
      snackbar.error(`Failed to ${isEdit ? 'Update' : 'Add'} Github Account`);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={() => handleCloseGithubModal()}
      title={isEdit ? 'Edit Github Account' : 'Add Github Account'}
      loader={isSubmitting}
    >
      <Box sx={{ minHeight: '200px', pt: 3, pb: 1 }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {/* Auth Type Selection */}
          {!isEdit && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
              <Typography component='span' sx={{ ...HELPER_TEXT_SX, color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                Authentication method
              </Typography>
              <Typography component='span' sx={HELPER_TEXT_SX}>
                Choose authentication method for connecting to GitHub
              </Typography>
              <Box sx={{ alignSelf: 'flex-start' }}>
                <ToggleGroup
                  id='github-auth-type'
                  ariaLabel='Authentication method'
                  selection='single'
                  size='md'
                  value={authType}
                  onChange={setAuthType}
                  options={[
                    { value: 'github-app', label: 'Application', disabled: isSubmitting },
                    { value: 'user-token', label: 'User Token', disabled: isSubmitting },
                  ]}
                />
              </Box>
            </Box>
          )}

          {/* Github App Authentication */}
          {authType === 'github-app' && !isEdit && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              <Typography component='span' sx={HELPER_TEXT_SX}>
                Click the button below to authenticate with nudgebee GitHub App. This will open a popup window to complete the authentication process.
              </Typography>
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'flex-start',
                  gap: 1,
                  p: 1.25,
                  borderRadius: 'var(--ds-radius-md)',
                  backgroundColor: colors.background.primaryLightest,
                  border: `1px solid ${colors.border.primaryLight}`,
                }}
              >
                <Typography component='span' sx={HELPER_TEXT_SX}>
                  <strong>Note:</strong> GitHub App integration supports only organization-level repositories. Personal repositories are not
                  supported.
                </Typography>
              </Box>
              <Button
                id='authenticate-btn'
                tone='primary'
                size='md'
                loading={isSubmitting}
                disabled={isSubmitting}
                onClick={() => {
                  setIsSubmitting(true);
                  const popup = window.open(
                    `https://github.com/apps/${githubAppName}/installations/new?redirect_uri=${getAppBaseUrl()}/api/integrations/github/callback`,
                    'github-auth',
                    'width=600,height=700,scrollbars=yes,resizable=yes'
                  );

                  // Store interval ID in ref so it can be cleared by message handler
                  popupCheckIntervalRef.current = setInterval(() => {
                    if (popup?.closed) {
                      clearInterval(popupCheckIntervalRef.current);
                      popupCheckIntervalRef.current = null;
                      setIsSubmitting(false);
                      handleCloseGithubModal();
                    }
                  }, 1000);
                }}
              >
                Authenticate with Github App
              </Button>
            </Box>
          )}

          {/* User Token Fields */}
          {(authType === 'user-token' || isEdit) && (
            <>
              <Input
                value={githubName}
                size='sm'
                id='githubName'
                label='Name'
                instructionText='A unique name to identify this GitHub account configuration'
                required
                onChange={(value) => {
                  setGithubName(value);
                  if (errors.githubName) {
                    setErrors((prev) => ({ ...prev, githubName: '' }));
                  }
                }}
                disabled={isSubmitting}
                error={errors.githubName}
              />

              {authType === 'user-token' && (
                <Input
                  value={githubUserName}
                  size='sm'
                  id='githubUserName'
                  label='Username'
                  instructionText='Your GitHub username'
                  required
                  onChange={(value) => {
                    setGithubUserName(value);
                    if (errors.githubUserName) {
                      setErrors((prev) => ({ ...prev, githubUserName: '' }));
                    }
                  }}
                  disabled={isSubmitting}
                  error={errors.githubUserName}
                />
              )}

              {authType === 'user-token' && (
                <Input
                  value={githubToken}
                  size='sm'
                  id='githubToken'
                  label='Token'
                  instructionText={
                    isEdit ? (
                      <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5 }}>
                        A token is stored. Click the field to enter a new one, or leave unchanged to keep it.
                        <CustomTooltip title='Stored token will be used if left unchanged'>
                          <Box component='span' sx={{ cursor: 'pointer', display: 'inline-flex', alignItems: 'center' }}>
                            <SafeIcon src={infoIcon} alt='info' width={14} height={14} />
                          </Box>
                        </CustomTooltip>
                      </Box>
                    ) : (
                      'Personal access token for authentication with GitHub'
                    )
                  }
                  required={!isEdit}
                  onFocus={() => {
                    if (githubToken === TOKEN_PLACEHOLDER) setGithubToken('');
                  }}
                  onChange={(value) => {
                    setGithubToken(value);
                    if (errors.githubToken) {
                      setErrors((prev) => ({ ...prev, githubToken: '' }));
                    }
                  }}
                  type='password'
                  disabled={isSubmitting || isTesting}
                  error={errors.githubToken}
                />
              )}
            </>
          )}
        </Box>
      </Box>
      <Box
        sx={{
          display: 'flex',
          gap: 'var(--ds-space-3)',
          justifyContent: 'flex-end',
          mt: 3,
          mb: 4,
        }}
      >
        <Button id='cancel-btn' tone='secondary' size='md' onClick={() => handleCloseGithubModal()} disabled={isSubmitting || isTesting}>
          Cancel
        </Button>
        {(authType === 'user-token' || isEdit) && (
          <Button
            id='test-github-connection'
            tone='secondary'
            size='md'
            loading={isTesting}
            onClick={handleTestConnection}
            disabled={isSubmitting || isTesting}
          >
            Test Connection
          </Button>
        )}
        {(authType === 'user-token' || isEdit) && (
          <Button
            id={isEdit ? 'update-github-acc' : 'create-github-acc'}
            tone='primary'
            size='md'
            loading={isSubmitting}
            disabled={isSubmitting || isTesting}
            onClick={() => {
              submitForm(
                {
                  name: githubName,
                  password: githubToken,
                  url: githubUrl,
                  username: githubUserName,
                },
                'GITHUB'
              );
            }}
          >
            {isEdit ? 'Update' : 'Save'}
          </Button>
        )}
      </Box>
    </Modal>
  );
};

GithubAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
    username: PropTypes.string,
  }),
};

export default GithubAccountModal;
