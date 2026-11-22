import { useMemo, useSyncExternalStore } from 'react';
import { useSession } from 'next-auth/react';

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
// Bolder circle-badge variant of the nubi icon — reads better at small sizes
// (e.g. 16px row-action buttons in listing tables).
export const DEFAULT_NUBI_ICON_CIRCLE = '/branding/default/nubi-icon-circle.svg';
// Empty by default — Loader.tsx falls back to the animated flying-Nubi mascot (NubiAnimation) when unset.
export const DEFAULT_LOADER_URL = '';

// Module-level cache so the fetch happens at most once per page load. It doubles
// as an external store: hooks read it *synchronously during render* via
// useSyncExternalStore, so a component mounting after the fetch has resolved (any
// modal, tab or table opened post-hydration) sees the real branding on its very
// first render. Resolving it in an effect instead used to hand those components
// the Nudgebee defaults for one render — long enough for anything that snapshots
// a branded string into state to latch it permanently.
let _configCache = null;
let _configPromise = null;
const _configListeners = new Set();
// Resolved-but-empty marker. A failed config fetch must still clear `loading`,
// or every Loader / favicon gated on it would wait forever; the per-field merge
// below then falls back to the Nudgebee defaults, as it did before.
const EMPTY_CONFIG = {};

const subscribeToBrandingConfig = (onStoreChange) => {
  _configListeners.add(onStoreChange);
  return () => _configListeners.delete(onStoreChange);
};
// Must return a stable reference — _configCache is replaced once, never mutated.
const getBrandingSnapshot = () => _configCache;
// SSR and hydration both render the unbranded defaults, so the server HTML and
// the first client render agree; React re-renders with the real value right after.
const getBrandingServerSnapshot = () => null;

function fetchBrandingConfig() {
  if (typeof window === 'undefined') return Promise.resolve(null);
  if (_configCache) return Promise.resolve(_configCache);
  if (!_configPromise) {
    _configPromise = fetch('/api/public/app_config')
      .then((r) => r.json())
      .then((data) => {
        _configCache = data || EMPTY_CONFIG;
        _configListeners.forEach((notify) => notify());
        return data;
      })
      .catch(() => {
        _configCache = EMPTY_CONFIG;
        _configListeners.forEach((notify) => notify());
        return null;
      });
  }
  return _configPromise;
}

// Start fetch eagerly at module load so _configCache is ready before components mount.
if (typeof window !== 'undefined') {
  fetchBrandingConfig();
}

// Resolves once the branding config has loaded (or failed), so non-React code
// that bakes the brand into stored data can read getBrandTitle() without
// latching the default.
export const whenBrandingReady = () => fetchBrandingConfig();

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

// Default (Nudgebee) tenant — branding fallbacks use Nudgebee assets (nubi bee, etc.).
const BRANDING_DEFAULTS = {
  isWhiteLabel: false,
  logoUrl: DEFAULT_LOGO,
  faviconUrl: DEFAULT_FAVICON,
  title: DEFAULT_TITLE,
  assistantName: DEFAULT_ASSISTANT_NAME,
  nubiIconUrl: DEFAULT_NUBI_ICON,
  nubiIconLightUrl: DEFAULT_NUBI_ICON_LIGHT,
  loaderUrl: DEFAULT_LOADER_URL,
  // Empty (not DEFAULT_SIGNIN_IMAGE) to mirror app_config's `signinImageUrl ?? ''`
  // for the default tenant: signin/signup then fall back to the bundled
  // NBIconSignIn asset rather than the page logo.
  signinImageUrl: '',
  signinLeftImageUrl: '',
  carouselSlides: null,
  theme: null,
  colorTokens: null,
  fontRemap: null,
  relayUrl: DEFAULT_RELAY_URL,
  k8sCollectorUrl: DEFAULT_K8S_COLLECTOR_URL,
  signingPublicKey: DEFAULT_SIGNING_PUBLIC_KEY,
};

