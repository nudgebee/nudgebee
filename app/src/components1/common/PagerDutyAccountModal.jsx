import React, { useEffect, useState } from 'react';
import { Modal } from './modal';
import { Typography, Box } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Checkbox } from '@components1/ds/Checkbox';
import apiIntegrations from '@api1/integrations';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import apiTicketIntegrations from '@api1/tickets';
import PropTypes from 'prop-types';
import { Button } from '@components1/ds/Button';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';

const PAGERDUTY_DEFAULT_URL = 'api.pagerduty.com';

// Pure display placeholder shown in edit mode to indicate a key is stored.
// The real key is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const TOKEN_PLACEHOLDER = '••••••••';

const PagerDutyAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [pagerDutyName, setPagerDutyName] = useState('');
  const [pagerDutyEmail, setPagerDutyEmail] = useState('');
  const [pagerDutyApiKey, setPagerDutyApiKey] = useState('');
  const [rcaWritebackEnabled, setRcaWritebackEnabled] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);
  const [errors, setErrors] = useState({});

  useEffect(() => {
    if (openModal) {
      if (isEdit && editConfig) {
        setPagerDutyName(editConfig.name || '');
        setPagerDutyEmail(editConfig.username || '');
        setPagerDutyApiKey(TOKEN_PLACEHOLDER);
        setRcaWritebackEnabled(!!editConfig.rca_writeback_enabled);
      } else {
        setPagerDutyName('');
        setPagerDutyEmail('');
        setPagerDutyApiKey('');
        setRcaWritebackEnabled(false);
      }
      setErrors({});
      setHasAttemptedSubmit(false);
      setIsTesting(false);
    }
  }, [openModal, isEdit, editConfig]);

  // Empty key, or unchanged placeholder in edit mode, both mean "keep stored value".
  // Trim guards against pasted keys with leading/trailing whitespace.
  const keyForSubmit = () => {
    const trimmed = pagerDutyApiKey.trim();
    return trimmed && trimmed !== TOKEN_PLACEHOLDER ? trimmed : '';
  };

  const handleTestConnection = async () => {
    if (!pagerDutyName.trim() || !pagerDutyEmail.trim()) {
      snackbar.error('Please fill name and email before testing');
      return;
    }
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: pagerDutyName.trim(),
        url: PAGERDUTY_DEFAULT_URL,
        username: pagerDutyEmail.trim(),
        password: keyForSubmit(),
        tool: 'pagerduty',
      });
      if (result?.success) {
        snackbar.success('PagerDuty connection successful');
      } else {
        snackbar.error(result?.error || 'PagerDuty connection test failed');
      }
    } catch {
      snackbar.error('Failed to test PagerDuty connection');
    } finally {
      setIsTesting(false);
    }
  };

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

    // API key is optional on edit — ticket-server rehydrates the stored value
    // when the field is left blank.
    if (!isEdit && !pagerDutyApiKey.trim()) {
      newErrors.apiKey = 'API Key is required';
    }

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };

  const handleClosePagerDutyModal = (shouldRefresh = false) => {
    setPagerDutyName('');
    setPagerDutyEmail('');
    setPagerDutyApiKey('');
    setRcaWritebackEnabled(false);
    setHasAttemptedSubmit(false);
    setErrors({});
    setIsTesting(false);
    handleClose(shouldRefresh);
  };

  const isDuplicateName = (toolConfList, name) => toolConfList.some((config) => config.name === name && (!isEdit || config.id !== editConfig.id));

  const buildIntegrationPayload = (data) => {
    const realKey = keyForSubmit();
    return {
      ...(isEdit && editConfig?.id && { id: editConfig.id }),
      name: data.name,
      url: data.url,
      username: data.username,
      // Empty key on edit is intentional — ticket-server's
      // LoadExistingPassword rehydrates the stored value before validation.
      ...(realKey ? { password: realKey } : {}),
      tool: 'pagerduty',
      // Persisted as an arbitrary config value (ticket-server upserts non-reserved
      // config_values). Read back by the RCA write-back finalizer in api-server.
      config_values: [{ name: 'rca_writeback_enabled', value: String(rcaWritebackEnabled) }],
    };
  };

  const handleSubmitResponse = async (res, cloud_provider) => {
    const fallbackError = `Failed to ${isEdit ? 'Update' : 'Add'} PagerDuty Account`;
    const responseData = res?.data;
    const successId = responseData?.data?.ticket_integration_create_config?.id;
    if (successId) {
      await apiTicketIntegrations.listTicketConfigurations({}, true);
      snackbar.success(isEdit ? 'PagerDuty account updated successfully' : getAccountCreationSuccessMsg(cloud_provider));
      handleClosePagerDutyModal(true);
      return;
    }
    snackbar.error(responseData?.data?.errors?.[0]?.message || fallbackError);
  };

  const submitForm = async (data, cloud_provider) => {
    setHasAttemptedSubmit(true);
    if (!validateForm()) return;
    setIsSubmitting(true);

    try {
      const configRes = await apiTicketIntegrations.listTicketConfigurations({ tool: 'pagerduty' });
      if (isDuplicateName(configRes?.data || [], data.name)) {
        setErrors({ name: `${data.name} already exists. Please choose a different name.` });
        return;
      }
      const res = await apiIntegrations.createTicketIntegration(buildIntegrationPayload(data));
      await handleSubmitResponse(res, cloud_provider);
    } catch (error) {
      snackbar.error(error?.response?.data?.errors?.[0]?.message || `Failed to ${isEdit ? 'Update' : 'Add'} PagerDuty Account`);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={handleClosePagerDutyModal}
      title={isEdit ? 'Edit PagerDuty Account' : 'Add PagerDuty Account'}
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
                fontSize: 'var(--ds-text-small)',
                lineHeight: 1.5,
                mb: 1,
                pl: 0.5,
              }}
            >
              A unique name to identify this PagerDuty account configuration
            </Typography>
            <Input
              value={pagerDutyName}
              size='sm'
              id='pagerDutyName'
              label='Name'
              required
              onChange={(value) => {
                setPagerDutyName(value);
                if (errors.name) {
                  setErrors((prev) => ({ ...prev, name: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit ? errors.name : undefined}
            />
          </Box>

          {/* URL Field (Read-only) */}
          <Box sx={{ mb: 0.5 }}>
            <Typography
              variant='body2'
              sx={{
                color: colors.text.secondaryDark,
                fontSize: 'var(--ds-text-small)',
                lineHeight: 1.5,
                mb: 1,
                pl: 0.5,
              }}
            >
              PagerDuty API URL (automatically configured)
            </Typography>
            <Input value={PAGERDUTY_DEFAULT_URL} size='sm' disabled onChange={() => {}} id='pagerDutyUrl' label='Account URL' />
          </Box>

          {/* Email Field */}
          <Box sx={{ mb: 0.5 }}>
            <Typography
              variant='body2'
              sx={{
                color: colors.text.secondaryDark,
                fontSize: 'var(--ds-text-small)',
                lineHeight: 1.5,
                mb: 1,
                pl: 0.5,
              }}
            >
              Email address associated with your PagerDuty account
            </Typography>
            <Input
              value={pagerDutyEmail}
              size='sm'
              id='pagerDutyEmail'
              label='Email'
              required
              onChange={(value) => {
                setPagerDutyEmail(value);
                if (errors.email) {
                  setErrors((prev) => ({ ...prev, email: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit ? errors.email : undefined}
            />
          </Box>

          {/* API Key Field */}
          <Box sx={{ mb: 0.5 }}>
            <Typography
              variant='body2'
              sx={{
                color: colors.text.secondaryDark,
                fontSize: 'var(--ds-text-small)',
                lineHeight: 1.5,
                mb: 1,
                pl: 0.5,
              }}
            >
              {isEdit
                ? 'An API key is stored. Click the field to enter a new one, or leave unchanged to keep it.'
                : 'API key for authentication with PagerDuty'}
            </Typography>
            <Input
              value={pagerDutyApiKey}
              size='sm'
              id='pagerDutyApiKey'
              label='API Key'
              required={!isEdit}
              onFocus={() => {
                if (pagerDutyApiKey === TOKEN_PLACEHOLDER) setPagerDutyApiKey('');
              }}
              onChange={(value) => {
                setPagerDutyApiKey(value);
                if (errors.apiKey) {
                  setErrors((prev) => ({ ...prev, apiKey: '' }));
                }
              }}
              type='password'
              disabled={isSubmitting || isTesting}
              error={hasAttemptedSubmit ? errors.apiKey : undefined}
            />
          </Box>

          {/* RCA write-back toggle */}
          <Box sx={{ mb: 0.5 }}>
            <Checkbox
              id='pagerDutyRcaWriteback'
              checked={rcaWritebackEnabled}
              onChange={(next) => setRcaWritebackEnabled(next)}
              disabled={isSubmitting}
              label='Post RCA analysis to PagerDuty incidents'
              description='When enabled, NudgeBee posts its root-cause analysis as a note on the originating PagerDuty incident (HIGH severity).'
            />
          </Box>
        </Box>
      </Box>
      <Box
        sx={{
          display: 'flex',
          gap: 'var(--ds-space-3)',
          justifyContent: 'flex-end',
          mt: 3,
          mb: 4,
          button: {
            minWidth: '140px',
          },
        }}
      >
        <Button id='cancel-btn' tone='secondary' size='md' onClick={handleClosePagerDutyModal} disabled={isSubmitting || isTesting}>
          Cancel
        </Button>
        <Button
          id='test-pagerduty-connection'
          tone='secondary'
          size='md'
          loading={isTesting}
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        >
          Test Connection
        </Button>
        <Button
          id={isEdit ? 'update-pagerduty-acc' : 'create-pagerduty-acc'}
          tone='primary'
          size='md'
          loading={isSubmitting}
          disabled={isSubmitting || isTesting}
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
        >
          {isEdit ? 'Update' : 'Save'}
        </Button>
      </Box>
    </Modal>
  );
};

PagerDutyAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
    username: PropTypes.string,
    rca_writeback_enabled: PropTypes.bool,
  }),
};

export default PagerDutyAccountModal;
