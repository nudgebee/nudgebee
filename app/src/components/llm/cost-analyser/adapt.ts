/**
 * AI Cost Analyser — API → UI adapters.
 *
 * Maps the shipped backend DTOs (`@api1/ai-cost`) onto the UI's `types.ts` shapes
 * so the existing (mock-era) components render real data unchanged:
 *
 *   - `rowToRun`        ConversationCostRow → shallow Run (no per-step tree; the
 *                       list endpoint carries only row-level rollups).
 *   - `treeToRun`       ConversationTree   → full Run with steps + model calls,
 *                       reconstructed from the flat arrays (detail drill-down).
 *   - `groupRowsToSlices` / `groupRowsToModelSlices` — breakdown rows → chart slices.
 *   - `totalsToKpi`     UsageTotals        → the KPI strip shape.
 *
 * What the API can't back (per-model rates, wait time, anomalies, templates,
 * assistant, trigger-type) is left at neutral defaults; those widgets stay on
 * mock data elsewhere.
 */
import type {
  AgentModelCall,
  ConversationAgentDetail,
  ConversationCostRow,
  ConversationTree,
  TreeAgent,
  TreeModelCall,
  UsageGroupRow,
  UsageStackDimension,
  UsageTimeSeries,
  UsageTimeSeriesRow,
  UsageTotals,
} from '@api1/ai-cost';
import type { ModelSlice } from './components/BreakdownWidgets';
import { bucketKey, type KpiTotals } from './format';
import type { Granularity, ModelCall, Provider, RankedSlice, Run, RunStatus, Step, StepToolCall, TimeBucket } from './types';

const S_TO_MS = 1000;
const DAY_MS = 86_400_000;

/**
 * Real per-token $ rates for the input/output columns. When the backend supplies
 * a cost breakdown, split it by kind (input-side = input + cached_input +
 * cache_creation, output-side = output + thinking) so the two columns show
 * genuinely distinct rates. Without a breakdown, fall back to the blended rate
 * (cost / all tokens) in both — no worse than before, just not split.
 */
function perTokenRates(
  mc: { cost_usd?: number; cost_breakdown?: { components?: { kind: string; cost_usd: number }[] } },
  inTok: number,
  outTok: number
): { costPerInputToken: number; costPerOutputToken: number } {
  const comps = mc.cost_breakdown?.components;
  if (comps && comps.length) {
    const sumKinds = (kinds: string[]) => comps.filter((c) => kinds.includes(c.kind)).reduce((s, c) => s + (c.cost_usd ?? 0), 0);
    const inputCost = sumKinds(['input', 'cached_input', 'cache_creation']);
    const outputCost = sumKinds(['output', 'thinking']);
    return { costPerInputToken: inTok ? inputCost / inTok : 0, costPerOutputToken: outTok ? outputCost / outTok : 0 };
  }
  const blended = (mc.cost_usd ?? 0) / (inTok + outTok || 1);
  return { costPerInputToken: inTok ? blended : 0, costPerOutputToken: outTok ? blended : 0 };
}

/** Map a raw backend status string to the UI's run-status enum. */
export function toRunStatus(raw: string | undefined): RunStatus {
  const s = (raw ?? '').toLowerCase();
  if (s.includes('fail') || s.includes('error')) return 'failed';
  if (s.includes('cancel') || s.includes('abort')) return 'cancelled';
  if (s.includes('await') || s.includes('approval') || s.includes('pending') || s.includes('progress')) return 'awaiting-approval';
  return 'completed';
}

// ─── Conversation list row → shallow Run ──────────────────────────────────────

export function rowToRun(row: ConversationCostRow): Run {
  return {
    runId: row.session_id || row.conversation_id,
    title: row.title || row.session_id || '(untitled conversation)',
    templateId: '',
    triggerType: 'user_chat',
    triggerSource: row.source || '—',
    assistant: 'Custom',
    tenantId: '',
    userId: row.user_id,
    startedAt: row.started_at,
    endedAt: row.ended_at,
    status: toRunStatus(row.status),
    steps: [],
    totalCost: row.cost_usd ?? 0,
    totalInputTokens: row.input_tokens ?? 0,
    totalOutputTokens: row.output_tokens ?? 0,
    cachedInputTokens: row.cached_input_tokens ?? 0,
    totalModelLatencyMs: (row.model_latency_seconds ?? 0) * S_TO_MS,
    totalWaitTimeMs: 0,
    wallClockMs: (row.wall_clock_seconds ?? 0) * S_TO_MS,
    modelsUsed: row.models_used ?? [],
    anomalyFlag: false,
    // real-data extras
    sessionId: row.session_id,
    accountId: row.account_id,
    source: row.source,
    llmCallCount: row.llm_call_count,
    agentCount: row.agent_count,
    modelStats: (row.model_breakdown ?? []).map((m) => ({ model: m.model, calls: m.calls, cost: m.cost_usd })),
  };
}

