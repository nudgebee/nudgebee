import type { NextApiRequest, NextApiResponse } from 'next';
import { canManageMessagingPlatforms } from '@lib/messagingPlatforms';
import { resolveRequestAuth } from '@lib/sessionToken';
import { getRequestId, handleErrorResponse, sendAuthenticationError } from 'src/utils/apiUtils';

// Redeems a connect link from the Microsoft Teams bot for the session's workspace.
// notifications-server checks the link and that the email is whoever added the bot;
// the workspace and email always come from the session, never from the link.
export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    return res.status(405).json({ error: 'Method not allowed' });
  }

  const requestId: string = getRequestId(req);
  try {
    const auth = await resolveRequestAuth(req);
    const tenantId = ((auth?.jwt?.tenant as { id?: string } | undefined)?.id as string) || null;
    const userEmail = (auth?.jwt?.email as string) || null;

    if (!auth?.jwt || !tenantId) {
      return sendAuthenticationError(res);
    }
    if (!canManageMessagingPlatforms(auth.jwt)) {
      return res.status(403).json({ code: 'role_missing' });
    }
    // The workspace the page showed. The page isn't under withAuth, so it sends no
    // tab-tenant header; a switch in another window since it rendered is caught here.
    if (req.body?.tenant_id !== tenantId) {
      return res.status(409).json({ code: 'workspace_changed' });
    }

    const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:80';
    const response = await fetch(`${notificationServiceEndpoint}/api/integrations/link/ms-teams`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'tenant-id': tenantId,
        'x-user-email': userEmail || '',
        'x-request-id': requestId,
        'X-ACTION-TOKEN': process.env.NOTIFICATION_SERVER_TOKEN || '',
      },
      body: JSON.stringify({ token: typeof req.body?.token === 'string' ? req.body.token : '' }),
    });

    if (response.status >= 500) {
      return res.status(response.status).json({ code: 'error' });
    }
    const data = await response.json().catch(() => ({ code: 'error' }));
    return res.status(response.status).json(data);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}
