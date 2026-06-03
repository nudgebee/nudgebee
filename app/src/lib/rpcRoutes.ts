import fs from 'fs';
import path from 'path';
import yaml from 'js-yaml';
import { parse, type InputObjectTypeDefinitionNode, type ObjectTypeDefinitionNode, type TypeNode } from 'graphql';

// Routing table for /api/rpc — the local app/src/lib/actions.yaml file
// next to this module is now the sole source of truth (RPC's metadata
// directory was deleted when the RPC service was removed). Edit it
// directly to register / unregister actions or change their handler URLs.
//
// Override the path via ACTIONS_YAML_PATH if needed.

export type RpcRoute = {
  handler: string;
  forwardClientHeaders: boolean;
  // Roles permitted to invoke this action (from actions.yaml `permissions:`).
  // Mirrors RPC's role-based gate: callers whose active role isn't in the
  // set get blocked at the gateway. Empty set means no role can invoke
  // (admin-secret only under RPC → super_admin only here).
  allowedRoles: Set<string>;
  // Per-action upstream headers from actions.yaml `headers:` block.
  // Each entry maps a header name to an env-var name; the gateway looks up
  // process.env[valueFromEnv] at request time. Used to pick the right
  // service-to-service token per destination (X-ACTION-TOKEN against
  // ACTION_API_SERVER_TOKEN for services-server, LLM_SERVER_TOKEN for
  // llm-server, etc.).
  headers: Array<{ name: string; valueFromEnv: string }>;
};

type ActionsYaml = {
  actions?: Array<{
    name?: string;
    definition?: {
      handler?: string;
      forward_client_headers?: boolean;
      headers?: Array<{ name?: string; value_from_env?: string }>;
    };
    permissions?: Array<{ role?: string }>;
  }>;
};

const DEFAULT_PATH = path.join(process.cwd(), 'src', 'lib', 'actions.yaml');
const EE_OVERLAY_PATH = path.join(process.cwd(), 'src', 'ee', 'actions.yaml');

let cached: Record<string, RpcRoute> | null = null;

function readActionsYaml(yamlPath: string, required: boolean): ActionsYaml | null {
  let raw: string;
  try {
    raw = fs.readFileSync(yamlPath, 'utf8');
  } catch (err: unknown) {
    if (!required) return null;
    const msg = err instanceof Error ? err.message : String(err);
    throw new Error(`[rpcRoutes] Failed to read actions.yaml at ${yamlPath}: ${msg}`);
  }
  try {
    return yaml.load(raw) as ActionsYaml;
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err);
    throw new Error(`[rpcRoutes] Failed to parse actions.yaml at ${yamlPath}: ${msg}`);
  }
}

export function loadRpcRoutes(): Record<string, RpcRoute> {
  if (cached) return cached;
  const yamlPath = process.env.ACTIONS_YAML_PATH || DEFAULT_PATH;
  const doc = readActionsYaml(yamlPath, true)!;
  const eeDoc = readActionsYaml(EE_OVERLAY_PATH, false);
  const allActions = [...(doc?.actions || []), ...(eeDoc?.actions || [])];
  const out: Record<string, RpcRoute> = {};
  const missingHeaders: string[] = [];
  for (const action of allActions) {
    if (!action.name || !action.definition?.handler) continue;
    const allowedRoles = new Set<string>();
    for (const p of action.permissions || []) {
      if (p.role) allowedRoles.add(p.role);
    }
    const headers: Array<{ name: string; valueFromEnv: string }> = [];
    for (const h of action.definition.headers || []) {
      if (h.name && h.value_from_env) {
        headers.push({ name: h.name, valueFromEnv: h.value_from_env });
      }
    }
    if (headers.length === 0) {
      missingHeaders.push(action.name);
    }
    out[action.name] = {
      handler: action.definition.handler,
      forwardClientHeaders: !!action.definition.forward_client_headers,
      allowedRoles,
      headers,
    };
  }
  // The gateway no longer has a silent fallback for X-ACTION-TOKEN — every
  // action must declare its own `headers:` block in actions.yaml with the
  // env var the destination's middleware expects. Warn loudly at startup
  // for any action that's missing one so misconfigurations surface here
  // instead of as a 401 at request time.
  if (missingHeaders.length > 0) {
    // eslint-disable-next-line no-console
    console.warn(
      `[rpcRoutes] ${missingHeaders.length} action(s) lack a 'headers:' block — ` +
        `requests will be sent without X-ACTION-TOKEN and may be rejected by ` +
        `strict-auth backends (services-server, cloud-collector-server). ` +
        `Add an explicit headers block to each: ${missingHeaders.join(', ')}`
    );
  }
  cached = out;
  return out;
}

