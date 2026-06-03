import React, { type CSSProperties } from 'react';
import { Loadergif } from '@assets'; // Bundled fallback GIF
import SafeIcon from '@components1/common/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';

interface LoaderProps {
  style?: CSSProperties;
}

const Loader: React.FC<LoaderProps> = ({ style }) => {
  const { loaderUrl, loading } = useBrandingConfig();

  const loaderStyle: CSSProperties = {
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'center',
    height: '100vh', // Full viewport height
    width: '100vw', // Full viewport width
    ...style, // Merge with any additional styles passed as props
  };

  // Don't render any GIF until /api/public/app_config resolves — avoids flashing the
  // bundled default before the tenant-configured loader is known.
  if (loading) {
    return <div style={loaderStyle} />;
  }

  // Prefer the runtime config URL when set (per-tenant branding); otherwise
  // fall back to the bundled Loadergif.
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
          width: '150px',
          height: 'auto',
        }}
      />
    </div>
  );
};

export default Loader;
