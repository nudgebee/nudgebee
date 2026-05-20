import { getToken, type JWT } from 'next-auth/jwt';
import type { NextApiRequest } from 'next';
import { parse, type FieldNode, type OperationDefinitionNode, type SelectionSetNode, type TypeNode, type ValueNode } from 'graphql';
import { decrypt } from '@lib/internal';
import { loadActionInputSchema, loadRpcRoutes, type SchemaFieldInfo } from '@lib/rpcRoutes';
import { elevateRoles } from '@lib/authHooks';

// Shared primitives for the Hasura-bypass path. Used by both /api/rpc
// (JSON-RPC envelope) and /api/graphql (GraphQL envelope when the bypass is
// enabled). The endpoints handle protocol shape; everything else — auth,
// route lookup, env-var URL resolution, HasuraActionRequest construction,
// upstream fetch, error classification — lives here.

const routes = loadRpcRoutes();
const inputSchema = loadActionInputSchema();

// Apply Hasura/GraphQL list-input coercion recursively. When a field is
// declared as a list but the caller passed a single value, wrap it in a
// single-element list — and recurse into nested objects/lists so operators
// like `_in: "x"` (schema: `_in: [String!]`) survive the bypass. Without
// this, the upstream Go decoder fails with a confusing "cannot unmarshal
// string into []string" while the Hasura path coerces silently.
function coerceValue(value: unknown, info: SchemaFieldInfo | undefined): unknown {
  if (value === null || value === undefined) return value;
  if (!info) return value; // unknown field — pass through, upstream decides

  // Step 1: if the schema says list but the caller passed a scalar/object,
  // wrap it. We only wrap when the value can be a list element — primitives,
  // objects, arrays of objects. Hasura does the same.
  let v = value;
  if (info.isList && !Array.isArray(v)) v = [v];

  // Step 2: recurse. Inside an array each element is the non-list type;
  // inside an object each key uses the named input type's field map.
  if (Array.isArray(v)) {
    const elementInfo: SchemaFieldInfo = { ...info, isList: false };
    return v.map((el) => coerceValue(el, elementInfo));
  }
  if (typeof v === 'object') {
    const fields = inputSchema.inputTypes.get(info.typeName);
    if (!fields) return v; // scalar named type (String, jsonb, etc.) — done
    const obj = v as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(obj)) {
      out[k] = coerceValue(val, fields.get(k));
    }
    return out;
  }
  return v;
}

function coerceActionInput(actionName: string, input: Record<string, unknown>): Record<string, unknown> {
  const argSchema = inputSchema.actions.get(actionName);
  if (!argSchema) return input;
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(input)) {
    out[k] = coerceValue(v, argSchema.get(k));
  }
  return out;
}

export type AuthContext = {
  // The token forwarded as `Authorization: Bearer ...` to upstreams that
  // declared `forward_client_headers: true`.
  token: string;
  // The decoded NextAuth JWT, used to populate x-hasura-* session_variables.
  // Null when the caller authenticated only via a Bearer header that
  // getToken() couldn't decode (pure non-browser flow). Callers that need
  // session variables must reject this case explicitly.
  jwt: JWT | null;
};

export async function authenticateRequest(req: NextApiRequest): Promise<AuthContext | null> {
  let token: string | null = null;
  if (req.headers.authorization) {
    const splits = req.headers.authorization.split(' ');
    if (splits.length > 1) {
      token = await decrypt(splits[1]);
    }
  }
  const jwt = await getToken({ req });
  if (!token && jwt) {
    token = (jwt.hasuraIdToken as string) || (jwt.idToken as string) || null;
  }
  if (!token) return null;
  return { token, jwt };
}

function pgArray(values: string[] | undefined): string {
  return `{${(values || []).join(',')}}`;
}

