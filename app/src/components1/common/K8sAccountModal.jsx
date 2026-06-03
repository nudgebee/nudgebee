import { useState, useMemo } from 'react';
import { Grid, Typography, IconButton, Box, Stepper, Step, StepLabel, Divider, ButtonBase } from '@mui/material';
import apiAccount from '@api1/account';
import { Modal } from '@components1/ds/Modal';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import { Checkbox } from '@components1/ds/Checkbox';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import { Tabs } from '@components1/ds/Tabs';
import { Banner } from '@components1/ds/Banner';
import Tooltip from '@components1/ds/Tooltip';
import { isK8sAccountNameValid } from 'src/utils/common';
import { DEFAULT_IMAGE_REGISTRY, docsUrl } from '@lib/externalUrls';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { snackbar } from './snackbarService';
import { useUpdateAllClusterOption } from './UpdateDataContext';
import { CopyIconBlue, PlayCircleIcon } from '@assets';
import SafeIcon from './SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';

const componentCardSx = (isDisabled) => ({
  display: 'flex',
  alignItems: 'flex-start',
  gap: 1,
  p: 'var(--ds-space-3) var(--ds-space-3)',
  border: `1px solid ${colors.border.secondaryLight}`,
  borderRadius: 'var(--ds-radius-lg)',
  backgroundColor: isDisabled ? '#fafafa' : colors.background.white,
  opacity: isDisabled ? 0.72 : 1,
  transition: 'border-color 0.15s, background 0.15s',
  '&:hover': { borderColor: 'var(--ds-brand-200)', backgroundColor: isDisabled ? '#fafafa' : '#fafbfc' },
});

const terminalBarSx = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--ds-space-1)',
  px: 'var(--ds-space-3)',
  py: 'var(--ds-space-2)',
  backgroundColor: 'var(--ds-brand-700)',
  borderBottom: '1px solid rgba(255,255,255,0.06)',
};

const terminalCodeSx = {
  m: 0,
  fontFamily: 'monospace',
  fontSize: 'var(--ds-text-small)',
  lineHeight: 1.65,
  color: 'var(--ds-brand-150)',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
};

const TerminalDots = () => (
  <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)' }}>
    <Box sx={{ width: 9, height: 9, borderRadius: 'var(--ds-radius-pill)', backgroundColor: 'var(--ds-brand-500)' }} />
    <Box sx={{ width: 9, height: 9, borderRadius: 'var(--ds-radius-pill)', backgroundColor: 'var(--ds-brand-500)' }} />
    <Box sx={{ width: 9, height: 9, borderRadius: 'var(--ds-radius-pill)', backgroundColor: 'var(--ds-brand-500)' }} />
  </Box>
);

