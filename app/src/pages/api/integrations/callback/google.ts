import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';

import type { NextApiRequest, NextApiResponse } from 'next';

import { authOptions } from '@pages/api/auth/[...nextauth]';
import { decrypt } from '@lib/internal';
import querystring from 'querystring';
import { getIdsFromSession, getRequestId, handleOAuthCallbackResponse, sendAuthenticationError } from '@utils/apiUtils';

const unprotected: string[] = [];

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId: string = getRequestId(req);
  try {
    const body = req.body;
    const authenticate = shouldAuthenticate(body);
    // check if token is available as bearer token then use it
    const splits = req.headers.authorization ? req.headers.authorization.split(' ') : [];
    let token = splits.length > 1 ? await decrypt(splits[1]) : null;

    const session = await getServerSession(req, res, authOptions);
    const { userEmail } = getIdsFromSession(session);

    const jwtToken = await getToken({ req });
    const tenantId = (jwtToken?.tenant as any)?.id ?? null;
    token = !token && session?.user ? ((jwtToken?.hasuraIdToken || jwtToken?.idToken) as string) : token;

    if (authenticate) {
      if (!token) {
        return sendAuthenticationError(res);
      }
    }

    await doRedirect(req, token, requestId, tenantId, userEmail, res);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}

async function doRedirect(
  req: NextApiRequest,
  token: string | null,
  requestId: string,
  tenantId: string | null,
  userEmail: string | null,
  res: NextApiResponse
) {
  const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL ? process.env.NOTIFICATION_SERVICE_URL : 'http://notifications:80';
  const queryParams = querystring.stringify(req.query);
  const url = notificationServiceEndpoint + '/api/integrations/callback/google?' + queryParams;
  const code = req.query.code;
  await redirectOauthToNotificationService(url, token, requestId, code, tenantId, userEmail, res);
}

function shouldAuthenticate(body: any) {
  let authenticate = true;
  if (unprotected.indexOf(body?.operationName) >= 0 && body?.query?.indexOf('query ' + body?.operationName) >= 0) {
    authenticate = false;
  }
  return authenticate;
}

async function redirectOauthToNotificationService(
  url: string,
  token: string | null,
  requestId: string,
  code: string | string[] | undefined,
  tenantId: any,
  userEmail: string | null,
  res: NextApiResponse
) {
  let attempt = 3;
  let proxyResponse = null;
  if (userEmail == null) {
    userEmail = 'system';
  }
  while (attempt > 0) {
    proxyResponse = await fetchAndGetResponse(url, token, requestId, code, tenantId, userEmail);
    if (proxyResponse.status === 500 && (await proxyResponse.json()).code === 'ECONNRESET') {
      console.error('Connection Reset - retrying');
      attempt -= 1;
      continue;
    }
    break;
  }
  await handleOAuthCallbackResponse(proxyResponse, res, requestId);
}

async function fetchAndGetResponse(
  url: string,
  token: string | null,
  requestId: string,
  code: string | string[] | undefined,
  tenantId: any,
  userEmail: string
) {
  return await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      Authorization: token ? `Bearer ${token}` : '',
      'x-request-id': requestId,
      'x-user-email': userEmail,
    },
    body: JSON.stringify({ code, tenantId }),
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
