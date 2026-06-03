// Centralized NextAuth/session-JWT secret resolution.
//
// `.env.example` ships a sentinel value so `cp .env.example .env && npm run dev`
// works without any manual key generation — eliminates first-time-contributor
// friction. The helper detects the sentinel (or an empty value) at boot:
//
//   - production (NODE_ENV !== 'development'): throws fast — refuses to sign
//     session tokens with a known/empty value
//   - development:                              warns loudly once and uses
//                                               the sentinel as a working key
//
// Rotation safety: this secret signs the session JWT (HS256). Rotating it
// invalidates existing sessions — the only user-visible effect is that
// active users must sign in again. There is no encrypted-at-rest data
// derived from this key, so rotation is cheap.

export const DEV_SENTINEL_NEXTAUTH_SECRET = 'dev-insecure-default-REPLACE-before-prod-openssl-rand-base64-32';

let _warned = false;

function warnOnce(message: string): void {
  if (_warned) return;
  _warned = true;
  console.warn(message);
}

export function resolveNextAuthSecret(): string {
  const raw = process.env.NEXTAUTH_SECRET ?? '';
  // Default to strict mode whenever NODE_ENV is anything but 'development'
  // (production, test, undefined). Fail-safe: an unset NODE_ENV in a
  // hosted environment still throws rather than silently using the sentinel.
  const strict = process.env.NODE_ENV !== 'development';

  if (!raw) {
    if (strict) {
      throw new Error('NEXTAUTH_SECRET is required. Generate with: openssl rand -base64 32');
    }
    warnOnce(
      '⚠️  NEXTAUTH_SECRET is empty — using the dev sentinel as a fallback. ' +
        'Set NEXTAUTH_SECRET in app/.env for stable sessions across restarts. ' +
        'Generate with: openssl rand -base64 32'
    );
    return DEV_SENTINEL_NEXTAUTH_SECRET;
  }

  if (raw === DEV_SENTINEL_NEXTAUTH_SECRET) {
    if (strict) {
      throw new Error('NEXTAUTH_SECRET is still set to the dev sentinel from .env.example. ' + 'Rotate before deploying: openssl rand -base64 32');
    }
    warnOnce(
      '⚠️  NEXTAUTH_SECRET is using the dev sentinel from .env.example. ' +
        'Fine for local dev — replace before deploying. ' +
        'Rotation impact: users re-login. Generate with: openssl rand -base64 32'
    );
  }

  return raw;
}
