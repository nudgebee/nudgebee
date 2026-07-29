import { resolveServerBranding } from '@lib/serverBranding';

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
// shown. The EE provider overrides it to true only when an operator supplies a
// custom (non-default) TENANT_BRANDING_FILE.
export default function handler(req, res) {
  const branding = resolveServerBranding() || {};

  res.status(200).json({
    isWhiteLabel: false,
    ...branding,
    relayUrl: process.env.RELAY_WSSERVER_ENDPOINT || '',
    k8sCollectorUrl: process.env.K8S_COLLECTOR_ENDPOINT || '',
    signingPublicKey: process.env.SIGNING_PUBLIC_KEY || '',
    // Background-watch feature flag. Mirrors LLM_SERVER_WATCH_ENABLED on
    // llm-server, which only mounts the /v1/watches route when set. The chat
    // UI polls the watch-list endpoint solely when this is true, so the route's
    // absence never produces a 404.
    watchEnabled: process.env.LLM_SERVER_WATCH_ENABLED === 'true',
  });
}
