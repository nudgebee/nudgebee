import type { NextApiRequest, NextApiResponse } from 'next';
import { createRemoteJWKSet, jwtVerify } from 'jose';

// Marketplace calls this webhook with a client-credentials token that our own
// tenant issues to Microsoft's Marketplace SaaS API app, for the Entra app set in
// Partner Center > Technical configuration. That app is not the NextAuth sign-in
// app (AZURE_AD_CLIENT_ID). EE-only, so set both through additional_env_vars.
const MARKETPLACE_API_APP_ID = '20e940b3-4c77-4b0b-9a53-9e16a1b010a7'; // gitleaks:allow — Microsoft's public Marketplace app ID, not a secret
const servicesEndpoint = process.env.SERVICE_API_SERVER_URL ?? 'http://localhost:8000';

const jwksByTenant = new Map<string, ReturnType<typeof createRemoteJWKSet>>();

function getJwks(tenantId: string): ReturnType<typeof createRemoteJWKSet> {
  // createRemoteJWKSet caches keys and refetches on an unknown kid.
  let jwks = jwksByTenant.get(tenantId);
  if (!jwks) {
    jwks = createRemoteJWKSet(new URL(`https://login.microsoftonline.com/${tenantId}/discovery/v2.0/keys`));
    jwksByTenant.set(tenantId, jwks);
  }
  return jwks;
}

async function verifyMarketplaceToken(token: string, appId: string, tenantId: string): Promise<string | null> {
  try {
    const { payload } = await jwtVerify(token, getJwks(tenantId), {
      algorithms: ['RS256'],
      audience: appId,
      // v1 and v2 access tokens from the same tenant carry different issuers.
      issuer: [`https://sts.windows.net/${tenantId}/`, `https://login.microsoftonline.com/${tenantId}/v2.0`],
    });
    // azp on v2 tokens, appid on v1: the caller must be Marketplace itself, not
    // any other app that can get a token for ours.
    const caller = payload.azp ?? payload.appid;
    if (caller !== MARKETPLACE_API_APP_ID) {
      return `unexpected caller app ${String(caller)}`;
    }
    return null;
  } catch (err) {
    return err instanceof Error ? err.message : String(err);
  }
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    return res.status(405).json({ error: 'Method Not Allowed' });
  }

  const appId = process.env.AZURE_MARKETPLACE_APP_ID ?? '';
  const tenantId = process.env.AZURE_MARKETPLACE_TENANT_ID ?? '';
  if (!appId || !tenantId) {
    console.error('AZURE_MARKETPLACE_APP_ID / AZURE_MARKETPLACE_TENANT_ID not configured — cannot verify Azure marketplace webhooks');
    return res.status(503).json({ error: 'Azure marketplace integration not configured' });
  }

  const authHeader = req.headers.authorization;
  if (!authHeader?.startsWith('Bearer ')) {
    console.warn('Azure marketplace webhook missing Authorization Bearer token');
    return res.status(401).json({ error: 'No token provided' });
  }

  const failure = await verifyMarketplaceToken(authHeader.slice('Bearer '.length), appId, tenantId);
  if (failure) {
    console.warn('Azure marketplace webhook token verification failed', { reason: failure });
    return res.status(401).json({ error: 'Token verification failed' });
  }

  const requestBody = req.body;
  console.log('Received Azure marketplace webhook', { action: requestBody?.action, subscriptionId: requestBody?.subscriptionId });
  try {
    const response = await fetch(servicesEndpoint + '/marketplace/azure/webhook', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(requestBody),
    });
    if (response.ok) {
      return res.status(200).end();
    }
    console.error('Azure marketplace webhook rejected by api-server', { status: response.status });
  } catch (err) {
    console.error('Error forwarding Azure marketplace webhook', err);
  }
  return res.status(500).json({ error: 'Internal Server Error' });
}
