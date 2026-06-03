import type { NextApiRequest, NextApiResponse } from 'next';

import { getRequestId, sendAuthenticationError } from '@utils/apiUtils';
import { authenticateRequest } from '@lib/rpcGateway';
import { encrypt } from '@lib/internal';
import { getAppBaseUrl } from '@lib/externalUrls';

// GitHub App installation entry point.
//
// This runs same-site (the popup opens THIS endpoint, not github.com
// directly), so the NextAuth session cookie is present and we can identify
// the user here — the one place the cookie is reliably available. We then
// fold a minimal, encrypted identity payload into the OAuth `state`
// parameter so the callback can recover who initiated the install WITHOUT
// depending on the cookie surviving GitHub's cross-site redirect into the
// popup (which third-party-cookie partitioning, SameSite, and host drift all
// break). This mirrors how slack/google/ms-teams carry identity through
// `state` and is why those callbacks don't need the cookie either.

// State carries only what buildSessionVariables() reads. The account-ids
// family has no post-RPC consumers (see rpcGateway.ts), so it's intentionally
// omitted to keep `state` short enough for the URL.
type StatePayload = {
  id: string;
  tenant_id: string;
  roles: string[];
  isSuperAdmin?: boolean;
  isSuperAdminReadonly?: boolean;
  ts: number;
};

function resolveOrigin(req: NextApiRequest): string {
  const forwardedHost = req.headers['x-forwarded-host'];
  const host = (Array.isArray(forwardedHost) ? forwardedHost[0] : forwardedHost) || req.headers.host;
  // Prefer the forwarded host so the redirect_uri matches the exact origin the
  // user loaded the app from (and where their session cookie lives); fall back
  // to the env-driven base only when no host header is available.
  if (!host) return getAppBaseUrl();
  const forwardedProto = req.headers['x-forwarded-proto'];
  let proto = (Array.isArray(forwardedProto) ? forwardedProto[0] : forwardedProto)?.split(',')[0];
  // No proxy proto header (typical local dev): localhost is plain http,
  // assume https everywhere else. Mirrors the old client-side getAppBaseUrl()
  // which read window.location.protocol.
  if (!proto) proto = host.startsWith('localhost') || host.startsWith('127.0.0.1') ? 'http' : 'https';
  return `${proto}://${host}`;
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId = getRequestId(req);

  try {
    const auth = await authenticateRequest(req);
    if (!auth?.jwt) {
      return sendAuthenticationError(res);
    }

    const jwt = auth.jwt as Record<string, unknown>;
    const payload: StatePayload = {
      id: ((jwt.id || jwt.sub) as string) || '',
      tenant_id: ((jwt.tenant as { id?: string } | undefined)?.id as string) || '',
      roles: (jwt.roles as string[]) || [],
      isSuperAdmin: !!jwt.isSuperAdmin,
      isSuperAdminReadonly: !!jwt.isSuperAdminReadonly,
      ts: Date.now(),
    };
    const state = await encrypt(JSON.stringify(payload));

    const appName = process.env.NEXT_PUBLIC_GITHUB_APP_NAME || process.env.GITHUB_APP_NAME || 'nudgebee';
    const redirectUri = `${resolveOrigin(req)}/api/integrations/github/callback`;

    const installUrl =
      `https://github.com/apps/${encodeURIComponent(appName)}/installations/new` +
      `?redirect_uri=${encodeURIComponent(redirectUri)}&state=${encodeURIComponent(state)}`;

    return res.redirect(installUrl);
  } catch (error: any) {
    console.error('GitHub install error:', error, { requestId });
    return res.status(500).json({ error: error.message || 'Internal Server Error', requestId });
  }
}
