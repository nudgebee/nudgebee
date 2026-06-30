/**
 * Returns a route for a given insight, using the rule object from insight_rules.json
 * to derive filters (eventAggregationKey, source, time range, etc.) instead of
 * hardcoding them based on label text.
 *
 * @param {string} label - The insight title/label text
 * @param {string} accountId - The account ID
 * @param {string} [cloudProvider='K8s'] - Cloud provider: 'K8s', 'aws', 'azure', 'gcp'
 * @param {object} [rule=null] - The insight rule object from insight_rules.json
 */
export const getInsightRoute = (label, accountId, cloudProvider = 'K8s', rule = null) => {
  // Use backend-computed redirect_url if available
  if (rule?.redirect_url) {
    return rule.redirect_url;
  }

  const isK8s = cloudProvider === 'K8s';
  const base = isK8s ? `/kubernetes/details/${accountId}?accountId=${accountId}` : `/cloud-account/details/${accountId}?accountId=${accountId}`;

  // If we have a rule object, build the route from its fields
  if (rule) {
    return buildRouteFromRule(base, rule, isK8s);
  }

  // Legacy fallback: label-based matching (for data without rule object)
  return buildRouteFromLabel(label, base, isK8s);
};

function getTimeParams(rule) {
  if (!rule) return '';
  const source = rule.source;
  if (source !== 'Event' && source !== 'Prometheus') return '';

  const now = Date.now();
  let rangeMs = 24 * 60 * 60 * 1000; // default 1 day
  const range = rule.range > 0 ? rule.range : 1;
  const unit = rule.range_unit || 'DAY';

  switch (unit) {
    case 'HOUR':
      rangeMs = range * 60 * 60 * 1000;
      break;
    case 'WEEK':
      rangeMs = range * 7 * 24 * 60 * 60 * 1000;
      break;
    case 'MONTH':
      rangeMs = range * 30 * 24 * 60 * 60 * 1000;
      break;
    default:
      rangeMs = range * 24 * 60 * 60 * 1000;
  }
  return `&start_time=${now - rangeMs}&end_time=${now}`;
}

function getFilterValue(rule, column) {
  if (!rule?.filters) return null;
  for (const f of rule.filters) {
    if (f.column === column) {
      return Array.isArray(f.value) ? f.value[0] : f.value;
    }
  }
  return null;
}

function getFilterValues(rule, column) {
  if (!rule?.filters) return [];
  for (const f of rule.filters) {
    if (f.column === column) {
      return Array.isArray(f.value) ? f.value : [f.value];
    }
  }
  return [];
}

function getUIFilterValue(rule, name) {
  if (!rule?.ui_filters) return null;
  for (const f of rule.ui_filters) {
    if (f.name === name) return f.value;
  }
  return null;
}

function buildRouteFromRule(base, rule, isK8s) {
  const tp = getTimeParams(rule);
  const source = rule.source;
  const category = rule.category;
  const subcategory = rule.subcategory;
  const aggKey = getUIFilterValue(rule, 'aggregation_key') || getFilterValue(rule, 'aggregation_key');
  const filterSource = getFilterValue(rule, 'source');
  const filterSources = getFilterValues(rule, 'source');
  const ruleName = getFilterValue(rule, 'rule_name');

  // --- Event source ---
  if (source === 'Event') {
    if (isK8s) {
      return buildK8sEventRoute(base, tp, aggKey, filterSource, filterSources, subcategory, rule);
    }
    // Cloud events
    if (filterSources.length > 0) {
      return `${base}&source=${filterSources.join(',')}&eventStatus=FIRING${tp}#events/events`;
    }
    return `${base}&eventStatus=FIRING${tp}#events/events`;
  }

  // --- Prometheus source ---
  if (source === 'Prometheus' && isK8s) {
    return buildK8sPrometheusRoute(base, subcategory, aggKey, tp);
  }

  // --- Recommendation source ---
  if (source === 'Recommendation') {
    const ruleNames = getFilterValues(rule, 'rule_name');
    if (isK8s) {
      return buildK8sRecommendationRoute(base, ruleName, category, rule.unique_id);
    }
    return buildCloudRecommendationRoute(base, ruleNames, category, rule.unique_id);
  }

  // --- Spends source ---
  if (source === 'Spends') {
    return isK8s ? `${base}#summary` : `${base}#summary`;
  }

  // --- Metrics source ---
  if (source === 'Metrics') {
    return isK8s ? `${base}${tp}#events/all-events` : `${base}${tp}#services`;
  }

  // --- Trace-related types ---
  if (rule.type === 'TraceAggregation') {
    return `${base}${tp}#monitoring/traces`;
  }

  // --- EventAggregation (K8s) ---
  if (rule.type === 'EventAggregation') {
    if (aggKey) {
      return `${base}&eventAggregationKey=${aggKey}&eventStatus=FIRING${tp}#events/all-events`;
    }
    return `${base}&eventStatus=FIRING${tp}#events/all-events`;
  }

  return `${base}#summary`;
}

