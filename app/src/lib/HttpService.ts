import axios, { type AxiosInstance } from 'axios';
import { getGqlString } from '@lib/datetime';
import crypto from 'crypto';
//import { loadProgressBar } from 'axios-progress-bar';

const Axios: AxiosInstance = axios.create();

export const getClient = () => Axios;

function isServer() {
  return typeof window === 'undefined' ? true : false;
}

export function getGQLEndpoint() {
  // Browser only. Server-side callers go through the in-process gateway
  // registered on globalThis by instrumentation.ts (see queryGraphQL below).
  return '/api/graphql';
}

// Server-side GraphQL gateway entry point registered by instrumentation.ts
// on boot. Stashed on globalThis so this isomorphic file can call it without
// a static import (which would pull rpcGateway → rpcRoutes → fs/bcrypt into
// the browser bundle). Always present on the server post-boot; undefined on
// the client.
type ServerGateway = (opts: {
  query: string;
  variables: Record<string, unknown> | undefined;
  traceparent: string;
  requestId: string;
}) => Promise<{ handled: true; status: number; body: any } | { handled: false; reason: string }>;

export function getRelayServerEndpoint() {
  return isServer() ? process.env.RELAY_SERVER_ENDPOINT || 'http://localhost:52832' : '/api/proxy/relay';
}

function getTraceHeaders(existingHeaders?: Record<string, string>): Record<string, string> {
  let traceParent = '';
  let requestId = '';
  if (existingHeaders && existingHeaders['traceparent']) {
    traceParent = existingHeaders['traceparent'];
  }
  if (existingHeaders && existingHeaders['x-request-id']) {
    requestId = existingHeaders['x-request-id'];
  }
  if (!traceParent) {
    const version = Buffer.alloc(1).toString('hex');
    const traceId = crypto.randomBytes(16).toString('hex');
    const id = crypto.randomBytes(8).toString('hex');
    const flags = '01';
    traceParent = `${version}-${traceId}-${id}-${flags}`;
  }
  if (!requestId) {
    requestId = traceParent;
  }

  return {
    traceparent: traceParent,
    'X-Request-ID': requestId,
  };
}

function getGQLDefaultHeaders(existingHeaders?: Record<string, string>) {
  const headers: Record<string, string> = getTraceHeaders(existingHeaders);
  // Merge any additional custom headers (e.g. x-tenant-id) that aren't trace or default headers
  if (existingHeaders) {
    for (const [key, value] of Object.entries(existingHeaders)) {
      if (!(key in headers)) {
        headers[key] = value;
      }
    }
  }
  headers['Content-Type'] = 'application/json';
  return headers;
}

export const queryGraphQL = async (
  operationsDoc: string,
  operationName: string,
  variables?: any,
  headers?: Record<string, string>,
  signal?: AbortSignal
) => {
  try {
    const updatedVariables: any = {};
    if (variables) {
      for (const [k, v] of Object.entries(variables)) {
        if (v instanceof Date) {
          updatedVariables[k] = getGqlString(v);
        } else {
          updatedVariables[k] = v;
        }
      }
    }

    // Server-side callers (NextAuth callbacks, server-only API routes) go
    // through the in-process GraphQL gateway. The function is registered on
    // globalThis from instrumentation.ts at server boot. Falling back to an
    // HTTP request from inside the same process would just loop back through
    // /api/graphql to the same gateway, so we surface a clear error instead
    // if it's missing.
    if (isServer()) {
      const serverGateway = (globalThis as { __nbBypassGraphQLAsServer?: ServerGateway }).__nbBypassGraphQLAsServer;
      if (!serverGateway) {
        const msg = `[queryGraphQL] server gateway not registered for operation ${operationName}`;
        console.error(msg);
        return { data: { errors: [{ message: 'server_gateway_unavailable' }] }, status: 503 } as any;
      }
      const traceHdrs = getTraceHeaders(headers);
      const result = await serverGateway({
        query: operationsDoc,
        variables: updatedVariables,
        traceparent: traceHdrs.traceparent,
        requestId: traceHdrs['X-Request-ID'],
      });
      if (result.handled) {
        if (result.body?.errors?.length) {
          console.log('[queryGraphQL] server gateway returned errors for', operationName, JSON.stringify(result.body.errors));
        }
        return { data: result.body, status: result.status } as any;
      }
      return { data: { errors: [{ message: `server_gateway_unhandled:${result.reason}` }] }, status: 502 } as any;
    }

    const result = await getClient().post(
      getGQLEndpoint(),
      {
        query: operationsDoc,
        variables: updatedVariables,
        operationName,
      },
      {
        headers: getGQLDefaultHeaders(headers),
        signal,
      }
    );

    if (result.status == 401 && !isServer()) {
      window.location.href = '/api/auth/signin';
    } else if (result.status == 500 && !isServer()) {
      if (result.data.errors && result.data.errors[0].extensions && result.data.errors[0].extensions.code == 'invalid-jwt') {
        window.location.href = '/api/auth/signin';
      }
    }

    return result;
  } catch (error) {
    console.log('error on api call', error);
    const e = error as any;
    if (e.response?.status == 401 && !isServer()) {
      window.location.href = '/api/auth/signin';
    } else if (e.response?.status == 500 && !isServer()) {
      if (e.response?.data?.errors && e.response?.data?.errors[0].extensions && e.response?.data?.errors[0].extensions.code == 'invalid-jwt') {
        window.location.href = '/api/auth/signin';
      }
    } else {
      return e.response || e.request;
    }
  }
};

