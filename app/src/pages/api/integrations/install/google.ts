import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';

import type { NextApiRequest, NextApiResponse } from 'next';

import { authOptions } from '@pages/api/auth/[...nextauth]';
import { decrypt } from '@lib/internal';
import { fetchData, getRequestId, handleErrorResponse, sendAuthenticationError } from 'src/utils/apiUtils';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId: string = getRequestId(req);
  try {
    const splits = req.headers.authorization ? req.headers.authorization.split(' ') : [];
    let token = splits.length > 1 ? await decrypt(splits[1]) : null;

    const session = await getServerSession(req, res, authOptions);
    token = !token && session?.user ? (((await getToken({ req }))?.hasuraIdToken || (await getToken({ req }))?.idToken) as string) : token;

    if (!token) {
      return sendAuthenticationError(res);
    }

    await doRedirect(req, token, requestId, res);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}

async function doRedirect(req: NextApiRequest, token: string | null, requestId: string, res: NextApiResponse) {
  const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:80';
  const url = `${notificationServiceEndpoint}/api/integrations/install/google`;

  try {
    const response = await fetchData(url, token, requestId);
    const data = await response.json();
    if (!data || !data.url) {
      handleErrorResponse(res, new Error('Invalid response from oauth API'), requestId);
      return;
    }
    res.status(302).setHeader('Location', data.url).end();
  } catch (error: any) {
    console.error('Error fetching data:', error);
    handleErrorResponse(res, error, requestId);
  }
}
