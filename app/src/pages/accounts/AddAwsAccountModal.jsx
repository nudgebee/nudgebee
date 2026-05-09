import { Grid, CircularProgress, Typography, TextField, RadioGroup, FormControlLabel, Radio, Checkbox, Alert, Link } from '@mui/material';
import { useState, useRef, useCallback, useEffect } from 'react';
import apiAccount from '@api1/account';
import { Modal } from '@components1/common/modal';
import { isK8sAccountNameValid } from 'src/utils/common';
import apiKubernetes1 from '@api1/kubernetes1';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';
import { inputSx } from '@data/themes/inputField';

const SETUP_INSTRUCTIONS = `### Step 1. Give Account Name
  ### Step 2. Click on Connect via AWS Console
     - It will get redirected to Cloud Formation link.
     - All the values are pre-filled. **DO NOT** change any value in the field.
     - Create the stack.
  ### Step 3. Wait for auto-detection
     - Once the CloudFormation stack is created, the account will be detected automatically.
     - No need to copy any values.`;

const POLL_INTERVAL_MS = 7000;
const ROLE_ARN_REGEX = /^arn:aws:iam::\d{12}:role\/.+$/;

const AddAwsAccountModal = ({ open, onClose }) => {
  const [accountNameValue, setAccountNameValue] = useState('');
  const [validationError, setValidationError] = useState({});
  const [isFetchingCloudFormationUrl, setIsFetchingCloudFormationUrl] = useState(false);
  const [externalId, setExternalId] = useState('');
  const [isPolling, setIsPolling] = useState(false);
  const [accessMode, setAccessMode] = useState('readwrite');
  const [ssmAccess, setSsmAccess] = useState(false);
  const [showManualInput, setShowManualInput] = useState(false);
  const [roleArn, setRoleArn] = useState('');
  const [isSubmittingManual, setIsSubmittingManual] = useState(false);
  const pollingRef = useRef(null);

  const stopPolling = useCallback(() => {
    if (pollingRef.current) {
      clearInterval(pollingRef.current);
      pollingRef.current = null;
    }
    setIsPolling(false);
  }, []);

  const resetForm = useCallback(() => {
    setAccountNameValue('');
    setExternalId('');
    setValidationError({});
    setIsFetchingCloudFormationUrl(false);
    setAccessMode('readwrite');
    setSsmAccess(false);
    setShowManualInput(false);
    setRoleArn('');
    setIsSubmittingManual(false);
    stopPolling();
  }, [stopPolling]);

  useEffect(() => {
    return () => {
      stopPolling();
    };
  }, [stopPolling]);

  const handleCloseModal = (wasSuccessful = false) => {
    resetForm();
    onClose(wasSuccessful);
  };

  const startPolling = (reqId) => {
    setIsPolling(true);
    pollingRef.current = setInterval(async () => {
      try {
        const res = await apiAccount.awsOnboardStatus(reqId);
        const statusData = res?.data?.aws_onboard_status;
        if (statusData && statusData.status === 'completed') {
          stopPolling();
          if (statusData.is_reconnected) {
            snackbar.success(`Existing AWS Account "${statusData.account_name}" reconnected successfully.`);
          } else {
            snackbar.success(`AWS Account "${statusData.account_name}" connected successfully.`);
          }
          handleCloseModal(true);
        }
      } catch {
        // Keep polling on transient errors
      }
    }, POLL_INTERVAL_MS);
  };

  const handleNavToAwsConsole = () => {
    setIsFetchingCloudFormationUrl(true);
    apiKubernetes1
      .getAWSCloudFormationURL({
        account_name: accountNameValue,
        account_type: 'cloud',
        cloud_provider: 'AWS',
        account_access: accessMode === 'readonly' ? 'readonly' : undefined,
        ssm_access: ssmAccess || undefined,
      })
      .then((res) => {
        const cloudFormation = res?.data?.data?.aws_cloud_formation || {};
        if (cloudFormation?.url) {
          setExternalId(cloudFormation.external_id);
          window.open(cloudFormation.url, '_blank');
          if (cloudFormation.auto_detection_enabled) {
            startPolling(cloudFormation.external_id);
          } else {
            setShowManualInput(true);
          }
        } else {
          snackbar.error('Failed to get Cloud Formation URL');
        }
      })
      .catch(() => {
        snackbar.error('Failed to get Cloud Formation URL');
      })
      .finally(() => {
        setIsFetchingCloudFormationUrl(false);
      });
  };

  const handleManualSubmit = () => {
    if (!roleArn || !ROLE_ARN_REGEX.test(roleArn)) {
      snackbar.error('Please enter a valid IAM Role ARN (e.g. arn:aws:iam::123456789012:role/RoleName)');
      return;
    }
    setIsSubmittingManual(true);
    stopPolling();
    apiAccount
      .createAccount({
        account_name: accountNameValue,
        cloud_provider: 'AWS',
        account_type: 'cloud',
        assume_role: roleArn,
        account_access: accessMode === 'readonly' ? 'readonly' : undefined,
      })
      .then((res) => {
        if (res?.data?.status === 'SUCCESS') {
          snackbar.success(`AWS Account "${accountNameValue}" connected successfully.`);
          handleCloseModal(true);
        } else {
          snackbar.error(res?.data?.message || 'Failed to connect account');
        }
      })
      .catch(() => {
        snackbar.error('Failed to connect account. Please verify the Role ARN and try again.');
      })
      .finally(() => {
        setIsSubmittingManual(false);
      });
  };

  const handleAWSAccountNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setValidationError((prevState) => ({
        ...prevState,
        awsAccountName:
          'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore',
      }));
    } else {
      setValidationError((prevState) => {
        const newState = { ...prevState };
        delete newState.awsAccountName;
        return newState;
      });
    }
    setAccountNameValue(value);
  };

  return (
    <Modal
      width='md'
      open={open}
      handleClose={isPolling || isSubmittingManual ? () => {} : () => handleCloseModal(false)}
      title={'Add AWS Account'}
      loader={isSubmittingManual}
    >
      <MarkDowns data={SETUP_INSTRUCTIONS} sx={{ width: 'auto' }} />
      <Grid container>
        <TextField
          sx={inputSx}
          value={accountNameValue}
          size='small'
          margin='normal'
          fullWidth
          id='account-name'
          label='Display Name'
          required
          onChange={(e) => handleAWSAccountNameChange(e.target.value)}
          error={!!validationError.awsAccountName}
          helperText={validationError.awsAccountName}
          disabled={!!externalId}
        />

        <Grid item xs={12} sx={{ mt: 1, mb: 1 }}>
          <Typography variant='subtitle2' sx={{ mb: 0.5 }}>
            Access Mode
          </Typography>
          <RadioGroup row value={accessMode} onChange={(e) => setAccessMode(e.target.value)}>
            <FormControlLabel value='readwrite' control={<Radio size='small' />} label='Standard' disabled={!!externalId} />
            <FormControlLabel value='readonly' control={<Radio size='small' />} label='Read-Only' disabled={!!externalId} />
          </RadioGroup>
          {accessMode === 'readonly' && (
            <Alert severity='info' sx={{ mt: 1 }}>
              Read-only mode does not grant write permissions to your AWS account. The following features will be unavailable: CloudWatch alarm
              creation, EventBridge real-time event tracking, and automated recommendation actions.
            </Alert>
          )}
        </Grid>

        <Grid item xs={12} sx={{ mb: 1 }}>
          <FormControlLabel
            control={<Checkbox checked={ssmAccess} onChange={(e) => setSsmAccess(e.target.checked)} size='small' />}
            label={
              <Typography variant='body2'>
                <strong>Enable SSM Parameter Store access</strong> — allows Nudgebee to read parameter values. Only enable if your parameters do not
                contain secrets.
              </Typography>
            }
            disabled={!!externalId}
          />
        </Grid>

        <CustomButton
          id='connect-aws-console-btn'
          loading={isFetchingCloudFormationUrl}
          size='Medium'
          disabled={!!externalId || !!validationError.awsAccountName || !accountNameValue}
          text='Connect via AWS Console'
          onClick={handleNavToAwsConsole}
        />
      </Grid>

      {(isPolling || showManualInput) && (
        <Grid container direction='column' mt={2} mb={2}>
          {isPolling && (
            <Grid container alignItems='center' spacing={1} mb={1}>
              <Grid item>
                <CircularProgress size={20} />
              </Grid>
              <Grid item>
                <Typography variant='body2' color='text.secondary'>
                  Waiting for CloudFormation stack to complete... The account will be detected automatically.
                </Typography>
              </Grid>
            </Grid>
          )}

          {!showManualInput && isPolling && (
            <Grid item sx={{ mt: 1 }}>
              <Link component='button' variant='body2' onClick={() => setShowManualInput(true)} sx={{ textDecoration: 'none' }}>
                Having trouble? Connect manually using Role ARN
              </Link>
            </Grid>
          )}

          {showManualInput && (
            <Grid container direction='column' sx={{ mt: 1 }}>
              <Typography variant='subtitle2' sx={{ mb: 0.5 }}>
                Enter the IAM Role ARN from the CloudFormation stack outputs
              </Typography>
              <TextField
                sx={inputSx}
                value={roleArn}
                size='small'
                fullWidth
                id='role-arn'
                label='IAM Role ARN'
                placeholder='arn:aws:iam::123456789012:role/NudgebeeRole'
                onChange={(e) => setRoleArn(e.target.value)}
                disabled={isSubmittingManual}
              />
              <Grid container justifyContent='flex-start' sx={{ mt: 1 }}>
                <CustomButton
                  id='manual-connect-btn'
                  loading={isSubmittingManual}
                  size='Medium'
                  disabled={!roleArn || isSubmittingManual}
                  text='Connect'
                  onClick={handleManualSubmit}
                />
              </Grid>
            </Grid>
          )}
        </Grid>
      )}

      <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
        <Grid item>
          <CustomButton
            id='cancel-btn'
            size='Medium'
            text='Cancel'
            variant='secondary'
            onClick={() => handleCloseModal(false)}
            disabled={isSubmittingManual}
          />
        </Grid>
      </Grid>
    </Modal>
  );
};

export default AddAwsAccountModal;
