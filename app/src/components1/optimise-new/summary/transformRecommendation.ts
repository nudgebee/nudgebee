import { formatRuleName, getRecommendationBrief, getResourceDisplayName, safeParseJSON } from '../utils';
import type { InsightItem, MainCategory, SubCategory, Provider, Environment } from './insights';

// ─── Category & SubCategory mapping ────────────────────────────────────────
// Maps the API's category + rule_name to the UI's MainCategory + SubCategory.
// Pattern-based matching handles the 150+ rule_names in production data.

type CategoryMapping = { category: MainCategory; subCategory: SubCategory };

const ABANDONED_PATTERNS = ['unused', 'abandoned', 'orphaned', 'unallocated', 'empty', 'stopped', 'retention', 'idle'];
const SAVINGS_PATTERNS = ['savings_plan', 'reserved', 'class_optimization', 'native_rightsize', 'serverless_optimization'];
const PERF_CONFIG_PATTERNS = ['alarm_missing', 'monitoring', 'diagnostics', '_insights'];
const COMPLIANCE_PATTERNS = ['tags', 'labels', 'versioning', 'lifecycle', 'logging', 'access_log', 'pitr', 'ttl', 'cmek'];

const matchesAny = (rule: string, patterns: string[]): boolean => patterns.some((p) => rule.includes(p));

function mapRightSizing(rule: string): CategoryMapping {
  if (matchesAny(rule, ABANDONED_PATTERNS)) return { category: 'cost', subCategory: 'abandoned' };
  if (matchesAny(rule, SAVINGS_PATTERNS)) return { category: 'cost', subCategory: 'savings' };
  return { category: 'cost', subCategory: 'rightsizing' };
}

function mapConfiguration(rule: string): CategoryMapping {
  if (rule === 'health_check' || matchesAny(rule, PERF_CONFIG_PATTERNS)) return { category: 'performance', subCategory: 'utilization' };
  if (rule.includes('public_access')) return { category: 'security_config', subCategory: 'security_vulnerability' };
  if (rule.endsWith('misconfigurations') || rule === 'misconfigurations') return { category: 'security_config', subCategory: 'critical_config' };
  if (matchesAny(rule, COMPLIANCE_PATTERNS)) return { category: 'security_config', subCategory: 'compliance' };
  return { category: 'security_config', subCategory: 'critical_config' };
}

function mapInfraUpgrade(rule: string): CategoryMapping {
  if (rule === 'helm_chart_upgrade' || rule === 'k8s_api_deprecated' || rule === 'k8s_helm_compatibility') {
    return { category: 'security_config', subCategory: 'drift' };
  }
  if (matchesAny(rule, ['generation_upgrade', 'pricing_model', 'ssd_v2'])) return { category: 'cost', subCategory: 'savings' };
  return { category: 'security_config', subCategory: 'critical_config' };
}

const CATEGORY_MAPPERS: Record<string, (rule: string) => CategoryMapping> = {
  RightSizing: mapRightSizing,
  K8sSpotRecommendation: () => ({ category: 'cost', subCategory: 'savings' }),
  Cost: () => ({ category: 'cost', subCategory: 'anomalies' }),
  Configuration: mapConfiguration,
  InfraUpgrade: mapInfraUpgrade,
  K8sVersionUpgrade: () => ({ category: 'security_config', subCategory: 'critical_config' }),
};

function mapCategoryAndSubCategory(apiCategory: string, ruleName: string): CategoryMapping {
  const mapper = CATEGORY_MAPPERS[apiCategory];
  return mapper ? mapper(ruleName || '') : { category: 'security_config', subCategory: 'critical_config' };
}

// ─── Description generators ───────────────────────────────────────────────

const formatSavings = (amount: number): string => {
  if (!amount || amount <= 0) return '';
  return '$' + Math.round(amount) + '/mo';
};

const withSavings = (message: string, verb: string, savingsStr: string): string => {
  if (!savingsStr) return message;
  return message + ` ${verb} saves ~${savingsStr}.`;
};

const SIMPLE_RULE_DESCRIPTIONS: Record<string, [string, string]> = {
  'replica-rightsizing': ['Running more replicas than needed for the current traffic.', 'Scaling down'],
  unused_pvc: ["This volume claim isn't mounted to any pod — it's allocated but unused.", 'Removing it'],
  pv_rightsize: ['This volume is significantly over-provisioned for its actual usage.', 'Resizing'],
  'volume-rightsizing': ['This volume is significantly over-provisioned for its actual usage.', 'Resizing'],
  abandoned_resource: ['This resource appears idle with no meaningful activity.', 'Cleaning it up'],
  'abandoned-resources': ['This resource appears idle with no meaningful activity.', 'Cleaning it up'],
  'Spot instance recommendation': ["This workload's usage pattern makes it a strong candidate for spot instances.", 'Switching'],
  vm_idle: ['This VM is idle with no compute activity.', 'Downsizing or removing'],
  vm_underutilized: ['This VM is running well below its capacity.', 'Downsizing or removing'],
  unused_load_balancer: ['This load balancer has no healthy targets behind it.', 'Removing'],
  unassociated_public_ip: ["This public IP isn't attached to any resource — it's incurring cost while unused.", 'Releasing'],
  orphaned_volume: ["This volume isn't attached to any instance.", 'Deleting'],
  health_check: ['No readiness or liveness probes configured. Unhealthy pods may receive traffic, causing user-facing errors.', ''],
  missing_tags: ['Required tags are missing, making cost allocation and ownership tracking unreliable.', ''],
  db_backup_disabled: ['Backups are disabled for this database. A failure could mean permanent data loss.', ''],
  db_public_access: ['This database is publicly accessible. Restrict access to reduce your attack surface.', ''],
  storage_public_access: ['This storage bucket is publicly accessible. Restrict access to prevent data leaks.', ''],
  helm_chart_upgrade: ['A newer Helm chart version is available. Upgrading picks up bug fixes and security patches.', ''],
};

