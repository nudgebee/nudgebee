import { getToken, type JWT } from 'next-auth/jwt';
import type { NextApiRequest } from 'next';
import { parse, type FieldNode, type OperationDefinitionNode, type SelectionSetNode, type TypeNode, type ValueNode } from 'graphql';
import { decodeSessionJWT, decrypt } from '@lib/internal';
import { loadActionInputSchema, loadRpcRoutes, type SchemaFieldInfo } from '@lib/rpcRoutes';
import { elevateRoles } from '@lib/authHooks';
import { pickDefaultRole } from '@lib/rolePriority';

// Shared primitives for the RPC-bypass path. Used by both /api/rpc
// (JSON-RPC envelope) and /api/graphql (GraphQL envelope when the bypass is
// enabled). The endpoints handle protocol shape; everything else — auth,
// route lookup, env-var URL resolution, RpcActionRequest construction,
// upstream fetch, error classification — lives here.

const routes = loadRpcRoutes();
const inputSchema = loadActionInputSchema();

// Apply RPC/GraphQL list-input coercion recursively. When a field is
// declared as a list but the caller passed a single value, wrap it in a
// single-element list — and recurse into nested objects/lists so operators
// like `_in: "x"` (schema: `_in: [String!]`) survive the bypass. Without
// this, the upstream Go decoder fails with a confusing "cannot unmarshal
// string into []string" while the RPC path coerces silently.
function coerceValue(value: unknown, info: SchemaFieldInfo | undefined): unknown {
  if (value === null || value === undefined) return value;
  if (!info) return value; // unknown field — pass through, upstream decides

  // Step 1: if the schema says list but the caller passed a scalar/object,
  // wrap it. We only wrap when the value can be a list element — primitives,
  // objects, arrays of objects. RPC does the same.
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
  // declared `forward_client_headers: true`. Empty for browser-cookie flow
  // (no Bearer token to forward); non-empty for nbctl/bearer flow.
  token: string;
  // The decoded session JWT (NextAuth cookie OR synthesized from a bearer
  // token), used to populate session_variables sent to upstream action
  // handlers.
  jwt: JWT;
};

// Synthesize a JWT-shaped object from a decoded session JWT. Used in the
// Bearer-only flow (non-browser callers like nbctl) where there's no
// NextAuth cookie, so getToken({req}) returns null and we have to derive
// the same shape from the bearer token's decoded payload.
//
// Bearer tokens minted by /api/auth/token are tenant-scoped by design:
// super-admin elevation flags (isSuperAdmin / isSuperAdminReadonly) are
// set by the EE elevator at NextAuth session creation, not stored in the
// signed JWT itself. So the synthesized JWT here will never carry those
// flags — Bearer callers can't perform super-admin operations.
function synthesizeJwtFromBearer(rawJwt: string): Promise<JWT | null> {
  return decodeSessionJWT(rawJwt)
    .then((decoded) => {
      const p = decoded.payload as Record<string, unknown>;
      const userId = p.user_id as string | undefined;
      if (!userId) return null;
      const synth: Record<string, unknown> = {
        id: userId,
        sub: userId,
        tenant: { id: (p.tenant_id as string) ?? '' },
        roles: (p.allowed_roles as string[]) ?? [],
        accountIds: (p.account_ids as string[]) ?? [],
        readOnlyAccountIds: (p.readonly_account_ids as string[]) ?? [],
        namespacedAccountIds: (p.namespaced_account_ids as string[]) ?? [],
        namespacedReadOnlyAccountIds: (p.namespaced_readonly_account_ids as string[]) ?? [],
      };
      return synth as unknown as JWT;
    })
    .catch(() => null);
}

export async function authenticateRequest(req: NextApiRequest): Promise<AuthContext | null> {
  let token: string | null = null;
  if (req.headers.authorization) {
    const splits = req.headers.authorization.split(' ');
    if (splits.length > 1) {
      try {
        token = await decrypt(splits[1]);
      } catch {
        // malformed/tampered Bearer — treat as no token, fall through to cookie path
      }
    }
  }
  let jwt = await getToken({ req });
  // Bearer-only flow (no NextAuth cookie): decode the bearer JWT and
  // synthesize the JWT shape buildSessionVariables expects. Without this,
  // callers like nbctl get 401 even though the token is valid.
  if (token && !jwt) {
    jwt = await synthesizeJwtFromBearer(token);
    if (!jwt) return null; // verifiable failure — reject
  }
  // Browser flow: jwt comes from the NextAuth cookie and is sufficient on
  // its own — no Bearer token is forwarded. `token` stays empty; downstream
  // forwardAction already gates Authorization-header emission on a
  // non-empty clientAuthorization, so missing token is a no-op there.
  if (!jwt) return null;
  return { token: token ?? '', jwt };
}

