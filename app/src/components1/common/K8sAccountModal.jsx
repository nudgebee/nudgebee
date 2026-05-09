import { useState, useMemo } from 'react';
import {
  Grid,
  Typography,
  TextField,
  IconButton,
  Box,
  RadioGroup,
  FormControlLabel,
  Radio,
  Stepper,
  Step,
  StepLabel,
  Divider,
  Checkbox,
  Alert,
  ButtonBase,
} from '@mui/material';
import apiAccount from '@api1/account';
import { Modal } from './modal';
import { isK8sAccountNameValid } from 'src/utils/common';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import Tooltip from '@mui/material/Tooltip';
import PropTypes from 'prop-types';
import CustomButton from './NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from './snackbarService';
import { useUpdateAllClusterOption } from './UpdateDataContext';
import { CopyIconBlue, PlayCircleIcon } from '@assets';
import SafeIcon from './SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { inputSx } from '@data/themes/inputField';

const componentCardSx = (isDisabled) => ({
  display: 'flex',
  alignItems: 'flex-start',
  gap: 1,
  p: '11px 12px',
  border: `1px solid ${colors.border.secondaryLight}`,
  borderRadius: '8px',
  backgroundColor: isDisabled ? '#fafafa' : colors.background.white,
  opacity: isDisabled ? 0.72 : 1,
  transition: 'border-color 0.15s, background 0.15s',
  '&:hover': { borderColor: '#cbd5e1', backgroundColor: isDisabled ? '#fafafa' : '#fafbfc' },
});

const terminalBarSx = {
  display: 'flex',
  alignItems: 'center',
  gap: '6px',
  px: '12px',
  py: '8px',
  backgroundColor: '#1a2337',
  borderBottom: '1px solid rgba(255,255,255,0.06)',
};

const terminalCodeSx = {
  m: 0,
  fontFamily: 'monospace',
  fontSize: '12px',
  lineHeight: 1.65,
  color: '#e5e7eb',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
};

const terminalActionBtnSx = {
  fontSize: '12px',
  color: '#93c5fd',
  backgroundColor: 'rgba(147,197,253,0.08)',
  border: '1px solid rgba(147,197,253,0.18)',
  borderRadius: '6px',
  '&:hover': { backgroundColor: 'rgba(147,197,253,0.18)', color: '#dbeafe' },
};

