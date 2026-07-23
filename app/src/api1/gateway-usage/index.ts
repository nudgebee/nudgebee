/**
 * AI Gateway usage — backend API client.
 *
 * Typed caller for the gateway's `POST /rpc/usage/aggregate` endpoint, routed
 * through the `llm_gateway_aggregate_usage` action (registered in
 * `src/lib/actions.yaml`, handled by the NB AI Gateway). The action takes a
 * single `request` object and returns an untyped JSON envelope under `data`;
 * we unwrap to the documented payload here.
 *
 * Mirrors `@api1/ai-cost` (GraphQL mutation via `queryGraphQL`).
 *
 * Auth/tenant scoping is handled by the gateway (session) — the UI sends no
 * tokens; the `X-ACTION-TOKEN` service-to-service guard is injected by the RPC
 * gateway from `LLM_GATEWAY_ACTION_TOKEN`.
 */
import { queryGraphQL } from '@lib/HttpService';

// ─── Response shapes (mirror the gateway contract exactly) ─────────────────────

export interface GatewayTotals {
  total_cost_usd: number;
  total_requests: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cached_input_tokens: number;
  cache_hit_rate_pct: number;
  /** Request-weighted average end-to-end latency, in seconds. */
  avg_latency_seconds: number;
}

/** One breakdown row (per provider / per model / per user). */
export interface GatewayGroupRow {
  key: string;
  /** Stable id for drill-down — the user_id on the user breakdown; empty otherwise. */
  id?: string;
  cost_usd: number;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  /** Share of input tokens served from cache (0–100). */
  cache_hit_rate_pct: number;
  /** Request-weighted average latency for this bucket, in seconds. */
  avg_latency_seconds: number;
}

/** One (bucket, key) cell of the over-time series. */
export interface GatewayTimeSeriesRow {
  /** Bucket start, RFC3339 UTC (truncated to the granularity). */
  bucket: string;
  key: string;
  cost_usd: number;
  requests: number;
  tokens: number;
}

export interface GatewayTimeSeries {
  granularity: string; // day | hour
  by_dimension: {
    overall: GatewayTimeSeriesRow[];
  };
}

export interface GatewayBreakdowns {
  provider: GatewayGroupRow[];
  model: GatewayGroupRow[];
  /** Per-user usage; `key` is the resolved display name (falls back to user id). */
  user: GatewayGroupRow[];
}

/** One row of the tool breakdown — a tool the caller OFFERED (from request tool
 * definitions). `requests` = how many requests made it available. Per-tool
 * cost/tokens are omitted (a request can offer several tools → double-count). */
export interface GatewayToolRow {
  tool: string;
  requests: number;
  avg_latency_seconds: number;
  /** Distinct models that offered this tool. */
  models: string[];
}

/** One (label, count) pair in a governance breakdown (a reject/routing reason). */
export interface GatewayLabelCount {
  key: string;
  requests: number;
}

/** Request outcomes by HTTP status class. Exhaustive → the four sum to total_requests. */
export interface GatewayOutcomes {
  success: number; // 2xx
  client_error: number; // 4xx (incl. rate-limit / blocked rejections)
  server_error: number; // 5xx
  other: number; // 1xx/3xx or unrecorded (e.g. aborted stream)
  error_rate_pct: number; // 100 * (total - success) / total
}

/** One upstream provider's request count + error share (reliability by provider). */
export interface GatewayProviderOutcome {
  provider: string;
  requests: number;
  errors: number;
  error_rate_pct: number;
}

/** One time bucket's outcome mix, for the reliability trend chart. */
export interface GatewayOutcomeBucket {
  bucket: string; // RFC3339 UTC
  success: number;
  client_error: number;
  server_error: number;
  other: number;
}

/** Tail-latency profile in seconds (avg alone hides the slow tail). */
export interface GatewayLatencyPct {
  p50_seconds: number;
  p95_seconds: number;
  p99_seconds: number;
}

/** One from→to substitution pair and how often it fired. */
export interface GatewayRoute {
  from_provider: string;
  from_model: string;
  to_provider: string;
  to_model: string;
  requests: number;
}

