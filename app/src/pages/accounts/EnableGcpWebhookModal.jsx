import {
  Grid,
  TextField,
  InputAdornment,
  IconButton,
  Typography,
  Stepper,
  Step,
  StepLabel,
  StepConnector,
  stepConnectorClasses,
  styled,
  Box,
  Collapse,
  Alert,
  CircularProgress,
  Link,
} from '@mui/material';
import { ContentCopy, Check, HelpOutline, ExpandMore, ExpandLess, CheckCircleOutline, ErrorOutline } from '@mui/icons-material';
import { useState, useEffect, useCallback } from 'react';
import PropTypes from 'prop-types';
import apiAccount from '@api1/account';
import apiIntegrations from '@api1/integrations';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';
import { inputSx } from '@data/themes/inputField';
import { colors } from 'src/utils/colors';
import { safeJSONParse } from 'src/utils/common';

const StepConnectorStyled = styled(StepConnector)(() => ({
  [`&.${stepConnectorClasses.alternativeLabel}`]: {
    top: 10,
    left: 'calc(-50% + 16px)',
    right: 'calc(50% + 16px)',
  },
  [`&.${stepConnectorClasses.active}, &.${stepConnectorClasses.completed}`]: {
    [`& .${stepConnectorClasses.line}`]: {
      borderColor: colors.success,
      borderTopWidth: 2,
    },
  },
  [`& .${stepConnectorClasses.line}`]: {
    borderColor: colors.border.secondary,
    borderTopWidth: 1,
    borderRadius: 1,
  },
}));

const StepIconCustom = ({ active, completed, icon }) => {
  const styles = completed
    ? { backgroundColor: colors.success, border: 'none', color: colors.white }
    : {
        backgroundColor: colors.white,
        border: active ? `1px solid ${colors.success}` : `1px solid ${colors.border.secondary}`,
        color: active ? colors.success : colors.text.greyDark,
      };

  return (
    <Box
      sx={{
        width: '24px',
        height: '24px',
        borderRadius: '50%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: '14px',
        fontWeight: 'bold',
        ...styles,
      }}
    >
      {completed ? <Check sx={{ fontSize: '16px' }} /> : icon}
    </Box>
  );
};

const STEPS = ['Prerequisites', 'Verify Permissions', 'Enable Alerts'];

const buildSetupGuide = (projectId) => `### Required IAM Role

Your GCP service account needs the **Monitoring Editor** role (\`roles/monitoring.editor\`) for auto-setup.

### How to grant the permission

1. Open the [IAM page](https://console.cloud.google.com/iam-admin/iam?project=${projectId}) for project **${projectId}**
2. Find the Nudgebee service account email
3. Click the pencil icon to edit permissions
4. Click **Add Another Role** and select **Monitoring Editor**
5. Click **Save**
`;

const WEBHOOK_INSTRUCTIONS = `### Complete the setup in GCP Console

1. Click **Open GCP Console** below — it opens the webhook notification channel creation page
2. Paste the **Webhook URL** above into the **Endpoint URL** field
3. Give the channel a display name (e.g., **Nudgebee Alerts**)
4. Click **Save**
5. Then attach this notification channel to your alert policies
`;

// Look up an existing gcp_monitoring_webhook integration whose name matches
// either the new id-keyed pattern or the legacy account-name pattern.
// Returns an existedIntegration-shaped object or null.
const lookupExistingGcpWebhook = async (account) => {
  const idKeyedName = `GCP Monitoring - ${account.id}`;
  const legacyName = `GCP Monitoring - ${account.account_name}`;
  try {
    const listResp = await apiIntegrations.listIntegrations({
      type: 'gcp_monitoring_webhook',
      limit: 1000,
      offset: 0,
    });
    const rows = listResp?.data?.data?.admin_get_integrations_v2?.rows || [];
    const match = rows.find((r) => (r.name === idKeyedName || r.name === legacyName) && r.status === 'enabled');
    if (!match) {
      return null;
    }
    const rawAccounts = match.integrations_cloud_accounts;
    const cloudAccounts = Array.isArray(rawAccounts) ? rawAccounts : safeJSONParse(rawAccounts) || [];
    const existingIds = cloudAccounts.map((ca) => ca.cloud_account_id).filter(Boolean);
    return {
      id: match.id,
      integration_config_name: match.name,
      accountIds: Array.from(new Set([...existingIds, account.id])),
    };
  } catch (lookupErr) {
    console.warn('GCP webhook integration lookup failed; will attempt create:', lookupErr);
    return null;
  }
};

