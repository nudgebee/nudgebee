import {
  Grid,
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
  FormControlLabel,
  Chip,
} from '@mui/material';
import { Input } from '@components1/ds/Input';
import {
  Visibility,
  VisibilityOff,
  Check,
  HelpOutline,
  ExpandMore,
  ExpandLess,
  InfoOutlined,
  SearchOutlined,
  CheckCircleOutline,
  ErrorOutline,
} from '@mui/icons-material';
import { useState } from 'react';
import apiAccount from '@api1/account';
import { Modal } from '@components1/common/modal';
import { isK8sAccountNameValid, parseHttpResponseBodyMessage } from 'src/utils/common';
import { Button } from '@components1/ds/Button';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';

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

Your Azure service principal needs **Reader** role access to the subscriptions you want to monitor.

### Option 1: Azure CLI

**Step 1.** Create a service principal:

\`\`\`bash
az ad sp create-for-rbac -n "nudgebee"
\`\`\`

This outputs \`appId\` (Client ID), \`password\` (Client Secret), and \`tenant\` (Tenant ID).

**Step 2.** Assign **Reader** role to a subscription (replace \`APP_ID\` and \`SUBSCRIPTION_ID\`):

\`\`\`bash
az role assignment create --assignee APP_ID --role Reader --scope /subscriptions/SUBSCRIPTION_ID
\`\`\`

Repeat Step 2 for each subscription you want to monitor.

### Option 2: Azure Portal

1. Go to **Microsoft Entra ID** → **App registrations** → **New registration**
2. Name it "nudgebee" and register
3. Go to **Certificates & secrets** → **New client secret** — copy the secret value
4. Go to the target **Subscription** → **Access control (IAM)** → **Add role assignment**
5. Assign the **Reader** role to your new app registration

[Open Azure App Registrations](https://portal.azure.com/#blade/Microsoft_AAD_IAM/ActiveDirectoryMenuBlade/RegisteredApps)
`;

const STEP_LABELS = ['Credentials', 'Select Subscriptions', 'Review & Onboard'];

