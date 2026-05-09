import React, { useState, useEffect } from 'react';
import { Modal } from './modal';
import { Typography, TextField, Box, FormControlLabel, Checkbox } from '@mui/material';
import apiIntegrations from '@api1/integrations';
import apiTicketIntegrations from '@api1/tickets';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import PropTypes from 'prop-types';
import CustomButton from './NewCustomButton';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';

const ServiceNowAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [accountName, setAccountName] = useState('');
  const [accountUrl, setAccountUrl] = useState('');
  const [accountPassword, setAccountPassword] = useState('');
  const [accountUsername, setAccountUsername] = useState('');
  const [syncKnowledgeBase, setSyncKnowledgeBase] = useState(false);
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);

  useEffect(() => {
    if (openModal) {
      if (isEdit && editConfig) {
        setAccountName(editConfig.name || '');
        setAccountUrl(editConfig.url || '');
        setAccountUsername(editConfig.username || '');
        setAccountPassword(''); // never prefill the password
        // sync_knowledge_base may or may not be on the legacy listing payload;
        // default to off and let the user opt in/out again on edit.
        setSyncKnowledgeBase(false);
      } else {
        setAccountName('');
        setAccountUrl('');
        setAccountUsername('');
        setAccountPassword('');
        setSyncKnowledgeBase(false);
      }
      setValidationError({});
      setHasAttemptedSubmit(false);
    }
  }, [openModal, isEdit, editConfig]);

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

    if (!accountPassword.trim()) {
      errors.password = 'Password is required';
    }

    setValidationError(errors);
    return Object.keys(errors).length === 0;
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
        { name: 'password', value: data.accountPassword },
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
              A unique name to identify this ServiceNow account configuration
            </Typography>
            <TextField
              value={accountName}
              size='small'
              fullWidth
              id='accountName'
              label='Name'
              required
              onChange={(e) => {
                const value = e.target.value;
                setAccountName(value);
                if (validationError.name) {
                  setValidationError((prev) => ({ ...prev, name: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!validationError.name}
              helperText={hasAttemptedSubmit && validationError.name}
            />
          </Box>

          {/* Instance URL Field */}
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
              Your ServiceNow instance URL (e.g., https://your-instance.service-now.com)
            </Typography>
            <TextField
              value={accountUrl}
              size='small'
              fullWidth
              id='accountUrl'
              label='Instance URL'
              required
              onChange={(e) => {
                setAccountUrl(e.target.value);
                if (validationError.url) {
                  setValidationError((prev) => ({ ...prev, url: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!validationError.url}
              helperText={hasAttemptedSubmit && validationError.url}
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
              Username for ServiceNow authentication
            </Typography>
            <TextField
              value={accountUsername}
              size='small'
              fullWidth
              id='accountUsername'
              label='Username'
              required
              onChange={(e) => {
                setAccountUsername(e.target.value);
                if (validationError.username) {
                  setValidationError((prev) => ({ ...prev, username: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!validationError.username}
              helperText={hasAttemptedSubmit && validationError.username}
            />
          </Box>

          {/* Password Field */}
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
              Password for ServiceNow authentication
            </Typography>
            <TextField
              value={accountPassword}
              size='small'
              fullWidth
              id='accountPassword'
              label='Password'
              required
              onChange={(e) => {
                setAccountPassword(e.target.value);
                if (validationError.password) {
                  setValidationError((prev) => ({ ...prev, password: '' }));
                }
              }}
              type='password'
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!validationError.password}
              helperText={hasAttemptedSubmit && validationError.password}
            />
          </Box>

          {/* Sync Knowledge Base Checkbox */}
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
              Enable syncing of ServiceNow Knowledge Base articles
            </Typography>
            <FormControlLabel
              id='sync-knowledge-base-label'
              control={<Checkbox checked={syncKnowledgeBase} onChange={(e) => setSyncKnowledgeBase(e.target.checked)} />}
              label='Sync Knowledge Base'
              disabled={isSubmitting}
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
        <CustomButton id='cancel-btn' text='Cancel' variant='secondary' size='Medium' onClick={handleAccountClose} disabled={isSubmitting} />
        <CustomButton
          size='Medium'
          id={isEdit ? 'update-servicenow-acc' : 'create-servicenow-acc'}
          text={isEdit ? 'Update' : 'Save'}
          disabled={isSubmitting}
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
          label={isEdit ? 'Update ServiceNow' : 'Save ServiceNow'}
        />
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
