import React, { useState, useEffect } from 'react';
import { Modal } from '@components1/ds/Modal';
import { Box } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import apiIntegrations from '@api1/integrations';
import apiTicketIntegrations from '@api1/tickets';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import PropTypes from 'prop-types';
import { snackbar } from './snackbarService';

// Pure display placeholder shown in edit mode to indicate a password is stored.
// The real password is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const PASSWORD_PLACEHOLDER = '••••••••';

const ServiceNowAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [accountName, setAccountName] = useState('');
  const [accountUrl, setAccountUrl] = useState('');
  const [accountPassword, setAccountPassword] = useState('');
  const [accountUsername, setAccountUsername] = useState('');
  const [syncKnowledgeBase, setSyncKnowledgeBase] = useState(false);
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);

  useEffect(() => {
    if (openModal) {
      if (isEdit && editConfig) {
        setAccountName(editConfig.name || '');
        setAccountUrl(editConfig.url || '');
        setAccountUsername(editConfig.username || '');
        setAccountPassword(PASSWORD_PLACEHOLDER);
        setSyncKnowledgeBase(!!editConfig.sync_knowledge_base);
      } else {
        setAccountName('');
        setAccountUrl('');
        setAccountUsername('');
        setAccountPassword('');
        setSyncKnowledgeBase(false);
      }
      setValidationError({});
      setHasAttemptedSubmit(false);
      setIsTesting(false);
    }
  }, [openModal, isEdit, editConfig]);

  // Empty password, or unchanged placeholder in edit mode, both mean "keep stored value".
  // Trim guards against pasted passwords with leading/trailing whitespace.
  const passwordForSubmit = () => {
    const trimmed = accountPassword.trim();
    return trimmed && trimmed !== PASSWORD_PLACEHOLDER ? trimmed : '';
  };

  const validateForm = () => {
    const errors = {};

    if (!accountName.trim()) {
      errors.name = 'Name is required';
    }

    if (!accountUrl.trim()) {
      errors.url = 'Instance URL is required';
    }

    if (!accountUsername.trim()) {
      errors.username = 'Username is required';
    }

    // On edit, an unchanged placeholder is valid (stored password is used).
    if (!isEdit && !accountPassword.trim()) {
      errors.password = 'Password is required';
    }

    setValidationError(errors);
    return Object.keys(errors).length === 0;
  };

  const handleTestConnection = async () => {
    if (!accountName.trim() || !accountUrl.trim() || !accountUsername.trim()) {
      snackbar.error('Please fill name, URL and username before testing');
      return;
    }
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: accountName.trim(),
        url: accountUrl.trim(),
        username: accountUsername.trim(),
        password: passwordForSubmit(),
        tool: 'servicenow',
      });
      if (result?.success) {
        snackbar.success('ServiceNow connection successful');
      } else {
        snackbar.error(result?.error || 'ServiceNow connection test failed');
      }
    } catch {
      snackbar.error('Failed to test ServiceNow connection');
    } finally {
      setIsTesting(false);
    }
  };

  const handleAccountClose = (trigger = false) => {
    setAccountName('');
    setAccountUrl('');
    setAccountPassword('');
    setAccountUsername('');
    setSyncKnowledgeBase(false);
    setValidationError({});
    setHasAttemptedSubmit(false);
    setIsSubmitting(false);
    setIsTesting(false);
    handleClose(trigger);
  };

  const isDuplicateName = (toolConfList, name) => toolConfList.some((config) => config.name === name && (!isEdit || config.id !== editConfig.id));

  // Saved via ticket-server (AddTicketConfiguration), which rehydrates the stored
  // password + auth_type when the form omits them on edit. sync_knowledge_base is a
  // non-reserved config value, read back by the KB-sync consumers (llm-server
  // knowledgebase_sync, rag-server) from integration_config_values.
  const buildIntegrationPayload = (data) => {
    const realPassword = passwordForSubmit();
    return {
      ...(isEdit && editConfig?.id && { id: editConfig.id }),
      name: data.accountName,
      url: data.accountUrl,
      username: data.accountUsername,
      auth_type: 'token',
      // Empty password on edit is intentional — ticket-server rehydrates the
      // stored value before validation, so we omit the key rather than send "".
      ...(realPassword ? { password: realPassword } : {}),
      tool: 'servicenow',
      config_values: [{ name: 'sync_knowledge_base', value: String(data.syncKnowledgeBase) }],
    };
  };

  const handleSubmitResponse = async (res, cloud_provider) => {
    const fallbackError = `Failed to ${isEdit ? 'Update' : 'Add'} ServiceNow Account`;
    const responseData = res?.data;
    const successId = responseData?.data?.ticket_integration_create_config?.id;
    if (successId) {
      await apiTicketIntegrations.listTicketConfigurations({}, true);
      snackbar.success(isEdit ? 'ServiceNow account updated successfully' : getAccountCreationSuccessMsg(cloud_provider));
      handleAccountClose(true);
      return;
    }
    snackbar.error(responseData?.errors?.[0]?.message || fallbackError);
  };

  const submitForm = async (data, cloud_provider) => {
    setHasAttemptedSubmit(true);
    if (!validateForm()) {
      return;
    }
    setIsSubmitting(true);

    try {
      const configRes = await apiIntegrations.listTicketConfigurationsByTool({ tool: 'servicenow' });
      if (isDuplicateName(configRes?.data || [], data.accountName)) {
        setValidationError({ name: `${data.accountName} already exists. Please choose a different name.` });
        return;
      }
      const res = await apiIntegrations.createTicketIntegration(buildIntegrationPayload(data));
      await handleSubmitResponse(res, cloud_provider);
    } catch (error) {
      snackbar.error(error?.response?.data?.errors?.[0]?.message || `Failed to ${isEdit ? 'Update' : 'Add'} ServiceNow Account`);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={handleAccountClose}
      title={isEdit ? 'Edit ServiceNow Account' : 'Add ServiceNow Account'}
      loader={isSubmitting}
    >
      <Box sx={{ minHeight: '200px', pt: 3, pb: 1 }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <Input
            value={accountName}
            size='sm'
            id='accountName'
            label='Name'
            instructionText='A unique name to identify this ServiceNow account configuration'
            required
            onChange={(value) => {
              setAccountName(value);
              if (validationError.name) {
                setValidationError((prev) => ({ ...prev, name: '' }));
              }
            }}
            disabled={isSubmitting}
            error={hasAttemptedSubmit ? validationError.name : undefined}
          />

          <Input
            value={accountUrl}
            size='sm'
            id='accountUrl'
            label='Instance URL'
            instructionText='Your ServiceNow instance URL (e.g., https://your-instance.service-now.com)'
            required
            onChange={(value) => {
              setAccountUrl(value);
              if (validationError.url) {
                setValidationError((prev) => ({ ...prev, url: '' }));
              }
            }}
            disabled={isSubmitting}
            error={hasAttemptedSubmit ? validationError.url : undefined}
          />

          <Input
            value={accountUsername}
            size='sm'
            id='accountUsername'
            label='Username'
            instructionText='Username for ServiceNow authentication'
            required
            onChange={(value) => {
              setAccountUsername(value);
              if (validationError.username) {
                setValidationError((prev) => ({ ...prev, username: '' }));
              }
            }}
            disabled={isSubmitting}
            error={hasAttemptedSubmit ? validationError.username : undefined}
          />

          <Input
            value={accountPassword}
            size='sm'
            id='accountPassword'
            label='Password'
            instructionText={
              isEdit
                ? 'A password is stored. Click the field to enter a new one, or leave unchanged to keep it.'
                : 'Password for ServiceNow authentication'
            }
            required={!isEdit}
            onFocus={() => {
              if (accountPassword === PASSWORD_PLACEHOLDER) setAccountPassword('');
            }}
            onChange={(value) => {
              setAccountPassword(value);
              if (validationError.password) {
                setValidationError((prev) => ({ ...prev, password: '' }));
              }
            }}
            type='password'
            disabled={isSubmitting || isTesting}
            error={hasAttemptedSubmit ? validationError.password : undefined}
          />

          <Checkbox
            id='sync-knowledge-base-label'
            checked={syncKnowledgeBase}
            onChange={(next) => setSyncKnowledgeBase(next)}
            label='Sync Knowledge Base'
            description='Enable syncing of ServiceNow Knowledge Base articles'
            disabled={isSubmitting}
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
        <Button id='cancel-btn' tone='secondary' size='md' onClick={handleAccountClose} disabled={isSubmitting || isTesting}>
          Cancel
        </Button>
        <Button
          id='test-servicenow-connection'
          tone='secondary'
          size='md'
          loading={isTesting}
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        >
          Test Connection
        </Button>
        <Button
          id={isEdit ? 'update-servicenow-acc' : 'create-servicenow-acc'}
          tone='primary'
          size='md'
          loading={isSubmitting}
          disabled={isSubmitting || isTesting}
          onClick={() => {
            submitForm(
              {
                accountName: accountName,
                accountUrl: accountUrl,
                accountUsername: accountUsername,
                accountPassword: accountPassword,
                syncKnowledgeBase: syncKnowledgeBase,
              },
              'ServiceNow'
            );
          }}
        >
          {isEdit ? 'Update' : 'Save'}
        </Button>
      </Box>
    </Modal>
  );
};

ServiceNowAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
    username: PropTypes.string,
    sync_knowledge_base: PropTypes.bool,
  }),
};

export default ServiceNowAccountModal;
