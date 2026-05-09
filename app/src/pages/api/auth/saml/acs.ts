import type { NextApiRequest, NextApiResponse } from 'next';
import crypto from 'crypto';
import { getSamlService } from '@lib/samlServiceFactory';
import { findOrCreateSamlUser } from '@lib/samlUserAdapter';

// Token expires in 60 seconds - only valid for redirect from ACS to session endpoint
const TOKEN_EXPIRY_SECONDS = 60;

function signPayload(payload: object, secret: string): string {
  const timestamp = Math.floor(Date.now() / 1000);
  const data = JSON.stringify({ ...payload, ts: timestamp });
  const signature = crypto.createHmac('sha256', secret).update(data).digest('hex');
  return Buffer.from(JSON.stringify({ data, signature })).toString('base64');
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    return res.status(405).json({ error: 'Method not allowed' });
  }

  console.info('[SAML:ACS] ACS callback received (POST)');

  const saml = getSamlService();
  if (!saml) {
    console.error('[SAML:ACS] SAML service not available — check SAML configuration env vars');
    return res.redirect('/signin?error=saml_not_configured');
  }

  const secret = process.env.NEXTAUTH_SECRET;
  if (!secret) {
    console.error('[SAML:ACS] NEXTAUTH_SECRET not configured — cannot sign session payload');
    return res.redirect('/signin?error=server_misconfiguration');
  }

  try {
    // decode and log SAML response structure for signature diagnosis — REMOVE after fix
    if (req.body?.SAMLResponse) {
      const xml = Buffer.from(req.body.SAMLResponse, 'base64').toString('utf-8');
      console.info(`[SAML:DEBUG] SAMLResponse XML:\n${xml}`);
    }
    console.info('[SAML:ACS] Validating SAML response from IdP...');
    const { user: samlUser } = await saml.validatePostResponse(req.body);

    console.info(`[SAML:ACS] SAML user validated — email=${samlUser.email}. Looking up or creating app user...`);
    const appUser = await findOrCreateSamlUser(samlUser);
    if (!appUser) {
      console.warn(`[SAML:ACS] User DENIED access — email=${samlUser.email}. User not found, suspended, or domain not authorized.`);
      return res.redirect('/signin?error=user_not_allowed&message=User is suspended or not authorized for this application');
    }

    if (appUser.status === 'suspended') {
      console.warn(`[SAML:ACS] User SUSPENDED — email=${samlUser.email}, userId=${appUser.id}`);
      return res.redirect('/signin?error=user_suspended&message=Your account has been suspended');
    }

    console.info(`[SAML:ACS] Login SUCCESS — email=${samlUser.email}, userId=${appUser.id}. Creating session...`);
    const signedPayload = signPayload({ id: appUser.id, email: appUser.email, provider: 'okta_saml' }, secret);

    return res.redirect(`/api/auth/saml/session?user=${encodeURIComponent(signedPayload)}`);
  } catch (err: any) {
    console.error(`[SAML:ACS] FAILED — error=${err.message}`, err.stack);
    return res.redirect(`/signin?error=saml_error&message=${encodeURIComponent(err.message || 'SAML authentication failed')}`);
  }
}

export { TOKEN_EXPIRY_SECONDS };
