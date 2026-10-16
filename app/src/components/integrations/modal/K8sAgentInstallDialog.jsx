import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography, Grid } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Chip } from '@ui/Chip';
import { Banner } from '@ui/Banner';
import { Link } from '@ui/Link';
import { ds } from 'src/utils/colors';
import CopyButton from '@shared/buttons/CopyButton';
import { ProgressLinear } from '@ui/ProgressLinear';
import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { docsUrl } from '@lib/externalUrls';
import { toast as snackbar } from '@ui/Toast';
import { AGENT_CONNECTED } from '@components/integrations/pendingAgentInstall';

const AGENT_POLL_MS = 10000;
// Bare-minimum install, same defaults the add-cluster wizard's step 2 starts
// on — this dialog has no advanced-options UI of its own, it just gets a
// forgotten command back in front of the user.
const MINIMAL_INSTALL_SHELL_FLAGS = ' -g true -x true -t true';
const MINIMAL_INSTALL_HELM_VALUES = [
  'enablePrometheusStack=false',
  'opencost.enabled=false',
  'opentelemetry-collector.enabled=false',
  'clickhouse.enabled=false',
];

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
  fontFamily: ds.font.mono,
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

/**
 * Re-shows the install command for an existing K8s account whose agent was
 * never confirmed — reached from that account's own chip in Setup Center
 * (`SetupCenter.tsx`'s `reopenAgentInstall`). The original key is shown once
 * at creation and never stored, so this fetches a fresh one on open via the
 * same `agents_create_token` mutation the Integrations page's VM "Regenerate
 * Token" action already uses, just for `agentType: 'k8s'` instead of 'proxy'
 * — `RegenerateAgentKeys` (api-server) has carried that branch from the
 * start. The agent ROW this reads/writes already exists from account
 * creation, so there is nothing to create here, only new credentials for it.
 */
