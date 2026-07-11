/**
 * AI Cost Analyser — backend API client.
 *
 * Typed callers for the five Cost-Analyser gateway actions (shipped in PR #31470,
 * registered in `src/lib/actions.yaml`, handled by llm-server `/v1/completions/*`).
 * Each action takes a single `request` object and returns an untyped JSON envelope
 * under `data`; we unwrap to the documented payload here.
 *
 * Contract: api-server/nudgebee-ai-cost-analyser-ui-api-contract.md
 * Schemas:  api-server/nudgebee-ai-cost-analyser-openapi.yaml
 *
 * Auth/tenant scoping is handled by the gateway (session) — the UI sends no tokens.
 */
import { queryGraphQL, gqlStringify } from '@lib/HttpService';

// ─── Shared response shapes (mirror the OpenAPI components exactly) ────────────

export interface UsageTotals {
  total_cost_usd: number;
  cache_savings_usd: number;
  total_tasks: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cached_input_tokens: number;
  cache_hit_rate_pct: number;
  total_requests: number;
}

export interface UsageGroupRow {
  key: string;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  cache_hit_rate_pct: number;
  requests: number;
  conversations: number;
  avg_latency_seconds: number;
}

export interface UsageFilterOption {
  id: string;
  name: string;
}

export interface UsageFilters {
  sources: string[];
  models: string[];
  providers: string[];
  agents: string[];
  statuses: string[];
  users: UsageFilterOption[];
  accounts: UsageFilterOption[];
}

/** Breakdown dimension accepted by `group_by`. */
export type UsageDimension = 'model' | 'provider' | 'source' | 'agent' | 'status' | 'user' | 'account';

/** Dimensions a time-series may be stacked by (all returned together). */
export type UsageStackDimension = 'model' | 'source' | 'agent';

/** One (period, stack key) cell of the over-time series. */
export interface UsageTimeSeriesRow {
  /** Bucket start, RFC3339 UTC (truncated to the granularity). */
  bucket: string;
  /** Stack value (model name / source / agent) this cell belongs to. */
  key: string;
  cost_usd: number;
  requests: number;
}

/** Over-time payload: bucketed cost+calls stacked by each dimension at once. */
export interface UsageTimeSeries {
  granularity: string; // day|week|month
  by_dimension: Partial<Record<UsageStackDimension, UsageTimeSeriesRow[]>>;
}

/** One cache scope's prorated storage cost over the window. */
export interface CacheStorageScope {
  scope: string; // account | global | conversation
  cost_usd: number;
  cached_tokens: number;
  entries: number;
}

/**
 * Cache-lifecycle storage cost — prorated to the report window, separate from
 * token cost. Account + date (+ model/provider) scoped only: the cache table
 * carries no source/user/agent/status, so this does NOT respond to those filters.
 */
export interface CacheStorage {
  total_usd: number;
  by_scope: CacheStorageScope[];
}

export interface UsageMetrics {
  group_by: string[];
  totals: UsageTotals;
  breakdowns: Partial<Record<UsageDimension, UsageGroupRow[]>>;
  /** Present only when a granularity was requested; powers the over-time charts. */
  time_series?: UsageTimeSeries | null;
  /** Cache storage cost (prorated, by scope) — added to token cost for the all-in total. */
  storage?: CacheStorage | null;
}

/** One model's rolled-up calls + cost within a single conversation (list row). */
export interface ConversationModelStat {
  model: string;
  provider: string;
  calls: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
}

export interface ConversationCostRow {
  conversation_id: string;
  session_id: string;
  source: string;
  status: string;
  title: string;
  user_id: string;
  account_id: string;
  started_at: string;
  ended_at: string;
  wall_clock_seconds: number;
  model_latency_seconds: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  message_count: number;
  agent_count: number;
  llm_call_count: number;
  models_used: string[];
  /** Per-model calls + cost, cost-desc; cost reconciles with `cost_usd`. */
  model_breakdown: ConversationModelStat[];
}

export interface ConversationListPage {
  limit: number;
  offset: number;
  total: number;
}

export interface ConversationCostList {
  totals: UsageTotals;
  page: ConversationListPage;
  rows: ConversationCostRow[];
}

export interface ConversationTreeSummary {
  conversation_id: string;
  session_id: string;
  source: string;
  status: string;
  title: string;
  started_at: string;
  ended_at: string;
  wall_clock_seconds: number;
  model_latency_seconds: number;
  total_cost_usd: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cached_input_tokens: number;
  cache_hit_rate_pct: number;
  message_count: number;
  agent_count: number;
  tool_call_count: number;
  model_call_count: number;
}

export interface TreeMessage {
  id: string;
  parent_agent_id: string;
  role: string;
  message_type: string;
  agent_name: string;
  status: string;
  created_at: string;
  updated_at: string;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
}

export interface TreeAgent {
  id: string;
  message_id: string;
  parent_agent_id: string;
  agent_name: string;
  status: string;
  created_at: string;
  updated_at: string;
  duration_seconds: number;
  /** This agent's DIRECT model-call cost. */
  cost_usd: number;
  /** Cost of this agent + all descendant agents (the new tree rollup). */
  subtree_cost_usd?: number;
  input_tokens: number;
  output_tokens: number;
  model_latency_seconds: number;
  /** Per-agent counts (the tree no longer ships the model_calls array). */
  model_call_count?: number;
  tool_call_count?: number;
}

export interface TreeToolCall {
  id: string;
  agent_id: string;
  message_id: string;
  tool_name: string;
  tool_id: string;
  tool_type: string;
  child_agent_id: string;
  status: string;
  created_at: string;
  updated_at: string;
  duration_seconds: number;
}

export interface TreeModelCall {
  id: string;
  agent_id: string;
  message_id: string;
  model: string;
  provider: string;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  cache_creation_tokens: number;
  thinking_tokens: number;
  cost_usd: number;
  latency_seconds: number;
  ttft_ms: number;
  retry_attempt: number;
  is_cache_hit: boolean;
  request_status: string;
  stop_reason: string;
  error_message: string;
  created_at: string;
}

