// Server-side branding registry. The server-shaped counterpart to the
// client `slots.ts` registry: an optional plugin bundle registers a single
// provider that returns branding data; OSS code calls `resolveServerBranding`
// and gets `null` when nothing is registered, so it stays neutral.
//
// Why a registry (not a direct import): the branding source (file load +
// env overrides + license gate) is EE-only and lives under `app/src/ee/`,
// which is stripped from OSS snapshots. The two SSR consumers (`_document.tsx`,
// `getCriticalCssTokens`) and the `/api/public/app_config` route call
// `resolveServerBranding()` synchronously, so the provider must be sync —
// the EE side resolves its async license check once at boot before
// registering (see `app/src/ee/branding/serverInit.ts`).

export interface ServerBrandingData {
  logoUrl?: string;
  faviconUrl?: string;
  title?: string;
  assistantName?: string;
  nubiIconUrl?: string;
  nubiIconLightUrl?: string;
  signinImageUrl?: string;
  signinLeftImageUrl?: string;
  // Optional partner-supplied auth carousel slides: [{ title, image }].
  carouselSlides?: Array<{ title?: string; image?: string }> | null;
  loaderUrl?: string;
  helpbeeIconUrl?: string;
  troubleshootBeeUrl?: string;
  optimizeBeeUrl?: string;
  k8sBeeUrl?: string;
  newUserBeeUrl?: string;
  theme?: Record<string, unknown> | null;
  colorTokens?: Record<string, string> | null;
  // True when the active brand is NOT the Nudgebee house brand — suppresses
  // Nudgebee mascots (e.g. the bee loader). OSS defaults to false (app_config
  // hardcodes it) so the Nudgebee mascots show.
  isWhiteLabel?: boolean;
  // Optional tenant font remap: [{ family, src, weight?, style? }]. Re-points
  // hardcoded font-family names at a brand font via @font-face injected
  // client-side (see useThemeProvider). Null ⇒ no remap.
  fontRemap?: Array<{ family: string; src: string; weight?: string; style?: string }> | null;
}

type ServerBrandingProvider = () => ServerBrandingData | null;

// The provider lives on globalThis so it survives Next.js's dual module graph
// between the Node-runtime (`instrumentation.ts` → `@ee/init-server` →
// `branding/serverInit.ts`) and page/API-route contexts. With a module-scoped
// `let`, the two graphs each got their own copy: the route-side copy was never
// written, so Turbopack constant-folded `resolveServerBranding` down to
// `() => null` and dropped the `registerServerBranding(...)` call from the EE
// chunk as dead code — custom branding silently reverted to Nudgebee defaults
// in every built image. Same pattern (and same root cause) as
// `globalThis.__nbAuthHooks` in `authHooks.ts` and
// `globalThis.__nbBypassGraphQLAsServer` in `instrumentation.ts`.
type ServerBrandingRegistry = { provider: ServerBrandingProvider | null };

const _g = globalThis as unknown as { __nbServerBranding?: ServerBrandingRegistry };
if (!_g.__nbServerBranding) {
  _g.__nbServerBranding = { provider: null };
}
const _registry = _g.__nbServerBranding!;

/**
 * Register the branding provider. Called once at server boot from the EE
 * init-server entry point. The last registration wins.
 */
export function registerServerBranding(provider: ServerBrandingProvider): void {
  _registry.provider = provider;
}

/**
 * Resolve branding data for the current request, or `null` when no provider
 * is registered (the OSS case — always neutral).
 */
export function resolveServerBranding(): ServerBrandingData | null {
  const provider = _registry.provider;
  if (!provider) return null;
  try {
    return provider();
  } catch {
    // A misbehaving provider must never break SSR / the config endpoint.
    return null;
  }
}
