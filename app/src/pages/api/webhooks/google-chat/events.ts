import type { NextApiRequest, NextApiResponse } from 'next';
import axios from 'axios';
import { decodeProtectedHeader, importX509, jwtVerify } from 'jose';

const GOOGLE_CHAT_PROJECT_NUMBER = process.env.GOOGLE_CHAT_PROJECT_NUMBER ?? '';
const GOOGLE_CHAT_CERTS_URL = 'https://www.googleapis.com/service_accounts/v1/metadata/x509/chat@system.gserviceaccount.com';
const GOOGLE_CHAT_ISSUER = 'chat@system.gserviceaccount.com';

// Cache Google's public certificates to avoid fetching on every request.
// Certs rotate infrequently; 30-minute TTL is safe and matches Google's recommendation.
let cachedCerts: Record<string, string> = {};
let certsCachedAt = 0;
const CERTS_TTL_MS = 30 * 60 * 1000;

async function getGoogleCerts(): Promise<Record<string, string>> {
  const now = Date.now();
  if (Object.keys(cachedCerts).length > 0 && now - certsCachedAt < CERTS_TTL_MS) {
    return cachedCerts;
  }
  const response = await axios.get<Record<string, string>>(GOOGLE_CHAT_CERTS_URL);
  cachedCerts = response.data;
  certsCachedAt = now;
  return cachedCerts;
}

async function verifyGoogleChatJwt(token: string): Promise<boolean> {
  try {
    const certs = await getGoogleCerts();

    const header = decodeProtectedHeader(token);
    const kid = header.kid as string;

    const certPem = certs[kid];
    if (!certPem) {
      console.warn(`Google Chat JWT kid "${kid}" not found in fetched certificates`);
      return false;
    }

    const publicKey = await importX509(certPem, header.alg || 'RS256');
    await jwtVerify(token, publicKey, {
      issuer: GOOGLE_CHAT_ISSUER,
      audience: GOOGLE_CHAT_PROJECT_NUMBER,
    });

    return true;
  } catch (err) {
    console.warn('Google Chat JWT verification error:', err);
    return false;
  }
}

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.debug(`Incoming request to google chat events api - Method: ${req.method}`);

    if (req.method !== 'POST') {
      return res.status(405).send('Method Not Allowed');
    }

    // Verify the Bearer JWT — project number must be configured
    if (!GOOGLE_CHAT_PROJECT_NUMBER) {
      console.error('GOOGLE_CHAT_PROJECT_NUMBER not configured — cannot verify Google Chat webhooks');
      return res.status(500).send('Error: Google Chat integration not configured');
    }

    const authHeader = req.headers['authorization'] as string;
    if (!authHeader?.startsWith('Bearer ')) {
      console.warn('Google Chat webhook missing Authorization Bearer token');
      return res.status(401).send('Error: Missing authorization token');
    }

    const token = authHeader.slice('Bearer '.length);
    const isValid = await verifyGoogleChatJwt(token);
    if (!isValid) {
      console.warn('Google Chat webhook JWT verification failed');
      return res.status(401).send('Error: JWT verification failed');
    }

    // Forward to notification service, then respond.
    // Google Chat allows up to 5s — enough for this internal proxy call.
    const endpoint = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
    const response = await axios.post(endpoint + '/webhooks/google-chat/events', req.body);
    console.log(`Response from notification service - Status Code: ${response.status}, Response Body: ${response.data}`);

    return res.status(200).send('OK');
  } catch (err: any) {
    console.error('Google Chat webhook error:', err);
    if (!res.headersSent) {
      return res.status(500).json({ error: err.toString() });
    }
  }
}