const AddAzureAccountModal = ({ open, onClose }) => {
  // Step 0: Credentials
  const [accountNameValue, setAccountNameValue] = useState('');
  const [tenantId, setTenantId] = useState('');
  const [clientId, setClientId] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [showSecret, setShowSecret] = useState(false);
  const [validationError, setValidationError] = useState({});
  const [guideExpanded, setGuideExpanded] = useState(false);

  // Step 1: Subscriptions
  const [step, setStep] = useState(0);
  const [isDiscovering, setIsDiscovering] = useState(false);
  const [discoveredSubscriptions, setDiscoveredSubscriptions] = useState([]);
  const [selectedSubscriptionIds, setSelectedSubscriptionIds] = useState(new Set());
  const [subscriptionSearchFilter, setSubscriptionSearchFilter] = useState('');
  const [discoveryError, setDiscoveryError] = useState('');

  // Step 2: Onboard results
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [onboardResults, setOnboardResults] = useState(null);

  const clearForm = () => {
    setAccountNameValue('');
    setTenantId('');
    setClientId('');
    setClientSecret('');
    setShowSecret(false);
    setValidationError({});
    setGuideExpanded(false);
    setStep(0);
    setIsDiscovering(false);
    setDiscoveredSubscriptions([]);
    setSelectedSubscriptionIds(new Set());
    setSubscriptionSearchFilter('');
    setDiscoveryError('');
    setIsSubmitting(false);
    setOnboardResults(null);
  };

  const handleCloseModal = (wasSuccessful = false) => {
    clearForm();
    onClose(wasSuccessful);
  };

  const validateField = (name, value) => {
    let errorMsg = '';
    if (!value) {
      errorMsg = 'This field is required';
    } else if (name === 'accountName' && !isK8sAccountNameValid(value)) {
      errorMsg =
        'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore';
    }

    setValidationError((prevState) => {
      const newState = { ...prevState };
      if (errorMsg) {
        newState[name] = errorMsg;
      } else {
        delete newState[name];
      }
      return newState;
    });
  };

  const handleChange = (name, value) => {
    setValidationError({});
    if (name === 'accountName') {
      setAccountNameValue(value);
    } else if (name === 'tenantId') {
      setTenantId(value);
    } else if (name === 'clientId') {
      setClientId(value);
    } else if (name === 'clientSecret') {
      setClientSecret(value);
    }
    validateField(name, value);
  };

  const handleNextToSubscriptions = () => {
    const errors = {};
    if (!accountNameValue) {
      errors.accountName = 'This field is required';
    } else if (!isK8sAccountNameValid(accountNameValue)) {
      errors.accountName =
        'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore';
    }
    if (!tenantId) {
      errors.tenantId = 'This field is required';
    }
    if (!clientId) {
      errors.clientId = 'This field is required';
    }
    if (!clientSecret) {
      errors.clientSecret = 'This field is required';
    }

    setValidationError(errors);
    if (Object.keys(errors).length > 0) {
      snackbar.error('Please fill out all required fields correctly.');
      return;
    }

    setStep(1);
  };

  const handleDiscoverSubscriptions = async () => {
    setIsDiscovering(true);
    setDiscoveryError('');
    setDiscoveredSubscriptions([]);
    setSelectedSubscriptionIds(new Set());

    try {
      const subscriptions = await apiAccount.listAzureSubscriptions(tenantId, clientId, clientSecret);
      if (!subscriptions || subscriptions.length === 0) {
        setDiscoveryError('No subscriptions found. Ensure the service principal has Reader role on at least one subscription.');
        return;
      }
      setDiscoveredSubscriptions(subscriptions);
      // Select all by default
      setSelectedSubscriptionIds(new Set(subscriptions.map((s) => s.subscription_id)));
    } catch (err) {
      const errMsg = err?.response?.data?.message || err?.message || 'Failed to discover subscriptions';
      setDiscoveryError(errMsg);
    } finally {
      setIsDiscovering(false);
    }
  };

  const handleToggleSubscription = (subId) => {
    setSelectedSubscriptionIds((prev) => {
      const next = new Set(prev);
      if (next.has(subId)) {
        next.delete(subId);
      } else {
        next.add(subId);
      }
      return next;
    });
  };

  const handleSelectAll = () => {
    const filtered = getFilteredSubscriptions();
    const allSelected = filtered.every((s) => selectedSubscriptionIds.has(s.subscription_id));
    if (allSelected) {
      // Deselect all filtered
      setSelectedSubscriptionIds((prev) => {
        const next = new Set(prev);
        filtered.forEach((s) => next.delete(s.subscription_id));
        return next;
      });
    } else {
      // Select all filtered
      setSelectedSubscriptionIds((prev) => {
        const next = new Set(prev);
        filtered.forEach((s) => next.add(s.subscription_id));
        return next;
      });
    }
  };

  const getFilteredSubscriptions = () => {
    if (!subscriptionSearchFilter) {
      return discoveredSubscriptions;
    }
    const filter = subscriptionSearchFilter.toLowerCase();
    return discoveredSubscriptions.filter((s) => s.display_name.toLowerCase().includes(filter) || s.subscription_id.toLowerCase().includes(filter));
  };

  const handleNextToReview = () => {
    if (selectedSubscriptionIds.size === 0) {
      snackbar.error('Please select at least one subscription.');
      return;
    }
    setStep(2);
  };

  const handleBulkOnboard = async () => {
    setIsSubmitting(true);
    setOnboardResults(null);

    const selectedSubs = discoveredSubscriptions
      .filter((s) => selectedSubscriptionIds.has(s.subscription_id))
      .map((s) => ({
        subscription_id: s.subscription_id,
        display_name: s.display_name,
      }));

    try {
      const response = await apiAccount.azureBulkOnboard({
        account_name: accountNameValue,
        tenant_id: tenantId,
        client_id: clientId,
        client_secret: clientSecret,
        subscriptions: selectedSubs,
      });
      const errMsg = parseHttpResponseBodyMessage(response) || response?.error;
      if (errMsg) {
        snackbar.error(errMsg);
        return;
      }
      const result = response.data;
      if (result) {
        setOnboardResults(result);
        const successCount = result.accounts?.filter((a) => a.status === 'created').length || 0;
        const errorCount = result.accounts?.filter((a) => a.status === 'error').length || 0;
        if (errorCount === 0) {
          snackbar.success(`Successfully onboarded ${successCount} subscription${successCount > 1 ? 's' : ''}.`);
        } else {
          snackbar.warning(`Onboarded ${successCount} subscription${successCount > 1 ? 's' : ''}, ${errorCount} failed.`);
        }
      }
    } catch (err) {
      const errMsg = err?.response?.data?.message || err?.message || 'Failed to onboard subscriptions';
      snackbar.error(errMsg);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleClickShowSecret = () => setShowSecret((show) => !show);
  const handleMouseDownPassword = (event) => {
    event.preventDefault();
  };

  const filteredSubscriptions = getFilteredSubscriptions();
  const allFilteredSelected = filteredSubscriptions.length > 0 && filteredSubscriptions.every((s) => selectedSubscriptionIds.has(s.subscription_id));
  const isLoading = isSubmitting || isDiscovering;

  return (
    <Modal
      width='md'
      open={open}
      handleClose={isLoading ? () => {} : () => handleCloseModal(onboardResults !== null)}
      title='Add Azure Account'
      loader={isLoading}
    >
      <Stepper activeStep={step} alternativeLabel connector={<StepConnectorStyled />} sx={{ mb: 3, mt: 2 }}>
        {STEP_LABELS.map((label, index) => (
          <Step key={label} completed={step > index}>
            <StepLabel
              StepIconComponent={StepIconCustom}
              sx={{
                '& .MuiStepLabel-label.MuiStepLabel-alternativeLabel': {
                  fontSize: '14px',
                  marginTop: '10px',
                  color: step === index ? '#374151' : 'inherit',
                  fontWeight: step === index ? 500 : 'normal',
                },
              }}
            >
              {label}
            </StepLabel>
          </Step>
        ))}
      </Stepper>

      {/* Step 0: Credentials */}
      {step === 0 && (
        <>
          {/* Collapsible Setup Guide */}
          <Box sx={{ mb: 1 }}>
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                cursor: 'pointer',
                gap: 0.5,
                py: 1,
              }}
              onClick={() => setGuideExpanded(!guideExpanded)}
            >
              <HelpOutline sx={{ fontSize: 18, color: '#6B7280' }} />
              <Typography sx={{ fontSize: 13, color: '#6B7280', fontWeight: 500 }}>Setup Guide — How to create an Azure service principal</Typography>
              {guideExpanded ? <ExpandLess sx={{ fontSize: 18, color: '#6B7280' }} /> : <ExpandMore sx={{ fontSize: 18, color: '#6B7280' }} />}
            </Box>
            <Collapse in={guideExpanded}>
              <Box
                sx={{
                  mt: 1,
                  p: 2,
                  bgcolor: '#f8f9fa',
                  borderRadius: '8px',
                  border: '1px solid #e0e0e0',
                }}
              >
                <MarkDowns
                  data={SETUP_GUIDE_CONTENT}
                  sx={{
                    maxHeight: '300px',
                    overflowY: 'auto',
                    padding: '0px',
                    borderRadius: '0px',
                  }}
                />
              </Box>
            </Collapse>
          </Box>

          <Grid container>
            <Box sx={{ mt: 2, width: '100%' }}>
              <Input
                value={accountNameValue}
                size='sm'
                id='account-name'
                label='Display Name'
                required
                onChange={(value) => handleChange('accountName', value)}
                onBlur={(e) => validateField('accountName', e.target.value)}
                error={validationError.accountName || undefined}
              />
            </Box>
            <Box sx={{ mt: 2, width: '100%' }}>
              <Input
                value={tenantId}
                size='sm'
                id='tenant-id'
                label='Directory (tenant) ID'
                required
                onChange={(value) => handleChange('tenantId', value)}
                onBlur={(e) => validateField('tenantId', e.target.value)}
                error={validationError.tenantId || undefined}
                help={validationError.tenantId ? undefined : 'Found in Azure Portal > Microsoft Entra ID > Overview'}
              />
            </Box>
            <Box sx={{ mt: 2, width: '100%' }}>
              <Input
                value={clientId}
                size='sm'
                id='client-id'
                label='Application (client) ID'
                required
                onChange={(value) => handleChange('clientId', value)}
                onBlur={(e) => validateField('clientId', e.target.value)}
                error={validationError.clientId || undefined}
                help={validationError.clientId ? undefined : 'Found in Azure Portal > App Registrations > Your App > Overview'}
              />
            </Box>
            <Box sx={{ mt: 2, width: '100%', display: 'flex', alignItems: 'flex-start', gap: 1 }}>
              <Box sx={{ flex: 1 }}>
                <Input
                  value={clientSecret}
                  size='sm'
                  id='client-secret'
                  label='Client Secret'
                  required
                  type={showSecret ? 'text' : 'password'}
                  onChange={(value) => handleChange('clientSecret', value)}
                  onBlur={(e) => validateField('clientSecret', e.target.value)}
                  error={validationError.clientSecret || undefined}
                  help={validationError.clientSecret ? undefined : 'Found in App Registrations > Certificates & secrets'}
                />
              </Box>
              <IconButton
                aria-label='toggle password visibility'
                onClick={handleClickShowSecret}
                onMouseDown={handleMouseDownPassword}
                sx={{ mt: '22px' }}
              >
                {showSecret ? <VisibilityOff /> : <Visibility />}
              </IconButton>
            </Box>
          </Grid>

          <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            <Grid item>
              <Button id='cancel-btn' size='md' tone='secondary' onClick={() => handleCloseModal(false)}>
                Cancel
              </Button>
            </Grid>
            <Grid item>
              <Button size='md' id='next-to-subscriptions' tone='primary' onClick={handleNextToSubscriptions}>
                Next
              </Button>
            </Grid>
          </Grid>
        </>
      )}

      {/* Step 1: Select Subscriptions */}
      {step === 1 && (
        <>
          {discoveredSubscriptions.length === 0 && !discoveryError && (
            <Box sx={{ textAlign: 'center', py: 4 }}>
              <Typography sx={{ fontSize: 14, color: '#6B7280', mb: 2 }}>Discover subscriptions accessible by your service principal.</Typography>
              <Button
                size='md'
                id='discover-subscriptions'
                tone='primary'
                loading={isDiscovering}
                onClick={handleDiscoverSubscriptions}
                disabled={isDiscovering}
              >
                Discover Subscriptions
              </Button>
            </Box>
          )}

          {discoveryError && (
            <Box sx={{ py: 2 }}>
              <Alert severity='error' sx={{ mb: 2 }}>
                {discoveryError}
              </Alert>
              <Box sx={{ textAlign: 'center' }}>
                <Button size='md' id='retry-discover' tone='secondary' onClick={handleDiscoverSubscriptions} disabled={isDiscovering}>
                  Retry
                </Button>
              </Box>
            </Box>
          )}

          {discoveredSubscriptions.length > 0 && (
            <>
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
                <Typography sx={{ fontSize: 14, fontWeight: 500 }}>
                  {discoveredSubscriptions.length} subscription{discoveredSubscriptions.length > 1 ? 's' : ''} found
                  <Chip label={`${selectedSubscriptionIds.size} selected`} size='small' sx={{ ml: 1 }} color='primary' variant='outlined' />
                </Typography>
              </Box>

              <Box sx={{ mb: 1 }}>
                <Input
                  size='sm'
                  placeholder='Search subscriptions...'
                  value={subscriptionSearchFilter}
                  onChange={setSubscriptionSearchFilter}
                  leadingIcon={<SearchOutlined sx={{ fontSize: 18, color: '#9CA3AF' }} />}
                />
              </Box>

              <Box sx={{ display: 'flex', alignItems: 'center', mb: 0.5 }}>
                <FormControlLabel
                  control={<Checkbox checked={allFilteredSelected} onChange={handleSelectAll} size='small' />}
                  label={<Typography sx={{ fontSize: 13 }}>{allFilteredSelected ? 'Deselect all' : 'Select all'}</Typography>}
                />
              </Box>

              <Box
                sx={{
                  maxHeight: '280px',
                  overflowY: 'auto',
                  border: '1px solid #E5E7EB',
                  borderRadius: '8px',
                }}
              >
                {filteredSubscriptions.map((sub) => (
                  <Box
                    key={sub.subscription_id}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      px: 1.5,
                      py: 0.5,
                      borderBottom: '1px solid #F3F4F6',
                      '&:last-child': { borderBottom: 'none' },
                      '&:hover': { bgcolor: '#F9FAFB' },
                    }}
                  >
                    <Checkbox
                      checked={selectedSubscriptionIds.has(sub.subscription_id)}
                      onChange={() => handleToggleSubscription(sub.subscription_id)}
                      size='small'
                    />
                    <Box sx={{ flex: 1, ml: 0.5 }}>
                      <Typography sx={{ fontSize: 13, fontWeight: 500 }}>{sub.display_name}</Typography>
                      <Typography sx={{ fontSize: 11, color: '#9CA3AF' }}>{sub.subscription_id}</Typography>
                    </Box>
                  </Box>
                ))}
              </Box>
            </>
          )}

          <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            <Grid item>
              <Button id='back-to-credentials' size='md' tone='secondary' onClick={() => setStep(0)}>
                Back
              </Button>
            </Grid>
            <Grid item>
              <Button size='md' id='next-to-review' tone='primary' onClick={handleNextToReview} disabled={selectedSubscriptionIds.size === 0}>
                Next
              </Button>
            </Grid>
          </Grid>
        </>
      )}

      {/* Step 2: Review & Onboard */}
      {step === 2 && (
        <>
          {!onboardResults && (
            <>
              <Alert severity='info' icon={<InfoOutlined sx={{ fontSize: 16 }} />} sx={{ mb: 2 }}>
                {selectedSubscriptionIds.size} subscription{selectedSubscriptionIds.size > 1 ? 's' : ''} will be onboarded under &quot;
                {accountNameValue}&quot;.
                {selectedSubscriptionIds.size > 1 && ' The first subscription becomes the parent account.'}
              </Alert>

              <Box
                sx={{
                  maxHeight: '300px',
                  overflowY: 'auto',
                  border: '1px solid #E5E7EB',
                  borderRadius: '8px',
                }}
              >
                {discoveredSubscriptions
                  .filter((s) => selectedSubscriptionIds.has(s.subscription_id))
                  .map((sub, idx) => (
                    <Box
                      key={sub.subscription_id}
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        px: 2,
                        py: 1,
                        borderBottom: '1px solid #F3F4F6',
                        '&:last-child': { borderBottom: 'none' },
                      }}
                    >
                      <Box sx={{ flex: 1 }}>
                        <Typography sx={{ fontSize: 13, fontWeight: 500 }}>
                          {sub.display_name}
                          {idx === 0 && selectedSubscriptionIds.size > 1 && (
                            <Chip label='Parent' size='small' sx={{ ml: 1, height: 20 }} color='primary' />
                          )}
                        </Typography>
                        <Typography sx={{ fontSize: 11, color: '#9CA3AF' }}>{sub.subscription_id}</Typography>
                      </Box>
                    </Box>
                  ))}
              </Box>
            </>
          )}

          {onboardResults && (
            <Box sx={{ mb: 2 }}>
              {onboardResults.accounts?.map((result) => (
                <Box
                  key={result.subscription_id}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    px: 2,
                    py: 1,
                    borderBottom: '1px solid #F3F4F6',
                    '&:last-child': { borderBottom: 'none' },
                  }}
                >
                  {result.status === 'created' ? (
                    <CheckCircleOutline sx={{ color: '#16A34A', fontSize: 18, mr: 1 }} />
                  ) : (
                    <ErrorOutline sx={{ color: '#DC2626', fontSize: 18, mr: 1 }} />
                  )}
                  <Box sx={{ flex: 1 }}>
                    <Typography sx={{ fontSize: 13, fontWeight: 500 }}>
                      {discoveredSubscriptions.find((s) => s.subscription_id === result.subscription_id)?.display_name || result.subscription_id}
                    </Typography>
                    <Typography sx={{ fontSize: 11, color: result.status === 'created' ? '#16A34A' : '#DC2626' }}>
                      {result.status === 'created' ? 'Onboarded successfully' : result.error}
                    </Typography>
                  </Box>
                </Box>
              ))}
            </Box>
          )}

          <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            {!onboardResults && (
              <>
                <Grid item>
                  <Button id='back-to-subscriptions' size='md' tone='secondary' onClick={() => setStep(1)} disabled={isSubmitting}>
                    Back
                  </Button>
                </Grid>
                <Grid item>
                  <Button
                    size='md'
                    id='onboard-subscriptions'
                    tone='primary'
                    loading={isSubmitting}
                    disabled={isSubmitting}
                    onClick={handleBulkOnboard}
                  >
                    Onboard
                  </Button>
                </Grid>
              </>
            )}
            {onboardResults && (
              <Grid item>
                <Button size='md' id='close-modal' tone='primary' onClick={() => handleCloseModal(true)}>
                  Done
                </Button>
              </Grid>
            )}
          </Grid>
        </>
      )}
    </Modal>
  );
};

export default AddAzureAccountModal;
