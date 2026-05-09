import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';
import type { NextApiRequest, NextApiResponse } from 'next';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import { decrypt } from '@lib/internal';
import crypto from 'crypto';
import { context, propagation, trace, SpanStatusCode } from '@opentelemetry/api';

const graphqlEndpoint = process.env.GRAPHQL_API_ENDPOINT || 'http://localhost:8080/v1/graphql';
const unprotected: string[] = [];

const SLOW_THRESHOLD_MS = 500;

// We stream upstream Hasura responses through this handler, so the framework's
// 4MB warning ("API response for /api/graphql exceeds 4MB") is no longer useful
// signal — disable it to cut log noise. Our own bytes_out timing field replaces it.
export const config = {
  api: {
    responseLimit: false,
  },
};

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const t0 = performance.now();
  const tracer = trace.getTracer('graphql-api');
  const operationName = req.body?.operationName || 'unknown';

  // --- Extract or create traceparent ---
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
  const span = tracer.startSpan('graphql-handler', undefined, parentCtx);

  await context.with(trace.setSpan(context.active(), span), async () => {
    const requestId =
      Array.isArray(req.headers['x-request-id']) && req.headers['x-request-id'].length > 0
        ? req.headers['x-request-id'][0]
        : (req.headers['x-request-id'] as string) || traceParent;

    const timing: Record<string, number> = {};

    try {
      const body = req.body;
      let authenticate = true;

      if (unprotected.indexOf(body?.operationName) >= 0 && body?.query?.includes(`query ${body?.operationName}`)) {
        authenticate = false;
      }

      // --- Step 1: Authentication ---
      const authSpan = tracer.startSpan('authenticateUser', undefined, trace.setSpan(context.active(), span));
      let token: string | null = null;
      try {
        if (req.headers.authorization) {
          const tDecrypt = performance.now();
          const splits = req.headers.authorization.split(' ');
          if (splits.length > 1) {
            token = await decrypt(splits[1]);
          }
          timing.decrypt_ms = Math.round(performance.now() - tDecrypt);
        }

        if (!token) {
          const tSession = performance.now();
          const session = await getServerSession(req, res, authOptions);
          timing.getServerSession_ms = Math.round(performance.now() - tSession);

          if (session?.user) {
            const tGetToken = performance.now();
            const jwtToken = await getToken({ req });
            timing.getToken1_ms = Math.round(performance.now() - tGetToken);
            token = (jwtToken?.hasuraIdToken as string) || (jwtToken?.idToken as string) || null;
          }
        }

        if (authenticate && !token) {
          authSpan.setStatus({ code: SpanStatusCode.ERROR, message: 'User not authenticated' });
          res.status(401).json({
            error: 'not_authenticated',
            description: 'The user does not have an active session or is not authenticated',
          });
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
      timing.auth_total_ms = Math.round(performance.now() - t0);

      // --- Step 2: Outgoing GraphQL call ---
      const gqlSpan = tracer.startSpan('proxyGraphQL', undefined, trace.setSpan(context.active(), span));
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };

      // Check if this is a super admin session
      const tGetToken2 = performance.now();
      const jwtSessionToken = await getToken({ req });
      timing.getToken2_ms = Math.round(performance.now() - tGetToken2);

      const isSuperAdminSession = jwtSessionToken?.isSuperAdmin || jwtSessionToken?.isSuperAdminReadonly;

      if (isSuperAdminSession) {
        // Super admin: use admin-secret with role override
        headers['x-hasura-admin-secret'] = process.env.HASURA_GRAPHQL_ADMIN_SECRET || '';

        // Determine default role using same priority as encodeHasuraJWT
        const roles = (jwtSessionToken.roles as string[]) || [];
        const rolePriority = ['tenant_admin', 'account_admin', 'account_admin_readonly', 'k8s_namespace_admin', 'k8s_namespace_admin_readonly'];
        headers['x-hasura-role'] = rolePriority.find((role) => roles.includes(role)) || 'tenant_admin_readonly';

        const tenantId = (jwtSessionToken.tenant as any)?.id || '';
        if (tenantId) {
          headers['x-hasura-user-tenant-id'] = tenantId;
        }
        headers['x-hasura-user-id'] = (jwtSessionToken.id || jwtSessionToken.sub) as string;

        // Add super_admin to allowed-roles so api-server can identify super admin sessions
        const allowedRoles = [...roles, 'super_admin'];
        headers['x-hasura-allowed-roles'] = `{${allowedRoles.join(',')}}`;

        const accountIds = (jwtSessionToken.accountIds as string[]) || [];
        headers['x-hasura-user-account-ids'] = `{${accountIds.join(',')}}`;
        headers['x-hasura-user-readonly-account-ids'] = `{${((jwtSessionToken.readOnlyAccountIds as string[]) || []).join(',')}}`;
        headers['x-hasura-user-namespaced-account-ids'] = `{${((jwtSessionToken.namespacedAccountIds as string[]) || []).join(',')}}`;
        headers['x-hasura-user-namespaced-readonly-account-ids'] = `{${((jwtSessionToken.namespacedReadOnlyAccountIds as string[]) || []).join(
          ','
        )}}`;
      } else if (token) {
        headers['Authorization'] = `Bearer ${token}`;
      }
      headers['traceparent'] = traceParent;
      headers['X-Request-ID'] = requestId;

      try {
        let attempt = 3;
        let proxyResponse: Response | null = null;
        let success = false;
        let retries = 0;

        while (attempt > 0 && !success) {
          const tFetch = performance.now();
          proxyResponse = await fetch(graphqlEndpoint, {
            headers,
            body: JSON.stringify(req.body),
            method: 'POST',
          });
          timing[`fetch_attempt${3 - attempt + 1}_ms`] = Math.round(performance.now() - tFetch);

          if (proxyResponse.status === 500) {
            try {
              // clone() so the original body remains readable for downstream forwarding
              const error = await proxyResponse.clone().json();
              if (error['code'] === 'ECONNRESET') {
                retries++;
                attempt--;
                continue;
              }
            } catch {
              console.error();
            }
          }

          success = true;
        }

        timing.hasura_total_ms = Math.round(performance.now() - tGetToken2);
        if (retries > 0) {
          timing.retries = retries;
        }

        if (!proxyResponse) {
          gqlSpan.setStatus({ code: SpanStatusCode.ERROR, message: 'No response from server' });
          res.status(500).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId).json({ error: 'InternalServerError' });
          return;
        }

        // On 2xx, stream the body straight through to avoid the JSON.parse + JSON.stringify
        // memory spike on large Hasura responses (each ~5–10× the wire bytes on the V8 heap).
        if (proxyResponse.ok) {
          res.status(200).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId);
          res.setHeader('Content-Type', proxyResponse.headers.get('content-type') || 'application/json');

          let bytesOut = 0;
          if (proxyResponse.body) {
            const reader = proxyResponse.body.getReader();
            try {
              while (true) {
                const { done, value } = await reader.read();
                if (done) break;
                bytesOut += value.byteLength;
                if (!res.write(value)) {
                  await new Promise<void>((resolve) => res.once('drain', resolve));
                }
              }
            } finally {
              try {
                reader.releaseLock();
              } catch {
                // noop
              }
            }
          }
          res.end();
          timing.bytes_out = bytesOut;
          gqlSpan.setStatus({ code: SpanStatusCode.OK });
          return;
        }

        // Error path: parse body so we can forward structured error to the client
        const tParse = performance.now();
        const data = await proxyResponse.json();
        timing.response_parse_ms = Math.round(performance.now() - tParse);

        gqlSpan.setStatus({
          code: SpanStatusCode.ERROR,
          message: `GraphQL responded with ${proxyResponse.status}`,
        });
        res.status(proxyResponse.status).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId).json(data);
      } catch (err: any) {
        gqlSpan.recordException(err);
        gqlSpan.setStatus({ code: SpanStatusCode.ERROR, message: err.message });
        // If streaming already started, headers/status are flushed — can't send a JSON error;
        // just terminate the response so the client sees a truncated body instead of a crash.
        if (res.headersSent) {
          if (!res.writableEnded) res.end();
          return;
        }
        res.status(500).json({ error: 'internal_error', message: err.message });
        return;
      } finally {
        gqlSpan.end();
      }

      span.setStatus({ code: SpanStatusCode.OK });
    } catch (error: any) {
      span.recordException(error);
      span.setStatus({ code: SpanStatusCode.ERROR, message: error.message });
      res.status(500).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId).json({
        code: error.code,
        error: error.message,
      });
    } finally {
      timing.total_ms = Math.round(performance.now() - t0);
      const totalMs = timing.total_ms;
      if (totalMs > SLOW_THRESHOLD_MS) {
        console.warn(`[graphql-proxy] SLOW ${operationName} ${totalMs}ms`, JSON.stringify(timing));
      } else {
        console.log(`[graphql-proxy] ${operationName} ${totalMs}ms`, JSON.stringify(timing));
      }
      span.end();
    }
  });
}
