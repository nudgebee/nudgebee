/**
 * Mock trigger payloads for the builder's trigger simulator — the shape a workflow
 * receives as `Inputs.event`. Mirrors the event row (api-server event/lifecycle +
 * queue/consumer) and the optimization map (runbook-server recommendation_poller).
 */

/** Emitted phases only. `params.on` accepts nine, so builders take a plain string. */
export type LifecyclePhase = 'event.created' | 'investigation.completed' | 'investigation.failed';

export const DEFAULT_LIFECYCLE_PHASE: LifecyclePhase = 'event.created';

/**
 * The only phases with a live producer (api-server Emits from PostProcessEvent and
 * processInvestigationCompleted, nowhere else). Listing more would advertise
 * triggers that never fire.
 */
export const EMITTED_LIFECYCLE_PHASES: { value: LifecyclePhase; label: string; note: string }[] = [
  {
    value: 'event.created',
    label: 'Event is created',
    note: 'Event has been triaged and classified. No root-cause analysis is attached yet.',
  },
  {
    value: 'investigation.completed',
    label: 'Investigation completed',
    note: 'AI investigation finished. The event also carries the analysis_* fields.',
  },
  {
    value: 'investigation.failed',
    label: 'Investigation failed',
    note: 'AI investigation could not complete. The analysis_* fields are present but empty.',
  },
];

/** Values taken from the trigger's own filter config, so the mock resembles it. */
export interface EventMockOverrides {
  event_type?: string;
  cluster?: string;
  cloud_account_id?: string;
  subject_namespace?: string;
  source?: string;
  priority?: string;
}

/**
 * The Cluster picker stores an account id (matched on `event.cloud_account_id`), while
 * `event.cluster` carries the account's name, so the pick feeds both fields.
 */
export const eventAccountOverrides = (
  accountId: string,
  accounts: { label: string; value: string }[]
): Pick<EventMockOverrides, 'cluster' | 'cloud_account_id'> => ({
  cloud_account_id: accountId,
  cluster: accounts.find((a) => a.value === accountId)?.label || '',
});

export interface OptimizationMockOverrides {
  category?: string;
  rule_name?: string;
  cloud_account_id?: string;
}

const isoMinutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

/**
 * The `analysis_*` extras merged onto the event map on the investigation phases.
 * On `investigation.failed` the producer sends the same keys with empty values.
 */
const analysisExtras = (phase: string): Record<string, any> => {
  if (phase === 'investigation.completed') {
    return {
      analysis_status: 'COMPLETED',
      analysis_summary: 'The checkout-api pods are restarting because the liveness probe times out under load.',
      analysis_investigation: 'Step 1: checked pod events...\nStep 2: correlated with the deploy at 09:12 UTC...',
      analysis_log_summary: 'Repeated "context deadline exceeded" on /healthz.',
      analysis_log_analysis: 'The liveness probe timeout (1s) is below the p99 handler latency (2.4s).',
    };
  }
  if (phase === 'investigation.failed') {
    return {
      analysis_status: 'FAILED',
      analysis_summary: '',
      analysis_investigation: '',
      analysis_log_summary: '',
      analysis_log_analysis: '',
      analysis_status_reason: 'No log data available for the affected pods',
    };
  }
  return {};
};

/**
 * Mock payload for an event trigger at the given phase. Every key of the event row
 * is present, because a real payload carries the whole row.
 */