// SessionVariables is the body block sent to upstream action handlers.
// Snake_case matches typical JSON-API conventions and Go's default json
// tag style. The account-ids family (previously RPC RLS inputs) has no
// consumers post-RPC; only the 4 fields below are needed.
export type SessionVariables = {
  role: string;
  allowed_roles: string[];
  user_id: string;
  tenant_id: string;
};

export function buildSessionVariables(jwt: JWT): SessionVariables {
  const userRoles = (jwt.roles as string[]) || [];
  const tenantId = ((jwt.tenant as { id?: string } | undefined)?.id as string) || '';
  const userId = ((jwt.id || jwt.sub) as string) || '';
  // Default role: pick the highest-priority role the user holds (shared with
  // internal.ts via @lib/rolePriority). Fallback chain: user's first listed
  // role, then 'tenant_admin_readonly' as a final safety net.
  const defaultRole = pickDefaultRole(userRoles, userRoles[0] || 'tenant_admin_readonly');
  // Copy before any mutation — elevateRoles' default impl returns the input
  // array unchanged, so the promote step below would otherwise .push onto the
  // JWT's own roles slice and leak super_admin into the cached session.
  const allowedRoles = [...elevateRoles(userRoles, jwt as unknown as Record<string, unknown>)];
  // Promote super-admin sessions into allowed-roles so the api-server's role
  // extraction (services/api/actions.go:sessionAllowedRoles) sees the token
  // and elevates the security context via AddRole. Distinct tokens for full
  // vs readonly so destructive action gates (tenant_delete etc.) can reject
  // readonly. Idempotent against elevateRoles.
  if (jwt.isSuperAdmin && !allowedRoles.includes('super_admin')) {
    allowedRoles.push('super_admin');
  }
  if (jwt.isSuperAdminReadonly && !allowedRoles.includes('super_admin_readonly')) {
    allowedRoles.push('super_admin_readonly');
  }
  return {
    role: defaultRole,
    allowed_roles: allowedRoles,
    user_id: userId,
    tenant_id: tenantId,
  };
}

function resolveHandler(handler: string): string {
  return handler.replace(/\{\{(\w+)\}\}/g, (_, name) => process.env[name] || '');
}

