import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';

import type { NextApiRequest, NextApiResponse } from 'next';

import { authOptions } from '@pages/api/auth/[...nextauth]';
import { decrypt } from '@lib/internal';
import crypto from 'crypto';

const unprotected: string[] = [];

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  let traceParent: string;
  const requestIds = req.headers['traceparent'];
  if (requestIds && requestIds.length > 0) {
    if (Array.isArray(requestIds)) {
      traceParent = requestIds[0];
    } else {
      traceParent = requestIds;
    }
  } else {
    const version = Buffer.alloc(1).toString('hex');
    const traceId = crypto.randomBytes(16).toString('hex');
    const id = crypto.randomBytes(8).toString('hex');
    const flags = '01';
    traceParent = `${version}-${traceId}-${id}-${flags}`;
  }

  try {
    const body = req.body;
    const authenticate = shouldAuthenticate(body);

    // check if token is available as bearer token then use it
    const splits = req.headers.authorization ? req.headers.authorization.split(' ') : [];
    let token = splits.length > 1 ? await decrypt(splits[1]) : null;

    const session = await getServerSession(req, res, authOptions);
    const jwtToken = await getToken({ req });
    const tenantId = (jwtToken?.tenant as any)?.id ?? null;

    token = !token && session?.user ? (jwtToken?.idToken as string) : token;

    if (authenticate) {
      if (!token) {
        return sendAuthenticationError(res);
      }
    }
    const notificaionServiceUrl = process.env.NOTIFICATION_SERVICE_URL ? process.env.NOTIFICATION_SERVICE_URL : 'http://notifications:80';
    const installEndpoint = notificaionServiceUrl + '/slack/install?tenant=' + `${tenantId}`;
    const headers: { [key: string]: string } = {
      'Content-Type': 'application/json',
      'tenant-id': `${tenantId}`,
      traceparent: traceParent,
      Authorization: token ? `Bearer ${token}` : '',
    };

    let attempt = 3;
    let proxyResponse = null;
    while (attempt > 0) {
      proxyResponse = await fetch(installEndpoint, {
        headers: headers,
        method: 'get',
      });
      if (proxyResponse.status === 500) {
        const error = await proxyResponse.json();
        if (error['code'] === 'ECONNRESET') {
          console.error('Connection Reset - retrying');
          attempt = attempt - 1;
          continue;
        }
      }

      if (proxyResponse.status === 403) {
        const error = await proxyResponse.json();
        res.status(403).setHeader('traceparent', traceParent).json(error);
        return;
      }
      break;
    }

    await validateAndReturnResponse(proxyResponse, req, res, traceParent);
  } catch (error: any) {
    handleErrorResponse(error, res, traceParent);
  }
}

function sendAuthenticationError(res: NextApiResponse) {
  res.status(401).json({
    error: 'not_authenticated',
    description: 'The user does not have an active session or is not authenticated',
  });
}

function shouldAuthenticate(body: any) {
  let authenticate = true;
  if (unprotected.indexOf(body?.operationName) >= 0 && body?.query?.indexOf('query ' + body?.operationName) >= 0) {
    authenticate = false;
  }
  return authenticate;
}

function handleErrorResponse(error: any, res: NextApiResponse, traceParent: string) {
  console.log('api error', error);
  res
    .status(error.status || 500)
    .setHeader('traceparent', traceParent)
    .json({
      code: error.code,
      error: error.message,
    });
}

async function validateAndReturnResponse(proxyResponse: Response | null, req: NextApiRequest, res: NextApiResponse, traceParent: string) {
  if (proxyResponse != null) {
    const data = await proxyResponse.json();
    if (data?.url) {
      const session = await getServerSession(req, res, authOptions);
      const userEmail = session?.user?.email;
      const redirectUrl = new URL(data.url);

      const rawState = redirectUrl.searchParams.get('state') || '{}';
      let stateObj: Record<string, any> = {};
      try {
        stateObj = { originalState: rawState };
      } catch (e) {
        console.error('Error parsing state', e);
        stateObj = {};
      }

      if (userEmail) {
        stateObj.email = userEmail;
      }

      redirectUrl.searchParams.set('state', JSON.stringify(stateObj));

      return res.setHeader('traceparent', traceParent).redirect(redirectUrl.toString());
    }
  } else {
    res.status(500).setHeader('traceparent', traceParent).json({ error: 'InternalServerError' });
  }
}