export interface ConversationTree {
  conversation: ConversationTreeSummary;
  messages: TreeMessage[];
  agents: TreeAgent[];
  tool_calls: TreeToolCall[];
  /** Legacy/demo only — the live tree is structure-only; per-agent model calls
   * come from `ai_get_conversation_agent`. Optional so the demo fixtures still adapt. */
  model_calls?: TreeModelCall[];
}

// ─── ai_get_conversation_agent (on-click per-agent detail) ─────────────────────

export interface CostBreakdownComponent {
  kind: string; // input | cached_input | cache_creation | output | thinking | …
  tokens: number;
  cost_usd: number;
}
export interface CostBreakdown {
  tier: string;
  components: CostBreakdownComponent[];
}

export interface AgentModelCall {
  id: string;
  model: string;
  provider: string;
  status: string;
  is_error: boolean;
  error_message: string;
  stop_reason: string;
  is_cache_hit: boolean;
  retry_attempt: number;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  cache_creation_tokens: number;
  thinking_tokens: number;
  cost_usd: number;
  cost_breakdown?: CostBreakdown;
  latency_seconds: number;
  ttft_ms: number;
  created_at: string;
  /** True when a stored prompt/response trace exists (LLM_TRACE_ENABLED). The UI
   * shows a "view prompt" action; the text itself is fetched lazily by id. */
  has_trace?: boolean;
  /** Raw trace — populated ONLY by the by-id lazy fetch (model_call_id), omitted
   * from the normal per-agent listing. */
  prompt_messages?: string;
  response_content?: string;
}

export interface AgentToolCall {
  id: string;
  tool_name: string;
  tool_id: string;
  tool_type: string;
  status: string;
  is_error: boolean;
  parameters: string;
  response: string;
  thought: string;
  child_agent_id: string;
  created_at: string;
  updated_at: string;
  duration_seconds: number;
}

export interface AgentDetailAgent extends TreeAgent {
  is_error?: boolean;
  query?: string;
  thought?: string;
  response?: string;
}

export interface ConversationAgentDetail {
  agent: AgentDetailAgent;
  tool_calls: AgentToolCall[];
  model_calls: AgentModelCall[];
}

// ─── ai_get_conversation_usage_metrics (legacy "basic summary" action) ─────────
// NOTE: this action's cost figures use a different (legacy) methodology than the
// list/tree on-read formula and can be internally inconsistent — use it for
// requests / cache-hit / success-rate / latency split, NOT for per-model cost.

export interface ConversationModelUsage {
  model_provider: string;
  model_name: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  cache_creation_tokens: number;
  thinking_tokens: number;
  cache_hit_rate_percentage: number;
  cost_usd: number;
  success_rate_percentage: number;
  successful_requests: number;
  failed_requests: number;
}

export interface ConversationCacheSavings {
  total_cached_tokens: number;
  cache_hit_rate_percentage: number;
  estimated_cost_without_cache_usd: number;
  actual_cost_usd: number;
  cost_savings_usd: number;
  tokens_saved: number;
}

export interface ConversationUsageSummary {
  conversation_id: string;
  total_cost_usd: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cached_input_tokens: number;
  total_cache_hit_rate_percentage: number;
  model_usage: ConversationModelUsage[];
  cache_savings: ConversationCacheSavings;
  success_rate_percentage: number;
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
  total_tool_calls: number;
  successful_tool_calls: number;
  total_latency_seconds: number;
  average_latency_seconds: number;
  wall_time_seconds: number;
  agent_active_time_seconds: number;
  tool_time_seconds: number;
  api_time_seconds: number;
  api_time_percentage: number;
  tool_time_percentage: number;
}

// ─── Request shapes ────────────────────────────────────────────────────────────

/** Fields shared by filters / aggregate / list requests (empty array = no constraint). */
export interface UsageFilterRequest {
  accountIds?: string[];
  startDate: string; // RFC3339 UTC
  endDate: string; // RFC3339 UTC
  userId?: string;
  sources?: string[];
  models?: string[];
  providers?: string[];
  agents?: string[];
  statuses?: string[];
}

export interface AggregateUsageRequest extends UsageFilterRequest {
  groupBy?: UsageDimension[];
  topN?: number;
  /** day|week|month — when set, the response includes the over-time `time_series`. */
  granularity?: string;
}

export interface ListConversationCostsRequest extends UsageFilterRequest {
  sortBy?: 'cost' | 'start_time' | 'duration' | 'llm_calls' | 'tokens';
  sortDir?: 'asc' | 'desc';
  limit?: number;
  offset?: number;
}

export interface ConversationDetailRequest {
  conversationId: string; // the session_id
  accountId: string;
  userId?: string;
}

// ─── Callers ────────────────────────────────────────────────────────────────────

const arr = (v?: string[]): string[] => v ?? [];