export type ForwardOptions = {
  method: string;
  params: Record<string, unknown>;
  requestQuery?: string;
  sessionVariables: SessionVariables;
  tenantId: string;
  userId: string;
  // Forwarded as Authorization header when the action's forward_client_headers
  // is true (matches RPC's behavior).
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

export async function forwardAction(opts: ForwardOptions): Promise<ForwardResult> {
  const route = routes[opts.method];
  if (!route) return { ok: false, error: { kind: 'method_not_found', method: opts.method } };

  // Action-level role gate. Allow the call if the user holds ANY role that
  // the action permits, not just the active/default role — RPC's gate
  // worked on the active role because it also exposed role-switching to
  // clients, which we don't replicate. A user with `[tenant_admin_readonly,
  // account_admin]` whose `defaultRole` is `tenant_admin_readonly` would
  // otherwise be falsely denied from `account_admin`-only actions even
  // though they genuinely have account_admin. super_admin sessions bypass.
  const role = opts.sessionVariables.role || '';
  const allowedRoles = opts.sessionVariables.allowed_roles || [];
  const isSuperAdmin = allowedRoles.includes('super_admin');
  const hasAllowedRole = allowedRoles.some((r) => route.allowedRoles.has(r));
  if (!isSuperAdmin && !hasAllowedRole) {
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

  // Apply RPC's recursive list-input coercion before forwarding. Both the
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
    // actions.go (buildContextFromPayload).
    'x-tenant-id': opts.tenantId,
    'x-user-id': opts.userId,
  };
  // Apply per-action headers declared in actions.yaml's `headers:` block.
  // The yaml is authoritative — every action must explicitly declare which
  // env var its destination expects (services-server: ACTION_API_SERVER_TOKEN,
  // llm-server: LLM_SERVER_TOKEN, cloud-collector-server:
  // CLOUD_COLLECTOR_SERVER_TOKEN). No silent fallback: a missing or
  // misspelled env in the yaml results in no header sent, and the
  // destination decides based on its own leniency (e.g. llm-server allows
  // an empty token but rejects a wrong one). rpcRoutes.ts warns at startup
  // for any action that lacks a headers block so misconfigurations surface
  // early instead of failing at request time.
  for (const h of route.headers) {
    const v = process.env[h.valueFromEnv];
    if (v) upstreamHeaders[h.name] = v;
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
// Lets /api/graphql skip RPC by parsing the incoming operation, mapping
// each top-level field to an action, and forwarding via forwardAction. Falls
// back to "not handled" for anything we don't yet support (subscriptions,
// fragments, unknown actions) so the caller can keep proxying to RPC.

export type ParsedField = {
  name: string; // action name (= GraphQL field name)
  alias: string; // response key (alias if present, else field name)
  input: Record<string, unknown>;
  // The GraphQL selection set on this top-level field. Used to prune the
  // upstream response so the bypass returns only the fields the query asked
  // for, matching RPC's selection-pruning behavior.
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
  // that every required (non-null, no-default) variable is supplied. RPC
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
// matches what RPC does to action responses today. Without this, bypass
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
  const tenantId = sessionVariables.tenant_id;
  const userId = sessionVariables.user_id;

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
      } else if (r.error.kind === 'upstream_error') {
        err.extensions = { upstream: { status: r.error.status, body: r.error.payload } };
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
// route the upstream `buildContextFromRpcPayload` to the dedicated
// super-admin branch (`NewSecurityContextForSuperAdmin()`), which keeps
// `tenantId == ""` so the query engine skips its auto-injected
// `tenant_id = current_tenant` filter (service.go:59). isSuperAdmin=true
// on the JWT also adds super_admin to allowed-roles, which bypasses
// forwardAction's action-level role gate.
//
// Previously the ids were the nil UUID to satisfy a hypothetical
// uuid-parse path; in practice that path is never hit because the
// admin branch above short-circuits before any DB lookup, and the
// nil-UUID was the root cause of `tenants_list WHERE id = '00000000-…'`
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
    case 'upstream_error':
      return extractUpstreamErrorDetail(err.payload) || genericMessageForStatus(err.status);
  }
}

function genericMessageForStatus(status: number): string {
  if (status === 429) return 'Rate limit exceeded. Please retry shortly.';
  if (status === 403) return 'You do not have permission to perform this action.';
  if (status === 401) return 'Authentication required. Please sign in again.';
  if (status >= 500) return 'Service is temporarily unavailable. Please retry.';
  return 'Request failed. Please retry.';
}

// Handles `{message}`, `{errors:[{message}]}`, and `[{message}]` (api-server's
// common.Error shape) envelopes our Go services emit; degenerate JSON
// (`{}` / `[]` / `null`) is treated as no-detail so the caller falls through
// to a per-status generic.
function extractUpstreamErrorDetail(payload: unknown): string {
  if (Array.isArray(payload) && payload.length > 0) {
    const first = (payload[0] as { message?: unknown } | undefined)?.message;
    if (typeof first === 'string' && first.length > 0) return first;
  }
  if (payload && typeof payload === 'object') {
    const obj = payload as { message?: unknown; errors?: Array<{ message?: unknown } | undefined> };
    if (typeof obj.message === 'string' && obj.message.length > 0) return obj.message;
    if (Array.isArray(obj.errors) && obj.errors.length > 0) {
      const first = obj.errors[0]?.message;
      if (typeof first === 'string' && first.length > 0) return first;
    }
  }
  if (typeof payload === 'string' && payload.length > 0) return payload.slice(0, 500);
  try {
    const serialized = JSON.stringify(payload);
    if (!serialized || serialized === '{}' || serialized === '[]' || serialized === 'null') return '';
    return serialized.slice(0, 500);
  } catch {
    return '';
  }
}
