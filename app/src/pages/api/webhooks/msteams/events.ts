import type { NextApiRequest, NextApiResponse } from 'next';
import axios from 'axios';
import { createRemoteJWKSet, jwtVerify } from 'jose';

// Bot Framework authenticates inbound activities with a Bearer JWT (not an
// x-ms-signature body HMAC); for public-cloud Teams it is signed by
// login.botframework.com and issued by https://api.botframework.com.
const MS_TEAMS_CLIENT_ID = process.env.MS_TEAMS_CLIENT_ID ?? '';
const BOTFRAMEWORK_OPENID_URL = 'https://login.botframework.com/v1/.well-known/openidconfiguration';
const BOTFRAMEWORK_ISSUER = 'https://api.botframework.com';

let cachedJwks: ReturnType<typeof createRemoteJWKSet> | null = null;

async function getBotFrameworkJwks(): Promise<ReturnType<typeof createRemoteJWKSet>> {
  // createRemoteJWKSet caches keys and refetches on an unknown kid, so the
  // jwks_uri only needs resolving from the OpenID document once.
  if (cachedJwks) {
    return cachedJwks;
  }
  const response = await axios.get<{ jwks_uri: string }>(BOTFRAMEWORK_OPENID_URL, { timeout: 5000 });
  const jwksUri = response.data.jwks_uri;
  if (!jwksUri) {
    throw new Error('Bot Framework OpenID metadata missing jwks_uri');
  }
  cachedJwks = createRemoteJWKSet(new URL(jwksUri));
  return cachedJwks;
}

async function verifyBotFrameworkJwt(token: string, activityServiceUrl: unknown): Promise<boolean> {
  try {
    const jwks = await getBotFrameworkJwks();
    const { payload } = await jwtVerify(token, jwks, {
      issuer: BOTFRAMEWORK_ISSUER,
      audience: MS_TEAMS_CLIENT_ID,
      algorithms: ['RS256'],
      clockTolerance: 300, // 5-min skew, matching the Bot Framework spec
    });

    // The serviceurl claim binds the URL the bot may reply to; require it to be
    // present and match the activity (default-deny on a missing claim/field).
    const claimUrl = typeof payload.serviceurl === 'string' ? payload.serviceurl.replace(/\/+$/, '').toLowerCase() : '';
    const activityUrl = typeof activityServiceUrl === 'string' ? activityServiceUrl.replace(/\/+$/, '').toLowerCase() : '';
    if (!claimUrl || !activityUrl || claimUrl !== activityUrl) {
      console.warn('MS Teams JWT serviceurl claim missing or does not match activity serviceUrl');
      return false;
    }

    return true;
  } catch (err) {
    console.warn('MS Teams JWT verification error:', err);
    return false;
  }
}

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.debug(`Incoming request to ms teams events api - Method: ${req.method}`);

    if (req.method !== 'POST') {
      return res.status(405).send('Method Not Allowed');
    }

    if (!MS_TEAMS_CLIENT_ID) {
      console.error('MS_TEAMS_CLIENT_ID not configured — cannot verify MS Teams webhooks');
      return res.status(503).send('Error: MS Teams integration not configured');
    }

    const authHeader = req.headers['authorization'];
    const authString = Array.isArray(authHeader) ? authHeader[0] : authHeader;
    if (!authString?.startsWith('Bearer ')) {
      console.warn('MS Teams webhook missing Authorization Bearer token');
      return res.status(401).send('Error: Missing authorization token');
    }

    const payload = req.body;
    const token = authString.slice('Bearer '.length);
    const isValid = await verifyBotFrameworkJwt(token, payload?.serviceUrl);
    if (!isValid) {
      console.warn('MS Teams webhook JWT verification failed');
      return res.status(401).send('Error: JWT verification failed');
    }

    if (payload.type === 'message' && payload.text === 'verify') {
      return res.status(200).send('OK');
    }

    res.status(200).send('OK');

    const endpoint = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
    const response = await axios.post(endpoint + '/webhooks/msteams/events', req.body, {
      headers: { 'X-ACTION-TOKEN': process.env.ACTION_API_SERVER_TOKEN ?? '' },
      timeout: 5000,
    });
    console.log(`Response from notification service - Status Code: ${response.status}, Response Body: ${response.data}`);

    return;
  } catch (err: any) {
    console.error(err);
    if (!res.headersSent) {
      return res.status(500).json({ error: err.toString() });
    }
  }
}