/** Filter-bar option-sets present in the window + accounts the caller may read. */
export async function getUsageFilters(
  req: { accountIds?: string[]; startDate: string; endDate: string },
  signal?: AbortSignal
): Promise<UsageFilters | null> {
  const query = `mutation GetUsageFilters($accountIds: [String!], $startDate: String!, $endDate: String!) {
    ai_get_usage_filters(request: { account_ids: $accountIds, start_date: $startDate, end_date: $endDate }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'GetUsageFilters',
    {
      accountIds: arr(req.accountIds),
      startDate: req.startDate,
      endDate: req.endDate,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_get_usage_filters?.data ?? null;
}

/** KPI totals + one cost breakdown per requested `group_by` dimension. */
export async function aggregateUsageMetrics(req: AggregateUsageRequest, signal?: AbortSignal): Promise<UsageMetrics | null> {
  const query = `mutation AggregateUsageMetrics(
    $accountIds: [String!], $startDate: String!, $endDate: String!, $userId: String,
    $sources: [String!], $models: [String!], $providers: [String!], $agents: [String!], $statuses: [String!],
    $groupBy: [String!], $topN: Int, $granularity: String
  ) {
    ai_aggregate_usage_metrics(request: {
      account_ids: $accountIds, start_date: $startDate, end_date: $endDate, user_id: $userId,
      sources: $sources, models: $models, providers: $providers, agents: $agents, statuses: $statuses,
      group_by: $groupBy, top_n: $topN, granularity: $granularity
    }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'AggregateUsageMetrics',
    {
      accountIds: arr(req.accountIds),
      startDate: req.startDate,
      endDate: req.endDate,
      userId: req.userId ?? null,
      sources: arr(req.sources),
      models: arr(req.models),
      providers: arr(req.providers),
      agents: arr(req.agents),
      statuses: arr(req.statuses),
      groupBy: req.groupBy ?? [],
      topN: req.topN ?? 0,
      granularity: req.granularity ?? null,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_aggregate_usage_metrics?.data ?? null;
}

/** Conversations explorer: filtered/sorted/paginated rows + filter-wide header totals. */
export async function listConversationCosts(req: ListConversationCostsRequest, signal?: AbortSignal): Promise<ConversationCostList | null> {
  const query = `mutation ListConversationCosts(
    $accountIds: [String!], $startDate: String!, $endDate: String!, $userId: String,
    $sources: [String!], $models: [String!], $providers: [String!], $agents: [String!], $statuses: [String!],
    $sortBy: String, $sortDir: String, $limit: Int, $offset: Int
  ) {
    ai_list_conversation_costs(request: {
      account_ids: $accountIds, start_date: $startDate, end_date: $endDate, user_id: $userId,
      sources: $sources, models: $models, providers: $providers, agents: $agents, statuses: $statuses,
      sort_by: $sortBy, sort_dir: $sortDir, limit: $limit, offset: $offset
    }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'ListConversationCosts',
    {
      accountIds: arr(req.accountIds),
      startDate: req.startDate,
      endDate: req.endDate,
      userId: req.userId ?? null,
      sources: arr(req.sources),
      models: arr(req.models),
      providers: arr(req.providers),
      agents: arr(req.agents),
      statuses: arr(req.statuses),
      sortBy: req.sortBy ?? 'start_time',
      sortDir: req.sortDir ?? 'desc',
      limit: req.limit ?? 50,
      offset: req.offset ?? 0,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_list_conversation_costs?.data ?? null;
}

// ─── ai_list_agent_costs (Agents leaderboard) ──────────────────────────────────
// Top agent INVOCATIONS across conversations, ranked by cost | latency | errors.
// One row per invocation (same agent name recurs), each linked to its conversation.

export type AgentSortBy = 'cost' | 'latency' | 'errors';

export interface AgentCallRow {
  agent_id: string;
  agent_name: string;
  conversation_id: string; // session_id — cross-link target
  conversation_title: string;
  account_id: string;
  status: string;
  started_at: string;
  cost_usd: number;
  latency_sum_seconds: number;
  latency_max_seconds: number;
  latency_median_seconds: number;
  llm_call_count: number;
  error_count: number;
  input_tokens: number;
  output_tokens: number;
  models_used: string[];
}

/** One agent name's invocation-latency distribution over the report window (graph). */
export interface AgentLatencyProfile {
  agent_name: string;
  p50_seconds: number;
  p90_seconds: number;
  p99_seconds: number;
  invocations: number;
}

export interface AgentCallList {
  sort_by: AgentSortBy;
  limit: number;
  /** Echoed pXX when a latency-outlier filter is applied (0 = none). */
  latency_percentile: number;
  /** Resolved threshold (total invocation latency, seconds) over the 24h baseline. 0 = no baseline. */
  latency_threshold_seconds: number;
  /** Per-agent latency profile (top agents by p90) — powers the agent-wise latency graph. */
  latency_by_agent: AgentLatencyProfile[];
  rows: AgentCallRow[];
}

/** Allowed latency-outlier percentiles; 0 = no filter. */
export type AgentLatencyPercentile = 0 | 80 | 85 | 90 | 95 | 99;

export interface ListAgentCostsRequest extends UsageFilterRequest {
  /** Exclude these agent names (e.g. infra-debug agents). */
  agentsExclude?: string[];
  sortBy?: AgentSortBy;
  limit?: number;
  /** Show only invocations whose total latency ≥ this percentile (24h baseline). 0/undefined = all. */
  latencyPercentile?: AgentLatencyPercentile;
}

export async function listAgentCosts(req: ListAgentCostsRequest, signal?: AbortSignal): Promise<AgentCallList | null> {
  const query = `mutation ListAgentCosts(
    $accountIds: [String!], $startDate: String!, $endDate: String!, $userId: String,
    $sources: [String!], $models: [String!], $providers: [String!], $agents: [String!], $agentsExclude: [String!], $statuses: [String!],
    $sortBy: String, $limit: Int, $latencyPercentile: Int
  ) {
    ai_list_agent_costs(request: {
      account_ids: $accountIds, start_date: $startDate, end_date: $endDate, user_id: $userId,
      sources: $sources, models: $models, providers: $providers, agents: $agents, agents_exclude: $agentsExclude, statuses: $statuses,
      sort_by: $sortBy, limit: $limit, latency_percentile: $latencyPercentile
    }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'ListAgentCosts',
    {
      accountIds: arr(req.accountIds),
      startDate: req.startDate,
      endDate: req.endDate,
      userId: req.userId ?? null,
      sources: arr(req.sources),
      models: arr(req.models),
      providers: arr(req.providers),
      agents: arr(req.agents),
      agentsExclude: arr(req.agentsExclude),
      statuses: arr(req.statuses),
      sortBy: req.sortBy ?? 'cost',
      limit: req.limit ?? 100,
      latencyPercentile: req.latencyPercentile ?? 0,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_list_agent_costs?.data ?? null;
}

// ─── ai_aggregate_tool_usage (Tools leaderboard) ───────────────────────────────
// Per-tool rollup across conversations from llm_conversation_tool_calls: usage
// (calls), reliability (success/error), latency (avg/p90/max duration), and reach
// (distinct agents/conversations). Tool calls carry no LLM token cost, so the only
// cost is "downstream": the LLM cost of sub-agents a tool spawned (child_agent_id),
// 0 for plain tools. The tool-calls table has no model/provider/status columns, so
// only account + date + source from the shared bar apply here.

export type ToolSortBy = 'calls' | 'errors' | 'duration' | 'cost';

export interface ToolUsageRow {
  tool_name: string;
  tool_type: string;
  calls: number;
  success_count: number;
  error_count: number;
  in_progress_count: number;
  error_rate_pct: number;
  avg_duration_seconds: number;
  p90_duration_seconds: number;
  max_duration_seconds: number;
  distinct_agents: number;
  distinct_conversations: number;
  /** LLM cost of sub-agents this tool spawned; 0 for non-spawn tools. */
  downstream_cost_usd: number;
  downstream_llm_calls: number;
}

export interface ToolUsageList {
  sort_by: ToolSortBy;
  limit: number;
  rows: ToolUsageRow[];
}

export interface ListToolUsageRequest {
  accountIds?: string[];
  startDate: string; // RFC3339 UTC
  endDate: string; // RFC3339 UTC
  userId?: string;
  /** Only account + date + source apply — tool calls have no model/provider/status. */
  sources?: string[];
  sortBy?: ToolSortBy;
  limit?: number;
}

export async function listToolUsage(req: ListToolUsageRequest, signal?: AbortSignal): Promise<ToolUsageList | null> {
  const query = `mutation AggregateToolUsage(
    $accountIds: [String!], $startDate: String!, $endDate: String!, $userId: String,
    $sources: [String!], $sortBy: String, $limit: Int
  ) {
    ai_aggregate_tool_usage(request: {
      account_ids: $accountIds, start_date: $startDate, end_date: $endDate, user_id: $userId,
      sources: $sources, sort_by: $sortBy, limit: $limit
    }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'AggregateToolUsage',
    {
      accountIds: arr(req.accountIds),
      startDate: req.startDate,
      endDate: req.endDate,
      userId: req.userId ?? null,
      sources: arr(req.sources),
      sortBy: req.sortBy ?? 'calls',
      limit: req.limit ?? 100,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_aggregate_tool_usage?.data ?? null;
}

// ─── ai_list_tool_calls (per-tool invocation explorer / failures drill-in) ──────
// The recent invocations of ONE tool (newest first), optionally restricted to a set
// of statuses, each linked to its conversation. Powers the Tools-tab drill-in.

/** Tool statuses, grouped for the drill-in toggle (lowercased on write). */
export const TOOL_STATUS_GROUPS = {
  errors: ['fail', 'error', 'terminated'],
  in_progress: ['in_progress', 'waiting', 'waiting_for_client'],
} as const;

export type ToolStatusGroup = 'all' | 'errors' | 'in_progress';

/** Resolve a UI status group to the status values the API filters on ([] = all). */
export function toolStatusesFor(group: ToolStatusGroup): string[] {
  return group === 'all' ? [] : [...TOOL_STATUS_GROUPS[group]];
}

export interface ToolCallRow {
  id: string;
  tool_name: string;
  tool_type: string;
  status: string;
  agent_id: string;
  agent_name: string;
  conversation_id: string; // session_id — cross-link target
  conversation_title: string;
  account_id: string;
  duration_seconds: number;
  created_at: string;
  parameters: string; // input args snippet
  response: string; // output snippet
  stderr: string; // separate stderr stream when present
}

export interface ToolCallList {
  tool_name: string;
  statuses: string[];
  limit: number;
  rows: ToolCallRow[];
}

export interface ListToolCallsRequest {
  accountIds?: string[];
  startDate: string; // RFC3339 UTC
  endDate: string; // RFC3339 UTC
  userId?: string;
  sources?: string[];
  toolName: string;
  /** Restrict to these tool statuses (empty = all). */
  statuses?: string[];
  limit?: number;
}

export async function listToolCalls(req: ListToolCallsRequest, signal?: AbortSignal): Promise<ToolCallList | null> {
  const query = `mutation ListToolCalls(
    $accountIds: [String!], $startDate: String!, $endDate: String!, $userId: String,
    $sources: [String!], $toolName: String!, $statuses: [String!], $limit: Int
  ) {
    ai_list_tool_calls(request: {
      account_ids: $accountIds, start_date: $startDate, end_date: $endDate, user_id: $userId,
      sources: $sources, tool_name: $toolName, statuses: $statuses, limit: $limit
    }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'ListToolCalls',
    {
      accountIds: arr(req.accountIds),
      startDate: req.startDate,
      endDate: req.endDate,
      userId: req.userId ?? null,
      sources: arr(req.sources),
      toolName: req.toolName,
      statuses: arr(req.statuses),
      limit: req.limit ?? 100,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_list_tool_calls?.data ?? null;
}

/** Full recursive drill-down of one conversation (flat arrays + per-node cost). */
export async function getConversationTree(req: ConversationDetailRequest, signal?: AbortSignal): Promise<ConversationTree | null> {
  const query = `mutation GetConversationTree($conversationId: String!, $accountId: String!, $userId: String) {
    ai_get_conversation_tree(request: { conversation_id: $conversationId, account_id: $accountId, user_id: $userId }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'GetConversationTree',
    {
      conversationId: req.conversationId,
      accountId: req.accountId,
      userId: req.userId ?? null,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_get_conversation_tree?.data ?? null;
}

export interface ConversationAgentRequest extends ConversationDetailRequest {
  agentId: string;
  /** When set, the response carries ONLY that model call, with its prompt/response
   * trace populated (the lazy "view prompt" fetch). Omit for the normal listing. */
  modelCallId?: string;
}

/** On-click detail for one agent: its model calls (with cost_breakdown), tool calls
 * (params/response/thought), and the agent's query/thought/response. When
 * `modelCallId` is set, returns just that call with its prompt/response trace. */
export async function getConversationAgent(req: ConversationAgentRequest, signal?: AbortSignal): Promise<ConversationAgentDetail | null> {
  const query = `mutation GetConversationAgent($conversationId: String!, $accountId: String!, $agentId: String!, $userId: String, $modelCallId: String) {
    ai_get_conversation_agent(request: { conversation_id: $conversationId, account_id: $accountId, agent_id: $agentId, user_id: $userId, model_call_id: $modelCallId }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'GetConversationAgent',
    {
      conversationId: req.conversationId,
      accountId: req.accountId,
      agentId: req.agentId,
      userId: req.userId ?? null,
      modelCallId: req.modelCallId ?? null,
    },
    undefined,
    signal
  );
  return response?.data?.data?.ai_get_conversation_agent?.data ?? null;
}

// ─── ai_generate_conversation_optimization (read-only "Optimize" button) ───────
// Profiles one conversation's cost/flow and returns optimization findings — model
// downgrades, redundant agents, retry/failure waste — with every dollar computed
// server-side. Creates no conversation rows (unlike invoking the chat agent).

export type OptFindingType =
  | 'retry_waste'
  | 'failure_waste'
  | 'model_downgrade'
  | 'agent_redundant'
  | 'context_bloat'
  | 'failure_root_cause'
  | 'excessive_iteration'
  | 'cache_underutilization';

export interface OptTarget {
  kind: string;
  agent_name?: string;
  agent_id?: string;
  model?: string;
  call_count?: number;
}

/** One server-derived, verifiable fact backing a finding (never LLM numbers). */
export interface OptEvidenceFact {
  label: string;
  value: string;
  source?: string; // profile path the value came from
}

/** A real model call backing a finding — inline ground truth for verification. */
export interface OptExemplar {
  agent_id: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cost_usd: number;
  task?: string;
  outcome?: string;
}

export interface OptFinding {
  id: string;
  type: OptFindingType;
  title: string;
  target: OptTarget;
  evidence: string;
  /** Server-derived proof facts (distribution, prices, counts). */
  supporting_evidence?: OptEvidenceFact[];
  /** Agent instances the aggregate was computed from — deep-link to agent detail. */
  backing_agent_ids?: string[];
  /** A few real calls (priciest) with their actual numbers. */
  exemplars?: OptExemplar[];
  recommendation: string;
  suggested_model?: string;
  current_cost_usd: number;
  estimated_savings_usd: number;
  estimated_savings_pct: number;
  confidence: 'high' | 'medium' | 'low';
  overlaps_with?: string[];
  /** Trade-off grouping: cost_only | cost_and_accuracy | cost_and_latency | reliability. */
  category?: string;
  /** Directional effect per axis: keys cost/latency/accuracy → improves|neutral|degrades. */
  impact?: Record<string, string>;
  /** Advisory findings carry no recomputed dollar saving (use current_cost_usd as addressable). */
  advisory?: boolean;
}

export interface OptimizationTotals {
  cost_usd: number;
  model_calls: number;
  tool_calls: number;
  agents: number;
  retry_waste_usd: number;
  failure_waste_usd: number;
  cache_savings_usd: number;
  model_latency_sec?: number;
  tool_duration_sec?: number;
}

export interface OptimizationProfile {
  conversation_id: string;
  session_id: string;
  title: string;
  totals: OptimizationTotals;
  // The remaining rollups (agents_by_type, models, spawn_graph, top_cost_agents,
  // pricing) are echoed for the "export" — kept loose; the UI renders findings.
  [key: string]: unknown;
}

export interface ConversationOptimization {
  conversation_id: string;
  current_cost_usd: number;
  total_potential_savings_usd: number;
  total_potential_savings_pct: number;
  summary: string;
  findings: OptFinding[];
  profile: OptimizationProfile;
}

export type ConversationOptimizationRequest = ConversationDetailRequest;

const OPT_POLL_INTERVAL_MS = 3000;
const OPT_POLL_TIMEOUT_MS = 6 * 60 * 1000; // the analysis is a large LLM call

function optSleep(ms: number, signal?: AbortSignal): Promise<void> {
  // Already-aborted: reject now, else the 'abort' listener never fires (the event
  // has passed) and we'd wait the full interval before the caller notices.
  if (signal?.aborted) return Promise.reject(new DOMException('aborted', 'AbortError'));
  return new Promise((resolve, reject) => {
    const t = setTimeout(resolve, ms);
    signal?.addEventListener(
      'abort',
      () => {
        clearTimeout(t);
        reject(new DOMException('aborted', 'AbortError'));
      },
      { once: true }
    );
  });
}

// Runs the cost_optimizer agent through the chat/investigate path. The analysis is
// a minutes-long LLM call, so we run it ASYNC and poll: the agent is hosted in a
// real conversation (session = optimizer-<target>, source = "Optimize") — which
// lets its own token usage be tracked (the token table needs non-null conversation
// /message ids) and keeps it out of the analyser (source=Optimize is excluded).
// The agent stores the structured ConversationOptimization as JSON in the message
// response; we poll ai_get_conversation_v3 for it and parse.
export async function generateConversationOptimization(
  req: ConversationOptimizationRequest,
  signal?: AbortSignal
): Promise<ConversationOptimization | null> {
  const target = req.conversationId; // the conversation's session id
  const sessionId = `optimizer-${target}`;

  // Build the async trigger payload (fired below, after the baseline snapshot).
  const triggerObj: Record<string, unknown> = {
    account_id: req.accountId,
    query: `@cost_optimizer ${target}`,
    session_id: sessionId,
    source: 'Optimize',
    async: true,
  };
  if (req.userId) triggerObj.user_id = req.userId;

  // The optimizer session (optimizer-<target>) is reused across re-analyses, so a
  // prior COMPLETED message may already exist. Snapshot the newest existing
  // message timestamp BEFORE triggering; we only accept a result newer than this,
  // so a re-run can't return the stale previous answer.
  const pollQuery = `query OptimizerPoll($request: AiGetConversationV3Request!) {
    ai_get_conversation_v3(request: $request) {
      messages { response status created_at }
    }
  }`;
  const newestTs = (msgs: { created_at?: string }[]): string => msgs.reduce((max, m) => ((m.created_at ?? '') > max ? m.created_at ?? '' : max), '');
  let baselineTs = '';
  try {
    const pre = await queryGraphQL(pollQuery, 'OptimizerPoll', { request: { account_id: req.accountId, session_id: sessionId } }, undefined, signal);
    baselineTs = newestTs(pre?.data?.data?.ai_get_conversation_v3?.messages ?? []);
  } catch {
    /* first run / no prior conversation — baseline stays empty */
  }

  // 1) Fire async — returns immediately; analysis runs in the worker pool.
  const triggerMutation = `mutation OptimizeConversation {
    ai_execute_investigation(request: __REQUEST__) { data { conversation_id } }
  }`;
  await queryGraphQL(triggerMutation.replace('__REQUEST__', gqlStringify(triggerObj)), 'OptimizeConversation', {}, undefined, signal);

  // 2) Poll the optimizer conversation for the stored result (newer than baseline).
  const deadline = Date.now() + OPT_POLL_TIMEOUT_MS;
  while (Date.now() < deadline) {
    await optSleep(OPT_POLL_INTERVAL_MS, signal); // also lets the message row be created before first read
    const resp = await queryGraphQL(pollQuery, 'OptimizerPoll', { request: { account_id: req.accountId, session_id: sessionId } }, undefined, signal);
    const messages: { response?: string; status?: string; created_at?: string }[] = resp?.data?.data?.ai_get_conversation_v3?.messages ?? [];
    if (!messages.length) continue;
    const latest = messages
      .slice()
      .sort((a, b) => (a.created_at ?? '').localeCompare(b.created_at ?? ''))
      .pop();
    // Ignore the prior run's message until the fresh one supersedes it.
    if (baselineTs && (latest?.created_at ?? '') <= baselineTs) continue;
    const status = (latest?.status ?? '').toUpperCase();
    if (status === 'FAILED' || status === 'KILLED') throw new Error('Optimization failed');
    if (status === 'COMPLETED' && latest?.response) {
      try {
        return JSON.parse(latest.response) as ConversationOptimization;
      } catch {
        return null; // completed but unparseable
      }
    }
    // IN_PROGRESS / WAITING → keep polling
  }
  throw new Error('Optimization timed out — try again');
}

/** A previously-stored analysis + when it was produced. */
export interface StoredOptimization {
  optimization: ConversationOptimization;
  analyzedAt: string;
}

// Cheap read (NO LLM): if this conversation was already analyzed, return the stored
// result from its optimizer-<target> conversation so the Optimize tab can show it
// immediately instead of re-running. Returns null if there's no completed analysis.
export async function getStoredConversationOptimization(
  req: ConversationOptimizationRequest,
  signal?: AbortSignal
): Promise<StoredOptimization | null> {
  const sessionId = `optimizer-${req.conversationId}`;
  const query = `query StoredOptimization($request: AiGetConversationV3Request!) {
    ai_get_conversation_v3(request: $request) {
      messages { response status created_at }
    }
  }`;
  const resp = await queryGraphQL(query, 'StoredOptimization', { request: { account_id: req.accountId, session_id: sessionId } }, undefined, signal);
  const messages: { response?: string; status?: string; created_at?: string }[] = resp?.data?.data?.ai_get_conversation_v3?.messages ?? [];
  // newest completed message with a parseable payload
  const completed = messages
    .filter((m) => (m.status ?? '').toUpperCase() === 'COMPLETED' && m.response)
    .sort((a, b) => (a.created_at ?? '').localeCompare(b.created_at ?? ''));
  const latest = completed.pop();
  if (!latest?.response) return null;
  try {
    return { optimization: JSON.parse(latest.response) as ConversationOptimization, analyzedAt: latest.created_at ?? '' };
  } catch {
    return null;
  }
}

/** Existing per-conversation summary metrics (basic detail panel + per-model rollups). */
export async function getConversationUsageMetrics(req: ConversationDetailRequest, signal?: AbortSignal): Promise<ConversationUsageSummary | null> {
  const query = `mutation GetConversationUsageMetrics($conversationId: String!, $accountId: String!) {
    ai_get_conversation_usage_metrics(request: { conversation_id: $conversationId, account_id: $accountId }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'GetConversationUsageMetrics',
    {
      conversationId: req.conversationId,
      accountId: req.accountId,
    },
    undefined,
    signal
  );
  // Response envelope is { data: { conversation: <summary> } }.
  return response?.data?.data?.ai_get_conversation_usage_metrics?.data?.conversation ?? null;
}

/** Three optimization levers, in priority order. */
export type PromptLever = 'count' | 'cache' | 'relevance';
/** Whether a component is byte-identical every call (cacheable) or varies. */
export type PromptClassification = 'static' | 'dynamic' | 'mixed';

/** One node in the prompt's component tree. The FRONTEND builds and measures this
 * tree from the real prompt (a MACRO component per message groups its section
 * SUB-components via `parent`); the model only annotates each node by `id`
 * (classification + note). loc/size/tokens are measured from the real content — the
 * model never defines sizes — so the numbers always match the actual prompt. */
export interface PromptComponent {
  /** Stable hierarchical id, e.g. "1", "1.2". */
  id: string;
  name: string;
  /** Parent component id, omitted/empty for top-level (macro) components. */
  parent?: string;
  role?: string; // system | human | ai | tool | …
  /** Filled from the model's per-id annotation (frontend leaves it unset). */
  classification?: PromptClassification;
  loc: number; // lines of content
  size: number; // bytes (UTF-8)
  tokens: number; // chars ÷ 4 (text estimate; the header total uses billed tokens)
  pct: number; // % of measured-token total
  /** Verbatim content of this component — present on leaf nodes (empty for groups). */
  content?: string;
  /** Filled from the model's per-id annotation. */
  note?: string;
}

/** The model's per-component annotation (keyed by component id). */
export interface PromptComponentAnnotation {
  classification?: PromptClassification;
  note?: string;
}

/** Cache-prefix verdict: is all STATIC content contiguous at the front? */
export interface PromptCacheVerdict {
  /** True when every STATIC component precedes every DYNAMIC one (clean cacheable prefix). */
  prefix_contiguous: boolean;
  /** The first dynamic-before-static offender that poisons the prefix (e.g. a timestamp); empty if none. */
  poisoning?: string;
  detail: string;
}

/** Declared-but-unused content — dead weight billed on every step. */
export interface PromptDeadWeight {
  name: string;
  tokens: number;
  detail: string;
}

/** One ranked optimization with its projected saving + explicit accuracy impact. */
export interface PromptOptimization {
  title: string;
  lever: PromptLever; // count | cache | relevance
  /** tier | subset | relocate | dedupe | conditionalize | reorder | cut | compact */
  technique?: string;
  projected_token_saving: number;
  /** Cache reorders can be ~10× cost wins at zero token change — captured here. */
  projected_cost_saving_pct?: number;
  /** Implementation cost: low (text edit / reorder) … high (code changes across agents). */
  effort?: string;
  /** REQUIRED per the methodology's guardrails: never trade accuracy for size silently. */
  accuracy_impact: string;
  detail: string;
}

// The model's output. Components/sizes are NOT here — the frontend owns those; the
// model returns per-id classifications plus the qualitative judgment.
export interface PromptAnalysis {
  summary: string;
  /** Pareto: name the 1–2 components that dominate the token count, with their %. */
  dominant_buckets: string;
  /** Per-component annotation, keyed by the frontend component id. */
  classifications?: Record<string, PromptComponentAnnotation | PromptClassification>;
  cache_verdict: PromptCacheVerdict;
  dead_weight: PromptDeadWeight[];
  optimizations: PromptOptimization[];
  /** One concrete real call validated bottom-up (where tokens/cost/latency go). */
  concrete_instance?: string;
}

export interface StoredPromptAnalysis {
  analysis: PromptAnalysis;
  analyzedAt: string;
}

export interface AnalyzePromptRequest {
  accountId: string;
  /** model_call_id — gives the analysis a stable per-call session id so re-opening
   * the same call shows the cached result instead of re-running. */
  callId: string;
  /** Raw `prompt_messages` JSON for the call (the exact bytes sent to the model). */
  promptJson: string;
  /** Frontend-built component list (id / parent / name / role / loc / size / tokens /
   * % / content preview) — the leaves the model must classify by id and reference. */
  measured?: string;
  /** The model's response for this call — the usage signal for declared-vs-used. */
  responseContent?: string;
  userId?: string;
}

// The query is inlined into a GraphQL mutation string; a multi-hundred-KB prompt
// would blow both the request and the model's context window. Cap the embedded
// prompt and mark the truncation so the analysis (and the UI) can say so honestly.
const PROMPT_ANALYSIS_MAX_CHARS = 120_000;

const PROMPT_ANALYSIS_POLL_INTERVAL_MS = 3000;
const PROMPT_ANALYSIS_POLL_TIMEOUT_MS = 3 * 60 * 1000; // a single completion, shorter than the optimizer

const PROMPT_ANALYSIS_INSTRUCTIONS = `You are an LLM prompt-engineering analyst. Objective: reduce this prompt's token cost and latency WITHOUT degrading model accuracy. Optimize three levers in priority order: (1) token count, (2) cache rate, (3) relevance.

The prompt has ALREADY been broken into COMPONENTS and measured for you (id / parent / name / role / loc / size / tokens / % / content-preview, in send order). You do NOT define components, sizes, or content — those are ground truth. Your job is to (a) classify each component by its id, and (b) produce the qualitative analysis below. The RAW PROMPT JSON and the model's RESPONSE are also provided for deeper inspection.

Procedure:
- CLASSIFY every component id as STATIC (byte-identical every call: role, format rules, tool details, app overview), DYNAMIC (changes per call: date, memory, history, notebook, observations, question), or MIXED. Return a map of component id → classification (+ an optional one-line note).
- Follow the money (Pareto): name the 1-2 components (by name) that dominate the token count, with their %. Do not trim uniformly.
- Check the cache layout: a prompt cache matches a byte-identical PREFIX. Using the send order, verify all STATIC components are contiguous at the front and DYNAMIC at the tail. Any dynamic value before static content (a timestamp is the classic offender) poisons the cache from that byte on — flag it.
- Separate the two levers: cutting/tiering/compacting/deduping lowers token COUNT; caching (reordering static-first) lowers COST at the same token count (~10× cheaper). A pure reorder can be a cost win with zero content change.
- Prove waste with usage data: cross-check tools/sections DECLARED in the prompt against what the RESPONSE actually used. Declared-but-never-used content is dead weight on every step. If usage can't be confirmed from the response, say so and mark it a hypothesis.

Optimizations must be REALISTIC and PRACTICAL — an engineer should be able to act on each one as written:
- Target SPECIFIC components by name (e.g. "tool: kubectl_exec", "system › Command Schema"), never the prompt as a whole.
- State the EXACT change: what is cut, tiered (one-line default + load-on-demand), deduped against which other section, relocated to where (e.g. into the sub-agent's own prompt), conditionalized on which request type, or reordered to fix the cache prefix.
- projected_token_saving MUST be grounded in the targeted components' MEASURED tokens (from the table) and never exceed them. Be conservative; a partial trim saves a fraction, not the whole.
- Ban vague advice ("be more concise", "simplify", "optimize the prompt"). If you cannot name the target and the mechanism, drop the suggestion.
- When removing content risks accuracy or its usage is unconfirmed, prefer tiering / subsetting / load-on-demand over deletion.
- Give an implementation "effort" of low | medium | high (low = text edit / reorder; high = code changes across agents).
- Guardrails: keep the user question/task LAST (recency). If a critical rule loses end-of-prompt recency after a reorder, restate it in one short line at the tail. Reordering must be semantically equivalent. State the accuracy impact explicitly for EVERY optimization.

Respond with ONLY a single JSON object — no prose, no markdown code fences — matching this schema exactly:
{
  "summary": "2-3 sentence verdict: where the tokens/cost/latency go and the top lever to pull",
  "dominant_buckets": "the 1-2 components that dominate, each with its % of total tokens",
  "classifications": { "<component id>": { "classification": "static|dynamic|mixed", "note": "optional one line" } },
  "cache_verdict": { "prefix_contiguous": <bool>, "poisoning": "first dynamic-before-static offender, empty if none", "detail": "one line" },
  "dead_weight": [ { "name": "declared-but-unused item", "tokens": <int, from the table>, "detail": "why it is dead weight + confidence" } ],
  "optimizations": [
    { "title": "concrete action naming the target", "lever": "count|cache|relevance", "technique": "tier|subset|relocate|dedupe|conditionalize|reorder|cut|compact", "projected_token_saving": <int, bounded by the target's measured tokens>, "projected_cost_saving_pct": <0-100, optional>, "effort": "low|medium|high", "accuracy_impact": "explicit note — neutral/safe or the specific risk", "detail": "exactly what to change and where" }
  ],
  "concrete_instance": "one real call validated bottom-up: tokens × price → cost, confirming the thesis"
}
Rules: classifications must cover every component id. optimizations ranked by projected saving, biggest first, and each must be actionable as written. Use empty arrays where there is genuinely nothing to report.

COMPONENTS (already measured — id | parent | name | role | loc | size | tokens | % | preview):
__MEASURED__

MODEL RESPONSE (usage signal for declared-vs-used):
__RESPONSE__

RAW PROMPT JSON:
`;

/** Tolerant parse of the LLM's reply into a PromptAnalysis: strips ```json fences
 * and falls back to the first {...} span, so a chatty model can't break rendering.
 * Components/sizes are owned by the frontend — this only validates the model's
 * classifications + qualitative analysis. */
function parsePromptAnalysis(raw: string): PromptAnalysis | null {
  if (!raw) return null;
  const tryParse = (s: string): PromptAnalysis | null => {
    try {
      const o = JSON.parse(s);
      if (o && Array.isArray(o.optimizations) && o.cache_verdict) return o as PromptAnalysis;
    } catch {
      /* not JSON */
    }
    return null;
  };
  const direct = tryParse(raw.trim());
  if (direct) return direct;
  const fenced = raw.match(/```(?:json)?\s*([\s\S]*?)```/i);
  if (fenced) {
    const f = tryParse(fenced[1].trim());
    if (f) return f;
  }
  const start = raw.indexOf('{');
  const end = raw.lastIndexOf('}');
  if (start >= 0 && end > start) return tryParse(raw.slice(start, end + 1));
  return null;
}

const promptAnalysisSession = (callId: string): string => `prompt-analysis-${callId}`;

/** Fire the @LLM analysis async and poll the prompt-analysis-<callId> conversation
 * for the structured result (newer than any prior run). Mirrors
 * generateConversationOptimization's baseline+poll so a re-run never returns stale JSON. */
export async function analyzePromptTrace(req: AnalyzePromptRequest, signal?: AbortSignal): Promise<PromptAnalysis | null> {
  const sessionId = promptAnalysisSession(req.callId);

  let promptBody = req.promptJson ?? '';
  if (promptBody.length > PROMPT_ANALYSIS_MAX_CHARS) {
    promptBody = `${promptBody.slice(0, PROMPT_ANALYSIS_MAX_CHARS)}\n…[prompt truncated for analysis — ${
      promptBody.length - PROMPT_ANALYSIS_MAX_CHARS
    } more chars]`;
  }
  // The model response is only a usage signal; cap it so it can't dominate the call.
  const responseSignal = (req.responseContent ?? '').slice(0, 8000) || '(response not captured)';
  const instructions = PROMPT_ANALYSIS_INSTRUCTIONS.replace('__MEASURED__', req.measured || '(no measured table provided)').replace(
    '__RESPONSE__',
    responseSignal
  );
  const triggerObj: Record<string, unknown> = {
    account_id: req.accountId,
    query: `@LLM ${instructions}${promptBody}`,
    session_id: sessionId,
    source: 'Optimize',
    async: true,
  };
  if (req.userId) triggerObj.user_id = req.userId;

  const pollQuery = `query PromptAnalysisPoll($request: AiGetConversationV3Request!) {
    ai_get_conversation_v3(request: $request) {
      messages { response status created_at }
    }
  }`;
  const newestTs = (msgs: { created_at?: string }[]): string => msgs.reduce((max, m) => ((m.created_at ?? '') > max ? m.created_at ?? '' : max), '');
  let baselineTs = '';
  try {
    const pre = await queryGraphQL(
      pollQuery,
      'PromptAnalysisPoll',
      { request: { account_id: req.accountId, session_id: sessionId } },
      undefined,
      signal
    );
    baselineTs = newestTs(pre?.data?.data?.ai_get_conversation_v3?.messages ?? []);
  } catch {
    /* first run — baseline stays empty */
  }

  const triggerMutation = `mutation AnalyzePrompt {
    ai_execute_investigation(request: __REQUEST__) { data { conversation_id } }
  }`;
  await queryGraphQL(triggerMutation.replace('__REQUEST__', gqlStringify(triggerObj)), 'AnalyzePrompt', {}, undefined, signal);

  const deadline = Date.now() + PROMPT_ANALYSIS_POLL_TIMEOUT_MS;
  while (Date.now() < deadline) {
    await optSleep(PROMPT_ANALYSIS_POLL_INTERVAL_MS, signal);
    const resp = await queryGraphQL(
      pollQuery,
      'PromptAnalysisPoll',
      { request: { account_id: req.accountId, session_id: sessionId } },
      undefined,
      signal
    );
    const messages: { response?: string; status?: string; created_at?: string }[] = resp?.data?.data?.ai_get_conversation_v3?.messages ?? [];
    if (!messages.length) continue;
    const latest = messages
      .slice()
      .sort((a, b) => (a.created_at ?? '').localeCompare(b.created_at ?? ''))
      .pop();
    if (baselineTs && (latest?.created_at ?? '') <= baselineTs) continue;
    const status = (latest?.status ?? '').toUpperCase();
    if (status === 'FAILED' || status === 'KILLED') throw new Error('Prompt analysis failed');
    if (status === 'COMPLETED' && latest?.response) return parsePromptAnalysis(latest.response);
    // IN_PROGRESS / WAITING → keep polling
  }
  throw new Error('Prompt analysis timed out — try again');
}

/** Cheap read (NO LLM): return a previously-stored analysis for this call so the
 * trace modal can show it immediately instead of re-running. */
export async function getStoredPromptAnalysis(
  req: { accountId: string; callId: string },
  signal?: AbortSignal
): Promise<StoredPromptAnalysis | null> {
  const sessionId = promptAnalysisSession(req.callId);
  const query = `query StoredPromptAnalysis($request: AiGetConversationV3Request!) {
    ai_get_conversation_v3(request: $request) {
      messages { response status created_at }
    }
  }`;
  const resp = await queryGraphQL(
    query,
    'StoredPromptAnalysis',
    { request: { account_id: req.accountId, session_id: sessionId } },
    undefined,
    signal
  );
  const messages: { response?: string; status?: string; created_at?: string }[] = resp?.data?.data?.ai_get_conversation_v3?.messages ?? [];
  const completed = messages
    .filter((m) => (m.status ?? '').toUpperCase() === 'COMPLETED' && m.response)
    .sort((a, b) => (a.created_at ?? '').localeCompare(b.created_at ?? ''));
  const latest = completed.pop();
  if (!latest?.response) return null;
  const analysis = parsePromptAnalysis(latest.response);
  return analysis ? { analysis, analyzedAt: latest.created_at ?? '' } : null;
}
