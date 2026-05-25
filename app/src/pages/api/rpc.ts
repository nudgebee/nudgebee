import type { NextApiRequest, NextApiResponse } from 'next';
import crypto from 'crypto';
import { context, propagation, trace, SpanStatusCode } from '@opentelemetry/api';
import { authenticateRequest, buildSessionVariables, forwardAction } from '@lib/rpcGateway';

// JSON-RPC 2.0 gateway. Thin protocol shell — auth, route lookup, envelope
// construction and upstream forwarding live in @lib/rpcGateway, shared with
// /api/graphql.

// JSON-RPC 2.0 reserved error codes (subset we emit)
const RPC_INVALID_REQUEST = -32600;
const RPC_METHOD_NOT_FOUND = -32601;
const RPC_INTERNAL_ERROR = -32603;
// Application errors use -32000 to -32099 (server errors range)
const RPC_AUTH_ERROR = -32001;
const RPC_UPSTREAM_ERROR = -32002;
const RPC_FORBIDDEN = -32003;

const SLOW_THRESHOLD_MS = 500;

function rpcError(id: unknown, code: number, message: string, data?: unknown) {
  const err: { code: number; message: string; data?: unknown } = { code, message };
  if (data !== undefined) err.data = data;
  return { jsonrpc: '2.0', id: id ?? null, error: err };
}
function rpcResult(id: unknown, result: unknown) {
  return { jsonrpc: '2.0', id: id ?? null, result };
}