// Attempt the GCP-side notification-channel auto-setup. Returns the
// resolution intent for setupStatus/setupFailReason rather than touching
// component state directly.
const attemptGcpAutoSetup = async (accountId, url) => {
  try {
    await apiAccount.setupGcpMonitoringWebhook(accountId, url);
    return { status: 'success' };
  } catch (err) {
    console.error('GCP webhook auto-setup failed:', err);
    const msg = err?.message || '';
    if (msg.includes('PermissionDenied') || msg.includes('Permission denied')) {
      return {
        status: 'fallback',
        reason: 'The service account lacks permission to create notification channels. Grant the Monitoring Editor role and try again.',
      };
    }
    return { status: 'fallback', reason: msg || 'Auto-setup failed. You can set it up manually below.' };
  }
};

const EnableGcpWebhookModal = ({ open, onClose, account, isAlreadyEnabled = false, existedIntegration = undefined }) => {
  const [showWizard, setShowWizard] = useState(!isAlreadyEnabled);
  const [step, setStep] = useState(0);
  const [guideExpanded, setGuideExpanded] = useState(false);

  // Step 1: Permission check
  const [isCheckingPermission, setIsCheckingPermission] = useState(false);
  const [permissionResult, setPermissionResult] = useState(null);

  // Step 2: Integration creation + auto-setup
  const [isSettingUp, setIsSettingUp] = useState(false);
  const [setupStatus, setSetupStatus] = useState(null); // 'success' | 'fallback'
  const [setupFailReason, setSetupFailReason] = useState(''); // reason for fallback
  const [webhookUrl, setWebhookUrl] = useState('');
  const [setupError, setSetupError] = useState('');
  const [copied, setCopied] = useState(false);

  const projectId = account?.account_number || '';

  const clearForm = () => {
    setShowWizard(!isAlreadyEnabled);
    setStep(0);
    setGuideExpanded(false);
    setIsCheckingPermission(false);
    setPermissionResult(null);
    setIsSettingUp(false);
    setSetupStatus(null);
    setSetupFailReason('');
    setWebhookUrl('');
    setSetupError('');
    setCopied(false);
  };

  const handleClose = () => {
    clearForm();
    onClose();
  };

  const checkPermission = useCallback(async () => {
    if (!account?.id) {
      return;
    }
    setIsCheckingPermission(true);
    setPermissionResult(null);
    try {
      const result = await apiAccount.checkGcpMonitoringPermission(account.id);
      setPermissionResult(result);
    } catch (err) {
      setPermissionResult({ has_permission: false, error_detail: err?.message || 'Failed to check permissions.' });
    } finally {
      setIsCheckingPermission(false);
    }
  }, [account]);

  // Auto-run permission check when entering step 1
  useEffect(() => {
    if (step === 1) {
      checkPermission();
    }
  }, [step, checkPermission]);

  // Auto-create integration + attempt auto-setup when entering step 2
  useEffect(() => {
    if (step !== 2 || setupStatus || isSettingUp || setupError) {
      return;
    }

    const setupWebhook = async () => {
      setIsSettingUp(true);
      setSetupError('');
      try {
        // New integrations are named `GCP Monitoring - <account_id>` so a
        // later account rename can't drift the name. Older rows still use
        // `<account_name>`; lookupExistingGcpWebhook covers both shapes.
        const existing = existedIntegration || (await lookupExistingGcpWebhook(account));
        const idKeyedName = `GCP Monitoring - ${account.id}`;

        // 1. Create or update integration config to get token
        const payload = existing
          ? {
              integration_id: existing.id,
              integration_name: 'gcp_monitoring_webhook',
              integration_config_name: existing.integration_config_name,
              account_ids: existing.accountIds,
            }
          : {
              integration_name: 'gcp_monitoring_webhook',
              integration_config_name: idKeyedName,
              account_ids: [account.id],
              integration_config_values: [],
            };
        const resp = await apiIntegrations.addIntegrations(payload);
        const respErrors = resp?.data?.errors;
        if (respErrors?.length) {
          setSetupError(respErrors[0]?.message || 'Failed to enable real-time alerts. Please try again.');
          return;
        }
        const data = resp?.data?.data?.integrations_create_config;
        const tokenConfig = data?.configs?.find((c) => c.name === 'token');
        if (!tokenConfig?.value) {
          setSetupError('Failed to generate webhook token. Please try again.');
          return;
        }
        const url = `${window.location.origin}/api/webhooks/gcp-monitoring?token=${tokenConfig.value}`;
        setWebhookUrl(url);

        // 2. Try auto-creating notification channel in GCP (only if permission check passed)
        if (!permissionResult?.has_permission) {
          setSetupFailReason('Service account does not have the required permissions for auto-setup. You can set it up manually below.');
          setSetupStatus('fallback');
          return;
        }
        const autoSetup = await attemptGcpAutoSetup(account.id, url);
        if (autoSetup.status === 'success') {
          setSetupStatus('success');
        } else {
          setSetupFailReason(autoSetup.reason);
          setSetupStatus('fallback');
        }
      } catch (err) {
        setSetupError(err?.message || 'Failed to create webhook integration.');
      } finally {
        setIsSettingUp(false);
      }
    };

    setupWebhook();
  }, [step, setupStatus, isSettingUp, setupError, permissionResult, account]);

  const handleCopy = async (text) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      snackbar.success('Copied to clipboard');
      setTimeout(() => setCopied(false), 2000);
    } catch {
      snackbar.error('Failed to copy to clipboard');
    }
  };

  if (!open || !account) {
    return null;
  }

  return (
    <Modal width='md' open={open} handleClose={handleClose} title={isAlreadyEnabled && !showWizard ? 'Real-Time Alerts' : 'Enable Real-Time Alerts'}>
      <Box sx={{ px: 3, pt: 1, pb: 3 }}>
        {isAlreadyEnabled && !showWizard && (
          <>
            <Alert icon={<CheckCircleOutline fontSize='small' />} severity='success' sx={{ mb: 2, mt: 2 }}>
              Real-time alerts are enabled for project <strong>{projectId}</strong>.
            </Alert>
            <Typography sx={{ fontSize: '14px', color: colors.text.secondary, mb: 2 }}>
              A webhook notification channel is configured and attached to alert policies in this GCP project. Alerts will be forwarded to Nudgebee
              automatically.
            </Typography>
            <Grid container spacing={2} mt={1} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
              <Grid item>
                <CustomButton size='Medium' text='Close' variant='secondary' onClick={handleClose} />
              </Grid>
              <Grid item>
                <CustomButton
                  size='Medium'
                  text='Re-run Setup'
                  onClick={() => {
                    setShowWizard(true);
                    setStep(0);
                  }}
                />
              </Grid>
            </Grid>
          </>
        )}

        {showWizard && (
          <>
            <Stepper activeStep={step} alternativeLabel connector={<StepConnectorStyled />} sx={{ mb: 3, mt: 2 }}>
              {STEPS.map((label, idx) => (
                <Step key={label} completed={step > idx}>
                  <StepLabel
                    StepIconComponent={StepIconCustom}
                    sx={{ '& .MuiStepLabel-label': { fontSize: '13px', mt: 0.5, color: step === idx ? colors.success : colors.text.greyDark } }}
                  >
                    {label}
                  </StepLabel>
                </Step>
              ))}
            </Stepper>

            {step === 0 && (
              <>
                <Typography sx={{ fontSize: '14px', color: colors.text.secondary, mb: 2 }}>
                  Forward GCP Cloud Monitoring alerts to Nudgebee in real-time via a webhook notification channel. This lets you see alerts alongside
                  your cloud resources without delay.
                </Typography>

                <Box sx={{ mb: 2 }}>
                  <Box
                    sx={{ display: 'flex', alignItems: 'center', cursor: 'pointer', gap: 0.5, py: 1 }}
                    onClick={() => setGuideExpanded(!guideExpanded)}
                  >
                    <HelpOutline sx={{ fontSize: 18, color: colors.text.tertiary }} />
                    <Typography sx={{ fontSize: 13, color: colors.text.tertiary, fontWeight: 500 }}>
                      Setup Guide — IAM permissions required
                    </Typography>
                    {guideExpanded ? <ExpandLess sx={{ color: colors.text.tertiary }} /> : <ExpandMore sx={{ color: colors.text.tertiary }} />}
                  </Box>
                  <Collapse in={guideExpanded}>
                    <Box
                      sx={{
                        mt: 1,
                        p: 2,
                        bgcolor: colors.background.suggestionCardBG,
                        borderRadius: '8px',
                        border: `1px solid ${colors.border.secondaryLightest}`,
                      }}
                    >
                      <MarkDowns data={buildSetupGuide(projectId)} sx={{ maxHeight: '300px', overflowY: 'auto' }} />
                    </Box>
                  </Collapse>
                </Box>

                <Grid container spacing={2} mt={1} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
                  <Grid item>
                    <CustomButton size='Medium' text='Cancel' variant='secondary' onClick={handleClose} />
                  </Grid>
                  <Grid item>
                    <CustomButton size='Medium' text='Next' onClick={() => setStep(1)} />
                  </Grid>
                </Grid>
              </>
            )}

            {step === 1 && (
              <>
                <Typography sx={{ fontSize: '14px', color: colors.text.secondary, mb: 2 }}>
                  Verifying that the service account has the required Cloud Monitoring permissions.
                </Typography>

                {isCheckingPermission && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 3, justifyContent: 'center' }}>
                    <CircularProgress size={24} />
                    <Typography sx={{ fontSize: '14px', color: colors.text.greyDark }}>Checking permissions...</Typography>
                  </Box>
                )}

                {!isCheckingPermission && permissionResult?.has_permission && (
                  <Alert icon={<CheckCircleOutline fontSize='small' />} severity='success' sx={{ mb: 2 }}>
                    Service account has the required monitoring permissions.
                  </Alert>
                )}

                {!isCheckingPermission && permissionResult && !permissionResult.has_permission && (
                  <>
                    <Alert icon={<ErrorOutline fontSize='small' />} severity='error' sx={{ mb: 2 }}>
                      {permissionResult.error_detail || 'Service account lacks Cloud Monitoring permissions.'}
                    </Alert>
                    <Box
                      sx={{
                        p: 1.5,
                        bgcolor: colors.background.suggestionCardBG,
                        borderRadius: '8px',
                        border: `1px solid ${colors.border.secondaryLightest}`,
                        mb: 2,
                      }}
                    >
                      <Typography sx={{ fontSize: '13px', color: colors.text.secondary, mb: 1 }}>
                        Grant the <strong>Monitoring Editor</strong> role to the service account, then click <strong>Re-check</strong>.
                      </Typography>
                      <Link
                        href={`https://console.cloud.google.com/iam-admin/iam?project=${projectId}`}
                        target='_blank'
                        rel='noopener noreferrer'
                        sx={{ fontSize: '13px' }}
                      >
                        Open GCP IAM Console →
                      </Link>
                    </Box>
                  </>
                )}

                <Grid container spacing={2} mt={1} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
                  <Grid item>
                    <CustomButton size='Medium' text='Back' variant='secondary' onClick={() => setStep(0)} />
                  </Grid>
                  {!isCheckingPermission && permissionResult && !permissionResult.has_permission && (
                    <Grid item>
                      <CustomButton size='Medium' text='Re-check' variant='secondary' onClick={checkPermission} />
                    </Grid>
                  )}
                  <Grid item>
                    <CustomButton size='Medium' text='Next' disabled={isCheckingPermission || !permissionResult} onClick={() => setStep(2)} />
                  </Grid>
                </Grid>
              </>
            )}

            {step === 2 && (
              <>
                {isSettingUp && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 3, justifyContent: 'center' }}>
                    <CircularProgress size={24} />
                    <Typography sx={{ fontSize: '14px', color: colors.text.greyDark }}>Setting up real-time alerts...</Typography>
                  </Box>
                )}

                {setupError && (
                  <Alert severity='error' sx={{ mb: 2 }}>
                    {setupError}
                  </Alert>
                )}

                {String(setupStatus) === 'success' && (
                  <Alert icon={<CheckCircleOutline fontSize='small' />} severity='success' sx={{ mb: 2 }}>
                    Real-time alerts enabled successfully. A webhook notification channel has been created in project <strong>{projectId}</strong>.
                  </Alert>
                )}

                {String(setupStatus) === 'fallback' && webhookUrl && (
                  <>
                    <Alert severity='warning' sx={{ mb: 2 }}>
                      {setupFailReason || 'Auto-setup could not complete. You can set it up manually using the instructions below.'}
                    </Alert>

                    <Grid container spacing={2} mb={2}>
                      <Grid item xs={12}>
                        <TextField
                          sx={inputSx}
                          value={webhookUrl}
                          size='small'
                          fullWidth
                          id='gcp-webhook-url'
                          label='Webhook URL'
                          InputProps={{
                            readOnly: true,
                            endAdornment: (
                              <InputAdornment position='end'>
                                <IconButton aria-label='copy webhook url' onClick={() => handleCopy(webhookUrl)} edge='end'>
                                  {copied ? <Check fontSize='small' color='success' /> : <ContentCopy fontSize='small' />}
                                </IconButton>
                              </InputAdornment>
                            ),
                          }}
                          helperText='Copy this URL and paste it as the Endpoint URL in GCP Console.'
                        />
                      </Grid>
                    </Grid>

                    <MarkDowns data={WEBHOOK_INSTRUCTIONS} sx={{ width: 'auto' }} />
                  </>
                )}

                <Grid container spacing={2} mt={1} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
                  <Grid item>
                    <CustomButton size='Medium' text='Close' variant='secondary' onClick={handleClose} />
                  </Grid>
                  {setupError && (
                    <Grid item>
                      <CustomButton
                        size='Medium'
                        text='Retry'
                        onClick={() => {
                          setSetupError('');
                          setSetupStatus(null);
                          setWebhookUrl('');
                        }}
                      />
                    </Grid>
                  )}
                  {String(setupStatus) === 'fallback' && (
                    <Grid item>
                      <CustomButton
                        size='Medium'
                        text='Open GCP Console'
                        onClick={() =>
                          window.open(
                            `https://console.cloud.google.com/monitoring/alerting/notifications/webhooks/new?project=${projectId}`,
                            '_blank',
                            'noopener,noreferrer'
                          )
                        }
                      />
                    </Grid>
                  )}
                </Grid>
              </>
            )}
          </>
        )}
      </Box>
    </Modal>
  );
};

EnableGcpWebhookModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  account: PropTypes.object,
  isAlreadyEnabled: PropTypes.bool,
  existedIntegration: PropTypes.object,
};

export default EnableGcpWebhookModal;
