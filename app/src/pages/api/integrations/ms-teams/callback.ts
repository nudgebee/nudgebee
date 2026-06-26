import type { NextApiRequest, NextApiResponse } from 'next';

import { decodeIdentityState, type IntegrationIdentity } from '@lib/integrationState';
import { resolveRequestJwt } from '@lib/sessionToken';
import { getRequestId, handleOAuthCallbackResponse, sendAuthenticationError } from '@utils/apiUtils';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId: string = getRequestId(req);
  try {
    // Tenant is authoritative from the installer's own session, never from the
    // redirect. The signed `state` is a CSRF token: it must decrypt and its
    // tenant must match the session, proving this callback completes a flow the
    // session itself initiated — which blocks both cross-tenant state injection
    // and OAuth code injection.
    const jwt = await resolveRequestJwt(req);
    const tenantId = ((jwt?.tenant as { id?: string } | undefined)?.id as string) || null;
    if (!tenantId) {
      return sendAuthenticationError(res);
    }

    const signed = await decodeIdentityState(req.query.state);
    if (!signed || signed.tenant_id !== tenantId) {
      res
        .status(400)
        .setHeader('x-request-id', requestId)
        .json({ error: 'invalid_state', description: 'State missing, expired, or tenant mismatch' });
      return;
    }

    const identity: IntegrationIdentity = { tenant_id: tenantId, user_email: (jwt?.email as string) || 'system' };
    await doRedirect(req, identity, requestId, res);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}

async function doRedirect(req: NextApiRequest, identity: IntegrationIdentity, requestId: string, res: NextApiResponse) {
  const code = req.query.code;
  if (typeof code !== 'string' || code.length === 0) {
    res.status(400).setHeader('x-request-id', requestId).json({ error: 'invalid_request', description: 'Missing authorization code' });
    return;
  }
  const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL ? process.env.NOTIFICATION_SERVICE_URL : 'http://notifications:80';
  const url = notificationServiceEndpoint + '/api/integrations/callback/ms-teams';
  await redirectOauthToNotificationService(url, identity, requestId, code, res);
}

async function redirectOauthToNotificationService(url: string, identity: IntegrationIdentity, requestId: string, code: string, res: NextApiResponse) {
  let attempt = 3;
  let proxyResponse = null;

  while (attempt > 0) {
    proxyResponse = await fetchAndGetResponse(url, identity, requestId, code);
    if (proxyResponse.status === 500) {
      // clone() so reading the body to detect ECONNRESET doesn't consume it before handleOAuthCallbackResponse.
      const body = await proxyResponse
        .clone()
        .json()
        .catch(() => ({}));
      if (body.code === 'ECONNRESET') {
        console.error('Connection Reset - retrying');
        attempt -= 1;
        continue;
      }
    }
    break;
  }
  await handleOAuthCallbackResponse(proxyResponse, res, requestId);
}

async function fetchAndGetResponse(url: string, identity: IntegrationIdentity, requestId: string, code: string) {
  return await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      'x-request-id': requestId,
      'x-user-email': identity.user_email,
    },
    body: JSON.stringify({ code, tenant_id: identity.tenant_id }),
    method: 'post',
  });
}

function handleErrorResponse(res: NextApiResponse, error: any, requestId: string): void {
  console.log('api error', error);
  res
    .status(error.status || 500)
    .setHeader('x-request-id', requestId)
    .json({
      code: error.code,
      error: error.message,
    });
}
