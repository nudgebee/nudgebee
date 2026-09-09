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

// Identity for integration OAuth routes: session cookie or encrypted bearer, plus the resilient cookie read.
export async function resolveRequestJwt(req: NextApiRequest): Promise<JWT | null> {
  const auth = await authenticateRequest(req);
  if (auth?.jwt) return auth.jwt;
  return getSessionTokenResilient(req);
}

// Like resolveRequestJwt but returns the full AuthContext ({ token, jwt }) for callers
// that also need the bearer (e.g. GitHub callback's clientAuthorization). The resilient
// fallback yields an empty token, which those callers treat as "no bearer to forward".
export async function resolveRequestAuth(req: NextApiRequest): Promise<AuthContext | null> {
  const auth = await authenticateRequest(req);
  if (auth?.jwt) return auth;
  const jwt = await getSessionTokenResilient(req);
  return jwt ? { token: '', jwt } : null;
}