const describeRightSizing = (recData: any, savingsStr: string): string => {
  const notifications = Array.isArray(recData?.notifications)
    ? recData.notifications
    : (Object.values(recData || {}).find((v: any) => Array.isArray(v) && v.length > 0 && v[0]?.resource) as any[] | undefined);
  if (!notifications) return withSavings("Resource requests don't match actual usage.", 'Adjusting', savingsStr);
  const cpu = notifications.find((n: any) => n?.resource === 'cpu');
  const mem = notifications.find((n: any) => n?.resource === 'memory');
  const cpuPct = cpu?.allocated?.request && cpu?.recommended?.request ? Math.round((1 - cpu.recommended.request / cpu.allocated.request) * 100) : 0;
  const memPct = mem?.allocated?.request && mem?.recommended?.request ? Math.round((1 - mem.recommended.request / mem.allocated.request) * 100) : 0;
  if (cpuPct > 0 || memPct > 0) {
    const parts = [];
    if (cpuPct > 0) parts.push(cpuPct + '% more CPU');
    if (memPct > 0) parts.push(memPct + '% more memory');
    return withSavings('This workload is requesting ' + parts.join(' and ') + ' than it actually uses.', 'Right-sizing would', savingsStr);
  }
  return withSavings("Resource requests don't match actual usage.", 'Adjusting', savingsStr);
};

const describeCertExpiry = (recData: any): string => {
  const daysLeft = recData?.days_until_expiry;
  if (daysLeft == null) return 'Certificate is approaching expiry. Renew to avoid service disruption.';
  const plural = daysLeft !== 1 ? 's' : '';
  return 'Certificate expires in ' + daysLeft + ' day' + plural + '. Renew before it causes outages.';
};

const describeDeprecatedApi = (recData: any): string => {
  const api = recData?.current_api_version;
  if (!api) return 'A deprecated API version is in use. Migrate to avoid breakage on cluster upgrades.';
  return 'The API version ' + api + ' is deprecated and will be removed in a future release. Migrate before upgrading.';
};

const isClusterUpgradeRule = (rule: string): boolean =>
  rule.includes('cluster_upgrade') || rule.includes('eks_cluster') || rule.includes('aks_cluster') || rule.includes('gke_cluster');

const describeClusterUpgrade = (recData: any): string => {
  const from = recData?.current_version;
  const to = recData?.recommended_version;
  if (from && to) return 'Running ' + from + ', which is behind the recommended ' + to + '. Upgrading brings security patches and stability fixes.';
  return 'A newer cluster version is available with security and stability improvements.';
};

const describeImageScan = (recData: any): string => {
  const crit = recData?.critical_count || recData?.Findings?.filter?.((f: any) => f.Severity === 'CRITICAL')?.length;
  if (!crit) return 'Image scan found vulnerabilities that should be reviewed and patched.';
  const plural = crit !== 1 ? 'ies' : 'y';
  return 'Image scan found ' + crit + ' critical vulnerabilit' + plural + '. Patch or rebuild to reduce exposure.';
};

const mapCategoryForFallback = (apiCat: string): string => {
  switch (apiCat) {
    case 'RightSizing':
    case 'Cost':
    case 'K8sSpotRecommendation':
      return 'cost';
    default:
      return 'config';
  }
};

const CATEGORY_FALLBACK_SUFFIX: Record<string, [string, string]> = {
  cost: ['Addressing this', ''],
  security: ['', '. Review and remediate to reduce risk.'],
  config: ['', '. Fixing this improves reliability.'],
};

