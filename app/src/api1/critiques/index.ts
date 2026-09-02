/** Critique Analytics API client — cross-tenant, no account_id. Gated server-side to tenant_admin+. */
import { queryGraphQL } from '@lib/HttpService';

// ─── Response shapes (mirror agents/core/critique_analytics.go's json tags) ────

export interface CritiqueTotals {
  judged: number;
  refined: number;
  refine_pct: number;
}

export interface CritiqueAgentRow {
  agent_name: string;
  judged: number;
  refined: number;
  refine_pct: number;
}

/** One real critique backing a theme count. */
export interface CritiqueThemeExample {
  id: string;
  agent_name: string;
  feedback: string;
}

/** One theme's count among 'refine' decisions, plus recent matching critiques. Heuristic, not a taxonomy. */
export interface CritiqueThemeRow {
  theme: string;
  count: number;
  examples: CritiqueThemeExample[];
}

export interface CritiqueSummary {
  totals: CritiqueTotals;
  by_agent: CritiqueAgentRow[];
  themes: CritiqueThemeRow[];
}

export interface CritiqueTrendPoint {
  bucket: string; // RFC3339 UTC, truncated to the granularity
  judged: number;
  refined: number;
  refine_pct: number;
}

export interface CritiqueTrend {
  granularity: string; // day|week|month
  points: CritiqueTrendPoint[];
}

/** One raw critique record: query, answer, and feedback. */
export interface CritiqueListRow {
  id: string;
  agent_name: string;
  decision: string;
  input: string;
  critiqued_content: string;
  feedback: string;
  created_at: string;
  /** Cross-tenant list — account_id is per-row, not a request-level scope. */
  account_id: string;
  conversation_id: string;
  message_id: string;
  /** Empty when the owning conversation no longer exists. */
  session_id: string;
}

export interface CritiqueList {
  rows: CritiqueListRow[];
  total: number;
}

// ─── Request shapes ─────────────────────────────────────────────────────────

export interface CritiqueFilterRequest {
  startDate: string; // RFC3339 UTC
  endDate: string; // RFC3339 UTC
  agents?: string[];
}

export interface CritiqueTrendRequest extends CritiqueFilterRequest {
  granularity?: 'day' | 'week' | 'month';
}

export interface CritiqueListRequest extends CritiqueFilterRequest {
  decisions?: string[];
  /** When set, overrides `decisions` server-side. */
  theme?: string;
  limit?: number;
  offset?: number;
}

// ─── Callers ────────────────────────────────────────────────────────────────

const arr = (v?: string[]): string[] => v ?? [];

/** Filter-wide totals, refine-rate-by-agent breakdown, and heuristic theme counts. */
export async function aggregateCritiques(req: CritiqueFilterRequest, signal?: AbortSignal): Promise<CritiqueSummary | null> {
  const query = `mutation AggregateCritiques($startDate: String!, $endDate: String!, $agents: [String!]) {
    critiques_aggregate_all(request: { start_date: $startDate, end_date: $endDate, agents: $agents }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'AggregateCritiques',
    { startDate: req.startDate, endDate: req.endDate, agents: arr(req.agents) },
    undefined,
    signal
  );
  return response?.data?.data?.critiques_aggregate_all?.data ?? null;
}

/** Judged/refined counts bucketed by day/week/month — the Overview rate-over-time chart. */
export async function trendCritiques(req: CritiqueTrendRequest, signal?: AbortSignal): Promise<CritiqueTrend | null> {
  const query = `mutation TrendCritiques($startDate: String!, $endDate: String!, $agents: [String!], $granularity: String) {
    critiques_trend_all(request: { start_date: $startDate, end_date: $endDate, agents: $agents, granularity: $granularity }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'TrendCritiques',
    { startDate: req.startDate, endDate: req.endDate, agents: arr(req.agents), granularity: req.granularity ?? null },
    undefined,
    signal
  );
  return response?.data?.data?.critiques_trend_all?.data ?? null;
}

/** Paginated raw critique rows for the Browse view drill-down. */
export async function listCritiques(req: CritiqueListRequest, signal?: AbortSignal): Promise<CritiqueList | null> {
  const query = `mutation ListCritiques(
    $startDate: String!, $endDate: String!, $agents: [String!], $decisions: [String!], $theme: String, $limit: Int, $offset: Int
  ) {
    critiques_list_all(request: {
      start_date: $startDate, end_date: $endDate, agents: $agents, decisions: $decisions, theme: $theme, limit: $limit, offset: $offset
    }) {
      data
    }
  }`;
  const response = await queryGraphQL(
    query,
    'ListCritiques',
    {
      startDate: req.startDate,
      endDate: req.endDate,
      agents: arr(req.agents),
      decisions: arr(req.decisions),
      theme: req.theme || null,
      limit: req.limit ?? 50,
      offset: req.offset ?? 0,
    },
    undefined,
    signal
  );
  return response?.data?.data?.critiques_list_all?.data ?? null;
}
