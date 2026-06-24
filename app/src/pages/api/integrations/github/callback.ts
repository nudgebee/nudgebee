import crypto from 'crypto';
import type { NextApiRequest, NextApiResponse } from 'next';

import { generateGithubAppJwt, getRequestId, sendAuthenticationError } from '@utils/apiUtils';
import { tryBypassGraphQL } from '@lib/rpcGateway';
import { decodeIdentityState } from '@lib/integrationState';
import { resolveRequestAuth } from '@lib/sessionToken';
import { getAppBaseUrl } from '@lib/externalUrls';

export const CREATE_INTEGRATION = `
mutation CreateIntegration($object: ticket_integration_create_config_input!) {
  ticket_integration_create_config(object: $object) {
    id
  }
}
`;

async function fetchInstallation(installationId: string) {
  // installationId arrives from the OAuth callback query string. Validate it
  // is a positive integer before interpolating into the GitHub API URL —
  // anything else (path traversal, scheme tricks) cannot reach api.github.com.
  if (!/^\d+$/.test(installationId)) {
    throw new Error(`Invalid installation_id: ${installationId}`);
  }
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
    // Tenant is authoritative from the installer's own session, never the
    // redirect. The signed `state` is a CSRF token: it must decrypt and its
    // tenant must match the session, blocking cross-tenant state injection and
    // OAuth installation/code injection.
    const auth = await resolveRequestAuth(req);
    const tenantId = ((auth?.jwt?.tenant as { id?: string } | undefined)?.id as string) || null;
    if (!auth?.jwt || !tenantId) {
      return sendAuthenticationError(res);
    }
    const signed = await decodeIdentityState(req.query.state);
    if (!signed || signed.tenant_id !== tenantId) {
      return res.status(400).json({ error: 'invalid_state', description: 'State missing, expired, or tenant mismatch' });
    }
    const jwt = auth.jwt;
    const clientAuthorization = auth.token ? `Bearer ${auth.token}` : undefined;

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
