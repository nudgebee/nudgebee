import { Box, FormControlLabel, Grid, Radio, RadioGroup } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import React, { useState } from 'react';
import PropTypes from 'prop-types';
import apiAccount from '@api1/account';
import { Modal } from '@components1/common/modal';
import { Input } from '@components1/ds/Input';
import { isK8sAccountNameValid } from 'src/utils/common';
import { Button } from '@components1/ds/Button';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';

const SETUP_INSTRUCTIONS = `### Cloud Foundry Account Setup
  ### Step 1. Enter Account Name
  ### Step 2. Enter CF API URL
     - The Cloud Foundry API endpoint (e.g., \`https://api.sys.example.com\`)
  ### Step 3. Choose Authentication Method
     - **Bearer Token**: For Korifi or K8s service account token
     - **UAA OAuth2**: For PCF with UAA client credentials
  ### Step 4. Provide Credentials
     - Enter the token or UAA client credentials
  ### Step 5. Save`;

const AddCloudFoundryAccountModal = ({ open, onClose }) => {
  const [accountNameValue, setAccountNameValue] = useState('');
  const [cfApiUrl, setCfApiUrl] = useState('');
  const [authType, setAuthType] = useState('token');
  const [bearerToken, setBearerToken] = useState('');
  const [clientId, setClientId] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [skipSSL, setSkipSSL] = useState(false);
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);

  const resetForm = () => {
    setAccountNameValue('');
    setCfApiUrl('');
    setAuthType('token');
    setBearerToken('');
    setClientId('');
    setClientSecret('');
    setSkipSSL(false);
    setValidationError({});
    setIsSubmitting(false);
  };

  const handleCloseModal = (wasSuccessful = false) => {
    resetForm();
    onClose(wasSuccessful);
  };

  const handleAccountNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setValidationError((prev) => ({
        ...prev,
        accountName:
          'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore',
      }));
    } else {
      setValidationError((prev) => {
        const newState = { ...prev };
        delete newState.accountName;
        return newState;
      });
    }
    setAccountNameValue(value);
  };

  const handleCfApiUrlChange = (value) => {
    setCfApiUrl(value);
    if (value && !value.startsWith('http')) {
      setValidationError((prev) => ({ ...prev, cfApiUrl: 'URL must start with http:// or https://' }));
    } else {
      setValidationError((prev) => {
        const newState = { ...prev };
        delete newState.cfApiUrl;
        return newState;
      });
    }
  };

  const isFormValid = () => {
    if (!accountNameValue || !cfApiUrl || !!validationError.accountName || !!validationError.cfApiUrl) {
      return false;
    }
    if (authType === 'token' && !bearerToken) {
      return false;
    }
    if (authType === 'uaa' && (!clientId || !clientSecret)) {
      return false;
    }
    return true;
  };

  const handleSubmit = () => {
    setIsSubmitting(true);

    const data = {
      cf_api_url: cfApiUrl,
      auth_type: authType,
      skip_ssl: skipSSL,
    };

    const body = {
      account_name: accountNameValue,
      access_secret: authType === 'token' ? bearerToken : clientSecret,
      access_key: authType === 'uaa' ? clientId : '',
      data: data,
      cloud_provider: 'CloudFoundry',
      account_type: 'cloud',
    };

    apiAccount
      .createAccount(body)
      .then((res) => {
        if (res?.data?.status === 'ERROR') {
          snackbar.error(`Failed to Add Cloud Foundry Account - ${res?.data?.message}`);
          return;
        }
        snackbar.success('Cloud Foundry Account added successfully');
        handleCloseModal(true);
      })
      .catch((error) => {
        snackbar.error('Failed to add Cloud Foundry account');
        console.error('Failed to add Cloud Foundry account:', error);
      })
      .finally(() => {
        setIsSubmitting(false);
      });
  };

  return (
    <Modal
      width='md'
      open={open}
      handleClose={isSubmitting ? () => {} : () => handleCloseModal(false)}
      title='Add Cloud Foundry Account'
      loader={isSubmitting}
    >
      <MarkDowns data={SETUP_INSTRUCTIONS} sx={{ width: 'auto' }} />
      <Grid container>
        <Box sx={{ mt: 2, width: '100%' }}>
          <Input
            value={accountNameValue}
            size='sm'
            id='cf-account-name'
            label='Account Name'
            required
            onChange={handleAccountNameChange}
            error={validationError.accountName || undefined}
          />
        </Box>
        <Box sx={{ mt: 2, width: '100%' }}>
          <Input
            value={cfApiUrl}
            size='sm'
            id='cf-api-url'
            label='CF API URL'
            required
            onChange={handleCfApiUrlChange}
            error={validationError.cfApiUrl || undefined}
            placeholder='https://api.sys.example.com'
          />
        </Box>

        <Grid item xs={12} sx={{ mt: 1, mb: 1 }}>
          <RadioGroup row value={authType} onChange={(e) => setAuthType(e.target.value)}>
            <FormControlLabel value='token' control={<Radio size='small' />} label='Bearer Token' />
            <FormControlLabel value='uaa' control={<Radio size='small' />} label='UAA OAuth2' />
          </RadioGroup>
        </Grid>

        {authType === 'token' && (
          <Box sx={{ mt: 2, width: '100%' }}>
            <Input
              value={bearerToken}
              size='sm'
              id='cf-bearer-token'
              label='Bearer Token'
              required
              type='textarea'
              rows={4}
              onChange={setBearerToken}
              placeholder='Paste the bearer token or K8s service account token'
            />
          </Box>
        )}

        {authType === 'uaa' && (
          <>
            <Box sx={{ mt: 2, width: '100%' }}>
              <Input value={clientId} size='sm' id='cf-uaa-client-id' label='UAA Client ID' required onChange={setClientId} />
            </Box>
            <Box sx={{ mt: 2, width: '100%' }}>
              <Input
                value={clientSecret}
                size='sm'
                id='cf-uaa-client-secret'
                label='UAA Client Secret'
                required
                type='password'
                onChange={setClientSecret}
              />
            </Box>
          </>
        )}

        <Box sx={{ mt: 1 }}>
          <Checkbox size='sm' checked={skipSSL} onChange={(next) => setSkipSSL(next)} label='Skip SSL Verification (for self-signed certificates)' />
        </Box>
      </Grid>

      <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
        <Grid item>
          <Button id='cf-cancel-btn' size='md' tone='secondary' onClick={() => handleCloseModal(false)} disabled={isSubmitting}>
            Cancel
          </Button>
        </Grid>
        <Grid item>
          <Button id='cf-save-btn' size='md' tone='primary' disabled={!isFormValid() || isSubmitting} onClick={handleSubmit}>
            Save
          </Button>
        </Grid>
      </Grid>
    </Modal>
  );
};

AddCloudFoundryAccountModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
};

export default AddCloudFoundryAccountModal;
