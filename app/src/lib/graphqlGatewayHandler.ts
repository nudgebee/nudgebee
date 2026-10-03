import type { NextApiRequest, NextApiResponse } from 'next';
import crypto from 'crypto';
import { context, propagation, trace, SpanStatusCode } from '@opentelemetry/api';
import { authenticateRequest, tryBypassGraphQL } from '@lib/rpcGateway';
import { checkIdentityRateLimit, checkPreAuthRateLimit } from '@lib/rateLimitHooks';

const SLOW_THRESHOLD_MS = 500;

// Rate limiting is an EE feature. The budgets, the key derivation and the
// shared counters all live in `@ee/ratelimit`, which registers itself through
// `@lib/rateLimitHooks` at server boot. With nothing registered — an OSS
// build, where the EE tree is removed — both checks below admit every
// request and this gateway is unthrottled by design.

const THROTTLED_MESSAGE = 'Too many requests. Please retry in a moment.';

// GraphQL clients expect an `errors` array; keep the 429 and Retry-After so a
// caller that reads status codes can back off properly.
function sendThrottled(res: NextApiResponse, retryAfterSec: number) {
  res.setHeader('Retry-After', String(retryAfterSec));
  res.status(429).json({ errors: [{ message: THROTTLED_MESSAGE }] });
}

// NextAuth puts the user id in `sub`; bearer-synthesized JWTs carry the same
// shape. Fall back through the fields that identify a caller, and return an
// empty string when none is present — the limiter decides the fallback.
function jwtSubject(jwt: Record<string, unknown> | null | undefined): string {
  if (!jwt) {
    return '';
  }
  const candidate = jwt.sub ?? jwt.email ?? jwt.userId;
  return typeof candidate === 'string' ? candidate : '';
}

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

      // --- Step 0: Throttle before authentication ---
      // authenticateRequest decrypts a bearer token or verifies a session
      // cookie on every call; an unauthenticated flood should not get that far.
      const preAuthCheck = await checkPreAuthRateLimit(req);
      if (!preAuthCheck.allowed) {
        span.setAttribute('ratelimit.denied', preAuthCheck.deniedBy ?? 'preauth');
        sendThrottled(res, preAuthCheck.retryAfterSec);
        return;
      }

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

      // --- Step 1b: Per-caller budget ---
      // Passes the authenticated subject and lets the registered limiter
      // decide what to key on (and what to fall back to when there is none),
      // so no identity derivation lives on this side of the hook.
      const userCheck = await checkIdentityRateLimit(req, jwtSubject(auth.jwt));
      if (!userCheck.allowed) {
        span.setAttribute('ratelimit.denied', userCheck.deniedBy ?? 'user');
        sendThrottled(res, userCheck.retryAfterSec);
        return;
      }

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
      console.error('GraphQL gateway error:', error);
      res.status(500).setHeader('traceparent', traceParent).setHeader('X-Request-ID', requestId).json({
        code: error.code,
        error: error.message,
      });
    } finally {
      timing.total_ms = Math.round(performance.now() - t0);
      const totalMs = timing.total_ms;
      // trace_id from the forwarded traceparent (00-<trace_id>-<span_id>-<flags>)
      // so this line joins the backend services' logs for the same request in Loki.
      const traceId = traceParent.split('-')[1] || '';
      if (totalMs > SLOW_THRESHOLD_MS) {
        console.warn('%s', `[${opts.logPrefix}] SLOW ${operationName} ${totalMs}ms trace_id=${traceId}`, JSON.stringify(timing));
      } else {
        console.log('%s', `[${opts.logPrefix}] ${operationName} ${totalMs}ms trace_id=${traceId}`, JSON.stringify(timing));
      }
      span.end();
    }
  });
}
