/**
 * AI Cost Analyser — data model (PROTOTYPE).
 *
 * Mirrors the spec's §2 cost-telemetry hierarchy exactly:
 *
 *   Tenant
 *   └── Conversation / Workflow Run   (one execution instance)
 *       └── Sub-task / Step           (an agent or node invocation)
 *           └── Model Call            (one LLM API request — the billable unit)
 *
 * This file is the *contract* a backend implementation should populate. The UI
 * derives every aggregation (cost-over-time, by-model, per-template median/p90)
 * from these fixtures — see `mockData.ts` and the per-view selectors.
 *
 * NOTE: no backend telemetry exists yet; `mockData.ts` is the only producer.
 */

// ─── Enums / unions ──────────────────────────────────────────────────────────

export type TriggerType = 'user_chat' | 'user_manual' | 'auto_event' | 'auto_schedule';

export type Assistant = 'AI-SRE' | 'AI-FinOps' | 'AI-K8s' | 'AI-CloudOps' | 'Custom';

export type RunStatus = 'completed' | 'failed' | 'awaiting-approval' | 'cancelled';

export type Provider = 'Anthropic' | 'OpenAI' | 'Bedrock' | 'Google' | 'Ollama';

/** Statistical anomaly levels per spec §8. */
export type AnomalyLevel = 1 | 2 | 3;

/** Common drivers surfaced on every anomaly (spec §8). */
export type AnomalyDriver = 'token_spike' | 'retry_loop' | 'expensive_model' | 'agentic_loop' | 'long_output' | 'similar_run_multiple';

// ─── Model Call (atomic, billable unit) ──────────────────────────────────────

export interface ModelCall {
  callId: string;
  stepId: string;
  runId: string;
  model: string;
  provider: Provider;
  inputTokens: number;
  outputTokens: number;
  /** Rate snapshot at call time — rates change, so the row stores the rate used. */
  costPerInputToken: number;
  costPerOutputToken: number;
  /** input·rateIn + output·rateOut. */
  totalCost: number;
  /** Model round-trip time (ms). */
  latencyMs: number;
  cached: boolean;
  retry: boolean;
  error: boolean;
  timestamp: string;

  // ── Optional real-API diagnostics (populated by `adapt.ts` from the tree) ──
  /** Owning agent's name — which (possibly child) agent issued this call. */
  agentName?: string;
  /** Cached portion of `inputTokens` (cost lever). */
  cachedInputTokens?: number;
  /** Reasoning ("thinking") tokens — a hidden cost driver. */
  thinkingTokens?: number;
  /** Time-to-first-token, ms. */
  ttftMs?: number;
  /** Why generation stopped (e.g. `STOP`, `MAX_TOKENS` = truncation). */
  stopReason?: string;
  /** Backend error string when the call failed. */
  errorMessage?: string;
  /** Per-component cost split (input / cached / output / …), when available. */
  costBreakdown?: ModelCostBreakdown;
  /** True when a stored prompt/response trace exists — gates the "view prompt"
   * action. The trace text itself is fetched lazily on click, not carried here. */
  hasTrace?: boolean;
  /** Raw trace, populated only by the lazy by-id fetch (modal); undefined in the list. */
  promptMessages?: string;
  responseContent?: string;
}

// ─── Sub-task / Step ─────────────────────────────────────────────────────────

/** One tool invocation a task made (kubectl, shell, fetch_logs, …). */
export interface StepToolCall {
  name: string;
  /** Backend tool_type — typically "tool". */
  type: string;
  status: string;
  durationMs: number;
  /** Rich fields, present only from the per-agent detail endpoint. */
  parameters?: string;
  response?: string;
  thought?: string;
  isError?: boolean;
}

/** Per-component cost split for a model call (input / cached / output / …). */
export interface ModelCostBreakdown {
  tier: string;
  components: { kind: string; tokens: number; cost_usd: number }[];
}

export interface Step {
  stepId: string;
  runId: string;
  /** e.g. "Log Analysis Agent", "Kubectl Agent", "RCA", a conditional node. */
  agent: string;
  /** Ordering within the run. */
  sequence: number;
  /** Nesting for the trace/waterfall (null = top-level). */
  parentStepId: string | null;
  startedAt: string;
  endedAt: string;
  /** Non-LLM step time (e.g. running kubectl), ms. Distinct from model latency. */
  toolTimeMs: number;
  /** Time the step sat at an approval gate / waiting for user input, ms. */
  waitTimeMs: number;
  calls: ModelCall[];

  // Derived (precomputed in mockData for convenience).
  stepCost: number;
  stepLatencyMs: number;
  stepInputTokens: number;
  stepOutputTokens: number;

  // ── Optional real-API diagnostics (populated by `adapt.ts` from the tree) ──
  /** Number of model (LLM) calls in this task. */
  modelCallCount?: number;
  /** Number of tool calls (kubectl, shell, fetch_logs, …) the task issued. */
  toolCallCount?: number;
  /** The actual tool calls this task made (excludes sub-agent spawns). */
  tools?: StepToolCall[];
  /** How many model calls were retries (`retry_attempt > 0`). */
  retryCount?: number;
  /** Cached portion of `stepInputTokens` — drives the cache-hit %. */
  cachedInputTokens?: number;
  /** Cost of this task + all descendant tasks (`subtree_cost_usd` rollup). */
  subtreeCost?: number;
  /** Normalised task status. */
  status?: RunStatus;
  /** Raw backend status string (`success` / `error` / `fail` / `COMPLETED`). */
  statusRaw?: string;
  /** First backend error message on the task (when failed). */
  errorMessage?: string;
  /** Parent agent's name (from `parent_agent_id`) — "where did this come from". */
  parentAgentName?: string;
  /** Tool that spawned this task as a sub-agent (`tool_call.tool_type='agent'`). */
  invokedByTool?: string;
}