// The value branding resolves to BEFORE /api/public/app_config answers.
//
// Deliberately empty rather than BRANDING_DEFAULTS. That window is a real,
// visible server round trip, and every field here reaches something a user
// reads within it: the browser tab, the header logo, the favicon, the mascots.
// Falling back to the Nudgebee defaults meant a partner deployment painted the
// house brand for those frames and then repainted — the "Nudgebee, then
// Acme" flash. Empty degrades to "not there yet" instead: consumers gated
// on `loading` render nothing, and SafeIcon returns null for an empty src.
//
// This is the loading state ONLY. Once the config resolves, BRANDING_DEFAULTS
// comes back as the per-field fallback for anything the response omits.
const BRANDING_UNRESOLVED = {
  isWhiteLabel: false,
  logoUrl: '',
  faviconUrl: '',
  title: '',
  assistantName: '',
  nubiIconUrl: '',
  nubiIconLightUrl: '',
  loaderUrl: '',
  signinImageUrl: '',
  signinLeftImageUrl: '',
  carouselSlides: null,
  theme: null,
  colorTokens: null,
  fontRemap: null,
  relayUrl: DEFAULT_RELAY_URL,
  k8sCollectorUrl: DEFAULT_K8S_COLLECTOR_URL,
  signingPublicKey: DEFAULT_SIGNING_PUBLIC_KEY,
};

/**
 * Lightweight hook for pages that only need the four branding defaults
 * (logo, favicon, title, assistantName) without the full tenant/partner logic.
 * Useful for auth pages (signin, signup, etc.) that render before any session exists.
 */
export const useBrandingConfig = () => {
  const config = useSyncExternalStore(subscribeToBrandingConfig, getBrandingSnapshot, getBrandingServerSnapshot);

  // Two states, and the split between them is the point:
  //
  //   not resolved yet (config === null) -> BRANDING_UNRESOLVED, i.e. empty.
  //     See that constant for why this must not be the Nudgebee defaults.
  //
  //   resolved -> merge defaults UNDER the response so any field it omits falls
  //     back to its Nudgebee default per-field. The branding apparatus is
  //     EE-only: in OSS (and EE without a custom branding file)
  //     /api/public/app_config returns only the non-branding runtime fields, so
  //     the branding fields must default here.
  return useMemo(() => (config === null ? { ...BRANDING_UNRESOLVED, loading: true } : { ...BRANDING_DEFAULTS, ...config, loading: false }), [config]);
};

/**
 * Runtime feature flag for background watches. Driven by LLM_SERVER_WATCH_ENABLED
 * on llm-server and surfaced to the client via /api/public/app_config
 * (`watchEnabled`). The chat UI polls the watch-list endpoint only when this is
 * true — llm-server unmounts the /v1/watches route when the flag is off, so
 * gating here avoids the otherwise-guaranteed 404 per conversation.
 *
 * Reads the same store as useBrandingConfig: false on the server and during
 * hydration, then the real value as soon as the config resolves.
 */
export const useWatchFeatureEnabled = () => {
  const config = useSyncExternalStore(subscribeToBrandingConfig, getBrandingSnapshot, getBrandingServerSnapshot);
  return !!config?.watchEnabled;
};

/**
 * Runtime feature flag for PWA activation.
 * Reads pwaEnabled from /api/public/app_config.
 */
export const usePwaFeatureEnabled = () => {
  const config = useSyncExternalStore(subscribeToBrandingConfig, getBrandingSnapshot, getBrandingServerSnapshot);
  return !!config?.pwaEnabled;
};

export const usePwaThemeColor = () => {
  const config = useSyncExternalStore(subscribeToBrandingConfig, getBrandingSnapshot, getBrandingServerSnapshot);
  return config?.pwaThemeColor || '#1B2D4A';
};

/**
 * Non-hook getter for nubi icon URLs.
 * Reads from the eagerly-fetched config cache, falling back to defaults.
 * Safe to call from plain functions (e.g. getIcon) after initial page load.
 */
// Same rule as useBrandingConfig above: before the config resolves these answer
// EMPTY, not with a Nudgebee default. `_configCache` is the resolved/unresolved
// signal — the `?.` chains these used to have could not tell "no config yet"
// from "config resolved without this field", so both took the house-brand
// branch and a partner saw Nudgebee assets until the fetch landed.
export const getNubiIconUrl = () => (_configCache ? _configCache.nubiIconUrl || DEFAULT_NUBI_ICON : '');
export const getNubiIconLightUrl = () => (_configCache ? _configCache.nubiIconLightUrl || DEFAULT_NUBI_ICON_LIGHT : '');
// White-label tenants without an explicit circle variant fall back to their
// regular nubi icon rather than the Nudgebee default.
export const getNubiIconCircleUrl = () =>
  _configCache ? _configCache.nubiIconCircleUrl || _configCache.nubiIconUrl || DEFAULT_NUBI_ICON_CIRCLE : '';
export const getLoaderUrl = () => (_configCache ? _configCache.loaderUrl || DEFAULT_LOADER_URL : '');
export const getAssistantName = () => (_configCache ? _configCache.assistantName || DEFAULT_ASSISTANT_NAME : '');
export const getBrandTitle = () => (_configCache ? _configCache.title || DEFAULT_TITLE : '');

