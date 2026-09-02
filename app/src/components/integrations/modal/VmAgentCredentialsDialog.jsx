import React, { useState } from 'react';
import { Box, Typography, Grid } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Chip } from '@ui/Chip';
import { Banner } from '@ui/Banner';
import { Link } from '@ui/Link';
import { ds } from 'src/utils/colors';
import CopyButton from '@shared/buttons/CopyButton';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { docsUrl } from '@lib/externalUrls';

const VmAgentCredentialsDialog = ({ open, onClose, accessKey, accessSecret }) => {
  const { relayUrl, signingPublicKey } = useBrandingConfig();
  const [deployMethod, setDeployMethod] = useState('script');

  if (!accessKey || !accessSecret) {
    return null;
  }

  const deployScript =
    deployMethod === 'script'
      ? `curl -fsSL https://registry.nudgebee.com/downloads/forager/latest/install.sh | \\\n  NB_ACCESS_KEY=${accessKey} \\\n  NB_ACCESS_SECRET=${accessSecret} \\\n  NB_RELAY_URL=${relayUrl} \\\n${
          signingPublicKey ? `  NB_SIGNING_PUBLIC_KEY=${signingPublicKey} \\\n` : ''
        }  bash`
      : deployMethod === 'macos'
      ? `curl -fsSL https://registry.nudgebee.com/downloads/forager/latest/install-macos.sh | \\\n  sudo NB_ACCESS_KEY='${accessKey}' \\\n  NB_ACCESS_SECRET='${accessSecret}' \\\n  NB_RELAY_URL='${relayUrl}' \\\n${
          signingPublicKey ? `  NB_SIGNING_PUBLIC_KEY='${signingPublicKey}' \\\n` : ''
        }  bash`
      : deployMethod === 'windows'
      ? `powershell -ExecutionPolicy Bypass -Command "& { $env:NB_ACCESS_KEY='${accessKey}'; $env:NB_ACCESS_SECRET='${accessSecret}'; $env:NB_RELAY_URL='${relayUrl}'${
          signingPublicKey ? `; $env:NB_SIGNING_PUBLIC_KEY='${signingPublicKey}'` : ''
        }; iwr -useb https://registry.nudgebee.com/downloads/forager/latest/install.ps1 | iex }"`
      : deployMethod === 'docker'
      ? `docker run -d --name nudgebee-forager \\\n  -e NB_ACCESS_KEY=${accessKey} \\\n  -e NB_ACCESS_SECRET=${accessSecret} \\\n  -e NB_RELAY_URL=${relayUrl} \\\n${
          signingPublicKey ? `  -e NB_SIGNING_PUBLIC_KEY=${signingPublicKey} \\\n` : ''
        }  -v forager-data:/data \\\n  --restart unless-stopped \\\n  registry.nudgebee.com/nudgebee-forager:latest`
      : deployMethod === 'compose'
      ? `# docker-compose.yaml\nservices:\n  forager:\n    image: registry.nudgebee.com/nudgebee-forager:latest\n    restart: unless-stopped\n    environment:\n      - NB_ACCESS_KEY=${accessKey}\n      - NB_ACCESS_SECRET=${accessSecret}\n      - NB_RELAY_URL=${relayUrl}${
          signingPublicKey ? `\n      - NB_SIGNING_PUBLIC_KEY=${signingPublicKey}` : ''
        }\n      - NB_DATA_DIR=/data\n    volumes:\n      - forager-data:/data\n\nvolumes:\n  forager-data:`
      : deployMethod === 'helm'
      ? `helm install nudgebee-forager \\\n  oci://registry.nudgebee.com/nudgebee-forager-chart \\\n  --set forager.accessKey=${accessKey} \\\n  --set forager.accessSecret=${accessSecret} \\\n  --set forager.relayURL=${relayUrl}${
          signingPublicKey ? ` \\\n  --set forager.signingPublicKey=${signingPublicKey}` : ''
        }`
      : '';

  const credentials = (
    <Grid container sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], mt: ds.space[2], mb: ds.space[2] }}>
      <Banner tone='warning' surface='section' message='Save these credentials now. The secret will not be shown again.' />
      {[
        { label: 'Access Key', value: accessKey },
        { label: 'Access Secret', value: accessSecret },
      ].map(({ label, value }) => (
        <Box key={label}>
          <Typography
            variant='body2'
            sx={{ fontSize: 'var(--ds-text-small)', color: ds.brand[500], mb: ds.space[1], fontWeight: 'var(--ds-font-weight-medium)' }}
          >
            {label}
          </Typography>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[2],
              border: `1px solid ${ds.brand[200]}`,
              borderRadius: ds.radius.lg,
              p: ds.space[4],
            }}
          >
            <Typography sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-body-lg)', wordBreak: 'break-all' }} variant='body1'>
              {value}
            </Typography>
            <CopyButton text={value} size='sm' />
          </Box>
        </Box>
      ))}
      <Box sx={{ mt: ds.space[2] }}>
        <Typography
          variant='body2'
          sx={{ fontSize: 'var(--ds-text-body)', color: ds.brand[500], fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[2] }}
        >
          Deploy the Forager Agent
        </Typography>
        <Box sx={{ display: 'flex', gap: ds.space[1], mb: ds.space[3], flexWrap: 'wrap' }}>
          {[
            { key: 'script', label: 'Linux' },
            { key: 'macos', label: 'macOS' },
            { key: 'windows', label: 'Windows' },
            { key: 'docker', label: 'Docker' },
            { key: 'compose', label: 'Docker Compose' },
            { key: 'helm', label: 'Helm' },
          ].map(({ key, label }) => (
            <Chip key={key} variant='filter' size='sm' selected={deployMethod === key} onClick={() => setDeployMethod(key)}>
              {label}
            </Chip>
          ))}
        </Box>
        <Box sx={{ backgroundColor: 'var(--ds-gray-700)', borderRadius: ds.radius.sm, position: 'relative' }}>
          <Box sx={{ position: 'absolute', top: ds.space[1], right: ds.space[1] }}>
            <CopyButton text={deployScript} />
          </Box>
          <Box sx={{ p: ds.space[4], pr: ds.space.mul(0, 20), maxHeight: ds.space.mul(0, 110), overflowY: 'auto' }}>
            <Typography
              component='pre'
              sx={{
                color: 'var(--ds-brand-200)',
                fontSize: 'var(--ds-text-small)',
                fontFamily: '"Roboto Mono", monospace',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
                m: 0,
                lineHeight: 1.6,
              }}
            >
              {deployMethod === 'script' && (
                <>
                  {'curl -fsSL https://registry.nudgebee.com/downloads/forager/latest/install.sh | \\\n'}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  NB_ACCESS_KEY'}</span>
                  {'='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessKey}</span>
                  {' \\\n'}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  NB_ACCESS_SECRET'}</span>
                  {'='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessSecret}</span>
                  {' \\\n'}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  NB_RELAY_URL'}</span>
                  {'='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{relayUrl}</span>
                  {' \\\n'}
                  {signingPublicKey && (
                    <>
                      <span style={{ color: 'var(--ds-blue-300)' }}>{'  NB_SIGNING_PUBLIC_KEY'}</span>
                      {'='}
                      <span style={{ color: 'var(--ds-red-400)' }}>{signingPublicKey}</span>
                      {' \\\n'}
                    </>
                  )}
                  {'  bash'}
                </>
              )}
              {deployMethod === 'macos' && (
                <>
                  {'curl -fsSL https://registry.nudgebee.com/downloads/forager/latest/install-macos.sh | \\\n'}
                  {'  sudo '}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'NB_ACCESS_KEY'}</span>
                  {"='"}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessKey}</span>
                  {"' \\\n"}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  NB_ACCESS_SECRET'}</span>
                  {"='"}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessSecret}</span>
                  {"' \\\n"}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  NB_RELAY_URL'}</span>
                  {"='"}
                  <span style={{ color: 'var(--ds-red-400)' }}>{relayUrl}</span>
                  {"' \\\n"}
                  {signingPublicKey && (
                    <>
                      <span style={{ color: 'var(--ds-blue-300)' }}>{'  NB_SIGNING_PUBLIC_KEY'}</span>
                      {"='"}
                      <span style={{ color: 'var(--ds-red-400)' }}>{signingPublicKey}</span>
                      {"' \\\n"}
                    </>
                  )}
                  {'  bash'}
                </>
              )}
              {deployMethod === 'windows' && (
                <>
                  {'powershell -ExecutionPolicy Bypass -Command '}
                  <span style={{ color: 'var(--ds-red-400)' }}>{'"& { '}</span>
                  {'\n'}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  $env:NB_ACCESS_KEY'}</span>
                  {"='"}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessKey}</span>
                  {"';\n"}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  $env:NB_ACCESS_SECRET'}</span>
                  {"='"}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessSecret}</span>
                  {"';\n"}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  $env:NB_RELAY_URL'}</span>
                  {"='"}
                  <span style={{ color: 'var(--ds-red-400)' }}>{relayUrl}</span>
                  {"';\n"}
                  {signingPublicKey && (
                    <>
                      <span style={{ color: 'var(--ds-blue-300)' }}>{'  $env:NB_SIGNING_PUBLIC_KEY'}</span>
                      {"='"}
                      <span style={{ color: 'var(--ds-red-400)' }}>{signingPublicKey}</span>
                      {"';\n"}
                    </>
                  )}
                  {'  iwr -useb '}
                  <span style={{ color: 'var(--ds-teal-400)' }}>{'https://registry.nudgebee.com/downloads/forager/latest/install.ps1'}</span>
                  {' | iex'}
                  <span style={{ color: 'var(--ds-red-400)' }}>{' }"'}</span>
                </>
              )}
              {deployMethod === 'docker' && (
                <>
                  {'docker run -d --name nudgebee-forager \\\n'}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  -e '}</span>
                  {'NB_ACCESS_KEY='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessKey}</span>
                  {' \\\n'}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  -e '}</span>
                  {'NB_ACCESS_SECRET='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessSecret}</span>
                  {' \\\n'}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'  -e '}</span>
                  {'NB_RELAY_URL='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{relayUrl}</span>
                  {' \\\n'}
                  {signingPublicKey && (
                    <>
                      <span style={{ color: 'var(--ds-blue-300)' }}>{'  -e '}</span>
                      {'NB_SIGNING_PUBLIC_KEY='}
                      <span style={{ color: 'var(--ds-red-400)' }}>{signingPublicKey}</span>
                      {' \\\n'}
                    </>
                  )}
                  {'  -v forager-data:/data \\\n'}
                  {'  --restart unless-stopped \\\n'}
                  <span style={{ color: 'var(--ds-teal-400)' }}>{'  registry.nudgebee.com/nudgebee-forager:latest'}</span>
                </>
              )}
              {deployMethod === 'compose' && (
                <>
                  <span style={{ color: 'var(--ds-gray-600)' }}>{'# docker-compose.yaml'}</span>
                  {'\n'}
                  <span style={{ color: 'var(--ds-blue-400)' }}>{'services'}</span>
                  {':\n'}
                  {'  '}
                  <span style={{ color: 'var(--ds-blue-400)' }}>{'forager'}</span>
                  {':\n'}
                  {'    image: '}
                  <span style={{ color: 'var(--ds-teal-400)' }}>{'registry.nudgebee.com/nudgebee-forager:latest'}</span>
                  {'\n'}
                  {'    restart: unless-stopped\n'}
                  {'    environment:\n'}
                  {'      - NB_ACCESS_KEY='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessKey}</span>
                  {'\n'}
                  {'      - NB_ACCESS_SECRET='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessSecret}</span>
                  {'\n'}
                  {'      - NB_RELAY_URL='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{relayUrl}</span>
                  {'\n'}
                  {signingPublicKey && (
                    <>
                      {'      - NB_SIGNING_PUBLIC_KEY='}
                      <span style={{ color: 'var(--ds-red-400)' }}>{signingPublicKey}</span>
                      {'\n'}
                    </>
                  )}
                  {'      - NB_DATA_DIR=/data\n'}
                  {'    volumes:\n'}
                  {'      - forager-data:/data\n\n'}
                  <span style={{ color: 'var(--ds-blue-400)' }}>{'volumes'}</span>
                  {':\n'}
                  {'  forager-data:'}
                </>
              )}
              {deployMethod === 'helm' && (
                <>
                  {'helm install nudgebee-forager \\\n'}
                  {'  '}
                  <span style={{ color: 'var(--ds-teal-400)' }}>{'oci://registry.nudgebee.com/nudgebee-forager-chart'}</span>
                  {' \\\n'}
                  {'  --set '}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'forager.accessKey'}</span>
                  {'='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessKey}</span>
                  {' \\\n'}
                  {'  --set '}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'forager.accessSecret'}</span>
                  {'='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{accessSecret}</span>
                  {' \\\n'}
                  {'  --set '}
                  <span style={{ color: 'var(--ds-blue-300)' }}>{'forager.relayURL'}</span>
                  {'='}
                  <span style={{ color: 'var(--ds-red-400)' }}>{relayUrl}</span>
                  {signingPublicKey && (
                    <>
                      {' \\\n'}
                      {'  --set '}
                      <span style={{ color: 'var(--ds-blue-300)' }}>{'forager.signingPublicKey'}</span>
                      {'='}
                      <span style={{ color: 'var(--ds-red-400)' }}>{signingPublicKey}</span>
                    </>
                  )}
                </>
              )}
            </Typography>
          </Box>
        </Box>
        <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mt: ds.space[3] }}>
          Learn more about{' '}
          <Link href={docsUrl('/docs/installation/proxy-agent/')} openInNew secondaryText>
            agent deployment options
          </Link>
        </Typography>
      </Box>
    </Grid>
  );

  return (
    <Modal
      handleClose={onClose}
      title={
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
          <Typography component='h2' variant='h6' fontWeight={600}>
            Proxy Agent Credentials
          </Typography>
        </Box>
      }
      open={open}
      width='md'
      isConfirmRequired={false}
    >
      {credentials}
    </Modal>
  );
};

export default VmAgentCredentialsDialog;
