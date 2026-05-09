import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';
import type { NextApiRequest, NextApiResponse } from 'next';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import { decrypt, decodeHasuraJWT } from '@lib/internal';
import { queryGraphQL } from '@lib/HttpService';
import crypto from 'crypto';
import { context, propagation, trace, SpanStatusCode } from '@opentelemetry/api';

const relayEndpoint = process.env.RELAY_SERVER_ENDPOINT ?? 'http://localhost:52832';
const auditEndpoint = process.env.SERVICE_API_SERVER_URL ?? 'http://localhost:8000';
const secretKey = process.env.RELAY_SERVER_SECRET_KEY ?? '';
const servicesServerToken = process.env.ACTION_API_SERVER_TOKEN ?? '';

async function hasAccountAccess(userId: string, tenantId: string, accountId: string, traceParent: string): Promise<boolean> {
  const tracer = trace.getTracer('relay-api');
  const span = tracer.startSpan('hasAccountAccess');

  try {
    const authResp = await fetch(auditEndpoint + '/v1/authz/validate_access', {
      headers: {
        'Content-Type': 'application/json',
        traceparent: traceParent,
        'X-Request-ID': traceParent,
        'X-ACTION-TOKEN': servicesServerToken,
      },
      body: JSON.stringify({
        user_id: userId,
        access: [
          {
            tenant_id: tenantId,
            permission: 'read',
            category: 'ACCOUNTS',
            args: { account_id: accountId },
          },
        ],
      }),
      method: 'post',
    });

    if (authResp.ok) {
      const responseJson = await authResp.json();
      const allowed = responseJson?.access && responseJson.access?.length > 0 && responseJson.access[0]?.allowed;
      if (allowed) {
        span.setStatus({ code: SpanStatusCode.OK });
      } else {
        span.setStatus({ code: SpanStatusCode.ERROR, message: 'Access denied' });
      }
      return allowed;
    }

    span.setStatus({
      code: SpanStatusCode.ERROR,
      message: `Access API returned ${authResp.status}`,
    });
  } catch (e: any) {
    span.recordException(e);
    span.setStatus({ code: SpanStatusCode.ERROR, message: e.message });
  } finally {
    span.end();
  }
  return false;
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const tracer = trace.getTracer('relay-api');
  const { relay } = req.query;

  // --- Traceparent setup ---
  let traceParent: string;
  const requestIds = req.headers['traceparent'];
  if (requestIds && requestIds.length > 0) {
    traceParent = Array.isArray(requestIds) ? requestIds[0] : requestIds;
  } else {
    const version = Buffer.alloc(1).toString('hex');
    const traceId = crypto.randomBytes(16).toString('hex');
    const id = crypto.randomBytes(8).toString('hex');
    const flags = '01';
    traceParent = `${version}-${traceId}-${id}-${flags}`;
  }

  const parentCtx = propagation.extract(context.active(), { traceparent: traceParent });
  const span = tracer.startSpan(`relay-handler-${relay}`, undefined, parentCtx);

  try {
    await context.with(trace.setSpan(context.active(), span), async () => {
      const requestId =
        Array.isArray(req.headers['x-request-id']) && req.headers['x-request-id'].length > 0
          ? req.headers['x-request-id'][0]
          : (req.headers['x-request-id'] as string) || traceParent;

      let token: string | null = null;
      const userDetails = { userId: '', tenantId: '' };

      // --- Step 1: Auth ---
      const authSpan = tracer.startSpan('authenticateUser', undefined, trace.setSpan(context.active(), span));
      try {
        if (req.headers.authorization) {
          const splits = req.headers.authorization.split(' ');
          if (splits.length > 1) {
            token = await decrypt(splits[1]);
          }
        }

        if (!token) {
          const session = await getServerSession(req, res, authOptions);
          if (session?.user) {
            const jwtToken = await getToken({ req });
            if (jwtToken) {
              userDetails.userId = jwtToken?.sub as string;
              userDetails.tenantId = (jwtToken?.tenant as any)?.id as string;
            }
            token = (jwtToken?.hasuraIdToken as string) || (jwtToken?.idToken as string) || null;
          }
        } else {
          const parsedToken = await decodeHasuraJWT(token);
          const hasuraPayload = JSON.parse(parsedToken.payload['https://hasura.io/jwt/claims'] as string);
          userDetails.userId = hasuraPayload['x-hasura-user-id'];
          userDetails.tenantId = hasuraPayload['x-hasura-user-tenant-id'];
        }

        if (!token) {
          authSpan.setStatus({ code: SpanStatusCode.ERROR, message: 'Unauthenticated' });
          res.status(401).json({ error: 'not_authenticated' });
          return;
        }

        authSpan.setStatus({ code: SpanStatusCode.OK });
      } catch (e: any) {
        authSpan.recordException(e);
        authSpan.setStatus({ code: SpanStatusCode.ERROR, message: e.message });
        res.status(401).json({ error: 'invalid_token' });
        return;
      } finally {
        authSpan.end();
      }

      // --- Step 2: Account Access Validation ---
      if (!(await hasAccountAccess(userDetails.userId, userDetails.tenantId, req.body.body?.account_id, traceParent))) {
        span.setStatus({ code: SpanStatusCode.ERROR, message: 'Access denied' });
        res.status(403).json({ error: 'forbidden', description: 'Access denied' });
        return;
      }

      // --- Step 3: Relay request ---
      const relaySpan = tracer.startSpan('relayFetch', undefined, trace.setSpan(context.active(), span));
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        traceparent: traceParent,
        'X-Request-ID': requestId,
        'X-SECRET-KEY': secretKey,
        'X-USER-ID': userDetails.userId,
        'X-TENANT-ID': userDetails.tenantId,
      };

      let status = 'SUCCESS';
      const startDate = new Date();
      let success = false;

      try {
        let attempt = 3;
        while (attempt > 0 && !success) {
          const response = await fetch(`${relayEndpoint}/${relay}`, {
            headers,
            body: JSON.stringify(req.body),
            method: 'post',
          });

          if (response.ok) {
            const data = await response.json();
            relaySpan.setStatus({ code: SpanStatusCode.OK });
            res.status(200).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId).json(data);
            success = true;
          } else {
            const error = await response.json();
            if (error['code'] === 'ECONNRESET') {
              attempt--;
              continue;
            } else {
              relaySpan.setStatus({
                code: SpanStatusCode.ERROR,
                message: `Relay error ${response.status}`,
              });
              status = 'FAILURE';
              res.status(response.status).json(error);
              return;
            }
          }
        }

        if (!success) {
          status = 'FAILURE';
          relaySpan.setStatus({ code: SpanStatusCode.ERROR, message: 'Retries exhausted' });
          res.status(500).json({ error: 'InternalServerError' });
        }
      } catch (err: any) {
        relaySpan.recordException(err);
        relaySpan.setStatus({ code: SpanStatusCode.ERROR, message: err.message });
        status = 'FAILURE';
        res.status(500).json({ error: 'internal_error', message: err.message });
      } finally {
        relaySpan.end();
      }

      // --- Step 4: Post-processing (Audit / History) ---
      const diff = new Date().getTime() - startDate.getTime();
      const postSpan = tracer.startSpan('postProcessing', undefined, trace.setSpan(context.active(), span));
      try {
        if (req.body.track_history) {
          await queryGraphQL(
            `mutation UserHistory($request: UserHistoryInput!) {
              user_history(request: $request) {
                status
              }
            }`,
            'UserHistory',
            {
              request: {
                account_id: req.body.body?.account_id,
                module: req.body.body?.action_name || 'relay_action',
                data: JSON.stringify(req.body.body),
                duration: diff,
                status,
              },
            },
            {
              traceParent,
              'x-request-id': requestId,
            }
          );
          postSpan.setStatus({ code: SpanStatusCode.OK });
        }
      } catch (e: any) {
        postSpan.recordException(e);
        postSpan.setStatus({ code: SpanStatusCode.ERROR, message: e.message });
      } finally {
        postSpan.end();
      }

      if (status === 'SUCCESS') {
        span.setStatus({ code: SpanStatusCode.OK });
      } else {
        span.setStatus({ code: SpanStatusCode.ERROR, message: 'Relay flow failed' });
      }
    });
  } catch (error: any) {
    span.recordException(error);
    span.setStatus({ code: SpanStatusCode.ERROR, message: error.message });
    res.status(500).json({ error: 'internal_server_error', message: error.message });
  } finally {
    span.end();
  }
}
