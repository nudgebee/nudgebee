import { getToken, type JWT } from 'next-auth/jwt';
import type { NextApiRequest } from 'next';

/**
 * Read the NextAuth session cookie without depending on `NEXTAUTH_URL`.
 *
 * `getToken()` derives the cookie NAME from that env var alone:
 *
 *   // next-auth/jwt/index.js
 *   secureCookie = process.env.NEXTAUTH_URL?.startsWith("https://") ?? !!process.env.VERCEL
 *   cookieName   = secureCookie ? "__Secure-next-auth.session-token" : "next-auth.session-token"
 *
 * That breaks in two directions. On-prem HTTP-behind-a-TLS-proxy writes the cookie
 * under the secure name and reads it under the non-secure one. And a deployment that
 * unsets `NEXTAUTH_URL` so NextAuth resolves its origin per request (several partner
 * hostnames on one deployment) flips `secureCookie` to false while NextAuth — seeing
 * https on the request — still WRITES `__Secure-…`: every authenticated request then
 * fails to find a session the user just established. That took dev down on
 * 2026-09-01; the deployment-wide `NEXTAUTH_URL` had to be restored to recover.
 *
 * Trying both names is what makes the cookie name independent of that env var, so
 * `NEXTAUTH_URL` can be unset safely. Hardcoding `secureCookie: true` would fix the
 * per-host case and re-break the on-prem HTTP one.
 *
 * This module deliberately imports nothing from the app: `sessionToken.ts` pulls in
 * `rpcGateway`, and `rpcGateway` is itself a caller, so the helper cannot live there
 * without a require cycle.
 */
export async function readSessionToken(req: NextApiRequest): Promise<JWT | null> {
  return (await getToken({ req, secureCookie: true })) ?? (await getToken({ req, secureCookie: false }));
}