export const buildEventTriggerMock = (phase: string = DEFAULT_LIFECYCLE_PHASE, overrides: EventMockOverrides = {}): Record<string, any> => {
  const aggregationKey = overrides.event_type || 'KubePodCrashLooping';

  return {
    id: '3f2a51c8-9d64-4e17-b0aa-5c8f1d2e7b04',
    created_at: isoMinutesAgo(4),
    updated_at: isoMinutesAgo(1),
    finding_id: 'f1c4e2b9-7a35-4d80-9e61-2b0c8a3f5d17',
    title: 'Pod checkout-api-7d9c is crash looping',
    description: 'Container checkout-api restarted 6 times in the last 10 minutes.',
    source: overrides.source || 'k8s-collector',
    aggregation_key: aggregationKey,
    // Backfilled from aggregation_key by runbook-server before filters run, so
    // `event.event_type` is always available in a filter expression.
    event_type: aggregationKey,
    failure: 'CrashLoopBackOff',
    finding_type: 'Alert',
    category: 'Availability',
    priority: overrides.priority || 'HIGH',
    subject_type: 'Pod',
    subject_name: 'checkout-api-7d9c',
    // On cloud accounts this carries the cloud service name (AmazonEC2, AWS_RDS)
    // rather than a Kubernetes namespace.
    subject_namespace: overrides.subject_namespace || 'payments',
    subject_node: 'ip-10-0-3-42.ec2.internal',
    service_key: 'payments/checkout-api',
    cluster: overrides.cluster || 'prod-us-east-1',
    ends_at: null,
    starts_at: isoMinutesAgo(10),
    fingerprint: 'a91f3c7d5b204e68',
    tenant: '8c1b7e40-5f2a-4d93-8b16-0a7e4c9d3f52',
    cloud_account_id: overrides.cloud_account_id || 'd47a1e93-2b60-4c85-9f31-6e0a8b5c2d17',
    cloud_resource_id: 'arn:aws:eks:us-east-1:123456789012:cluster/prod-us-east-1',
    status: 'FIRING',
    nb_status: 'ACTIVE',
    nb_status_changed_at: isoMinutesAgo(4),
    nb_status_changed_by: 'triage-engine',
    snoozed_until: null,
    principal: 'system:serviceaccount:payments:checkout-api',
    subject_owner: 'checkout-api',
    subject_owner_kind: 'Deployment',
    labels: { team: 'payments', env: 'prod', severity: 'page' },
    urgency: 'HIGH',
    computed_score: 82,
    computed_priority: 'HIGH',
    score_factors: { blast_radius: 0.7, service_tier: 0.9, recurrence: 0.4 },
    score_confidence: 0.86,
    // Evidences are stripped before publish (multi-MB); this flag records that
    // they existed. Tasks that need them refetch the event by id.
    has_evidences: true,
    lifecycle_phase: phase,
    ...analysisExtras(phase),
  };
};

/** Builds the mock payload an optimization trigger receives for a new recommendation. */
export const buildOptimizationTriggerMock = (overrides: OptimizationMockOverrides = {}): Record<string, any> => {
  const cloudAccountId = overrides.cloud_account_id || 'd47a1e93-2b60-4c85-9f31-6e0a8b5c2d17';

  return {
    event_type: 'optimization.recommendation',
    cloud_account_id: cloudAccountId,
    // The poller sets account_id to the same cloud account id.
    account_id: cloudAccountId,
    tenant_id: '8c1b7e40-5f2a-4d93-8b16-0a7e4c9d3f52',
    category: overrides.category || 'RightSizing',
    rule_name: overrides.rule_name || 'AWS Ec2 Idle Instance',
    resource_id: 'i-0a1b2c3d4e5f67890',
    severity: 'High',
    estimated_savings: 142.5,
    // Empty for recommendations that aren't Kubernetes-scoped.
    cluster: 'prod-us-east-1',
    status: 'Open',
    recommendation_id: '6b3d8f21-4c07-49ae-a52e-1f8c0d7b3a95',
  };
};

/** Returns the mock for a trigger type, or null for trigger types the simulator does not cover. */
export const buildTriggerMock = (
  triggerType: string | undefined,
  phase: string,
  eventOverrides: EventMockOverrides = {},
  optimizationOverrides: OptimizationMockOverrides = {}
): Record<string, any> | null => {
  if (triggerType === 'event') return buildEventTriggerMock(phase, eventOverrides);
  if (triggerType === 'optimization') return buildOptimizationTriggerMock(optimizationOverrides);
  return null;
};

export interface PayloadField {
  /** Dotted path within the event object, e.g. `labels.team`. */
  path: string;
  type: string;
  example: string;
}

const describeType = (value: any): string => {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  return typeof value;
};

const describeExample = (value: any): string => {
  if (typeof value === 'string') return value;
  return JSON.stringify(value);
};

/**
 * Flattens a payload into referenceable field paths, so the field list is derived
 * from the mock rather than maintained beside it. Arrays are left whole.
 */
export const flattenPayloadFields = (payload: Record<string, any>, prefix = ''): PayloadField[] =>
  Object.entries(payload).flatMap(([key, value]) => {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      return flattenPayloadFields(value as Record<string, any>, path);
    }
    return [{ path, type: describeType(value), example: describeExample(value) }];
  });
