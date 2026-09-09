import { useState, useMemo, useEffect } from 'react';
import { Grid, Typography, Box, Divider, ButtonBase } from '@mui/material';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import apiAccount from '@api1/account';
import { Modal } from '@ui/Modal';
import { Stepper } from '@ui/Stepper';
import { Input } from '@ui/Input';
import { Button } from '@ui/Button';
import { Checkbox } from '@ui/Checkbox';
import { Card } from '@ui/Card';
import AccountEnvToggle, { DEFAULT_ACCOUNT_ENV, accountEnvTooltip } from '@shared/forms/AccountEnvToggle';
import LabelWithInfo from '@components/ownership/LabelWithInfo';
import Tabs from '@shared/navigation/Tabs';
import { Banner } from '@ui/Banner';
import { isK8sAccountNameValid } from 'src/utils/common';
import { DEFAULT_IMAGE_REGISTRY, docsUrl } from '@lib/externalUrls';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { toast as snackbar } from '@ui/Toast';
import { useUpdateAllClusterOption } from '@shared/layout/UpdateDataContext';
import { CopyIconBlue, PlayCircleIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { useTour } from '@components/common/tour';

// The chart enables these three by default; a minimum install has to turn them
// off explicitly. Isolated here so the whole set is one edit when the chart's
// defaults change (see docs/k8s-onboarding.md §1.4).
const MINIMAL_INSTALL_SHELL_FLAGS = ' -g true -x true -t true';
const MINIMAL_INSTALL_HELM_VALUES = [
  'enablePrometheusStack=false',
  'opencost.enabled=false',
  'opentelemetry-collector.enabled=false',
  'clickhouse.enabled=false',
];

const componentCardSx = (isDisabled) => ({
  display: 'flex',
  alignItems: 'flex-start',
  gap: 1,
  p: 'var(--ds-space-3) var(--ds-space-3)',
  border: `1px solid ${ds.gray[200]}`,
  borderRadius: 'var(--ds-radius-lg)',
  backgroundColor: isDisabled ? ds.background[200] : ds.background[100],
  opacity: isDisabled ? 0.72 : 1,
  transition: 'border-color 0.15s, background 0.15s',
  '&:hover': { borderColor: 'var(--ds-brand-200)', backgroundColor: isDisabled ? ds.background[200] : ds.background[200] },
});

const terminalBarSx = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--ds-space-1)',
  px: 'var(--ds-space-3)',
  py: 'var(--ds-space-2)',
  backgroundColor: 'var(--ds-brand-700)',
  borderBottom: `1px solid color-mix(in srgb, ${ds.background[100]} 6%, transparent)`,
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

// Inline rationale under a field — the "Why" device from the approved mocks
// (docs/mockups/k8s-account-onboarding-v2.html). Built from Typography/Box and
// DS tokens only; not a new ds/* primitive since it's a single-file pattern.
const WhyNote = ({ children }) => (
  <Typography
    component='div'
    sx={{
      mt: 'var(--ds-space-2)',
      pl: 'var(--ds-space-3)',
      borderLeft: `2px solid ${ds.blue[200]}`,
      fontSize: 'var(--ds-text-caption)',
      lineHeight: 1.55,
      color: ds.gray[500],
      '& b': { fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] },
    }}
  >
    <Box
      component='span'
      sx={{
        fontFamily: 'monospace',
        fontSize: '9px',
        fontWeight: 'var(--ds-font-weight-medium)',
        letterSpacing: '0.11em',
        textTransform: 'uppercase',
        color: ds.blue[600],
        mr: 'var(--ds-space-2)',
      }}
    >
      Why
    </Box>
    {children}
  </Typography>
);

// Uppercase caption group header for the prerequisites card (Software / Network).
const PrereqGroupHeader = ({ children }) => (
  <Typography
    sx={{
      fontWeight: 'var(--ds-font-weight-semibold)',
      fontSize: 'var(--ds-text-caption)',
      letterSpacing: '0.06em',
      textTransform: 'uppercase',
      color: ds.brand[600],
      mb: 'var(--ds-space-2)',
    }}
  >
    {children}
  </Typography>
);