export function buildSessionVariables(jwt: JWT): Record<string, string> {
  const userRoles = (jwt.roles as string[]) || [];
  const tenantId = ((jwt.tenant as { id?: string } | undefined)?.id as string) || '';
  const userId = (jwt.id || jwt.sub) as string;
  // Default role priority matches the existing graphql.ts super-admin path.
  const rolePriority = [
    'super_admin',
    'tenant_admin',
    'account_admin',
    'account_admin_readonly',
    'k8s_namespace_admin',
    'k8s_namespace_admin_readonly',
  ];
  const defaultRole = rolePriority.find((r) => userRoles.includes(r)) || userRoles[0] || 'tenant_admin_readonly';
  const allowedRoles = elevateRoles(userRoles, jwt as unknown as Record<string, unknown>);
  // Promote super-admin sessions into allowed-roles so the api-server's
  // pg-array extraction (services/api/actions.go) sees the token and
  // elevates the security context. Idempotent against elevateRoles, which
  // EE may already use to add the same tokens.
  if (jwt.isSuperAdmin && !allowedRoles.includes('super_admin')) {
    allowedRoles.push('super_admin');
  }
  if (jwt.isSuperAdminReadonly && !allowedRoles.includes('super_admin_readonly')) {
    allowedRoles.push('super_admin_readonly');
  }
  return {
    'x-hasura-role': defaultRole,
    'x-hasura-allowed-roles': pgArray(allowedRoles),
    'x-hasura-user-id': userId,
    'x-hasura-user-tenant-id': tenantId,
    'x-hasura-user-account-ids': pgArray((jwt.accountIds as string[]) || []),
    'x-hasura-user-readonly-account-ids': pgArray((jwt.readOnlyAccountIds as string[]) || []),
    'x-hasura-user-namespaced-account-ids': pgArray((jwt.namespacedAccountIds as string[]) || []),
    'x-hasura-user-namespaced-readonly-account-ids': pgArray((jwt.namespacedReadOnlyAccountIds as string[]) || []),
  };
}

function resolveHandler(handler: string): string {
  return handler.replace(/\{\{(\w+)\}\}/g, (_, name) => process.env[name] || '');
}

export type ForwardOptions = {
  method: string;
  params: Record<string, unknown>;
  requestQuery?: string;
  sessionVariables: Record<string, string>;
  tenantId: string;
  userId: string;
  // Forwarded as Authorization header when the action's forward_client_headers
  // is true (matches Hasura's behavior).
  clientAuthorization?: string;
  traceparent: string;
  requestId: string;
};

export type ForwardError =
  | { kind: 'method_not_found'; method: string }
  | { kind: 'handler_unresolved'; method: string; handler: string }
  | { kind: 'forbidden'; method: string; role: string; allowedRoles: string[] }
  | { kind: 'upstream_unreachable'; method: string; url: string; detail: string }
  | { kind: 'upstream_parse_failed'; method: string; url: string; detail: string }
  | { kind: 'upstream_error'; method: string; url: string; status: number; payload: unknown };

export type ForwardResult = { ok: true; payload: unknown } | { ok: false; error: ForwardError };

// super_admin in allowed-roles bypasses action-level role gating, mirroring
// Hasura's admin-secret behavior. Allowed-roles is serialized as a Postgres-
// style array literal, e.g. `{tenant_admin,super_admin}`.
function hasSuperAdmin(allowedRolesPgArray: string | undefined): boolean {
  if (!allowedRolesPgArray) return false;
  return allowedRolesPgArray
    .replace(/[{}]/g, '')
    .split(',')
    .some((r) => r.trim() === 'super_admin');
}

