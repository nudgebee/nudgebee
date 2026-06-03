import { ds } from 'src/utils/colors';

// ─── Shared types ───

export type SortField = 'severity' | 'estimated_savings' | 'updated_at';
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
  const isPodRightSizing = rec.category === 'RightSizing' && rec.rule_name === 'pod_right_sizing';

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
