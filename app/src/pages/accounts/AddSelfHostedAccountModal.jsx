import { Box, Grid } from '@mui/material';
import React, { useState } from 'react';
import PropTypes from 'prop-types';
import apiAccount from '@api1/account';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { isK8sAccountNameValid } from 'src/utils/common';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import MarkDowns from '@shared/viewers/MarkDowns';
import AccountEnvToggle, { DEFAULT_ACCOUNT_ENV } from '@shared/forms/AccountEnvToggle';

const SETUP_INSTRUCTIONS = `### Self-Hosted VM Fleet
  A self-hosted account groups virtual machines we reach through an agent rather
  than a cloud provider API — on-premise fleets, private clouds, or anywhere the
  machines are not visible to AWS, Azure or GCP.
  ### Step 1. Enter an account name
  ### Step 2. Choose the environment
  ### Step 3. Save
     - Then add a **VM Agent** integration for each network segment you want to
       reach. Each one gets its own credentials and install command.`;

const AddSelfHostedAccountModal = ({ open, onClose }) => {
  const [accountNameValue, setAccountNameValue] = useState('');
  const [accountEnvValue, setAccountEnvValue] = useState(DEFAULT_ACCOUNT_ENV);
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);

  const resetForm = () => {
    setAccountNameValue('');
    setAccountEnvValue(DEFAULT_ACCOUNT_ENV);
    setValidationError({});
  };

  const handleCloseModal = (refresh) => {
    resetForm();
    onClose(refresh);
  };

  const handleAccountNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setValidationError({
        accountName:
          'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore',
      });
    } else {
      setValidationError({});
    }
    setAccountNameValue(value);
  };

  const handleSubmit = () => {
    setIsSubmitting(true);

    // Name and environment only. There is no provider to authenticate against,
    // and no credentials are issued here: a VM account holds many foragers, one
    // per network segment, and each gets its own identity from the VM agent
    // integration along with its install command.
    //
    // account_type says what is managed, cloud_provider says who runs it —
    // hence `vm` + `SelfHosted` rather than the same word twice.
    const body = {
      account_name: accountNameValue,
      account_env: accountEnvValue,
      cloud_provider: 'SelfHosted',
      account_type: 'vm',
    };

    apiAccount
      .createAccount(body)
      .then((res) => {
        if (res?.data?.status === 'ERROR') {
          snackbar.error(`Failed to add self-hosted account - ${res?.data?.message}`);
          return;
        }
        snackbar.success('Self-hosted account added successfully');
        handleCloseModal(true);
      })
      .catch((error) => {
        snackbar.error('Failed to add self-hosted account');
        console.error('Failed to add self-hosted account:', error);
      })
      .finally(() => {
        setIsSubmitting(false);
      });
  };

  const isSaveDisabled = isSubmitting || !accountNameValue || Boolean(validationError.accountName);

  return (
    <Modal
      width='md'
      open={open}
      handleClose={isSubmitting ? () => {} : () => handleCloseModal(false)}
      title='Add Self-Hosted Account'
      loader={isSubmitting}
    >
      <MarkDowns data={SETUP_INSTRUCTIONS} sx={{ width: 'auto' }} />
      <Grid container>
        <Box sx={{ mt: 2, width: '100%' }}>
          <Input
            value={accountNameValue}
            size='sm'
            id='selfhosted-account-name'
            label='Account Name'
            placeholder='on-prem-fleet'
            required
            onChange={handleAccountNameChange}
            error={validationError.accountName || undefined}
          />
        </Box>
        <Box sx={{ mt: 2, width: '100%' }}>
          <AccountEnvToggle value={accountEnvValue} onChange={setAccountEnvValue} />
        </Box>
        <Box sx={{ mt: 3, width: '100%', display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-2)' }}>
          <Button tone='secondary' size='md' onClick={() => handleCloseModal(false)} disabled={isSubmitting}>
            Cancel
          </Button>
          <Button tone='primary' size='md' onClick={handleSubmit} disabled={isSaveDisabled}>
            Save
          </Button>
        </Box>
      </Grid>
    </Modal>
  );
};

AddSelfHostedAccountModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
};

export default AddSelfHostedAccountModal;
