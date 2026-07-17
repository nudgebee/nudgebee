import React, { type CSSProperties } from 'react';
import CircularProgress from '@mui/material/CircularProgress';
import { Loadergif } from '@assets'; // Bundled fallback GIF
import SafeIcon from '@shared/icons/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { ds } from 'src/utils/colors';

interface LoaderProps {
  style?: CSSProperties;
}

const Loader: React.FC<LoaderProps> = ({ style }) => {
  const { loaderUrl, isWhiteLabel, loading } = useBrandingConfig();

  const loaderStyle: CSSProperties = {
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'center',
    height: '100vh', // Full viewport height
    width: '100vw', // Full viewport width
    ...style, // Merge with any additional styles passed as props
  };

  // Don't render any loader until /api/public/app_config resolves — avoids flashing the
  // bundled default before the tenant-configured loader is known.
  if (loading) {
    return <div style={loaderStyle} />;
  }

  // Loader precedence:
  //   1. Explicit per-tenant loaderUrl (e.g. Rackspace's ria-icon) — always wins.
  //   2. White-label client with no explicit loaderUrl — generic, brand-agnostic CSS
  //      spinner. `color="primary"` picks up the client's theme primary color (set from
  //      theme.palette.primary by useThemeProvider), so it's on-brand with zero per-client
  //      config and never leaks the Nudgebee bee.
  //   3. Default (Nudgebee) tenant — the bundled nubi bee GIF.
  if (!loaderUrl && isWhiteLabel) {
    return (
      <div style={loaderStyle}>
        <CircularProgress size={48} thickness={4} aria-label='Loading...' color='primary' />
      </div>
    );
  }

  const iconSrc = loaderUrl || Loadergif;

  return (
    <div style={loaderStyle}>
      <SafeIcon
        unoptimized={true}
        src={iconSrc}
        alt='Loading...'
        // Intrinsic dims required by next/image when src is a URL string;
        // ignored for the bundled StaticImageData fallback. CSS below keeps
        // the visual size at 150px regardless of source.
        width={150}
        height={116}
        style={{
          width: ds.space.mul(0, 75),
          height: 'auto',
        }}
      />
    </div>
  );
};

export default Loader;