// ─── Conversation tree → full Run (steps + model calls) ───────────────────────

function modelCallFrom(mc: TreeModelCall, stepId: string, runId: string, agentName: string): ModelCall {
  const inTok = mc.input_tokens ?? 0;
  const outTok = mc.output_tokens ?? 0;
  // Rates aren't returned (cost is computed on-read); approximate per-token rate
  // from the call's own cost so the detail table's $/tok columns aren't zeroed.
  return {
    callId: mc.id,
    stepId,
    runId,
    agentName,
    model: mc.model,
    provider: (mc.provider as Provider) ?? ('—' as Provider),
    inputTokens: inTok,
    outputTokens: outTok,
    ...perTokenRates(mc, inTok, outTok),
    totalCost: mc.cost_usd ?? 0,
    latencyMs: (mc.latency_seconds ?? 0) * S_TO_MS,
    cached: !!mc.is_cache_hit,
    retry: (mc.retry_attempt ?? 0) > 0,
    error: (mc.request_status && mc.request_status.toLowerCase() !== 'success') || !!mc.error_message,
    timestamp: mc.created_at,
    cachedInputTokens: mc.cached_input_tokens ?? 0,
    thinkingTokens: mc.thinking_tokens ?? 0,
    ttftMs: mc.ttft_ms ?? 0,
    stopReason: mc.stop_reason || '',
    errorMessage: mc.error_message || '',
  };
}

interface StepExtra {
  toolCallCount: number;
  tools: StepToolCall[];
  parentAgentName?: string;
  invokedByTool?: string;
}

function stepFromAgent(agent: TreeAgent, sequence: number, runId: string, calls: ModelCall[], toolTimeMs: number, extra: StepExtra): Step {
  // Prefer the agent's own aggregates (the live tree carries them); fall back to
  // summing in-tree model calls for the legacy/demo fixture shape.
  const sumIn = calls.reduce((a, c) => a + c.inputTokens, 0);
  const sumOut = calls.reduce((a, c) => a + c.outputTokens, 0);
  const sumCost = calls.reduce((a, c) => a + c.totalCost, 0);
  const sumLatency = calls.reduce((a, c) => a + c.latencyMs, 0);
  const cachedInputTokens = calls.reduce((a, c) => a + (c.cachedInputTokens ?? 0), 0);
  const failedCall = calls.find((c) => c.errorMessage);
  return {
    stepId: agent.id,
    runId,
    agent: agent.agent_name || 'agent',
    sequence,
    parentStepId: agent.parent_agent_id || null,
    startedAt: agent.created_at,
    endedAt: agent.updated_at,
    toolTimeMs,
    waitTimeMs: 0,
    calls,
    stepCost: agent.cost_usd ?? sumCost,
    stepLatencyMs: (agent.model_latency_seconds ?? 0) * S_TO_MS || sumLatency,
    stepInputTokens: agent.input_tokens ?? sumIn,
    stepOutputTokens: agent.output_tokens ?? sumOut,
    modelCallCount: agent.model_call_count ?? calls.length,
    toolCallCount: agent.tool_call_count ?? extra.toolCallCount,
    tools: extra.tools,
    retryCount: calls.filter((c) => c.retry).length,
    cachedInputTokens,
    subtreeCost: agent.subtree_cost_usd ?? agent.cost_usd ?? sumCost,
    status: toRunStatus(agent.status),
    statusRaw: agent.status || '',
    errorMessage: failedCall?.errorMessage || '',
    parentAgentName: extra.parentAgentName,
    invokedByTool: extra.invokedByTool,
  };
}

