import crypto from 'crypto';
import type { NextApiRequest, NextApiResponse } from 'next';

import { generateGithubAppJwt, getRequestId, sendAuthenticationError } from '@utils/apiUtils';
import { authenticateRequest, tryBypassGraphQL } from '@lib/rpcGateway';
import { getAppBaseUrl } from '@lib/externalUrls';

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

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId = getRequestId(req);

  try {
    const auth = await authenticateRequest(req);
    if (!auth?.jwt) {
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
      jwt: auth.jwt,
      clientAuthorization: auth.token ? `Bearer ${auth.token}` : undefined,
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
      return res.send(`
        <html lang="en">
          <body>
            <script>
              if (window.opener) {
                window.opener.postMessage({ type: 'GITHUB_AUTH_ERROR', error: 'failed_to_add_github_account' }, '${origin}');
                window.close();
              } else {
                window.location.href = '${origin}?error=failed_to_add_github_account';
              }
            </script>
          </body>
        </html>
      `);
    }

    return res.send(`
      <html lang="en">
        <body>
          <script>
            if (window.opener) {
              window.opener.postMessage({ type: 'GITHUB_AUTH_SUCCESS' }, '${origin}');
              window.close();
            } else {
              window.location.href = '${origin}';
            }
          </script>
        </body>
      </html>
    `);
  } catch (error: any) {
    console.error('GitHub callback error:', error);
    res.status(500).json({
      error: error.message || 'Internal Server Error',
      requestId,
    });
  }
}
