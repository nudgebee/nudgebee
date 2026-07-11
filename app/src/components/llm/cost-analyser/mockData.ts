/**
 * AI Cost Analyser — deterministic mock fixtures (PROTOTYPE ONLY).
 *
 * No backend cost-telemetry exists yet; this module synthesises a realistic
 * tenant → run → step → model-call dataset so all five screens are fully
 * clickable. It is intentionally deterministic (seeded PRNG, fixed anchor date)
 * so the UI renders identically on every load — and so it doubles as a concrete
 * example payload for whoever implements the real telemetry pipeline.
 *
 * Swap-out path: replace `mockRuns` / `mockAnomalies` with data from an
 * `api1/ai-cost` module returning the same `types.ts` shapes.
 */
import type { Anomaly, Assistant, ModelCall, Provider, Run, RunStatus, Step, TriggerType } from './types';

// Fixed "now" so wall-clock math is stable (today, per the prototype brief).
const ANCHOR = Date.parse('2026-06-01T12:00:00Z');
const DAY = 86_400_000;

// ─── Seeded PRNG (mulberry32) — deterministic, no Math.random ─────────────────

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rng = mulberry32(20260601);
const rand = (min: number, max: number) => min + rng() * (max - min);
const randInt = (min: number, max: number) => Math.floor(rand(min, max + 1));
const pick = <T>(arr: T[]): T => arr[randInt(0, arr.length - 1)];

// ─── Model rate card (USD per token). Self-hosted = $0 imputed (spec note). ───

interface ModelRate {
  model: string;
  provider: Provider;
  in: number;
  out: number;
}

const MODEL_RATES: Record<string, ModelRate> = {
  'claude-opus-4': { model: 'claude-opus-4', provider: 'Anthropic', in: 0.000015, out: 0.000075 },
  'claude-haiku-4': { model: 'claude-haiku-4', provider: 'Anthropic', in: 0.0000008, out: 0.000004 },
  'gpt-4o': { model: 'gpt-4o', provider: 'OpenAI', in: 0.0000025, out: 0.00001 },
  'gemini-2.5-pro': { model: 'gemini-2.5-pro', provider: 'Google', in: 0.00000125, out: 0.000005 },
  'titan-text-bedrock': { model: 'titan-text-bedrock', provider: 'Bedrock', in: 0.0000008, out: 0.0000016 },
  'llama-3-70b-self': { model: 'llama-3-70b-self', provider: 'Ollama', in: 0, out: 0 },
};

// ─── Templates (the reusable workflow/chat definitions) ───────────────────────

interface StepDef {
  agent: string;
  /** Approval/human gate — contributes wait time, not latency. */
  gate?: boolean;
  /** Non-LLM tool execution (kubectl, API call) — contributes tool time. */
  tool?: boolean;
}

interface TemplateDef {
  id: string;
  assistant: Assistant;
  trigger: TriggerType;
  source: string;
  steps: StepDef[];
  /** Frontier + SLM mix the template tends to use. */
  models: string[];
  cluster?: string;
  cloudAccount?: string;
}