// One prerequisite line: a small accent dot + a coloured label so the list
// reads as more than flat black-on-grey text.
const PrereqItem = ({ label, children }) => (
  <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', mb: 'var(--ds-space-2)', '&:last-child': { mb: 0 } }}>
    <Box sx={{ width: 5, height: 5, borderRadius: 'var(--ds-radius-pill)', backgroundColor: ds.brand[300], mt: '7px', flexShrink: 0 }} />
    <Typography component='div' sx={{ fontSize: 'var(--ds-text-small)', lineHeight: 1.5, color: ds.gray[600] }}>
      <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: ds.brand[600] }}>
        {label}:{' '}
      </Box>
      {children}
    </Typography>
  </Box>
);

const K8sAccountModal = ({ openModal, handleClose, handleOnAccountCreate }) => {
  const [k8sNameValue, setK8sNameValue] = useState('');
  const [validationError, setValidationError] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [accountEnvValue, setAccountEnvValue] = useState(DEFAULT_ACCOUNT_ENV);
  const [currentStep, setCurrentStep] = useState(1);

  const [authKey, setAuthKey] = useState('');
  // Both read from the nodes themselves — no hosted tool can substitute for
  // either, and changing them later means re-running the install.
  const [nodeAgentEnabled, setNodeAgentEnabled] = useState(true);
  const [podMonitorEnabled, setPodMonitorEnabled] = useState(true);
  const [imageRegistry, setImageRegistry] = useState(DEFAULT_IMAGE_REGISTRY);
  // Collapsed by default — most installs never touch it.
  const [advancedOpen, setAdvancedOpen] = useState(false);
  // Helm leads: its values are explicit/reviewable and it's the only tab where
  // every option on this screen is honoured (the shell installer has no Pod
  // Monitor flag). See docs/k8s-onboarding.md §1.6.
  const [activeInstallTab, setActiveInstallTab] = useState('helm');
  // True when the install step is shown as a guided-tour preview (no real account
  // created). Drives the "preview" banner; reset with the rest of the form.
  const [isPreview, setIsPreview] = useState(false);

  const updateAllClusters = useUpdateAllClusterOption();
  const { relayUrl, k8sCollectorUrl, signingPublicKey, title: baseTitle } = useBrandingConfig();

  // The "connect-cluster" guided tour drives this modal to demonstrate the flow.
  // When it's the active tour we preview the install step with a sample key
  // instead of creating a real account (see handleNext). isActive is false
  // outside any tour, so real account creation is completely unaffected.
  const { isActive, activeTourId } = useTour();
  const isTourDemo = isActive && activeTourId === 'connect-cluster';

  // When the guided tour ends while the install preview is open, close the modal
  // too — it's a demo with no real account behind it, so leaving it open (with an
  // enabled Finish) would be confusing.
  useEffect(() => {
    if (isPreview && !isTourDemo) {
      resetState();
      handleClose();
    }
    // resetState/handleClose are stable enough for this one-shot cleanup.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isPreview, isTourDemo]);

  const resetState = () => {
    setK8sNameValue('');
    setAccountEnvValue(DEFAULT_ACCOUNT_ENV);
    setValidationError({});
    setCurrentStep(1);
    setAuthKey('');
    setNodeAgentEnabled(true);
    setPodMonitorEnabled(true);
    setImageRegistry(DEFAULT_IMAGE_REGISTRY);
    setAdvancedOpen(false);
    setActiveInstallTab('helm');
    setIsPreview(false);
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
    // Guided-tour preview: reveal the install step with a sample key rather than
    // creating a real account. Gated on the connect-cluster tour being active, so
    // this branch never runs during normal use.
    if (isTourDemo && currentStep === 1) {
      setAuthKey('NB-DEMO-ACCESS-KEY:NB-DEMO-SECRET');
      setIsPreview(true);
      setCurrentStep(2);
      return;
    }
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

  // Pod Monitor rides on the Node Agent DaemonSet, so turning Node Agent off
  // turns Pod Monitor off too.
  const handleNodeAgentToggle = (checked) => {
    setNodeAgentEnabled(checked);
    if (!checked) {
      setPodMonitorEnabled(false);
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
    if (!nodeAgentEnabled) {
      cmd += ' -d true';
    }
    // Bare-minimum install: the chart enables the Prometheus stack, OpenCost
    // and the OTel collector by default, so turn all three off explicitly.
    cmd += MINIMAL_INSTALL_SHELL_FLAGS;
    if (signingPublicKey) {
      cmd += ` -S "${signingPublicKey}"`;
    }

    return cmd;
  }, [authKey, relayUrl, k8sCollectorUrl, imageRegistry, nodeAgentEnabled, signingPublicKey]);

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
    // Bare-minimum install: the chart enables the Prometheus stack, OpenCost
    // and the OTel collector by default, so turn all three off explicitly.
    MINIMAL_INSTALL_HELM_VALUES.forEach((value) => {
      upgradeCommand += ` \\\n  --set ${value}`;
    });
    if (!nodeAgentEnabled) {
      upgradeCommand += ' \\\n  --set nodeAgent.enabled=false';
    }
    if (!podMonitorEnabled) {
      upgradeCommand += ' \\\n  --set nodeAgent.podmonitor.enabled=false';
    }
    if (signingPublicKey) {
      // --set-string: the key may contain '=' (base64 padding) or spaces (ssh form).
      upgradeCommand += ` \\\n  --set-string runner.nudgebee.relay_signing_public_key="${signingPublicKey}"`;
    }

    return `${staticCommands}\n\n${upgradeCommand}`;
  }, [authKey, relayUrl, k8sCollectorUrl, imageRegistry, nodeAgentEnabled, podMonitorEnabled, signingPublicKey]);

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
        <Box sx={{ mr: 1, display: 'flex', alignItems: 'center', gap: 1 }}>
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
          <Divider orientation='vertical' flexItem sx={{ borderColor: ds.gray[300], my: 0.5 }} />
          <Button
            id='required-permissions-btn'
            tone='link'
            size='sm'
            icon={<LockOutlinedIcon sx={{ fontSize: 16 }} />}
            iconPlacement='start'
            onClick={() => {
              window.open(
                'https://github.com/nudgebee/k8s-agent/blob/main/charts/nudgebee-agent/templates/runner-service-account.yaml',
                '_blank',
                'noopener,noreferrer'
              );
            }}
          >
            Required Permissions
          </Button>
        </Box>
      }
      loader={isSubmitting}
      actionButtons={
        currentStep === 1 ? (
          <>
            <Button id='cancel-btn' tone='secondary' size='md' onClick={handleClose} disabled={isSubmitting}>
              Cancel
            </Button>
            <Button
              id='create-k8s-acc'
              tone='primary'
              size='md'
              loading={isSubmitting}
              // In the tour preview the account name is irrelevant, so keep Next
              // enabled; otherwise require a valid name as usual.
              disabled={isSubmitting || (!isTourDemo && (!k8sNameValue || !!validationError.k8sAccountName))}
              onClick={handleNext}
            >
              Next
            </Button>
          </>
        ) : (
          <Button id='finish-btn' tone='primary' size='md' onClick={handleFinish}>
            Finish
          </Button>
        )
      }
      // DialogContent's own default overflow-y:auto (MUI) would otherwise become the
      // nearest scroll container for the sticky stepper below, and it never actually
      // scrolls — only Modal's outer wrapper Box does. Neutralizing it here lets the
      // stepper stick relative to the Box that really scrolls.
      contentStyles={{ overflowY: 'visible' }}
    >
      {/* Bleeds past DialogContent's own padding (--ds-space-6 horizontal,
          --ds-space-5 top) so the sticky bar spans the full modal width, then
          re-adds padding matching the body content's actual inset below
          (DialogContent's --ds-space-6 plus the body Box's own --ds-space-5)
          so the Stepper lines up with the Card edges instead of sitting
          closer to the modal's edge than everything under it. */}
      <Box
        sx={{
          position: 'sticky',
          top: 0,
          zIndex: 2,
          mx: 'calc(-1 * var(--ds-space-6))',
          mt: 'calc(-1 * var(--ds-space-5))',
          px: 'calc(var(--ds-space-6) + var(--ds-space-5))',
          pt: 'var(--ds-space-4)',
          pb: 'var(--ds-space-3)',
          backgroundColor: 'var(--ds-background-100)',
          borderBottom: `1px solid ${ds.gray[200]}`,
        }}
      >
        <Stepper
          steps={[
            { id: 'account-name', label: 'Set Name & Prerequisites' },
            { id: 'finish-setup', label: 'Finish Setup' },
          ]}
          current={currentStep - 1}
          orientation='horizontal'
        />
      </Box>
      <Box sx={{ px: 'var(--ds-space-5)', pb: 3 }}>
        {currentStep === 1 && (
          <>
            <Card
              variant='outlined'
              elevation='flat'
              size='md'
              sx={{ mt: 3 }}
              header={
                <>
                  <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.brand[600] }}>
                    Account Details
                  </Typography>
                  <Typography variant='body2' sx={{ color: ds.gray[600], fontSize: 'var(--ds-text-small)', mt: 0.5 }}>
                    {`This name will be used to identify your Kubernetes account in ${baseTitle}. It should be unique and descriptive.`}
                  </Typography>
                </>
              }
            >
              <Grid container columnSpacing={4}>
                <Grid item xs={12} md={6}>
                  <Input
                    value={k8sNameValue}
                    size='sm'
                    id='k8sName'
                    required
                    label={
                      <LabelWithInfo
                        text='Account Name'
                        info={
                          <>
                            The label this cluster carries in every picker, alert and report in the product.{' '}
                            <b>Pick something you will recognise in a list</b> — 4–50 characters, letters, digits, space, hyphen and underscore, not
                            starting or ending with a separator. It must be unique across your tenant; a clash comes back as &ldquo;Account name
                            already exists.&rdquo;
                          </>
                        }
                      />
                    }
                    onChange={handleK8sAccountNameChange}
                    error={validationError.k8sAccountName}
                    disabled={isSubmitting}
                  />
                </Grid>
                <Grid item xs={12} md={6}>
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                    <Box
                      component='label'
                      sx={{
                        fontFamily: 'var(--ds-font-display)',
                        fontSize: 'var(--ds-text-small)',
                        color: 'var(--ds-gray-700)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                      }}
                    >
                      <LabelWithInfo text='Account Type' info={accountEnvTooltip()} />
                    </Box>
                    {/* label='' suppresses AccountEnvToggle's own built-in label + info
                        icon (a larger fontSize='small' icon) so it doesn't duplicate the
                        LabelWithInfo row above, which matches Account Name's label style
                        and the app's more common 14px tooltip-icon size. */}
                    <AccountEnvToggle value={accountEnvValue} onChange={setAccountEnvValue} disabled={isSubmitting} label='' />
                  </Box>
                </Grid>
              </Grid>
            </Card>

            <Divider
              sx={{
                my: 3,
                borderStyle: 'dotted',
                borderColor: 'var(--ds-brand-200)',
                borderBottomWidth: '2px',
              }}
            />

            <Card
              id='k8s-prerequisites'
              variant='tinted'
              tone='neutral'
              size='md'
              sx={{ mt: 2 }}
              header={
                <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body)', color: ds.brand[600] }}>
                  Check these prerequisites before starting the installation.
                </Typography>
              }
            >
              <PrereqGroupHeader>Software</PrereqGroupHeader>
              <PrereqItem label='Helm'>{`The ${baseTitle} Agent is deployed using Helm. Ensure that Helm is installed and configured on your system.`}</PrereqItem>
              <PrereqItem label='Kubernetes'>
                The minimum supported Kubernetes version is 1.27. The agent has been tested on this version and newer versions.
              </PrereqItem>
              <PrereqItem label='Linux Kernel'>
                Kubernetes cluster nodes must run at least Linux Kernel version 4.2 or later to ensure eBPF compatibility for the Node Agent.
              </PrereqItem>

              <Divider sx={{ my: 'var(--ds-space-3)', borderColor: ds.gray[200] }} />

              <PrereqGroupHeader>Network</PrereqGroupHeader>
              <PrereqItem label='Docker Registry Access'>
                The installer must be able to access {DEFAULT_IMAGE_REGISTRY} and https://nudgebee.github.io/k8s-agent/ to pull necessary Docker
                images.
              </PrereqItem>
              <PrereqItem label='Collector/Relay Server Connectivity'>
                Agents must be able to connect to Collector/Relay Servers over both Websocket and HTTP. These protocols must be allowed.
              </PrereqItem>
            </Card>
          </>
        )}

        {currentStep === 2 && (
          <>
            {isPreview && (
              <Banner
                tone='info'
                title='Guided tour preview'
                message='This is a demo of the install step — no cluster was created. The key shown below is a sample. Close this window when you’re done exploring.'
              />
            )}
            <Box mt={3}>
              <Grid item xs={12}>
                <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, mb: 0.5 }}>
                  <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.brand[600] }}>
                    Data Collection
                  </Typography>
                  <a
                    href={docsUrl('/docs/installation/agent/#components')}
                    target='_blank'
                    rel='noopener noreferrer'
                    style={{
                      textDecoration: 'none',
                      fontSize: 'var(--ds-text-small)',
                      color: 'var(--ds-blue-500)',
                      marginLeft: 'auto',
                    }}
                  >
                    View component details ›
                  </a>
                </Box>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mb: 1.5 }}>
                  On by default. They read from the nodes directly, so changing either later means re-running the install.
                </Typography>
              </Grid>

              <Box id='included-components' sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: 'var(--ds-space-2)' }}>
                <Box sx={componentCardSx(!nodeAgentEnabled)}>
                  <Box sx={{ width: '100%' }}>
                    <Checkbox
                      size='md'
                      checked={nodeAgentEnabled}
                      onChange={handleNodeAgentToggle}
                      label='Node Agent'
                      description='eBPF network & process monitoring'
                    />
                    <WhyNote>
                      Is it needed? Skip it only when your nodes run a Linux kernel older than 4.2, where eBPF will not load, or when policy forbids
                      privileged DaemonSets. Nothing on the setup card replaces it: no hosted tool can see this data.
                    </WhyNote>
                  </Box>
                </Box>

                <Box sx={componentCardSx(!podMonitorEnabled)}>
                  <Box sx={{ width: '100%' }}>
                    <Checkbox
                      size='md'
                      checked={podMonitorEnabled}
                      onChange={setPodMonitorEnabled}
                      disabled={!nodeAgentEnabled}
                      label='Pod Monitor'
                      description='Scrape pod-level metrics'
                    />
                    <WhyNote>Is it needed? Not if your apps publish no custom metrics, or if a PodMonitor you already manage scrapes them.</WhyNote>
                  </Box>
                </Box>
              </Box>

              <Box
                sx={{
                  mt: 'var(--ds-space-3)',
                  border: `1px solid ${ds.gray[200]}`,
                  borderRadius: 'var(--ds-radius-lg)',
                  overflow: 'hidden',
                }}
              >
                <ButtonBase
                  id='advanced-toggle'
                  component='div'
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 1,
                    p: 'var(--ds-space-2) var(--ds-space-3)',
                    width: '100%',
                    justifyContent: 'flex-start',
                    backgroundColor: ds.background[100],
                    '&:hover': { backgroundColor: 'var(--ds-background-200)' },
                  }}
                  onClick={() => setAdvancedOpen((prev) => !prev)}
                  aria-expanded={advancedOpen}
                  aria-controls='adv-fields-panel'
                >
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      color: ds.gray[400],
                      transition: 'transform 0.2s',
                      lineHeight: 1,
                      transform: advancedOpen ? 'rotate(90deg)' : 'rotate(0deg)',
                    }}
                  >
                    ›
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.brand[600] }}>
                    Advanced
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], ml: 'var(--ds-space-1)' }}>
                    Private registry · air-gapped environments
                  </Typography>
                </ButtonBase>
                {advancedOpen && (
                  <Box
                    id='adv-fields-panel'
                    role='region'
                    sx={{
                      p: 'var(--ds-space-3)',
                      borderTop: `1px solid ${ds.gray[300]}`,
                      backgroundColor: 'var(--ds-background-200)',
                    }}
                  >
                    <Input
                      id='image-registry'
                      value={imageRegistry}
                      size='sm'
                      label='Image Registry'
                      onChange={setImageRegistry}
                      placeholder={`${DEFAULT_IMAGE_REGISTRY} (default)`}
                    />
                    <WhyNote>Change it only for air-gapped or on-prem clusters that mirror our images into a private registry.</WhyNote>
                  </Box>
                )}
              </Box>

              <Divider sx={{ my: 'var(--ds-space-6)', borderColor: ds.gray[300] }} />

              <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1 }}>
                <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.brand[600] }}>
                  Install the Agent
                </Typography>
              </Box>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mb: 'var(--ds-space-2)' }}>
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
                  border: `1px solid ${ds.gray[200]}`,
                  borderRadius: 'var(--ds-radius-lg)',
                  overflow: 'hidden',
                  backgroundColor: ds.background[100],
                }}
              >
                <Box sx={{ p: 'var(--ds-space-3)', borderBottom: `1px solid ${ds.gray[300]}` }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                    <Tabs
                      options={{
                        tabOptions: [
                          { value: 'helm', text: 'Helm' },
                          { value: 'shell', text: 'Shell Script' },
                        ],
                      }}
                      value={activeInstallTab}
                      onChange={setActiveInstallTab}
                      behavior='filter'
                      variant='secondary'
                      ariaLabel='Install method'
                    />
                    {activeInstallTab === 'helm' && (
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
                    )}
                  </Box>
                </Box>

                {/* Shell Script Panel */}
                {activeInstallTab === 'shell' && (
                  <Box id='panel-shell' role='tabpanel' sx={{ p: 'var(--ds-space-3) var(--ds-space-4) var(--ds-space-4)' }}>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], mb: 'var(--ds-space-2)', lineHeight: 1.5 }}>
                      <strong style={{ fontWeight: 'var(--ds-font-weight-medium)' }}>Auto-discovers</strong> existing Prometheus and Loki; installs
                      any missing dependencies. Safe to re-run.
                    </Typography>
                    {!podMonitorEnabled && (
                      <Box sx={{ mb: 1.5 }}>
                        <Banner
                          tone='warning'
                          surface='section'
                          message='Known gap: the shell installer has no Pod Monitor flag, so it installs anyway. Switch to Helm to turn it off.'
                        />
                      </Box>
                    )}
                    <Box
                      sx={{
                        borderRadius: 'var(--ds-radius-lg)',
                        overflow: 'hidden',
                        border: `1px solid color-mix(in srgb, ${ds.background[100]} 4%, transparent)`,
                      }}
                    >
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
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], mb: 'var(--ds-space-2)', lineHeight: 1.5 }}>
                      Its values are explicit and reviewable, so the install can be committed to a GitOps repo and re-applied identically — and it's
                      the only tab where every option above is honoured.
                    </Typography>

                    <Box
                      sx={{
                        borderRadius: 'var(--ds-radius-lg)',
                        overflow: 'hidden',
                        border: `1px solid color-mix(in srgb, ${ds.background[100]} 4%, transparent)`,
                      }}
                    >
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
