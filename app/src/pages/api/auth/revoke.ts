import type { NextApiRequest, NextApiResponse } from 'next';
import { decodeSessionJWT, decrypt } from '@lib/internal';
import { revokeBearerSession } from '@lib/sessionRevocation';

// Bearer-token logout. NextAuth's signOut event covers browser sessions,
// but Bearer callers (nbctl, automation, third-party scripts) hold an
// encrypted token minted by /api/auth/token and never touch the cookie
// flow, so they need their own way to invalidate. Caller authenticates
// itself by sending the token it wants to kill — we verify it's a valid
// JWT for the user we'll insert into the denylist (so callers can't revoke
// other users' sessions by guessing jti / user_id).
//
// Idempotent: re-revoking an already-revoked token is a no-op insert.
export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    res.status(405).json({ message: 'Method not allowed' });
    return;
  }

  const authHeader = req.headers.authorization;
  if (!authHeader) {
    res.status(401).json({ message: 'authorization header required' });
    return;
  }
  const parts = authHeader.split(' ');
  if (parts.length < 2) {
    res.status(401).json({ message: 'malformed authorization header' });
    return;
  }

  let rawJwt: string;
  try {
    rawJwt = await decrypt(parts[1]);
  } catch {
    res.status(401).json({ message: 'invalid bearer token' });
    return;
  }

  let payload: { sub?: string; jti?: string; exp?: number };
  try {
    const decoded = await decodeSessionJWT(rawJwt);
    payload = decoded.payload as { sub?: string; jti?: string; exp?: number };
  } catch {
    // Either signature/expiry failed or it's already revoked — both mean
    // there's nothing left to revoke. 200 keeps logout-twice idempotent.
    res.status(200).json({ revoked: true });
    return;
  }

  const userId = payload.sub;
  const jti = payload.jti;
  const exp = payload.exp;
  if (!userId || !jti || !exp) {
    // Pre-jti token: can't pin a per-jti row. The cookie-flow user-wide
    // signOut path handles those; for a pure-Bearer caller, refuse rather
    // than silently lying.
    res.status(400).json({ message: 'token missing required claims (sub, jti, exp)' });
    return;
  }

  try {
    await revokeBearerSession(userId, jti, exp);
    res.status(200).json({ revoked: true });
  } catch (err: any) {
    res.status(500).json({ message: err?.message || 'revoke failed' });
  }
}