export function treeToRun(tree: ConversationTree): Run {
  const c = tree.conversation;
  const runId = c.session_id || c.conversation_id;

  const agentNameById = new Map<string, string>((tree.agents ?? []).map((a) => [a.id, a.agent_name || 'agent']));

  // Model calls grouped by their owning agent (orphans → "" bucket). Each call is
  // stamped with its owning agent name so a parent's drill-down can show which
  // (child) agent issued it.
  const callsByAgent = new Map<string, ModelCall[]>();
  for (const mc of tree.model_calls ?? []) {
    const key = mc.agent_id || '';
    const ownerName = key ? agentNameById.get(key) || 'agent' : 'Setup';
    const list = callsByAgent.get(key) ?? [];
    list.push(modelCallFrom(mc, key || 'setup', runId, ownerName));
    callsByAgent.set(key, list);
  }

  // Per-agent tool aggregates + the parent/child lineage links.
  //  - toolMsByAgent: total tool duration (overhead, not token cost).
  //  - toolCountByAgent: real tool calls (tool_type !== 'agent'); the 'agent'
  //    type is a sub-agent spawn, surfaced via invokedByTool instead.
  //  - childToParent: child_agent_id → the tool call that spawned it (R5 lineage).
  const toolMsByAgent = new Map<string, number>();
  const toolsByAgent = new Map<string, StepToolCall[]>();
  const childToParent = new Map<string, { parentAgentId: string; toolName: string }>();
  for (const tc of tree.tool_calls ?? []) {
    if (tc.agent_id) {
      toolMsByAgent.set(tc.agent_id, (toolMsByAgent.get(tc.agent_id) ?? 0) + (tc.duration_seconds ?? 0) * S_TO_MS);
      if ((tc.tool_type ?? '').toLowerCase() !== 'agent') {
        const list = toolsByAgent.get(tc.agent_id) ?? [];
        list.push({
          name: tc.tool_name || 'tool',
          type: tc.tool_type || 'tool',
          status: tc.status || '',
          durationMs: (tc.duration_seconds ?? 0) * S_TO_MS,
        });
        toolsByAgent.set(tc.agent_id, list);
      }
    }
    if (tc.child_agent_id) childToParent.set(tc.child_agent_id, { parentAgentId: tc.agent_id || '', toolName: tc.tool_name || '' });
  }

  const agents = [...(tree.agents ?? [])].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
  const steps: Step[] = agents.map((a, i) => {
    const link = childToParent.get(a.id);
    const parentId = a.parent_agent_id || link?.parentAgentId || '';
    const tools = toolsByAgent.get(a.id) ?? [];
    return stepFromAgent(a, i + 1, runId, callsByAgent.get(a.id) ?? [], toolMsByAgent.get(a.id) ?? 0, {
      toolCallCount: tools.length,
      tools,
      parentAgentName: parentId ? agentNameById.get(parentId) : undefined,
      invokedByTool: link?.toolName || undefined,
    });
  });

  // Orphan model calls (no agent_id) → a single synthetic setup step.
  const orphans = callsByAgent.get('') ?? [];
  if (orphans.length) {
    const inTok = orphans.reduce((a, c) => a + c.inputTokens, 0);
    const outTok = orphans.reduce((a, c) => a + c.outputTokens, 0);
    steps.push({
      stepId: `${runId}-setup`,
      runId,
      agent: 'Setup / classification',
      sequence: steps.length + 1,
      parentStepId: null,
      startedAt: orphans[0]?.timestamp ?? c.started_at,
      endedAt: orphans[orphans.length - 1]?.timestamp ?? c.ended_at,
      toolTimeMs: 0,
      waitTimeMs: 0,
      calls: orphans,
      stepCost: orphans.reduce((a, x) => a + x.totalCost, 0),
      stepLatencyMs: orphans.reduce((a, x) => a + x.latencyMs, 0),
      stepInputTokens: inTok,
      stepOutputTokens: outTok,
      modelCallCount: orphans.length,
      toolCallCount: 0,
      tools: [],
      retryCount: orphans.filter((x) => x.retry).length,
      cachedInputTokens: orphans.reduce((a, x) => a + (x.cachedInputTokens ?? 0), 0),
      status: 'completed',
      statusRaw: '',
      errorMessage: orphans.find((x) => x.errorMessage)?.errorMessage || '',
    });
  }

  const modelsUsed = Array.from(new Set((tree.model_calls ?? []).map((m) => m.model).filter(Boolean)));

  return {
    runId,
    title: c.title || runId,
    templateId: '',
    triggerType: 'user_chat',
    triggerSource: c.source || '—',
    assistant: 'Custom',
    tenantId: '',
    userId: '',
    startedAt: c.started_at,
    endedAt: c.ended_at,
    status: toRunStatus(c.status),
    steps,
    totalCost: c.total_cost_usd ?? 0,
    totalInputTokens: c.total_input_tokens ?? 0,
    totalOutputTokens: c.total_output_tokens ?? 0,
    totalModelLatencyMs: (c.model_latency_seconds ?? 0) * S_TO_MS,
    totalWaitTimeMs: 0,
    wallClockMs: (c.wall_clock_seconds ?? 0) * S_TO_MS,
    modelsUsed,
    anomalyFlag: false,
    sessionId: c.session_id,
    source: c.source,
    llmCallCount: c.model_call_count,
    agentCount: c.agent_count,
  };
}