const getAgenticDescription = (apiRec: any): string => {
  const rule = apiRec.rule_name || '';
  const savingsStr = formatSavings(apiRec.estimated_savings);
  const recData = safeParseJSON(apiRec.recommendation);

  if (rule === 'pod_right_sizing' || rule === 'vertical-rightsizing') {
    return describeRightSizing(recData, savingsStr);
  }

  const simple = SIMPLE_RULE_DESCRIPTIONS[rule];
  if (simple) return withSavings(simple[0], simple[1], savingsStr);

  if (rule === 'certificate_expiry') return describeCertExpiry(recData);
  if (rule === 'k8s_api_deprecated') return describeDeprecatedApi(recData);
  if (isClusterUpgradeRule(rule)) return describeClusterUpgrade(recData);
  if (rule === 'image_scan') return describeImageScan(recData);

  const cat = mapCategoryForFallback(apiRec.category);
  const brief = getRecommendationBrief(apiRec) || formatRuleName(rule);
  const fallback = CATEGORY_FALLBACK_SUFFIX[cat];
  if (fallback?.[0]) return withSavings(brief + '.', fallback[0], savingsStr);
  if (fallback) return brief + fallback[1];
  return withSavings(brief + '.', 'Potential savings:', savingsStr);
};

// ─── Confidence derivation ────────────────────────────────────────────────

const SEV_CONFIDENCE: Record<string, number> = { critical: 95, high: 85, medium: 70, low: 55, info: 40 };

const computeConfidence = (apiRec: any): number => {
  if (apiRec.finops_score != null) return Math.min(100, Math.max(0, apiRec.finops_score));
  const base = SEV_CONFIDENCE[(apiRec.severity || 'medium').toLowerCase()] ?? 70;
  return apiRec.estimated_savings > 0 ? Math.min(100, base + 5) : base;
};

// ─── Environment heuristic ────────────────────────────────────────────────

const deriveEnvironment = (accountName: string): Environment => {
  const lower = (accountName || '').toLowerCase();
  if (lower.includes('prod')) return 'prod';
  if (lower.includes('stag')) return 'staging';
  if (lower.includes('dev')) return 'dev';
  if (lower.includes('sandbox')) return 'sandbox';
  return 'prod';
};

// ─── Next step label ──────────────────────────────────────────────────────

const getNextStepLabel = (category: MainCategory, subCategory: SubCategory): string => {
  if (category === 'cost') {
    switch (subCategory) {
      case 'rightsizing':
        return 'Review sizing';
      case 'abandoned':
        return 'Clean up';
      case 'savings':
        return 'Model savings';
      case 'anomalies':
        return 'Investigate';
    }
  }
  if (category === 'performance') return 'Fix performance';
  switch (subCategory) {
    case 'security_vulnerability':
      return 'Remediate';
    case 'critical_config':
      return 'Fix config';
    case 'compliance':
      return 'Enable compliance';
    case 'drift':
      return 'Update version';
  }
  return 'View details';
};

// ─── Provider mapping ─────────────────────────────────────────────────────

const toProvider = (cloudProvider: string): Provider => {
  const lower = (cloudProvider || '').toLowerCase();
  if (lower === 'aws' || lower === 'amazon') return 'aws';
  if (lower === 'azure' || lower === 'microsoft') return 'azure';
  if (lower === 'gcp' || lower === 'google') return 'gcp';
  return 'k8s';
};

// ─── Main transformer ─────────────────────────────────────────────────────

export function transformApiToInsight(
  apiRec: any,
  accountsMap: Record<string, { account_name: string; cloud_provider: string }>,
  currencySymbols?: Record<string, string>
): InsightItem {
  const { category, subCategory } = mapCategoryAndSubCategory(apiRec.category, apiRec.rule_name);
  const severity = (apiRec.severity || 'medium').toLowerCase();
  const acct = accountsMap[apiRec.account_id];
  const provider = toProvider(acct?.cloud_provider || '');
  const savings = apiRec.estimated_savings > 0 ? Math.round(apiRec.estimated_savings) : 0;
  // Use created_at so age reflects how long the finding has been open —
  // updated_at gets bumped every time the finops-score recompute cron runs
  // (~every 6 hours), which makes almost everything look like "Today".
  const ageSource = apiRec.created_at || apiRec.updated_at;
  const recencyHours = ageSource ? (Date.now() - new Date(ageSource).getTime()) / (1000 * 60 * 60) : 0;
  // Keep fractional days so formatAge can render sub-day durations as hours/minutes.
  const ageDays = Math.max(0, recencyHours / 24);
  const resourceName = getResourceDisplayName(apiRec);

  return {
    id: apiRec.id,
    category,
    subCategory,
    severity,
    resourceId: apiRec.account_object_id || apiRec.resource_id || apiRec.resource_name || '',
    resourceName,
    provider,
    env: deriveEnvironment(acct?.account_name || ''),
    region: apiRec.resource_k8s_namespace || apiRec.cloud_resourse?.meta?.region || '',
    impactValue: savings > 0 ? `${currencySymbols?.[apiRec.account_id] || '$'}${savings.toLocaleString()}/mo` : '',
    impactLabel: savings > 0 ? 'savings potential' : '',
    dollarImpact: savings,
    summary: getAgenticDescription(apiRec),
    nextStep: { label: getNextStepLabel(category, subCategory) },
    ageDays,
    confidence: computeConfidence(apiRec),
    owner: null,
    accountId: apiRec.account_id,
    accountName: acct?.account_name || apiRec.account_id || '',
    _raw: apiRec,
  };
}
