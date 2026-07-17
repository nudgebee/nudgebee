import { Grid, Typography, Box, Stepper, Step, StepLabel } from '@mui/material';
import { Chip } from '@ui/Chip';
import { Divider } from '@ui/Divider';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import RefreshIcon from '@mui/icons-material/Refresh';
import { useState, useEffect, useRef } from 'react';
import apiAccount from '@api1/account';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { isK8sAccountNameValid } from 'src/utils/common';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';
import { CopyIconBlue } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';

const STATUS_COLORS = {
  active: ds.green[400],
  pending: ds.amber[400],
  disabled: ds.gray[400],
  failed: ds.red[500],
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
        const data = res?.data?.accounts_create_aws_org;
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
      <Grid container mb={ds.space[4]}>
        <Grid item xs={12}>
          <Box>
            <Typography sx={{ fontSize: ds.text.heading, fontWeight: ds.weight.medium, color: ds.brand[500], mb: ds.space[2] }}>
              Set Organization Name
            </Typography>
            <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: ds.text.small }}>
              Enter a display name for your AWS Organization. This will be used to identify the organization in Nudgebee.
            </Typography>
          </Box>
        </Grid>
        <Grid item xs={12} md={6}>
          <Box sx={{ mt: ds.space[4] }}>
            <Input
              value={accountName}
              size='sm'
              id='org-name'
              label='Organization Display Name'
              required
              onChange={handleNameChange}
              error={validationError.accountName || undefined}
              disabled={isSubmitting}
            />
          </Box>
        </Grid>
      </Grid>

      <Divider style='dashed' color={ds.brand[200]} thickness={2} sx={{ my: ds.space[2] }} />

      <Grid item xs={12} mt={ds.space[4]}>
        <Typography sx={{ fontWeight: ds.weight.medium, fontSize: ds.text.body, mb: ds.space[2], color: ds.brand[500] }}>
          What happens next
        </Typography>
        <Box sx={{ p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.lg, border: `1px solid ${ds.gray[300]}` }}>
          <Box sx={{ pl: ds.space[4] }}>
            <Typography component='div' sx={{ fontSize: ds.text.small, lineHeight: 1.4, mb: ds.space[1] }}>
              <span style={{ fontWeight: 'bold' }}>1. Generate credentials:</span> A verification token and StackSet template URL will be created for
              you.
            </Typography>
            <Typography component='div' sx={{ fontSize: ds.text.small, lineHeight: 1.4, mb: ds.space[1] }}>
              <span style={{ fontWeight: 'bold' }}>2. Deploy StackSet:</span> Launch the CloudFormation StackSet in your AWS Management Account
              console.
            </Typography>
            <Typography component='div' sx={{ fontSize: ds.text.small, lineHeight: 1.4, mb: ds.space[1] }}>
              <span style={{ fontWeight: 'bold' }}>3. Use service-managed permissions:</span> Deploy to your entire organization or selected OUs with
              automatic deployment for new accounts.
            </Typography>
            <Typography component='div' sx={{ fontSize: ds.text.small, lineHeight: 1.4, mb: ds.space[1] }}>
              <span style={{ fontWeight: 'bold' }}>4. Automatic registration:</span> Member accounts will appear automatically as the StackSet deploys
              to each account.
            </Typography>
          </Box>
        </Box>
        <Box
          sx={{
            mt: ds.space[3],
            p: ds.space[3],
            backgroundColor: ds.amber[100],
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.yellow[300]}`,
          }}
        >
          <Typography sx={{ fontSize: ds.text.small, lineHeight: 1.5, color: ds.pink[700] }}>
            <span style={{ fontWeight: 'bold' }}>Note:</span> StackSets deploy only to member accounts, not the management account itself. If you also
            need to monitor your management account, add it separately using <span style={{ fontWeight: 'bold' }}>Add AWS Account</span>.
          </Typography>
        </Box>
      </Grid>

      <Box
        sx={{
          display: 'flex',
          justifyContent: 'flex-end',
          gap: ds.space[4],
          mt: ds.space[5],
          mb: ds.space[4],
          '& button': { minWidth: ds.space.mul(1, 35) },
        }}
      >
        <Button id='cancel-org-btn' size='md' tone='secondary' onClick={() => handleCloseModal(false)} disabled={isSubmitting}>
          Cancel
        </Button>
        <Button
          id='generate-token-btn'
          size='md'
          tone='primary'
          loading={isSubmitting}
          disabled={!accountName || !!validationError.accountName || isSubmitting}
          onClick={handleGenerateToken}
        >
          Next
        </Button>
      </Box>
    </>
  );

  const renderStep2 = () => (
    <Box mt={ds.space[4]} mb={ds.space[4]}>
      {/* Verification Token Section */}
      <Grid item xs={12}>
        <Box>
          <Typography sx={{ fontSize: ds.text.heading, fontWeight: ds.weight.medium, color: ds.brand[500], mb: ds.space[2] }}>
            Verification Token
          </Typography>
          <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: ds.text.small }}>
            Copy this token now — it is shown only once and will be needed as a StackSet parameter.
          </Typography>
        </Box>
      </Grid>

      <Box display='flex' alignItems='flex-start' mt={ds.space[4]}>
        <Box
          sx={{
            backgroundColor: ds.brand[700],
            padding: ds.space[3],
            borderRadius: ds.radius.sm,
            flexGrow: 1,
            overflowX: 'auto',
          }}
        >
          <Typography variant='body2' sx={{ fontSize: ds.text.bodyLg, color: ds.background[100], fontFamily: 'monospace', wordBreak: 'break-all' }}>
            {verificationToken}
          </Typography>

          <Box sx={{ display: 'flex', gap: ds.space[2], mt: ds.space[2] }}>
            <Button
              id='copy-token-btn'
              size='sm'
              tone='ghost'
              icon={<SafeIcon src={CopyIconBlue} alt='copy token' height={16} width={16} />}
              iconPlacement='start'
              onClick={handleCopyToken}
              sx={{ fontSize: ds.text.small }}
            />
          </Box>
        </Box>
      </Box>

      <Divider sx={{ my: ds.space[5] }} />

      {/* Template URL Section */}
      <Grid item xs={12}>
        <Box>
          <Typography sx={{ fontSize: ds.text.heading, fontWeight: ds.weight.medium, color: ds.brand[500], mb: ds.space[2] }}>
            StackSet Template URL
          </Typography>
          <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: ds.text.small }}>
            This URL points to the CloudFormation template that will be deployed to each member account.
          </Typography>
        </Box>
      </Grid>

      <Box display='flex' alignItems='flex-start' mt={ds.space[4]}>
        <Box
          sx={{
            backgroundColor: ds.brand[700],
            padding: ds.space[3],
            borderRadius: ds.radius.sm,
            flexGrow: 1,
            overflowX: 'auto',
          }}
        >
          <Typography variant='body2' sx={{ fontSize: ds.text.body, color: ds.background[100], fontFamily: 'monospace', wordBreak: 'break-all' }}>
            {templateUrl}
          </Typography>

          <Box sx={{ display: 'flex', gap: ds.space[2], mt: ds.space[2] }}>
            <Button
              id='copy-template-url-btn'
              size='sm'
              tone='ghost'
              icon={<SafeIcon src={CopyIconBlue} alt='copy url' height={16} width={16} />}
              iconPlacement='start'
              onClick={handleCopyTemplateUrl}
              sx={{ fontSize: ds.text.small }}
            />
          </Box>
        </Box>
      </Box>

      {/* StackSet Parameters */}
      {Object.keys(stackSetParameters).length > 0 && (
        <>
          <Divider sx={{ my: ds.space[5] }} />

          <Grid item xs={12}>
            <Box>
              <Typography sx={{ fontSize: ds.text.heading, fontWeight: ds.weight.medium, color: ds.brand[500], mb: ds.space[2] }}>
                StackSet Parameters
              </Typography>
              <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: ds.text.small, mb: ds.space[4] }}>
                Fill these parameter values in the AWS CloudFormation console when creating the StackSet.
              </Typography>
            </Box>
          </Grid>

          <Box sx={{ p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.lg, border: `1px solid ${ds.gray[300]}` }}>
            {Object.entries(stackSetParameters).map(([key, value]) => (
              <Box
                key={key}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  py: ds.space.mul(0, 3),
                  borderBottom: `1px solid ${ds.gray[200]}`,
                  '&:last-child': { borderBottom: 'none' },
                }}
              >
                <Box sx={{ minWidth: ds.space.mul(1, 50) }}>
                  <Typography variant='body2' sx={{ fontWeight: ds.weight.semibold, fontSize: ds.text.small, color: ds.brand[500] }}>
                    {key}
                  </Typography>
                </Box>
                <Box
                  sx={{
                    flex: 1,
                    fontFamily: 'monospace',
                    fontSize: ds.text.caption,
                    wordBreak: 'break-all',
                    mx: ds.space[2],
                    color: ds.brand[500],
                  }}
                >
                  {value}
                </Box>
                <Button
                  size='xs'
                  tone='ghost'
                  icon={<SafeIcon src={CopyIconBlue} alt={`copy ${key}`} height={14} width={14} />}
                  iconPlacement='start'
                  onClick={() => {
                    navigator.clipboard.writeText(value);
                    snackbar.success(`${key} copied`);
                  }}
                  sx={{ fontSize: ds.text.caption, minWidth: 'auto' }}
                />
              </Box>
            ))}
          </Box>
        </>
      )}

      <Divider sx={{ my: ds.space[5] }} />

      {/* Launch Button */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: ds.text.small }}>
          Open the AWS CloudFormation console to create the StackSet with the template and parameters above.
        </Typography>
        <Button
          id='launch-stackset-btn'
          size='md'
          tone='primary'
          onClick={handleLaunchStackSet}
          icon={<OpenInNewIcon fontSize='small' />}
          iconPlacement='end'
        >
          Launch StackSet in AWS
        </Button>
      </Box>

      {/* Organization Status — appears after launching */}
      {isPolling && (
        <>
          <Divider sx={{ my: ds.space[5] }} />

          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: ds.space[2] }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                <Typography sx={{ fontSize: ds.text.heading, fontWeight: ds.weight.medium, color: ds.brand[500] }}>Organization Status</Typography>
                <Chip
                  size='sm'
                  sx={{
                    bgcolor: STATUS_COLORS[orgStatus] || ds.gray[400],
                    color: ds.background[100],
                    fontWeight: ds.weight.semibold,
                    fontSize: ds.text.caption,
                    border: 'none',
                  }}
                >
                  {orgStatus || 'loading...'}
                </Chip>
              </Box>
              <Button
                tone='ghost'
                composition='icon-only'
                size='sm'
                icon={<RefreshIcon fontSize='small' />}
                onClick={fetchOrgStatus}
                aria-label='Refresh status'
                tooltip='Refresh status'
              />
            </Box>

            <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: ds.text.small, mb: ds.space[4] }}>
              {memberAccounts.length} member account{memberAccounts.length !== 1 ? 's' : ''} registered
              {isPolling && ' (auto-refreshing every 10s)'}
            </Typography>

            {memberAccounts.length > 0 && (
              <Box sx={{ maxHeight: ds.space.mul(1, 50), overflowY: 'auto' }}>
                <Box
                  sx={{
                    display: 'grid',
                    gridTemplateColumns: `1fr 1fr ${ds.space.mul(1, 25)} ${ds.space.mul(1, 35)}`,
                    gap: `${ds.space[1]} ${ds.space[3]}`,
                    p: ds.space[2],
                    backgroundColor: ds.background[200],
                    borderRadius: `${ds.radius.lg} ${ds.radius.lg} 0 0`,
                    border: `1px solid ${ds.gray[300]}`,
                    borderBottom: 'none',
                    fontWeight: ds.weight.semibold,
                    fontSize: ds.text.small,
                    color: ds.brand[500],
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
                      gridTemplateColumns: `1fr 1fr ${ds.space.mul(1, 25)} ${ds.space.mul(1, 35)}`,
                      gap: `${ds.space[1]} ${ds.space[3]}`,
                      p: ds.space[2],
                      border: `1px solid ${ds.gray[300]}`,
                      borderTop: 'none',
                      fontSize: ds.text.body,
                      '&:last-child': { borderRadius: `0 0 ${ds.radius.lg} ${ds.radius.lg}` },
                    }}
                  >
                    <Box sx={{ fontFamily: 'monospace', fontSize: ds.text.small }}>{account.account_number}</Box>
                    <Box>{account.account_name}</Box>
                    <Box>
                      <Chip
                        size='sm'
                        sx={{
                          bgcolor: STATUS_COLORS[account.status] || ds.gray[400],
                          color: ds.background[100],
                          fontSize: ds.text.caption,
                          height: ds.space.mul(1, 5),
                          border: 'none',
                        }}
                      >
                        {account.status}
                      </Chip>
                    </Box>
                    <Box sx={{ fontSize: ds.text.caption, color: ds.gray[400] }}>
                      {account.created_at ? new Date(account.created_at).toLocaleString() : '-'}
                    </Box>
                  </Box>
                ))}
              </Box>
            )}

            {memberAccounts.length === 0 && (
              <Box
                sx={{
                  p: ds.space[4],
                  textAlign: 'center',
                  backgroundColor: ds.background[200],
                  borderRadius: ds.radius.lg,
                  border: `1px solid ${ds.gray[300]}`,
                }}
              >
                <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: ds.text.small }}>
                  No member accounts registered yet. Deploy the StackSet and accounts will appear here as they register.
                </Typography>
              </Box>
            )}
          </Box>
        </>
      )}

      {/* Footer buttons */}
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'flex-end',
          gap: ds.space[4],
          mt: ds.space[5],
          mb: ds.space[4],
          '& button': { minWidth: ds.space.mul(1, 35) },
        }}
      >
        <Button id='close-org-btn' size='md' tone='secondary' onClick={() => handleCloseModal(isPolling || memberAccounts.length > 0)}>
          Close
        </Button>
      </Box>
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
      <Box sx={{ px: ds.space[5] }}>
        <Box sx={{ mb: ds.space[5], mt: ds.space[4] }}>
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