function buildK8sEventRoute(base, tp, aggKey, filterSource, filterSources, subcategory, rule) {
  // Webhook sources (PagerDuty, Datadog, ServiceNow, Zenduty, SLO)
  if (
    filterSource === 'pagerduty_webhook' ||
    filterSource === 'datadog_webhook' ||
    filterSource === 'servicenow_webhook' ||
    filterSource === 'zenduty_webhook'
  ) {
    return `${base}&source=${filterSources.join(',')}&eventStatus=FIRING&sortBy=computed_score${tp}#events/all-events`;
  }
  if (filterSource === 'slo') {
    return `${base}&eventAggregationKey=SLOViolation&eventStatus=FIRING${tp}#events/all-events`;
  }
  if (filterSource === 'AWS_CloudWatch_Alarm') {
    return `${base}&source=${filterSources.join(',')}&eventStatus=FIRING${tp}#monitoring/alert-manager`;
  }
  if (filterSource === 'Azure_Monitor_Alert' || filterSource === 'azure_monitor_webhook') {
    return `${base}&source=${filterSources.join(',')}&eventStatus=FIRING${tp}#monitoring/alert-manager`;
  }

  // Aggregation key based events
  if (aggKey) {
    return `${base}&eventAggregationKey=${aggKey}&eventStatus=FIRING${tp}#events/all-events`;
  }

  // High-priority / generic events
  const status = getFilterValue(rule, 'status');
  if (status === 'FIRING') {
    return `${base}&eventStatus=FIRING&sortBy=computed_score${tp}#events/all-events`;
  }

  return `${base}&eventStatus=FIRING${tp}#events/all-events`;
}

function buildK8sPrometheusRoute(base, subcategory, aggKey, tp) {
  switch (subcategory) {
    case 'Events':
    case 'Application':
      return aggKey ? `${base}&eventAggregationKey=${aggKey}&eventStatus=FIRING${tp}#events/all-events` : `${base}${tp}#events/all-events`;
    case 'Trace':
      return `${base}#monitoring/traces`;
    case 'LogGroup':
      return `${base}#monitoring/groups`;
    case 'Storage':
      return `${base}#optimize/pv-rightsizing`;
    default:
      return `${base}#summary`;
  }
}

function buildK8sRecommendationRoute(base, ruleName, category, uniqueId) {
  const uid = String(uniqueId ?? '');
  switch (ruleName) {
    case 'pod_right_sizing':
      return `${base}#optimize/right-sizing`;
    case 'unused_pvc':
      return `${base}#optimize/unused-volume`;
    case 'pv_rightsize':
      return `${base}#optimize/pv-rightsizing`;
    case 'abandoned_resource':
      return `${base}#optimize/abandoned-resources`;
    case 'image_scan':
      return `${base}#security/image-scan`;
    case 'eks_cluster_upgrade':
      return `${base}#security/cluster-upgrade`;
    case 'certificate_expiry':
      return `${base}#security/ssl-certificate-issues`;
  }
  switch (category) {
    case 'Security':
      return `${base}#security/image-scan`;
    case 'InfraUpgrade':
      return `${base}#security/cluster-upgrade`;
  }
  // Ratio-type rules without rule_name filters — match by unique_id
  switch (uid) {
    case '17':
      return `${base}#security/image-scan`;
    case '19':
      return `${base}#security/cluster-upgrade`;
  }
  return `${base}#summary`;
}

