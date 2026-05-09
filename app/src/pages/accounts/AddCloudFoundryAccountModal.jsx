import { Checkbox, FormControlLabel, Grid, Radio, RadioGroup, TextField } from '@mui/material';
import React, { useState } from 'react';
import PropTypes from 'prop-types';
import apiAccount from '@api1/account';
import { Modal } from '@components1/common/modal';
import { isK8sAccountNameValid } from 'src/utils/common';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';
import { inputSx } from '@data/themes/inputField';

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
        <TextField
          sx={inputSx}
          value={accountNameValue}
          size='small'
          margin='normal'
          fullWidth
          id='cf-account-name'
          label='Account Name'
          required
          onChange={(e) => handleAccountNameChange(e.target.value)}
          error={!!validationError.accountName}
          helperText={validationError.accountName}
        />
        <TextField
          sx={inputSx}
          value={cfApiUrl}
          size='small'
          margin='normal'
          fullWidth
          id='cf-api-url'
          label='CF API URL'
          required
          onChange={(e) => handleCfApiUrlChange(e.target.value)}
          error={!!validationError.cfApiUrl}
          helperText={validationError.cfApiUrl}
          placeholder='https://api.sys.example.com'
        />

        <Grid item xs={12} sx={{ mt: 1, mb: 1 }}>
          <RadioGroup row value={authType} onChange={(e) => setAuthType(e.target.value)}>
            <FormControlLabel value='token' control={<Radio size='small' />} label='Bearer Token' />
            <FormControlLabel value='uaa' control={<Radio size='small' />} label='UAA OAuth2' />
          </RadioGroup>
        </Grid>

        {authType === 'token' && (
          <TextField
            sx={inputSx}
            value={bearerToken}
            margin='normal'
            fullWidth
            id='cf-bearer-token'
            label='Bearer Token'
            required
            multiline
            rows={4}
            onChange={(e) => setBearerToken(e.target.value)}
            placeholder='Paste the bearer token or K8s service account token'
          />
        )}

        {authType === 'uaa' && (
          <>
            <TextField
              sx={inputSx}
              value={clientId}
              size='small'
              margin='normal'
              fullWidth
              id='cf-uaa-client-id'
              label='UAA Client ID'
              required
              onChange={(e) => setClientId(e.target.value)}
            />
            <TextField
              sx={inputSx}
              value={clientSecret}
              size='small'
              margin='normal'
              fullWidth
              id='cf-uaa-client-secret'
              label='UAA Client Secret'
              required
              type='password'
              onChange={(e) => setClientSecret(e.target.value)}
            />
          </>
        )}

        <FormControlLabel
          control={<Checkbox checked={skipSSL} onChange={(e) => setSkipSSL(e.target.checked)} size='small' />}
          label='Skip SSL Verification (for self-signed certificates)'
          sx={{ mt: 1 }}
        />
      </Grid>

      <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
        <Grid item>
          <CustomButton
            id='cf-cancel-btn'
            size='Medium'
            text='Cancel'
            variant='secondary'
            onClick={() => handleCloseModal(false)}
            disabled={isSubmitting}
          />
        </Grid>
        <Grid item>
          <CustomButton id='cf-save-btn' size='Medium' text='Save' disabled={!isFormValid() || isSubmitting} onClick={handleSubmit} />
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
