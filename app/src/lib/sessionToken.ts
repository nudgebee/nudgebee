import { type JWT } from 'next-auth/jwt';
import type { NextApiRequest } from 'next';
import { authenticateRequest, type AuthContext } from '@lib/rpcGateway';
import { readSessionToken } from '@lib/sessionCookie';

// Kept as the name the OAuth-integration routes already import. The implementation
// moved to @lib/sessionCookie so `rpcGateway` can use it too without a require cycle
// (this module imports rpcGateway).
export async function getSessionTokenResilient(req: NextApiRequest): Promise<JWT | null> {
  return readSessionToken(req);
}

// These routes call backends directly, not through forwardAction, so a scoped API key's
// scopes would never be checked here. Such a key is treated as unauthenticated.
function isScopedKey(auth: AuthContext | null): boolean {
  return !!auth && Array.isArray(auth.jwt.tokenScopes);
}

// Identity for integration OAuth routes: session cookie or encrypted bearer.
// Does NOT fall back to raw unverified cookies if authenticateRequest fails or revokes the session.
export async function resolveRequestJwt(req: NextApiRequest): Promise<JWT | null> {
  const auth = await authenticateRequest(req);
  if (isScopedKey(auth)) return null;
  if (auth?.jwt) return auth.jwt;
  return null;
}

// Like resolveRequestJwt but returns the full AuthContext ({ token, jwt }) for callers
// that also need the bearer (e.g. GitHub callback's clientAuthorization).
export async function resolveRequestAuth(req: NextApiRequest): Promise<AuthContext | null> {
  const auth = await authenticateRequest(req);
  if (isScopedKey(auth)) return null;
  if (auth?.jwt) return auth;
  return null;
}
