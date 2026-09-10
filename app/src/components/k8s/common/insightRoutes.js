/**
 * Returns a route for a given insight.
 *
 * The backend (Go `computeRedirectURL`) is the single source of truth for insight
 * deep-links: it writes a complete, filter-applied relative URL to
 * `rule.redirect_url` at insight-generation time and recomputes it live on the
 * insight-list endpoint. This helper hands that value back untouched.
 *
 * `buildRouteFromLabel` remains only as a fallback for legacy insight rows
 * written before `redirect_url` existed — their `rule` jsonb has no
 * `redirect_url`, and a fresh Insight refresh backfills it.
 *
 * @param {string} label - The insight title/label text (used only by the legacy fallback)
 * @param {string} accountId - The account ID
 * @param {string} [cloudProvider='K8s'] - Cloud provider: 'K8s', 'aws', 'azure', 'gcp'
 * @param {object} [rule=null] - The insight rule object; carries `redirect_url`
 */
export const getInsightRoute = (label, accountId, cloudProvider = 'K8s', rule = null) => {
  if (rule?.redirect_url) {
    return rule.redirect_url;
  }

  const isK8s = cloudProvider === 'K8s';
  const base = isK8s ? `/kubernetes/details/${accountId}?accountId=${accountId}` : `/cloud-account/details/${accountId}?accountId=${accountId}`;
  return buildRouteFromLabel(label, base, isK8s);
};

// Legacy fallback for insights without a backend-computed redirect_url.
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