// ─── Agent detail (ai_get_conversation_agent) → UI shapes ─────────────────────

export interface AgentDetail {
  query: string;
  thought: string;
  response: string;
  calls: ModelCall[];
  tools: StepToolCall[];
}

function agentModelCallToUi(mc: AgentModelCall, runId: string, agentName: string): ModelCall {
  const inTok = mc.input_tokens ?? 0;
  const outTok = mc.output_tokens ?? 0;
  return {
    callId: mc.id,
    stepId: '',
    runId,
    agentName,
    model: mc.model,
    provider: (mc.provider as Provider) ?? ('—' as Provider),
    inputTokens: inTok,
    outputTokens: outTok,
    ...perTokenRates(mc, inTok, outTok),
    totalCost: mc.cost_usd ?? 0,
    latencyMs: (mc.latency_seconds ?? 0) * S_TO_MS,
    cached: !!mc.is_cache_hit,
    retry: (mc.retry_attempt ?? 0) > 0,
    error: !!mc.is_error || !!mc.error_message,
    timestamp: mc.created_at,
    cachedInputTokens: mc.cached_input_tokens ?? 0,
    thinkingTokens: mc.thinking_tokens ?? 0,
    ttftMs: mc.ttft_ms ?? 0,
    stopReason: mc.stop_reason || '',
    errorMessage: mc.error_message || '',
    costBreakdown: mc.cost_breakdown
      ? {
          tier: mc.cost_breakdown.tier,
          components: (mc.cost_breakdown.components ?? []).map((x) => ({ kind: x.kind, tokens: x.tokens, cost_usd: x.cost_usd })),
        }
      : undefined,
    hasTrace: !!mc.has_trace,
    promptMessages: mc.prompt_messages || undefined,
    responseContent: mc.response_content || undefined,
  };
}

/** ai_get_conversation_agent payload → the UI's model-call + tool-call rows. */
export function adaptAgentDetail(d: ConversationAgentDetail, runId: string): AgentDetail {
  const name = d.agent?.agent_name || 'agent';
  return {
    query: d.agent?.query || '',
    thought: d.agent?.thought || '',
    response: d.agent?.response || '',
    calls: (d.model_calls ?? []).map((mc) => agentModelCallToUi(mc, runId, name)),
    tools: (d.tool_calls ?? []).map((tc) => ({
      name: tc.tool_name || 'tool',
      type: tc.tool_type || 'tool',
      status: tc.status || '',
      durationMs: (tc.duration_seconds ?? 0) * S_TO_MS,
      parameters: tc.parameters || '',
      response: tc.response || '',
      thought: tc.thought || '',
      isError: !!tc.is_error,
    })),
  };
}

// ─── Breakdown rows → chart slices ────────────────────────────────────────────

export function groupRowsToSlices(rows: UsageGroupRow[] | undefined): RankedSlice[] {
  const list = rows ?? [];
  const total = list.reduce((a, r) => a + (r.cost_usd ?? 0), 0) || 1;
  return list
    .map((r) => ({
      key: r.key || '—',
      cost: r.cost_usd ?? 0,
      tokens: (r.input_tokens ?? 0) + (r.output_tokens ?? 0),
      share: (r.cost_usd ?? 0) / total,
    }))
    .sort((a, b) => b.cost - a.cost);
}

export function groupRowsToModelSlices(rows: UsageGroupRow[] | undefined, topN = 7): ModelSlice[] {
  const list = rows ?? [];
  const total = list.reduce((a, r) => a + (r.cost_usd ?? 0), 0) || 1;
  return list
    .slice()
    .sort((a, b) => (b.cost_usd ?? 0) - (a.cost_usd ?? 0))
    .slice(0, topN)
    .map((r) => ({
      key: r.key || '—',
      cost: r.cost_usd ?? 0,
      calls: r.requests ?? 0,
      tokens: (r.input_tokens ?? 0) + (r.output_tokens ?? 0),
      share: (r.cost_usd ?? 0) / total,
    }));
}