export async function forwardAction(opts: ForwardOptions): Promise<ForwardResult> {
  const route = routes[opts.method];
  if (!route) return { ok: false, error: { kind: 'method_not_found', method: opts.method } };

  // Action-level role gate. Hasura blocks calls whose active role isn't in
  // the action's `permissions:` list; we replicate that here. super_admin
  // sessions bypass the gate.
  const role = opts.sessionVariables['x-hasura-role'] || '';
  const allowedRolesHeader = opts.sessionVariables['x-hasura-allowed-roles'];
  if (!hasSuperAdmin(allowedRolesHeader) && !route.allowedRoles.has(role)) {
    return {
      ok: false,
      error: {
        kind: 'forbidden',
        method: opts.method,
        role,
        allowedRoles: [...route.allowedRoles],
      },
    };
  }

  const upstreamUrl = resolveHandler(route.handler);
  if (!upstreamUrl) {
    return { ok: false, error: { kind: 'handler_unresolved', method: opts.method, handler: route.handler } };
  }

  // Apply Hasura's recursive list-input coercion before forwarding. Both the
  // GraphQL bypass and JSON-RPC callers benefit: the upstream Go decoders
  // expect lists where the schema declares them, and silently producing
  // wrong-shape input is the worst failure mode here.
  const coercedInput = coerceActionInput(opts.method, opts.params);

  const upstreamBody = {
    action: { name: opts.method },
    input: coercedInput,
    request_query: opts.requestQuery || '',
    session_variables: opts.sessionVariables,
  };

  const upstreamHeaders: Record<string, string> = {
    'Content-Type': 'application/json',
    traceparent: opts.traceparent,
    'X-Request-ID': opts.requestId,
    // Services read these directly (not via session_variables) — see
    // hasura_actions.go:47-48 (buildContextFromHasuraPayload).
    'x-tenant-id': opts.tenantId,
    'x-user-id': opts.userId,
  };
  if (process.env.ACTION_API_SERVER_TOKEN) {
    upstreamHeaders['X-ACTION-TOKEN'] = process.env.ACTION_API_SERVER_TOKEN;
  }
  if (route.forwardClientHeaders && opts.clientAuthorization) {
    upstreamHeaders['Authorization'] = opts.clientAuthorization;
  }

  let upstream: Response;
  try {
    upstream = await fetch(upstreamUrl, {
      method: 'POST',
      headers: upstreamHeaders,
      body: JSON.stringify(upstreamBody),
    });
  } catch (err: unknown) {
    const detail = err instanceof Error ? err.message : String(err);
    return { ok: false, error: { kind: 'upstream_unreachable', method: opts.method, url: upstreamUrl, detail } };
  }

  const upstreamCT = upstream.headers.get('content-type') || '';
  let payload: unknown;
  try {
    payload = upstreamCT.includes('application/json') ? await upstream.json() : await upstream.text();
  } catch (err: unknown) {
    const detail = err instanceof Error ? err.message : String(err);
    return { ok: false, error: { kind: 'upstream_parse_failed', method: opts.method, url: upstreamUrl, detail } };
  }

  if (!upstream.ok) {
    return { ok: false, error: { kind: 'upstream_error', method: opts.method, url: upstreamUrl, status: upstream.status, payload } };
  }
  return { ok: true, payload };
}

// --- GraphQL bypass --------------------------------------------------------
// Lets /api/graphql skip Hasura by parsing the incoming operation, mapping
// each top-level field to an action, and forwarding via forwardAction. Falls
// back to "not handled" for anything we don't yet support (subscriptions,
// fragments, unknown actions) so the caller can keep proxying to Hasura.

export type ParsedField = {
  name: string; // action name (= GraphQL field name)
  alias: string; // response key (alias if present, else field name)
  input: Record<string, unknown>;
  // The GraphQL selection set on this top-level field. Used to prune the
  // upstream response so the bypass returns only the fields the query asked
  // for, matching Hasura's selection-pruning behavior.
  selectionSet?: SelectionSetNode;
};

export type ParsedOperation = { ok: true; type: 'query' | 'mutation'; fields: ParsedField[] } | { ok: false; reason: string };

function resolveValue(value: ValueNode, variables: Record<string, unknown> | undefined): unknown {
  switch (value.kind) {
    case 'Variable':
      return variables?.[value.name.value];
    case 'IntValue':
      return parseInt(value.value, 10);
    case 'FloatValue':
      return parseFloat(value.value);
    case 'StringValue':
      return value.value;
    case 'BooleanValue':
      return value.value;
    case 'NullValue':
      return null;
    case 'EnumValue':
      return value.value;
    case 'ListValue':
      return value.values.map((v) => resolveValue(v, variables));
    case 'ObjectValue': {
      const obj: Record<string, unknown> = {};
      for (const f of value.fields) obj[f.name.value] = resolveValue(f.value, variables);
      return obj;
    }
    default:
      return null;
  }
}

function isNonNullType(t: TypeNode): boolean {
  return t.kind === 'NonNullType';
}

