import React, { type CSSProperties } from 'react';
import Head from 'next/head';
import SafeIcon from '@shared/icons/SafeIcon';
import NubiAnimation from '@shared/NubiAnimation';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { ds } from 'src/utils/colors';

interface LoaderProps {
  style?: CSSProperties;
}

const Loader: React.FC<LoaderProps> = ({ style }) => {
  const { loaderUrl, isWhiteLabel, loading } = useBrandingConfig();

  // Absolute overlay centered in the loader's nearest positioned ancestor.
  // For in-app loaders that's the PageLayout content region (position: relative),
  // so the loader fills the CONTENT area — not the whole viewport — and never
  // overflows into horizontal/vertical scrollbars the way `100vw`/`100vh` did.
  // For pre-shell loaders (auth/account guards) no ancestor is positioned, so it
  // falls back to the viewport and centers full-screen. Being out of flow, it
  // stays put even as sibling chrome (header, tab bars) hydrates in above it,
  // instead of being pushed down. It intentionally blocks pointer events over the
  // area it covers (the default of a loading overlay); a caller that needs the
  // underlying UI to stay interactive can pass `style={{ pointerEvents: 'none' }}`.
  const loaderStyle: CSSProperties = {
    position: 'absolute',
    inset: 0,
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'center',
    ...style, // Merge with any additional styles passed as props
  };

  // Brand-neutral CSS spinner. It carries no logo, so it's safe to show before we know the
  // tenant (no Nudgebee-bee leak on white-label). Being pure inline CSS — inline styles plus a
  // @keyframes injected into <head> via next/head — it paints AND animates in the
  // statically-prerendered HTML at FCP, before the JS bundle hydrates. The branded loaders
  // below, by contrast, only render after the bundle parses (historically the bundled GIF
  // appeared on an otherwise blank screen ~4.7s in and became the LCP element). The
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
  //   3. Default (Nudgebee) tenant — the flying-Nubi mascot animation (CSS/SVG,
  //      replaced the bundled Loader.gif).
  if (!loaderUrl && isWhiteLabel) {
    return neutralSpinner;
  }

  if (!loaderUrl) {
    return (
      <div style={loaderStyle} role='status'>
        <NubiAnimation state='flying' size={ds.space.mul(0, 75)} ariaLabel='Loading...' />
      </div>
    );
  }

  return (
    <div style={loaderStyle} role='status'>
      <SafeIcon
        unoptimized={true}
        src={loaderUrl}
        alt='Loading...'
        // Intrinsic dims required by next/image when src is a URL string.
        // CSS below keeps the visual size at 150px regardless of source.
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
