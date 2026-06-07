import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';

import type { NextApiRequest, NextApiResponse } from 'next';

import { authOptions } from '@pages/api/auth/[...nextauth]';
import { hasAccountAccess } from '@lib/accountAccess';
import crypto from 'crypto';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  let traceParent: string;
  const { grafana } = req.query;
  const requestIds = req.headers['traceparent'];
  if (requestIds && requestIds.length > 0) {
    if (Array.isArray(requestIds)) {
      traceParent = requestIds[0];
    } else {
      traceParent = requestIds;
    }
  } else {
    const version = Buffer.alloc(1).toString('hex');
    const traceId = crypto.randomBytes(16).toString('hex');
    const id = crypto.randomBytes(8).toString('hex');
    const flags = '01';
    traceParent = `${version}-${traceId}-${id}-${flags}`;
  }

  try {
    // Reject bearer auth at proxy endpoints: bearer tokens are intended for
    // /rpc/* and /graphql only. Allowing them here would let any bearer-bearing
    // caller pivot across tenants by claiming an arbitrary X-NB-ACCOUNT-ID in
    // the URL path (the relay grafana handler routes by that header without
    // re-checking per-user authz).
    if (req.headers.authorization) {
      res.status(401).json({
        error: 'auth_method_not_allowed',
        description: 'Bearer auth is for /rpc and /graphql endpoints only; use the session cookie for proxy endpoints',
      });
      return;
    }

    let token: string | null = null;
    const userDetails = {
      userId: '',
      tenantId: '',
    };

    const session = await getServerSession(req, res, authOptions);
    if (session && session?.user) {
      const jwtToken = await getToken({ req });
      if (jwtToken) {
        userDetails.userId = jwtToken?.sub as string;
        userDetails.tenantId = (jwtToken?.tenant as any)?.id as string;
      }
      token = (jwtToken?.idToken as string) || null;
    }

    if (!token || !userDetails.userId || !userDetails.tenantId) {
      res.status(401).json({
        error: 'not_authenticated',
        description: 'The user does not have an active session or is not authenticated',
      });
      return;
    }
    const relayEndpoint = process.env.RELAY_SERVER_ENDPOINT ?? 'http://localhost:52832';
    const secretKey = process.env.RELAY_SERVER_SECRET_KEY ?? '';

    if (!secretKey || secretKey.trim() === '') {
      throw new Error('Authentication is empty or undefined');
    }

    const headers: { [key: string]: string } = {
      'Content-Type': 'application/json',
      traceparent: traceParent,
      'X-SECRET-KEY': secretKey,
      'X-USER-ID': userDetails.userId,
      'X-TENANT-ID': userDetails.tenantId,
      'X-NB-REQUEST-ID': crypto.randomUUID(),
    };

    for (const k of Object.keys(req.headers)) {
      if (k.toLowerCase().startsWith('x-')) {
        headers[k] = req.headers[k] as string;
      }
    }

    let attempt = 3;
    try {
      while (attempt > 0) {
        try {
          let endpoint = 'grafana/';
          let accountId = '';

          if (grafana) {
            if (Array.isArray(grafana)) {
              accountId = grafana[0].replace('gr-', '');
              endpoint += grafana.join('/').replace(accountId + '/', '');
            } else {
              endpoint = grafana;
            }
          }

          // Per-tenant account-level authz. The relay's /grafana/* handler
          // routes by X-NB-ACCOUNT-ID without re-checking the calling user's
          // access; without this gate, a tenant-A user could request
          // `/api/proxy/grafana/gr-<tenant-B-account>/...` and receive
          // tenant B's grafana data. Mirrors the check in [relay].ts.
          if (!accountId || !(await hasAccountAccess(userDetails.userId, userDetails.tenantId, accountId, traceParent))) {
            res.status(403).json({ error: 'forbidden', description: 'Access denied' });
            return;
          }

          headers['X-NB-ACCOUNT-ID'] = accountId;
          const options: any = {
            headers: headers,
            method: req.method,
            query: req.query,
          };

          const nextRequestMeta = Object.getOwnPropertySymbols(req).find((s) => {
            return s.description === 'NextInternalRequestMeta';
          }) as keyof NextApiRequest;
          if (nextRequestMeta) {
            const metadata: any = req[nextRequestMeta];
            const initUrl = metadata?.initURL;
            if (initUrl) {
              const splits = initUrl.split('?');
              if (splits.length > 1) {
                endpoint = endpoint + '?' + splits[1];
              }
            }
          }

          // do not send map files to relay
          if (req.method == 'GET' && endpoint.endsWith('.js.map')) {
            res.status(200).setHeader('traceparent', traceParent).send('{}');
            return;
          }

          if (req.method != 'GET') {
            options.body = JSON.stringify(req.body);
          }
          const response = await fetch(relayEndpoint + '/' + endpoint, options);

          if (response.ok) {
            const contentType = response.headers.get('content-type');

            if (contentType && contentType.includes('application/json')) {
              // Handle JSON response
              const data = await response.json();
              res
                .status(response.status || 200)
                .setHeader('traceparent', traceParent)
                .json(data);
              return;
            }
            // Handle non-JSON response
            for (const [k, v] of response.headers.entries()) {
              if (k == 'origin' || k?.toLowerCase() == 'x-frame-options') {
                continue;
              }
              res.setHeader(k, v);
            }

            if (response.headers.get('content-type')?.includes('text/html')) {
              let data = await response.text();
              if (data.includes('<base href=')) {
                data = data.replace(/<base href="\/"\s*\/>/, `<base href="${process.env.BASE_URL}/api/proxy/grafana/${accountId}/"/>`);
                data = data.replace('"liveEnabled":true', `"liveEnabled":false`);
                data = data.replace('"gravatarUrl":"/avatar/', `"gravatarUrl":"avatar/`);
                data = data.replace('"img":"/avatar/', `"img":"avatar/`);
                data = data.replace('"url":"/admin', `"url":"admin`);
                data = data.replace('"url":"/admin/access', `"url":"admin/access`);
                data = data.replace('"url":"/api/datasources', `"url":"api/datasources`);
                data = data.replace('"url":"/connections/datasources', `"url":"connections/datasources`);
                data = data.replace('"url":"/connections/add-new-connection', `"url":"connections/add-new-connection`);
                data = data.replace('"url":"/connections', `"url":"connections`);
                data = data.replace('"url":"/alerting', `"url":"alerting`);
                data = data.replace('"url":"/dashboards', `"url":"dashboards`);
                data = data.replace('"url":"/dashboard', `"url":"dashboard`);
                data = data.replace('"url":"/api/', `"url":"api/`);
                data = data.replace(
                  '"appUrl": "http://localhost:3000/"',
                  `"appUrl": "http://localhost:3000/kubernetes/details/0053b816-4b45-4dcd-a612-19545110f8aa"`
                );
                //http://localhost:3000/kubernetes/details/0053b816-4b45-4dcd-a612-19545110f8aa
                data = data.replace(
                  /<\/html>/,
                  `<a href="/dashboards" id="manualtriggertohome" class="external-link"></a>
                  <script>
                  var _existCondition = setInterval(function() {
                    if (document.getElementById('ngRoot') !== null) {
                        console.log("Exists!");
                        clearInterval(_existCondition);
                        document.getElementById('manualtriggertohome').click();
                    }
                  }, 100);
                  </script>
                  </html>`
                );
              }
              res
                .status(response.status || 200)
                .setHeader('traceparent', traceParent)
                .send(data);
              return;
            }
            const bytedata = await response.arrayBuffer();
            res
              .status(response.status || 200)
              .setHeader('traceparent', traceParent)
              .send(Buffer.from(bytedata));
            return;
          }
          const error = await response.json();
          if (error['code'] === 'ECONNRESET') {
            console.error('Connection Reset - retrying');
            attempt--;
            continue;
          } else {
            throw new Error('Unexpected response from server');
          }
        } catch (error) {
          console.error('Error occurred:', error);
          attempt = 0; // Stop retrying
        }
      }

      res.status(500).setHeader('traceparent', traceParent).send(`<!DOCTYPE html>
      <html lang="en">
      <head>
          <meta charset="UTF-8">
          <meta name="viewport" content="width=device-width, initial-scale=1.0">
          <title>500 Internal Server Error</title>
          <style>
              body {
                  display: flex;
                  justify-content: center;
                  align-items: center;
                  height: 100vh;
                  margin: 0;
                  background-color: #2c3e50;
                  color: #ecf0f1;
                  font-family: Arial, sans-serif;
              }
              .container {
                  text-align: center;
              }
              .logo {
                  width: 100px;
                  margin-bottom: 20px;
              }
              h1 {
                  font-size: 48px;
                  margin: 0;
              }
              p {
                  font-size: 24px;
                  margin: 10px 0 30px;
              }
              a {
                  color: #3498db;
                  text-decoration: none;
                  font-size: 18px;
                  border: 1px solid #3498db;
                  padding: 10px 20px;
                  border-radius: 5px;
              }
              a:hover {
                  background-color: #3498db;
                  color: #ecf0f1;
              }
          </style>
      </head>
      <body>
          <div class="container">
              <div class="logo">
              <svg width="30" height="28" viewBox="0 0 30 28" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M9.825 13.337s-.467 7.942 4.789 13.782c0 0-5.71 1.358-7.826-5.373-1.927-6.132 3.037-8.41 3.037-8.41m6.501-1.046s-.407 6.918 4.171 12.006c0 0-4.974 1.183-6.817-4.68-1.679-5.342 2.646-7.326 2.646-7.326M4.277 18.341s-.316 5.375 3.241 9.328c0 0-3.387.209-4.832-3.791-1.077-2.981 1.591-5.537 1.591-5.537m17.55-8.128S10.089-4.504 2.03 1.978s19.797 8.235 19.797 8.235m4.078 9.355a3.64 3.64 0 1 0 0-7.278 3.64 3.64 0 0 0 0 7.278" fill="#FACF39"/></svg>
              </div>
              <h1>500 Internal Server Error</h1>
              <p>Something went wrong on our end. Please try again later.</p>
          </div>
      </body>
      </html>
      `);
    } catch (error) {
      console.error('Error occurred:', error);
    }
  } catch (error: any) {
    console.log('api error', error);
    res
      .status(error.status || 500)
      .setHeader('traceparent', traceParent)
      .json({
        code: error.code,
        error: error.message,
      });
  }
}
