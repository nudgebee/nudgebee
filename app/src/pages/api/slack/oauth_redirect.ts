import { v4 as uuidv4 } from 'uuid';
import type { NextApiRequest, NextApiResponse } from 'next';
import { handleOAuthCallbackResponse } from '@utils/apiUtils';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId: string = getRequestId(req);
  try {
    await doRedirect(req, requestId, res);
  } catch (error: any) {
    handleErrorResponse(res, error, requestId);
  }
}

async function doRedirect(req: NextApiRequest, requestId: string, res: NextApiResponse) {
  const notificationServiceEndpoint = process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:80';
  const url = `${notificationServiceEndpoint}/slack/oauth_redirect`;

  const rawCode = req.query.code;
  const rawState = req.query.state;

  const code = Array.isArray(rawCode) ? rawCode[0] : rawCode;
  const state = Array.isArray(rawState) ? rawState[0] : rawState;

  if (!code || !state) {
    return res.status(400).setHeader('x-request-id', requestId).json({ error: 'Missing code or state' });
  }

  let stateObj: { originalState?: string; email?: string; [k: string]: any } = {};
  try {
    stateObj = JSON.parse(state);
  } catch (e) {
    console.error('Error parsing state', e);
    stateObj.originalState = state;
  }
  const userEmail = stateObj.email;

  const params = new URLSearchParams();
  params.set('code', code);
  params.set('state', stateObj.originalState ?? state);

  const fullUrl = `${url}?${params.toString()}`;

  const incomingTrace = req.headers['traceparent'];
  const traceparent = Array.isArray(incomingTrace) ? incomingTrace[0] : incomingTrace ?? requestId;

  const headers: Record<string, string> = {
    'x-request-id': requestId,
    traceparent: traceparent,
  };
  if (userEmail) {
    headers['x-user-email'] = userEmail;
  }
  const proxyResponse = await fetch(fullUrl, {
    method: 'GET',
    headers,
  });

  await handleOAuthCallbackResponse(proxyResponse, res, requestId);
}

function getRequestId(req: NextApiRequest): string {
  const requestIds = req.headers['x-request-id'];
  return requestIds && requestIds.length > 0 ? (Array.isArray(requestIds) ? requestIds[0] : requestIds) : uuidv4();
}

function handleErrorResponse(res: NextApiResponse, error: any, requestId: string): void {
  console.error('API error:', error);
  res
    .status(error.status || 500)
    .setHeader('x-request-id', requestId)
    .json({
      code: error.code || 'internal_error',
      error: error.message || 'Internal Server Error',
    });
}