export const config = {
  api: {
    responseLimit: false,
  },
};

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const t0 = performance.now();
  const tracer = trace.getTracer('rpc-api');

  // --- Trace context ---
  let traceParent: string;
  const tpHeader = req.headers['traceparent'];
  if (tpHeader && tpHeader.length > 0) {
    traceParent = Array.isArray(tpHeader) ? tpHeader[0] : tpHeader;
  } else {
    const version = Buffer.alloc(1).toString('hex');
    const traceId = crypto.randomBytes(16).toString('hex');
    const id = crypto.randomBytes(8).toString('hex');
    traceParent = `${version}-${traceId}-${id}-01`;
  }
  const parentCtx = propagation.extract(context.active(), { traceparent: traceParent });
  const span = tracer.startSpan('rpc-handler', undefined, parentCtx);

  await context.with(trace.setSpan(context.active(), span), async () => {
    const requestId =
      Array.isArray(req.headers['x-request-id']) && req.headers['x-request-id'].length > 0
        ? req.headers['x-request-id'][0]
        : (req.headers['x-request-id'] as string) || traceParent;

    const timing: Record<string, number> = {};
    let methodForLog = 'unknown';
    let rpcId: unknown = null;

    res.setHeader('traceparent', traceParent);
    res.setHeader('X-Request-ID', requestId);

    try {
      if (req.method !== 'POST') {
        res.status(405).json(rpcError(null, RPC_INVALID_REQUEST, 'Only POST is supported'));
        return;
      }

      // --- Parse JSON-RPC envelope ---
      const body = req.body;
      if (!body || typeof body !== 'object' || Array.isArray(body)) {
        // Batch requests not supported yet
        res.status(400).json(rpcError(null, RPC_INVALID_REQUEST, 'Expected a single JSON-RPC request object'));
        return;
      }
      if (body.jsonrpc !== '2.0' || typeof body.method !== 'string') {
        res.status(400).json(rpcError(body.id ?? null, RPC_INVALID_REQUEST, 'Invalid JSON-RPC 2.0 envelope'));
        return;
      }
      rpcId = body.id ?? null;
      const method = body.method as string;
      methodForLog = method;
      const params = (body.params as Record<string, unknown> | undefined) || {};
      // Optional passthrough of the GraphQL selection string for handlers that
      // still parse `request_query` to determine which columns to return.
      const requestQuery = typeof body._query === 'string' ? (body._query as string) : '';

      // --- Authenticate ---
      const authSpan = tracer.startSpan('authenticateUser', undefined, trace.setSpan(context.active(), span));
      const tAuth = performance.now();
      let auth;
      try {
        auth = await authenticateRequest(req);
        timing.auth_ms = Math.round(performance.now() - tAuth);
      } catch (e: any) {
        authSpan.recordException(e);
        authSpan.setStatus({ code: SpanStatusCode.ERROR, message: e.message });
        authSpan.end();
        res.status(401).json(rpcError(rpcId, RPC_AUTH_ERROR, 'invalid_token'));
        return;
      }
      if (!auth) {
        authSpan.setStatus({ code: SpanStatusCode.ERROR, message: 'User not authenticated' });
        authSpan.end();
        res.status(401).json(rpcError(rpcId, RPC_AUTH_ERROR, 'not_authenticated'));
        return;
      }
      authSpan.setStatus({ code: SpanStatusCode.OK });
      authSpan.end();

      // session_variables require a JWT. authenticateRequest synthesizes one
      // from the bearer token in the Bearer-only flow (see
      // synthesizeJwtFromBearer in rpcGateway.ts), so the only way to land
      // here with auth.jwt null would be a verifiable failure that
      // authenticateRequest already converted to a null return. Keep this
      // guard as defense in depth.
      if (!auth.jwt) {
        res.status(401).json(rpcError(rpcId, RPC_AUTH_ERROR, 'not_authenticated'));
        return;
      }
      const sessionVariables = buildSessionVariables(auth.jwt);
      const tenantId = sessionVariables.tenant_id;
      const userId = sessionVariables.user_id;

      // --- Forward to upstream ---
      const fetchSpan = tracer.startSpan('rpc-upstream', undefined, trace.setSpan(context.active(), span));
      const tFetch = performance.now();
      const result = await forwardAction({
        method,
        params,
        requestQuery,
        sessionVariables,
        tenantId,
        userId,
        // Forward the Bearer token (for nbctl/automation callers only — empty
        // for browser-cookie flow). Upstreams gated by `forward_client_headers:
        // true` in actions.yaml receive this as `Authorization: Bearer …`.
        clientAuthorization: auth.token ? `Bearer ${auth.token}` : undefined,
        traceparent: traceParent,
        requestId,
      });
      timing.upstream_ms = Math.round(performance.now() - tFetch);

      if (!result.ok) {
        fetchSpan.setStatus({ code: SpanStatusCode.ERROR, message: result.error.kind });
        fetchSpan.end();
        switch (result.error.kind) {
          case 'method_not_found':
            res.status(404).json(rpcError(rpcId, RPC_METHOD_NOT_FOUND, `Method not found: ${method}`));
            return;
          case 'handler_unresolved':
            res.status(500).json(rpcError(rpcId, RPC_INTERNAL_ERROR, `Handler URL unresolved for ${method}`));
            return;
          case 'forbidden':
            res.status(403).json(
              rpcError(rpcId, RPC_FORBIDDEN, `Role '${result.error.role}' is not permitted to invoke '${method}'`, {
                role: result.error.role,
                allowedRoles: result.error.allowedRoles,
              })
            );
            return;
          case 'upstream_unreachable':
            res.status(502).json(
              rpcError(rpcId, RPC_UPSTREAM_ERROR, 'upstream_unreachable', {
                method: result.error.method,
                url: result.error.url,
                detail: result.error.detail,
              })
            );
            return;
          case 'upstream_parse_failed':
            res.status(502).json(
              rpcError(rpcId, RPC_UPSTREAM_ERROR, 'upstream_parse_failed', {
                method: result.error.method,
                url: result.error.url,
                detail: result.error.detail,
              })
            );
            return;
          case 'upstream_error':
            res.status(result.error.status).json(
              rpcError(rpcId, RPC_UPSTREAM_ERROR, `upstream_${result.error.status}`, {
                method: result.error.method,
                url: result.error.url,
                payload: result.error.payload,
              })
            );
            return;
        }
      }

      fetchSpan.setStatus({ code: SpanStatusCode.OK });
      fetchSpan.end();
      res.status(200).json(rpcResult(rpcId, result.payload));
      span.setStatus({ code: SpanStatusCode.OK });
    } catch (err: any) {
      span.recordException(err);
      span.setStatus({ code: SpanStatusCode.ERROR, message: err.message });
      if (!res.headersSent) {
        res.status(500).json(rpcError(rpcId, RPC_INTERNAL_ERROR, err.message));
      } else if (!res.writableEnded) {
        res.end();
      }
    } finally {
      timing.total_ms = Math.round(performance.now() - t0);
      const totalMs = timing.total_ms;
      const logFn = totalMs > SLOW_THRESHOLD_MS ? console.warn : console.log;
      logFn(`[rpc-proxy] ${methodForLog} ${totalMs}ms`, JSON.stringify(timing));
      span.end();
    }
  });
}
