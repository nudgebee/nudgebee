import React, { useState, useEffect } from 'react';
import { Modal } from './modal';
import { Typography, TextField, Box } from '@mui/material';
import apiIntegrations from '@api1/integrations';
import apiTicketIntegrations from '@api1/tickets';
import { infoIcon } from '@assets';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import PropTypes from 'prop-types';
import CustomButton from './NewCustomButton';
import CustomTooltip from './CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';

// Pure display placeholder shown in edit mode to indicate a token is stored.
// The real token is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const TOKEN_PLACEHOLDER = '••••••••';

const JiraAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [jiraName, setJiraName] = useState('');
  const [jiraAccUrl, setJiraAccUrl] = useState('');
  const [jiraToken, setJiraToken] = useState('');
  const [jiraUserName, setJiraUserName] = useState('');
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);

  // Prefill fields in edit mode
  useEffect(() => {
    if (isEdit && editConfig) {
      setJiraName(editConfig.name || '');
      setJiraAccUrl(editConfig.url || '');
      setJiraToken(TOKEN_PLACEHOLDER);
      setJiraUserName(editConfig.username || '');
    } else {
      setJiraName('');
      setJiraAccUrl('');
      setJiraToken('');
      setJiraUserName('');
    }
    setValidationError({});
    setIsSubmitting(false);
    setIsTesting(false);
  }, [isEdit, editConfig, openModal]);

  const handleJiraAccountClose = (shouldRefresh = false) => {
    setJiraName('');
    setJiraAccUrl('');
    setJiraToken('');
    setJiraUserName('');
    handleClose(shouldRefresh);
    setValidationError({});
    setIsSubmitting(false);
    setIsTesting(false);
  };

  // Empty token, or unchanged placeholder in edit mode, both mean "keep stored value".
  // Trim guards against pasted tokens with leading/trailing whitespace.
  const tokenForSubmit = () => {
    const trimmed = jiraToken.trim();
    return trimmed && trimmed !== TOKEN_PLACEHOLDER ? trimmed : '';
  };

  const handleTestConnection = async () => {
    if (!jiraName?.trim() || !jiraAccUrl?.trim() || !jiraUserName?.trim()) {
      snackbar.error('Please fill name, URL and username before testing');
      return;
    }
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: jiraName.trim(),
        url: jiraAccUrl.trim(),
        username: jiraUserName.trim(),
        password: tokenForSubmit(),
        tool: 'jira',
      });
      if (result?.success) {
        snackbar.success('Jira connection successful');
      } else {
        snackbar.error(result?.error || 'Jira connection test failed');
      }
    } catch {
      snackbar.error('Failed to test Jira connection');
    } finally {
      setIsTesting(false);
    }
  };

  const submitForm = (data, cloud_provider) => {
    setIsSubmitting(true);
    const bodyData = {
      name: data.jiraName,
      password: tokenForSubmit(),
      url: data.jiraAccUrl,
      username: data.jiraUserName,
      tool: 'jira',
    };
    apiIntegrations
      .listTicketConfigurationsByTool({ tool: 'jira' })
      .then((res) => {
        const jiraConfList = res?.data || [];
        let duplicateExists = false;
        if (isEdit) {
          duplicateExists = jiraConfList.some((config) => config.name === bodyData.name && config.id !== editConfig.id);
        } else {
          duplicateExists = jiraConfList.some((config) => config.name === bodyData.name);
        }
        if (duplicateExists) {
          setValidationError({ name: `${bodyData.name} already exists. Please choose a different name.` });
          setIsSubmitting(false);
          return;
        }

        if (isEdit) {
          if (bodyData.password && bodyData.password.length < 8) {
            setValidationError({ password: 'Password must be at least 8 characters long.' });
            setIsSubmitting(false);
            return;
          }

          // Update flow
          apiIntegrations
            .createTicketIntegration({
              id: editConfig.id,
              name: bodyData.name,
              url: bodyData.url,
              username: bodyData.username,
              password: bodyData.password || undefined,
              tool: 'jira',
            })
            .then((res) => {
              const { data } = res;
              const successId = data?.data?.ticket_integration_create_config?.id;

              if (successId) {
                snackbar.success('Jira account updated successfully.');
                handleJiraAccountClose(true);
              } else if (data?.data?.errors?.length > 0) {
                snackbar.error(data.data.errors[0]?.message || 'Failed to Update Jira Account');
                handleJiraAccountClose();
              } else {
                snackbar.error('Failed to Update Jira Account');
                handleJiraAccountClose();
              }
            })
            .catch((error) => {
              const errorMessage = error?.response?.data?.errors?.[0]?.message || 'Failed to Update Jira Account';
              snackbar.error(errorMessage);
            })
            .finally(() => {
              setIsSubmitting(false);
            });
        } else {
          // Add flow
          apiIntegrations
            .createTicketIntegration(bodyData)
            .then((res) => {
              const { data } = res;
              const successId = data?.data?.ticket_integration_create_config?.id;

              if (successId) {
                const message = getAccountCreationSuccessMsg(cloud_provider);
                apiTicketIntegrations.listTicketConfigurations({}, true);
                snackbar.success(message);
                handleJiraAccountClose(true);
              } else if (data?.data?.errors?.length > 0) {
                snackbar.error(data.data.errors[0]?.message || 'Failed to Add Jira Account');
                handleJiraAccountClose();
              } else {
                snackbar.error('Failed to Add Jira Account');
                handleJiraAccountClose();
              }
            })
            .catch((error) => {
              const errorMessage = error?.response?.data?.errors?.[0]?.message || 'Failed to Add Jira Account';
              snackbar.error(errorMessage);
            })
            .finally(() => {
              setIsSubmitting(false);
            });
        }
      })
      .catch(() => {
        setIsSubmitting(false);
      });
  };

  return (
    <Modal width='md' open={openModal} handleClose={handleClose} title={isEdit ? 'Edit Jira Account' : 'Add Jira Account'} loader={isSubmitting}>
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
              A unique name to identify this Jira account configuration
            </Typography>
            <TextField
              value={jiraName}
              size='small'
              fullWidth
              id='jiraName'
              label='Name'
              required
              onChange={(e) => {
                const value = e.target.value;
                setJiraName(value);
                setValidationError((prev) => ({
                  ...prev,
                  name: '',
                }));
              }}
              error={!!validationError.name}
              helperText={validationError.name}
              disabled={isSubmitting}
            />
          </Box>

          {/* Account URL Field */}
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
              Your Jira instance URL (e.g., https://your-domain.atlassian.net)
            </Typography>
            <TextField
              value={jiraAccUrl}
              size='small'
              fullWidth
              id='jiraAccUrl'
              label='Account URL'
              required
              onChange={(e) => setJiraAccUrl(e.target.value)}
              disabled={isSubmitting}
            />
          </Box>

          {/* User Name Field */}
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
              The email address associated with your Jira account
            </Typography>
            <TextField
              value={jiraUserName}
              size='small'
              fullWidth
              id='jiraUserName'
              label='User Name'
              required
              onChange={(e) => setJiraUserName(e.target.value)}
              disabled={isSubmitting}
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
                  : 'API token for authentication with Jira'}
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
              value={jiraToken}
              size='small'
              fullWidth
              id='jiraToken'
              label='Token'
              required={!isEdit}
              onFocus={() => {
                if (jiraToken === TOKEN_PLACEHOLDER) setJiraToken('');
              }}
              onChange={(e) => setJiraToken(e.target.value)}
              type='password'
              disabled={isSubmitting || isTesting}
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
          id='cancel-create-account'
          text='Cancel'
          variant='secondary'
          size='Medium'
          onClick={handleClose}
          disabled={isSubmitting || isTesting}
        />
        <CustomButton
          id='test-jira-connection'
          text={isTesting ? 'Testing...' : 'Test Connection'}
          variant='secondary'
          size='Medium'
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        />
        <CustomButton
          size='Medium'
          id={isEdit ? 'update-jira-acc' : 'create-jira-acc'}
          text={isEdit ? 'Update' : 'Save'}
          disabled={isSubmitting || isTesting}
          onClick={() => {
            submitForm(
              {
                jiraName: jiraName,
                jiraToken: jiraToken,
                jiraAccUrl: jiraAccUrl,
                jiraUserName: jiraUserName,
              },
              'JIRA'
            );
          }}
          label={isEdit ? 'Update Jira' : 'Save Jira'}
        />
      </Box>
    </Modal>
  );
};

JiraAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
    username: PropTypes.string,
  }),
};

export default JiraAccountModal;