export type ParallelQueryConfig = {
  query: string;
  operationName: string;
  variables?: any;
  headers?: Record<string, string>;
  signal?: AbortSignal;
};

/**
 * Runs multiple GraphQL queries in parallel and merges their `data.data` fields
 * into a single response with the same shape as a regular `queryGraphQL` call.
 * Failed individual queries are silently skipped (their keys are absent from the result).
 */
export const queryGraphQLParallel = async (
  queries: ParallelQueryConfig[],
  acceptPartialData = true
): Promise<{ data: { data: Record<string, any>; errors?: any[] } }> => {
  const results = await Promise.allSettled(
    queries.map(({ query, operationName, variables, headers, signal }) => queryGraphQL(query, operationName, variables, headers, signal))
  );

  const mergedData: Record<string, any> = {};
  const allErrors: any[] = [];
  let firstError: any = null;

  for (let i = 0; i < results.length; i++) {
    const result = results[i];
    const opName = queries[i].operationName;
    if (result.status === 'fulfilled' && result.value?.data?.data) {
      Object.assign(mergedData, result.value.data.data);
      if (result.value?.data?.errors) allErrors.push(...result.value.data.errors);
    } else if (result.status === 'fulfilled' && result.value?.data?.errors) {
      console.error(`[queryGraphQLParallel] GraphQL error in "${opName}":`, result.value.data.errors);
      allErrors.push(...result.value.data.errors);
      if (!firstError) firstError = result.value;
    } else if (result.status === 'rejected') {
      console.error(`[queryGraphQLParallel] Network error in "${opName}":`, result.reason);
      if (!firstError) firstError = result.reason;
    }
  }

  if (firstError && !acceptPartialData) {
    return firstError;
  }

  const response: { data: { data: Record<string, any>; errors?: any[] } } = { data: { data: mergedData } };
  if (allErrors.length > 0) response.data.errors = allErrors;
  return response;
};

function parseQueryHeader(query: string): {
  variablesDecl: string;
  bodyStart: number;
  bodyEnd: number;
} | null {
  const opMatch = query.match(/^(query|mutation|subscription)\s+\w+/);
  if (!opMatch) return null;

  let pos = opMatch[0].length;
  while (pos < query.length && /\s/.test(query[pos])) pos++;

  let variablesDecl = '';
  if (query[pos] === '(') {
    let depth = 0;
    const start = pos;
    while (pos < query.length) {
      if (query[pos] === '(') depth++;
      else if (query[pos] === ')') {
        depth--;
        if (depth === 0) {
          pos++;
          break;
        }
      }
      pos++;
    }
    variablesDecl = query.slice(start, pos);
  }

  while (pos < query.length && /\s/.test(query[pos])) pos++;
  if (query[pos] !== '{') return null;

  return { variablesDecl, bodyStart: pos + 1, bodyEnd: query.lastIndexOf('}') };
}

function extractTopLevelFields(body: string): string[] {
  const fields: string[] = [];
  let braceDepth = 0;
  let parenDepth = 0;
  let fieldStart = -1;

  for (let i = 0; i < body.length; i++) {
    const ch = body[i];
    if (ch === '(') parenDepth++;
    else if (ch === ')') parenDepth--;
    else if (ch === '{' && parenDepth === 0) braceDepth++;
    else if (ch === '}' && parenDepth === 0) {
      braceDepth--;
      if (braceDepth === 0 && fieldStart !== -1) {
        fields.push(body.slice(fieldStart, i + 1).trim());
        fieldStart = -1;
      }
    } else if (braceDepth === 0 && parenDepth === 0) {
      if (fieldStart === -1 && /\S/.test(ch)) {
        fieldStart = i;
      } else if (fieldStart !== -1 && /\s/.test(ch)) {
        // Scalar field: peek ahead — flush only when no `{`, `(`, or `:` follows,
        // and the accumulated content doesn't end with `:` (alias prefix).
        let j = i + 1;
        while (j < body.length && /\s/.test(body[j])) j++;
        const nextCh = j < body.length ? body[j] : '';
        const currentToken = body.slice(fieldStart, i).trim();
        if (!currentToken.endsWith(':') && nextCh !== '{' && nextCh !== '(' && nextCh !== ':') {
          fields.push(currentToken);
          fieldStart = -1;
        }
      }
    }
  }

  // Flush any trailing scalar field
  if (fieldStart !== -1) {
    const remaining = body.slice(fieldStart).trim();
    if (remaining) fields.push(remaining);
  }

  return fields;
}

