import type { NextApiRequest, NextApiResponse } from 'next';

import { resolveRequestJwt } from '@lib/sessionToken';
import { getRequestId, handleOAuthCallbackResponse, sendAuthenticationError } from '@utils/apiUtils';

type Identity = { tenantId: string; userEmail: string };

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId: string = getRequestId(req);
  try {
    // The installer must complete the callback from their own authenticated
    // session. Tenant and email are taken from that session, never from the
    // Slack-controlled query params. The notifications service additionally
    // checks that the OAuth state was issued to this same tenant, which blocks
    // completing an install flow that was started for a different tenant.
    const jwt = await resolveRequestJwt(req);
    const tenantId = ((jwt?.tenant as { id?: string } | undefined)?.id as string) || null;
    if (!tenantId) {
      return sendAuthenticationError(res);
    }
    const identity: Identity = { tenantId, userEmail: (jwt?.email as string) || 'system' };
    await doRedirect(req, identity, requestId, res);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}

async function doRedirect(req: NextApiRequest, identity: Identity, requestId: string, res: NextApiResponse) {
  const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:80';
  const url = `${notificationServiceEndpoint}/slack/oauth_redirect`;

  const rawCode = req.query.code;
  const rawState = req.query.state;
  const code = Array.isArray(rawCode) ? rawCode[0] : rawCode;
  const state = Array.isArray(rawState) ? rawState[0] : rawState;

  if (!code || !state) {
    res.status(400).setHeader('x-request-id', requestId).json({ error: 'invalid_request', description: 'Missing code or state' });
    return;
  }

  // Forward Slack's own state unchanged: the notifications service resolves the
  // tenant from it (the state was bound to the tenant at /install) and validates
  // it for single-use CSRF protection.
  const params = new URLSearchParams();
  params.set('code', code);
  params.set('state', state);

  const fullUrl = `${url}?${params.toString()}`;

  const incomingTrace = req.headers['traceparent'];
  const traceparent = Array.isArray(incomingTrace) ? incomingTrace[0] : incomingTrace ?? requestId;

  const headers: Record<string, string> = {
    'x-request-id': requestId,
    traceparent: traceparent,
    'x-user-email': identity.userEmail,
    'x-tenant-id': identity.tenantId,
  };

  let attempt = 3;
  let proxyResponse: Response | null = null;
  while (attempt > 0) {
    proxyResponse = await fetch(fullUrl, {
      method: 'GET',
      headers,
    });
    if (proxyResponse.status === 500) {
      const body = await proxyResponse
        .clone()
        .json()
        .catch(() => ({}));
      if (body?.code === 'ECONNRESET') {
        attempt -= 1;
        continue;
      }
    }
    break;
  }

  await handleOAuthCallbackResponse(proxyResponse, res, requestId);
}

function handleErrorResponse(res: NextApiResponse, error: any, requestId: string): void {
  console.error('Slack callback error:', error);
  res
    .status(error.status || 500)
    .setHeader('x-request-id', requestId)
    .json({
      code: error.code || 'internal_error',
      error: 'Internal Server Error',
    });
}
