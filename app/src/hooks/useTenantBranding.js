import { useState, useMemo, useEffect } from 'react';
import { getUserSession } from '@lib/auth';

// Hardcoded fallback defaults — used during SSR and before the runtime config fetch resolves.
// These are also exported so existing direct imports continue to work as safe fallbacks.
export const DEFAULT_LOGO = '/branding/default/logo.svg';
export const DEFAULT_FAVICON = '/favicon.ico';
export const DEFAULT_TITLE = 'Nudgebee';
export const DEFAULT_ASSISTANT_NAME = 'nubi';
export const DEFAULT_SIGNIN_IMAGE = '/branding/default/logo.svg';
export const DEFAULT_RELAY_URL = '';
export const DEFAULT_K8S_COLLECTOR_URL = '';
export const DEFAULT_SIGNING_PUBLIC_KEY = '';
export const DEFAULT_NUBI_ICON = '/branding/default/nubi-icon.svg';
export const DEFAULT_NUBI_ICON_LIGHT = '/branding/default/nubi-icon-light.svg';
// Empty by default — Loader.tsx falls back to the bundled `Loadergif` asset when unset.
export const DEFAULT_LOADER_URL = '';

// Module-level cache so the fetch happens at most once per page load.
let _configCache = null;
let _configPromise = null;

function fetchBrandingConfig() {
  if (typeof window === 'undefined') return Promise.resolve(null);
  if (_configCache) return Promise.resolve(_configCache);
  if (!_configPromise) {
    _configPromise = fetch('/api/app/config')
      .then((r) => r.json())
      .then((data) => {
        _configCache = data;
        return data;
      })
      .catch(() => null);
  }
  return _configPromise;
}

// Start fetch eagerly at module load so _configCache is ready before components mount.
if (typeof window !== 'undefined') {
  fetchBrandingConfig();
}

/**
 * Derive a tenant key from the tenant name.
 * e.g., "Acme Corp" → "acme_corp"
 */
export const getTenantKey = (tenantName) => {
  return (tenantName || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/(?:^_)|(?:_$)/g, '');
};

/**
 * Lightweight hook for pages that only need the four branding defaults
 * (logo, favicon, title, assistantName) without the full tenant/partner logic.
 * Useful for auth pages (signin, signup, etc.) that render before any session exists.
 */
export const useBrandingConfig = () => {
  const defaults = {
    logoUrl: DEFAULT_LOGO,
    faviconUrl: DEFAULT_FAVICON,
    title: DEFAULT_TITLE,
    assistantName: DEFAULT_ASSISTANT_NAME,
    nubiIconUrl: DEFAULT_NUBI_ICON,
    nubiIconLightUrl: DEFAULT_NUBI_ICON_LIGHT,
    loaderUrl: DEFAULT_LOADER_URL,
    signinImageUrl: DEFAULT_SIGNIN_IMAGE,
    signinLeftImageUrl: '',
    relayUrl: DEFAULT_RELAY_URL,
    k8sCollectorUrl: DEFAULT_K8S_COLLECTOR_URL,
    signingPublicKey: DEFAULT_SIGNING_PUBLIC_KEY,
  };

  // Always start with null/loading on both server and client to avoid hydration mismatch.
  // The useEffect will pick up the cached value immediately on the client.
  const [config, setConfig] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (_configCache) {
      setConfig(_configCache);
      setLoading(false);
      return;
    }
    fetchBrandingConfig().then((data) => {
      setConfig(data || defaults);
      setLoading(false);
    });
  }, []);

  return { ...(config || defaults), loading };
};

/**
 * Non-hook getter for nubi icon URLs.
 * Reads from the eagerly-fetched config cache, falling back to defaults.
 * Safe to call from plain functions (e.g. getIcon) after initial page load.
 */
export const getNubiIconUrl = () => _configCache?.nubiIconUrl || DEFAULT_NUBI_ICON;
export const getNubiIconLightUrl = () => _configCache?.nubiIconLightUrl || DEFAULT_NUBI_ICON_LIGHT;
export const getLoaderUrl = () => _configCache?.loaderUrl || DEFAULT_LOADER_URL;
export const getAssistantName = () => _configCache?.assistantName || DEFAULT_ASSISTANT_NAME;
export const getBrandTitle = () => _configCache?.title || DEFAULT_TITLE;

/**
 * Resolve a branding asset URL.
 * Checks for a direct URL override in the branding config (e.g. "helpbeeIconUrl"),
 * falls back to /branding/default/{filename}.
 * Partners set explicit URLs per asset in their theme.json, same as logoUrl/nubiIconUrl.
 */
const BRANDING_ASSETS = {
  helpbeeIcon: { configKey: 'helpbeeIconUrl', defaultFile: 'helpbee-icon.svg' },
  troubleshootBee: { configKey: 'troubleshootBeeUrl', defaultFile: 'troubleshoot-bee.svg' },
  optimizeBee: { configKey: 'optimizeBeeUrl', defaultFile: 'optimize-bee.svg' },
  k8sBee: { configKey: 'k8sBeeUrl', defaultFile: 'k8s-bee.svg' },
  newUserBee: { configKey: 'newUserBeeUrl', defaultFile: 'new-user-bee.svg' },
};

export const getBrandingAsset = (key) => {
  const asset = BRANDING_ASSETS[key];
  const defaultSrc = `/branding/default/${asset?.defaultFile || key}`;
  const customSrc = asset && _configCache?.[asset.configKey];
  if (customSrc) return { src: customSrc, fallbackSrc: defaultSrc };
  return defaultSrc;
};

/**
 * Hook that provides tenant branding from the config API (/api/app/config).
 * All branding is driven by TENANT_BRANDING_FILE (falls back to branding/default/theme.json).
 *
 * Returns:
 *   - baseTitle: resolved page/app title
 *   - assistantName: AI chatbot display name (default "nubi")
 *   - logoUrl: resolved logo URL
 *   - faviconUrl: resolved favicon URL
 *   - tenantKey: tenant name-derived key
 *   - isDefaultTenant: true if tenant is nudgebee/default
 */
export const useTenantBranding = () => {
  const session = getUserSession();
  const tenantName = session?.tenant?.tenant?.name || '';
  const tenantKey = useMemo(() => getTenantKey(tenantName), [tenantName]);
  const isDefaultTenant = !tenantKey || tenantKey === 'nudgebee';

  const brandingConfig = useBrandingConfig();

  return {
    baseTitle: brandingConfig.title,
    assistantName: brandingConfig.assistantName,
    logoUrl: brandingConfig.logoUrl,
    faviconUrl: brandingConfig.faviconUrl,
    nubiIconUrl: brandingConfig.nubiIconUrl,
    nubiIconLightUrl: brandingConfig.nubiIconLightUrl,
    tenantKey,
    isDefaultTenant,
    loading: brandingConfig.loading,
    theme: brandingConfig.theme || null,
    colorTokens: brandingConfig.colorTokens || null,
  };
};
