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
  Checkbox,
  Chip,
  Tab,
  Tabs,
} from '@mui/material';
import { ContentCopy, CheckCircleOutline, Check, HelpOutline, ExpandMore, ExpandLess, InfoOutlined, Search, ErrorOutline } from '@mui/icons-material';
import { useState, useMemo } from 'react';
import apiAccount from '@api1/account';
import apiIntegrations from '@api1/integrations';
// TODO: Re-enable after Pub/Sub testing
// import apiKubernetes1 from '@api1/kubernetes1';
import { Modal } from '@components1/common/modal';
import { isK8sAccountNameValid, parseHttpResponseBodyMessage } from 'src/utils/common';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';
import { inputSx } from '@data/themes/inputField';
import ValidationResultBanner from '@components1/accounts/ValidationResultBanner';

const StepConnectorStyled = styled(StepConnector)(() => ({
  [`&.${stepConnectorClasses.alternativeLabel}`]: {
    top: 10,
    left: 'calc(-50% + 16px)',
    right: 'calc(50% + 16px)',
  },
  [`&.${stepConnectorClasses.active}, &.${stepConnectorClasses.completed}`]: {
    [`& .${stepConnectorClasses.line}`]: {
      borderColor: '#16A34A',
      borderTopWidth: 2,
    },
  },
  [`& .${stepConnectorClasses.line}`]: {
    borderColor: '#D0D0D0',
    borderTopWidth: 1,
    borderRadius: 1,
  },
}));