const TEMPLATES: TemplateDef[] = [
  {
    id: 'P1 Incident RCA',
    assistant: 'AI-SRE',
    trigger: 'auto_event',
    source: 'alert: HighErrorRate',
    steps: [
      { agent: 'Triage Classifier' },
      { agent: 'Log Analysis Agent' },
      { agent: 'Kubectl Agent', tool: true },
      { agent: 'Prometheus Agent', tool: true },
      { agent: 'RCA Reasoner' },
      { agent: 'Summary' },
    ],
    models: ['claude-haiku-4', 'claude-opus-4', 'gpt-4o'],
    cluster: 'prod-us-east',
  },
  {
    id: 'Cost Autopilot',
    assistant: 'AI-FinOps',
    trigger: 'auto_schedule',
    source: 'cron: 0 6 * * *',
    steps: [
      { agent: 'Fetch Cost Data', tool: true },
      { agent: 'Rightsizing Analysis' },
      { agent: 'Recommendation' },
      { agent: 'Approval Gate', gate: true },
      { agent: 'Apply' },
    ],
    models: ['claude-haiku-4', 'gemini-2.5-pro'],
    cloudAccount: 'aws-prod-1',
  },
  {
    id: 'SSL Monitor',
    assistant: 'AI-CloudOps',
    trigger: 'auto_schedule',
    source: 'cron: 0 */6 * * *',
    steps: [{ agent: 'Check Certs', tool: true }, { agent: 'Classify Risk' }, { agent: 'Notify' }],
    models: ['claude-haiku-4', 'titan-text-bedrock'],
    cloudAccount: 'aws-prod-1',
  },
  {
    id: 'K8s Scaling Advisor',
    assistant: 'AI-K8s',
    trigger: 'auto_event',
    source: 'drift: replica-pressure',
    steps: [{ agent: 'Metrics Collector', tool: true }, { agent: 'Forecast' }, { agent: 'Scaling Plan' }],
    models: ['llama-3-70b-self', 'claude-opus-4'],
    cluster: 'prod-eu-west',
  },
  {
    id: 'Ad-hoc SRE Chat',
    assistant: 'AI-SRE',
    trigger: 'user_chat',
    source: 'user: priya',
    steps: [{ agent: 'Chat Reasoner' }],
    models: ['claude-opus-4', 'gpt-4o'],
    cluster: 'prod-us-east',
  },
  {
    id: 'Manual Investigation',
    assistant: 'AI-SRE',
    trigger: 'user_manual',
    source: 'user: dan',
    steps: [{ agent: 'Context Builder' }, { agent: 'Log Analysis Agent' }, { agent: 'RCA Reasoner' }],
    models: ['claude-haiku-4', 'claude-opus-4'],
    cluster: 'prod-us-east',
  },
];

const USERS = ['priya', 'dan', 'aisha', 'marco', 'lena'];
const STATUSES: RunStatus[] = ['completed', 'completed', 'completed', 'completed', 'failed', 'awaiting-approval', 'cancelled'];

// ─── Builders ─────────────────────────────────────────────────────────────────

let callSeq = 0;

interface AnomalyInjection {
  tokenSpike?: boolean;
  retryLoop?: boolean;
  expensiveModel?: boolean;
  agenticLoop?: boolean;
}

function buildModelCall(stepId: string, runId: string, tsMs: number, model: string, inject: AnomalyInjection): ModelCall {
  const rate = MODEL_RATES[inject.expensiveModel ? 'claude-opus-4' : model] ?? MODEL_RATES['claude-haiku-4'];
  let inputTokens = randInt(400, 3500);
  let outputTokens = randInt(120, 1400);
  if (inject.tokenSpike) {
    inputTokens *= randInt(6, 14); // oversized context / log dump
    outputTokens = Math.round(outputTokens * rand(1.5, 3));
  }
  const cached = !inject.tokenSpike && rng() < 0.15;
  const retry = inject.retryLoop ? true : rng() < 0.05;
  const error = retry && rng() < 0.4;
  const effIn = cached ? Math.round(inputTokens * 0.1) : inputTokens;
  const totalCost = effIn * rate.in + outputTokens * rate.out;
  callSeq += 1;
  return {
    callId: `call_${runId}_${callSeq}`,
    stepId,
    runId,
    model: rate.model,
    provider: rate.provider,
    inputTokens,
    outputTokens,
    costPerInputToken: rate.in,
    costPerOutputToken: rate.out,
    totalCost,
    latencyMs: Math.round(rand(350, 4200) * (inject.tokenSpike ? 1.8 : 1)),
    cached,
    retry,
    error,
    timestamp: new Date(tsMs).toISOString(),
  };
}