function parseVariableDeclarations(variablesDecl: string): Array<{ name: string; decl: string }> {
  if (!variablesDecl) return [];
  const inner = variablesDecl.slice(1, -1).trim();
  if (!inner) return [];
  const vars: Array<{ name: string; decl: string }> = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i <= inner.length; i++) {
    const ch = inner[i];
    if (ch === '[' || ch === '(') depth++;
    else if (ch === ']' || ch === ')') depth--;
    else if ((ch === ',' || i === inner.length) && depth === 0) {
      const part = inner.slice(start, i).trim();
      const m = part.match(/^\$(\w+)/);
      if (m) vars.push({ name: m[1], decl: part });
      start = i + 1;
    }
  }
  return vars;
}

/**
 * Drop-in replacement for `queryGraphQL` that auto-splits a multi-field query
 * into one request per top-level field, fires them all in parallel, and returns
 * a merged response with the same shape (`response.data.data.fieldName`).
 * Falls back to a single request if the query has only one field or cannot be parsed.
 */
export const splitAndParallelQuery = async (
  combinedQuery: string,
  operationName: string,
  variables?: any,
  headers?: Record<string, string>,
  signal?: AbortSignal
): Promise<{ data: { data: Record<string, any> } }> => {
  const trimmed = combinedQuery.trim();
  const header = parseQueryHeader(trimmed);

  if (!header) {
    return queryGraphQL(combinedQuery, operationName, variables, headers, signal);
  }

  const { variablesDecl, bodyStart, bodyEnd } = header;
  const fields = extractTopLevelFields(trimmed.slice(bodyStart, bodyEnd));

  if (fields.length <= 1) {
    return queryGraphQL(combinedQuery, operationName, variables, headers, signal);
  }

  const allVars = parseVariableDeclarations(variablesDecl);

  const queries: ParallelQueryConfig[] = fields.map((field, i) => {
    const alias = field.match(/^(\w+)/)?.[1] ?? `field${i}`;
    const opName = `${operationName}_${alias}`;
    const usedVars = allVars.filter((v) => new RegExp(`\\$${v.name}\\b`).test(field));
    const usedVarsDecl = usedVars.map((v) => v.decl).join(', ');
    const varsStr = usedVarsDecl ? `(${usedVarsDecl})` : '';
    const filteredVariables = variables ? Object.fromEntries(usedVars.map((v) => [v.name, variables[v.name]])) : undefined;
    return {
      query: `query ${opName}${varsStr} {\n  ${field}\n}`,
      operationName: opName,
      variables: filteredVariables,
      headers,
      signal,
    };
  });

  return queryGraphQLParallel(queries);
};

export function gqlStringify(obj_from_json: any, enums: string[] = [], pkey = ''): string {
  if (typeof obj_from_json !== 'object') {
    if (enums && enums.indexOf(pkey) >= 0) {
      return obj_from_json;
    }
    return JSON.stringify(obj_from_json);
  }

  if (Array.isArray(obj_from_json)) {
    return `[${obj_from_json.map((o) => gqlStringify(o, enums, pkey)).join(',')}]`;
  }

  const ops = [
    '_eq',
    '_ne',
    '_lt',
    '_gt',
    '_le',
    '_ge',
    '_neq',
    '_in',
    '_nin',
    '_like',
    '_nlike',
    '_ilike',
    '_nilike',
    '_similar',
    '_nsimilar',
    '_is_null',
    '_is_null',
  ];
  const props = Object.keys(obj_from_json || {})
    .map((key) => {
      if (ops.indexOf(key) >= 0) {
        return `${key}:${gqlStringify(obj_from_json[key], enums, pkey)}`;
      }
      return `${key}:${gqlStringify(obj_from_json[key], enums, key)}`;
    })
    .join(',');
  return `{${props}}`;
}

export const hitRelayServer = async (data: any, type = '') => {
  try {
    let headerObject = getTraceHeaders();
    let pathVariable = '/request';
    if (type && type == 'grafana') {
      headerObject = {
        ...headerObject,
        'X-NB-ACCOUNT-ID': data.accountId,
        'X-NB-REQUEST-ID': crypto.randomUUID(),
        'X-SECRET-KEY': '',
        'X-REQUEST-TYPE': 'grafana',
      };
      pathVariable = '/grafana';
    }
    const result = await getClient().post(getRelayServerEndpoint() + pathVariable, data, {
      headers: headerObject,
    });
    return result;
  } catch (error: any) {
    console.log('error on external api call', error);
    return error?.response;
  }
};
