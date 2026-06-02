import { Grid, CircularProgress, Typography, RadioGroup, FormControlLabel, Radio, Alert, Link, Tabs, Tab, Box } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import { useState, useRef, useCallback, useEffect } from 'react';
import apiAccount from '@api1/account';
import { Modal } from '@components1/common/modal';
import { Input } from '@components1/ds/Input';
import { isK8sAccountNameValid } from 'src/utils/common';
import apiKubernetes1 from '@api1/kubernetes1';
import { Button } from '@components1/ds/Button';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';
import ValidationResultBanner from '@components1/accounts/ValidationResultBanner';

const CF_INSTRUCTIONS = `### Step 1. Give Account Name
  ### Step 2. Click on Connect via AWS Console
     - It will get redirected to Cloud Formation link.
     - All the values are pre-filled. **DO NOT** change any value in the field.
     - Create the stack.
  ### Step 3. Wait for auto-detection
     - Once the CloudFormation stack is created, the account will be detected automatically.
     - No need to copy any values.`;

const ROLE_INSTRUCTIONS = `### IAM Role ARN
  Use this flow if you already have a cross-account IAM role that Nudgebee can assume.
  The role must allow \`sts:AssumeRole\`, \`cur:DescribeReportDefinitions\`, and \`s3:GetBucketLocation\` / \`s3:ListBucket\` on the CUR bucket.
  Click **Validate** before connecting — we will probe STS, Cost & Usage Report discovery, and CUR S3 access upfront.`;

const KEYS_INSTRUCTIONS = `### Access Keys
  Use this flow when you cannot grant a cross-account role (segregated billing accounts, dev/test, etc.).
  Create an IAM user with the same CUR + read-only permissions as the CloudFormation template, then paste the **Access Key ID** and **Secret Access Key** below.
  Click **Validate** before connecting — we will probe STS, Cost & Usage Report discovery, and CUR S3 access upfront.`;

const POLL_INTERVAL_MS = 7000;
const ROLE_ARN_REGEX = /^arn:aws:iam::\d{12}:role\/.+$/;
const AWS_ACCESS_KEY_REGEX = /^[A-Z0-9]{16,128}$/;

const TAB_CLOUDFORMATION = 0;
const TAB_ROLE_ARN = 1;
const TAB_ACCESS_KEYS = 2;

