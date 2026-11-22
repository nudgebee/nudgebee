import type { NextApiRequest, NextApiResponse } from 'next';
import { actionAllowedByTokenScopes, authenticateRequest, buildSessionVariables } from '@lib/rpcGateway';
import { loadRpcRoutes } from '@lib/rpcRoutes';
import { attachmentResponseHeaders } from '@lib/attachmentHeaders';

// Streams a ticket comment attachment's bytes (ServiceNow/Jira screenshots).
// Not a Hasura-Action/RPC call like the rest of @lib/rpcGateway's traffic -
// those return JSON and forwardAction() reads the upstream body with
// `.text()`, which would corrupt binary image bytes. This route reuses the
// gateway's auth + the ticket_get_attachment route's role gate (actions.yaml
// is still the single source of truth for who may call it), then proxies the
// response body through unparsed. Real per-account authorization happens
// ticket-server-side (bindAndAuthoriseTicketRequest -> Authorization.HasAccess),
// the same check ticket_get_comments/add-comment already rely on.
export const config = {
  api: {
    responseLimit: false,
  },
};

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'GET') {
    res.status(405).json({ error: 'method_not_allowed' });
    return;
  }

  const auth = await authenticateRequest(req);
  if (!auth) {
    res.status(401).json({ error: 'not_authenticated' });
    return;
  }

  const { account_id, integration_id, source, ticket_id, attachment_id } = req.query;
  if (
    typeof account_id !== 'string' ||
    typeof integration_id !== 'string' ||
    typeof source !== 'string' ||
    typeof ticket_id !== 'string' ||
    typeof attachment_id !== 'string'
  ) {
    res.status(400).json({ error: 'missing_params' });
    return;
  }

  const route = loadRpcRoutes()['ticket_get_attachment'];
  if (!route) {
    res.status(500).json({ error: 'route_unregistered' });
    return;
  }

  const sessionVariables = buildSessionVariables(auth.jwt);
  // Scoped API key: same gate forwardAction applies to every other action.
  const tokenScopes = sessionVariables.token_scopes;
  if (tokenScopes && !actionAllowedByTokenScopes('ticket_get_attachment', route, tokenScopes)) {
    res.status(403).json({ error: 'token_scope_denied' });
    return;
  }
  const allowedRoles = sessionVariables.allowed_roles || [];
  if (!allowedRoles.includes('super_admin') && !allowedRoles.some((r) => route.allowedRoles.has(r))) {
    res.status(403).json({ error: 'forbidden' });
    return;
  }

  const upstreamUrl = route.handler.replace(/\{\{(\w+)\}\}/g, (_, name) => process.env[name] || '');
  if (!upstreamUrl || upstreamUrl.includes('{{')) {
    res.status(502).json({ error: 'handler_unresolved' });
    return;
  }

  const upstreamHeaders: Record<string, string> = { 'Content-Type': 'application/json' };
  for (const h of route.headers) {
    const v = process.env[h.valueFromEnv];
    if (v) upstreamHeaders[h.name] = v;
  }
  if (route.forwardClientHeaders && auth.token) {
    upstreamHeaders['Authorization'] = auth.token;
  }

  let upstream: Response;
  try {
    upstream = await fetch(upstreamUrl, {
      method: 'POST',
      headers: upstreamHeaders,
      body: JSON.stringify({
        action: { name: 'ticket_get_attachment' },
        input: { object: { account_id, integration_id, source, ticket_id, attachment_id } },
        request_query: '',
        session_variables: { role: sessionVariables.role, user_id: sessionVariables.user_id, tenant_id: sessionVariables.tenant_id },
      }),
    });
  } catch {
    res.status(502).json({ error: 'upstream_unreachable' });
    return;
  }

  if (!upstream.ok || !upstream.body) {
    res.status(upstream.status || 502).json({ error: 'upstream_error' });
    return;
  }

  for (const [name, value] of Object.entries(
    attachmentResponseHeaders(upstream.headers.get('content-type'), upstream.headers.get('content-disposition'))
  )) {
    res.setHeader(name, value);
  }
  // Attachment bytes for a given ticket/comment don't change - safe to cache
  // in the browser, which also means a re-rendered comment list doesn't
  // re-fetch every image.
  res.setHeader('Cache-Control', 'private, max-age=3600');
  res.status(200);

  const reader = upstream.body.getReader();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      res.write(Buffer.from(value));
    }
  } catch (err) {
    await reader.cancel().catch(() => {});
    throw err;
  } finally {
    res.end();
  }
}
