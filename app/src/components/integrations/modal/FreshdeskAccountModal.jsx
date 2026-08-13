import React, { useState, useEffect } from 'react';
import { Modal } from '@ui/Modal';
import { Box } from '@mui/material';
import { Input } from '@ui/Input';
import { Button } from '@ui/Button';
import apiIntegrations from '@api1/integrations';
import apiTicketIntegrations from '@api1/tickets';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import PropTypes from 'prop-types';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';

// Pure display placeholder shown in edit mode to indicate an API key is stored.
// The real key is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const API_KEY_PLACEHOLDER = '••••••••';

const FreshdeskAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [accountName, setAccountName] = useState('');
  const [accountUrl, setAccountUrl] = useState('');
  const [accountApiKey, setAccountApiKey] = useState('');
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);

  useEffect(() => {
    if (openModal) {
      if (isEdit && editConfig) {
        setAccountName(editConfig.name || '');
        setAccountUrl(editConfig.url || '');
        setAccountApiKey(API_KEY_PLACEHOLDER);
      } else {
        setAccountName('');
        setAccountUrl('');
        setAccountApiKey('');
      }
      setValidationError({});
      setHasAttemptedSubmit(false);
      setIsTesting(false);
    }
  }, [openModal, isEdit, editConfig]);

  // Empty key, or unchanged placeholder in edit mode, both mean "keep stored value".
  // Trim guards against pasted keys with leading/trailing whitespace.
  const apiKeyForSubmit = () => {
    const trimmed = accountApiKey.trim();
    return trimmed && trimmed !== API_KEY_PLACEHOLDER ? trimmed : '';
  };

  const validateForm = () => {
    const errors = {};

    if (!accountName.trim()) {
      errors.name = 'Name is required';
    }

    if (!accountUrl.trim()) {
      errors.url = 'Freshdesk URL is required';
    }

    // On edit, an unchanged placeholder is valid (stored key is used).
    if (!isEdit && !accountApiKey.trim()) {
      errors.apiKey = 'API key is required';
    }

    setValidationError(errors);
    return Object.keys(errors).length === 0;
  };

  const handleTestConnection = async () => {
    if (!accountName.trim() || !accountUrl.trim()) {
      snackbar.error('Please fill name and URL before testing');
      return;
    }
    setIsTesting(true);
    try {
      // Freshdesk basic auth is <api_key>:X, so the API key is sent as the
      // password. No username field is required.
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: accountName.trim(),
        url: accountUrl.trim(),
        password: apiKeyForSubmit(),
        auth_type: 'basic',
        tool: 'freshdesk',
      });
      if (result?.success) {
        snackbar.success('Freshdesk connection successful');
      } else {
        snackbar.error(result?.error || 'Freshdesk connection test failed');
      }
    } catch {
      snackbar.error('Failed to test Freshdesk connection');
    } finally {
      setIsTesting(false);
    }
  };

  const handleAccountClose = (trigger = false) => {
    setAccountName('');
    setAccountUrl('');
    setAccountApiKey('');
    setValidationError({});
    setHasAttemptedSubmit(false);
    setIsSubmitting(false);
    setIsTesting(false);
    handleClose(trigger);
  };

  const isDuplicateName = (toolConfList, name) => toolConfList.some((config) => config.name === name && (!isEdit || config.id !== editConfig.id));

  // Saved via ticket-server (AddTicketConfiguration), which rehydrates the stored
  // API key (as password) + auth_type when the form omits them on edit. Freshdesk
  // basic auth is <api_key>:X, so the key is stored in the encrypted password field.
  const buildIntegrationPayload = (data) => {
    const realApiKey = apiKeyForSubmit();
    return {
      ...(isEdit && editConfig?.id && { id: editConfig.id }),
      name: data.accountName,
      url: data.accountUrl,
      auth_type: 'basic',
      // Empty key on edit is intentional — ticket-server rehydrates the stored
      // value before validation, so we omit the key rather than send "".
      ...(realApiKey ? { password: realApiKey } : {}),
      tool: 'freshdesk',
    };
  };

  const handleSubmitResponse = async (res, cloud_provider) => {
    const fallbackError = `Failed to ${isEdit ? 'Update' : 'Add'} Freshdesk Account`;
    const responseData = res?.data;
    const successId = responseData?.data?.ticket_integration_create_config?.id;
    if (successId) {
      await apiTicketIntegrations.listTicketConfigurations({}, true);
      snackbar.success(isEdit ? 'Freshdesk account updated successfully' : getAccountCreationSuccessMsg(cloud_provider));
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
      const configRes = await apiIntegrations.listTicketConfigurationsByTool({ tool: 'freshdesk' });
      if (isDuplicateName(configRes?.data || [], data.accountName)) {
        setValidationError({ name: `${data.accountName} already exists. Please choose a different name.` });
        return;
      }
      const res = await apiIntegrations.createTicketIntegration(buildIntegrationPayload(data));
      await handleSubmitResponse(res, cloud_provider);
    } catch (error) {
      snackbar.error(error?.response?.data?.errors?.[0]?.message || `Failed to ${isEdit ? 'Update' : 'Add'} Freshdesk Account`);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={handleAccountClose}
      title={isEdit ? 'Edit Freshdesk Account' : 'Add Freshdesk Account'}
      loader={isSubmitting}
    >
      <Box sx={{ minHeight: ds.space.mul(0, 100), pt: 1 }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <Input
            value={accountName}
            size='sm'
            id='accountName'
            label='Name'
            instructionText='A unique name to identify this Freshdesk account configuration'
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
            label='Freshdesk URL'
            instructionText='Your Freshdesk URL (e.g., https://yourcompany.freshdesk.com)'
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
            value={accountApiKey}
            size='sm'
            id='accountApiKey'
            label='API Key'
            instructionText={
              isEdit
                ? 'An API key is stored. Click the field to enter a new one, or leave unchanged to keep it.'
                : 'Your Freshdesk API key (Profile Settings → Your API Key)'
            }
            required={!isEdit}
            onFocus={() => {
              if (accountApiKey === API_KEY_PLACEHOLDER) setAccountApiKey('');
            }}
            onChange={(value) => {
              setAccountApiKey(value);
              if (validationError.apiKey) {
                setValidationError((prev) => ({ ...prev, apiKey: '' }));
              }
            }}
            type='password'
            disabled={isSubmitting || isTesting}
            error={hasAttemptedSubmit ? validationError.apiKey : undefined}
          />
        </Box>
      </Box>
      <Box
        sx={{
          display: 'flex',
          gap: 'var(--ds-space-3)',
          justifyContent: 'flex-end',
          mt: 3,
          mb: 1,
        }}
      >
        <Button id='cancel-btn' tone='secondary' size='md' onClick={handleAccountClose} disabled={isSubmitting || isTesting}>
          Cancel
        </Button>
        <Button
          id='test-freshdesk-connection'
          tone='secondary'
          size='md'
          loading={isTesting}
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        >
          Test Connection
        </Button>
        <Button
          id={isEdit ? 'update-freshdesk-acc' : 'create-freshdesk-acc'}
          tone='primary'
          size='md'
          loading={isSubmitting}
          disabled={isSubmitting || isTesting}
          onClick={() => {
            submitForm(
              {
                accountName: accountName,
                accountUrl: accountUrl,
                accountApiKey: accountApiKey,
              },
              'Freshdesk'
            );
          }}
        >
          {isEdit ? 'Update' : 'Save'}
        </Button>
      </Box>
    </Modal>
  );
};

FreshdeskAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
  }),
};

export default FreshdeskAccountModal;