const TerminalDots = () => (
  <Box sx={{ display: 'flex', gap: '5px' }}>
    <Box sx={{ width: 9, height: 9, borderRadius: '999px', backgroundColor: '#3a4558' }} />
    <Box sx={{ width: 9, height: 9, borderRadius: '999px', backgroundColor: '#3a4558' }} />
    <Box sx={{ width: 9, height: 9, borderRadius: '999px', backgroundColor: '#3a4558' }} />
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
  const [imageRegistry, setImageRegistry] = useState('registry.nudgebee.com');
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
    setImageRegistry('registry.nudgebee.com');
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
        const newAuthKey = `${res.data?.data?.cloud_accounts_insert_one?.access_key}:${res.data?.data?.cloud_accounts_insert_one?.access_secret}`;
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
    if (imageRegistry && imageRegistry !== 'registry.nudgebee.com') {
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
    if (imageRegistry && imageRegistry !== 'registry.nudgebee.com') {
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

  const installTabSx = (isActive) => ({
    display: 'inline-flex',
    alignItems: 'center',
    gap: 1,
    px: '14px',
    py: '12px',
    fontSize: '13px',
    fontWeight: isActive ? 600 : 500,
    color: isActive ? colors.primary : colors.text.secondaryDark,
    borderBottom: `2px solid ${isActive ? colors.primary : 'transparent'}`,
    cursor: 'pointer',
    mb: '-1px',
    borderRadius: 0,
    '&:hover': { color: isActive ? colors.darkPrimary : colors.text.secondary },
  });

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
        <CustomButton
          id='learn-how-to-install-btn'
          size='Small'
          text='Learn How to Install'
          startIcon={<SafeIcon src={PlayCircleIcon} alt='play' height={16} width={16} />}
          variant='tertiary'
          onClick={() => {
            window.open('https://docs.nudgebee.com/docs/installation/agent/installation/', '_blank', 'noopener,noreferrer');
          }}
          sx={{ fontSize: '12px', mr: 1 }}
        />
      }
      loader={isSubmitting}
    >
      <Box sx={{ px: '26px', pb: 3 }}>
        <Box sx={{ mb: 2, mt: '18px' }}>
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
                  <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.title, mb: 0.5 }}>Choose your account name</Typography>
                  <Typography variant='body2' sx={{ color: colors.text.secondaryDark, fontSize: '12px' }}>
                    This name will be used to identify your Kubernetes account in nudgebee. It should be unique and descriptive.
                  </Typography>
                </Box>
              </Grid>
              <Grid item xs={12} md={6}>
                <TextField
                  value={k8sNameValue}
                  size='small'
                  fullWidth
                  id='k8sName'
                  required
                  label='Account Name'
                  onChange={(e) => handleK8sAccountNameChange(e.target.value)}
                  helperText={validationError.k8sAccountName}
                  error={!!validationError.k8sAccountName}
                  disabled={isSubmitting}
                  sx={{ mt: 2 }}
                />
              </Grid>
            </Grid>

            <Grid item xs={12}>
              <Box display='flex' alignItems='center' mt={2}>
                <Typography variant='body2' sx={{ fontWeight: 500, mr: 2 }}>
                  Account Type:
                </Typography>
                <RadioGroup row name='account-env' value={accountEnvValue} onChange={(e) => setAccountEnvValue(e.target.value)}>
                  <FormControlLabel id='production-label' value='prod' control={<Radio />} label='Production' disabled={isSubmitting} />
                  <FormControlLabel id='non-production-label' value='non_prod' control={<Radio />} label='Non-production' disabled={isSubmitting} />
                </RadioGroup>
                <Tooltip
                  title='Used to preset notification policies based on the environment type. You can modify these policies anytime later.'
                  placement='right'
                  arrow
                >
                  <IconButton id='info-btn' size='small' sx={{ ml: 1, p: 0.5 }}>
                    <InfoOutlinedIcon fontSize='small' />
                  </IconButton>
                </Tooltip>
              </Box>
            </Grid>

            <Divider
              sx={{
                my: 1,
                borderStyle: 'dotted',
                borderColor: '#D0D0D0',
                borderBottomWidth: '2px',
              }}
            />

            <Grid item xs={12} mt={2}>
              <Typography sx={{ fontWeight: 600, fontSize: '13px', mb: 1, color: colors.text.title }}>
                Check these prerequisites before starting the installation.
              </Typography>
              <Box sx={{ p: 1.5, backgroundColor: '#fafbfc', borderRadius: '8px', border: `1px solid ${colors.border.secondaryLight}` }}>
                <Typography sx={{ fontWeight: 'bold', fontSize: '13px', mb: 1 }}>Software</Typography>
                <Box sx={{ pl: 2 }}>
                  <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Helm:</span> The Nudgebee Agent is deployed using Helm. Ensure that Helm is installed and
                    configured on your system.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Kubernetes:</span> The minimum supported Kubernetes version is 1.27. The agent has been
                    tested on this version and newer versions.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Linux Kernel:</span> Kubernetes cluster nodes must run at least Linux Kernel version 4.2 or
                    later to ensure eBPF compatibility for the Node Agent.
                  </Typography>
                </Box>
                <Typography sx={{ fontWeight: 'bold', fontSize: '13px', mt: 2, mb: 1 }}>Network</Typography>
                <Box sx={{ pl: 2 }}>
                  <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Docker Registry Access:</span> The installer must be able to access registry.nudgebee.com and
                    https://nudgebee.github.io/k8s-agent/ to pull necessary Docker images.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Collector/Relay Server Connectivity:</span> Agents must be able to connect to Collector/Relay
                    Servers over both Websocket and HTTP. These protocols must be allowed.
                  </Typography>
                  <Typography component='div' sx={{ fontSize: '12px', lineHeight: 1.4, mb: 0.5 }}>
                    <span style={{ fontWeight: 'bold' }}>Cloud Provider Pricing Endpoints:</span> If using OpenCost, the agent must be able to collect
                    pricing data from cloud providers such as AWS and Azure. The relevant pricing endpoints must be accessible.
                  </Typography>
                </Box>
              </Box>
            </Grid>

            <Grid container spacing={2} mt={3} mb={2} justifyContent='flex-end'>
              <Grid item>
                <CustomButton id='cancel-btn' size='Medium' text='Cancel' variant='secondary' onClick={handleClose} disabled={isSubmitting} />
              </Grid>
              <Grid item>
                <CustomButton
                  size='Medium'
                  id={'create-k8s-acc'}
                  text='Next'
                  loading={isSubmitting}
                  disabled={isSubmitting || !k8sNameValue || !!validationError.k8sAccountName}
                  onClick={handleNext}
                />
              </Grid>
            </Grid>
          </>
        )}

        {currentStep === 2 && (
          <>
            <Box mt={2} mb={3}>
              <Grid item xs={12}>
                <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, mb: 0.5 }}>
                  <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.title }}>Included Components</Typography>
                  <a
                    href='https://docs.nudgebee.com/docs/installation/agent/#components'
                    target='_blank'
                    rel='noopener noreferrer'
                    style={{ textDecoration: 'none', fontSize: '12px', fontWeight: 500, marginLeft: 'auto' }}
                  >
                    View component details ›
                  </a>
                </Box>
                <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark, mb: 1.5 }}>
                  All components are included by default. Check the box to disable any you don't need — the install command updates automatically.
                </Typography>
              </Grid>

              <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: '8px' }}>
                <Box sx={componentCardSx(disablePrometheusStack)}>
                  <Checkbox
                    checked={disablePrometheusStack}
                    onChange={(e) => handleDisablePrometheusStack(e.target.checked)}
                    size='small'
                    sx={{ p: 0, mt: '2px' }}
                  />
                  <Box>
                    <Typography sx={{ fontSize: '12.5px', fontWeight: 600, color: colors.text.title, lineHeight: 1.3 }}>Prometheus Stack</Typography>
                    <Typography sx={{ fontSize: '11.5px', color: colors.text.secondaryDark, lineHeight: 1.4, mt: '2px' }}>
                      Metrics collection — also controls OpenCost and Pod Monitor
                    </Typography>
                  </Box>
                </Box>

                <Box sx={componentCardSx(disableOpenCost)}>
                  <Checkbox checked={disableOpenCost} onChange={(e) => setDisableOpenCost(e.target.checked)} size='small' sx={{ p: 0, mt: '2px' }} />
                  <Box>
                    <Typography sx={{ fontSize: '12.5px', fontWeight: 600, color: colors.text.title, lineHeight: 1.3 }}>OpenCost</Typography>
                    <Typography sx={{ fontSize: '11.5px', color: colors.text.secondaryDark, lineHeight: 1.4, mt: '2px' }}>
                      Cost monitoring &amp; cloud pricing
                    </Typography>
                  </Box>
                </Box>

                <Box sx={componentCardSx(disableNodeAgent)}>
                  <Checkbox
                    checked={disableNodeAgent}
                    onChange={(e) => handleDisableNodeAgent(e.target.checked)}
                    size='small'
                    sx={{ p: 0, mt: '2px' }}
                  />
                  <Box>
                    <Typography sx={{ fontSize: '12.5px', fontWeight: 600, color: colors.text.title, lineHeight: 1.3 }}>Node Agent</Typography>
                    <Typography sx={{ fontSize: '11.5px', color: colors.text.secondaryDark, lineHeight: 1.4, mt: '2px' }}>
                      eBPF network &amp; process monitoring
                    </Typography>
                    <Box
                      sx={{
                        mt: '10px',
                        pt: '9px',
                        borderTop: `1px dashed ${colors.border.secondaryLight}`,
                        display: 'flex',
                        alignItems: 'center',
                        gap: 1,
                      }}
                    >
                      <Checkbox
                        checked={disablePodMonitor}
                        onChange={(e) => setDisablePodMonitor(e.target.checked)}
                        disabled={disableNodeAgent || disablePrometheusStack}
                        size='small'
                        sx={{ p: 0 }}
                      />
                      <Box>
                        <Typography
                          sx={{
                            fontSize: '11.5px',
                            fontWeight: 600,
                            color: disableNodeAgent || disablePrometheusStack ? colors.text.disabled : colors.text.secondary,
                            lineHeight: 1.3,
                          }}
                        >
                          Pod Monitor
                        </Typography>
                        <Typography
                          sx={{
                            fontSize: '11px',
                            color: disableNodeAgent || disablePrometheusStack ? colors.text.disabled : colors.text.secondaryDark,
                            lineHeight: 1.3,
                          }}
                        >
                          Scrape pod-level metrics
                        </Typography>
                      </Box>
                    </Box>
                  </Box>
                </Box>

                <Box sx={componentCardSx(disableOtelCollector)}>
                  <Checkbox
                    checked={disableOtelCollector}
                    onChange={(e) => setDisableOtelCollector(e.target.checked)}
                    size='small'
                    sx={{ p: 0, mt: '2px' }}
                  />
                  <Box>
                    <Typography sx={{ fontSize: '12.5px', fontWeight: 600, color: colors.text.title, lineHeight: 1.3 }}>
                      OpenTelemetry Collector
                    </Typography>
                    <Typography sx={{ fontSize: '11.5px', color: colors.text.secondaryDark, lineHeight: 1.4, mt: '2px' }}>
                      Traces &amp; logs · also controls ClickHouse
                    </Typography>
                  </Box>
                </Box>
              </Box>

              <Box sx={{ mt: '14px', border: `1px solid ${colors.border.secondaryLight}`, borderRadius: '8px', overflow: 'hidden' }}>
                <ButtonBase
                  component='div'
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 1,
                    p: '10px 14px',
                    width: '100%',
                    justifyContent: 'flex-start',
                    backgroundColor: colors.background.white,
                    '&:hover': { backgroundColor: '#fafbfc' },
                  }}
                  onClick={() => setAdvancedOpen((prev) => !prev)}
                  aria-expanded={advancedOpen}
                  aria-controls='adv-fields-panel'
                >
                  <Typography
                    sx={{
                      fontSize: '12px',
                      color: colors.text.secondaryDark,
                      transition: 'transform 0.2s',
                      lineHeight: 1,
                      transform: advancedOpen ? 'rotate(90deg)' : 'rotate(0deg)',
                    }}
                  >
                    ›
                  </Typography>
                  <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.title }}>Advanced</Typography>
                  <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark, ml: '6px' }}>
                    Use existing Prometheus · private registry · air-gapped environments
                  </Typography>
                </ButtonBase>
                {advancedOpen && (
                  <Box
                    id='adv-fields-panel'
                    role='region'
                    sx={{ p: '14px', borderTop: `1px solid ${colors.border.secondaryLightest}`, backgroundColor: '#fafbfc' }}
                  >
                    <Grid container spacing={2}>
                      <Grid item xs={12} sm={6}>
                        <TextField
                          sx={inputSx}
                          id='external-prometheus-url'
                          value={externalPrometheusUrl}
                          size='small'
                          fullWidth
                          label='Prometheus URL'
                          onChange={(e) => setExternalPrometheusUrl(e.target.value)}
                          placeholder='http://prometheus.namespace.svc:9090'
                          helperText={
                            disablePrometheusStack && !disableOpenCost
                              ? 'Required — no built-in stack, OpenCost needs Prometheus'
                              : 'Set if you have an existing Prometheus. Required for Helm + OpenCost.'
                          }
                          error={disablePrometheusStack && !disableOpenCost && !externalPrometheusUrl}
                          required={disablePrometheusStack && !disableOpenCost}
                        />
                      </Grid>
                      <Grid item xs={12} sm={6}>
                        <TextField
                          sx={inputSx}
                          id='image-registry'
                          value={imageRegistry}
                          size='small'
                          fullWidth
                          label='Image Registry'
                          onChange={(e) => setImageRegistry(e.target.value)}
                          placeholder='registry.nudgebee.com (default)'
                          helperText='Override for air-gapped or on-prem environments'
                        />
                      </Grid>
                    </Grid>
                  </Box>
                )}
              </Box>

              <Divider sx={{ my: '22px', borderColor: colors.border.secondaryLightest }} />

              <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, mb: 0.5 }}>
                <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.title }}>Install the Agent</Typography>
              </Box>
              <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark, mb: '10px' }}>
                Run the command on any machine with{' '}
                <code
                  style={{
                    fontFamily: 'monospace',
                    fontSize: '11.5px',
                    background: '#f3f4f6',
                    padding: '1px 5px',
                    borderRadius: '3px',
                  }}
                >
                  kubectl
                </code>{' '}
                access to your cluster.
              </Typography>

              <Box
                sx={{
                  border: `1px solid ${colors.border.secondaryLight}`,
                  borderRadius: '10px',
                  overflow: 'hidden',
                  backgroundColor: colors.background.white,
                }}
              >
                <Box
                  role='tablist'
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 0,
                    px: '6px',
                    backgroundColor: colors.background.white,
                    borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                  }}
                >
                  <ButtonBase
                    role='tab'
                    aria-selected={activeInstallTab === 'shell'}
                    aria-controls='panel-shell'
                    onClick={() => setActiveInstallTab('shell')}
                    sx={installTabSx(activeInstallTab === 'shell')}
                  >
                    Shell Script
                    <Box
                      component='span'
                      sx={{
                        fontSize: '9px',
                        fontWeight: 600,
                        letterSpacing: '0.4px',
                        textTransform: 'uppercase',
                        px: '5px',
                        py: '2px',
                        borderRadius: '3px',
                        backgroundColor: '#dcfce7',
                        color: '#15803d',
                      }}
                    >
                      Recommended
                    </Box>
                  </ButtonBase>
                  <ButtonBase
                    role='tab'
                    aria-selected={activeInstallTab === 'helm'}
                    aria-controls='panel-helm'
                    onClick={() => setActiveInstallTab('helm')}
                    sx={installTabSx(activeInstallTab === 'helm')}
                  >
                    Helm
                  </ButtonBase>
                </Box>

                {/* Shell Script Panel */}
                {activeInstallTab === 'shell' && (
                  <Box id='panel-shell' role='tabpanel' sx={{ p: '14px 16px 16px' }}>
                    <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark, mb: '10px', lineHeight: 1.5 }}>
                      <strong style={{ fontWeight: 500 }}>Auto-discovers</strong> existing Prometheus and Loki; installs any missing dependencies.
                      Safe to re-run.
                    </Typography>
                    <Box sx={{ borderRadius: '8px', overflow: 'hidden', border: '1px solid rgba(255,255,255,0.04)' }}>
                      <Box sx={terminalBarSx}>
                        <TerminalDots />
                        <Typography sx={{ fontFamily: 'monospace', fontSize: '11px', color: '#94a3b8', letterSpacing: '0.3px', flex: 1 }}>
                          install.sh
                        </Typography>
                      </Box>
                      <Box sx={{ backgroundColor: '#0f1729', p: '14px 16px' }}>
                        <Typography component='pre' sx={terminalCodeSx}>
                          {shellCommand}
                        </Typography>
                      </Box>
                      <Box sx={{ display: 'flex', gap: 1, p: '10px 16px 12px', backgroundColor: '#0f1729' }}>
                        <CustomButton
                          id='copy-command-btn'
                          size='Small'
                          text='Copy Command'
                          startIcon={<SafeIcon src={CopyIconBlue} alt='copy command' height={13} width={13} />}
                          variant='tertiary'
                          onClick={copyShellToClipboard}
                          sx={terminalActionBtnSx}
                        />
                        <CustomButton
                          id='copy-key-only-btn'
                          size='Small'
                          text='Copy Key Only'
                          startIcon={<SafeIcon src={CopyIconBlue} alt='copy key' height={13} width={13} />}
                          variant='tertiary'
                          onClick={copyAuthKeyToClipboard}
                          sx={terminalActionBtnSx}
                        />
                      </Box>
                    </Box>
                  </Box>
                )}

                {/* Helm Panel */}
                {activeInstallTab === 'helm' && (
                  <Box id='panel-helm' role='tabpanel' sx={{ p: '14px 16px 16px' }}>
                    <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark, mb: '10px', lineHeight: 1.5 }}>
                      Manual Helm installation. <strong style={{ fontWeight: 500 }}>Requires Prometheus URL</strong> if OpenCost is enabled (set it in
                      Advanced above).
                    </Typography>

                    {disablePrometheusStack && !disableOpenCost && !externalPrometheusUrl && (
                      <Alert severity='warning' sx={{ mb: 1.5 }}>
                        OpenCost requires a Prometheus URL. Set it in Advanced above, or disable OpenCost.
                      </Alert>
                    )}

                    <Box sx={{ borderRadius: '8px', overflow: 'hidden', border: '1px solid rgba(255,255,255,0.04)' }}>
                      <Box sx={terminalBarSx}>
                        <TerminalDots />
                        <Typography sx={{ fontFamily: 'monospace', fontSize: '11px', color: '#94a3b8', letterSpacing: '0.3px', flex: 1 }}>
                          helm install
                        </Typography>
                      </Box>
                      <Box sx={{ backgroundColor: '#0f1729', p: '14px 16px' }}>
                        <Typography component='pre' sx={terminalCodeSx}>
                          {helmCommand}
                        </Typography>
                      </Box>
                      <Box sx={{ p: '10px 16px 12px', backgroundColor: '#0f1729' }}>
                        <CustomButton
                          id='copy-play-btn'
                          size='Small'
                          text='Copy'
                          startIcon={<SafeIcon src={CopyIconBlue} alt='copy' height={13} width={13} />}
                          variant='tertiary'
                          onClick={copyHelmToClipboard}
                          sx={terminalActionBtnSx}
                        />
                      </Box>
                    </Box>
                  </Box>
                )}
              </Box>
            </Box>

            <Typography mt={2} sx={{ fontSize: '12px', color: colors.text.secondaryDark }}>
              Learn more about{' '}
              <a
                style={{ textDecoration: 'none', fontWeight: 500 }}
                href='https://docs.nudgebee.com/docs/installation/agent/installation/'
                target='_blank'
                rel='noopener noreferrer'
              >
                how to install
              </a>{' '}
              &{' '}
              <a
                target='_blank'
                style={{ textDecoration: 'none', fontWeight: 500 }}
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
                pt: '14px',
                borderTop: `1px solid ${colors.border.secondaryLightest}`,
              }}
            >
              <CustomButton id='finish-btn' size='Medium' text='Finish' onClick={handleFinish} />
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
