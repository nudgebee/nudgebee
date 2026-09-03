import React, { useEffect, useState } from 'react';
import { Modal } from '@ui/Modal';
import { Typography, Box } from '@mui/material';
import { Input } from '@ui/Input';
import apiIntegrations from '@api1/integrations';
import { getAccountCreationSuccessMsg } from 'src/utils/common';
import apiTicketIntegrations from '@api1/tickets';
import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';

// Pure display placeholder shown in edit mode to indicate a key is stored.
// The real key is never sent to the client. A field still equal to this on
// submit/test is treated as "leave the stored value untouched".
const TOKEN_PLACEHOLDER = '••••••••';

// incident.io authenticates with an account-scoped API key and, unlike
// PagerDuty/ZenDuty, does not bind that key to a user. There is deliberately no
// email field here — nothing downstream would use it.
const INCIDENT_IO_URL = 'api.incident.io';

const IncidentIOAccountModal = ({ openModal, handleClose, editConfig = null }) => {
  const isEdit = !!editConfig;
  const [incidentIOName, setIncidentIOName] = useState('');
  const [incidentIOToken, setIncidentIOToken] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [hasAttemptedSubmit, setHasAttemptedSubmit] = useState(false);
  const [errors, setErrors] = useState({});

  useEffect(() => {
    if (openModal) {
      if (isEdit && editConfig) {
        setIncidentIOName(editConfig.name || '');
        setIncidentIOToken(TOKEN_PLACEHOLDER);
      } else {
        setIncidentIOName('');
        setIncidentIOToken('');
      }
      setErrors({});
      setHasAttemptedSubmit(false);
      setIsTesting(false);
    }
  }, [openModal, isEdit, editConfig]);

  // Empty key, or unchanged placeholder in edit mode, both mean "keep stored value".
  // Trim guards against pasted keys with leading/trailing whitespace.
  const tokenForSubmit = () => {
    const trimmed = incidentIOToken.trim();
    return trimmed && trimmed !== TOKEN_PLACEHOLDER ? trimmed : '';
  };

  const handleTestConnection = async () => {
    if (!incidentIOName.trim()) {
      snackbar.error('Please fill name before testing');
      return;
    }
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testTicketConnectionByConfig({
        ...(isEdit ? { id: editConfig.id } : {}),
        name: incidentIOName.trim(),
        url: INCIDENT_IO_URL,
        password: tokenForSubmit(),
        tool: 'incidentio',
      });
      if (result?.success) {
        snackbar.success('incident.io connection successful');
      } else {
        snackbar.error(result?.error || 'incident.io connection test failed');
      }
    } catch {
      snackbar.error('Failed to test incident.io connection');
    } finally {
      setIsTesting(false);
    }
  };

  const validateForm = () => {
    const newErrors = {};

    if (!incidentIOName.trim()) {
      newErrors.name = 'Name is required';
    }

    // Key is optional on edit — ticket-server rehydrates the stored value
    // when the field is left blank.
    if (!isEdit && !incidentIOToken.trim()) {
      newErrors.token = 'API Key is required';
    }

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  };

  const handleCloseIncidentIOModal = (shouldRefresh = false) => {
    setIncidentIOName('');
    setIncidentIOToken('');
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
      // Empty key on edit is intentional — ticket-server's
      // LoadExistingPassword rehydrates the stored value before validation.
      ...(realPassword ? { password: realPassword } : {}),
      tool: 'incidentio',
    };
  };

  const handleSubmitResponse = async (res, cloud_provider) => {
    const fallbackError = `Failed to ${isEdit ? 'Update' : 'Add'} incident.io Account`;
    const responseData = res?.data;
    const successId = responseData?.data?.ticket_integration_create_config?.id;
    if (successId) {
      await apiTicketIntegrations.listTicketConfigurations({}, true);
      snackbar.success(isEdit ? 'incident.io account updated successfully' : getAccountCreationSuccessMsg(cloud_provider));
      handleCloseIncidentIOModal(true);
      return;
    }
    snackbar.error(responseData?.data?.errors?.[0]?.message || fallbackError);
  };

  const submitForm = async (data, cloud_provider) => {
    setHasAttemptedSubmit(true);
    if (!validateForm()) return;
    setIsSubmitting(true);

    try {
      const configRes = await apiTicketIntegrations.listTicketConfigurations({ tool: 'incidentio' });
      if (isDuplicateName(configRes?.data || [], data.name)) {
        setErrors({ name: `${data.name} already exists. Please choose a different name.` });
        return;
      }
      const res = await apiIntegrations.createTicketIntegration(buildIntegrationPayload(data));
      await handleSubmitResponse(res, cloud_provider);
    } catch (error) {
      snackbar.error(error?.response?.data?.errors?.[0]?.message || `Failed to ${isEdit ? 'Update' : 'Add'} incident.io Account`);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={handleCloseIncidentIOModal}
      title={isEdit ? 'Edit incident.io Account' : 'Add incident.io Account'}
      loader={isSubmitting}
    >
      <Box sx={{ minHeight: ds.space.mul(0, 100), pt: ds.space[5], pb: ds.space[2] }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[5] }}>
          {/* Name Field */}
          <Box sx={{ mb: ds.space[1] }}>
            <Typography
              variant='body2'
              sx={{
                color: ds.gray[400],
                fontSize: 'var(--ds-text-small)',
                lineHeight: 1.5,
                mb: ds.space[2],
                pl: ds.space[1],
              }}
            >
              A unique name to identify this incident.io account configuration
            </Typography>
            <Input
              value={incidentIOName}
              size='sm'
              id='incidentIOName'
              label='Name'
              required
              onChange={(value) => {
                setIncidentIOName(value);
                if (errors.name) {
                  setErrors((prev) => ({ ...prev, name: '' }));
                }
              }}
              disabled={isSubmitting}
              error={hasAttemptedSubmit ? errors.name : undefined}
            />
          </Box>

          {/* URL Field (Read-only) */}
          <Box sx={{ mb: ds.space[1] }}>
            <Typography
              variant='body2'
              sx={{
                color: ds.gray[400],
                fontSize: 'var(--ds-text-small)',
                lineHeight: 1.5,
                mb: ds.space[2],
                pl: ds.space[1],
              }}
            >
              incident.io API URL (automatically configured)
            </Typography>
            <Input value={INCIDENT_IO_URL} size='sm' disabled onChange={() => {}} id='incidentIOUrl' label='Account URL' />
          </Box>

          {/* API Key Field */}
          <Box sx={{ mb: ds.space[1] }}>
            <Typography
              variant='body2'
              sx={{
                color: ds.gray[400],
                fontSize: 'var(--ds-text-small)',
                lineHeight: 1.5,
                mb: ds.space[2],
                pl: ds.space[1],
              }}
            >
              {isEdit
                ? 'A key is stored. Click the field to enter a new one, or leave unchanged to keep it.'
                : 'API key from Settings → API keys in incident.io. Needs the incidents read/write and severities read scopes.'}
            </Typography>
            <Input
              value={incidentIOToken}
              size='sm'
              id='incidentIOToken'
              label='API Key'
              required={!isEdit}
              onFocus={() => {
                if (incidentIOToken === TOKEN_PLACEHOLDER) setIncidentIOToken('');
              }}
              onChange={(value) => {
                setIncidentIOToken(value);
                if (errors.token) {
                  setErrors((prev) => ({ ...prev, token: '' }));
                }
              }}
              type='password'
              disabled={isSubmitting || isTesting}
              error={hasAttemptedSubmit ? errors.token : undefined}
            />
          </Box>
        </Box>
      </Box>
      <Box
        sx={{
          display: 'flex',
          gap: 'var(--ds-space-3)',
          justifyContent: 'flex-end',
          mt: ds.space[5],
          mb: ds.space[6],
          button: {
            minWidth: ds.space.mul(0, 70),
          },
        }}
      >
        <Button id='cancel-btn' tone='secondary' size='md' onClick={handleCloseIncidentIOModal} disabled={isSubmitting || isTesting}>
          Cancel
        </Button>
        <Button
          id='test-incidentio-connection'
          tone='secondary'
          size='md'
          loading={isTesting}
          onClick={handleTestConnection}
          disabled={isSubmitting || isTesting}
        >
          Test Connection
        </Button>
        <Button
          id={isEdit ? 'update-incidentio-acc' : 'create-incidentio-acc'}
          tone='primary'
          size='md'
          loading={isSubmitting}
          disabled={isSubmitting || isTesting}
          onClick={() => {
            submitForm(
              {
                name: incidentIOName,
                password: incidentIOToken,
                url: INCIDENT_IO_URL,
              },
              'INCIDENTIO'
            );
          }}
        >
          {isEdit ? 'Update' : 'Save'}
        </Button>
      </Box>
    </Modal>
  );
};

IncidentIOAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  editConfig: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    url: PropTypes.string,
  }),
};

export default IncidentIOAccountModal;
