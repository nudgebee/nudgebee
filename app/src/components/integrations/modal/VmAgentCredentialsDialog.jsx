import React, { useState } from 'react';
import { Box, Typography, Grid, Chip } from '@mui/material';
import { Modal } from '@ui/Modal';
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
      <Box sx={{ backgroundColor: 'var(--ds-amber-100)', border: '1px solid var(--ds-yellow-300)', borderRadius: ds.radius.sm, p: ds.space[3] }}>
        <Typography
          variant='body2'
          sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-amber-500)', fontWeight: 'var(--ds-font-weight-medium)' }}
        >
          Save these credentials now. The secret will not be shown again.
        </Typography>
      </Box>
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
          <Grid
            container
            borderRadius={ds.radius.lg}
            p={ds.space[4]}
            sx={{
              display: 'flex',
              flexDirection: 'row',
              justifyContent: 'space-between',
              border: `1px solid ${ds.brand[200]}`,
            }}
          >
            <Grid item xs={11} sx={{ overflowY: 'auto', maxHeight: ds.space.mul(0, 50), display: 'flex' }}>
              <CopyButton text={value} />
              <Typography sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-body-lg)' }} variant='body1'>
                {value}
              </Typography>
            </Grid>
          </Grid>
        </Box>
      ))}
      <Box sx={{ mt: 1 }}>
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
            <Chip
              key={key}
              label={label}
              size='small'
              onClick={() => setDeployMethod(key)}
              variant={deployMethod === key ? 'filled' : 'outlined'}
              color={deployMethod === key ? 'primary' : 'default'}
              sx={{ fontSize: 'var(--ds-text-small)', cursor: 'pointer' }}
            />
          ))}
        </Box>
        <Box
          sx={{
            backgroundColor: 'var(--ds-gray-700)',
            borderRadius: ds.radius.sm,
            p: ds.space[4],
            position: 'relative',
            maxHeight: ds.space.mul(0, 110),
            overflowY: 'auto',
          }}
        >
          <CopyButton text={deployScript} />
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
        <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mt: ds.space[3] }}>
          Learn more about{' '}
          <a
            style={{ textDecoration: 'none', color: ds.blue[500] }}
            href={docsUrl('/docs/installation/proxy-agent/')}
            target='_blank'
            rel='noopener noreferrer'
          >
            agent deployment options
          </a>
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
