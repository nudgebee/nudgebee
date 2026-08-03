import { ds } from 'src/utils/colors';
import { recommendationDetails } from '@api1/recommendation/data';

// ─── Shared types ───

export type SortField = 'severity' | 'estimated_savings' | 'updated_at' | 'finops_score' | 'safety_band' | 'category';
export type SortDirection = 'asc' | 'desc';

// ─── Shared constants ───

export const CATEGORY_LABELS: Record<string, string> = {
  RightSizing: 'Right Sizing',
  Configuration: 'Config',
  K8sSpotRecommendation: 'Spot Instance',
  InfraUpgrade: 'Infra Upgrade',
};

export const RULE_LABELS: Record<string, string> = {
  pod_right_sizing: 'Pod Right Sizing',
  replica_right_sizing: 'Replica Right Sizing',
  unused_pvc: 'Unused PVC',
  abandoned_resource: 'Abandoned Resource',
  pv_rightsize: 'PV Right Sizing',
  'Spot instance recommendation': 'Spot Instance',
  helm_chart_upgrade: 'Helm Chart Upgrade',
  k8s_api_deprecated: 'K8s API Deprecated',
  certificate_expiry: 'Certificate Expiry',
  azure_app_service_plan_optimization: 'App Service Plan',
  cluster_upgrade_confidence: 'Cluster Upgrade Confidence',
  vm_underutilized: 'VM Underutilized',
  vm_idle: 'VM Idle',
  vm_generation_upgrade: 'VM Generation Upgrade',
  vm_stopped: 'VM Stopped',
  missing_tags: 'Missing Tags',
  orphaned_volume: 'Orphaned Volume',
  storage_public_access: 'Storage Public Access',
  storage_versioning_disabled: 'Storage Versioning Disabled',
  storage_no_lifecycle: 'Storage No Lifecycle',
  storage_no_cmek: 'Storage No Customer-Managed Key',
  storage_class_optimization: 'Storage Class Optimization',
  db_backup_disabled: 'Database Backup Disabled',
  db_public_access: 'Database Public Access',
  db_storage_autoscaling: 'Database Storage Autoscaling',
  k8s_logging_disabled: 'Kubernetes Logging Disabled',
  k8s_network_policy: 'Kubernetes Network Policy',
  unused_load_balancer: 'Unused Load Balancer',
  unassociated_public_ip: 'Unassociated Public IP',
};

export const NON_SECURITY_CATEGORIES = ['RightSizing', 'InfraUpgrade', 'Configuration', 'K8sSpotRecommendation'];
export const DEFAULT_STATUS = ['Open', 'InProgress'];

// InfraUpgrade rules that belong to the cluster-upgrade feature rather than to
// cost optimisation. Each already has a dedicated surface — the Upgrade Planner
// cards, the Cluster Upgrade tab, or the Helm Upgrade tab — so optimise excludes
// them. What remains under InfraUpgrade here is the cloud-collector set
// (aws_ec2_ebs_generation_upgrade, azure_sql_database_pricing_model_upgrade, …),
// which are genuine optimisation recommendations.
//
// Two producers write these, so check both when extending the list:
// the k8s collector (upgrade_handler.py / event_handler.py) and the api-server
// k8s_upgrade service, which stores a row per upgrade plan named for the health
// check type (StoreHealthCheckWithPlanID).
export const UPGRADE_PLANNER_RULES = [
  'k8s_api_deprecated',
  'k8s_api_deleted',
  'kube_proxy_version',
  'eks_add_ons_version',
  'eks_cluster_upgrade',
  'cluster_upgrade_confidence',
  'k8s_helm_compatibility',
  'helm_chart_upgrade',
  'pre_flight',
  'post_flight',
];

// ─── Filter buckets (Savings / Last seen presets) ───
//
// Preset buckets shown in the Savings and Last-seen filter dropdowns. The
// mappers below translate a bucket key into the server-side facet params
// consumed by getK8sRecommendation / getK8sRecommendationSummaryByRuleName.

export interface SavingsBucket {
  key: string;
  label: string;
  caption?: string;
  savingsGte?: number;
  savingsLt?: number;
}

export const SAVINGS_BUCKETS: SavingsBucket[] = [
  { key: '', label: 'Any' },
  { key: 'gte1', label: '≥ $1 /mo', savingsGte: 1 },
  { key: 'gte5', label: '≥ $5 /mo', savingsGte: 5 },
  { key: 'gte10', label: '≥ $10 /mo', savingsGte: 10 },
  { key: 'gte25', label: '≥ $25 /mo', savingsGte: 25 },
  { key: 'cost-increase', label: 'Cost increase (< $0)', caption: 'Reliability fixes that raise spend', savingsLt: 0 },
];

