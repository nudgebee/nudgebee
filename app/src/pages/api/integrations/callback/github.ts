import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';
import type { NextApiRequest, NextApiResponse } from 'next';

import { authOptions } from '@pages/api/auth/[...nextauth]';
import { decrypt } from '@lib/internal';
import { generateGithubAppJwt, getRequestId, sendAuthenticationError } from '@utils/apiUtils';
import { getGQLEndpoint } from '@lib/HttpService';
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

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const requestId = getRequestId(req);

  try {
    const authHeader = req.headers.authorization || '';
    const splits = authHeader.split(' ');
    let token: string | null = splits.length > 1 ? await decrypt(splits[1]) : null;

    const session = await getServerSession(req, res, authOptions);
    if (!token && session?.user) {
      const tokenObj = await getToken({ req });
      token = (tokenObj?.idToken as string) || null;
    }

    if (!token) {
      return sendAuthenticationError(res);
    }

    const installationId = req.query.installation_id as string;
    if (!installationId) {
      throw new Error('Missing installation_id from GitHub callback');
    }

    const installation = await fetchInstallation(installationId);

    const origin = getAppBaseUrl();

    const bodyData = {
      object: {
        name: installation.account.login,
        url: 'api.github.com',
        username: installation.account.login,
        password: installation.id.toString(),
        tool: 'github',
        auth_type: 'application',
      },
    };

    const apiResponse = await saveIntegrationToDb(bodyData, token);
    const integrationCreated = apiResponse.data?.data?.ticket_integration_create_config?.id;
    const errors = apiResponse.data?.errors || [];

    if (integrationCreated) {
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
    }

    if (errors.length > 0) {
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

interface IntegrationResponse {
  data?: {
    data?: {
      ticket_integration_create_config?: {
        id: string;
      };
    };
    errors?: any[];
  };
}

async function saveIntegrationToDb(data: Record<string, any>, token: string): Promise<IntegrationResponse> {
  try {
    const response = await fetch(getGQLEndpoint(), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({
        query: CREATE_INTEGRATION,
        variables: data,
      }),
    });

    const result = await response.json();
    return { data: result } as IntegrationResponse;
  } catch (err: any) {
    throw new Error('Error saving github app integration: ' + err.message);
  }
}
