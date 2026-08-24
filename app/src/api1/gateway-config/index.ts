/**
 * AI Gateway config — backend API client (routing rules + rate-limit quotas).
 *
 * Typed callers for the gateway's `/rpc/config/*` endpoints, routed through the
 * `llm_gateway_*` actions (registered in `src/lib/actions.yaml`, handled by the
 * NB AI Gateway). Each action takes a single `request` object and returns an
 * untyped JSON envelope under `data`; we unwrap to the typed payload here.
 *
 * Mirrors `@api1/gateway-usage` (GraphQL mutation via `queryGraphQL`).
 *
 * Auth/tenant scoping is handled by the gateway (session) — the UI NEVER sends
 * tenant_id; the RPC gateway injects it as the x-tenant-id header. The
 * `X-ACTION-TOKEN` service-to-service guard is injected by the RPC gateway from
 * `LLM_GATEWAY_ACTION_TOKEN`.
 *
 * On a validation failure the backend returns HTTP 400 with `{ error: <msg> }`;
 * the RPC gateway relays that as a GraphQL error, which `queryGraphQL` returns
 * under `response.data.errors` (it does not throw). `unwrap` detects that,
 * extracts the backend message, and throws so the UI can surface it inline.
 */
import { queryGraphQL } from '@lib/HttpService';

/**
 * Extracts the payload at `response.data.data[action].data`, throwing on any
 * GraphQL/transport error so callers get the backend's `{ error: <msg> }` text.
 */
function unwrap<T>(response: any, action: string): T | null {
  const errors = response?.data?.errors;
  if (Array.isArray(errors) && errors.length > 0) {
    throw new Error(errors[0]?.message || 'Gateway request failed');
  }
  // A transport-level failure returns the raw axios response (no `data.data`).
  if (!response?.data?.data) {
    throw new Error('Gateway request failed');
  }
  return (response.data.data[action]?.data ?? null) as T | null;
}

// ─── Shapes (match the Go JSON tags exactly) ───────────────────────────────────

export interface GatewayEndpoint {
  provider?: string;
  model?: string;
  key_ref?: string;
  weight?: number;
}

export interface GatewayMatch {
  provider?: string;
  model?: string;
  user_id?: string;
}

export interface GatewayTarget extends GatewayEndpoint {
  fallbacks?: GatewayEndpoint[];
  affinity?: string;
  /** When true, matching requests are BLOCKED (403) instead of routed. `model` (if
   * set) is offered as a suggested alternative in the error. */
  deny?: boolean;
  /** When true, this is a DEPRECATION shield: the retired matched model is rewritten
   * to `model` (the replacement) and forwarded, with an x-nb-llm-deprecated header. */
  deprecated?: boolean;
}

export interface GatewayRoutingRule {
  id: string;
  /** Injected server-side; the UI never sends it. */
  tenant_id?: string;
  priority: number;
  enabled: boolean;
  match: GatewayMatch;
  target: GatewayTarget;
}

export type GatewayMetric = 'requests' | 'tokens' | 'cost';
export type GatewayPeriod = 'minute' | 'hour' | 'day' | 'month';

export interface GatewayRateLimit {
  id: string;
  /** Empty string = tenant-wide scope; otherwise a specific user_id. */
  user_id: string;
  metric: GatewayMetric;
  period: GatewayPeriod;
  limit_value: number;
  enabled: boolean;
}

/** Provider enum for match/target forms. Empty = "any" (match) / addressed (target). */
export type GatewayProvider = 'anthropic' | 'openai' | 'gemini';
export const GATEWAY_PROVIDERS: GatewayProvider[] = ['anthropic', 'openai', 'gemini'];

/** Target endpoint routing affinity. */
export type GatewayAffinity = 'single' | 'prefix_hash';
export const GATEWAY_AFFINITIES: GatewayAffinity[] = ['single', 'prefix_hash'];