export function parseOperation(query: string, variables: Record<string, unknown> | undefined): ParsedOperation {
  let doc;
  try {
    doc = parse(query, { noLocation: true });
  } catch (err: unknown) {
    const detail = err instanceof Error ? err.message : String(err);
    return { ok: false, reason: `parse_failed: ${detail}` };
  }
  const ops = doc.definitions.filter((d) => d.kind === 'OperationDefinition') as OperationDefinitionNode[];
  if (ops.length === 0) return { ok: false, reason: 'no_operation' };
  if (ops.length > 1) return { ok: false, reason: 'multi_operation' };
  const op = ops[0];
  if (op.operation === 'subscription') return { ok: false, reason: 'subscription_unsupported' };
  if (doc.definitions.some((d) => d.kind === 'FragmentDefinition')) {
    return { ok: false, reason: 'fragments_unsupported' };
  }

  // Apply default values from the operation's variableDefinitions, then check
  // that every required (non-null, no-default) variable is supplied. Hasura
  // does both at validation time; without this the bypass would silently
  // forward `undefined` (which JSON-stringifies away) and let the upstream
  // see an empty field — at best a confusing 400, at worst a query scoped
  // to "no account" if the missing var was a tenant id.
  const effectiveVars: Record<string, unknown> = { ...variables };
  for (const vd of op.variableDefinitions || []) {
    const name = vd.variable.name.value;
    const provided = variables && Object.prototype.hasOwnProperty.call(variables, name) && variables[name] !== undefined;
    if (!provided) {
      if (vd.defaultValue) {
        effectiveVars[name] = resolveValue(vd.defaultValue, undefined);
      } else if (isNonNullType(vd.type)) {
        // Reject up front so the caller sees a clear "missing required
        // variable" diagnostic rather than a downstream decode error.
        return { ok: false, reason: `missing_required_variable:$${name}` };
      }
    }
  }

  const fields: ParsedField[] = [];
  for (const sel of op.selectionSet.selections) {
    if (sel.kind !== 'Field') return { ok: false, reason: `non_field_selection_${sel.kind}` };
    const field = sel as FieldNode;
    const name = field.name.value;
    const alias = field.alias?.value || name;
    const input: Record<string, unknown> = {};
    for (const arg of field.arguments || []) input[arg.name.value] = resolveValue(arg.value, effectiveVars);
    fields.push({ name, alias, input, selectionSet: field.selectionSet });
  }
  if (fields.length === 0) return { ok: false, reason: 'no_fields' };
  return { ok: true, type: op.operation, fields };
}

// Prune a JSON value to the shape requested by a GraphQL selection set —
// matches what Hasura does to action responses today. Without this, bypass
// would leak any field the upstream returns even if the GraphQL query didn't
// ask for it (extra wire bytes, plus possible exposure of fields the schema
// hides from the frontend).
//
// Known limitation: __typename injection isn't implemented. The audit found
// no __typename usage in frontend queries, so this is fine in practice.
export function applySelection(sel: SelectionSetNode | undefined, value: unknown): unknown {
  if (!sel) return value; // scalar leaf — nothing to prune
  if (value === null || value === undefined) return value;
  if (Array.isArray(value)) return value.map((v) => applySelection(sel, v));
  if (typeof value !== 'object') return value;
  const obj = value as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const s of sel.selections) {
    if (s.kind !== 'Field') continue; // fragments are rejected upstream by parseOperation
    const fieldName = s.name.value;
    const key = s.alias?.value || fieldName;
    if (!(fieldName in obj)) continue;
    out[key] = applySelection(s.selectionSet, obj[fieldName]);
  }
  return out;
}

export type GraphQLBypassResult =
  | {
      handled: true;
      status: number;
      body: {
        data: Record<string, unknown> | null;
        errors?: Array<{ message: string; path?: string[]; extensions?: Record<string, unknown> }>;
      };
    }
  | { handled: false; reason: string };

export async function tryBypassGraphQL(opts: {
  query: string;
  variables: Record<string, unknown> | undefined;
  jwt: JWT;
  clientAuthorization?: string;
  traceparent: string;
  requestId: string;
}): Promise<GraphQLBypassResult> {
  const parsed = parseOperation(opts.query, opts.variables);
  if (!parsed.ok) return { handled: false, reason: parsed.reason };

  // Bail before forwarding if any field's action isn't routable — we want
  // a clean handled:false rather than a partial response. The caller (e.g.
  // /api/graphql) turns this into a 502 with the unknown action name.
  for (const f of parsed.fields) {
    if (!routes[f.name]) return { handled: false, reason: `unknown_action:${f.name}` };
  }

  const sessionVariables = buildSessionVariables(opts.jwt);
  const tenantId = sessionVariables['x-hasura-user-tenant-id'];
  const userId = sessionVariables['x-hasura-user-id'];

  const results = await Promise.all(
    parsed.fields.map((f) =>
      forwardAction({
        method: f.name,
        // Coercion is now applied inside forwardAction so /api/rpc gets
        // the same treatment.
        params: f.input,
        // Pass the original GraphQL query so handlers that still parse
        // request_query for column selection keep working.
        requestQuery: opts.query,
        sessionVariables,
        tenantId,
        userId,
        clientAuthorization: opts.clientAuthorization,
        traceparent: opts.traceparent,
        requestId: opts.requestId,
      })
    )
  );

  const data: Record<string, unknown> = {};
  const errors: Array<{ message: string; path?: string[] }> = [];
  parsed.fields.forEach((f, i) => {
    const r = results[i];
    if (r.ok) {
      data[f.alias] = applySelection(f.selectionSet, r.payload);
    } else {
      data[f.alias] = null;
      const err: { message: string; path?: string[]; extensions?: Record<string, unknown> } = {
        message: forwardErrorMessage(r.error),
        path: [f.alias],
      };
      if (r.error.kind === 'forbidden') {
        err.extensions = { code: 'FORBIDDEN', role: r.error.role, allowedRoles: r.error.allowedRoles };
      }
      errors.push(err);
    }
  });

  return {
    handled: true,
    status: 200, // GraphQL convention: errors live in the body, not the status code
    body: { data, ...(errors.length ? { errors } : {}) },
  };
}

