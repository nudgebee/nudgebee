import { Grid, TextField, Typography, Box, IconButton, Tooltip, Chip, Stepper, Step, StepLabel, Divider } from '@mui/material';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import RefreshIcon from '@mui/icons-material/Refresh';
import { useState, useEffect, useRef } from 'react';
import apiAccount from '@api1/account';
import { Modal } from '@components1/common/modal';
import { isK8sAccountNameValid } from 'src/utils/common';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { inputSx } from '@data/themes/inputField';
import { colors } from 'src/utils/colors';
import { CopyIconBlue } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

const STATUS_COLORS = {
  active: '#4CAF50',
  pending: '#FF9800',
  disabled: '#9E9E9E',
  failed: '#F44336',
};

const AddAwsOrgModal = ({ open, onClose }) => {
  const [step, setStep] = useState(1); // 1=name, 2=token+params+status
  const [accountName, setAccountName] = useState('');
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);

  // Step 2 state
  const [verificationToken, setVerificationToken] = useState('');
  const [templateUrl, setTemplateUrl] = useState('');
  const [launchUrl, setLaunchUrl] = useState('');
  const [stackSetParameters, setStackSetParameters] = useState({});

  // Status state (shown inline in step 2 after launch)
  const [orgStatus, setOrgStatus] = useState(null);
  const [memberAccounts, setMemberAccounts] = useState([]);
  const [isPolling, setIsPolling] = useState(false);
  const pollingRef = useRef(null);

  useEffect(() => {
    return () => {
      if (pollingRef.current) {
        clearInterval(pollingRef.current);
      }
    };
  }, []);

  const resetForm = () => {
    setStep(1);
    setAccountName('');
    setValidationError({});
    setIsSubmitting(false);
    setVerificationToken('');
    setTemplateUrl('');
    setLaunchUrl('');
    setStackSetParameters({});
    setOrgStatus(null);
    setMemberAccounts([]);
    setIsPolling(false);
    if (pollingRef.current) {
      clearInterval(pollingRef.current);
      pollingRef.current = null;
    }
  };

  const handleCloseModal = (wasSuccessful = false) => {
    resetForm();
    onClose(wasSuccessful);
  };

  const handleNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setValidationError((prev) => ({
        ...prev,
        accountName: 'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore.',
      }));
    } else {
      setValidationError((prev) => {
        const newState = { ...prev };
        delete newState.accountName;
        return newState;
      });
    }
    setAccountName(value);
  };

  const handleGenerateToken = () => {
    setIsSubmitting(true);
    apiAccount
      .awsOrgOnboard({ account_name: accountName })
      .then((res) => {
        const data = res?.data?.aws_org_onboard;
        if (data) {
          setVerificationToken(data.verification_token);
          setTemplateUrl(data.stackset_template_url);
          setLaunchUrl(data.stackset_launch_url);
          setStackSetParameters(data.stackset_parameters || {});
          setStep(2);
          snackbar.success('Organization onboarding initiated.');
        } else {
          const errorMsg = res?.errors?.[0]?.message || 'Failed to initiate org onboarding';
          snackbar.error(errorMsg);
        }
      })
      .catch(() => {
        snackbar.error('Failed to initiate org onboarding');
      })
      .finally(() => {
        setIsSubmitting(false);
      });
  };

  const handleCopyToken = () => {
    navigator.clipboard.writeText(verificationToken);
    snackbar.success('Verification token copied to clipboard');
  };

  const handleCopyTemplateUrl = () => {
    navigator.clipboard.writeText(templateUrl);
    snackbar.success('Template URL copied to clipboard');
  };

  const handleLaunchStackSet = () => {
    window.open(launchUrl, '_blank');
    if (!isPolling) {
      startPolling();
    }
  };

  const startPolling = () => {
    setIsPolling(true);
    fetchOrgStatus();
    pollingRef.current = setInterval(fetchOrgStatus, 10000);
  };

  const fetchOrgStatus = () => {
    apiAccount
      .awsOrgStatus()
      .then((res) => {
        const data = res?.data?.aws_org_status;
        if (data) {
          setOrgStatus(data.org_status);
          setMemberAccounts(data.member_accounts || []);
        }
      })
      .catch(() => {
        // Silently ignore polling errors
      });
  };

  const renderStep1 = () => (
    <>
      <Grid container mb={2}>
        <Grid item xs={12}>
          <Box>
            <Typography sx={{ fontSize: '20px', fontWeight: 500, color: '#374151', mb: 1 }}>Set Organization Name</Typography>
            <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px' }}>
              Enter a display name for your AWS Organization. This will be used to identify the organization in Nudgebee.
            </Typography>
          </Box>
        </Grid>
        <Grid item xs={12} md={6}>
          <TextField
            sx={{ ...inputSx, mt: 2 }}
            value={accountName}
            size='small'
            fullWidth
            id='org-name'
            label='Organization Display Name'
            required
            onChange={(e) => handleNameChange(e.target.value)}
            error={!!validationError.accountName}
            helperText={validationError.accountName}
            disabled={isSubmitting}
          />
        </Grid>
      </Grid>

      <Divider sx={{ my: 1, borderStyle: 'dotted', borderColor: '#D0D0D0', borderBottomWidth: '2px' }} />

      <Grid item xs={12} mt={2}>
        <Typography sx={{ fontWeight: 500, fontSize: '13px', mb: 1, color: '#374151' }}>What happens next</Typography>
        <Box sx={{ p: 1.5, backgroundColor: '#f8f9fa', borderRadius: '8px', border: '1px solid #dee2e6' }}>
          <Box sx={{ pl: 2 }}>
            <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
              <span style={{ fontWeight: 'bold' }}>1. Generate credentials:</span> A verification token and StackSet template URL will be created for
              you.
            </Typography>
            <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
              <span style={{ fontWeight: 'bold' }}>2. Deploy StackSet:</span> Launch the CloudFormation StackSet in your AWS Management Account
              console.
            </Typography>
            <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
              <span style={{ fontWeight: 'bold' }}>3. Use service-managed permissions:</span> Deploy to your entire organization or selected OUs with
              automatic deployment for new accounts.
            </Typography>
            <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
              <span style={{ fontWeight: 'bold' }}>4. Automatic registration:</span> Member accounts will appear automatically as the StackSet deploys
              to each account.
            </Typography>
          </Box>
        </Box>
        <Box sx={{ mt: 1.5, p: 1.5, backgroundColor: '#FFF8E1', borderRadius: '8px', border: '1px solid #FFE082' }}>
          <Typography sx={{ fontSize: '12px', lineHeight: 1.5, color: '#5D4037' }}>
            <span style={{ fontWeight: 'bold' }}>Note:</span> StackSets deploy only to member accounts, not the management account itself. If you also
            need to monitor your management account, add it separately using <span style={{ fontWeight: 'bold' }}>Add AWS Account</span>.
          </Typography>
        </Box>
      </Grid>

      <Grid container spacing={2} mt={3} mb={2} justifyContent='flex-end'>
        <Grid item>
          <CustomButton
            id='cancel-org-btn'
            size='Medium'
            text='Cancel'
            variant='secondary'
            onClick={() => handleCloseModal(false)}
            disabled={isSubmitting}
          />
        </Grid>
        <Grid item>
          <CustomButton
            id='generate-token-btn'
            size='Medium'
            text='Next'
            loading={isSubmitting}
            disabled={!accountName || !!validationError.accountName || isSubmitting}
            onClick={handleGenerateToken}
          />
        </Grid>
      </Grid>
    </>
  );

  const renderStep2 = () => (
    <Box mt={2} mb={2}>
      {/* Verification Token Section */}
      <Grid item xs={12}>
        <Box>
          <Typography sx={{ fontSize: '20px', fontWeight: 500, color: '#374151', mb: 1 }}>Verification Token</Typography>
          <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px' }}>
            Copy this token now — it is shown only once and will be needed as a StackSet parameter.
          </Typography>
        </Box>
      </Grid>

      <Box display='flex' alignItems='flex-start' mt={2}>
        <Box
          sx={{
            backgroundColor: '#2F2F2F',
            padding: 1.5,
            borderRadius: 1,
            flexGrow: 1,
            overflowX: 'auto',
          }}
        >
          <Typography variant='body2' sx={{ fontSize: '14px', color: 'white', fontFamily: 'monospace', wordBreak: 'break-all' }}>
            {verificationToken}
          </Typography>

          <Box sx={{ display: 'flex', gap: 1, mt: 1 }}>
            <CustomButton
              id='copy-token-btn'
              size='Small'
              text='Copy Token'
              startIcon={<SafeIcon src={CopyIconBlue} alt='copy token' height={16} width={16} />}
              variant='tertiary'
              onClick={handleCopyToken}
              sx={{ fontSize: '12px' }}
            />
          </Box>
        </Box>
      </Box>

      <Divider sx={{ my: 3 }} />

      {/* Template URL Section */}
      <Grid item xs={12}>
        <Box>
          <Typography sx={{ fontSize: '20px', fontWeight: 500, color: '#374151', mb: 1 }}>StackSet Template URL</Typography>
          <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px' }}>
            This URL points to the CloudFormation template that will be deployed to each member account.
          </Typography>
        </Box>
      </Grid>

      <Box display='flex' alignItems='flex-start' mt={2}>
        <Box
          sx={{
            backgroundColor: '#2F2F2F',
            padding: 1.5,
            borderRadius: 1,
            flexGrow: 1,
            overflowX: 'auto',
          }}
        >
          <Typography variant='body2' sx={{ fontSize: '13px', color: 'white', fontFamily: 'monospace', wordBreak: 'break-all' }}>
            {templateUrl}
          </Typography>

          <Box sx={{ display: 'flex', gap: 1, mt: 1 }}>
            <CustomButton
              id='copy-template-url-btn'
              size='Small'
              text='Copy URL'
              startIcon={<SafeIcon src={CopyIconBlue} alt='copy url' height={16} width={16} />}
              variant='tertiary'
              onClick={handleCopyTemplateUrl}
              sx={{ fontSize: '12px' }}
            />
          </Box>
        </Box>
      </Box>

      {/* StackSet Parameters */}
      {Object.keys(stackSetParameters).length > 0 && (
        <>
          <Divider sx={{ my: 3 }} />

          <Grid item xs={12}>
            <Box>
              <Typography sx={{ fontSize: '20px', fontWeight: 500, color: '#374151', mb: 1 }}>StackSet Parameters</Typography>
              <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px', mb: 2 }}>
                Fill these parameter values in the AWS CloudFormation console when creating the StackSet.
              </Typography>
            </Box>
          </Grid>

          <Box sx={{ p: 1.5, backgroundColor: '#f8f9fa', borderRadius: '8px', border: '1px solid #dee2e6' }}>
            {Object.entries(stackSetParameters).map(([key, value]) => (
              <Box
                key={key}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  py: 0.75,
                  borderBottom: '1px solid #E8E8E8',
                  '&:last-child': { borderBottom: 'none' },
                }}
              >
                <Box sx={{ minWidth: '200px' }}>
                  <Typography variant='body2' sx={{ fontWeight: 600, fontSize: '12px', color: '#374151' }}>
                    {key}
                  </Typography>
                </Box>
                <Box
                  sx={{
                    flex: 1,
                    fontFamily: 'monospace',
                    fontSize: '11px',
                    wordBreak: 'break-all',
                    mx: 1,
                    color: '#374151',
                  }}
                >
                  {value}
                </Box>
                <CustomButton
                  size='xSmall'
                  text='Copy'
                  startIcon={<SafeIcon src={CopyIconBlue} alt={`copy ${key}`} height={14} width={14} />}
                  variant='tertiary'
                  onClick={() => {
                    navigator.clipboard.writeText(value);
                    snackbar.success(`${key} copied`);
                  }}
                  sx={{ fontSize: '11px', minWidth: 'auto' }}
                />
              </Box>
            ))}
          </Box>
        </>
      )}

      <Divider sx={{ my: 3 }} />

      {/* Launch Button */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px' }}>
          Open the AWS CloudFormation console to create the StackSet with the template and parameters above.
        </Typography>
        <CustomButton
          id='launch-stackset-btn'
          size='Medium'
          text='Launch StackSet in AWS'
          onClick={handleLaunchStackSet}
          endIcon={<OpenInNewIcon fontSize='small' />}
          sx={{ ml: 2, minWidth: '200px' }}
        />
      </Box>

      {/* Organization Status — appears after launching */}
      {isPolling && (
        <>
          <Divider sx={{ my: 3 }} />

          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <Typography sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>Organization Status</Typography>
                <Chip
                  label={orgStatus || 'loading...'}
                  size='small'
                  sx={{
                    bgcolor: STATUS_COLORS[orgStatus] || '#9E9E9E',
                    color: '#fff',
                    fontWeight: 600,
                    fontSize: '11px',
                  }}
                />
              </Box>
              <Tooltip title='Refresh status'>
                <IconButton size='small' onClick={fetchOrgStatus}>
                  <RefreshIcon fontSize='small' />
                </IconButton>
              </Tooltip>
            </Box>

            <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px', mb: 2 }}>
              {memberAccounts.length} member account{memberAccounts.length !== 1 ? 's' : ''} registered
              {isPolling && ' (auto-refreshing every 10s)'}
            </Typography>

            {memberAccounts.length > 0 && (
              <Box sx={{ maxHeight: '200px', overflowY: 'auto' }}>
                <Box
                  sx={{
                    display: 'grid',
                    gridTemplateColumns: '1fr 1fr 100px 140px',
                    gap: '4px 12px',
                    p: 1,
                    backgroundColor: '#f8f9fa',
                    borderRadius: '8px 8px 0 0',
                    border: '1px solid #dee2e6',
                    borderBottom: 'none',
                    fontWeight: 600,
                    fontSize: '12px',
                    color: '#374151',
                  }}
                >
                  <Box>Account Number</Box>
                  <Box>Name</Box>
                  <Box>Status</Box>
                  <Box>Created At</Box>
                </Box>
                {memberAccounts.map((account) => (
                  <Box
                    key={account.account_id}
                    sx={{
                      display: 'grid',
                      gridTemplateColumns: '1fr 1fr 100px 140px',
                      gap: '4px 12px',
                      p: 1,
                      border: '1px solid #dee2e6',
                      borderTop: 'none',
                      fontSize: '13px',
                      '&:last-child': { borderRadius: '0 0 8px 8px' },
                    }}
                  >
                    <Box sx={{ fontFamily: 'monospace', fontSize: '12px' }}>{account.account_number}</Box>
                    <Box>{account.account_name}</Box>
                    <Box>
                      <Chip
                        label={account.status}
                        size='small'
                        sx={{
                          bgcolor: STATUS_COLORS[account.status] || '#9E9E9E',
                          color: '#fff',
                          fontSize: '10px',
                          height: '20px',
                        }}
                      />
                    </Box>
                    <Box sx={{ fontSize: '11px', color: colors.secondary.dark }}>
                      {account.created_at ? new Date(account.created_at).toLocaleString() : '-'}
                    </Box>
                  </Box>
                ))}
              </Box>
            )}

            {memberAccounts.length === 0 && (
              <Box
                sx={{
                  p: 2,
                  textAlign: 'center',
                  backgroundColor: '#f8f9fa',
                  borderRadius: '8px',
                  border: '1px solid #dee2e6',
                }}
              >
                <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px' }}>
                  No member accounts registered yet. Deploy the StackSet and accounts will appear here as they register.
                </Typography>
              </Box>
            )}
          </Box>
        </>
      )}

      {/* Footer buttons */}
      <Grid container spacing={2} mt={3} mb={2} justifyContent='flex-end'>
        <Grid item>
          <CustomButton
            id='close-org-btn'
            size='Medium'
            text='Close'
            variant='secondary'
            onClick={() => handleCloseModal(isPolling || memberAccounts.length > 0)}
          />
        </Grid>
      </Grid>
    </Box>
  );

  return (
    <Modal
      width='md'
      open={open}
      handleClose={isSubmitting ? () => {} : () => handleCloseModal(false)}
      title='AWS Organization Onboarding'
      loader={isSubmitting}
    >
      <Box sx={{ px: 3 }}>
        <Box sx={{ mb: 3, mt: 2 }}>
          <Stepper activeStep={step - 1} orientation='horizontal'>
            <Step>
              <StepLabel>Set Organization Name</StepLabel>
            </Step>
            <Step>
              <StepLabel>Deploy StackSet</StepLabel>
            </Step>
          </Stepper>
        </Box>

        {step === 1 && renderStep1()}
        {step === 2 && renderStep2()}
      </Box>
    </Modal>
  );
};

export default AddAwsOrgModal;
