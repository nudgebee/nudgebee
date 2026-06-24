import type { NextApiRequest, NextApiResponse } from 'next';

import { encodeIdentityState } from '@lib/integrationState';
import { resolveRequestJwt } from '@lib/sessionToken';
import { getRequestId, handleErrorResponse, sendAuthenticationError } from 'src/utils/apiUtils';

// Same-origin entry point: gate on the session and sign the tenant into the OAuth
// `state` (a CSRF token); the callback re-derives tenant from the session and
// requires it to match.
export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId: string = getRequestId(req);
  try {
    const jwt = await resolveRequestJwt(req);
    const tenantId = ((jwt?.tenant as { id?: string } | undefined)?.id as string) || null;
    if (!tenantId) {
      return sendAuthenticationError(res);
    }

    const state = await encodeIdentityState({
      tenant_id: tenantId,
      user_email: (jwt?.email as string) || 'system',
    });

    await doRedirect(state, requestId, res);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}

async function doRedirect(state: string, requestId: string, res: NextApiResponse) {
  const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:80';
  const url = `${notificationServiceEndpoint}/api/integrations/install/ms-teams?state=${encodeURIComponent(state)}`;

  try {
    // Don't follow the redirect server-side — that makes THIS server fetch
    // Microsoft's login page (slow, and fails on-prem without outbound egress).
    // Read the notification service's Location header and send the browser there.
    const response = await fetch(url, { method: 'GET', headers: { 'x-request-id': requestId }, redirect: 'manual' });
    const location = response.headers.get('location');
    if (!location) {
      throw new Error('Missing Location header from notification service');
    }
    res.status(302).setHeader('Location', location).end();
  } catch (error: any) {
    console.error('Error fetching data:', error);
    handleErrorResponse(res, error, requestId);
  }
}