/** Reliability + intelligence rollup for the Governance tab. `governance` is
 * omitted on empty windows, so treat it as nullable. */
export interface GatewayGovernance {
  outcomes: GatewayOutcomes;
  /** Edge rejections by reason — supplementary; fills in on fresh traffic. */
  rejects: GatewayLabelCount[];
  /** Routing decisions by reason: passthrough / substitute / fallback / deprecated / … */
  routing_reasons: GatewayLabelCount[];
  /** Requests that tripped the egress secret filter. */
  dlp_hits: number;
  // The fields below were added in a later gateway build — optional so the UI
  // stays resilient to version skew (an older gateway omits them).
  /** Outcomes split by upstream provider — which one is failing. */
  outcomes_by_provider?: GatewayProviderOutcome[];
  /** Per-bucket outcome mix for the reliability trend chart. */
  outcome_series?: GatewayOutcomeBucket[];
  /** p50/p95/p99 end-to-end latency, seconds. */
  latency?: GatewayLatencyPct;
  /** from→to pairs for routing_reason=substitute — where substitution reroutes. */
  substitution_routes?: GatewayRoute[];
}

export interface GatewayUsageMetrics {
  totals: GatewayTotals;
  breakdowns: GatewayBreakdowns;
  tools: GatewayToolRow[];
  governance?: GatewayGovernance | null;
  time_series: GatewayTimeSeries;
}

/** One row of the recent-request list (Requests tab). */
export interface GatewayRequestRow {
  /** Fetch key for the body viewer — passed to `getRequestBody`. */
  id: string;
  /**
   * Server-authoritative: true only when body-logging is on AND this row is the
   * caller's own request. The UI shows the "view body" action only when true.
   */
  can_view_body: boolean;
  created_at: string; // RFC3339 UTC
  user: string; // resolved display name (falls back to user id)
  provider: string;
  model: string;
  requested_model: string;
  /** The model id the provider echoed back (e.g. a dated snapshot). '' when unknown. */
  responded_model: string;
  routing_reason: string;
  /** 'native' (passthrough mount) | 'generic' (/v1) | ''. */
  surface: string;
  /** Set only when this request tripped the egress secret filter. */
  dlp?: { mode: string; rules: string[] };
  status_code: number;
  streaming: boolean;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  latency_ms: number;
  cost_usd: number;
  session_id: string;
  /** How session_id was resolved, most-authoritative first:
   * 'header' | 'metadata.session_id' | 'metadata.user_id' | 'inferred' | ''. */
  session_source: string;
}

/** A page of recent requests plus the unpaged total (for the pager). */
export interface GatewayRequestList {
  rows: GatewayRequestRow[];
  total: number;
  limit: number;
  offset: number;
}

/** Time-bucket granularity accepted by the gateway. */
export type GatewayGranularity = 'day' | 'hour';

// ─── Request shape ─────────────────────────────────────────────────────────────

export interface AggregateGatewayUsageRequest {
  // Tenant is NOT sent from the UI — the RPC gateway injects it as the x-tenant-id
  // header from the session, and the backend scopes on that.
  accountIds?: string[];
  startDate: string; // RFC3339 UTC
  endDate: string; // RFC3339 UTC
  granularity: GatewayGranularity;
}

export interface ListGatewayRequestsRequest {
  startDate: string; // RFC3339 UTC
  endDate: string; // RFC3339 UTC
  userId?: string; // optional; Users-tab drill-down or the User filter
  providers?: string[]; // optional; empty = all providers
  models?: string[]; // optional; empty = all (routed) models
  status?: string; // optional; 'success' (2xx) | 'error' (everything else)
  tool?: string; // optional drill-down from the Tools tab
  // Governance-tab drill-ins — each maps a Governance count to the rows behind it.
  sessionId?: string; // one session/conversation (drill-in from a request's session)
  routingReason?: string; // e.g. substitute | fallback | deprecated | passthrough
  rejectReason?: string; // e.g. rate_limited | secret_blocked
  dlp?: boolean; // requests that tripped the egress filter
  limit?: number;
  offset?: number;
}