const StepIconCustom = ({ active, completed, icon }) => {
  const styles = completed
    ? { backgroundColor: '#4caf50', border: 'none', color: 'white' }
    : { backgroundColor: 'white', border: active ? '1px solid #16A34A' : '1px solid #D0D0D0', color: active ? '#16A34A' : '#666' };

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

const SETUP_GUIDE_CONTENT = `### Prerequisites

Your GCP service account needs appropriate IAM permissions to monitor the resources in your project.

### Step 1. Create a Service Account

Go to **IAM & Admin** → **Service Accounts** → **Create Service Account** in the GCP Console.

### Step 2. Assign Permissions

Grant the service account the **Viewer** role (or a custom role with read permissions for the resources you want to monitor).

### Step 3. Download the JSON Key

1. Click the service account → **Keys** → **Add Key** → **Create new key**
2. Choose **JSON** format and download the file
3. Copy the entire content of the JSON key file and paste it below

[Open GCP Service Accounts Console](https://console.cloud.google.com/iam-admin/serviceaccounts)
`;

const WEBHOOK_MANUAL_INSTRUCTIONS = `### Manual Webhook Setup
1. Copy the **Webhook URL** below
2. Go to **GCP Console** → **Monitoring** → **Notification channels**
3. Click **Add new** → **Webhook** → paste the URL
4. Attach the notification channel to your alert policies
5. Alerts will be delivered to Nudgebee in real-time`;

const STEPS = ['Service Account', 'Projects', 'Billing'];

const AddGcpAccountModal = ({ open, onClose }) => {
  // Step 1: Service Account
  const [accountNameValue, setAccountNameValue] = useState('');
  const [serviceAccountKey, setServiceAccountKey] = useState('');
  const [serviceAccountData, setServiceAccountData] = useState(null);
  const [validationError, setValidationError] = useState({});
  const [isValidating, setIsValidating] = useState(false);
  const [validationResult, setValidationResult] = useState(null);
  const [guideExpanded, setGuideExpanded] = useState(false);

  // Step 2: Projects
  const [projectTab, setProjectTab] = useState(0);
  const [discoveredProjects, setDiscoveredProjects] = useState([]);
  const [selectedProjectIds, setSelectedProjectIds] = useState(new Set());
  const [isDiscovering, setIsDiscovering] = useState(false);
  const [discoveryError, setDiscoveryError] = useState('');
  const [manualProjectIds, setManualProjectIds] = useState('');
  const [projectSearchFilter, setProjectSearchFilter] = useState('');

  // Step 3: Billing
  const [billingProjectId, setBillingProjectId] = useState('');
  const [billingDatasetName, setBillingDatasetName] = useState('');
  const [billingTableName, setBillingTableName] = useState('');
  const [billingValidationResult, setBillingValidationResult] = useState(null);
  const [isValidatingBilling, setIsValidatingBilling] = useState(false);

  // Step 4: Real-Time Alerts (Webhook)
  const [webhookUrl, setWebhookUrl] = useState('');
  const [hasMonitoringPermission, setHasMonitoringPermission] = useState(false);
  const [isSettingUpWebhook, setIsSettingUpWebhook] = useState(false);
  const [webhookSetupResult, setWebhookSetupResult] = useState(null); // { succeeded: [...], failed: [...] }
  const [copied, setCopied] = useState({});

  // General
  const [step, setStep] = useState(0);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [onboardResults, setOnboardResults] = useState(null);

  const isLoading = isSubmitting || isSettingUpWebhook || isDiscovering || isValidating || isValidatingBilling;

  const clearForm = () => {
    setAccountNameValue('');
    setServiceAccountKey('');
    setServiceAccountData(null);
    setValidationError({});
    setIsValidating(false);
    setValidationResult(null);
    setGuideExpanded(false);
    setProjectTab(0);
    setDiscoveredProjects([]);
    setSelectedProjectIds(new Set());
    setIsDiscovering(false);
    setDiscoveryError('');
    setManualProjectIds('');
    setProjectSearchFilter('');
    setBillingProjectId('');
    setBillingDatasetName('');
    setBillingTableName('');
    setBillingValidationResult(null);
    setIsValidatingBilling(false);
    setWebhookUrl('');
    setHasMonitoringPermission(false);
    setIsSettingUpWebhook(false);
    setWebhookSetupResult(null);
    setCopied({});
    setStep(0);
    setIsSubmitting(false);
    setOnboardResults(null);
  };

  const handleCloseModal = (wasSuccessful = false) => {
    clearForm();
    onClose(wasSuccessful);
  };

  // ──── Step 1 Handlers ────

  const handleServiceAccountKeyChange = (e) => {
    const value = e.target.value;
    setServiceAccountKey(value);
    setValidationResult(null);

    if (!value.trim()) {
      setServiceAccountData(null);
      setValidationError((prev) => {
        const next = { ...prev };
        delete next.serviceAccountKey;
        return next;
      });
      return;
    }

    try {
      const parsed = JSON.parse(value);
      if (!parsed.type || parsed.type !== 'service_account') {
        setServiceAccountData(null);
        setValidationError({ ...validationError, serviceAccountKey: 'Invalid service account key. Must be a valid GCP service account JSON.' });
        return;
      }
      if (!parsed.project_id || !parsed.private_key || !parsed.client_email) {
        setServiceAccountData(null);
        setValidationError({
          ...validationError,
          serviceAccountKey: 'Service account key is missing required fields (project_id, private_key, client_email).',
        });
        return;
      }
      setServiceAccountData(parsed);
      setValidationError((prev) => {
        const next = { ...prev };
        delete next.serviceAccountKey;
        return next;
      });
    } catch {
      setServiceAccountData(null);
      setValidationError({ ...validationError, serviceAccountKey: 'Invalid JSON format. Please paste a valid service account key.' });
    }
  };

  const handleAccountNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setValidationError({
        ...validationError,
        gcpAccountName:
          'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore',
      });
    } else {
      setValidationError((prev) => {
        const next = { ...prev };
        delete next.gcpAccountName;
        return next;
      });
    }
    setAccountNameValue(value);
  };

  const handleValidateCredentials = async () => {
    if (!serviceAccountData) {
      return;
    }
    setIsValidating(true);
    setValidationResult(null);
    try {
      const payload = {
        cloud_provider: 'GCP',
        credentials_json: serviceAccountKey,
        project_id: serviceAccountData?.project_id,
      };
      const result = await apiAccount.validateCloudCredentials(payload);
      setValidationResult(result);
    } catch {
      setValidationResult({ success: false, errorMessage: 'Failed to validate credentials. Please try again.' });
    } finally {
      setIsValidating(false);
    }
  };

  const canProceedStep1 = accountNameValue && serviceAccountData && !validationError.serviceAccountKey && !validationError.gcpAccountName;

  // ──── Step 2 Handlers ────

  const handleDiscoverProjects = async () => {
    setIsDiscovering(true);
    setDiscoveryError('');
    try {
      const projects = await apiAccount.listGcpProjects(serviceAccountKey);
      setDiscoveredProjects(projects || []);
      if (projects?.length > 0) {
        setSelectedProjectIds(new Set(projects.map((p) => p.project_id)));
      } else {
        setDiscoveryError('No accessible projects found. The service account may only have access to its own project.');
      }
    } catch (err) {
      setDiscoveryError(err?.message || 'Failed to discover projects. You can enter project IDs manually instead.');
    } finally {
      setIsDiscovering(false);
    }
  };

  const toggleProject = (projectId) => {
    setSelectedProjectIds((prev) => {
      const next = new Set(prev);
      if (next.has(projectId)) {
        next.delete(projectId);
      } else {
        next.add(projectId);
      }
      return next;
    });
  };

  const toggleAllProjects = () => {
    if (selectedProjectIds.size === filteredProjects.length) {
      setSelectedProjectIds(new Set());
    } else {
      setSelectedProjectIds(new Set(filteredProjects.map((p) => p.project_id)));
    }
  };

  const filteredProjects = useMemo(() => {
    if (!projectSearchFilter) {
      return discoveredProjects;
    }
    const filter = projectSearchFilter.toLowerCase();
    return discoveredProjects.filter((p) => p.project_id.toLowerCase().includes(filter) || p.name.toLowerCase().includes(filter));
  }, [discoveredProjects, projectSearchFilter]);

  const getSelectedProjectIdList = () => {
    if (projectTab === 0) {
      return [...selectedProjectIds];
    }
    return manualProjectIds
      .split(',')
      .map((id) => id.trim())
      .filter(Boolean);
  };

  const canProceedStep2 = getSelectedProjectIdList().length > 0;

  // ──── Step 3 Handlers ────

  const handleValidateBilling = async () => {
    if (!billingDatasetName || !billingTableName) {
      return;
    }
    setIsValidatingBilling(true);
    setBillingValidationResult(null);
    try {
      const payload = {
        cloud_provider: 'GCP',
        credentials_json: serviceAccountKey,
        project_id: serviceAccountData?.project_id,
        billing_project_id: billingProjectId || undefined,
        billing_dataset_id: billingDatasetName,
        billing_table_id: billingTableName,
      };
      const result = await apiAccount.validateCloudCredentials(payload);
      const bqDetail = result?.permissionDetails?.find((d) => d.permission === 'BigQuery Billing Data');
      setBillingValidationResult(bqDetail || null);
    } catch {
      setBillingValidationResult({ hasAccess: false, errorDetail: 'Failed to validate billing access.' });
    } finally {
      setIsValidatingBilling(false);
    }
  };

  // ──── Submit (creates accounts + transitions to step 4) ────

  const handleBulkOnboard = async () => {
    const projectIds = getSelectedProjectIdList();
    if (projectIds.length === 0) {
      return;
    }

    setIsSubmitting(true);
    try {
      const payload = {
        account_name: accountNameValue,
        credentials_json: serviceAccountKey,
        project_ids: projectIds,
        billing_project_id: billingProjectId.trim(),
        billing_dataset_id: billingDatasetName.trim(),
        billing_table_id: billingTableName.trim(),
      };

      const response = await apiAccount.gcpBulkOnboard(payload);
      const errMsg = parseHttpResponseBodyMessage(response);
      if (errMsg) {
        snackbar.error(errMsg);
        return;
      }
      const result = response.data;
      setOnboardResults(result);

      const created = result?.accounts?.filter((a) => a.status === 'created') || [];
      const failed = result?.accounts?.filter((a) => a.status === 'error') || [];

      if (created.length > 0) {
        snackbar.success(`${created.length} GCP project(s) onboarded successfully.`);
      }
      if (failed.length > 0) {
        snackbar.error(`${failed.length} project(s) failed to onboard.`);
      }

      // Check if Cloud Monitoring permission is available from Step 1 validation
      const monitoringPerm = validationResult?.permissionDetails?.find((d) => d.permission === 'Cloud Monitoring (Alerts Webhook)');
      setHasMonitoringPermission(monitoringPerm?.hasAccess === true);

      // Auto-create the webhook integration config to get a real token
      const createdAccountIds = created.map((a) => a.account_id).filter(Boolean);
      if (createdAccountIds.length > 0) {
        try {
          // Name the integration by the (immutable) parent account ID rather
          // than the user-typed name. Keeps detection stable across account
          // renames; legacy rows still use the name pattern and are recovered
          // by orphan-recovery logic in CloudAccountTile.
          const integrationPayload = {
            integration_name: 'gcp_monitoring_webhook',
            integration_config_name: `GCP Monitoring - ${createdAccountIds[0]}`,
            account_ids: createdAccountIds,
            integration_config_values: [],
          };
          const integrationResp = await apiIntegrations.addIntegrations(integrationPayload);
          const respErrors = integrationResp?.data?.errors;
          if (respErrors?.length) {
            const msg = respErrors[0]?.message || 'Failed to create webhook integration config.';
            console.error('Failed to create webhook integration config:', msg);
            snackbar.error(`Real-time alerts setup failed: ${msg}. You can re-run setup from Manage Real-Time Alerts.`);
          } else {
            const integrationData = integrationResp?.data?.data?.integrations_create_config;
            const tokenConfig = integrationData?.configs?.find((c) => c.name === 'token');
            if (tokenConfig?.value) {
              const baseUrl = window.location.origin;
              setWebhookUrl(`${baseUrl}/api/webhooks/gcp-monitoring?token=${tokenConfig.value}`);
            } else {
              snackbar.error('Real-time alerts setup did not return a webhook token. You can re-run setup from Manage Real-Time Alerts.');
            }
          }
        } catch (integrationErr) {
          console.error('Failed to create webhook integration config:', integrationErr);
          snackbar.error(
            `Real-time alerts setup failed: ${integrationErr?.message || 'unknown error'}. You can re-run setup from Manage Real-Time Alerts.`
          );
        }
      }

      // TODO: Re-enable Pub/Sub onboarding after testing
      // Fetch Pub/Sub deployment URL for parent account
      // if (result?.parent_id) {
      //   setIsFetchingDeployUrl(true);
      //   try {
      //     const deployRes = await apiKubernetes1.getGcpDeploymentManagerURL(result.parent_id);
      //     const deployData = deployRes?.data?.data?.gcp_pubsub_onboard;
      //     if (deployData?.deployment_manager_url) {
      //       setDeploymentManagerUrl(deployData.deployment_manager_url);
      //       setExternalId(deployData.external_id);
      //       setPubsubProjectId(deployData.pubsub_project_id || '');
      //       setSubscriptionName(deployData.subscription_name || '');
      //     }
      //   } catch (error) {
      //     console.error('Failed to fetch GCP Deployment Manager URL:', error);
      //   } finally {
      //     setIsFetchingDeployUrl(false);
      //   }
      // }

      handleCloseModal(true);
    } catch (error) {
      snackbar.error('Failed to onboard GCP projects.');
      console.error('Failed to onboard GCP projects:', error);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleCopyToClipboard = (value, field) => {
    navigator.clipboard.writeText(value);
    setCopied((prev) => ({ ...prev, [field]: true }));
    snackbar.success('Copied to clipboard.');
    setTimeout(() => setCopied((prev) => ({ ...prev, [field]: false })), 3000);
  };

  // ──── Render ────

  return (
    <Modal width='md' open={open} handleClose={isLoading ? () => {} : () => handleCloseModal(step === 3)} title='Add GCP Account' loader={isLoading}>
      <Stepper activeStep={step} alternativeLabel connector={<StepConnectorStyled />} sx={{ mb: 3, mt: 2 }}>
        {STEPS.map((label, idx) => (
          <Step key={label} completed={step > idx}>
            <StepLabel
              StepIconComponent={StepIconCustom}
              sx={{
                '& .MuiStepLabel-label.MuiStepLabel-alternativeLabel': {
                  fontSize: '13px',
                  marginTop: '10px',
                  color: step === idx ? '#374151' : 'inherit',
                  fontWeight: step === idx ? 500 : 'normal',
                },
              }}
            >
              {label}
            </StepLabel>
          </Step>
        ))}
      </Stepper>

      {/* ──── Step 1: Service Account ──── */}
      {step === 0 && (
        <>
          <Box sx={{ mb: 1 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', cursor: 'pointer', gap: 0.5, py: 1 }} onClick={() => setGuideExpanded(!guideExpanded)}>
              <HelpOutline sx={{ fontSize: 18, color: '#6B7280' }} />
              <Typography sx={{ fontSize: 13, color: '#6B7280', fontWeight: 500 }}>Setup Guide — How to create a GCP service account</Typography>
              {guideExpanded ? <ExpandLess sx={{ fontSize: 18, color: '#6B7280' }} /> : <ExpandMore sx={{ fontSize: 18, color: '#6B7280' }} />}
            </Box>
            <Collapse in={guideExpanded}>
              <Box sx={{ mt: 1, p: 2, bgcolor: '#f8f9fa', borderRadius: '8px', border: '1px solid #e0e0e0' }}>
                <MarkDowns data={SETUP_GUIDE_CONTENT} sx={{ maxHeight: '300px', overflowY: 'auto', padding: '0px', borderRadius: '0px' }} />
              </Box>
            </Collapse>
          </Box>

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
              onChange={(e) => handleAccountNameChange(e.target.value)}
              error={!!validationError.gcpAccountName}
              helperText={validationError.gcpAccountName}
            />
            <TextField
              sx={inputSx}
              value={serviceAccountKey}
              margin='normal'
              fullWidth
              id='service-account-key'
              label='Service Account Key (JSON)'
              required
              multiline
              rows={8}
              onChange={handleServiceAccountKeyChange}
              error={!!validationError.serviceAccountKey}
              helperText={validationError.serviceAccountKey}
              placeholder='Paste the entire service account JSON key here'
            />
          </Grid>

          <ValidationResultBanner result={validationResult} />

          <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            <Grid item>
              <CustomButton id='cancel-btn' size='Medium' text='Cancel' variant='secondary' onClick={() => handleCloseModal(false)} />
            </Grid>
            <Grid item>
              <CustomButton
                id='check-permissions-btn'
                size='Medium'
                text='Check Permissions'
                variant='secondary'
                disabled={!serviceAccountData || isValidating}
                loading={isValidating}
                onClick={handleValidateCredentials}
              />
            </Grid>
            <Grid item>
              <CustomButton size='Medium' id='next-step1' text='Next' disabled={!canProceedStep1} onClick={() => setStep(1)} />
            </Grid>
          </Grid>
        </>
      )}

      {/* ──── Step 2: Projects ──── */}
      {step === 1 && (
        <>
          <Typography sx={{ fontSize: 14, color: '#374151', mb: 2 }}>
            Select which GCP projects to monitor. You can auto-discover accessible projects or enter project IDs manually.
          </Typography>

          <Tabs value={projectTab} onChange={(_, v) => setProjectTab(v)} sx={{ mb: 2, minHeight: 36, '& .MuiTab-root': { minHeight: 36, py: 0.5 } }}>
            <Tab label='Auto-Discover' />
            <Tab label='Manual Entry' />
          </Tabs>

          {projectTab === 0 && (
            <>
              <Box sx={{ display: 'flex', gap: 1, mb: 2, alignItems: 'center' }}>
                <CustomButton
                  id='discover-projects-btn'
                  size='Medium'
                  text='Discover Projects'
                  loading={isDiscovering}
                  disabled={isDiscovering}
                  onClick={handleDiscoverProjects}
                />
                {discoveredProjects.length > 0 && (
                  <Typography sx={{ fontSize: 13, color: '#6B7280' }}>{discoveredProjects.length} project(s) found</Typography>
                )}
              </Box>

              {discoveryError && (
                <Alert severity='warning' sx={{ mb: 2, '& .MuiAlert-message': { fontSize: '13px' } }}>
                  {discoveryError}
                </Alert>
              )}

              {discoveredProjects.length > 0 && (
                <>
                  <TextField
                    size='small'
                    fullWidth
                    placeholder='Search projects...'
                    value={projectSearchFilter}
                    onChange={(e) => setProjectSearchFilter(e.target.value)}
                    sx={{ ...inputSx, mb: 1 }}
                    InputProps={{
                      startAdornment: (
                        <InputAdornment position='start'>
                          <Search sx={{ fontSize: 18, color: '#9CA3AF' }} />
                        </InputAdornment>
                      ),
                    }}
                  />
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.5 }}>
                    <Checkbox
                      size='small'
                      checked={selectedProjectIds.size === filteredProjects.length && filteredProjects.length > 0}
                      indeterminate={selectedProjectIds.size > 0 && selectedProjectIds.size < filteredProjects.length}
                      onChange={toggleAllProjects}
                    />
                    <Typography sx={{ fontSize: 13, fontWeight: 500 }}>
                      Select All ({selectedProjectIds.size}/{filteredProjects.length})
                    </Typography>
                  </Box>
                  <Box sx={{ maxHeight: 250, overflowY: 'auto', border: '1px solid #E5E7EB', borderRadius: '6px' }}>
                    {filteredProjects.map((project) => (
                      <Box
                        key={project.project_id}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          px: 1,
                          py: 0.5,
                          borderBottom: '1px solid #F3F4F6',
                          '&:hover': { bgcolor: '#F9FAFB' },
                          cursor: 'pointer',
                        }}
                        onClick={() => toggleProject(project.project_id)}
                      >
                        <Checkbox size='small' checked={selectedProjectIds.has(project.project_id)} />
                        <Box sx={{ ml: 0.5 }}>
                          <Typography sx={{ fontSize: 13, fontWeight: 500 }}>{project.project_id}</Typography>
                          {project.name && project.name !== project.project_id && (
                            <Typography sx={{ fontSize: 12, color: '#6B7280' }}>{project.name}</Typography>
                          )}
                        </Box>
                      </Box>
                    ))}
                  </Box>
                </>
              )}
            </>
          )}

          {projectTab === 1 && (
            <>
              <TextField
                sx={inputSx}
                value={manualProjectIds}
                margin='normal'
                fullWidth
                id='manual-project-ids'
                label='Project IDs (comma-separated)'
                multiline
                rows={4}
                onChange={(e) => setManualProjectIds(e.target.value)}
                placeholder='e.g., my-project-1, my-project-2, my-project-3'
                helperText='Enter one or more GCP project IDs separated by commas.'
              />
              {manualProjectIds && (
                <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, mt: 1 }}>
                  {manualProjectIds
                    .split(',')
                    .map((id) => id.trim())
                    .filter(Boolean)
                    .map((id) => (
                      <Chip key={id} label={id} size='small' variant='outlined' />
                    ))}
                </Box>
              )}
            </>
          )}

          <Grid container spacing={2} mt={2} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            <Grid item>
              <CustomButton id='back-step2' size='Medium' text='Back' variant='secondary' onClick={() => setStep(0)} />
            </Grid>
            <Grid item>
              <CustomButton size='Medium' id='next-step2' text='Next' disabled={!canProceedStep2} onClick={() => setStep(2)} />
            </Grid>
          </Grid>
        </>
      )}

      {/* ──── Step 3: Billing ──── */}
      {step === 2 && (
        <>
          <Alert severity='info' icon={<InfoOutlined sx={{ fontSize: 18 }} />} sx={{ mb: 2, '& .MuiAlert-message': { fontSize: '13px' } }}>
            Billing data is typically exported to a central BigQuery table. This may be in a different project than your resource projects. Configure
            this in GCP Console under Billing &rarr; Billing export.
          </Alert>

          <Grid container>
            <TextField
              sx={inputSx}
              value={billingProjectId}
              size='small'
              margin='normal'
              fullWidth
              id='billing-project-id'
              label='Billing Project ID'
              onChange={(e) => {
                setBillingProjectId(e.target.value);
                setBillingValidationResult(null);
              }}
              placeholder='e.g., my-billing-project (leave empty to use service account project)'
              helperText='The GCP project containing the BigQuery billing export. Leave empty if same as service account project.'
            />
            <TextField
              sx={inputSx}
              value={billingDatasetName}
              size='small'
              margin='normal'
              fullWidth
              required
              id='billing-dataset-name'
              label='BigQuery Dataset Name'
              onChange={(e) => {
                setBillingDatasetName(e.target.value);
                setBillingValidationResult(null);
              }}
              placeholder='e.g., billing_export'
            />
            <TextField
              sx={inputSx}
              value={billingTableName}
              size='small'
              margin='normal'
              fullWidth
              required
              id='billing-table-name'
              label='BigQuery Table Name'
              onChange={(e) => {
                setBillingTableName(e.target.value);
                setBillingValidationResult(null);
              }}
              placeholder='e.g., gcp_billing_export_v1_XXXXX'
            />
          </Grid>

          {billingValidationResult && (
            <Alert
              severity={billingValidationResult.hasAccess ? 'success' : 'error'}
              icon={billingValidationResult.hasAccess ? <CheckCircleOutline sx={{ fontSize: 18 }} /> : <ErrorOutline sx={{ fontSize: 18 }} />}
              sx={{ mt: 1, mb: 1, '& .MuiAlert-message': { fontSize: '13px' } }}
            >
              {billingValidationResult.hasAccess
                ? 'BigQuery billing table accessible.'
                : billingValidationResult.errorDetail || 'Unable to access BigQuery billing data.'}
            </Alert>
          )}

          <Grid container spacing={2} mt={2} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            <Grid item>
              <CustomButton id='back-step3' size='Medium' text='Back' variant='secondary' onClick={() => setStep(1)} />
            </Grid>
            <Grid item>
              <CustomButton
                id='validate-billing-btn'
                size='Medium'
                text='Validate Billing'
                variant='secondary'
                disabled={!billingDatasetName || !billingTableName || isValidatingBilling}
                loading={isValidatingBilling}
                onClick={handleValidateBilling}
              />
            </Grid>
            <Grid item>
              <CustomButton
                size='Medium'
                id='save-and-continue-btn'
                text='Save & Continue'
                disabled={isSubmitting || !billingDatasetName.trim() || !billingTableName.trim()}
                loading={isSubmitting}
                onClick={handleBulkOnboard}
              />
            </Grid>
          </Grid>
        </>
      )}

      {/* ──── Step 4: Real-Time Alerts ──── */}
      {step === 3 && (
        <>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1.5,
              mb: 1,
              px: 2,
              py: 1.5,
              backgroundColor: '#F0FDF4',
              borderRadius: '8px',
              border: '1px solid #BBF7D0',
            }}
          >
            <CheckCircleOutline sx={{ color: '#16A34A', fontSize: 22 }} />
            <Typography sx={{ fontSize: '14px', fontWeight: 500, color: '#15803D' }}>
              {onboardResults?.accounts?.filter((a) => a.status === 'created').length || 0} GCP project(s) onboarded successfully.
            </Typography>
          </Box>

          {onboardResults?.accounts?.some((a) => a.status === 'error') && (
            <Alert severity='warning' sx={{ mt: 1, mb: 1, '& .MuiAlert-message': { fontSize: '13px' } }}>
              <Typography variant='body2' sx={{ fontWeight: 500, mb: 0.5 }}>
                Some projects failed:
              </Typography>
              {onboardResults.accounts
                .filter((a) => a.status === 'error')
                .map((a) => (
                  <Typography key={a.project_id} variant='caption' display='block'>
                    {a.project_id}: {a.error}
                  </Typography>
                ))}
            </Alert>
          )}

          {webhookSetupResult?.succeeded?.length > 0 && webhookSetupResult?.failed?.length === 0 && (
            <Alert
              severity='success'
              icon={<CheckCircleOutline sx={{ fontSize: 18 }} />}
              sx={{ mt: 1, mb: 2, '& .MuiAlert-message': { fontSize: '13px' } }}
            >
              Real-time alerts enabled for {webhookSetupResult.succeeded.length} project(s). Webhook notification channels have been created.
            </Alert>
          )}

          {webhookSetupResult?.failed?.length > 0 && (
            <Alert severity='warning' sx={{ mt: 1, mb: 2, '& .MuiAlert-message': { fontSize: '13px' } }}>
              {webhookSetupResult?.succeeded?.length > 0 ? `Alerts enabled for ${webhookSetupResult.succeeded.length} project(s). ` : ''}
              Failed for {webhookSetupResult.failed.length}: {webhookSetupResult.failed.join(', ')}. You can set them up manually below or enable
              later from the account menu.
            </Alert>
          )}

          {!webhookSetupResult && (
            <>
              {hasMonitoringPermission && (
                <Box sx={{ mt: 2, mb: 2 }}>
                  <Typography sx={{ fontSize: 14, fontWeight: 500, mb: 1 }}>Auto-Setup (Recommended)</Typography>
                  <Typography sx={{ fontSize: 13, color: '#6B7280', mb: 1.5 }}>
                    Your service account has the required Cloud Monitoring permissions. Click below to automatically create a webhook notification
                    channel and attach it to all alert policies.
                  </Typography>
                  <CustomButton
                    id='auto-setup-webhook-btn'
                    size='Medium'
                    text='Enable Real-Time Alerts'
                    loading={isSettingUpWebhook}
                    disabled={isSettingUpWebhook || !webhookUrl}
                    onClick={async () => {
                      const created = onboardResults?.accounts?.filter((a) => a.status === 'created') || [];
                      if (created.length === 0) {
                        return;
                      }
                      setIsSettingUpWebhook(true);
                      const succeeded = [];
                      const failed = [];
                      for (const account of created) {
                        try {
                          await apiAccount.setupGcpMonitoringWebhook(account.account_id, webhookUrl);
                          succeeded.push(account.project_id);
                        } catch (err) {
                          failed.push(account.project_id);
                          console.error(`Webhook setup failed for ${account.project_id}:`, err);
                        }
                      }
                      setWebhookSetupResult({ succeeded, failed });
                      if (succeeded.length > 0 && failed.length === 0) {
                        snackbar.success(`Real-time alerts enabled for all ${succeeded.length} project(s).`);
                      } else if (succeeded.length > 0) {
                        snackbar.warning(`Alerts enabled for ${succeeded.length} project(s), failed for ${failed.length}.`);
                      } else {
                        snackbar.error('Failed to enable alerts for all projects. You can configure them manually below.');
                      }
                      setIsSettingUpWebhook(false);
                    }}
                  />
                </Box>
              )}

              {!webhookUrl && (
                <Alert
                  severity='info'
                  icon={<InfoOutlined sx={{ fontSize: 18 }} />}
                  sx={{ mt: 1, mb: 2, '& .MuiAlert-message': { fontSize: '13px' } }}
                >
                  Real-time alerts are not configured. Your account will use polling mode to detect alert changes. You can enable real-time alerts
                  later from the account settings.
                </Alert>
              )}
            </>
          )}

          {webhookUrl && (!webhookSetupResult || webhookSetupResult?.failed?.length > 0) && (
            <Box sx={{ mt: 1 }}>
              {webhookSetupResult?.failed?.length > 0 && (
                <Typography sx={{ fontSize: 14, fontWeight: 500, mb: 1 }}>Manual Setup for Failed Projects</Typography>
              )}
              {!webhookSetupResult && hasMonitoringPermission && (
                <Typography sx={{ fontSize: 14, fontWeight: 500, mb: 1, pt: 1, borderTop: '1px solid #E5E7EB' }}>Manual Setup</Typography>
              )}
              <MarkDowns data={WEBHOOK_MANUAL_INSTRUCTIONS} sx={{ width: 'auto' }} />

              <Grid container mt={1} mb={2} spacing={2}>
                <Grid item xs={12}>
                  <TextField
                    sx={inputSx}
                    value={webhookUrl}
                    size='small'
                    fullWidth
                    id='webhook-url'
                    label='Webhook URL'
                    InputProps={{
                      readOnly: true,
                      endAdornment: (
                        <InputAdornment position='end'>
                          <IconButton aria-label='copy webhook url' onClick={() => handleCopyToClipboard(webhookUrl, 'webhookUrl')} edge='end'>
                            <ContentCopy fontSize='small' />
                          </IconButton>
                        </InputAdornment>
                      ),
                    }}
                    helperText={copied.webhookUrl ? 'Copied!' : 'Copy this URL and create a webhook notification channel in GCP Console.'}
                  />
                </Grid>
              </Grid>
            </Box>
          )}

          <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            <Grid item>
              <CustomButton
                id='close-webhook-btn'
                size='Medium'
                text={webhookSetupResult?.succeeded?.length > 0 ? 'Close' : 'Skip for now'}
                onClick={() => handleCloseModal(true)}
              />
            </Grid>
          </Grid>
        </>
      )}
    </Modal>
  );
};

export default AddGcpAccountModal;