export const savingsBucketToParams = (key: string): { savingsGte?: number; savingsLt?: number } => {
  const bucket = SAVINGS_BUCKETS.find((b) => b.key === key);
  if (!bucket) return {};
  const params: { savingsGte?: number; savingsLt?: number } = {};
  if (bucket.savingsGte !== undefined) params.savingsGte = bucket.savingsGte;
  if (bucket.savingsLt !== undefined) params.savingsLt = bucket.savingsLt;
  return params;
};

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

export interface LastSeenBucket {
  key: string;
  label: string;
  caption?: string;
  withinMs?: number;
  olderThanMs?: number;
}

export const LAST_SEEN_BUCKETS: LastSeenBucket[] = [
  { key: '', label: 'Any time' },
  { key: '1h', label: 'Last hour', withinMs: HOUR_MS },
  { key: '24h', label: 'Last 24 hours', withinMs: DAY_MS },
  { key: '7d', label: 'Last 7 days', withinMs: 7 * DAY_MS },
  { key: '30d', label: 'Last 30 days', withinMs: 30 * DAY_MS },
  { key: 'stale', label: 'Not seen in 30+ days', caption: 'Stale, likely-resolved findings', olderThanMs: 30 * DAY_MS },
];

// "Last seen" is the row's updated_at: bumped by every re-scan upsert (and by
// user edits), so buckets are computed against the fetch time, not cached.
export const lastSeenBucketToParams = (key: string, now: number = Date.now()): { updatedAtGte?: string; updatedAtLt?: string } => {
  const bucket = LAST_SEEN_BUCKETS.find((b) => b.key === key);
  if (!bucket) return {};
  if (bucket.withinMs !== undefined) return { updatedAtGte: new Date(now - bucket.withinMs).toISOString() };
  if (bucket.olderThanMs !== undefined) return { updatedAtLt: new Date(now - bucket.olderThanMs).toISOString() };
  return {};
};

// ─── Shared helpers ───

export const formatRuleName = (ruleName: string): string => {
  if (RULE_LABELS[ruleName]) {
    return RULE_LABELS[ruleName];
  }
  return ruleName
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase())
    .replace(/^aws /i, 'AWS ')
    .replace(/^azure /i, 'Azure ')
    .replace(/^gcp /i, 'GCP ');
};

export const daysSince = (dateStr: string | null): string => {
  if (!dateStr) {
    return '—';
  }
  const diff = Date.now() - new Date(dateStr).getTime();
  const days = Math.floor(diff / (1000 * 60 * 60 * 24));
  if (days === 0) {
    return 'Today';
  }
  if (days === 1) {
    return '1d';
  }
  if (days < 30) {
    return `${days}d`;
  }
  if (days < 365) {
    return `${Math.floor(days / 30)}mo`;
  }
  return `${Math.floor(days / 365)}y`;
};

export const daysSinceLong = (dateStr: string | null): string | null => {
  if (!dateStr) {
    return null;
  }
  const diff = Date.now() - new Date(dateStr).getTime();
  const days = Math.floor(diff / (1000 * 60 * 60 * 24));
  if (days === 0) {
    return 'Today';
  }
  if (days === 1) {
    return '1 day ago';
  }
  return `${days} days ago`;
};

/** Safely parse a JSON string, returning the original value on failure */
export const safeParseJSON = (value: any): any => {
  if (typeof value !== 'string') {
    return value;
  }
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
};

/** Extract a display-friendly resource name from a recommendation row.
 *  Fallback order: resource_name → cloud_resourse.name → recommendation JSON fields → dash */
export const getResourceDisplayName = (rec: any, fallback = '—'): string => {
  if (rec.resource_name) return rec.resource_name;
  if (rec.cloud_resourse?.name) return rec.cloud_resourse.name;

  // Try extracting from the recommendation JSON when the top-level fields are null
  const recData = safeParseJSON(rec.recommendation);
  if (recData && typeof recData === 'object') {
    if (recData.resource_name) return recData.resource_name;
    // For Azure Advisor: use display SKU with term/region to differentiate similar recommendations
    if (recData.ext_displaysku) {
      const qualifiers = [recData.ext_term, recData.ext_region].filter(Boolean).join(', ');
      return qualifiers ? `${recData.ext_displaysku} (${qualifiers})` : recData.ext_displaysku;
    }
    // Use impacted_value only if it refers to an actual resource, not a subscription
    if (
      recData.impacted_value != null &&
      typeof recData.impacted_field === 'string' &&
      !recData.impacted_field.toLowerCase().includes('subscription')
    ) {
      return recData.impacted_value;
    }
    // Last resort: extract the last segment from resource_path if available
    if (typeof recData.resource_path === 'string') {
      const segments = recData.resource_path.split('/').filter(Boolean);
      if (segments.length > 0) return segments[segments.length - 1];
    }
  }

  return rec.account_object_id || fallback;
};

