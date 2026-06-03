import crypto from 'crypto';
import type { NextApiRequest, NextApiResponse } from 'next';
import type { JWT } from 'next-auth/jwt';

import { generateGithubAppJwt, getRequestId, sendAuthenticationError } from '@utils/apiUtils';
import { authenticateRequest, tryBypassGraphQL } from '@lib/rpcGateway';
import { decrypt } from '@lib/internal';
import { getAppBaseUrl } from '@lib/externalUrls';

// Identity travels through the OAuth `state` parameter (signed by
// /api/integrations/github/install), not the session cookie — the cookie is
// unreliable here because GitHub redirects into the popup cross-site. Recover
// the JWT shape buildSessionVariables() expects from `state`; reject stale or
// tampered values. Returns null so the caller can fall back to the cookie.
const STATE_MAX_AGE_MS = 10 * 60 * 1000;

async function jwtFromState(rawState: string | string[] | undefined): Promise<JWT | null> {
  const state = Array.isArray(rawState) ? rawState[0] : rawState;
  if (!state) return null;
  try {
    const payload = JSON.parse(await decrypt(state)) as {
      id?: string;
      tenant_id?: string;
      roles?: string[];
      isSuperAdmin?: boolean;
      isSuperAdminReadonly?: boolean;
      ts?: number;
    };
    if (!payload.id || !payload.tenant_id) return null;
    if (typeof payload.ts !== 'number' || Date.now() - payload.ts > STATE_MAX_AGE_MS) return null;
    return {
      id: payload.id,
      sub: payload.id,
      tenant: { id: payload.tenant_id },
      roles: Array.isArray(payload.roles) ? payload.roles : [],
      isSuperAdmin: !!payload.isSuperAdmin,
      isSuperAdminReadonly: !!payload.isSuperAdminReadonly,
    } as unknown as JWT;
  } catch {
    // tampered/stale/legacy state — fall through to the cookie path
    return null;
  }
}

export const CREATE_INTEGRATION = `
mutation CreateIntegration($object: ticket_integration_create_config_input!) {
  ticket_integration_create_config(object: $object) {
    id
  }
}
`;

async function fetchInstallation(installationId: string) {
  const jwt = generateGithubAppJwt();
  const res = await fetch(`https://api.github.com/app/installations/${installationId}`, {
    headers: {
      Authorization: `Bearer ${jwt}`,
      Accept: 'application/vnd.github+json',
    },
  });

  if (!res.ok) {
    throw new Error(`Failed to fetch installation: ${res.status} ${res.statusText}`);
  }

  return (await res.json()) as { id: number; account: { login: string } };
}

function generateTraceparent(): string {
  const version = Buffer.alloc(1).toString('hex');
  const traceId = crypto.randomBytes(16).toString('hex');
  const id = crypto.randomBytes(8).toString('hex');
  return `${version}-${traceId}-${id}-01`;
}

// Render the popup-closing page. The Content-Type header is required: without
// it the browser shows the markup as plain text instead of running the script,
// so the popup never posts its result back nor closes. Values are embedded via
// JSON.stringify so they're correctly quoted/escaped inside the inline script.
function sendPopupResult(res: NextApiResponse, origin: string, success: boolean): void {
  const message = success ? { type: 'GITHUB_AUTH_SUCCESS' } : { type: 'GITHUB_AUTH_ERROR', error: 'failed_to_add_github_account' };
  const fallbackHref = success ? origin : `${origin}?error=failed_to_add_github_account`;
  res.setHeader('Content-Type', 'text/html; charset=utf-8').send(`
    <html lang="en">
      <body>
        <script>
          if (window.opener) {
            window.opener.postMessage(${JSON.stringify(message)}, ${JSON.stringify(origin)});
            window.close();
          } else {
            window.location.href = ${JSON.stringify(fallbackHref)};
          }
        </script>
      </body>
    </html>
  `);
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId = getRequestId(req);

  try {
    // Identity comes from the signed `state` (cookie-independent). Fall back
    // to the session cookie for legacy links and during the migration window.
    let jwt = await jwtFromState(req.query.state);
    let clientAuthorization: string | undefined;
    if (!jwt) {
      const auth = await authenticateRequest(req);
      if (auth?.jwt) {
        jwt = auth.jwt;
        clientAuthorization = auth.token ? `Bearer ${auth.token}` : undefined;
      }
    }
    if (!jwt) {
      return sendAuthenticationError(res);
    }

    const installationId = req.query.installation_id as string;
    if (!installationId) {
      throw new Error('Missing installation_id from GitHub callback');
    }

    const installation = await fetchInstallation(installationId);

    const origin = getAppBaseUrl();

    const variables = {
      object: {
        name: installation.account.login,
        url: 'api.github.com',
        username: installation.account.login,
        password: installation.id.toString(),
        tool: 'github',
        auth_type: 'application',
      },
    };

    const result = await tryBypassGraphQL({
      query: CREATE_INTEGRATION,
      variables,
      jwt,
      clientAuthorization,
      traceparent: generateTraceparent(),
      requestId,
    });

    if (!result.handled) {
      throw new Error(`Error saving github app integration: ${result.reason}`);
    }

    const data = result.body.data as { ticket_integration_create_config?: { id?: string } } | null;
    const integrationCreated = data?.ticket_integration_create_config?.id;

    // Fail closed: report success only when the ticket-server returned an id.
    if (!integrationCreated) {
      console.error('GitHub integration not created', { requestId, errors: result.body.errors });
      return sendPopupResult(res, origin, false);
    }

    return sendPopupResult(res, origin, true);
  } catch (error: any) {
    console.error('GitHub callback error:', error);
    res.status(500).json({
      error: error.message || 'Internal Server Error',
      requestId,
    });
  }
}