/**
 * Captured request/response body for one gateway request (EE / body-logging).
 * `available:false` — expired, soft-deleted, or not the caller's own request
 * (indistinguishable by design: never leaks whether the row exists).
 */
export interface GatewayRequestBody {
  available: boolean;
  request_body?: string;
  response_body?: string;
}

// ─── Caller ─────────────────────────────────────────────────────────────────────

const arr = (v?: string[]): string[] => v ?? [];

/** Aggregate gateway usage: totals + provider/model breakdowns + over-time series. */
export async function aggregateGatewayUsage(req: AggregateGatewayUsageRequest, signal?: AbortSignal): Promise<GatewayUsageMetrics | null> {
  const query = `mutation AggregateGatewayUsage(
    $accountIds: [String!], $startDate: String!, $endDate: String!, $granularity: String!
  ) {
    llm_gateway_aggregate_usage(request: {
      account_ids: $accountIds, start_date: $startDate, end_date: $endDate, granularity: $granularity
    }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'AggregateGatewayUsage',
    {
      accountIds: arr(req.accountIds),
      startDate: req.startDate,
      endDate: req.endDate,
      granularity: req.granularity,
    },
    undefined,
    signal
  );
  return response?.data?.data?.llm_gateway_aggregate_usage?.data ?? null;
}

/** Exported for the rpcGateway contract test — asserts this mutation parses into
 * an upstream body that carries every filter field the Go handler binds. */
export const LIST_GATEWAY_REQUESTS = `mutation ListGatewayRequests(
    $startDate: String!, $endDate: String!, $userId: String, $providers: [String!], $models: [String!],
    $status: String, $tool: String, $sessionId: String, $routingReason: String, $rejectReason: String, $dlp: Boolean, $limit: Int, $offset: Int
  ) {
    llm_gateway_list_requests(request: {
      start_date: $startDate, end_date: $endDate, user_id: $userId, providers: $providers, models: $models,
      status: $status, tool: $tool, session_id: $sessionId, routing_reason: $routingReason, reject_reason: $rejectReason, dlp: $dlp, limit: $limit, offset: $offset
    }) {
      data
    }
  }`;

/** List recent requests (paginated, newest first) for the Requests tab. */
export async function listGatewayRequests(req: ListGatewayRequestsRequest, signal?: AbortSignal): Promise<GatewayRequestList | null> {
  const response = await queryGraphQL(
    LIST_GATEWAY_REQUESTS,
    'ListGatewayRequests',
    {
      startDate: req.startDate,
      endDate: req.endDate,
      userId: req.userId ?? '',
      providers: arr(req.providers),
      models: arr(req.models),
      status: req.status ?? '',
      tool: req.tool ?? '',
      routingReason: req.routingReason ?? '',
      sessionId: req.sessionId ?? '',
      rejectReason: req.rejectReason ?? '',
      dlp: req.dlp ?? false,
      limit: req.limit ?? 50,
      offset: req.offset ?? 0,
    },
    undefined,
    signal
  );
  return response?.data?.data?.llm_gateway_list_requests?.data ?? null;
}

/**
 * Fetch the captured request + response body for a single request (EE body view).
 * Returns `{ available: false }` when the body wasn't captured, has expired, or
 * isn't the caller's own request (the backend never distinguishes these cases).
 */
export async function getRequestBody(id: string, signal?: AbortSignal): Promise<GatewayRequestBody | null> {
  const query = `mutation GetGatewayRequestBody($id: String!) {
    llm_gateway_get_request_body(request: { id: $id }) {
      data
    }
  }`;
  const response = await queryGraphQL(query, 'GetGatewayRequestBody', { id }, undefined, signal);
  if (response?.data?.errors?.length) {
    // Surface a real fetch failure to the caller (→ error banner) rather than
    // letting it fall through to the neutral "not captured" empty state.
    throw new Error(response.data.errors[0]?.message || 'Failed to load the request body.');
  }
  return response?.data?.data?.llm_gateway_get_request_body?.data ?? null;
}
