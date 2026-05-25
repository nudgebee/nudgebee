import React, { useEffect, useState } from 'react';
import { Modal } from './modal';
import { Typography, TextField, Box } from '@mui/material';
import apiIntegrations from '@api1/integrations';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import apiTicketIntegrations from '@api1/tickets';
import PropTypes from 'prop-types';
import CustomButton from './NewCustomButton';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';

// Pure display placeholder shown in edit mode to indicate a token is stored.
// The real token is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const TOKEN_PLACEHOLDER = '••••••••';

const ZenDutyAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [zenDutyName, setZenDutyName] = useState('');
  const [zenDutyAccountName, setZenDutyAccountName] = useState('');
  const [zenDutyToken, setZenDutyToken] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);
  const [errors, setErrors] = useState({});

  const zenDutyUrl = 'www.zenduty.com';

  useEffect(() => {
    if (openModal) {
      if (isEdit && editConfig) {
        setZenDutyName(editConfig.name || '');
        setZenDutyAccountName(editConfig.username || '');
        setZenDutyToken(TOKEN_PLACEHOLDER);
      } else {
        setZenDutyName('');
        setZenDutyAccountName('');
        setZenDutyToken('');
      }
      setErrors({});
      setHasAttemptedSubmit(false);
      setIsTesting(false);
    }
  }, [openModal, isEdit, editConfig]);

  // Empty token, or unchanged placeholder in edit mode, both mean "keep stored value".
  // Trim guards against pasted tokens with leading/trailing whitespace.
  const tokenForSubmit = () => {
    const trimmed = zenDutyToken.trim();
    return trimmed && trimmed !== TOKEN_PLACEHOLDER ? trimmed : '';
  };

  const handleTestConnection = async () => {
    if (!zenDutyName.trim() || !zenDutyAccountName.trim()) {
      snackbar.error('Please fill name and email before testing');
      return;
    }
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: zenDutyName.trim(),
        url: zenDutyUrl,
        username: zenDutyAccountName.trim(),
        password: tokenForSubmit(),
        tool: 'zenduty',
      });
      if (result?.success) {
        snackbar.success('ZenDuty connection successful');
      } else {
        snackbar.error(result?.error || 'ZenDuty connection test failed');
      }
    } catch {
      snackbar.error('Failed to test ZenDuty connection');
    } finally {
      setIsTesting(false);
    }
  };

  const validateForm = () => {
    const newErrors = {};

    if (!zenDutyName.trim()) {
      newErrors.name = 'Name is required';
    }

    if (!zenDutyAccountName.trim()) {
      newErrors.email = 'Email is required';
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(zenDutyAccountName)) {
      newErrors.email = 'Please enter a valid email address';
    }

    // Token is optional on edit — ticket-server rehydrates the stored value
    // when the field is left blank.
    if (!isEdit && !zenDutyToken.trim()) {
      newErrors.token = 'API Token is required';
    }

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };

  const handleCloseZenDutyModal = (shouldRefresh = false) => {
    setZenDutyName('');
    setZenDutyAccountName('');
    setZenDutyToken('');
    setHasAttemptedSubmit(false);
    setErrors({});
    setIsTesting(false);
    handleClose(shouldRefresh);
  };

  const isDuplicateName = (toolConfList, name) => toolConfList.some((config) => config.name === name && (!isEdit || config.id !== editConfig.id));

  const buildIntegrationPayload = (data) => {
    const realPassword = tokenForSubmit();
    return {
      ...(isEdit && editConfig?.id && { id: editConfig.id }),
      name: data.name,
      url: data.url,
      username: data.username,
      // Empty token on edit is intentional — ticket-server's
      // LoadExistingPassword rehydrates the stored value before validation.
      ...(realPassword ? { password: realPassword } : {}),
      tool: 'zenduty',
    };
  };

  const handleSubmitResponse = async (res, cloud_provider) => {
    const fallbackError = `Failed to ${isEdit ? 'Update' : 'Add'} ZenDuty Account`;
    const responseData = res?.data;
    const successId = responseData?.data?.ticket_integration_create_config?.id;
    if (successId) {
      await apiTicketIntegrations.listTicketConfigurations({}, true);
      snackbar.success(isEdit ? 'ZenDuty account updated successfully' : getAccountCreationSuccessMsg(cloud_provider));
      handleCloseZenDutyModal(true);
      return;
    }
    snackbar.error(responseData?.data?.errors?.[0]?.message || fallbackError);
  };

  const submitForm = async (data, cloud_provider) => {
    setHasAttemptedSubmit(true);
    if (!validateForm()) return;
    setIsSubmitting(true);

    try {
      const configRes = await apiTicketIntegrations.listTicketConfigurations({ tool: 'zenduty' });
      if (isDuplicateName(configRes?.data || [], data.name)) {
        setErrors({ name: `${data.name} already exists. Please choose a different name.` });
        return;
      }
      const res = await apiIntegrations.createTicketIntegration(buildIntegrationPayload(data));
      await handleSubmitResponse(res, cloud_provider);
    } catch (error) {
      snackbar.error(error?.response?.data?.errors?.[0]?.message || `Failed to ${isEdit ? 'Update' : 'Add'} ZenDuty Account`);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={handleCloseZenDutyModal}
      title={isEdit ? 'Edit ZenDuty Account' : 'Add ZenDuty Account'}
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
              A unique name to identify this ZenDuty account configuration
            </Typography>
            <TextField
              value={zenDutyName}
              size='small'
              fullWidth
              id='zenDutyName'
              label='Name'
              required
              onChange={(e) => {
                setZenDutyName(e.target.value);
                if (errors.name) {
                  setErrors((prev) => ({ ...prev, name: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!errors.name}
              helperText={hasAttemptedSubmit && errors.name}
            />
          </Box>

          {/* URL Field (Read-only) */}
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
              ZenDuty API URL (automatically configured)
            </Typography>
            <TextField value={zenDutyUrl} size='small' disabled fullWidth id='zenDutyUrl' label='Account URL' />
          </Box>

          {/* Email Field */}
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
              Email address associated with your ZenDuty account
            </Typography>
            <TextField
              value={zenDutyAccountName}
              size='small'
              fullWidth
              id='zenDutyAccountName'
              label='Email'
              required
              onChange={(e) => {
                setZenDutyAccountName(e.target.value);
                if (errors.email) {
                  setErrors((prev) => ({ ...prev, email: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!errors.email}
              helperText={hasAttemptedSubmit && errors.email}
            />
          </Box>

          {/* Token Field */}
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
              {isEdit
                ? 'A token is stored. Click the field to enter a new one, or leave unchanged to keep it.'
                : 'API token for authentication with ZenDuty'}
            </Typography>
            <TextField
              value={zenDutyToken}
              size='small'
              fullWidth
              id='zenDutyToken'
              label='API Token'
              required={!isEdit}
              onFocus={() => {
                if (zenDutyToken === TOKEN_PLACEHOLDER) setZenDutyToken('');
              }}
              onChange={(e) => {
                setZenDutyToken(e.target.value);
                if (errors.token) {
                  setErrors((prev) => ({ ...prev, token: '' }));
                }
              }}
              type='password'
              disabled={isSubmitting || isTesting}
              error={hasAttemptedSubmit && !!errors.token}
              helperText={hasAttemptedSubmit && errors.token}
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
          onClick={handleCloseZenDutyModal}
          disabled={isSubmitting || isTesting}
        />
        <CustomButton
          id='test-zenduty-connection'
          text={isTesting ? 'Testing...' : 'Test Connection'}
          variant='secondary'
          size='Medium'
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        />
        <CustomButton
          size='Medium'
          id={isEdit ? 'update-zenduty-acc' : 'create-zenduty-acc'}
          text={isEdit ? 'Update' : 'Save'}
          disabled={isSubmitting || isTesting}
          onClick={() => {
            submitForm(
              {
                name: zenDutyName,
                password: zenDutyToken,
                url: zenDutyUrl,
                username: zenDutyAccountName,
              },
              'ZENDUTY'
            );
          }}
        />
      </Box>
    </Modal>
  );
};

ZenDutyAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
    username: PropTypes.string,
  }),
};

export default ZenDutyAccountModal;