const AddAwsAccountModal = ({ open, onClose }) => {
  const [activeTab, setActiveTab] = useState(TAB_CLOUDFORMATION);
  const [accountNameValue, setAccountNameValue] = useState('');
  const [validationError, setValidationError] = useState({});
  const [isFetchingCloudFormationUrl, setIsFetchingCloudFormationUrl] = useState(false);
  const [externalId, setExternalId] = useState('');
  const [isPolling, setIsPolling] = useState(false);
  const [accessMode, setAccessMode] = useState('readwrite');
  const [ssmAccess, setSsmAccess] = useState(false);
  const [showManualInput, setShowManualInput] = useState(false);
  const [roleArn, setRoleArn] = useState('');
  const [externalIdInput, setExternalIdInput] = useState('');
  const [accessKeyId, setAccessKeyId] = useState('');
  const [secretAccessKey, setSecretAccessKey] = useState('');
  const [keysRegion, setKeysRegion] = useState('us-east-1');
  const [isValidating, setIsValidating] = useState(false);
  const [validationResult, setValidationResult] = useState(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
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
    setExternalIdInput('');
    setAccessKeyId('');
    setSecretAccessKey('');
    setKeysRegion('us-east-1');
    setIsSubmitting(false);
    setIsValidating(false);
    setValidationResult(null);
    setActiveTab(TAB_CLOUDFORMATION);
    stopPolling();
  }, [stopPolling]);

  useEffect(() => {
    return () => {
      stopPolling();
    };
  }, [stopPolling]);

  // Invalidate prior validation result when inputs change. Prevents users
  // from changing credentials after a successful validate and slipping the
  // submit through unverified.
  useEffect(() => {
    setValidationResult(null);
  }, [activeTab, roleArn, externalIdInput, accessKeyId, secretAccessKey, keysRegion]);

  const handleCloseModal = (wasSuccessful = false) => {
    resetForm();
    onClose(wasSuccessful);
  };

  const startPolling = (reqId) => {
    setIsPolling(true);
    pollingRef.current = setInterval(async () => {
      try {
        const res = await apiAccount.awsOnboardStatus(reqId);
        const statusData = res?.data?.accounts_check_aws_onboarding;
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

  const handleValidate = async () => {
    if (activeTab === TAB_ROLE_ARN) {
      if (!ROLE_ARN_REGEX.test(roleArn)) {
        snackbar.error('Please enter a valid IAM Role ARN (e.g. arn:aws:iam::123456789012:role/RoleName)');
        return;
      }
    } else if (activeTab === TAB_ACCESS_KEYS) {
      if (!AWS_ACCESS_KEY_REGEX.test(accessKeyId)) {
        snackbar.error('Please enter a valid AWS Access Key ID (16+ chars, uppercase / digits)');
        return;
      }
      if (!secretAccessKey || secretAccessKey.length < 20) {
        snackbar.error('Please enter the AWS Secret Access Key');
        return;
      }
    }

    setIsValidating(true);
    setValidationResult(null);
    try {
      const payload = { cloud_provider: 'AWS' };
      if (activeTab === TAB_ROLE_ARN) {
        payload.assume_role = roleArn;
        if (externalIdInput) {
          payload.external_id = externalIdInput;
        }
      } else if (activeTab === TAB_ACCESS_KEYS) {
        payload.access_key = accessKeyId;
        payload.access_secret = secretAccessKey;
        if (keysRegion) {
          payload.region = keysRegion;
        }
      }
      const result = await apiAccount.validateCloudCredentials(payload);
      setValidationResult(result || { success: false, errorMessage: 'Validation returned no result.' });
    } catch (err) {
      setValidationResult({
        success: false,
        errorMessage: err?.message || 'Failed to validate credentials. Please try again.',
      });
    } finally {
      setIsValidating(false);
    }
  };

  const handleConnect = () => {
    if (!validationResult?.success) {
      snackbar.error('Please validate credentials before connecting.');
      return;
    }
    setIsSubmitting(true);
    stopPolling();

    const payload = {
      account_name: accountNameValue,
      cloud_provider: 'AWS',
      account_type: 'cloud',
      account_access: accessMode === 'readonly' ? 'readonly' : undefined,
    };
    if (activeTab === TAB_ROLE_ARN) {
      payload.assume_role = roleArn;
      if (externalIdInput) {
        payload.external_id = externalIdInput;
      }
    } else if (activeTab === TAB_ACCESS_KEYS) {
      payload.access_key = accessKeyId;
      payload.access_secret = secretAccessKey;
      if (keysRegion) {
        payload.region = keysRegion;
      }
    }

    apiAccount
      .createAccount(payload)
      .then((res) => {
        if (res?.data?.status === 'SUCCESS') {
          snackbar.success(`AWS Account "${accountNameValue}" connected successfully.`);
          handleCloseModal(true);
        } else {
          snackbar.error(res?.data?.message || 'Failed to connect account');
        }
      })
      .catch((err) => {
        snackbar.error(err?.message || 'Failed to connect account. Please verify the credentials and try again.');
      })
      .finally(() => {
        setIsSubmitting(false);
      });
  };

  const handleManualCfSubmit = () => {
    if (!roleArn || !ROLE_ARN_REGEX.test(roleArn)) {
      snackbar.error('Please enter a valid IAM Role ARN (e.g. arn:aws:iam::123456789012:role/RoleName)');
      return;
    }
    setIsSubmitting(true);
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
        setIsSubmitting(false);
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

  const tabLocked = !!externalId || isSubmitting;
  const accountNameOk = accountNameValue && !validationError.awsAccountName;

  const renderInstructions = () => {
    if (activeTab === TAB_CLOUDFORMATION) {
      return <MarkDowns data={CF_INSTRUCTIONS} sx={{ width: 'auto' }} />;
    }
    if (activeTab === TAB_ROLE_ARN) {
      return <MarkDowns data={ROLE_INSTRUCTIONS} sx={{ width: 'auto' }} />;
    }
    return <MarkDowns data={KEYS_INSTRUCTIONS} sx={{ width: 'auto' }} />;
  };

  const renderCloudFormationTab = () => (
    <Grid container direction='column'>
      <Button
        id='connect-aws-console-btn'
        tone='primary'
        loading={isFetchingCloudFormationUrl}
        size='md'
        disabled={!!externalId || !accountNameOk}
        onClick={handleNavToAwsConsole}
      >
        Connect via AWS Console
      </Button>

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
              <Input
                value={roleArn}
                size='sm'
                id='cf-role-arn'
                label='IAM Role ARN'
                placeholder='arn:aws:iam::123456789012:role/NudgebeeRole'
                onChange={setRoleArn}
                disabled={isSubmitting}
              />
              <Grid container justifyContent='flex-start' sx={{ mt: 1 }}>
                <Button
                  id='manual-connect-btn'
                  tone='primary'
                  loading={isSubmitting}
                  size='md'
                  disabled={!roleArn || isSubmitting}
                  onClick={handleManualCfSubmit}
                >
                  Connect
                </Button>
              </Grid>
            </Grid>
          )}
        </Grid>
      )}
    </Grid>
  );

  const renderRoleArnTab = () => (
    <Grid container direction='column' spacing={1}>
      <Grid item>
        <Input
          value={roleArn}
          size='sm'
          id='aws-role-arn'
          label='IAM Role ARN'
          placeholder='arn:aws:iam::123456789012:role/NudgebeeRole'
          onChange={setRoleArn}
          disabled={isValidating || isSubmitting}
          required
        />
      </Grid>
      <Grid item>
        <Input
          value={externalIdInput}
          size='sm'
          id='aws-external-id'
          label='External ID (optional)'
          placeholder='Required only if the trust policy specifies an external ID'
          onChange={setExternalIdInput}
          disabled={isValidating || isSubmitting}
        />
      </Grid>
    </Grid>
  );

  const renderAccessKeysTab = () => (
    <Grid container direction='column' spacing={1}>
      <Grid item>
        <Input
          value={accessKeyId}
          size='sm'
          id='aws-access-key-id'
          label='AWS Access Key ID'
          placeholder='AKIAIOSFODNN7EXAMPLE'
          onChange={(value) => setAccessKeyId(value.trim())}
          disabled={isValidating || isSubmitting}
          required
        />
      </Grid>
      <Grid item>
        <Input
          value={secretAccessKey}
          size='sm'
          id='aws-secret-access-key'
          label='AWS Secret Access Key'
          placeholder='wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'
          type='password'
          onChange={setSecretAccessKey}
          disabled={isValidating || isSubmitting}
          required
        />
      </Grid>
      <Grid item>
        <Input
          value={keysRegion}
          size='sm'
          id='aws-region'
          label='AWS Region'
          placeholder='us-east-1'
          onChange={(value) => setKeysRegion(value.trim())}
          disabled={isValidating || isSubmitting}
          help='Region used to bootstrap the AWS SDK. CUR discovery always runs in us-east-1.'
        />
      </Grid>
      <Grid item>
        <Alert severity='info' sx={{ mt: 1 }}>
          Access keys are stored encrypted at rest. Prefer the CloudFormation flow when possible — keys grant broader access and rotation is your
          responsibility.
        </Alert>
      </Grid>
    </Grid>
  );

  const renderValidateAndConnect = () => (
    <Grid container direction='column' sx={{ mt: 2 }}>
      <Grid container spacing={1}>
        <Grid item>
          <Button
            id='aws-validate-btn'
            tone='secondary'
            loading={isValidating}
            size='md'
            disabled={!accountNameOk || isValidating || isSubmitting}
            onClick={handleValidate}
          >
            Validate
          </Button>
        </Grid>
        <Grid item>
          <Button
            id='aws-connect-btn'
            tone='primary'
            loading={isSubmitting}
            size='md'
            disabled={!accountNameOk || !validationResult?.success || isSubmitting}
            onClick={handleConnect}
          >
            Connect
          </Button>
        </Grid>
      </Grid>

      <ValidationResultBanner result={validationResult} />

      {validationResult?.success && validationResult?.cur?.reportName && (
        <Alert severity='success' sx={{ mt: 1 }}>
          <Typography variant='body2'>
            Detected Cost &amp; Usage Report: <strong>{validationResult.cur.reportName}</strong> (bucket{' '}
            <code>{validationResult.cur.bucketName}</code>, region <code>{validationResult.cur.region}</code>).
          </Typography>
        </Alert>
      )}
    </Grid>
  );

  return (
    <Modal
      width='md'
      open={open}
      handleClose={isPolling || isSubmitting ? () => {} : () => handleCloseModal(false)}
      title={'Add AWS Account'}
      loader={isSubmitting}
    >
      <Tabs
        value={activeTab}
        onChange={(_, newValue) => {
          if (tabLocked) {
            return;
          }
          setActiveTab(newValue);
        }}
        variant='fullWidth'
        sx={{ mb: 1, borderBottom: 1, borderColor: 'divider' }}
        aria-label='AWS onboarding method'
      >
        <Tab id='aws-tab-cloudformation' label='CloudFormation' />
        <Tab id='aws-tab-role-arn' label='IAM Role ARN' />
        <Tab id='aws-tab-access-keys' label='Access Keys' />
      </Tabs>

      <Box sx={{ mb: 1 }}>{renderInstructions()}</Box>

      <Grid container>
        <Box sx={{ mt: 2, width: '100%' }}>
          <Input
            value={accountNameValue}
            size='sm'
            id='account-name'
            label='Display Name'
            required
            onChange={handleAWSAccountNameChange}
            error={validationError.awsAccountName || undefined}
            disabled={!!externalId}
          />
        </Box>

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

        {activeTab === TAB_CLOUDFORMATION && (
          <Grid item xs={12} sx={{ mb: 1 }}>
            <Checkbox
              size='sm'
              checked={ssmAccess}
              onChange={(next) => setSsmAccess(next)}
              disabled={!!externalId}
              label={
                <Typography variant='body2' component='span'>
                  <strong>Enable SSM Parameter Store access</strong> — allows Nudgebee to read parameter values. Only enable if your parameters do not
                  contain secrets.
                </Typography>
              }
            />
          </Grid>
        )}

        {activeTab === TAB_CLOUDFORMATION && renderCloudFormationTab()}
        {activeTab === TAB_ROLE_ARN && (
          <Grid item xs={12}>
            {renderRoleArnTab()}
            {renderValidateAndConnect()}
          </Grid>
        )}
        {activeTab === TAB_ACCESS_KEYS && (
          <Grid item xs={12}>
            {renderAccessKeysTab()}
            {renderValidateAndConnect()}
          </Grid>
        )}
      </Grid>

      <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
        <Grid item>
          <Button id='cancel-btn' tone='secondary' size='md' onClick={() => handleCloseModal(false)} disabled={isSubmitting}>
            Cancel
          </Button>
        </Grid>
      </Grid>
    </Modal>
  );
};

export default AddAwsAccountModal;