// --- Action argument schema -----------------------------------------------
// RPC applies GraphQL input coercion (notably: single value → single-
// element list when the schema declares a list type). Our bypass skips
// RPC's validation pipeline, so we have to do the same coercion ourselves
// or the upstream Go handler will fail to decode the input.
//
// We need this at every depth — not just top-level args — because nested
// inputs commonly use list operators (`_in`, `_not_in`, `_and`, `_or`) and
// frontend code passes single values relying on RPC's recursive coercion.
// To do that correctly we need the full input-type graph: for each named
// input type, the list-ness and base type of every field. Top-level action
// args are stored the same way, keyed by action name.

const SCHEMA_PATH = path.join(process.cwd(), 'src', 'lib', 'actions.graphql');

// Field-level type description used by the bypass for input coercion.
// `typeName` is the base named type after stripping NonNull / List wrappers,
// e.g. `[String!]!` → { typeName: 'String', isList: true, isNonNull: true }.
export type SchemaFieldInfo = { typeName: string; isList: boolean; isNonNull: boolean };

export type ActionInputSchema = {
  // For each action, the type info of each top-level argument.
  actions: Map<string, Map<string, SchemaFieldInfo>>;
  // For each named input type (e.g. `RecommendationWhereRequest`), the type
  // info of each field. Used to recurse into nested objects when coercing.
  inputTypes: Map<string, Map<string, SchemaFieldInfo>>;
};

function describeType(t: TypeNode): SchemaFieldInfo {
  let isNonNull = false;
  let isList = false;
  let cur: TypeNode = t;
  if (cur.kind === 'NonNullType') {
    isNonNull = true;
    cur = cur.type;
  }
  if (cur.kind === 'ListType') {
    isList = true;
    cur = cur.type;
    // strip element-level NonNull wrapper, e.g. [String!]
    if (cur.kind === 'NonNullType') cur = cur.type;
  }
  const typeName = cur.kind === 'NamedType' ? cur.name.value : '';
  return { typeName, isList, isNonNull };
}

let schemaCache: ActionInputSchema | null = null;

export function loadActionInputSchema(): ActionInputSchema {
  if (schemaCache) return schemaCache;
  const sdlPath = process.env.ACTIONS_GRAPHQL_PATH || SCHEMA_PATH;
  let raw: string;
  try {
    raw = fs.readFileSync(sdlPath, 'utf8');
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err);
    throw new Error(`[rpcRoutes] Failed to read actions.graphql at ${sdlPath}: ${msg}`);
  }
  let doc;
  try {
    doc = parse(raw, { noLocation: true });
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err);
    throw new Error(`[rpcRoutes] Failed to parse actions.graphql at ${sdlPath}: ${msg}`);
  }
  const actions = new Map<string, Map<string, SchemaFieldInfo>>();
  const inputTypes = new Map<string, Map<string, SchemaFieldInfo>>();
  for (const def of doc.definitions) {
    // RPC emits one `type Query { actionName(...) }` block per action, so
    // we walk every Query/Mutation ObjectType definition and collect fields
    // across all of them.
    if (def.kind === 'ObjectTypeDefinition') {
      const obj = def as ObjectTypeDefinitionNode;
      if (obj.name.value !== 'Query' && obj.name.value !== 'Mutation') continue;
      for (const field of obj.fields || []) {
        const args = new Map<string, SchemaFieldInfo>();
        for (const arg of field.arguments || []) {
          args.set(arg.name.value, describeType(arg.type));
        }
        if (args.size > 0) actions.set(field.name.value, args);
      }
    } else if (def.kind === 'InputObjectTypeDefinition') {
      const obj = def as InputObjectTypeDefinitionNode;
      const fields = new Map<string, SchemaFieldInfo>();
      for (const field of obj.fields || []) {
        fields.set(field.name.value, describeType(field.type));
      }
      inputTypes.set(obj.name.value, fields);
    }
  }
  schemaCache = { actions, inputTypes };
  return schemaCache;
}