// Sentence-cased assistant name, for copy that opens a sentence or names a UI
// surface ("Ask Nubi > Memory"). The configured name is lowercase by default,
// so callers must not hand-capitalise it — that is what hardcodes the house
// assistant back in.
export const toAssistantLabel = (name) => (name ? name.charAt(0).toUpperCase() + name.slice(1) : name);
export const getAssistantLabel = () => toAssistantLabel(getAssistantName());

// Fill `{brand}` / `{assistant}` / `{Assistant}` placeholders in STATIC copy, at
// call time.
//
// Static string tables (agent catalogue copy, dashboard widget and entity-table
// descriptions, tour steps, …) are module-level constants. A brand name
// interpolated where they are DECLARED evaluates at import, before
// /api/public/app_config resolves, so a white-label tenant latches the house
// brand for the life of the tab — the same defect class as the permanent
// `NUDGEBEE SYSTEM AGENT` badge. Keep the placeholder in the data and call this
// from whatever accessor the render path already goes through.
//
// `{Assistant}` is the sentence-cased form, for copy that opens a sentence.
export const fillBrandTokens = (text) => {
  if (typeof text !== 'string') return text;
  const assistant = getAssistantName();
  return text
    .replace(/\{brand\}/g, getBrandTitle())
    .replace(/\{Assistant\}/g, toAssistantLabel(assistant))
    .replace(/\{assistant\}/g, assistant);
};

// True on white-labeled (partner) hosts — set server-side when the host's branding row renames or re-logos the product.
// Use this (not the session tenant name) to gate Nudgebee-specific assets like the mascots.
export const getIsWhiteLabel = () => !!_configCache?.isWhiteLabel;

/**
 * Resolve a branding asset URL.
 * Checks for a direct URL override in the branding config (e.g. "helpbeeIconUrl"),
 * falls back to /branding/default/{filename}.
 * Partners set explicit URLs per asset in their theme.json, same as logoUrl/nubiIconUrl.
 */
const BRANDING_ASSETS = {
  helpbeeIcon: { configKey: 'helpbeeIconUrl', defaultFile: 'helpbee-icon.svg' },
  troubleshootBee: { configKey: 'troubleshootBeeUrl', defaultFile: 'nubi-investigating.svg' },
  optimizeBee: { configKey: 'optimizeBeeUrl', defaultFile: 'nubi-searching.svg' },
  k8sBee: { configKey: 'k8sBeeUrl', defaultFile: 'k8s-bee.svg' },
  newUserBee: { configKey: 'newUserBeeUrl', defaultFile: 'nubi-investigating.svg' },
  securityBee: { configKey: 'securityBeeUrl', defaultFile: 'nubi-debugging.svg' },
  // Optional onboarding GIF for the "connect a cluster" help popup. No default
  // ships (the popup falls back to the live interactive walkthrough, which is
  // brand-correct everywhere) — a white-label tenant sets connectClusterGifUrl
  // in their theme.json to show their own recording instead.
  connectClusterGif: { configKey: 'connectClusterGifUrl', defaultFile: 'connect-cluster.gif' },
};

export const getBrandingAsset = (key) => {
  // Unresolved config: no asset. Returning the Nudgebee default here would put
  // a house-brand mascot on a partner's page for the frames before the config
  // lands. Callers already treat a plain string as "no custom asset" and
  // SafeIcon renders nothing for an empty one, so this is a safe third value.
  if (!_configCache) return '';
  const asset = BRANDING_ASSETS[key];
  const customSrc = asset && _configCache[asset.configKey];

  // On a white-label deployment the Nudgebee assets are never the right answer:
  // not as the value for a slot the kit left unset, and not as the onError
  // fallback for a slot it did set. Both paths used to repaint a partner's page
  // with house-brand mascots — the second one silently, on nothing more than a
  // typo'd path. Same rule the sidebar logo already follows: a broken URL should
  // surface as a gap, not swap to another brand.
  if (_configCache.isWhiteLabel) return customSrc || '';

  const defaultSrc = `/branding/default/${asset?.defaultFile || key}`;
  if (customSrc) return { src: customSrc, fallbackSrc: defaultSrc };
  return defaultSrc;
};

/**
 * Hook that provides tenant branding from the config API (/api/public/app_config).
 * All branding comes from the host's tenant_branding_override row over branding/default/theme.json.
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
  const { data: session } = useSession();
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
    pwaEnabled: !!brandingConfig.pwaEnabled,
  };
};
