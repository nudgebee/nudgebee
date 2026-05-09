import { decodeJwt, jwtVerify, importSPKI } from 'jose';

export interface LicenseDetails {
  licenseType: string;
  tenantId?: string;
  email?: string;
}

let _cachedLicenseDetails: { details: LicenseDetails; expiresAt: number } | null = null;

function getLicensePublicKey(): string | null {
  const raw = process.env.LICENSE_PUBLIC_KEY;
  if (!raw) return null;
  // Allow PEM passed with `\n` escapes (common in env-file delivery).
  return raw.replace(/\\n/g, '\n');
}

/**
 * Validate and decode the license JWT.
 *
 * Returns `{ licenseType: 'free', tenantId: '' }` when:
 *   - no license JWT is configured (NUDGEBEE_LICENSE unset), or
 *   - no license public key is configured (LICENSE_PUBLIC_KEY unset).
 *
 * The OSS build leaves both unset, so this is the default path.
 * Commercial / on-prem deployments set both env vars to enforce a tenant-bound license.
 */
export async function getLicenseDetails(): Promise<LicenseDetails> {
  const currentTimeSec = Math.floor(Date.now() / 1000);
  if (_cachedLicenseDetails && _cachedLicenseDetails.expiresAt > currentTimeSec) {
    return _cachedLicenseDetails.details;
  }

  const license = process.env.NUDGEBEE_LICENSE;
  const publicKey = getLicensePublicKey();

  // OSS / unconfigured: no license enforcement.
  if (!license || !publicKey) {
    const details: LicenseDetails = { licenseType: 'free', tenantId: '' };
    _cachedLicenseDetails = { details, expiresAt: Infinity };
    return details;
  }

  let licenseExp = Infinity;
  try {
    const secret = await importSPKI(publicKey, 'RS256');
    const result = await jwtVerify(license, secret, { algorithms: ['RS256'] });
    if (result.payload.exp) {
      licenseExp = result.payload.exp;
      if (result.payload.exp < currentTimeSec) {
        throw new Error('License expired');
      }
      if (result.payload.licenseType === 'on-prem' && !result.payload.tenantId) {
        throw new Error('Invalid license: on-prem licenses require tenantId');
      }
    }
  } catch (e) {
    console.error('License verification failed:', e);
    throw e;
  }

  try {
    const decoded = decodeJwt(license);
    const details: LicenseDetails = {
      licenseType: decoded.licenseType as string,
      tenantId: decoded.tenantId as string,
      email: decoded.email as string,
    };
    _cachedLicenseDetails = { details, expiresAt: licenseExp };
    return details;
  } catch (e) {
    console.error('License decode failed:', e);
    throw e;
  }
}

/**
 * Convenience: returns true when a commercial/on-prem license has been verified.
 * OSS deployments always return false.
 */
export async function isLicensedDeployment(): Promise<boolean> {
  const details = await getLicenseDetails();
  return details.licenseType !== 'free';
}

/**
 * Synchronous, cheap check used by UI / signup gating to decide whether the deployment
 * is single-tenant ("on-prem") and self-signup should be disabled.
 *
 * Returns true when either:
 *   - ON_PREM_MODE=true is set (OSS single-tenant deployment), or
 *   - a commercial NUDGEBEE_LICENSE env var is present (validation happens lazily elsewhere).
 */
export function isOnPremMode(): boolean {
  if (process.env.ON_PREM_MODE === 'true') return true;
  return Boolean(process.env.NUDGEBEE_LICENSE);
}
