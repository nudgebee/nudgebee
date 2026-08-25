import { snackbar } from '@shared/snackbarService';

/**
 * Access-denied (RBAC / 403) UX helper.
 *
 * `queryGraphQL` RESOLVES (never rejects) on a GraphQL-level error, so an
 * access-denied result arrives as a normal response whose body carries an
 * `errors[]` array while the matching `data` field is null. The three shapes
 * our gateway emits for a denial (see `@lib/rpcGateway`):
 *   - action role-gate            → `extensions.code === 'FORBIDDEN'`
 *   - no role in the active tenant → `extensions.code === 'NO_TENANT_ROLE'`
 *   - upstream query-engine 403    → `extensions.upstream.status === 403`
 *
 * Before this module, a denied section (SLO, Insights, a table column, …) just
 * rendered empty with no explanation — the `.catch` blocks don't even fire
 * because the promise resolves. Detection is centralised in `queryGraphQL`
 * (keyed by the GraphQL operation name) so every tab benefits without wiring
 * each call site; the affected sections are collected into ONE consolidated,
 * deduped toast per burst (e.g. "You don't have permission to view: Insights,
 * SLO").
 */

type GraphQLError = { extensions?: { code?: string; upstream?: { status?: number } } };

function isForbiddenError(e: GraphQLError | null | undefined): boolean {
  const code = e?.extensions?.code;
  if (code === 'FORBIDDEN' || code === 'NO_TENANT_ROLE') return true;
  return e?.extensions?.upstream?.status === 403;
}

// Pull the GraphQL `errors[]` out of the several shapes a caller might hand us:
// the raw axios-style response from queryGraphQL (`{ data: { errors } }`), an
// already-unwrapped body (`{ errors }`), or a thrown error that carries a
// response (`err.response.data.errors`).
function extractGraphQLErrors(input: unknown): GraphQLError[] {
  if (!input || typeof input !== 'object') return [];
  const o = input as {
    data?: { errors?: unknown };
    errors?: unknown;
    response?: { data?: { errors?: unknown } };
  };
  const candidates = o.data?.errors ?? o.errors ?? o.response?.data?.errors;
  return Array.isArray(candidates) ? (candidates as GraphQLError[]) : [];
}

/**
 * True when a queryGraphQL response (or a thrown error carrying one) represents
 * an access-denied / 403 outcome.
 */
export function isAccessDenied(input: unknown): boolean {
  return extractGraphQLErrors(input).some(isForbiddenError);
}

// ---------------------------------------------------------------------------
// Consolidated, deduped toast batcher.
// ---------------------------------------------------------------------------

const BATCH_MS = 400; // collect denials fired during one page/tab load into one toast
const SUPPRESS_MS = 8000; // don't re-toast the same section within this window (refreshes/filters)

const pending = new Set<string>();
const lastShownAt = new Map<string, number>();
let flushTimer: ReturnType<typeof setTimeout> | null = null;

function flush(): void {
  flushTimer = null;
  if (pending.size === 0) return;
  const sections = [...pending].sort();
  pending.clear();
  const now = Date.now();
  sections.forEach((s) => lastShownAt.set(s, now));

  const message =
    sections.length === 1 ? `You don't have permission to view ${sections[0]}.` : `You don't have permission to view: ${sections.join(', ')}.`;
  snackbar.error(message, { description: 'Ask an administrator to grant access.', duration: SUPPRESS_MS });
}

/**
 * Queue a section for the consolidated access-denied toast. Client-only.
 * Deduped within {@link SUPPRESS_MS}, batched over {@link BATCH_MS} so a page
 * that 403s on several sections at once produces a single toast.
 */
export function notifyAccessDenied(section: string): void {
  if (!section || typeof window === 'undefined') return;
  const last = lastShownAt.get(section);
  if (last !== undefined && Date.now() - last < SUPPRESS_MS) return;
  pending.add(section);
  if (flushTimer === null) flushTimer = setTimeout(flush, BATCH_MS);
}

/**
 * Maps a GraphQL operation name to the user-facing label of the section it
 * powers. Opt-in: an operation absent from this map never toasts, so adding a
 * denial-aware section is a one-line addition here rather than a change at the
 * call site. Several finer operations legitimately share a label (all SLO reads
 * → "SLO") — the batcher dedupes, so one toast names each section once.
 *
 * Covers the Kubernetes cluster-details tabs — Summary, Apps & Infra, Optimize,
 * Troubleshoot, Monitoring, Security & Tools — plus the shared enrichment reads
 * that fill table columns. Some sub-tabs share an operation (e.g. all Optimize
 * lists + CIS Scan use `list_k8_recommendation`); the operation name alone can't
 * distinguish them, so those resolve to an umbrella label. Grafana is an iframe
 * (no queryGraphQL) and cannot be covered here. Extend as new sections gain
 * 403-aware messaging.
 */