export const GATEWAY_METRICS: GatewayMetric[] = ['requests', 'tokens', 'cost'];
export const GATEWAY_PERIODS: GatewayPeriod[] = ['minute', 'hour', 'day', 'month'];

// ─── Routing rules ─────────────────────────────────────────────────────────────

/** List all routing rules for the current tenant, priority-ordered by the backend. */
export async function listRoutingRules(signal?: AbortSignal): Promise<GatewayRoutingRule[]> {
  const query = `mutation ListRoutingRules($request: json!) {
    llm_gateway_list_routing_rules(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'ListRoutingRules', { request: {} }, undefined, signal);
  return unwrap<GatewayRoutingRule[]>(response, 'llm_gateway_list_routing_rules') ?? [];
}

/** Create a routing rule. `id`/`tenant_id` are set server-side — omit them. */
export async function createRoutingRule(
  rule: Omit<GatewayRoutingRule, 'id' | 'tenant_id'>,
  signal?: AbortSignal
): Promise<GatewayRoutingRule | null> {
  const query = `mutation CreateRoutingRule($request: json!) {
    llm_gateway_create_routing_rule(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'CreateRoutingRule', { request: rule }, undefined, signal);
  return unwrap<GatewayRoutingRule>(response, 'llm_gateway_create_routing_rule');
}

/** Update a routing rule. `id` is required; `tenant_id` is ignored server-side. */
export async function updateRoutingRule(rule: GatewayRoutingRule, signal?: AbortSignal): Promise<GatewayRoutingRule | null> {
  const query = `mutation UpdateRoutingRule($request: json!) {
    llm_gateway_update_routing_rule(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'UpdateRoutingRule', { request: rule }, undefined, signal);
  return unwrap<GatewayRoutingRule>(response, 'llm_gateway_update_routing_rule');
}

/** Delete a routing rule by id. */
export async function deleteRoutingRule(id: string, signal?: AbortSignal): Promise<{ id: string } | null> {
  const query = `mutation DeleteRoutingRule($request: json!) {
    llm_gateway_delete_routing_rule(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'DeleteRoutingRule', { request: { id } }, undefined, signal);
  return unwrap<{ id: string }>(response, 'llm_gateway_delete_routing_rule');
}

// ─── Tier mappings ───────────────────────────────────────────────────────────

/** One tier alias's effective mapping — the shipped default plus the tenant
 * override when set. `effective_*` is what actually resolves. */
export interface GatewayTier {
  alias: string;
  description: string;
  default_provider: string;
  default_model: string;
  overridden: boolean;
  effective_provider: string;
  effective_model: string;
  override_rule_id?: string;
}

/** List the tier aliases and what each resolves to (default or tenant override). */
export async function listTiers(signal?: AbortSignal): Promise<GatewayTier[]> {
  const query = `mutation ListTiers($request: json!) {
    llm_gateway_list_tiers(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'ListTiers', { request: {} }, undefined, signal);
  return unwrap<GatewayTier[]>(response, 'llm_gateway_list_tiers') ?? [];
}

/** Remap a tier to a concrete provider/model (upserts the tenant override rule). */
export async function setTier(alias: string, provider: string, model: string, signal?: AbortSignal): Promise<unknown> {
  const query = `mutation SetTier($request: json!) {
    llm_gateway_set_tier(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'SetTier', { request: { alias, provider, model } }, undefined, signal);
  return unwrap<unknown>(response, 'llm_gateway_set_tier');
}

/** Reset a tier to the shipped platform default (removes the tenant override). */
export async function resetTier(alias: string, signal?: AbortSignal): Promise<unknown> {
  const query = `mutation ResetTier($request: json!) {
    llm_gateway_reset_tier(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'ResetTier', { request: { alias } }, undefined, signal);
  return unwrap<unknown>(response, 'llm_gateway_reset_tier');
}

// ─── Rate limits (quotas) ────────────────────────────────────────────────────

/** List all rate-limit quotas for the current tenant. */
export async function listRateLimits(signal?: AbortSignal): Promise<GatewayRateLimit[]> {
  const query = `mutation ListRateLimits($request: json!) {
    llm_gateway_list_rate_limits(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'ListRateLimits', { request: {} }, undefined, signal);
  return unwrap<GatewayRateLimit[]>(response, 'llm_gateway_list_rate_limits') ?? [];
}

/** Insert (no `id`) or update (with `id`) a rate-limit quota. */
export async function upsertRateLimit(limit: Partial<GatewayRateLimit>, signal?: AbortSignal): Promise<GatewayRateLimit | null> {
  const query = `mutation UpsertRateLimit($request: json!) {
    llm_gateway_upsert_rate_limit(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'UpsertRateLimit', { request: limit }, undefined, signal);
  return unwrap<GatewayRateLimit>(response, 'llm_gateway_upsert_rate_limit');
}

/** Delete a rate-limit quota by id. */
export async function deleteRateLimit(id: string, signal?: AbortSignal): Promise<{ id: string } | null> {
  const query = `mutation DeleteRateLimit($request: json!) {
    llm_gateway_delete_rate_limit(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'DeleteRateLimit', { request: { id } }, undefined, signal);
  return unwrap<{ id: string }>(response, 'llm_gateway_delete_rate_limit');
}

// ─── Data & privacy settings ───────────────────────────────────────────────────

/**
 * Per-tenant data-governance settings, plus read-only platform context the UI needs.
 * `egress_filter_mode: null` means "inherit the env default" (env_dlp_default);
 * `capture_body_allowed` is the platform ceiling — false greys the capture toggle.
 */
export interface GatewaySettings {
  capture_body: boolean;
  egress_filter_mode: string | null; // null = inherit env default | '' /'off'/'detect'/'enforce'/'redact'
  updated_by: string;
  updated_at: string | null;
  // Platform context (read-only, from env).
  capture_body_allowed: boolean; // env ceiling + a sink: is capture possible at all
  body_retention_hours: number;
  env_dlp_default: string;
}

/** Get the current tenant's data-privacy settings (+ platform context). */
export async function getGatewaySettings(signal?: AbortSignal): Promise<GatewaySettings | null> {
  const query = `mutation GetGatewaySettings($request: json!) {
    llm_gateway_get_settings(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'GetGatewaySettings', { request: {} }, undefined, signal);
  return unwrap<GatewaySettings>(response, 'llm_gateway_get_settings');
}

/** Update the current tenant's data-privacy settings. */
export async function updateGatewaySettings(
  input: { capture_body: boolean; egress_filter_mode: string | null },
  signal?: AbortSignal
): Promise<GatewaySettings | null> {
  const query = `mutation UpdateGatewaySettings($request: json!) {
    llm_gateway_update_settings(request: $request) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'UpdateGatewaySettings', { request: input }, undefined, signal);
  return unwrap<GatewaySettings>(response, 'llm_gateway_update_settings');
}

// ─── Tenant user directory (for the user picker + id→name resolution) ──────────

export interface GatewayUser {
  id: string;
  username: string;
  display_name: string;
}

/**
 * The current tenant's users, for the routing/quota user picker and for resolving
 * a stored user_id to a display name. Reuses the shared `users_list_by_tenant`
 * action (a Hasura-style query, tenant-admin scoped — same gate as this tab), so
 * it returns `{ rows }` directly rather than the gateway `{ data }` envelope.
 */
export async function listTenantUsers(signal?: AbortSignal): Promise<GatewayUser[]> {
  const query = `query GatewayTenantUsers {
    users_list_by_tenant(order_by: [{ column: "username", order: asc }]) {
      rows {
        id
        username
        display_name
      }
    }
  }`;
  const response = await queryGraphQL(query, 'GatewayTenantUsers', {}, undefined, signal);
  return response?.data?.data?.users_list_by_tenant?.rows ?? [];
}
