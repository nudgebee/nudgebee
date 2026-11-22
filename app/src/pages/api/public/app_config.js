import { awaitServerBrandingReady, brandHostFromHeaders, resolvePwaThemeColor, resolveServerBranding } from '@lib/serverBranding';

// Public runtime config. This route must live in OSS (the pages-router maps
// this file path to /api/public/app_config) because it serves the agent-
// onboarding fields below, consumed by K8sAccountModal and
// VmAgentCredentialsDialog.
//
// It carries NO branding logic: branding fields (logo/title/colorTokens/
// fontRemap/isWhiteLabel/...) are supplied by the optional server branding
// provider (EE-only — app/src/ee/branding/serverInit.ts) and are simply
// absent in OSS, where the client falls back to neutral defaults.
//
// `isWhiteLabel` defaults to false: OSS (and EE without a custom branding
// file) ships as Nudgebee, so the Nudgebee mascots (e.g. the bee loader) are
// shown. The EE provider overrides it to true only when the request's host
// resolves to a custom (non-default) brand.
//
// Branding is resolved from the request host, so one deployment can serve
// several partner hostnames. This route is the client's only branding source
// (useTenantBranding fetches it on load), which is why per-host branding works
// on every page — including the ones prerendered at build time, whose SSR head
// cannot vary by host.
export default async function handler(req, res) {
  // Wait for the branding provider to be ready before answering. This route is
  // the client's ONLY source of branding, so answering while the provider is
  // still cold hands the browser house-brand values that it then caches for the
  // whole page load — the partner sees the wrong brand until they reload. An API
  // route can await where SSR cannot, and the readiness step is a no-op once
  // warm (and in OSS, where nothing registers one).
  await awaitServerBrandingReady();

  const branding = resolveServerBranding(brandHostFromHeaders(req.headers)) || {};

  res.status(200).json({
    isWhiteLabel: false,
    ...branding,
    relayUrl: process.env.RELAY_WSSERVER_ENDPOINT || '',
    k8sCollectorUrl: process.env.K8S_COLLECTOR_ENDPOINT || '',
    signingPublicKey: process.env.SIGNING_PUBLIC_KEY || '',
    // Background-watch feature flag. Mirrors LLM_SERVER_WATCH_ENABLED on
    // llm-server, which is on unless set to false (remediation waits run on
    // watches), so this is on unless set to false too. A llm-server with watches
    // off answers the watch list with 404, which the client latches off.
    watchEnabled: process.env.LLM_SERVER_WATCH_ENABLED !== 'false',
    pwaEnabled: !branding.isWhiteLabel && (process.env.ENABLE_PWA === 'true' || process.env.NEXT_PUBLIC_ENABLE_PWA === 'true'),
    pwaThemeColor: resolvePwaThemeColor(branding),
  });
}
