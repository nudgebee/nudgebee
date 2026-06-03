import type { NextApiRequest, NextApiResponse } from 'next';
import jwt, { type JwtPayload } from 'jsonwebtoken';

const ISSUER = 'https://login.microsoftonline.com/common/v2.0';
const JWKS_URI = `${ISSUER}/discovery/v2.0/keys`;
const servicesEndpoint = process.env.SERVICE_API_SERVER_URL ?? 'http://localhost:8000';

async function getSigningKey(kid: string): Promise<string | null> {
  try {
    const response = await fetch(JWKS_URI);
    if (!response.ok) {
      return null;
    }

    const keysJson = await response.json();
    const keys = keysJson.keys;

    const signingKey = keys.find((key: any) => key.kid === kid);
    if (!signingKey) {
      return null;
    }

    const cert = signingKey.x5c[0];
    return `-----BEGIN CERTIFICATE-----\n${cert}\n-----END CERTIFICATE-----`;
  } catch (error) {
    console.error('Error fetching signing keys', error);
    return null;
  }
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const token = req.headers.authorization?.split(' ')[1];
  if (!token) {
    return res.status(401).json({ error: 'No token provided' });
  }
  try {
    const decodedHeader: any = jwt.decode(token, { complete: true });
    const kid = decodedHeader.header.kid;

    const signingKey = await getSigningKey(kid);
    if (!signingKey) {
      return res.status(401).json({ error: 'Invalid token' });
    }
    const requestBody = req.body;
    const payload = jwt.verify(token, signingKey, { algorithms: ['RS256'], issuer: ISSUER }) as JwtPayload;
    console.log('Received webhook payload:', payload, requestBody);
    const response = await fetch(servicesEndpoint + '/marketplace/azure/webhook', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: requestBody,
    });

    if (response.ok) {
      return res.status(200);
    }
    return res.status(500).json({ error: 'Internal Server Error' });
  } catch (err) {
    console.error('Error validating token', err);
    return res.status(401).json({ error: 'Token verification failed', details: err });
  }
}