// ─── Conversation / Workflow Run ─────────────────────────────────────────────

export interface Run {
  runId: string;
  /** Human-readable run title. */
  title: string;
  /** The reusable definition — used for "similar conversation" comparison. */
  templateId: string;
  triggerType: TriggerType;
  /** Alert name, webhook, cron expression, user id, … */
  triggerSource: string;
  assistant: Assistant;
  tenantId: string;
  userId: string;
  cluster?: string;
  cloudAccount?: string;
  startedAt: string;
  endedAt: string;
  status: RunStatus;
  steps: Step[];

  // Derived (precomputed in mockData).
  totalCost: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  /** Sum of model-call latency (compute spent waiting on LLMs), ms. */
  totalModelLatencyMs: number;
  /** Time at approval gates / waiting for user input, ms. Shown separately. */
  totalWaitTimeMs: number;
  /** endedAt − startedAt, ms. */
  wallClockMs: number;
  modelsUsed: string[];
  anomalyFlag: boolean;

  // ── Optional real-API fields (populated by `adapt.ts` for API-backed rows) ──
  // The conversation-list endpoint is row-level: it carries no per-step tree, so
  // the selectors prefer these counts when present and fall back to `steps` for
  // mock runs. `sessionId`/`accountId` feed the detail (tree) fetch.
  /** session_id of the conversation — the id the detail actions expect. */
  sessionId?: string;
  /** account_id — needed to fetch the detail tree. */
  accountId?: string;
  /** Raw backend `source` (the trigger-type proxy); shown in place of `triggerType`. */
  source?: string;
  /** Model API calls (from the list row); avoids needing the per-step tree. */
  llmCallCount?: number;
  /** Distinct agents that produced LLM calls (from the list row). */
  agentCount?: number;
  /** Cached input tokens (from the list row) — for the "low cache hit" quick view. */
  cachedInputTokens?: number;
  /**
   * Per-model calls + cost from the list row's `model_breakdown` (cost-desc).
   * When present, the model-breakdown selector uses it directly instead of the
   * per-step tree — so the conversation list shows real per-model calls+cost
   * inline without a per-conversation fetch.
   */
  modelStats?: { model: string; calls: number; cost: number }[];
}

// ─── Anomaly (spec §8) ───────────────────────────────────────────────────────

export interface Anomaly {
  anomalyId: string;
  level: AnomalyLevel;
  driver: AnomalyDriver;
  /** Explainable, actionable message (e.g. "Daily cost $412 vs 28-day avg $180 (+129%)"). */
  message: string;
  /** Present for level-2/3 (single-run) anomalies. */
  runId?: string;
  /** The observed value (cost, token count, call count, …). */
  value: number;
  /** The baseline it was compared against. */
  baseline: number;
  timestamp: string;
}

// ─── Reports (spec §9) ───────────────────────────────────────────────────────

export type ReportCadence = 'daily' | 'weekly' | 'monthly';

// ─── Global filter state (spec §10) ──────────────────────────────────────────

export type Granularity = 'day' | 'week' | 'month';

/** Multi-model re-scope mode (spec §7c). */
export type ModelMatchMode = 'any' | 'all';

export interface CostFilters {
  startDate: string; // yyyy-mm-dd
  endDate: string; // yyyy-mm-dd
  granularity: Granularity;
  triggerTypes: TriggerType[];
  assistants: Assistant[];
  templates: string[];
  /** Backend `source` values (the trigger-type proxy) — an API-backed filter. */
  sources: string[];
  models: string[];
  /** Provider values — free strings once sourced from the API (e.g. "googleai"). */
  providers: string[];
  /** Status values — free strings once sourced from the API (e.g. "success"). */
  statuses: string[];
  minCost: number | null;
  maxCost: number | null;
  anomaliesOnly: boolean;
  modelMatchMode: ModelMatchMode;
}

// ─── Derived aggregation shapes (selectors) ──────────────────────────────────

export interface TimeBucket {
  /** Bucket label (e.g. "2026-05-12", "W20", "May"). */
  label: string;
  /** Series keyed by the active stack-by dimension → cost. */
  series: Record<string, number>;
  total: number;
}

export interface RankedSlice {
  key: string;
  cost: number;
  tokens: number;
  share: number; // 0..1
}

export interface ModelSummary {
  model: string;
  provider: Provider;
  totalCost: number;
  calls: number;
  conversations: number;
  avgCostPerCall: number;
  avgInputTokens: number;
  avgOutputTokens: number;
  avgLatencyMs: number;
  /** Per-bucket cost for the sparkline. */
  trend: number[];
}

/** A single run's position for the cost-vs-latency scatter. */
export interface ScatterPoint {
  runId: string;
  title: string;
  cost: number;
  latencyMs: number;
  model: string;
}
