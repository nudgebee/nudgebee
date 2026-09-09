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
//
// Branding resolves PER REQUEST HOST: one deployment can front several partner
// hostnames, each with its own brand kit. Callers holding the request pass its
// host; callers without one get the deployment-wide default.

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

type ServerBrandingProvider = (host?: string | null) => ServerBrandingData | null;

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
 * Normalize a `Host` / `X-Forwarded-Host` header into a brand-map lookup key:
 * first entry of a comma-joined list, lower-cased, port and trailing dot
 * stripped. Exported so the EE side normalizes the configured map with exactly
 * the same rules the lookup uses — a map key that normalizes differently from
 * the header would silently never match.
 *
 * IPv6 literals (`[::1]:3000`) keep their brackets: stripping the port must not
 * eat the address.
 */
export function normalizeBrandHost(host?: string | null): string {
  if (!host) return '';
  const first = host.split(',')[0].trim().toLowerCase();
  if (!first) return '';
  const hostname = first.startsWith('[') ? first.slice(0, first.indexOf(']') + 1) : first.split(':')[0];
  return hostname.replace(/\.$/, '');
}

type HeaderBag = Record<string, string | string[] | undefined> | undefined;

/**
 * Pick the brand-selecting hostname out of a request's headers.
 *
 * `X-Forwarded-Host` is checked FIRST, and the order is not cosmetic: the app
 * runs behind an nginx sidecar whose `location /` proxies to `127.0.0.1:3000`
 * and sets only `X-Forwarded-Host $host`. It does not set `Host`, so nginx's
 * default (`Host: $proxy_host`) applies and `req.headers.host` arrives as
 * `127.0.0.1:3000` in every deployed pod — reading it would resolve every
 * request to the same brand. `Host` remains the fallback for `next dev` and any
 * deployment that talks to Next directly.
 *
 * The sidecar overwrites a client-supplied `X-Forwarded-Host` with its own
 * `$host`, so the value is not spoofable past the proxy. `$host` itself derives
 * from the request's `Host`, which is also what the ingress routed on.
 */
export function brandHostFromHeaders(headers: HeaderBag): string | null {
  if (!headers) return null;
  const first = (v: string | string[] | undefined): string | undefined => (Array.isArray(v) ? v[0] : v);
  return first(headers['x-forwarded-host']) || first(headers.host) || null;
}

/**
 * Resolve branding data for `host`, or `null` when no provider is registered
 * (the OSS case — always neutral).
 *
 * `host` is the raw header value; it is normalized by the provider. Omitting it
 * resolves the deployment-wide default brand, which is the right answer for
 * callers with no request in hand: build-time prerendering, and the marketplace
 * callback pages, which are only ever reached at BASE_URL.
 */
export function resolveServerBranding(host?: string | null): ServerBrandingData | null {
  const provider = _registry.provider;
  if (!provider) return null;
  try {
    return provider(host);
  } catch {
    // A misbehaving provider must never break SSR / the config endpoint.
    return null;
  }
}
