import { useState } from 'react';
import PropTypes from 'prop-types';
import { Box, IconButton, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { HelpOutlineDarkIcon } from '@assets';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import { useTour } from '@components/common/tour';
import { ds } from 'src/utils/colors';

// Canonical route to the Kubernetes connect page (renders K8sIntegrationTile,
// which hosts #add-k8s-account — the connect-cluster tour's first anchor).
const K8S_CONNECT_ROUTE = '/accounts/account-form?cloudProvider=K8S';

/**
 * "How to connect a cluster" help affordance for the home empty state.
 *
 * Hybrid, brand-correct by design:
 *   - Default (all tenants): launches the live interactive walkthrough. Because
 *     it drives the already-themed UI, it matches every tenant's branding with
 *     zero per-tenant assets.
 *   - Override: if a tenant sets `connectClusterGifUrl` in their theme.json, the
 *     popup shows their own recording instead (resolved via getBrandingAsset,
 *     same mechanism as the logo) — never a hardcoded GIF.
 */
const ConnectClusterHelp = ({ size = 18 }) => {
  const [open, setOpen] = useState(false);
  const router = useRouter();
  const { start } = useTour();

  // getBrandingAsset returns an object only when the tenant set the configKey;
  // the default (no override) returns a plain string, which we treat as "no GIF"
  // and fall back to the interactive walkthrough.
  const asset = getBrandingAsset('connectClusterGif');
  const brandedGif = typeof asset === 'object' && asset?.src ? asset : null;

  const goConnect = () => {
    setOpen(false);
    router.push(K8S_CONNECT_ROUTE);
  };

  const showWalkthrough = () => {
    setOpen(false);
    // Navigate to the connect page, then start the tour. The tour's first step
    // waits for #add-k8s-account to mount after navigation (TourProvider is
    // global, so start() survives the route change).
    router.push(K8S_CONNECT_ROUTE).then(() => start('connect-cluster'));
  };

  return (
    <>
      <IconButton id='home-connect-cluster-help' aria-label='How to connect a cluster' size='small' onClick={() => setOpen(true)} sx={{ p: 0.5 }}>
        <SafeIcon src={HelpOutlineDarkIcon} alt='' width={size} height={size} />
      </IconButton>

      <Modal open={open} handleClose={() => setOpen(false)} title='How to connect a cluster' width='sm'>
        <Box sx={{ px: 'var(--ds-space-5)', pb: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          {brandedGif ? (
            <Box
              component='img'
              src={brandedGif.src}
              alt='How to connect a cluster'
              onError={(e) => {
                // Fall back to the tenant's default branding asset if their
                // custom GIF URL fails to load.
                if (brandedGif.fallbackSrc && e.currentTarget.src !== brandedGif.fallbackSrc) {
                  e.currentTarget.src = brandedGif.fallbackSrc;
                }
              }}
              sx={{ width: '100%', borderRadius: 'var(--ds-radius-lg)', border: `1px solid ${ds.gray[200]}` }}
            />
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[600], lineHeight: 1.5 }}>
              We’ll walk you through it live — name your cluster, pick the environment, then copy the one-line command to run against your cluster.
              The agent connects shortly after.
            </Typography>
          )}

          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-2)' }}>
            {!brandedGif && (
              <Button id='home-connect-cluster-walkthrough' tone='secondary' size='md' onClick={showWalkthrough}>
                Show me how
              </Button>
            )}
            <Button id='home-connect-cluster-go' tone='primary' size='md' onClick={goConnect}>
              Connect a cluster
            </Button>
          </Box>
        </Box>
      </Modal>
    </>
  );
};

ConnectClusterHelp.propTypes = {
  size: PropTypes.number,
};

export default ConnectClusterHelp;