function buildCloudRecommendationRoute(base, ruleNames, category, uniqueId) {
  const uid = String(uniqueId ?? '');
  const ruleNameParam = ruleNames.length > 0 ? `&ruleName=${ruleNames.join(',')}` : '';
  switch (category) {
    case 'Security':
      return `${base}${ruleNameParam}#optimize/security`;
    case 'Configuration':
      return `${base}${ruleNameParam}#optimize/configuration`;
    case 'InfraUpgrade':
      return `${base}${ruleNameParam}#optimize/infra-upgrade`;
    case 'Optimization':
      return `${base}${ruleNameParam}#optimize/right-sizing`;
    case 'Cost':
      return `${base}#summary`;
    case 'Performance':
      return `${base}#services`;
  }
  // unique_id fallback for Ops rules with specific IDs
  switch (uid) {
    case '110':
    case '111':
    case '112':
      return `${base}${ruleNameParam}#optimize/configuration`;
  }
  if (category === 'Ops') {
    return `${base}${ruleNameParam}#optimize/configuration`;
  }
  return `${base}#summary`;
}

// Legacy fallback for insights without a rule object
function buildRouteFromLabel(label, base, isK8s) {
  const lower = label.toLowerCase();
  const now = Date.now();
  const tp = `&start_time=${now - 24 * 60 * 60 * 1000}&end_time=${now}`;

  if (isK8s) {
    if (lower.includes('right') && lower.includes('sized')) return `${base}#optimize/right-sizing`;
    if (lower.includes('persistent volume') && lower.includes('abandoned')) return `${base}#optimize/unused-volume`;
    if (lower.includes('persistent volume') && lower.includes('rightsized')) return `${base}#optimize/pv-rightsizing`;
    if (lower.includes('abandoned') && lower.includes('no load')) return `${base}#optimize/abandoned-resources`;
    if (lower.includes('upgrading') && lower.includes('eks')) return `${base}#security/cluster-upgrade`;
    if (lower.includes('security vulnerabilit')) return `${base}#security/image-scan`;
    if (lower.includes('certificate') && lower.includes('expir')) return `${base}#security/ssl-certificate-issues`;
    if (lower.includes('api latency')) return `${base}${tp}#monitoring/traces`;
    if (lower.includes('api error rate')) return `${base}${tp}#monitoring/traces`;
  }

  if (!isK8s) {
    if (lower.includes('weekly cloud spend') || lower.includes('estimated monthly spend')) return `${base}#summary`;
    if (lower.includes('wasting') || lower.includes('rightsizing') || (lower.includes('rds') && lower.includes('alternate')))
      return `${base}#optimize/right-sizing`;
    if (
      lower.includes('security vulnerabilit') ||
      lower.includes('publicly accessible') ||
      lower.includes('missing encryption') ||
      (lower.includes('iam') && lower.includes('without mfa'))
    )
      return `${base}#optimize/security`;
    if (lower.includes('infra upgrade')) return `${base}#optimize/infra-upgrade`;
    if (
      lower.includes('configuration issue') ||
      lower.includes('missing backup') ||
      lower.includes('missing high availability') ||
      (lower.includes('missing') && (lower.includes('tag') || lower.includes('label')))
    )
      return `${base}#optimize/configuration`;
  }

  if (
    lower.includes('pagerduty') ||
    lower.includes('datadog') ||
    lower.includes('servicenow') ||
    lower.includes('zenduty') ||
    lower.includes('cloudwatch') ||
    lower.includes('monitor alert')
  ) {
    return `${base}${tp}#monitoring/alert-manager`;
  }

  return null;
}