const K8sAccountModal = ({ openModal, handleClose, handleOnAccountCreate }) => {
  const [k8sNameValue, setK8sNameValue] = useState('');
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [accountEnvValue, setAccountEnvValue] = useState('non_prod');
  const [currentStep, setCurrentStep] = useState(1);

  const [authKey, setAuthKey] = useState('');
  const [disableOpenCost, setDisableOpenCost] = useState(false);
  const [disableNodeAgent, setDisableNodeAgent] = useState(false);
  const [disablePodMonitor, setDisablePodMonitor] = useState(false);
  const [disableOtelCollector, setDisableOtelCollector] = useState(false);
  const [disablePrometheusStack, setDisablePrometheusStack] = useState(false);
  const [externalPrometheusUrl, setExternalPrometheusUrl] = useState('');
  const [imageRegistry, setImageRegistry] = useState(DEFAULT_IMAGE_REGISTRY);
  const [activeInstallTab, setActiveInstallTab] = useState('shell');
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const updateAllClusters = useUpdateAllClusterOption();
  const { relayUrl, k8sCollectorUrl } = useBrandingConfig();

  const resetState = () => {
    setK8sNameValue('');
    setAccountEnvValue('non_prod');
    setValidationError({});
    setCurrentStep(1);
    setAuthKey('');
    setDisableOpenCost(false);
    setDisableNodeAgent(false);
    setDisablePodMonitor(false);
    setDisableOtelCollector(false);
    setDisablePrometheusStack(false);
    setExternalPrometheusUrl('');
    setImageRegistry(DEFAULT_IMAGE_REGISTRY);
    setActiveInstallTab('shell');
    setAdvancedOpen(false);
  };

  const handleK8sAccountNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setValidationError({
        k8sAccountName:
          'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore',
      });
    } else {
      setValidationError({});
    }
    setK8sNameValue(value);
  };

  const submitForm = async (data) => {
    setIsSubmitting(true);
    const bodyData = {
      account_name: data.k8sName,
      cloud_provider: 'K8s',
      account_type: 'kubernetes',
      account_env: accountEnvValue,
    };
    try {
      const res = await apiAccount.createAccount(bodyData);
      if (res.data.status === 'SUCCESS') {
        const newAuthKey = `${res.data?.data?.accounts_create?.access_key}:${res.data?.data?.accounts_create?.access_secret}`;
        setAuthKey(newAuthKey);
        setCurrentStep(2);
        updateAllClusters(true);
        if (handleOnAccountCreate) {
          handleOnAccountCreate();
        }
        snackbar.success(`Kubernetes account "${data.k8sName}" has been created successfully.`);
        return;
      }
      let msg = res?.data?.message;
      msg = msg?.includes('Uniqueness violation') ? 'Account name already exists.' : msg;
      snackbar.error(msg);
    } catch {
      snackbar.error('Failed to create account. Please try again.');
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleNext = () => {
    if (currentStep === 1 && !validationError.k8sAccountName && k8sNameValue) {
      submitForm({
        k8sName: k8sNameValue,
      });
    }
  };

  const handleFinish = () => {
    resetState();
    handleClose();
  };

  const handleDisableNodeAgent = (checked) => {
    setDisableNodeAgent(checked);
    if (checked) {
      setDisablePodMonitor(true);
    }
  };

  const handleDisablePrometheusStack = (checked) => {
    setDisablePrometheusStack(checked);
    if (checked) {
      setDisableOpenCost(true);
      setDisablePodMonitor(true);
      setExternalPrometheusUrl('');
    }
  };

  const shellCommand = useMemo(() => {
    const key = authKey || '<NUDGEBEE_AUTH_KEY>';
    let cmd = `curl -fsSL https://raw.githubusercontent.com/nudgebee/k8s-agent/main/installation.sh -o installation.sh && bash installation.sh -a "${key}"`;

    if (relayUrl) {
      cmd += ` -w "${relayUrl}"`;
    }
    if (k8sCollectorUrl) {
      cmd += ` -c "${k8sCollectorUrl}"`;
    }
    if (imageRegistry && imageRegistry !== DEFAULT_IMAGE_REGISTRY) {
      cmd += ` -i "${imageRegistry}"`;
    }
    if (disableNodeAgent) {
      cmd += ' -d true';
    }
    if (disableOpenCost) {
      cmd += ' -x true';
    }
    if (disableOtelCollector) {
      cmd += ' -t true';
    }
    if (disablePrometheusStack) {
      cmd += ' -g true';
    }
    if (externalPrometheusUrl) {
      cmd += ` -p "${externalPrometheusUrl}"`;
    }

    return cmd;
  }, [
    authKey,
    relayUrl,
    k8sCollectorUrl,
    imageRegistry,
    disableNodeAgent,
    disableOpenCost,
    disableOtelCollector,
    disablePrometheusStack,
    externalPrometheusUrl,
  ]);

  const helmCommand = useMemo(() => {
    const staticCommands = `helm repo add nudgebee-agent https://nudgebee.github.io/k8s-agent/
helm repo update`;

    let upgradeCommand = `helm upgrade --install nudgebee-agent nudgebee-agent/nudgebee-agent \\
  --namespace nudgebee-agent --create-namespace \\
  --set runner.nudgebee.auth_secret_key="${authKey || '<NUDGEBEE_AUTH_KEY>'}"`;

    if (relayUrl) {
      upgradeCommand += ` \\\n  --set runner.relay_address="${relayUrl}"`;
    }
    if (k8sCollectorUrl) {
      upgradeCommand += ` \\\n  --set runner.nudgebee.endpoint="${k8sCollectorUrl}"`;
    }
    if (imageRegistry && imageRegistry !== DEFAULT_IMAGE_REGISTRY) {
      upgradeCommand += ` \\\n  --set runner.image_registry="${imageRegistry}"`;
    }
    if (disablePrometheusStack) {
      upgradeCommand += ' \\\n  --set enablePrometheusStack=false';
    }
    if (disableOpenCost) {
      upgradeCommand += ' \\\n  --set opencost.enabled=false';
    }
    if (disableNodeAgent) {
      upgradeCommand += ' \\\n  --set nodeAgent.enabled=false';
    }
    if (disablePodMonitor) {
      upgradeCommand += ' \\\n  --set nodeAgent.podmonitor.enabled=false';
    }
    if (disableOtelCollector) {
      upgradeCommand += ' \\\n  --set opentelemetry-collector.enabled=false';
      upgradeCommand += ' \\\n  --set clickhouse.enabled=false';
    }
    if (externalPrometheusUrl) {
      upgradeCommand += ` \\\n  --set globalConfig.prometheus_url="${externalPrometheusUrl}"`;
      upgradeCommand += ` \\\n  --set opencost.opencost.prometheus.external.url="${externalPrometheusUrl}"`;
    }

    return `${staticCommands}\n\n${upgradeCommand}`;
  }, [
    authKey,
    relayUrl,
    k8sCollectorUrl,
    imageRegistry,
    disablePrometheusStack,
    disableOpenCost,
    disableNodeAgent,
    disablePodMonitor,
    disableOtelCollector,
    externalPrometheusUrl,
  ]);

  const copyShellToClipboard = () => {
    try {
      navigator.clipboard.writeText(shellCommand);
      snackbar.success('Copied to clipboard!');
    } catch {
      snackbar.error('Failed to copy. Try manually.');
    }
  };

  const copyAuthKeyToClipboard = () => {
    try {
      navigator.clipboard.writeText(authKey);
      snackbar.success('Key copied to clipboard!');
    } catch {
      snackbar.error('Failed to copy key. Try manually.');
    }
  };

  const copyHelmToClipboard = () => {
    try {
      navigator.clipboard.writeText(helmCommand);
      snackbar.success('Copied to clipboard!');
    } catch {
      snackbar.error('Failed to copy. Try manually.');
    }
  };

  return (
    <Modal
      width='md'
      open={openModal}
      handleClose={() => {
        resetState();
        handleClose();
      }}
      title='Add Kubernetes Account'
      rightComponentOnTitle={
        <Box sx={{ mr: 1 }}>
          <Button
            id='learn-how-to-install-btn'
            tone='link'
            size='sm'
            icon={<SafeIcon src={PlayCircleIcon} alt='play' height={16} width={16} />}
            iconPlacement='start'
            onClick={() => {
              window.open(docsUrl('/docs/installation/agent/installation/'), '_blank', 'noopener,noreferrer');
            }}
          >
            Learn How to Install
          </Button>
        </Box>
      }
      loader={isSubmitting}
    >
      <Box sx={{ px: 'var(--ds-space-5)', pb: 3 }}>
        <Box sx={{ mb: 2, mt: 'var(--ds-space-4)' }}>
          <Stepper activeStep={currentStep - 1} orientation='horizontal'>
            <Step>
              <StepLabel>Set Account Name & Prerequisites</StepLabel>
            </Step>
            <Step>
              <StepLabel>Finish Setup</StepLabel>
            </Step>
          </Stepper>
        </Box>
        {currentStep === 1 && (
          <>
            <Grid container mb={2}>
              <Grid item xs={12}>
                <Box>
                  <Typography
                    sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.title, mb: 0.5 }}
                  >
                    Choose your account name
                  </Typography>
                  <Typography variant='body2' sx={{ color: colors.text.secondaryDark, fontSize: 'var(--ds-text-small)' }}>
                    This name will be used to identify your Kubernetes account in nudgebee. It should be unique and descriptive.
                  </Typography>
                </Box>
              </Grid>
              <Grid item xs={12} md={6}>
                <Box sx={{ mt: 2 }}>
                  <Input
                    value={k8sNameValue}
                    size='sm'
                    id='k8sName'
                    required
                    label='Account Name'
                    onChange={handleK8sAccountNameChange}
                    error={validationError.k8sAccountName}
                    disabled={isSubmitting}
                  />
                </Box>
              </Grid>
            </Grid>

            <Grid item xs={12}>
              <Box display='flex' alignItems='center' mt={2} gap={2}>
                <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                  Account Type:
                </Typography>
                <ToggleGroup
                  id='account-env'
                  ariaLabel='Account environment type'
                  selection='single'
                  size='md'
                  value={accountEnvValue}
                  onChange={setAccountEnvValue}
                  options={[
                    { value: 'prod', label: 'Production', disabled: isSubmitting },
                    { value: 'non_prod', label: 'Non-production', disabled: isSubmitting },
                  ]}
                />
                <Tooltip
                  title='Used to preset notification policies based on the environment type. You can modify these policies anytime later.'
                  placement='right'
                >
                  <IconButton id='info-btn' size='small' sx={{ p: 0.5 }}>
                    <InfoOutlinedIcon fontSize='small' />
                  </IconButton>
                </Tooltip>
              </Box>
            </Grid>

            <Divider
              sx={{
                my: 1,
                borderStyle: 'dotted',
                borderColor: 'var(--ds-brand-200)',
                borderBottomWidth: '2px',
              }}
            />

            <Grid item xs={12} mt={2}>
              <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body)', mb: 1, color: colors.text.title }}>
                Check these prerequisites before starting the installation.
              </Typography>
              <Box
                sx={{
                  p: 1.5,
                  backgroundColor: 'var(--ds-background-200)',
                  borderRadius: 'var(--ds-radius-lg)',
                  border: `1px solid ${colors.border.secondaryLight}`,
                }}
              >
                <Typography sx={{ fontWeight: 'bold', fontSize: 'var(--ds-text-body)', mb: 1 }}>Software</Typography>
                <Box sx={{ pl: 2 }}>
                  <Typography component='div' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Helm:</span> The Nudgebee Agent is deployed using Helm. Ensure that Helm is installed and
                    configured on your system.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Kubernetes:</span> The minimum supported Kubernetes version is 1.27. The agent has been
                    tested on this version and newer versions.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Linux Kernel:</span> Kubernetes cluster nodes must run at least Linux Kernel version 4.2 or
                    later to ensure eBPF compatibility for the Node Agent.
                  </Typography>
                </Box>
                <Typography sx={{ fontWeight: 'bold', fontSize: 'var(--ds-text-body)', mt: 2, mb: 1 }}>Network</Typography>
                <Box sx={{ pl: 2 }}>
                  <Typography component='div' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Docker Registry Access:</span> The installer must be able to access {DEFAULT_IMAGE_REGISTRY}{' '}
                    and https://nudgebee.github.io/k8s-agent/ to pull necessary Docker images.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Collector/Relay Server Connectivity:</span> Agents must be able to connect to Collector/Relay
                    Servers over both Websocket and HTTP. These protocols must be allowed.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Cloud Provider Pricing Endpoints:</span> If using OpenCost, the agent must be able to collect
                    pricing data from cloud providers such as AWS and Azure. The relevant pricing endpoints must be accessible.
                  </Typography>
                </Box>
              </Box>
            </Grid>

            <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', mt: 3, mb: 2 }}>
              <Button id='cancel-btn' tone='secondary' size='md' onClick={handleClose} disabled={isSubmitting}>
                Cancel
              </Button>
              <Button
                id='create-k8s-acc'
                tone='primary'
                size='md'
                loading={isSubmitting}
                disabled={isSubmitting || !k8sNameValue || !!validationError.k8sAccountName}
                onClick={handleNext}
              >
                Next
              </Button>
            </Box>
          </>
        )}

        {currentStep === 2 && (
          <>
            <Box mt={2} mb={3}>
              <Grid item xs={12}>
                <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, mb: 0.5 }}>
                  <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.title }}>
                    Included Components
                  </Typography>
                  <a
                    href={docsUrl('/docs/installation/agent/#components')}
                    target='_blank'
                    rel='noopener noreferrer'
                    style={{
                      textDecoration: 'none',
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      marginLeft: 'auto',
                    }}
                  >
                    View component details ›
                  </a>
                </Box>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, mb: 1.5 }}>
                  All components are included by default. Check the box to{' '}
                  <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.red }}>
                    DISABLE
                  </Box>{' '}
                  any you don't need — the install command updates automatically.
                </Typography>
              </Grid>

              <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: 'var(--ds-space-2)' }}>
                <Box sx={componentCardSx(disablePrometheusStack)}>
                  <Checkbox
                    size='sm'
                    checked={disablePrometheusStack}
                    onChange={handleDisablePrometheusStack}
                    label='Prometheus Stack'
                    description='Metrics collection — also controls OpenCost and Pod Monitor'
                  />
                </Box>

                <Box sx={componentCardSx(disableOpenCost)}>
                  <Box>
                    <Checkbox
                      size='sm'
                      checked={disableOpenCost}
                      onChange={setDisableOpenCost}
                      label='OpenCost'
                      description='Cost monitoring & cloud pricing'
                    />
                    {!disableOpenCost && disablePrometheusStack && (
                      <Typography
                        data-testid='opencost-prometheus-warning'
                        sx={{
                          mt: 'var(--ds-space-1)',
                          pl: 'var(--ds-space-4)',
                          fontSize: 'var(--ds-text-caption)',
                          lineHeight: 1.35,
                          color: externalPrometheusUrl ? colors.text.secondaryDark : colors.text.red,
                          fontWeight: externalPrometheusUrl ? 400 : 600,
                        }}
                      >
                        {externalPrometheusUrl
                          ? 'Using external Prometheus URL from Advanced.'
                          : 'Requires Prometheus URL — set in Advanced or OpenCost will fail.'}
                      </Typography>
                    )}
                  </Box>
                </Box>

                <Box sx={componentCardSx(disableNodeAgent)}>
                  <Box sx={{ width: '100%' }}>
                    <Checkbox
                      size='sm'
                      checked={disableNodeAgent}
                      onChange={handleDisableNodeAgent}
                      label='Node Agent'
                      description='eBPF network & process monitoring'
                    />
                    <Box
                      sx={{
                        mt: 'var(--ds-space-2)',
                        pt: 'var(--ds-space-2)',
                        borderTop: `1px dashed ${colors.border.secondaryLight}`,
                      }}
                    >
                      <Checkbox
                        size='sm'
                        checked={disablePodMonitor}
                        onChange={setDisablePodMonitor}
                        disabled={disableNodeAgent || disablePrometheusStack}
                        label='Pod Monitor'
                        description='Scrape pod-level metrics'
                      />
                    </Box>
                  </Box>
                </Box>

                <Box sx={componentCardSx(disableOtelCollector)}>
                  <Checkbox
                    size='sm'
                    checked={disableOtelCollector}
                    onChange={setDisableOtelCollector}
                    label='OpenTelemetry Collector'
                    description='Traces & logs · also controls ClickHouse'
                  />
                </Box>
              </Box>

              <Box
                sx={{
                  mt: 'var(--ds-space-3)',
                  border: `1px solid ${colors.border.secondaryLight}`,
                  borderRadius: 'var(--ds-radius-lg)',
                  overflow: 'hidden',
                }}
              >
                <ButtonBase
                  component='div'
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 1,
                    p: 'var(--ds-space-2) var(--ds-space-3)',
                    width: '100%',
                    justifyContent: 'flex-start',
                    backgroundColor: colors.background.white,
                    '&:hover': { backgroundColor: 'var(--ds-background-200)' },
                  }}
                  onClick={() => setAdvancedOpen((prev) => !prev)}
                  aria-expanded={advancedOpen}
                  aria-controls='adv-fields-panel'
                >
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      color: colors.text.secondaryDark,
                      transition: 'transform 0.2s',
                      lineHeight: 1,
                      transform: advancedOpen ? 'rotate(90deg)' : 'rotate(0deg)',
                    }}
                  >
                    ›
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.title }}>
                    Advanced
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, ml: 'var(--ds-space-1)' }}>
                    Use existing Prometheus · private registry · air-gapped environments
                  </Typography>
                </ButtonBase>
                {advancedOpen && (
                  <Box
                    id='adv-fields-panel'
                    role='region'
                    sx={{
                      p: 'var(--ds-space-3)',
                      borderTop: `1px solid ${colors.border.secondaryLightest}`,
                      backgroundColor: 'var(--ds-background-200)',
                    }}
                  >
                    <Grid container spacing={2}>
                      <Grid item xs={12} sm={6}>
                        <Input
                          id='external-prometheus-url'
                          value={externalPrometheusUrl}
                          size='sm'
                          label='Prometheus URL'
                          onChange={setExternalPrometheusUrl}
                          placeholder='http://prometheus.namespace.svc:9090'
                          help={
                            disablePrometheusStack && !disableOpenCost && !externalPrometheusUrl
                              ? undefined
                              : 'Set if you have an existing Prometheus. Required for Helm + OpenCost.'
                          }
                          error={
                            disablePrometheusStack && !disableOpenCost && !externalPrometheusUrl
                              ? 'Required — no built-in stack, OpenCost needs Prometheus'
                              : undefined
                          }
                          required={disablePrometheusStack && !disableOpenCost}
                        />
                      </Grid>
                      <Grid item xs={12} sm={6}>
                        <Input
                          id='image-registry'
                          value={imageRegistry}
                          size='sm'
                          label='Image Registry'
                          onChange={setImageRegistry}
                          placeholder={`${DEFAULT_IMAGE_REGISTRY} (default)`}
                          help='Override for air-gapped or on-prem environments'
                        />
                      </Grid>
                    </Grid>
                  </Box>
                )}
              </Box>

              <Divider sx={{ my: 'var(--ds-space-5)', borderColor: colors.border.secondaryLightest }} />

              <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, mb: 0.5 }}>
                <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.title }}>
                  Install the Agent
                </Typography>
              </Box>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, mb: 'var(--ds-space-2)' }}>
                Run the command on any machine with{' '}
                <code
                  style={{
                    fontFamily: 'monospace',
                    fontSize: '11.5px',
                    background: 'var(--ds-background-300)',
                    padding: 'var(--ds-space-1) var(--ds-space-1)',
                    borderRadius: 'var(--ds-radius-sm)',
                  }}
                >
                  kubectl
                </code>{' '}
                access to your cluster.
              </Typography>

              <Box
                sx={{
                  border: `1px solid ${colors.border.secondaryLight}`,
                  borderRadius: 'var(--ds-radius-lg)',
                  overflow: 'hidden',
                  backgroundColor: colors.background.white,
                }}
              >
                <Box sx={{ px: 'var(--ds-space-1)', borderBottom: `1px solid ${colors.border.secondaryLightest}` }}>
                  <Tabs
                    size='sm'
                    value={activeInstallTab}
                    onChange={setActiveInstallTab}
                    ariaLabel='Install method'
                    tabs={[
                      { id: 'shell', label: 'Shell Script', beta: false },
                      { id: 'helm', label: 'Helm' },
                    ]}
                    rightSlot={
                      activeInstallTab === 'shell' ? (
                        <Box
                          component='span'
                          sx={{
                            fontSize: 'var(--ds-text-caption)',
                            fontWeight: 'var(--ds-font-weight-semibold)',
                            letterSpacing: '0.4px',
                            textTransform: 'uppercase',
                            px: 'var(--ds-space-1)',
                            py: 'var(--ds-space-1)',
                            borderRadius: 'var(--ds-radius-sm)',
                            backgroundColor: 'var(--ds-green-100)',
                            color: 'var(--ds-green-600)',
                          }}
                        >
                          Recommended
                        </Box>
                      ) : undefined
                    }
                  />
                </Box>

                {/* Shell Script Panel */}
                {activeInstallTab === 'shell' && (
                  <Box id='panel-shell' role='tabpanel' sx={{ p: 'var(--ds-space-3) var(--ds-space-4) var(--ds-space-4)' }}>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, mb: 'var(--ds-space-2)', lineHeight: 1.5 }}>
                      <strong style={{ fontWeight: 'var(--ds-font-weight-medium)' }}>Auto-discovers</strong> existing Prometheus and Loki; installs
                      any missing dependencies. Safe to re-run.
                    </Typography>
                    <Box sx={{ borderRadius: 'var(--ds-radius-lg)', overflow: 'hidden', border: '1px solid rgba(255,255,255,0.04)' }}>
                      <Box sx={terminalBarSx}>
                        <TerminalDots />
                        <Typography
                          sx={{
                            fontFamily: 'monospace',
                            fontSize: 'var(--ds-text-caption)',
                            color: 'var(--ds-brand-300)',
                            letterSpacing: '0.3px',
                            flex: 1,
                          }}
                        >
                          install.sh
                        </Typography>
                      </Box>
                      <Box sx={{ backgroundColor: 'var(--ds-brand-700)', p: 'var(--ds-space-3) var(--ds-space-4)' }}>
                        <Typography component='pre' sx={terminalCodeSx}>
                          {shellCommand}
                        </Typography>
                      </Box>
                      <Box
                        sx={{
                          display: 'flex',
                          gap: 1,
                          p: 'var(--ds-space-2) var(--ds-space-4) var(--ds-space-3)',
                          backgroundColor: 'var(--ds-brand-700)',
                        }}
                      >
                        <Button
                          id='copy-command-btn'
                          tone='secondary'
                          size='sm'
                          icon={<SafeIcon src={CopyIconBlue} alt='copy command' height={13} width={13} />}
                          iconPlacement='start'
                          onClick={copyShellToClipboard}
                        >
                          Copy Command
                        </Button>
                        <Button
                          id='copy-key-only-btn'
                          tone='secondary'
                          size='sm'
                          icon={<SafeIcon src={CopyIconBlue} alt='copy key' height={13} width={13} />}
                          iconPlacement='start'
                          onClick={copyAuthKeyToClipboard}
                        >
                          Copy Key Only
                        </Button>
                      </Box>
                    </Box>
                  </Box>
                )}

                {/* Helm Panel */}
                {activeInstallTab === 'helm' && (
                  <Box id='panel-helm' role='tabpanel' sx={{ p: 'var(--ds-space-3) var(--ds-space-4) var(--ds-space-4)' }}>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, mb: 'var(--ds-space-2)', lineHeight: 1.5 }}>
                      Manual Helm installation. <strong style={{ fontWeight: 'var(--ds-font-weight-medium)' }}>Requires Prometheus URL</strong> if
                      OpenCost is enabled (set it in Advanced above).
                    </Typography>

                    {disablePrometheusStack && !disableOpenCost && !externalPrometheusUrl && (
                      <Box sx={{ mb: 1.5 }}>
                        <Banner
                          tone='warning'
                          surface='section'
                          message='OpenCost requires a Prometheus URL. Set it in Advanced above, or disable OpenCost.'
                        />
                      </Box>
                    )}

                    <Box sx={{ borderRadius: 'var(--ds-radius-lg)', overflow: 'hidden', border: '1px solid rgba(255,255,255,0.04)' }}>
                      <Box sx={terminalBarSx}>
                        <TerminalDots />
                        <Typography
                          sx={{
                            fontFamily: 'monospace',
                            fontSize: 'var(--ds-text-caption)',
                            color: 'var(--ds-brand-300)',
                            letterSpacing: '0.3px',
                            flex: 1,
                          }}
                        >
                          helm install
                        </Typography>
                      </Box>
                      <Box sx={{ backgroundColor: 'var(--ds-brand-700)', p: 'var(--ds-space-3) var(--ds-space-4)' }}>
                        <Typography component='pre' sx={terminalCodeSx}>
                          {helmCommand}
                        </Typography>
                      </Box>
                      <Box sx={{ p: 'var(--ds-space-2) var(--ds-space-4) var(--ds-space-3)', backgroundColor: 'var(--ds-brand-700)' }}>
                        <Button
                          id='copy-play-btn'
                          tone='secondary'
                          size='sm'
                          icon={<SafeIcon src={CopyIconBlue} alt='copy' height={13} width={13} />}
                          iconPlacement='start'
                          onClick={copyHelmToClipboard}
                        >
                          Copy
                        </Button>
                      </Box>
                    </Box>
                  </Box>
                )}
              </Box>
            </Box>

            <Typography mt={2} sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark }}>
              Learn more about{' '}
              <a
                style={{ textDecoration: 'none', fontWeight: 'var(--ds-font-weight-medium)' }}
                href={docsUrl('/docs/installation/agent/installation/')}
                target='_blank'
                rel='noopener noreferrer'
              >
                how to install
              </a>{' '}
              &{' '}
              <a
                target='_blank'
                style={{ textDecoration: 'none', fontWeight: 'var(--ds-font-weight-medium)' }}
                href='https://github.com/nudgebee/k8s-agent/blob/main/charts/nudgebee-agent/templates/runner-service-account.yaml'
                rel='noreferrer'
              >
                required permissions
              </a>
            </Typography>

            <Box
              sx={{
                display: 'flex',
                justifyContent: 'flex-end',
                mt: 3,
                mb: 2,
                pt: 'var(--ds-space-3)',
                borderTop: `1px solid ${colors.border.secondaryLightest}`,
              }}
            >
              <Button id='finish-btn' tone='primary' size='md' onClick={handleFinish}>
                Finish
              </Button>
            </Box>
          </>
        )}
      </Box>
    </Modal>
  );
};

K8sAccountModal.propTypes = {
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  handleOnAccountCreate: PropTypes.func,
};

export default K8sAccountModal;
