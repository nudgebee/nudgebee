// License client. Services-server is the verification authority — it parses
// the JWT, validates the RSA signature, applies grace-period rules, and
// exposes a flat view via GET /v1/license/me. The frontend no longer parses
// the JWT itself; callers consult getLicenseDetails() and read `tier`.

export type LicenseTier = 'oss' | 'ee' | 'saas';
export type LicenseStatus = 'active' | 'grace' | 'expired' | 'missing';

// Single source of truth for the error message thrown when services-server
// is unreachable. Imported by both license.ts and nextauth so the wording
// stays in sync.
export const SERVICES_SERVER_UNREACHABLE_MSG = 'SERVICE_API_SERVER_URL not set — services-server is core, the app cannot operate without it';

export interface LicenseDetails {
  tenantId?: string;
  email?: string;
  features?: string[];
  tier?: LicenseTier;
  status?: LicenseStatus;
  expiry?: number;
  // Non-empty when getLicenseDetails() fell back due to a fetch failure.
  // Most callers can ignore this; callers that need fail-closed gating
  // (e.g. hiding a paywall) should check it explicitly.
  error?: string;
}

let _cached: { details: LicenseDetails; expiresAt: number } | null = null;
// In-flight fetch dedupe: concurrent callers share a single network round
// trip rather than each spawning their own and each throwing if the call
// fails. Cleared on settle (success or failure).
let _inflight: Promise<LicenseDetails> | null = null;

function servicesURL(): string {
  const base = process.env.SERVICE_API_SERVER_URL;
  if (!base) {
    throw new Error(SERVICES_SERVER_UNREACHABLE_MSG);
  }
  return base.replace(/\/+$/, '') + '/v1/license/me';
}

async function fetchFromServices(): Promise<LicenseDetails> {
  const url = servicesURL();
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (process.env.ACTION_API_SERVER_TOKEN) {
    headers['X-ACTION-TOKEN'] = process.env.ACTION_API_SERVER_TOKEN;
  }
  // Fail-closed: services-server gates every API. If it's unreachable
  // nothing works anyway, so surface the failure here rather than
  // silently degrading to an unlicensed view in what's supposed to be
  // a licensed deployment.
  const resp = await fetch(url, { headers });
  if (!resp.ok) {
    throw new Error(`license: /v1/license/me returned ${resp.status}`);
  }
  const body = await resp.json();
  return {
    tenantId: body.tenant_id || '',
    email: body.email || '',
    features: Array.isArray(body.features) ? body.features : [],
    tier: body.tier as LicenseTier,
    status: body.status as LicenseStatus,
    expiry: typeof body.expiry === 'number' ? body.expiry : 0,
  };
}

// _fallback is what getLicenseDetails() returns when services-server is
// unreachable. Fail-soft is deliberate: many callers consult license fields
// from hot paths (session callback, OAuth signin callback) where throwing
// would cascade into broken sessions during a services-server hiccup. The
// empty `tenantId` / `email` against the fallback means licensed-tenant
// callsites short-circuit to their unlicensed default — the safest outcome
// under uncertainty.
//
// Callers that genuinely need fail-closed gating should check `error` on
// the returned object and react explicitly.
const _fallback: LicenseDetails = {
  tenantId: '',
  email: '',
  features: [],
  tier: 'oss' as LicenseTier,
  status: 'missing' as LicenseStatus,
  expiry: 0,
  error: 'license fetch failed',
};

/**
 * Returns license details from services-server's /v1/license/me endpoint.
 * Cached in-process until the license's exp passes; refreshed on next call.
 *
 * Fails soft: on network error / services-server unreachable, returns a
 * fallback object with empty fields and an `error` string. See the
 * `_fallback` comment above for the rationale.
 */
export async function getLicenseDetails(): Promise<LicenseDetails> {
  const now = Math.floor(Date.now() / 1000);
  if (_cached && _cached.expiresAt > now) {
    return _cached.details;
  }
  if (_inflight) return _inflight;
  _inflight = (async () => {
    try {
      const details = await fetchFromServices();
      // Unlicensed deployments report expiry=0; cache for an hour
      // rather than re-fetching every 5 minutes per process since the
      // tier doesn't change in that case.
      const ttl = details.expiry && details.expiry > now ? details.expiry : now + 3600;
      _cached = { details, expiresAt: ttl };
      return details;
    } catch (err) {
      // Don't cache failures — next call retries immediately.
      console.warn('[license] failed to fetch from services-server:', err);
      return _fallback;
    } finally {
      _inflight = null;
    }
  })();
  return _inflight;
}

/**
 * True when the deployment carries a verified non-OSS license.
 */
export async function isLicensedDeployment(): Promise<boolean> {
  const d = await getLicenseDetails();
  return d.tier !== 'oss' && d.tier !== undefined;
}

/**
 * Returns true when the given feature is enabled on this deployment. Backed
 * by the license features list — bridged into feature_flag for other
 * consumers; the frontend reads it directly from the license endpoint.
 */
export async function hasLicenseFeature(feature: string): Promise<boolean> {
  const d = await getLicenseDetails();
  return Boolean(d.features?.includes(feature));
}
