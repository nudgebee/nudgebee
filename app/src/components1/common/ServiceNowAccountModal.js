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

  const submitForm = (data, cloud_provider) => {
    setHasAttemptedSubmit(true);

    if (!validateForm()) {
      return;
    }

    setIsSubmitting(true);

    // Prepare data for modern integrations API. On edit we pass `integration_id`
    // so api-server upserts the existing row instead of failing on the
    // duplicate-name check.
    const integrationData = {
      ...(isEdit && editConfig?.id && { integration_id: editConfig.id }),
      integration_name: 'servicenow',
      integration_config_name: data.accountName,
      account_ids: [],
      integration_config_values: [
        { name: 'url', value: data.accountUrl },
        { name: 'username', value: data.accountUsername },
        { name: 'password', value: passwordForSubmit() },
        { name: 'auth_type', value: 'token' },
        { name: 'sync_knowledge_base', value: data.syncKnowledgeBase ? 'true' : 'false' },
      ],
    };

    apiIntegrations
      .listTicketConfigurationsByTool({
        tool: 'servicenow',
      })
      .then((res) => {
        const toolConfList = res?.data || [];
        const duplicateExists = toolConfList.some(
          (config) => config.name === integrationData.integration_config_name && (!isEdit || config.id !== editConfig.id)
        );
        if (duplicateExists) {
          setValidationError({
            name: `${integrationData.integration_config_name} already exists. Please choose a different name.`,
          });
          setIsSubmitting(false);
          return;
        }
        apiIntegrations
          .addIntegrations(integrationData)
          .then((res) => {
            const { data } = res;
            if (data?.data?.integrations_create_config) {
              const message = isEdit ? 'ServiceNow account updated successfully' : getAccountCreationSuccessMsg(cloud_provider);
              apiTicketIntegrations.listTicketConfigurations({}, true);
              snackbar.success(message);
              handleAccountClose(true);
            } else if (data?.errors && data?.errors.length > 0) {
              snackbar.error(data.errors[0]?.message || `Failed to ${isEdit ? 'Update' : 'Add'} ServiceNow Account`);
              handleAccountClose();
            } else {
              handleAccountClose();
            }
          })
          .catch((error) => {
            const errorMessage = error?.response?.data?.errors?.[0]?.message || `Failed to ${isEdit ? 'Update' : 'Add'} ServiceNow Account`;
            snackbar.error(errorMessage);
            handleAccountClose();
          })
          .finally(() => {
            setIsSubmitting(false);
          });
      })
      .catch(() => {
        setIsSubmitting(false);
        handleAccountClose();
      });
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
  }),
};

export default ServiceNowAccountModal;
