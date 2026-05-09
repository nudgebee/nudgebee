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

const ZenDutyAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [zenDutyName, setZenDutyName] = useState('');
  const [zenDutyAccountName, setZenDutyAccountName] = useState('');
  const [zenDutyToken, setZenDutyToken] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);
  const [errors, setErrors] = useState({});

  const zenDutyUrl = 'www.zenduty.com';

  useEffect(() => {
    if (openModal) {
      if (isEdit && editConfig) {
        setZenDutyName(editConfig.name || '');
        setZenDutyAccountName(editConfig.username || '');
        setZenDutyToken(''); // never prefill — empty value triggers ticket-server rehydrate
      } else {
        setZenDutyName('');
        setZenDutyAccountName('');
        setZenDutyToken('');
      }
      setErrors({});
      setHasAttemptedSubmit(false);
    }
  }, [openModal, isEdit, editConfig]);

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
    handleClose(shouldRefresh);
  };

  const isDuplicateName = (toolConfList, name) => toolConfList.some((config) => config.name === name && (!isEdit || config.id !== editConfig.id));

  const buildIntegrationPayload = (data) => ({
    ...(isEdit && editConfig?.id && { id: editConfig.id }),
    name: data.name,
    url: data.url,
    username: data.username,
    // Empty token on edit is intentional — ticket-server's
    // LoadExistingPassword rehydrates the stored value before validation.
    ...(data.password ? { password: data.password } : {}),
    tool: 'zenduty',
  });

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
              {isEdit ? 'API token for authentication. Leave empty to keep existing token.' : 'API token for authentication with ZenDuty'}
            </Typography>
            <TextField
              value={zenDutyToken}
              size='small'
              fullWidth
              id='zenDutyToken'
              label='API Token'
              required={!isEdit}
              onChange={(e) => {
                setZenDutyToken(e.target.value);
                if (errors.token) {
                  setErrors((prev) => ({ ...prev, token: '' }));
                }
              }}
              type='password'
              disabled={isSubmitting}
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
        <CustomButton id='cancel-btn' text='Cancel' variant='secondary' size='Medium' onClick={handleCloseZenDutyModal} disabled={isSubmitting} />
        <CustomButton
          size='Medium'
          id={isEdit ? 'update-zenduty-acc' : 'create-zenduty-acc'}
          text={isEdit ? 'Update' : 'Save'}
          disabled={isSubmitting}
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
