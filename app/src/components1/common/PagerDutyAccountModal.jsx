import React, { useState } from 'react';
import { Modal } from './modal';
import { Typography, TextField, Box } from '@mui/material';
import apiIntegrations from '@api1/integrations';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import apiTicketIntegrations from '@api1/tickets';
import PropTypes from 'prop-types';
import CustomButton from './NewCustomButton';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';

const PAGERDUTY_DEFAULT_URL = 'api.pagerduty.com';

const PagerDutyAccountModal = ({ openModal, handleClose }) => {
  const [pagerDutyName, setPagerDutyName] = useState('');
  const [pagerDutyEmail, setPagerDutyEmail] = useState('');
  const [pagerDutyApiKey, setPagerDutyApiKey] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);
  const [errors, setErrors] = useState({});

  const validateForm = () => {
    const newErrors = {};

    if (!pagerDutyName.trim()) {
      newErrors.name = 'Name is required';
    }

    if (!pagerDutyEmail.trim()) {
      newErrors.email = 'Email is required';
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(pagerDutyEmail)) {
      newErrors.email = 'Please enter a valid email address';
    }

    if (!pagerDutyApiKey.trim()) {
      newErrors.apiKey = 'API Key is required';
    }

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };

  const handleClosePagerDutyModal = (shouldRefresh = false) => {
    setPagerDutyName('');
    setPagerDutyEmail('');
    setPagerDutyApiKey('');
    setHasAttemptedSubmit(false);
    setErrors({});
    handleClose(shouldRefresh);
  };

  const submitForm = async (data, cloud_provider) => {
    setHasAttemptedSubmit(true);

    if (!validateForm()) {
      return;
    }

    setIsSubmitting(true);

    try {
      const configRes = await apiTicketIntegrations.listTicketConfigurations({
        tool: 'pagerduty',
      });
      const toolConfList = configRes?.data || [];
      const duplicateExists = toolConfList.some((config) => config.name === data.name);

      if (duplicateExists) {
        setErrors({
          name: `${data.name} already exists. Please choose a different name.`,
        });
        setIsSubmitting(false);
        return;
      }

      const integrationData = {
        name: data.name,
        url: data.url,
        username: data.username,
        password: data.password,
        tool: 'pagerduty',
      };

      const res = await apiIntegrations.createTicketIntegration(integrationData);
      const { data: responseData } = res;
      const successId = responseData?.data?.ticket_integration_create_config?.id;

      if (successId) {
        await apiTicketIntegrations.listTicketConfigurations({}, true);
        const message = getAccountCreationSuccessMsg(cloud_provider);
        snackbar.success(message);
        handleClosePagerDutyModal(true);
      } else if (responseData?.data?.errors?.length > 0) {
        snackbar.error(responseData.data.errors[0]?.message || 'Failed to Add PagerDuty Account');
      } else {
        snackbar.error('Failed to Add PagerDuty Account');
      }
    } catch (error) {
      const errorMessage = error?.response?.data?.errors?.[0]?.message || 'Failed to Add PagerDuty Account';
      snackbar.error(errorMessage);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal width='md' open={openModal} handleClose={handleClosePagerDutyModal} title={'Add PagerDuty Account'} loader={isSubmitting}>
      <Box sx={{ minHeight: '200px', pt: 3, pb: 1 }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
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
              A unique name to identify this PagerDuty account configuration
            </Typography>
            <TextField
              value={pagerDutyName}
              size='small'
              fullWidth
              id='pagerDutyName'
              label='Name'
              required
              onChange={(e) => {
                setPagerDutyName(e.target.value);
                if (errors.name) {
                  setErrors((prev) => ({ ...prev, name: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!errors.name}
              helperText={hasAttemptedSubmit && errors.name}
            />
          </Box>

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
              PagerDuty API URL (automatically configured)
            </Typography>
            <TextField value={PAGERDUTY_DEFAULT_URL} size='small' disabled fullWidth id='pagerDutyUrl' label='Account URL' />
          </Box>

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
              Email address associated with your PagerDuty account
            </Typography>
            <TextField
              value={pagerDutyEmail}
              size='small'
              fullWidth
              id='pagerDutyEmail'
              label='Email'
              required
              onChange={(e) => {
                setPagerDutyEmail(e.target.value);
                if (errors.email) {
                  setErrors((prev) => ({ ...prev, email: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!errors.email}
              helperText={hasAttemptedSubmit && errors.email}
            />
          </Box>

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
              API key for authentication with PagerDuty
            </Typography>
            <TextField
              value={pagerDutyApiKey}
              size='small'
              fullWidth
              id='pagerDutyApiKey'
              label='API Key'
              required
              onChange={(e) => {
                setPagerDutyApiKey(e.target.value);
                if (errors.apiKey) {
                  setErrors((prev) => ({ ...prev, apiKey: '' }));
                }
              }}
              type='password'
              disabled={isSubmitting}
              error={hasAttemptedSubmit && !!errors.apiKey}
              helperText={hasAttemptedSubmit && errors.apiKey}
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
        <CustomButton id='cancel-btn' text='Cancel' variant='secondary' size='Medium' onClick={handleClosePagerDutyModal} disabled={isSubmitting} />
        <CustomButton
          size='Medium'
          id={'create-pagerduty-acc'}
          text='Save'
          disabled={isSubmitting}
          onClick={() => {
            submitForm(
              {
                name: pagerDutyName,
                password: pagerDutyApiKey,
                url: PAGERDUTY_DEFAULT_URL,
                username: pagerDutyEmail,
              },
              'PAGERDUTY'
            );
          }}
        />
      </Box>
    </Modal>
  );
};

PagerDutyAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
};

export default PagerDutyAccountModal;