const K8sAgentInstallDialog = ({ open, onClose, account }) => {
  const { relayUrl, k8sCollectorUrl, signingPublicKey } = useBrandingConfig();
  const [activeTab, setActiveTab] = useState('helm');
  const [authKey, setAuthKey] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  // 'checking' → 'waiting' → 'connected'. No 'shared' phase like the VM
  // dialog's — a K8s account's agent is keyed to this one account already,
  // not shared across a fleet, so there is no prior-agent ambiguity to flag.
  const [agentPhase, setAgentPhase] = useState('checking');

  const accountId = account?.id;

  useEffect(() => {
    if (!open || !accountId) {
      setAuthKey('');
      setAgentPhase('checking');
      return undefined;
    }
    let cancelled = false;
    setIsLoading(true);
    apiAccount
      .generateAgentToken(accountId, 'k8s')
      .then((res) => {
        if (cancelled) return;
        const data = res?.data?.data?.agents_create_token;
        if (data?.access_key && data?.access_secret) {
          setAuthKey(`${data.access_key}:${data.access_secret}`);
        } else {
          snackbar.error('Failed to generate a new install command');
        }
      })
      .catch(() => {
        if (!cancelled) snackbar.error('Failed to generate a new install command');
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, accountId]);

  useEffect(() => {
    if (!open || !accountId || !authKey) {
      setAgentPhase('checking');
      return undefined;
    }
    let cancelled = false;
    let timer;
    setAgentPhase('checking');
    const check = async () => {
      const res = await apiKubernetes1.listK8sAccAgentHealth(accountId, 'k8s');
      if (cancelled) return;
      const agents = res?.data?.data?.agent;
      if (!Array.isArray(agents) || res?.data?.errors?.length) {
        timer = setTimeout(check, AGENT_POLL_MS);
        return;
      }
      if (agents.some((row) => row?.status === AGENT_CONNECTED)) {
        setAgentPhase('connected');
        return;
      }
      setAgentPhase('waiting');
      timer = setTimeout(check, AGENT_POLL_MS);
    };
    check();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [open, accountId, authKey]);

  const shellCommand = useMemo(() => {
    const key = authKey || '<NUDGEBEE_AUTH_KEY>';
    let cmd = `curl -fsSL https://raw.githubusercontent.com/nudgebee/k8s-agent/main/installation.sh -o installation.sh && bash installation.sh -a "${key}"`;
    if (relayUrl) cmd += ` -w "${relayUrl}"`;
    if (k8sCollectorUrl) cmd += ` -c "${k8sCollectorUrl}"`;
    cmd += MINIMAL_INSTALL_SHELL_FLAGS;
    if (signingPublicKey) cmd += ` -S "${signingPublicKey}"`;
    return cmd;
  }, [authKey, relayUrl, k8sCollectorUrl, signingPublicKey]);

  const helmCommand = useMemo(() => {
    const staticCommands = `helm repo add nudgebee-agent https://nudgebee.github.io/k8s-agent/\nhelm repo update`;
    let upgradeCommand = `helm upgrade --install nudgebee-agent nudgebee-agent/nudgebee-agent \\\n  --namespace nudgebee-agent --create-namespace \\\n  --set runner.nudgebee.auth_secret_key="${
      authKey || '<NUDGEBEE_AUTH_KEY>'
    }"`;
    if (relayUrl) upgradeCommand += ` \\\n  --set runner.relay_address="${relayUrl}"`;
    if (k8sCollectorUrl) upgradeCommand += ` \\\n  --set runner.nudgebee.endpoint="${k8sCollectorUrl}"`;
    MINIMAL_INSTALL_HELM_VALUES.forEach((value) => {
      upgradeCommand += ` \\\n  --set ${value}`;
    });
    if (signingPublicKey) {
      upgradeCommand += ` \\\n  --set-string runner.nudgebee.relay_signing_public_key="${signingPublicKey}"`;
    }
    return `${staticCommands}\n\n${upgradeCommand}`;
  }, [authKey, relayUrl, k8sCollectorUrl, signingPublicKey]);

  const activeCommand = activeTab === 'helm' ? helmCommand : shellCommand;

  return (
    <Modal
      handleClose={onClose}
      title={
        <Typography component='h2' variant='h6' fontWeight={ds.weight.semibold}>
          {account?.label ? `Install command — ${account.label}` : 'Install command'}
        </Typography>
      }
      open={open}
      width='md'
      isConfirmRequired={false}
    >
      <Grid container sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], mt: ds.space[2], mb: ds.space[2] }}>
        <Banner
          tone='info'
          surface='section'
          message='A new install key was generated — the original one from when this account was created is never shown again. Run the command below on your cluster.'
        />
        <Box>
          <Box sx={{ display: 'flex', gap: ds.space[1], mb: ds.space[3] }}>
            {[
              { key: 'helm', label: 'Helm' },
              { key: 'shell', label: 'Shell script' },
            ].map(({ key, label }) => (
              <Chip key={key} variant='filter' size='sm' selected={activeTab === key} onClick={() => setActiveTab(key)}>
                {label}
              </Chip>
            ))}
          </Box>
          <Box sx={{ backgroundColor: 'var(--ds-brand-800)', borderRadius: ds.radius.sm, overflow: 'hidden' }}>
            <Box sx={terminalBarSx}>
              <TerminalDots />
            </Box>
            <Box sx={{ p: ds.space[4], pr: ds.space.mul(0, 20), maxHeight: ds.space.mul(0, 110), overflowY: 'auto', position: 'relative' }}>
              <Box sx={{ position: 'absolute', top: ds.space[2], right: ds.space[2] }}>
                <CopyButton text={activeCommand} />
              </Box>
              <Typography component='pre' sx={terminalCodeSx}>
                {isLoading ? 'Generating a new key…' : activeCommand}
              </Typography>
            </Box>
          </Box>
          <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mt: ds.space[3] }}>
            Learn more about{' '}
            <Link href={docsUrl('/docs/installation/agent/installation/')} openInNew secondaryText>
              agent installation
            </Link>
          </Typography>
        </Box>
        {accountId && agentPhase === 'waiting' && (
          <Box data-testid='k8s-agent-install-waiting'>
            <ProgressLinear surface='section' tone='info' aria-label='Waiting for the agent to connect' />
            <Box sx={{ mt: ds.space[2] }}>
              <Banner
                tone='info'
                surface='section'
                title='Waiting for the agent to connect from your cluster'
                message='Run the command above. This window stays open so you can still copy it; close it when you are done.'
              />
            </Box>
          </Box>
        )}
        {accountId && agentPhase === 'connected' && (
          <Box data-testid='k8s-agent-install-connected'>
            <Banner tone='success' surface='section' title='Agent connected' message='The agent reported in from your cluster.' />
          </Box>
        )}
      </Grid>
    </Modal>
  );
};

export default K8sAgentInstallDialog;
