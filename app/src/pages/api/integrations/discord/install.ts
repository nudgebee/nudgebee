import type { NextApiRequest, NextApiResponse } from 'next';
import { canManageMessagingPlatforms } from '@lib/messagingPlatforms';
import { resolveRequestAuth } from '@lib/sessionToken';
import { getRequestId, handleErrorResponse, sendAuthenticationError } from 'src/utils/apiUtils';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    return res.status(405).json({ error: 'Method not allowed' });
  }

  const requestId: string = getRequestId(req);
  try {
    const auth = await resolveRequestAuth(req);
    const tenantId = ((auth?.jwt?.tenant as { id?: string } | undefined)?.id as string) || null;
    const userEmail = (auth?.jwt?.email as string) || null;
    const token = auth?.token || '';

    if (!auth?.jwt || !tenantId) {
      return sendAuthenticationError(res);
    }

    if (!canManageMessagingPlatforms(auth.jwt)) {
      return res.status(403).json({ error: 'forbidden' });
    }

    const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:80';
    const url = `${notificationServiceEndpoint}/api/integrations/install/discord`;

    const response = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${token}`,
        'tenant-id': tenantId,
        'x-user-email': userEmail || '',
        'x-request-id': requestId,
        'X-ACTION-TOKEN': process.env.NOTIFICATION_SERVER_TOKEN || '',
      },
      body: JSON.stringify(req.body),
    });

    const data = await response.json();
    return res.status(response.status).json(data);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}
