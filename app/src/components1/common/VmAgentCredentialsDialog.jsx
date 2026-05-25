import React, { useState } from 'react';
import { Box, Typography, Grid, Chip } from '@mui/material';
import NDialog from './modal/NDialog';
import { colors } from 'src/utils/colors';
import CopyableText from './CopyableText';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { docsUrl } from '@lib/externalUrls';

const VmAgentCredentialsDialog = ({ open, onClose, accessKey, accessSecret }) => {
  const { relayUrl, signingPublicKey } = useBrandingConfig();
  const [deployMethod, setDeployMethod] = useState('script');

  if (!accessKey || !accessSecret) {
    return null;
  }

  const credentials = (
    <Grid container sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 1, mb: 1 }}>
      <Box sx={{ backgroundColor: '#FFF8E1', border: '1px solid #FFE082', borderRadius: 1, p: 1.5 }}>
        <Typography variant='body2' sx={{ fontSize: '13px', color: '#F57F17', fontWeight: 500 }}>
          Save these credentials now. The secret will not be shown again.
        </Typography>
      </Box>
      {[
        { label: 'Access Key', value: accessKey },
        { label: 'Access Secret', value: accessSecret },
      ].map(({ label, value }) => (
        <Box key={label}>
          <Typography variant='body2' sx={{ fontSize: '12px', color: colors.text.secondary, mb: 0.5, fontWeight: 500 }}>
            {label}
          </Typography>
          <Grid
            container
            borderRadius={2}
            p={2}
            sx={{
              display: 'flex',
              flexDirection: 'row',
              justifyContent: 'space-between',
              border: `1px solid ${colors.border.secondary}`,
            }}
          >
            <Grid item xs={11} sx={{ overflowY: 'auto', maxHeight: '100px', display: 'flex' }}>
              <CopyableText copyableText={value} />
              <Typography sx={{ color: colors.text.secondary, fontSize: '14px' }} variant='body1'>
                {value}
              </Typography>
            </Grid>
          </Grid>
        </Box>
      ))}
      <Box sx={{ mt: 1 }}>
        <Typography variant='body2' sx={{ fontSize: '13px', color: colors.text.secondary, fontWeight: 600, mb: 1 }}>
          Deploy the Forager Agent
        </Typography>
        <Box sx={{ display: 'flex', gap: 0.5, mb: 1.5, flexWrap: 'wrap' }}>
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
              sx={{ fontSize: '12px', cursor: 'pointer' }}
            />
          ))}
        </Box>
        <Box
          sx={{
            backgroundColor: '#1E1E1E',
            borderRadius: 1,
            p: 2,
            position: 'relative',
            maxHeight: '220px',
            overflowY: 'auto',
          }}
        >
          <CopyableText
            copyableText={
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
                : ''
            }
          />
          <Typography
            component='pre'
            sx={{
              color: '#D4D4D4',
              fontSize: '12px',
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
                <span style={{ color: '#9CDCFE' }}>{'  NB_ACCESS_KEY'}</span>
                {'='}
                <span style={{ color: '#CE9178' }}>{accessKey}</span>
                {' \\\n'}
                <span style={{ color: '#9CDCFE' }}>{'  NB_ACCESS_SECRET'}</span>
                {'='}
                <span style={{ color: '#CE9178' }}>{accessSecret}</span>
                {' \\\n'}
                <span style={{ color: '#9CDCFE' }}>{'  NB_RELAY_URL'}</span>
                {'='}
                <span style={{ color: '#CE9178' }}>{relayUrl}</span>
                {' \\\n'}
                {signingPublicKey && (
                  <>
                    <span style={{ color: '#9CDCFE' }}>{'  NB_SIGNING_PUBLIC_KEY'}</span>
                    {'='}
                    <span style={{ color: '#CE9178' }}>{signingPublicKey}</span>
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
                <span style={{ color: '#9CDCFE' }}>{'NB_ACCESS_KEY'}</span>
                {"='"}
                <span style={{ color: '#CE9178' }}>{accessKey}</span>
                {"' \\\n"}
                <span style={{ color: '#9CDCFE' }}>{'  NB_ACCESS_SECRET'}</span>
                {"='"}
                <span style={{ color: '#CE9178' }}>{accessSecret}</span>
                {"' \\\n"}
                <span style={{ color: '#9CDCFE' }}>{'  NB_RELAY_URL'}</span>
                {"='"}
                <span style={{ color: '#CE9178' }}>{relayUrl}</span>
                {"' \\\n"}
                {signingPublicKey && (
                  <>
                    <span style={{ color: '#9CDCFE' }}>{'  NB_SIGNING_PUBLIC_KEY'}</span>
                    {"='"}
                    <span style={{ color: '#CE9178' }}>{signingPublicKey}</span>
                    {"' \\\n"}
                  </>
                )}
                {'  bash'}
              </>
            )}
            {deployMethod === 'windows' && (
              <>
                {'powershell -ExecutionPolicy Bypass -Command '}
                <span style={{ color: '#CE9178' }}>{'"& { '}</span>
                {'\n'}
                <span style={{ color: '#9CDCFE' }}>{'  $env:NB_ACCESS_KEY'}</span>
                {"='"}
                <span style={{ color: '#CE9178' }}>{accessKey}</span>
                {"';\n"}
                <span style={{ color: '#9CDCFE' }}>{'  $env:NB_ACCESS_SECRET'}</span>
                {"='"}
                <span style={{ color: '#CE9178' }}>{accessSecret}</span>
                {"';\n"}
                <span style={{ color: '#9CDCFE' }}>{'  $env:NB_RELAY_URL'}</span>
                {"='"}
                <span style={{ color: '#CE9178' }}>{relayUrl}</span>
                {"';\n"}
                {signingPublicKey && (
                  <>
                    <span style={{ color: '#9CDCFE' }}>{'  $env:NB_SIGNING_PUBLIC_KEY'}</span>
                    {"='"}
                    <span style={{ color: '#CE9178' }}>{signingPublicKey}</span>
                    {"';\n"}
                  </>
                )}
                {'  iwr -useb '}
                <span style={{ color: '#4EC9B0' }}>{'https://registry.nudgebee.com/downloads/forager/latest/install.ps1'}</span>
                {' | iex'}
                <span style={{ color: '#CE9178' }}>{' }"'}</span>
              </>
            )}
            {deployMethod === 'docker' && (
              <>
                {'docker run -d --name nudgebee-forager \\\n'}
                <span style={{ color: '#9CDCFE' }}>{'  -e '}</span>
                {'NB_ACCESS_KEY='}
                <span style={{ color: '#CE9178' }}>{accessKey}</span>
                {' \\\n'}
                <span style={{ color: '#9CDCFE' }}>{'  -e '}</span>
                {'NB_ACCESS_SECRET='}
                <span style={{ color: '#CE9178' }}>{accessSecret}</span>
                {' \\\n'}
                <span style={{ color: '#9CDCFE' }}>{'  -e '}</span>
                {'NB_RELAY_URL='}
                <span style={{ color: '#CE9178' }}>{relayUrl}</span>
                {' \\\n'}
                {signingPublicKey && (
                  <>
                    <span style={{ color: '#9CDCFE' }}>{'  -e '}</span>
                    {'NB_SIGNING_PUBLIC_KEY='}
                    <span style={{ color: '#CE9178' }}>{signingPublicKey}</span>
                    {' \\\n'}
                  </>
                )}
                {'  -v forager-data:/data \\\n'}
                {'  --restart unless-stopped \\\n'}
                <span style={{ color: '#4EC9B0' }}>{'  registry.nudgebee.com/nudgebee-forager:latest'}</span>
              </>
            )}
            {deployMethod === 'compose' && (
              <>
                <span style={{ color: '#6A9955' }}>{'# docker-compose.yaml'}</span>
                {'\n'}
                <span style={{ color: '#569CD6' }}>{'services'}</span>
                {':\n'}
                {'  '}
                <span style={{ color: '#569CD6' }}>{'forager'}</span>
                {':\n'}
                {'    image: '}
                <span style={{ color: '#4EC9B0' }}>{'registry.nudgebee.com/nudgebee-forager:latest'}</span>
                {'\n'}
                {'    restart: unless-stopped\n'}
                {'    environment:\n'}
                {'      - NB_ACCESS_KEY='}
                <span style={{ color: '#CE9178' }}>{accessKey}</span>
                {'\n'}
                {'      - NB_ACCESS_SECRET='}
                <span style={{ color: '#CE9178' }}>{accessSecret}</span>
                {'\n'}
                {'      - NB_RELAY_URL='}
                <span style={{ color: '#CE9178' }}>{relayUrl}</span>
                {'\n'}
                {signingPublicKey && (
                  <>
                    {'      - NB_SIGNING_PUBLIC_KEY='}
                    <span style={{ color: '#CE9178' }}>{signingPublicKey}</span>
                    {'\n'}
                  </>
                )}
                {'      - NB_DATA_DIR=/data\n'}
                {'    volumes:\n'}
                {'      - forager-data:/data\n\n'}
                <span style={{ color: '#569CD6' }}>{'volumes'}</span>
                {':\n'}
                {'  forager-data:'}
              </>
            )}
            {deployMethod === 'helm' && (
              <>
                {'helm install nudgebee-forager \\\n'}
                {'  '}
                <span style={{ color: '#4EC9B0' }}>{'oci://registry.nudgebee.com/nudgebee-forager-chart'}</span>
                {' \\\n'}
                {'  --set '}
                <span style={{ color: '#9CDCFE' }}>{'forager.accessKey'}</span>
                {'='}
                <span style={{ color: '#CE9178' }}>{accessKey}</span>
                {' \\\n'}
                {'  --set '}
                <span style={{ color: '#9CDCFE' }}>{'forager.accessSecret'}</span>
                {'='}
                <span style={{ color: '#CE9178' }}>{accessSecret}</span>
                {' \\\n'}
                {'  --set '}
                <span style={{ color: '#9CDCFE' }}>{'forager.relayURL'}</span>
                {'='}
                <span style={{ color: '#CE9178' }}>{relayUrl}</span>
                {signingPublicKey && (
                  <>
                    {' \\\n'}
                    {'  --set '}
                    <span style={{ color: '#9CDCFE' }}>{'forager.signingPublicKey'}</span>
                    {'='}
                    <span style={{ color: '#CE9178' }}>{signingPublicKey}</span>
                  </>
                )}
              </>
            )}
          </Typography>
        </Box>
        <Typography variant='body2' sx={{ fontSize: '12px', color: colors.text.tertiary, mt: 1.5 }}>
          Learn more about{' '}
          <a
            style={{ textDecoration: 'none', color: colors.text.primaryDark }}
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
    <NDialog
      handleClose={onClose}
      dialogTitle={
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px' }}>
          <Typography component='h2' variant='h6' fontWeight={600}>
            Proxy Agent Credentials
          </Typography>
        </Box>
      }
      open={open}
      dialogContent={credentials}
      additionalComponent={undefined}
      isSubmitRequired={false}
    />
  );
};

export default VmAgentCredentialsDialog;