export function totalsToKpi(t: UsageTotals | undefined): KpiTotals {
  const totalCost = t?.total_cost_usd ?? 0;
  const runs = t?.total_tasks ?? 0;
  return {
    totalCost,
    runs,
    avgCostPerRun: runs ? totalCost / runs : 0,
    inputTokens: t?.total_input_tokens ?? 0,
    outputTokens: t?.total_output_tokens ?? 0,
    // No per-run latency average in totals; left at 0 (latency lives per-row/per-model).
    avgLatencyMs: 0,
    openAnomalies: 0,
  };
}

// ─── Over-time series (cost / calls) ──────────────────────────────────────────

/** Stacked-chart legibility cap; series beyond this (by total) fold into "Other". */
const MAX_SERIES = 8;

/**
 * Ordered, de-duped bucket labels spanning [startDate, endDate] at granularity g.
 * Walks day-by-day and collapses each day to its granularity label (via the same
 * `bucketKey` the rows use), so day/week/month are handled uniformly and flat
 * periods still appear as zero buckets rather than being dropped.
 */
function bucketLabels(startDate: string, endDate: string, g: Granularity): string[] {
  const labels: string[] = [];
  const seen = new Set<string>();
  let ms = Date.parse(`${startDate}T00:00:00Z`);
  const end = Date.parse(`${endDate}T00:00:00Z`);
  if (Number.isNaN(ms) || Number.isNaN(end)) return labels;
  for (let guard = 0; ms <= end && guard < 1000; guard++, ms += DAY_MS) {
    const label = bucketKey(new Date(ms).toISOString(), g);
    if (!seen.has(label)) {
      seen.add(label);
      labels.push(label);
    }
  }
  return labels;
}

/**
 * Adapt the backend over-time series into the `{ buckets, keys }` shape the
 * BarSeries / LineSeries components consume, for one stack dimension and one
 * metric. Buckets are gap-filled across the full date range; series beyond
 * MAX_SERIES roll into an "Other" key so stacked charts stay legible.
 */
export function timeSeriesToChart(
  ts: UsageTimeSeries | null | undefined,
  dim: UsageStackDimension,
  metric: 'cost' | 'calls',
  startDate: string,
  endDate: string,
  g: Granularity
): { buckets: TimeBucket[]; keys: string[] } {
  const rows: UsageTimeSeriesRow[] = ts?.by_dimension?.[dim] ?? [];
  const valueOf = (r: UsageTimeSeriesRow) => (metric === 'cost' ? r.cost_usd : r.requests);

  // Rank stack keys by total; keep the top MAX_SERIES, fold the rest into "Other".
  const totals = new Map<string, number>();
  for (const r of rows) totals.set(r.key, (totals.get(r.key) ?? 0) + valueOf(r));
  const ranked = Array.from(totals.entries()).sort((a, b) => b[1] - a[1]);
  const topKeys = ranked.slice(0, MAX_SERIES).map(([k]) => k);
  const top = new Set(topKeys);
  const hasOther = ranked.length > MAX_SERIES;
  const keyFor = (k: string) => (top.has(k) ? k : 'Other');

  const labels = bucketLabels(startDate, endDate, g);
  const byLabel = new Map<string, TimeBucket>();
  for (const label of labels) byLabel.set(label, { label, series: {}, total: 0 });

  for (const r of rows) {
    const bucket = byLabel.get(bucketKey(r.bucket, g));
    const v = valueOf(r);
    if (!bucket || !v) continue;
    const sk = keyFor(r.key);
    bucket.series[sk] = (bucket.series[sk] ?? 0) + v;
    bucket.total += v;
  }

  const keys = hasOther ? [...topKeys, 'Other'] : topKeys;
  return { buckets: labels.map((l) => byLabel.get(l)!), keys };
}

/** Flatten {buckets, keys} into the generic `Chart.TimeSeries` shape ({labels, series}). */
export function bucketsToSeries(buckets: TimeBucket[], keys: string[]): { labels: string[]; series: { key: string; data: number[] }[] } {
  return {
    labels: buckets.map((b) => b.label),
    series: keys.map((k) => ({ key: k, data: buckets.map((b) => b.series[k] ?? 0) })),
  };
}