// Synthetic admin JWT for server-side callers (NextAuth callbacks,
// server-only API routes). The `admin` role + empty tenant/user ids
// route the upstream `buildContextFromHasuraPayload` to the dedicated
// super-admin branch (`NewSecurityContextForSuperAdmin()`), which keeps
// `tenantId == ""` so the query engine skips its auto-injected
// `tenant_id = current_tenant` filter (service.go:59). isSuperAdmin=true
// on the JWT also adds super_admin to allowed-roles, which bypasses
// forwardAction's action-level role gate.
//
// Previously the ids were the nil UUID to satisfy a hypothetical
// uuid-parse path; in practice that path is never hit because the
// admin branch above short-circuits before any DB lookup, and the
// nil-UUID was the root cause of `tenant_v2 WHERE id = '00000000-…'`
// silently returning zero rows in cross-tenant lookups (e.g. switch
// tenant for super admin).
const SERVER_ADMIN_JWT = {
  isSuperAdmin: true,
  roles: ['admin'],
  tenant: { id: '' },
  id: '',
  sub: '',
  accountIds: [],
  readOnlyAccountIds: [],
  namespacedAccountIds: [],
  namespacedReadOnlyAccountIds: [],
} as unknown as JWT;

// Entry point for queryGraphQL() running on the server. Wraps
// tryBypassGraphQL with a synthetic admin session so callers that go
// through getGQLEndpoint() directly — NextAuth callbacks, server-only
// API routes — share the same code path as /api/graphql. Returns
// handled:false for anything the gateway can't translate (unknown
// actions, subscriptions, parse errors); the caller surfaces this as
// an error.
export function bypassGraphQLAsServer(opts: {
  query: string;
  variables: Record<string, unknown> | undefined;
  traceparent: string;
  requestId: string;
}): Promise<GraphQLBypassResult> {
  return tryBypassGraphQL({
    query: opts.query,
    variables: opts.variables,
    jwt: SERVER_ADMIN_JWT,
    traceparent: opts.traceparent,
    requestId: opts.requestId,
  });
}

function forwardErrorMessage(err: ForwardError): string {
  switch (err.kind) {
    case 'method_not_found':
      return `Method not found: ${err.method}`;
    case 'handler_unresolved':
      return `Handler URL unresolved for ${err.method}`;
    case 'forbidden':
      return `Role '${err.role}' is not permitted to invoke '${err.method}'`;
    case 'upstream_unreachable':
      return `Upstream unreachable for ${err.method} at ${err.url}: ${err.detail}`;
    case 'upstream_parse_failed':
      return `Upstream response parse failed for ${err.method} at ${err.url}: ${err.detail}`;
    case 'upstream_error': {
      const detail = extractUpstreamErrorDetail(err.payload);
      return `Upstream error from ${err.method} at ${err.url} (HTTP ${err.status})${detail ? `: ${detail}` : ''}`;
    }
  }
}

// Hasura action handlers signal errors with `{ message, code?, extensions? }`.
// Fall back to a stringified payload (truncated) so non-conforming upstreams
// still surface something useful.
function extractUpstreamErrorDetail(payload: unknown): string {
  if (payload && typeof payload === 'object') {
    const message = (payload as { message?: unknown }).message;
    if (typeof message === 'string' && message.length > 0) return message;
  }
  if (typeof payload === 'string' && payload.length > 0) return payload.slice(0, 500);
  try {
    const serialized = JSON.stringify(payload);
    return serialized ? serialized.slice(0, 500) : '';
  } catch {
    return '';
  }
}
