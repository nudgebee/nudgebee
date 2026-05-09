import type { NextApiRequest, NextApiResponse } from 'next';
import { v4 as uuidv4 } from 'uuid';
import type { Session } from 'next-auth';
import jwt from 'jsonwebtoken';

const APP_ID = process.env.GITHUB_APP_ID;
if (!APP_ID) {
  console.warn('GITHUB_APP_ID environment variable is not set. GitHub App functionality will be disabled.');
}

let PRIVATE_KEY = process.env.GITHUB_PRIVATE_KEY;
if (!PRIVATE_KEY) {
  console.warn('GITHUB_PRIVATE_KEY environment variable is not set. GitHub App functionality will be disabled.');
} else {
  PRIVATE_KEY = PRIVATE_KEY.replace(/\\n/g, '\n');
}

export function getRequestId(req: NextApiRequest): string {
  const requestIds = req.headers['x-request-id'];
  return requestIds && requestIds.length > 0 ? (Array.isArray(requestIds) ? requestIds[0] : requestIds) : uuidv4();
}

export function sendAuthenticationError(res: NextApiResponse): void {
  res.status(401).json({
    error: 'not_authenticated',
    description: 'The user does not have an active session or is not authenticated',
  });
}

export async function fetchData(url: string, token: string | null, requestId: string): Promise<Response> {
  const response = await fetch(url, {
    method: 'GET',
    headers: {
      Authorization: token ? `Bearer ${token}` : '',
      'x-request-id': requestId,
    },
  });

  if (!response.ok) {
    const message = `An error occurred: ${response.status}`;
    throw new Error(message);
  }

  return response;
}

export function handleErrorResponse(res: NextApiResponse, error: any, requestId: string): void {
  console.error('API error:', error);
  res
    .status(error.status || 500)
    .setHeader('x-request-id', requestId)
    .json({
      code: error.code || 'internal_error',
      error: error.message || 'Internal Server Error',
    });
}

export function getIdsFromSession(session: Session | null): { userEmail: string } {
  let userEmail = 'system';
  if (session) {
    const sessionJson = JSON.parse(JSON.stringify(session));
    userEmail = sessionJson.user.email;
  }
  return { userEmail };
}

export async function handleOAuthCallbackResponse(proxyResponse: Response | null, res: NextApiResponse, requestId: string): Promise<void> {
  if (proxyResponse !== null) {
    const contentType = proxyResponse.headers.get('content-type');
    let data = await proxyResponse.text();

    // Inject auto-close script for OAuth popup
    const autoCloseScript = '<script>setTimeout(function(){ window.close(); }, 2000);</script>';

    if (contentType?.includes('text/html')) {
      // Inject script before </body> or append to end
      if (data.includes('</body>')) {
        data = data.replace('</body>', autoCloseScript + '</body>');
      } else {
        data = data + autoCloseScript;
      }
      res
        .status(proxyResponse.status || 200)
        .setHeader('x-request-id', requestId)
        .setHeader('Content-Type', 'text/html')
        .send(data);
    } else {
      try {
        const jsonData = JSON.parse(data);
        // For JSON responses, wrap in HTML with auto-close
        const html = `<!DOCTYPE html><html><body><pre>${JSON.stringify(jsonData, null, 2)}</pre>${autoCloseScript}</body></html>`;
        res
          .status(proxyResponse.status || 200)
          .setHeader('x-request-id', requestId)
          .setHeader('Content-Type', 'text/html')
          .send(html);
      } catch (e: unknown) {
        console.error(e);
        res.status(500).setHeader('x-request-id', requestId).json({ error: 'InternalServerError' });
      }
    }
  } else {
    res.status(500).setHeader('x-request-id', requestId).json({ error: 'InternalServerError' });
  }
}

export function generateGithubAppJwt(): string {
  if (!APP_ID || !PRIVATE_KEY) {
    throw new Error('GitHub App credentials not configured. Cannot generate JWT token.');
  }

  const now = Math.floor(Date.now() / 1000);

  const payload = {
    iat: now - 60,
    exp: now + 10 * 60, // valid for 10 minutes
    iss: APP_ID,
  };
  return jwt.sign(payload, PRIVATE_KEY, { algorithm: 'RS256' });
}
