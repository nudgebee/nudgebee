import type { NextApiRequest, NextApiResponse } from 'next';
import { getSamlService } from '@lib/samlServiceFactory';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'GET') {
    return res.status(405).json({ error: 'Method not allowed' });
  }

  console.info('[SAML:Login] Login request received');

  const saml = getSamlService();
  if (!saml) {
    console.error('[SAML:Login] SAML service not available — check SAML configuration env vars');
    return res.status(400).json({ error: 'SAML not configured' });
  }

  try {
    const host = req.headers.host;
    console.info(`[SAML:Login] Generating SAML authorize URL — host=${host}`);
    const loginUrl = await saml.getAuthorizeUrl(host);
    console.info(`[SAML:Login] Redirecting to IdP`);
    return res.redirect(302, loginUrl);
  } catch (err: any) {
    console.error(`[SAML:Login] FAILED to generate authorize URL — error=${err.message}`, err.stack);
    return res.status(500).json({ error: 'Failed to start SAML flow', message: err.message });
  }
}