function buildRun(index: number, tpl: TemplateDef, startMs: number, inject: AnomalyInjection): Run {
  const runId = `run_${String(index).padStart(3, '0')}`;
  const steps: Step[] = [];
  let cursor = startMs;
  let stepNo = 0;

  for (const def of tpl.steps) {
    stepNo += 1;
    const stepId = `${runId}_s${stepNo}`;
    const stepStart = cursor;

    // Model calls for this step (gates/tools may make zero LLM calls).
    const numCalls = def.gate ? 0 : inject.agenticLoop ? randInt(6, 12) : def.tool ? randInt(0, 1) : randInt(1, 3);
    const calls: ModelCall[] = [];
    let callCursor = stepStart;
    for (let i = 0; i < numCalls; i++) {
      const model = inject.expensiveModel ? 'claude-opus-4' : pick(tpl.models);
      const call = buildModelCall(stepId, runId, callCursor, model, inject);
      calls.push(call);
      callCursor += call.latencyMs + randInt(20, 120);
    }

    const modelLatency = calls.reduce((a, c) => a + c.latencyMs, 0);
    const toolTimeMs = def.tool ? randInt(800, 6000) : 0;
    const waitTimeMs = def.gate ? randInt(2 * 60_000, 90 * 60_000) : 0; // approval gates dominate wall-clock
    const stepCost = calls.reduce((a, c) => a + c.totalCost, 0);
    const stepInputTokens = calls.reduce((a, c) => a + c.inputTokens, 0);
    const stepOutputTokens = calls.reduce((a, c) => a + c.outputTokens, 0);
    const stepDuration = modelLatency + toolTimeMs + waitTimeMs + randInt(50, 400);
    const stepEnd = stepStart + stepDuration;

    steps.push({
      stepId,
      runId,
      agent: def.agent,
      sequence: stepNo,
      parentStepId: null,
      startedAt: new Date(stepStart).toISOString(),
      endedAt: new Date(stepEnd).toISOString(),
      toolTimeMs,
      waitTimeMs,
      calls,
      stepCost,
      stepLatencyMs: modelLatency,
      stepInputTokens,
      stepOutputTokens,
    });
    cursor = stepEnd;
  }

  const allCalls = steps.flatMap((s) => s.calls);
  const totalCost = allCalls.reduce((a, c) => a + c.totalCost, 0);
  const totalInputTokens = allCalls.reduce((a, c) => a + c.inputTokens, 0);
  const totalOutputTokens = allCalls.reduce((a, c) => a + c.outputTokens, 0);
  const totalModelLatencyMs = allCalls.reduce((a, c) => a + c.latencyMs, 0);
  const totalWaitTimeMs = steps.reduce((a, s) => a + s.waitTimeMs, 0);
  const wallClockMs = cursor - startMs;
  const modelsUsed = Array.from(new Set(allCalls.map((c) => c.model)));
  const anomalyFlag = Boolean(inject.tokenSpike || inject.retryLoop || inject.expensiveModel || inject.agenticLoop);

  const status: RunStatus = tpl.steps.some((s) => s.gate) && rng() < 0.4 ? 'awaiting-approval' : pick(STATUSES);

  return {
    runId,
    title: `${tpl.id} #${index}`,
    templateId: tpl.id,
    triggerType: tpl.trigger,
    triggerSource: tpl.source,
    assistant: tpl.assistant,
    tenantId: 'tenant_demo',
    userId: tpl.trigger.startsWith('user') ? pick(USERS) : 'system',
    cluster: tpl.cluster,
    cloudAccount: tpl.cloudAccount,
    startedAt: new Date(startMs).toISOString(),
    endedAt: new Date(cursor).toISOString(),
    status,
    steps,
    totalCost,
    totalInputTokens,
    totalOutputTokens,
    totalModelLatencyMs,
    totalWaitTimeMs,
    wallClockMs,
    modelsUsed,
    anomalyFlag,
  };
}

// ─── Generate the run set (~30 runs across ~30 days) ──────────────────────────

