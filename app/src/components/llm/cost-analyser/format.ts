/**
 * AI Cost Analyser — formatting + aggregation helpers (PROTOTYPE).
 *
 * Formatting wraps `@lib/formatter`'s `formatNumber` where useful; aggregation
 * selectors keep the fixtures (`mockData.ts`) as the single source of truth so
 * every screen reads from one derivation path.
 */
import { formatNumber } from '@lib/formatter';
import type { Anomaly, CostFilters, Granularity, ModelSummary, RankedSlice, Run, ScatterPoint, Step, TimeBucket } from './types';

// ─── Scalar formatting ───────────────────────────────────────────────────────

/** Currency, always rounded to the nearest 2nd decimal: $9.80 / $0.01 / $1.23K / $3.40M. */
export function fmtCost(amount: number | null | undefined): string {
  if (amount == null) return '—';
  const v = Math.abs(amount);
  if (v >= 1_000_000) return `$${(amount / 1_000_000).toFixed(2)}M`;
  if (v >= 1_000) return `$${(amount / 1_000).toFixed(2)}K`;
  return `$${amount.toFixed(2)}`;
}

/** Compact token count: 940 / 12.4K / 3.1M. */
export function fmtTokens(n: number | null | undefined): string {
  if (n == null) return '—';
  const v = Math.abs(n);
  if (v >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return formatNumber(n, '0', 0, 0);
}

/** Human duration from ms: 820ms / 4.2s / 3m 12s / 1h 04m. */
export function fmtDuration(ms: number | null | undefined): string {
  if (ms == null) return '—';
  if (ms < 1_000) return `${Math.round(ms)}ms`;
  const s = ms / 1_000;
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rem = Math.round(s % 60);
  if (m < 60) return `${m}m ${String(rem).padStart(2, '0')}s`;
  const h = Math.floor(m / 60);
  const remM = m % 60;
  return `${h}h ${String(remM).padStart(2, '0')}m`;
}

export function fmtPct(fraction: number | null | undefined, digits = 0): string {
  if (fraction == null) return '—';
  return `${(fraction * 100).toFixed(digits)}%`;
}

/** Signed delta percentage vs a baseline, e.g. +129% / -12%. */
export function deltaPct(value: number, baseline: number): number {
  if (!baseline) return 0;
  return Math.round(((value - baseline) / baseline) * 100);
}

export const triggerLabel: Record<string, string> = {
  user_chat: 'User chat',
  user_manual: 'User manual',
  auto_event: 'Auto · event',
  auto_schedule: 'Auto · schedule',
};

export const driverLabel: Record<string, string> = {
  token_spike: 'Token spike',
  retry_loop: 'Retry / error loop',
  expensive_model: 'Expensive model',
  agentic_loop: 'Agentic loop',
  long_output: 'Unusually long output',
  similar_run_multiple: 'High vs similar runs',
};

// ─── Date helpers (UTC, fixed — no Date.now reliance for determinism) ─────────

function toDate(s: string): Date {
  return new Date(s);
}

function dayKey(d: Date): string {
  return d.toISOString().slice(0, 10);
}

function weekKey(d: Date): string {
  // ISO-ish week number for bucketing (good enough for the prototype).
  const onejan = new Date(Date.UTC(d.getUTCFullYear(), 0, 1));
  const week = Math.ceil(((d.getTime() - onejan.getTime()) / 86_400_000 + onejan.getUTCDay() + 1) / 7);
  return `W${week}`;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function monthKey(d: Date): string {
  return `${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

export function bucketKey(s: string, g: Granularity): string {
  const d = toDate(s);
  if (g === 'day') return dayKey(d);
  if (g === 'week') return weekKey(d);
  return monthKey(d);
}

// ─── Filtering (spec §10 + §7c multi-model re-scope) ──────────────────────────

export function applyFilters(runs: Run[], f: CostFilters): Run[] {
  const start = f.startDate ? toDate(f.startDate).getTime() : -Infinity;
  const end = f.endDate ? toDate(f.endDate).getTime() + 86_400_000 : Infinity;

  return runs.filter((r) => {
    const t = toDate(r.startedAt).getTime();
    if (t < start || t > end) return false;
    if (f.triggerTypes.length && !f.triggerTypes.includes(r.triggerType)) return false;
    if (f.assistants.length && !f.assistants.includes(r.assistant)) return false;
    if (f.templates.length && !f.templates.includes(r.templateId)) return false;
    if (f.statuses.length && !f.statuses.includes(r.status)) return false;
    if (f.anomaliesOnly && !r.anomalyFlag) return false;
    if (f.minCost != null && r.totalCost < f.minCost) return false;
    if (f.maxCost != null && r.totalCost > f.maxCost) return false;

    if (f.providers.length) {
      const runProviders = new Set<string>(r.steps.flatMap((s) => s.calls.map((c) => c.provider)));
      if (!f.providers.some((p) => runProviders.has(p))) return false;
    }

    // §7c — selecting model(s) re-scopes to runs using "any of" / "all of".
    if (f.models.length) {
      if (f.modelMatchMode === 'all') {
        if (!f.models.every((m) => r.modelsUsed.includes(m))) return false;
      } else if (!f.models.some((m) => r.modelsUsed.includes(m))) return false;
    }
    return true;
  });
}

/**
 * Mock-only filter for the illustrative (un-API-backed) widgets.
 * Applies just the dimensions that exist in the mock fixtures — date window,
 * trigger, assistant, template, cost range, anomalies. The API-backed fields
 * (source / model / provider / status) carry real backend values that would not
 * match mock data, so they are intentionally ignored here.
 */
export function applyMockFilters(runs: Run[], f: CostFilters): Run[] {
  const start = f.startDate ? toDate(f.startDate).getTime() : -Infinity;
  const end = f.endDate ? toDate(f.endDate).getTime() + 86_400_000 : Infinity;

  return runs.filter((r) => {
    const t = toDate(r.startedAt).getTime();
    if (t < start || t > end) return false;
    if (f.triggerTypes.length && !f.triggerTypes.includes(r.triggerType)) return false;
    if (f.assistants.length && !f.assistants.includes(r.assistant)) return false;
    if (f.templates.length && !f.templates.includes(r.templateId)) return false;
    if (f.anomaliesOnly && !r.anomalyFlag) return false;
    if (f.minCost != null && r.totalCost < f.minCost) return false;
    if (f.maxCost != null && r.totalCost > f.maxCost) return false;
    return true;
  });
}

// ─── Aggregation selectors ────────────────────────────────────────────────────

export type StackBy = 'model' | 'trigger' | 'assistant' | 'template';

function stackKey(run: Run, by: StackBy): string {
  if (by === 'trigger') return triggerLabel[run.triggerType] ?? run.triggerType;
  if (by === 'assistant') return run.assistant;
  if (by === 'template') return run.templateId;
  return run.modelsUsed[0] ?? '—'; // dominant model for the run
}

/** Cost over time, stacked by a dimension (spec §4b). */
export function costOverTime(runs: Run[], g: Granularity, by: StackBy): { buckets: TimeBucket[]; keys: string[] } {
  const order: string[] = [];
  const map = new Map<string, TimeBucket>();
  const keySet = new Set<string>();

  for (const r of runs) {
    const bk = bucketKey(r.startedAt, g);
    if (!map.has(bk)) {
      map.set(bk, { label: bk, series: {}, total: 0 });
      order.push(bk);
    }
    const bucket = map.get(bk)!;
    const sk = stackKey(r, by);
    keySet.add(sk);
    bucket.series[sk] = (bucket.series[sk] ?? 0) + r.totalCost;
    bucket.total += r.totalCost;
  }

  const buckets = order.sort((a, b) => (a < b ? -1 : 1)).map((k) => map.get(k)!);
  return { buckets, keys: Array.from(keySet) };
}

/** Ranked cost share by an arbitrary key extractor (model / trigger / assistant). */
export function rankedBy(runs: Run[], keyOf: (r: Run) => string): RankedSlice[] {
  const cost = new Map<string, number>();
  const tokens = new Map<string, number>();
  for (const r of runs) {
    const k = keyOf(r);
    cost.set(k, (cost.get(k) ?? 0) + r.totalCost);
    tokens.set(k, (tokens.get(k) ?? 0) + r.totalInputTokens + r.totalOutputTokens);
  }
  const total = Array.from(cost.values()).reduce((a, b) => a + b, 0) || 1;
  return Array.from(cost.entries())
    .map(([key, c]) => ({ key, cost: c, tokens: tokens.get(key) ?? 0, share: c / total }))
    .sort((a, b) => b.cost - a.cost);
}

/** Per-model economics for the Models screen (spec §7a). */
export function modelSummaries(runs: Run[], g: Granularity): ModelSummary[] {
  interface Acc {
    provider: ModelSummary['provider'];
    totalCost: number;
    calls: number;
    convs: Set<string>;
    inTok: number;
    outTok: number;
    latency: number;
    byBucket: Map<string, number>;
  }
  const acc = new Map<string, Acc>();
  const allBuckets = new Set<string>();

  for (const r of runs) {
    const bk = bucketKey(r.startedAt, g);
    allBuckets.add(bk);
    for (const s of r.steps) {
      for (const c of s.calls) {
        if (!acc.has(c.model)) {
          acc.set(c.model, {
            provider: c.provider,
            totalCost: 0,
            calls: 0,
            convs: new Set(),
            inTok: 0,
            outTok: 0,
            latency: 0,
            byBucket: new Map(),
          });
        }
        const a = acc.get(c.model)!;
        a.totalCost += c.totalCost;
        a.calls += 1;
        a.convs.add(r.runId);
        a.inTok += c.inputTokens;
        a.outTok += c.outputTokens;
        a.latency += c.latencyMs;
        a.byBucket.set(bk, (a.byBucket.get(bk) ?? 0) + c.totalCost);
      }
    }
  }

  const bucketOrder = Array.from(allBuckets).sort((x, y) => (x < y ? -1 : 1));

  return Array.from(acc.entries())
    .map(([model, a]) => ({
      model,
      provider: a.provider,
      totalCost: a.totalCost,
      calls: a.calls,
      conversations: a.convs.size,
      avgCostPerCall: a.calls ? a.totalCost / a.calls : 0,
      avgInputTokens: a.calls ? a.inTok / a.calls : 0,
      avgOutputTokens: a.calls ? a.outTok / a.calls : 0,
      avgLatencyMs: a.calls ? a.latency / a.calls : 0,
      trend: bucketOrder.map((b) => a.byBucket.get(b) ?? 0),
    }))
    .sort((x, y) => y.totalCost - x.totalCost);
}

/** Cost-vs-latency scatter points, one per run (spec §7d). */
export function scatterPoints(runs: Run[]): ScatterPoint[] {
  return runs.map((r) => ({
    runId: r.runId,
    title: r.title,
    cost: r.totalCost,
    latencyMs: r.totalModelLatencyMs,
    model: r.modelsUsed[0] ?? '—',
  }));
}

// ─── Per-template distribution ("is this run normal for its type", §6e/§8) ────

export interface TemplateStats {
  median: number;
  p90: number;
  values: number[];
}

export function templateStats(runs: Run[], templateId: string): TemplateStats {
  const values = runs
    .filter((r) => r.templateId === templateId)
    .map((r) => r.totalCost)
    .sort((a, b) => a - b);
  if (!values.length) return { median: 0, p90: 0, values: [] };
  const at = (p: number) => values[Math.min(values.length - 1, Math.floor(values.length * p))];
  return { median: at(0.5), p90: at(0.9), values };
}

// ─── Per-run / per-step model breakdown (count + cost per model) ──────────────

export interface ModelStat {
  model: string;
  calls: number;
  cost: number;
}

function aggregateCalls(calls: { model: string; totalCost: number }[]): ModelStat[] {
  const map = new Map<string, ModelStat>();
  for (const c of calls) {
    const cur = map.get(c.model) ?? { model: c.model, calls: 0, cost: 0 };
    cur.calls += 1;
    cur.cost += c.totalCost;
    map.set(c.model, cur);
  }
  return Array.from(map.values()).sort((a, b) => b.cost - a.cost);
}

/** Models used in a run, each with its call count and cost. */
export function runModelBreakdown(run: Run): ModelStat[] {
  // API-backed list rows carry per-model calls+cost in `modelStats` (from the
  // list row's model_breakdown) — use it directly; no per-step tree needed.
  if (run.modelStats?.length) {
    return [...run.modelStats].sort((a, b) => b.cost - a.cost);
  }
  // Otherwise fall back to the names the row reports (no call/cost split).
  if (!run.steps.length && run.modelsUsed.length) {
    return run.modelsUsed.map((model) => ({ model, calls: 0, cost: 0 }));
  }
  return aggregateCalls(run.steps.flatMap((s) => s.calls));
}

/** Models used in a single step, each with its call count and cost. */
export function stepModelBreakdown(step: Step): ModelStat[] {
  return aggregateCalls(step.calls);
}

/** Non-LLM tool/execution time for a run (sum of step tool time), ms. */
export function runToolTimeMs(run: Run): number {
  return run.steps.reduce((a, s) => a + s.toolTimeMs, 0);
}

/** Total number of model calls in a run. */
export function runCallCount(run: Run): number {
  if (run.llmCallCount != null) return run.llmCallCount;
  return run.steps.reduce((a, s) => a + s.calls.length, 0);
}

/** Tenant-wide totals per model: cost, call count, tokens. */
export function modelTotals(runs: Run[]): { model: string; cost: number; calls: number; tokens: number }[] {
  const map = new Map<string, { model: string; cost: number; calls: number; tokens: number }>();
  for (const r of runs) {
    for (const s of r.steps) {
      for (const c of s.calls) {
        const cur = map.get(c.model) ?? { model: c.model, cost: 0, calls: 0, tokens: 0 };
        cur.cost += c.totalCost;
        cur.calls += 1;
        cur.tokens += c.inputTokens + c.outputTokens;
        map.set(c.model, cur);
      }
    }
  }
  return Array.from(map.values()).sort((a, b) => b.cost - a.cost);
}

// ─── Calls-per-model over time (line chart) ───────────────────────────────────

export function callsOverTime(runs: Run[], g: Granularity): { buckets: TimeBucket[]; keys: string[] } {
  const order: string[] = [];
  const map = new Map<string, TimeBucket>();
  const keySet = new Set<string>();

  for (const r of runs) {
    const bk = bucketKey(r.startedAt, g);
    if (!map.has(bk)) {
      map.set(bk, { label: bk, series: {}, total: 0 });
      order.push(bk);
    }
    const bucket = map.get(bk)!;
    for (const s of r.steps) {
      for (const c of s.calls) {
        keySet.add(c.model);
        bucket.series[c.model] = (bucket.series[c.model] ?? 0) + 1;
        bucket.total += 1;
      }
    }
  }
  const buckets = order.sort((a, b) => (a < b ? -1 : 1)).map((k) => map.get(k)!);
  return { buckets, keys: Array.from(keySet) };
}

// ─── Per-bucket × per-model matrix (cost + calls) ─────────────────────────────
// Powers the cost-over-time table view and the Reports weekly-by-models table.

export interface MatrixCell {
  cost: number;
  calls: number;
}

export interface MatrixBucket {
  label: string;
  byModel: Record<string, MatrixCell>;
  total: number;
}

export function modelMatrixOverTime(runs: Run[], g: Granularity): { buckets: MatrixBucket[]; models: string[] } {
  const order: string[] = [];
  const map = new Map<string, MatrixBucket>();
  const modelSet = new Set<string>();

  for (const r of runs) {
    const bk = bucketKey(r.startedAt, g);
    if (!map.has(bk)) {
      map.set(bk, { label: bk, byModel: {}, total: 0 });
      order.push(bk);
    }
    const bucket = map.get(bk)!;
    for (const s of r.steps) {
      for (const c of s.calls) {
        modelSet.add(c.model);
        const cell = bucket.byModel[c.model] ?? { cost: 0, calls: 0 };
        cell.cost += c.totalCost;
        cell.calls += 1;
        bucket.byModel[c.model] = cell;
        bucket.total += c.totalCost;
      }
    }
  }
  const buckets = order.sort((a, b) => (a < b ? -1 : 1)).map((k) => map.get(k)!);
  const models = Array.from(modelSet);
  // Order models by total cost desc for stable, meaningful columns.
  const totals = new Map<string, number>();
  for (const b of buckets) for (const m of models) totals.set(m, (totals.get(m) ?? 0) + (b.byModel[m]?.cost ?? 0));
  models.sort((a, b) => (totals.get(b) ?? 0) - (totals.get(a) ?? 0));
  return { buckets, models };
}

// ─── KPI totals ───────────────────────────────────────────────────────────────

export interface KpiTotals {
  totalCost: number;
  runs: number;
  avgCostPerRun: number;
  inputTokens: number;
  outputTokens: number;
  avgLatencyMs: number;
  openAnomalies: number;
}

export function kpiTotals(runs: Run[], anomalies: Anomaly[]): KpiTotals {
  const totalCost = runs.reduce((a, r) => a + r.totalCost, 0);
  const inputTokens = runs.reduce((a, r) => a + r.totalInputTokens, 0);
  const outputTokens = runs.reduce((a, r) => a + r.totalOutputTokens, 0);
  const latency = runs.reduce((a, r) => a + r.totalModelLatencyMs, 0);
  const runIds = new Set(runs.map((r) => r.runId));
  return {
    totalCost,
    runs: runs.length,
    avgCostPerRun: runs.length ? totalCost / runs.length : 0,
    inputTokens,
    outputTokens,
    avgLatencyMs: runs.length ? latency / runs.length : 0,
    openAnomalies: anomalies.filter((a) => !a.runId || runIds.has(a.runId)).length,
  };
}

// ─── CSV export (client-side) ─────────────────────────────────────────────────

export function runsToCsv(runs: Run[]): string {
  const head = [
    'run_id',
    'title',
    'template',
    'trigger',
    'assistant',
    'user',
    'started_at',
    'wall_clock_ms',
    'wait_time_ms',
    'total_cost',
    'input_tokens',
    'output_tokens',
    'models',
    'status',
    'anomaly',
  ];
  const rows = runs.map((r) =>
    [
      r.runId,
      r.title,
      r.templateId,
      r.triggerType,
      r.assistant,
      r.userId,
      r.startedAt,
      r.wallClockMs,
      r.totalWaitTimeMs,
      r.totalCost.toFixed(6),
      r.totalInputTokens,
      r.totalOutputTokens,
      r.modelsUsed.join('|'),
      r.status,
      r.anomalyFlag ? 'yes' : 'no',
    ]
      .map((v) => `"${String(v).replace(/"/g, '""')}"`)
      .join(',')
  );
  return [head.join(','), ...rows].join('\n');
}

/** Trigger a client-side file download (no-op during SSR). */
export function downloadFile(filename: string, content: string, mime = 'text/csv'): void {
  if (typeof document === 'undefined') return;
  const blob = new Blob([content], { type: mime });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}