// ─── Shared CLI command builder ───

const formatCpuValue = (value: number): string => (value < 1 ? Math.round(value * 1000) + 'm' : String(value));
const formatMemValue = (bytes: number): string => Math.round(bytes / (1024 * 1024)) + 'Mi';

const buildContainerPatch = (containerName: string, entries: any[], workloadType: string, workloadName: string, ns: string): string[] => {
  const cpu = entries.find((e: any) => e.resource === 'cpu');
  const mem = entries.find((e: any) => e.resource === 'memory');
  const requests: string[] = [];
  const limits: string[] = [];

  if (cpu?.recommended?.request != null) {
    requests.push(`cpu=${formatCpuValue(Number(cpu.recommended.request))}`);
  }
  if (mem?.recommended?.request != null) {
    requests.push(`memory=${formatMemValue(Number(mem.recommended.request))}`);
  }
  if (cpu?.recommended?.limit != null) {
    limits.push(`cpu=${formatCpuValue(Number(cpu.recommended.limit))}`);
  }
  if (mem?.recommended?.limit != null) {
    limits.push(`memory=${formatMemValue(Number(mem.recommended.limit))}`);
  }

  // Stock kubectl's `set resources` only supports built-in workload types, not
  // the Argo Rollout CRD — emit an edit instruction with the values instead.
  if (workloadType.toLowerCase() === 'rollout') {
    const lines = [`# Argo Rollout: kubectl set resources does not support the Rollout CRD.`];
    lines.push(`# In \`kubectl edit rollout/${workloadName} -n ${ns}\`, set for container "${containerName}":`);
    if (requests.length > 0) {
      lines.push(`#   requests: ${requests.join(', ')}`);
    }
    if (limits.length > 0) {
      lines.push(`#   limits: ${limits.join(', ')}`);
    }
    return requests.length > 0 || limits.length > 0 ? [lines.join('\n')] : [];
  }

  const base = `kubectl set resources ${workloadType}/${workloadName} -n ${ns} -c ${containerName}`;
  const patches: string[] = [];
  if (requests.length > 0) {
    patches.push(`${base} --requests=${requests.join(',')}`);
  }
  if (limits.length > 0) {
    patches.push(`${base} --limits=${limits.join(',')}`);
  }
  return patches;
};

export const buildKubectlCommand = (rec: any): string => {
  const recData = safeParseJSON(rec.recommendation);
  const isPodRightSizing = rec.rule_name === 'pod_right_sizing';

  if (!isPodRightSizing || !recData || typeof recData !== 'object') {
    return `# Recommendation ID: ${rec.id}\n# Category: ${rec.category}\n# Rule: ${rec.rule_name}`;
  }

  const ns = rec.resource_k8s_namespace || 'default';
  const isPod = rec.cloud_resourse?.type === 'Pod';
  const workloadName = isPod ? rec.cloud_resourse?.meta?.controller : rec.cloud_resourse?.name || rec.resource_name || 'workload';
  const workloadType = (isPod ? rec.cloud_resourse?.meta?.controllerKind : rec.cloud_resourse?.type)?.toLowerCase() || 'deployment';

  const patches: string[] = [];
  for (const [containerName, entries] of Object.entries(recData)) {
    if (!Array.isArray(entries)) {
      continue;
    }
    patches.push(...buildContainerPatch(containerName, entries, workloadType, workloadName, ns));
  }
  return patches.join('\n') || `# No resource changes recommended for ${workloadName}`;
};

// ─── Category colors ───

export const categoryColors: Record<string, { bg: string; color: string; border: string }> = {
  RightSizing: { bg: ds.blue[100], color: 'var(--ds-blue-700)', border: ds.blue[300] },
  InfraUpgrade: { bg: 'var(--ds-purple-100)', color: 'var(--ds-purple-600)', border: 'var(--ds-purple-200)' },
  Configuration: { bg: 'var(--ds-amber-100)', color: 'var(--ds-amber-700)', border: 'var(--ds-yellow-300)' },
  K8sSpotRecommendation: { bg: 'var(--ds-yellow-100)', color: 'var(--ds-red-700)', border: 'var(--ds-amber-200)' },
};

