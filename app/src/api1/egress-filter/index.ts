/**
 * LLM egress-filter config — backend API client (secret-filter mode + enabled).
 *
 * Typed callers for the llm-server tenant-scoped egress config, routed through
 * the `egressfilter_get` / `egressfilter_update` actions (registered in
 * `src/lib/actions.yaml`, handled by llm-server `/v1/egressfilter/config`).
 * Each action takes a single `request` object and returns the llm-server
 * `{ data, errors }` envelope under `data`; we unwrap to the typed payload.
 *
 * Mirrors `@api1/gateway-config` (GraphQL mutation via `queryGraphQL`).
 *
 * Auth/tenant scoping is server-side (x-tenant-id header injected by the RPC
 * gateway) — the UI NEVER sends tenant_id. The `X-ACTION-TOKEN` guard is
 * injected by the RPC gateway from `LLM_SERVER_TOKEN`.
 */
import { queryGraphQL } from '@lib/HttpService';

/**
 * Extracts `response.data.data[action].data`, throwing on any GraphQL/transport
 * error or an llm-server `errors[]` envelope so the UI can surface the message.
 */
function unwrap<T>(response: any, action: string): T | null {
  const gqlErrors = response?.data?.errors;
  if (Array.isArray(gqlErrors) && gqlErrors.length > 0) {
    throw new Error(gqlErrors[0]?.message || 'Egress-filter request failed');
  }
  if (!response?.data?.data) {
    throw new Error('Egress-filter request failed');
  }
  const envelope = response.data.data[action];
  // llm-server buildApiResponse: { data, errors }. Surface a backend-reported
  // error even when the transport succeeded (HTTP 400 relayed in-band).
  const backendErrors = envelope?.errors;
  if (Array.isArray(backendErrors) && backendErrors.length > 0) {
    throw new Error(backendErrors[0]?.message || 'Egress-filter request failed');
  }
  return (envelope?.data ?? null) as T | null;
}

/** Secret-filter mode. Detect = record only; enforce = block; redact = mask + forward. */
export type EgressFilterMode = 'detect' | 'enforce' | 'redact';
export const EGRESS_FILTER_MODES: EgressFilterMode[] = ['detect', 'enforce', 'redact'];

/** One tenant-defined custom detection pattern. Action on match follows the tenant mode. */
export interface EgressCustomPattern {
  id: string; // server-assigned; empty when creating
  name: string;
  regex: string;
  enabled: boolean;
}

export interface EgressFilterConfig {
  mode: EgressFilterMode;
  enabled: boolean;
  /** True when a per-tenant override row exists; false = env defaults in effect. */
  has_override: boolean;
  custom_patterns: EgressCustomPattern[];
  // Read-only platform context (from llm-server env).
  master_enabled: boolean; // whole egress subsystem on/off at the platform level
  secrets_enabled: boolean; // secret detector on/off at the platform level
  env_default_mode: EgressFilterMode; // mode applied when no tenant override exists
}

/** Get the current tenant's egress-filter config (+ platform context). */
export async function getEgressFilterConfig(signal?: AbortSignal): Promise<EgressFilterConfig | null> {
  const query = `mutation GetEgressFilterConfig($request: json!) {
    egressfilter_get(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'GetEgressFilterConfig', { request: {} }, undefined, signal);
  return unwrap<EgressFilterConfig>(response, 'egressfilter_get');
}

/** Update the current tenant's egress-filter mode and/or enabled flag. */
export async function updateEgressFilterConfig(
  input: { mode?: EgressFilterMode; enabled?: boolean },
  signal?: AbortSignal
): Promise<EgressFilterConfig | null> {
  const query = `mutation UpdateEgressFilterConfig($request: json!) {
    egressfilter_update(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'UpdateEgressFilterConfig', { request: input }, undefined, signal);
  return unwrap<EgressFilterConfig>(response, 'egressfilter_update');
}

/** Create (empty id) or update (with id) one custom detection pattern. */
export async function upsertEgressPattern(
  pattern: { id?: string; name: string; regex: string; enabled: boolean },
  signal?: AbortSignal
): Promise<EgressFilterConfig | null> {
  const query = `mutation UpsertEgressPattern($request: json!) {
    egressfilter_upsert_pattern(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'UpsertEgressPattern', { request: pattern }, undefined, signal);
  return unwrap<EgressFilterConfig>(response, 'egressfilter_upsert_pattern');
}

/** Delete one custom detection pattern by id. */
export async function deleteEgressPattern(id: string, signal?: AbortSignal): Promise<EgressFilterConfig | null> {
  const query = `mutation DeleteEgressPattern($request: json!) {
    egressfilter_delete_pattern(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'DeleteEgressPattern', { request: { id } }, undefined, signal);
  return unwrap<EgressFilterConfig>(response, 'egressfilter_delete_pattern');
}

/** Clear the tenant's override entirely so platform env defaults apply again. */
export async function clearEgressOverride(signal?: AbortSignal): Promise<EgressFilterConfig | null> {
  const query = `mutation ClearEgressOverride($request: json!) {
    egressfilter_clear_override(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'ClearEgressOverride', { request: {} }, undefined, signal);
  return unwrap<EgressFilterConfig>(response, 'egressfilter_clear_override');
}
