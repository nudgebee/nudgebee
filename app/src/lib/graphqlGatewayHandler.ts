import type { NextApiRequest, NextApiResponse } from 'next';
import crypto from 'crypto';
import { context, propagation, trace, SpanStatusCode } from '@opentelemetry/api';
import { authenticateRequest, tryBypassGraphQL } from '@lib/rpcGateway';
import { clientIpKey, consumeRateLimits, identityKey, limitsFor } from '@lib/rateLimit';

const SLOW_THRESHOLD_MS = 500;

// Rate limits for the GraphQL gateway. Every default here is overridable per
// deployment (RATE_LIMIT_GRAPHQL_IP, _USER, _ENTRY) and the whole scope can be
// switched off with RATE_LIMIT_ENABLED_GRAPHQL=false.
//
// Two layers, because they defend different things:
//
//  * Pre-auth, keyed on the client address — bounds an unauthenticated flood
//    before it reaches authenticateRequest. Deliberately coarse and generous:
//    ingress-nginx runs behind an L4 load balancer with
//    externalTrafficPolicy: Cluster, so the address that reaches this process
//    is the ingress *node* IP, shared by every user routed through it. A tight
//    per-IP budget here would throttle unrelated users, not attackers.
//  * Post-auth, keyed on the authenticated user — the meaningful budget, and
//    the one that catches a runaway client or a single abusive account.
//
// Sizing comes from measured traffic rather than a guess: the busiest single
// minute on dev over three hours was 125 requests to /api/graphql on one pod
// (~250/min across two), all from one active session opening dashboards. The
// per-user default is roughly 2.5x that.
const GRAPHQL_IP_LIMITS = limitsFor('graphql:ip', '1200/1m,30000/1h');
const GRAPHQL_USER_LIMITS = limitsFor('graphql:user', '600/1m,20000/1h');

// A cluster-wide cap on the app's main API is an outage risk and no
// representative production number exists for it, so there is no default: set
// RATE_LIMIT_GRAPHQL_ENTRY to opt in once real peak traffic is known.
const GRAPHQL_ENTRY_SPEC = process.env.RATE_LIMIT_GRAPHQL_ENTRY?.trim();
const GRAPHQL_ENTRY_LIMITS = GRAPHQL_ENTRY_SPEC ? limitsFor('graphql:entry', GRAPHQL_ENTRY_SPEC) : null;

const THROTTLED_MESSAGE = 'Too many requests. Please retry in a moment.';

// GraphQL clients expect an `errors` array; keep the 429 and Retry-After so a
// caller that reads status codes can back off properly.
function sendThrottled(res: NextApiResponse, retryAfterSec: number) {
  res.setHeader('Retry-After', String(retryAfterSec));
  res.status(429).json({ errors: [{ message: THROTTLED_MESSAGE }] });
}

// NextAuth puts the user id in `sub`; bearer-synthesized JWTs carry the same
// shape. Fall back through the fields that identify a caller, and let the
// call site fall back to the address if none is present.
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
      if (GRAPHQL_ENTRY_LIMITS) {
        const entryCheck = await consumeRateLimits('graphql:entry', 'all', GRAPHQL_ENTRY_LIMITS);
        if (!entryCheck.allowed) {
          span.setAttribute('ratelimit.denied', 'entry');
          sendThrottled(res, entryCheck.retryAfterSec);
          return;
        }
      }
      const ipCheck = await consumeRateLimits('graphql:ip', clientIpKey(req), GRAPHQL_IP_LIMITS);
      if (!ipCheck.allowed) {
        span.setAttribute('ratelimit.denied', 'ip');
        sendThrottled(res, ipCheck.retryAfterSec);
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

      // --- Step 1b: Per-user budget ---
      // The address-keyed budget above is coarse by necessity (see the note on
      // the constants); this is the one that isolates a single caller. Keyed on
      // the JWT subject, hashed because it is often an email address and a
      // counter key is not a place to keep PII.
      const identity = (jwtSubject(auth.jwt) || clientIpKey(req)).toString();
      const userCheck = await consumeRateLimits('graphql:user', identityKey(identity), GRAPHQL_USER_LIMITS);
      if (!userCheck.allowed) {
        span.setAttribute('ratelimit.denied', 'user');
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
        console.warn(`[${opts.logPrefix}] SLOW ${operationName} ${totalMs}ms trace_id=${traceId}`, JSON.stringify(timing));
      } else {
        console.log(`[${opts.logPrefix}] ${operationName} ${totalMs}ms trace_id=${traceId}`, JSON.stringify(timing));
      }
      span.end();
    }
  });
}