export const OPERATION_ACCESS_SECTIONS: Record<string, string> = {
  // --- Summary tab ---
  ListK8sInsights: 'Insights',
  GetK8sInsights: 'Insights',

  // --- SLO (Summary card, Applications column, Monitoring › SLO) ---
  GetSLOConfigs: 'SLO',
  SLOConfigList: 'SLO',
  SLOReport: 'SLO',
  SloReportObservationV2: 'SLO',

  // --- Metrics / utilization (Summary utilization, CPU/Memory/Cost columns,
  //     Monitoring › Query Metrics, Auto Scaler & Sensitive Logs share these) ---
  MetricsQueryUtilisation: 'Metrics',
  MetricsQuery: 'Metrics',
  k8s_pod_groupings: 'Metrics',

  // --- Apps & Infra primary lists ---
  k8s_workloads_list: 'Applications',
  k8s_nodes_list: 'Nodes',
  NodeInfographics: 'Nodes',
  k8s_pods_list: 'Pods',
  k8s_namespace_list: 'Namespaces',
  k8s_workload_namespace_list: 'Namespaces',

  // --- Optimize tab (all right-sizing/best-practice/spot lists + CIS scan share
  //     `list_k8_recommendation`; they differ only by variables, so the label is
  //     the umbrella "Recommendations") ---
  list_k8_recommendation: 'Recommendations',
  list_k8_recommendation_summary: 'Recommendations',
  k8s_recommendation_summary: 'Recommendations',
  ListK8sRecommendationAggregate: 'Recommendations',
  RecommendationResolution: 'Recommendation Resolution',
  listAutoOptimize: 'Auto-Pilot',

  // --- Troubleshoot tab ---
  EventWorkloadCount: 'Events',
  EventAggregate: 'Events',
  k8s_event_groupings: 'Events',
  list_k8_issues_data: 'Events',
  ListK8sAnomalies: 'Anomalies',
  ListK8sAnomaliesData: 'Anomalies',
  EventGetTriageRules: 'Triage Rules',
  WorkloadListCriticality: 'Service Criticality',

  // --- Monitoring tab ---
  FetchLogs: 'Logs',
  LogGroup: 'Log Groups',
  GetEventRules: 'Alert Manager',
  ServiceMapViaTraces: 'Service Map',
  TraceV3: 'Traces',
  TraceByTraceV3: 'Traces',
  TraceGroupingV3: 'Trace Groups',
  TraceGroupingV2: 'Cross-Zone Traffic',

  // --- Security & Tools tab (CIS Scan → `list_k8_recommendation` above;
  //     Sensitive Logs → `MetricsQueryUtilisation` above) ---
  get_security_recommendation: 'Image Scan',
  ListK8sVersions: 'Cluster Upgrade',
  FetchUpgradePlans: 'Upgrade Planner',

  // --- Ask-Nudgebee / LLM response view (KubernetesLLMResponseGeneratorV2 +
  //     its hooks). ListModels + GetModelConfig share the "Models" umbrella,
  //     and GetFunctions + ListAgents share "Agent Configuration" — the batcher
  //     dedupes so each names its section once. ---
  AiGetConversationV3: 'AI Response',
  GetConversationSuggestions: 'Follow-up Suggestions',
  GetConversationUsageMetrics: 'Token Usage',
  InsightSummary: 'Cluster Insights',
  ListModels: 'Models',
  GetModelConfig: 'Models',
  GetFunctions: 'Agent Configuration',
  ListAgents: 'Agent Configuration',
};

/**
 * Operations whose api1 wrapper runs through `splitAndParallelQuery`, which
 * forwards each top-level field to `queryGraphQL` as `${operationName}_${alias}`
 * (see HttpService.ts). Matched by prefix so the suffixed per-field ops still
 * resolve to their section. Keys must NOT collide with a real op name of the
 * form `<key>_<something>`.
 */
const SPLIT_OPERATION_SECTIONS: Record<string, string> = {
  K8sOptimizeSummaryInfographics: 'Optimize Summary',
};

function sectionForOperation(operationName: string): string | undefined {
  const exact = OPERATION_ACCESS_SECTIONS[operationName];
  if (exact) return exact;
  for (const base of Object.keys(SPLIT_OPERATION_SECTIONS)) {
    if (operationName === base || operationName.startsWith(`${base}_`)) return SPLIT_OPERATION_SECTIONS[base];
  }
  return undefined;
}

/**
 * Central entry point called by `queryGraphQL`. When a response is an access
 * denial and the operation maps to a known section, queue a consolidated toast.
 * `isAccessDenied` runs first so the common (successful) path stays O(1) and the
 * section lookup only happens on an actual denial. No-op on the server.
 */
export function reportAccessDeniedForOperation(operationName: string | undefined, response: unknown): void {
  if (!operationName || typeof window === 'undefined') return;
  if (!isAccessDenied(response)) return;
  const section = sectionForOperation(operationName);
  if (section) notifyAccessDenied(section);
}