function generateRuns(): Run[] {
  const runs: Run[] = [];
  const count = 48;
  for (let i = 1; i <= count; i++) {
    const tpl = TEMPLATES[i % TEMPLATES.length];
    // Guarantee a healthy batch lands on the anchor day ("Today" is the default
    // filter), and spread the rest across the prior ~29 days. `withinDay` is
    // capped below the noon anchor so each run sits on its intended calendar day.
    const daysAgo = i <= 14 ? 0 : randInt(1, 29);
    const withinDay = rand(0, 11.5 * 3_600_000);
    const startMs = ANCHOR - daysAgo * DAY - withinDay;

    // Inject a handful of explainable anomalies.
    const inject: AnomalyInjection = {};
    if (i === 6) inject.tokenSpike = true; // huge log dump
    if (i === 13) inject.retryLoop = true; // retry/error storm
    if (i === 19) inject.expensiveModel = true; // SLM step swapped to frontier
    if (i === 24) inject.agenticLoop = true; // runaway iterations

    runs.push(buildRun(i, tpl, startMs, inject));
  }
  return runs.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt));
}

export const mockRuns: Run[] = generateRuns();

// ─── Anomalies (3 levels per spec §8) ─────────────────────────────────────────

function buildAnomalies(runs: Run[]): Anomaly[] {
  const out: Anomaly[] = [];

  // Level 1 — daily total spike (rolling 28-day baseline).
  out.push({
    anomalyId: 'an_daily_1',
    level: 1,
    driver: 'token_spike',
    message: 'Daily cost $4.12 vs 28-day avg $1.80 (+129%)',
    value: 4.12,
    baseline: 1.8,
    timestamp: new Date(ANCHOR - 2 * DAY).toISOString(),
  });

  // Level 2 — single run abnormal in absolute terms (> p95 / hard ceilings).
  const spikeRun = runs.find((r) => r.runId === 'run_006');
  if (spikeRun) {
    out.push({
      anomalyId: 'an_run_spike',
      level: 2,
      driver: 'token_spike',
      message: `Run cost ${
        spikeRun.totalCost >= 1 ? '$' + spikeRun.totalCost.toFixed(2) : '$' + spikeRun.totalCost.toFixed(4)
      } — top 1% of all runs; oversized log context`,
      runId: spikeRun.runId,
      value: spikeRun.totalCost,
      baseline: 1.8,
      timestamp: spikeRun.startedAt,
    });
  }
  const loopRun = runs.find((r) => r.runId === 'run_024');
  if (loopRun) {
    const callCount = loopRun.steps.reduce((a, s) => a + s.calls.length, 0);
    out.push({
      anomalyId: 'an_run_loop',
      level: 2,
      driver: 'agentic_loop',
      message: `${callCount} model calls in one run (likely agent loop)`,
      runId: loopRun.runId,
      value: callCount,
      baseline: 8,
      timestamp: loopRun.startedAt,
    });
  }
  const retryRun = runs.find((r) => r.runId === 'run_013');
  if (retryRun) {
    out.push({
      anomalyId: 'an_run_retry',
      level: 2,
      driver: 'retry_loop',
      message: 'Repeated retries / errors inflated token spend',
      runId: retryRun.runId,
      value: retryRun.totalCost,
      baseline: 1.8,
      timestamp: retryRun.startedAt,
    });
  }

  // Level 3 — expensive for its kind (per-template median multiple).
  const expRun = runs.find((r) => r.runId === 'run_019');
  if (expRun) {
    out.push({
      anomalyId: 'an_tpl_expensive',
      level: 3,
      driver: 'expensive_model',
      message: `'${expRun.templateId}' run cost 4.2× the median for this workflow (frontier model used for an SLM step)`,
      runId: expRun.runId,
      value: expRun.totalCost,
      baseline: expRun.totalCost / 4.2,
      timestamp: expRun.startedAt,
    });
  }

  return out;
}

export const mockAnomalies: Anomaly[] = buildAnomalies(mockRuns);

// ─── Convenience option lists for the filter bar ──────────────────────────────

export const ALL_TEMPLATES = TEMPLATES.map((t) => t.id);
export const ALL_MODELS = Object.keys(MODEL_RATES);
export const ALL_PROVIDERS: Provider[] = ['Anthropic', 'OpenAI', 'Bedrock', 'Google', 'Ollama'];
export const ALL_ASSISTANTS: Assistant[] = ['AI-SRE', 'AI-FinOps', 'AI-K8s', 'AI-CloudOps', 'Custom'];
