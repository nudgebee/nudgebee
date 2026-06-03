import React, { useState, useEffect } from 'react';
import { Modal } from '@components1/ds/Modal';
import { Box } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import apiIntegrations from '@api1/integrations';
import apiTicketIntegrations from '@api1/tickets';
import { infoIcon } from '@assets';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import PropTypes from 'prop-types';
import CustomTooltip from './CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import { snackbar } from '@components1/common/snackbarService';

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
          <Input
            value={jiraName}
            size='sm'
            id='jiraName'
            label='Name'
            instructionText='A unique name to identify this Jira account configuration'
            required
            onChange={(value) => {
              setJiraName(value);
              setValidationError((prev) => ({
                ...prev,
                name: '',
              }));
            }}
            error={validationError.name}
            disabled={isSubmitting}
          />

          <Input
            value={jiraAccUrl}
            size='sm'
            id='jiraAccUrl'
            label='Account URL'
            instructionText='Your Jira instance URL (e.g., https://your-domain.atlassian.net)'
            required
            onChange={setJiraAccUrl}
            disabled={isSubmitting}
          />

          <Input
            value={jiraUserName}
            size='sm'
            id='jiraUserName'
            label='User Name'
            instructionText='The email address associated with your Jira account'
            required
            onChange={setJiraUserName}
            disabled={isSubmitting}
          />

          <Input
            value={jiraToken}
            size='sm'
            id='jiraToken'
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
                'API token for authentication with Jira'
              )
            }
            required={!isEdit}
            onFocus={() => {
              if (jiraToken === TOKEN_PLACEHOLDER) setJiraToken('');
            }}
            onChange={(value) => {
              setJiraToken(value);
              if (validationError.password) {
                setValidationError((prev) => ({ ...prev, password: '' }));
              }
            }}
            type='password'
            disabled={isSubmitting || isTesting}
            error={validationError.password}
          />
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
        <Button id='cancel-create-account' tone='secondary' size='md' onClick={handleClose} disabled={isSubmitting || isTesting}>
          Cancel
        </Button>
        <Button
          id='test-jira-connection'
          tone='secondary'
          size='md'
          loading={isTesting}
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        >
          Test Connection
        </Button>
        <Button
          id={isEdit ? 'update-jira-acc' : 'create-jira-acc'}
          tone='primary'
          size='md'
          loading={isSubmitting}
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
        >
          {isEdit ? 'Update' : 'Save'}
        </Button>
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