// ─── Recommendation brief helpers ───

const normalizeMem = (val: number): number => (val > 100000 ? val / (1024 * 1024) : val);

// Catalog title lookup (mirrors recommendationApi.getRecommendationDetails) so a
// catalog-backed config row can show the action ("Enable Dead Letter Queue …")
// instead of the generic "Configuration issue detected".
export const catalogTitle = (category: string, ruleName: string): string => {
  if (!ruleName) return '';
  const direct = (recommendationDetails as any)[category]?.[ruleName];
  if (direct?.title) return direct.title;
  for (const cat of Object.keys(recommendationDetails)) {
    const entry = (recommendationDetails as any)[cat]?.[ruleName];
    if (entry?.title) return entry.title;
  }
  return '';
};

// Verb + magnitude grammar so a row reads as an action, not a raw delta:
// "Reduce CPU 96%" / "Increase Mem 12%" / "Set CPU request".
const getResourceChangePart = (entry: any, label: string, isMem: boolean): string | null => {
  if (!entry?.recommended?.request) return null;
  if (!entry?.allocated?.request) {
    const val = isMem ? Math.round(normalizeMem(entry.recommended.request)) : entry.recommended.request;
    return isMem ? `Mem rec: ${val} Mi` : `CPU rec: ${val} cores`;
  }
  const allocated = isMem ? normalizeMem(entry.allocated.request) : entry.allocated.request;
  const recommended = isMem ? normalizeMem(entry.recommended.request) : entry.recommended.request;
  const pct = Math.round((1 - recommended / allocated) * 100);
  if (pct > 0) return `${label} req ${pct}% lower`;
  if (pct < 0) return `${label} req ${Math.abs(pct)}% higher`;
  return null;
};

const getRightSizingBrief = (data: any): string => {
  const notifications = Array.isArray(data.notifications)
    ? data.notifications
    : (Object.values(data).find((v: any) => Array.isArray(v) && v.length > 0 && v[0]?.resource) as any[] | undefined);
  if (!notifications) return 'Resource optimization available';
  const cpu = notifications.find((n: any) => n.resource === 'cpu');
  const mem = notifications.find((n: any) => n.resource === 'memory');
  const parts = [getResourceChangePart(cpu, 'CPU', false), getResourceChangePart(mem, 'Mem', true)].filter(Boolean);
  return parts.length > 0 ? parts.join(', ') : 'Resource optimization available';
};

const getConfigBrief = (data: any): string => {
  if (Array.isArray(data)) {
    const firstMsg = data[0]?.message;
    if (firstMsg) {
      const extra = data.length > 1 ? ` (+${data.length - 1} more)` : '';
      return firstMsg.replace(/\[b\]|\[\/b\]/g, '') + extra;
    }
    return `${data.length} configuration issue${data.length !== 1 ? 's' : ''} detected`;
  }
  return data.reason || data.description?.replace(/\[b\]|\[\/b\]/g, '') || data.message || 'Configuration issue detected';
};

const getInfraUpgradeBrief = (data: any): string => {
  if (data.description) return data.description.replace(/\[b\]|\[\/b\]/g, '');
  if (data.current_version && data.recommended_version) return `Upgrade from ${data.current_version} → ${data.recommended_version}`;
  if (data.current_api_version) return `Deprecated API: ${data.current_api_version}`;
  return 'Infrastructure upgrade available';
};

const getGenericBrief = (data: any): string => {
  if (Array.isArray(data) && data.length > 0 && data[0]?.message) return data[0].message;
  return data.description || data.reason || data.message || '';
};

export const getRecommendationBrief = (rec: any): string => {
  const jsonb = rec.recommendation;
  if (!jsonb) return '';
  const data = safeParseJSON(jsonb);
  if (typeof data === 'string') return data;
  // pod_right_sizing payloads keep the KRR shape under either category
  // (requests-unset rows live under Configuration)
  if (rec.rule_name === 'pod_right_sizing') return getRightSizingBrief(data);
  switch (rec.category || '') {
    case 'RightSizing':
      return getRightSizingBrief(data);
    case 'Configuration':
      return getConfigBrief(data);
    case 'InfraUpgrade':
      return getInfraUpgradeBrief(data);
    case 'K8sSpotRecommendation':
      return `${data.type || 'Workload'} candidate for spot instances`;
    default:
      return getGenericBrief(data);
  }
};
