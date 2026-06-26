import { getToken, type JWT } from 'next-auth/jwt';
import type { NextApiRequest } from 'next';
import { authenticateRequest, type AuthContext } from '@lib/rpcGateway';

// getToken() picks the session cookie name (`__Secure-` vs not) from NEXTAUTH_URL's scheme
// alone, so on-prem HTTP-behind-a-TLS-proxy can write it under the secure name but read it
// under the non-secure one. Try both so a scheme mismatch can't hide a valid session.
export async function getSessionTokenResilient(req: NextApiRequest): Promise<JWT | null> {
  return (await getToken({ req, secureCookie: true })) ?? (await getToken({ req, secureCookie: false }));
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
