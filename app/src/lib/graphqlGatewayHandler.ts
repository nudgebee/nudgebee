import type { NextApiRequest, NextApiResponse } from 'next';
import crypto from 'crypto';
import { context, propagation, trace, SpanStatusCode } from '@opentelemetry/api';
import { authenticateRequest, tryBypassGraphQL } from '@lib/rpcGateway';

const SLOW_THRESHOLD_MS = 500;

export type GatewayHandlerOptions = {
  // OpenTelemetry tracer name + log prefix.
  tracerName: string;
  logPrefix: string;
};

// Shared GraphQL→RPC gateway request handler used by /api/graphql.
export async function handleGatewayRequest(req: NextApiRequest, res: NextApiResponse, opts: GatewayHandlerOptions) {
  const t0 = performance.now();
  const tracer = trace.getTracer(opts.tracerName);
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
  const span = tracer.startSpan(`${opts.logPrefix}-handler`, undefined, parentCtx);

  await context.with(trace.setSpan(context.active(), span), async () => {
    const requestId =
      Array.isArray(req.headers['x-request-id']) && req.headers['x-request-id'].length > 0
        ? req.headers['x-request-id'][0]
        : (req.headers['x-request-id'] as string) || traceParent;

    const timing: Record<string, number> = {};

    try {
      const body = req.body;

      // --- Step 1: Authentication (NextAuth cookie OR Bearer token) ---
      // Two callers in practice: browser frontend (NextAuth session cookie)
      // and non-browser callers like nbctl (encrypted bearer token from
      // /api/auth/token). authenticateRequest resolves both flows to the
      // same shape; for Bearer the JWT is synthesized from the decrypted
      // token's claims.
      const authSpan = tracer.startSpan('authenticateUser', undefined, trace.setSpan(context.active(), span));
      const tGetToken = performance.now();
      const auth = await authenticateRequest(req);
      timing.getToken_ms = Math.round(performance.now() - tGetToken);

      if (!auth || !auth.jwt) {
        authSpan.setStatus({ code: SpanStatusCode.ERROR, message: 'User not authenticated' });
        authSpan.end();
        res.status(401).json({
          error: 'not_authenticated',
          description: 'The user does not have an active session',
        });
        return;
      }
      authSpan.setStatus({ code: SpanStatusCode.OK });
      authSpan.end();
      timing.auth_total_ms = Math.round(performance.now() - t0);

      const jwtSessionToken = auth.jwt;
      const token = auth.token;

      // --- Step 2: Forward to upstream services via the RPC gateway ---
      if (typeof body?.query !== 'string') {
        res
          .status(400)
          .setHeader('traceparent', traceParent)
          .setHeader('X-Request-ID', requestId)
          .json({ errors: [{ message: 'missing query body' }] });
        return;
      }

      const gatewaySpan = tracer.startSpan('rpcGateway', undefined, trace.setSpan(context.active(), span));
      const tGateway = performance.now();
      const result = await tryBypassGraphQL({
        query: body.query,
        variables: body.variables,
        jwt: jwtSessionToken,
        clientAuthorization: token ? `Bearer ${token}` : undefined,
        traceparent: traceParent,
        requestId,
      });
      timing.gateway_ms = Math.round(performance.now() - tGateway);

      if (result.handled) {
        gatewaySpan.setStatus({ code: SpanStatusCode.OK });
        gatewaySpan.end();
        res.status(result.status).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId).json(result.body);
        return;
      }

      gatewaySpan.setAttribute('gateway.unhandled_reason', result.reason);
      gatewaySpan.setStatus({ code: SpanStatusCode.ERROR, message: `unhandled:${result.reason}` });
      gatewaySpan.end();
      res
        .status(502)
        .setHeader('traceparent', traceParent)
        .setHeader('X-Request-ID', requestId)
        .json({ errors: [{ message: `RPC gateway could not handle the operation: ${result.reason}` }] });

      span.setStatus({ code: SpanStatusCode.OK });
    } catch (error: any) {
      span.recordException(error);
      span.setStatus({ code: SpanStatusCode.ERROR, message: error.message });
      if (res.headersSent) {
        if (!res.writableEnded) res.end();
        return;
      }
      res.status(500).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId).json({
        code: error.code,
        error: error.message,
      });
    } finally {
      timing.total_ms = Math.round(performance.now() - t0);
      const totalMs = timing.total_ms;
      if (totalMs > SLOW_THRESHOLD_MS) {
        console.warn(`[${opts.logPrefix}] SLOW ${operationName} ${totalMs}ms`, JSON.stringify(timing));
      } else {
        console.log(`[${opts.logPrefix}] ${operationName} ${totalMs}ms`, JSON.stringify(timing));
      }
      span.end();
    }
  });
}
