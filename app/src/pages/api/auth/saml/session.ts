import type { NextApiRequest, NextApiResponse } from 'next';
import crypto from 'crypto';
import { setCookie } from 'nookies';
import { encode as encodeJWT } from 'next-auth/jwt';
import { getUserByUsername } from '@lib/UserService';
import { extractUserPermissions, getSessionExpirationSeconds } from '@lib/userPermissionMapper';
import { TOKEN_EXPIRY_SECONDS } from './acs';

type EncodedPayload = {
  id: string;
  email: string;
  provider?: string;
  ts: number;
};

type SignedPayload = {
  data: string;
  signature: string;
};

function verifyPayload(signedPayload: string, secret: string): EncodedPayload | null {
  try {
    const decoded = Buffer.from(signedPayload, 'base64').toString('utf-8');
    const { data, signature } = JSON.parse(decoded) as SignedPayload;

    // Verify HMAC signature
    const expectedSignature = crypto.createHmac('sha256', secret).update(data).digest('hex');
    if (!crypto.timingSafeEqual(Buffer.from(signature, 'hex'), Buffer.from(expectedSignature, 'hex'))) {
      console.warn('session: invalid signature');
      return null;
    }

    const payload = JSON.parse(data) as EncodedPayload;

    // Verify timestamp to prevent replay attacks
    const now = Math.floor(Date.now() / 1000);
    if (!payload.ts || now - payload.ts > TOKEN_EXPIRY_SECONDS) {
      console.warn('session: token expired', { tokenAge: now - payload.ts });
      return null;
    }

    return payload;
  } catch (err) {
    console.error('session: failed to verify payload', err);
    return null;
  }
}

async function buildTokenPayload(user: any) {
  const permissions = await extractUserPermissions(user);

  return {
    sub: user.id,
    id: user.id,
    email: user.username,
    name: user.display_name || user.username,
    email_verified: true,
    roles: permissions.roles,
    tenant: permissions.tenant,
    accountIds: permissions.accountIds,
    readOnlyAccountIds: permissions.readonlyAccountIds,
    namespacedAccountIds: permissions.namespacedAccountIds,
    namespacedReadOnlyAccountIds: permissions.namespacedReadOnlyAccountIds,
    k8sNamespaces: permissions.k8sNamespaces,
    iat: Math.floor(Date.now() / 1000),
    exp: Math.floor(Date.now() / 1000) + getSessionExpirationSeconds(),
  };
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const { user: userEncoded } = req.query;

  if (!userEncoded || typeof userEncoded !== 'string') {
    console.warn('session: missing user query');
    return res.redirect('/signin?error=invalid_session_data');
  }

  const secret = process.env.NEXTAUTH_SECRET;
  if (!secret) {
    console.error('session: NEXTAUTH_SECRET not configured');
    return res.redirect('/signin?error=session_creation_failed&message=server_misconfiguration');
  }

  // Verify signed payload - prevents forging session tokens
  const payload = verifyPayload(userEncoded, secret);
  if (!payload) {
    console.warn('session: invalid or expired token');
    return res.redirect('/signin?error=invalid_session_data&message=Token verification failed');
  }

  if (!payload.email || !payload.id) {
    console.warn('session: payload missing id/email', payload);
    return res.redirect('/signin?error=invalid_user_data');
  }

  try {
    // Re-fetch user to ensure canonical data and permissions are up-to-date
    const userResp = await getUserByUsername({
      username: payload.email,
      fetchRoles: true,
      fetchAccounts: true,
      fetchGroups: true,
    });

    if (!userResp?.data?.users?.length) {
      console.warn('session: user not found in DB', payload.email);
      return res.redirect('/signin?error=user_not_found');
    }

    const user = userResp.data.users[0];

    // Check user status
    if (user.status === 'suspended') {
      console.warn('session: user is suspended', payload.email);
      return res.redirect('/signin?error=user_suspended');
    }

    // Build complete token payload with all roles, permissions, and tenant data
    const tokenPayload = await buildTokenPayload(user);

    const maxAge = getSessionExpirationSeconds();
    const sessionToken = await encodeJWT({
      token: tokenPayload as any,
      secret,
      maxAge,
    });

    if (!sessionToken) {
      console.error('session: encodeJWT returned empty token');
      return res.redirect('/signin?error=session_creation_failed&message=token_generation_failed');
    }

    // Determine if connection is actually secure - must work for both HTTP and HTTPS deployments
    // Priority: NEXTAUTH_URL (explicit config) > x-forwarded-proto header > direct connection check
    const nextAuthUrl = process.env.NEXTAUTH_URL || '';
    const forwardedProto = req.headers['x-forwarded-proto'];
    const isSecureConnection =
      nextAuthUrl.startsWith('https://') || forwardedProto === 'https' || (Array.isArray(forwardedProto) && forwardedProto[0] === 'https');

    // NextAuth v4 uses 'next-auth.session-token' for HTTP and '__Secure-next-auth.session-token' for HTTPS
    const cookieName = isSecureConnection ? '__Secure-next-auth.session-token' : 'next-auth.session-token';

    setCookie({ res }, cookieName, sessionToken, {
      httpOnly: true,
      secure: isSecureConnection,
      sameSite: 'lax',
      maxAge,
      path: '/',
    });

    console.info('session: created session cookie for', user.username);
    return res.redirect('/');
  } catch (err: any) {
    console.error('session: unexpected error', err);
    return res.redirect(`/signin?error=session_creation_failed&message=${encodeURIComponent(err?.message || 'unknown')}`);
  }
}
