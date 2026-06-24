import type { NextApiRequest, NextApiResponse } from 'next';

import { getRequestId, sendAuthenticationError } from '@utils/apiUtils';
import { encodeIdentityState } from '@lib/integrationState';
import { resolveRequestAuth } from '@lib/sessionToken';
import { getAppBaseUrl } from '@lib/externalUrls';

// GitHub App installation entry point. Runs same-origin (the popup opens THIS
// endpoint), so we read the session here and sign the tenant into the OAuth
// `state` as a CSRF token; the callback re-derives tenant from the session and
// requires it to match, so nothing in the redirect can be forged.
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
    const auth = await resolveRequestAuth(req);
    const tenantId = ((auth?.jwt?.tenant as { id?: string } | undefined)?.id as string) || null;
    if (!auth?.jwt || !tenantId) {
      return sendAuthenticationError(res);
    }

    const state = await encodeIdentityState({ tenant_id: tenantId, user_email: (auth.jwt.email as string) || 'system' });

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
