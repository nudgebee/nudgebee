import React, { type CSSProperties } from 'react';
import Head from 'next/head';
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

  // Brand-neutral CSS spinner. It carries no logo, so it's safe to show before we know the
  // tenant (no Nudgebee-bee leak on white-label). Being pure inline CSS — inline styles plus a
  // @keyframes injected into <head> via next/head — it paints AND animates in the
  // statically-prerendered HTML at FCP, before the JS bundle hydrates. The bundled GIF, by
  // contrast, is a raster image React only injects after the bundle parses, so on a long-loading
  // page it appeared on an otherwise blank screen ~4.7s in and became the LCP element. The
  // `--ds-*` tokens come from the render-blocking critical CSS in _document; the hex values are
  // neutral fallbacks. The keyframes go through next/head (keyed) so they land in <head> as
  // valid HTML and are de-duplicated if more than one Loader mounts.
  const neutralSpinner = (
    <div style={loaderStyle}>
      <Head>
        <style key='nb-loader-spin' dangerouslySetInnerHTML={{ __html: '@keyframes nb-loader-spin{to{transform:rotate(360deg)}}' }} />
      </Head>
      <span
        role='status'
        aria-label='Loading...'
        style={{
          display: 'inline-block',
          boxSizing: 'border-box',
          width: 44,
          height: 44,
          borderRadius: '50%',
          border: '4px solid var(--ds-gray-200, #e5e7eb)',
          borderTopColor: 'var(--ds-brand-500, #6b7280)',
          animation: 'nb-loader-spin 0.8s linear infinite',
        }}
      />
    </div>
  );

  // Until /api/public/app_config resolves we don't yet know the tenant's loader, but we still
  // paint immediately rather than a blank screen. (Previously this returned an empty <div>,
  // which left the page blank until the GIF was injected and made that GIF the LCP element.)
  if (loading) {
    return neutralSpinner;
  }

  // Loader precedence:
  //   1. Explicit per-tenant loaderUrl (e.g. Rackspace's ria-icon) — always wins.
  //   2. White-label client with no explicit loaderUrl — the brand-neutral spinner above
  //      (no per-client config, never leaks the Nudgebee bee).
  //   3. Default (Nudgebee) tenant — the bundled nubi bee GIF.
  if (!loaderUrl && isWhiteLabel) {
    return neutralSpinner;
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
