// Static list of navigable pages for the global header search box. Sub-pages
// under /troubleshoot, /optimise, /tickets, /user-management are hash-routed
// tabs (see each page's `filterOptions`/`tabOptions`), not real Next.js
// routes, so they're enumerated here by hand rather than derived from the
// router.
export interface NavSearchPage {
  group: string;
  label: string;
  path: string;
}

// Short acronym derived from a nav-search row's slash-joined path (e.g.
// "aws/rds/summary" -> "ars", "user-management/users" -> "umu") — first
// letter of every hyphen-separated word across all path segments. Lets a
// user type the short form instead of the full path; callers fold this into
// `searchText` alongside the label so it's matched but never displayed.
export const pathAcronym = (path: string): string =>
  path
    .split('/')
    .flatMap((segment) => segment.split('-'))
    .filter(Boolean)
    .map((word) => word[0])
    .join('')
    .toLowerCase();

// Restricted edit distance (Damerau-Levenshtein / "optimal string alignment")
// — like Levenshtein but an adjacent-character swap costs 1 instead of 2, so
// a transposition typo like "rigth" for "right" still counts as a single
// edit. Used only as a last-resort fallback in fuzzyTokenMatches below, once
// a query token has already failed a plain substring check — the global
// search's option pool is small (~200 rows) so the O(len(a)*len(b)) cost per
// comparison is negligible.
const editDistance = (a: string, b: string): number => {
  const m = a.length;
  const n = b.length;
  if (m === 0) return n;
  if (n === 0) return m;
  const d = Array.from({ length: m + 1 }, () => Array.from({ length: n + 1 }, () => 0));
  for (let i = 0; i <= m; i++) d[i][0] = i;
  for (let j = 0; j <= n; j++) d[0][j] = j;
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      d[i][j] = Math.min(d[i - 1][j] + 1, d[i][j - 1] + 1, d[i - 1][j - 1] + cost);
      if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) {
        d[i][j] = Math.min(d[i][j], d[i - 2][j - 2] + cost);
      }
    }
  }
  return d[m][n];
};

// Splits a haystack string into its individual words (alphanumeric runs) so a
// typo'd query token can be fuzzy-matched against one word at a time rather
// than the whole string — comparing "servics" against "aws services summary"
// as one blob would never be within a small edit distance.
export const wordsOf = (str: string): string[] => str.split(/[^a-zA-Z0-9]+/).filter(Boolean);

// A query token is only attempted against fuzzy matching once it's long
// enough that a small edit distance still means something (a 1-2 char token
// is within distance 1 of huge swaths of unrelated words). 3 is as low as
// this goes without real collisions: distinct 3-letter acronyms in this
// corpus (ec2/ecs, gcp/gcs, sql/ssl) sit at edit-distance 1 from each other,
// so e.g. "ec2" can now also surface an ECS row — acceptable since fuzzy
// matches always sort in the lowest rank tier (see rankOf in
// GlobalPageSearch.jsx), below every exact/prefix/substring hit, so they
// only ever add a trailing suggestion, never bury the right one. Distance
// budget grows with token length so a longer misspelled word still matches.
export const MIN_FUZZY_TOKEN_LENGTH = 3;
export const fuzzyTokenMatches = (token: string, words: string[]): boolean => {
  const maxDistance = token.length <= 5 ? 1 : 2;
  return words.some((word) => {
    if (Math.abs(word.length - token.length) <= maxDistance && editDistance(token, word) <= maxDistance) {
      return true;
    }
    if (word.length <= token.length) {
      return false;
    }
    // The word-length check above rejects a token that's a near-complete
    // typed word but diverges from a *longer* haystack word only near or
    // after the token's own end (e.g. "secure" vs "security" — edit
    // distance 3 across the whole words, but 1 once "security"'s extra
    // length is set aside). Try a few candidate prefix lengths of `word`
    // around token.length rather than one fixed cut: exactly token.length
    // is right when the token simply stops short of the real word, but a
    // dropped/extra character *inside* the token (e.g. "sevic" missing the
    // "r" in "services") shifts where the two strings actually realign, so
    // a single fixed-width slice can't catch both.
    for (let prefixLen = token.length; prefixLen <= Math.min(word.length, token.length + maxDistance); prefixLen++) {
      if (editDistance(token, word.slice(0, prefixLen)) <= maxDistance) {
        return true;
      }
    }
    return false;
  });
};

export const navSearchPages: NavSearchPage[] = [
  { group: 'Troubleshoot', label: 'Troubleshoot All Events', path: '/troubleshoot#all-events/all' },
  { group: 'Troubleshoot', label: 'Troubleshoot Triage Inbox', path: '/troubleshoot#all-events/fingerprint' },
  { group: 'Troubleshoot', label: 'Troubleshoot Events group by type', path: '/troubleshoot#all-events/event-type' },
  { group: 'Troubleshoot', label: 'Troubleshoot Events group by app', path: '/troubleshoot#all-events/event-app' },
  { group: 'Troubleshoot', label: 'Troubleshoot Triage Rules', path: '/troubleshoot#all-events/triage-rules' },
  { group: 'Troubleshoot', label: 'Alert Tuning', path: '/troubleshoot#all-events/threshold-suggestions' },
  { group: 'Troubleshoot', label: 'Event Resolutions', path: '/troubleshoot#all-events/event-resolutions' },
  { group: 'Troubleshoot', label: 'Auto Investigated', path: '/troubleshoot#investigations/auto-investigated' },
  { group: 'Troubleshoot', label: 'Manual Investigated', path: '/troubleshoot#investigations/manual-investigated' },
  { group: 'Troubleshoot', label: 'Knowledge Graph', path: '/troubleshoot#kg' },

  { group: 'Optimize', label: 'Optimize Summary', path: '/optimise#summary' },
  { group: 'Optimize', label: 'Optimize Recommendations', path: '/optimise#recommendations' },
  { group: 'Optimize', label: 'Optimize Resolutions', path: '/optimise#resolutions' },
  { group: 'Optimize', label: 'Auto Optimize - Optimizations', path: '/optimise#auto-optimize/optimizations' },
  { group: 'Optimize', label: 'Auto Optimize - Approvals', path: '/optimise#auto-optimize/approvals' },

  { group: 'Tickets', label: 'All Tickets', path: '/tickets#tickets' },
  { group: 'Tickets', label: 'All Tickets - Assigned to me', path: '/tickets#assigned-me' },

  { group: 'Admin', label: 'Users', path: '/user-management#users' },
  { group: 'Admin', label: 'Groups', path: '/user-management#groups' },
  { group: 'Admin', label: 'Audits', path: '/user-management#audits' },
  { group: 'Admin', label: 'Notifications', path: '/user-management#notifications' },
  { group: 'Admin', label: 'Integrations', path: '/user-management#integrations' },
  { group: 'Admin', label: 'Ownership', path: '/user-management#ownership' },
];

// Kubernetes Details (/kubernetes/details/[KubernetesDetails]) tabs, kept
// separate from navSearchPages above because its route needs an accountId the
// caller must resolve at render time — see each tab's `tabOptions`/fragment in
// src/pages/kubernetes/details/[KubernetesDetails].jsx. `fragment` is appended
// after `/kubernetes/details/{accountId}#`. `label` is the hardcoded row title
// shown to the user; `slug` is the slash-joined path used for the row's `type`
// chip, its searchText, and its acronym.
export interface K8sDetailsSearchFragment {
  label: string;
  slug: string;
  fragment: string;
}

export const k8sDetailsSearchFragments: K8sDetailsSearchFragment[] = [
  { label: 'K8s Summary', slug: 'k8s/summary', fragment: 'summary' },

  { label: 'K8s Optimize Summary', slug: 'k8s/optimize/summary', fragment: 'optimize/summary' },
  { label: 'K8s Optimize - Right Sizing', slug: 'k8s/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'Auto Scaler', slug: 'k8s/optimize/auto-scaler', fragment: 'optimize/auto-scaler' },
  { label: 'Unused Volume', slug: 'k8s/optimize/unused-volume', fragment: 'optimize/unused-volume' },
  { label: 'Best Practices', slug: 'k8s/optimize/best-practices', fragment: 'optimize/best-practices' },
  { label: 'Abandoned Resources', slug: 'k8s/optimize/abandoned-resources', fragment: 'optimize/abandoned-resources' },
  { label: 'PV Rightsizing', slug: 'k8s/optimize/pv-rightsizing', fragment: 'optimize/pv-rightsizing' },
  { label: 'Replica Rightsizing', slug: 'k8s/optimize/replica-rightsizing', fragment: 'optimize/replica-rightsizing' },
  { label: 'Spot Recommendation', slug: 'k8s/optimize/spot-recommendation', fragment: 'optimize/spot-recommendation' },
  {
    label: 'K8s Optimize - Recommendation Resolution',
    slug: 'k8s/optimize/recommendation-resolution',
    fragment: 'optimize/recommendation-resolution',
  },

  { label: 'K8s Events Summary', slug: 'k8s/events/summary', fragment: 'events/summary' },
  { label: 'K8s Triage Inbox', slug: 'k8s/events/inbox', fragment: 'events/inbox' },
  { label: 'Events - By Type', slug: 'k8s/events/grouped-events', fragment: 'events/grouped-events' },
  { label: 'Pod Errors', slug: 'k8s/events/pod-errors', fragment: 'events/pod-errors' },
  { label: 'Node Errors', slug: 'k8s/events/node-errors', fragment: 'events/node-errors' },
  { label: 'App Errors', slug: 'k8s/events/app-errors', fragment: 'events/app-errors' },
  { label: 'All Events', slug: 'k8s/events/all-events', fragment: 'events/all-events' },
  { label: 'Anomaly', slug: 'k8s/events/anomaly', fragment: 'events/anomaly' },
  { label: 'K8s Triage Rules', slug: 'k8s/events/triage-rules', fragment: 'events/triage-rules' },
  { label: 'Service Criticality', slug: 'k8s/events/service-criticality', fragment: 'events/service-criticality' },

  { label: 'Nodes', slug: 'k8s/apps-infra/nodes', fragment: 'kubernetes/nodes' },
  { label: 'Applications', slug: 'k8s/apps-infra/applications', fragment: 'kubernetes/applications' },
  { label: 'Pods', slug: 'k8s/apps-infra/pods', fragment: 'kubernetes/pods' },
  { label: 'Namespaces', slug: 'k8s/apps-infra/namespaces', fragment: 'kubernetes/namespaces' },
  { label: 'K8s Services', slug: 'k8s/apps-infra/services', fragment: 'kubernetes/services' },
  { label: 'PVC', slug: 'k8s/apps-infra/pvc', fragment: 'kubernetes/pvc' },
  { label: 'PV', slug: 'k8s/apps-infra/pv', fragment: 'kubernetes/pv' },
  { label: 'DBMS', slug: 'k8s/apps-infra/dbms', fragment: 'kubernetes/dbms' },
  { label: 'Queue', slug: 'k8s/apps-infra/queue', fragment: 'kubernetes/queue' },

  { label: 'Query Logs', slug: 'k8s/monitoring/query-log', fragment: 'monitoring/logs' },
  { label: 'Log Groups', slug: 'k8s/monitoring/log-groups', fragment: 'monitoring/groups' },
  { label: 'Query Metrics', slug: 'k8s/monitoring/query', fragment: 'monitoring/query' },
  { label: 'Alert Manager', slug: 'k8s/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'Service Map', slug: 'k8s/monitoring/service-map', fragment: 'monitoring/service-map' },
  { label: 'Traces', slug: 'k8s/monitoring/traces', fragment: 'monitoring/traces' },
  { label: 'Trace Grouping', slug: 'k8s/monitoring/trace-grouping', fragment: 'monitoring/grouping' },
  { label: 'Cross Zone', slug: 'k8s/monitoring/cross-zone', fragment: 'monitoring/cross-zone' },
  { label: 'SLO', slug: 'k8s/monitoring/slo', fragment: 'monitoring/slo' },
  { label: 'Grafana', slug: 'k8s/monitoring/grafana', fragment: 'monitoring/grafana' },

  { label: 'Image Scan', slug: 'k8s/security/image-scan', fragment: 'security/image-scan' },
  { label: 'CIS Scan', slug: 'k8s/security/cis-scan', fragment: 'security/cis-scan' },
  { label: 'Sensitive Logs', slug: 'k8s/security/sensitive-log', fragment: 'security/sensitive-log' },
  { label: 'Cluster Upgrade', slug: 'k8s/security/cluster-upgrade', fragment: 'security/cluster-upgrade' },
  { label: 'Upgrade Planner', slug: 'k8s/security/upgrade-planner', fragment: 'security/upgrade-planner' },
  {
    label: 'SSL Certificate Issues',
    slug: 'k8s/security/ssl-certificate-issues',
    fragment: 'security/ssl-certificate-issues',
  },
  { label: 'Helm Upgrade', slug: 'k8s/security/helm-upgrade', fragment: 'security/helm-upgrade' },
];

// AWS Cloud Account Details (/cloud-account/details/[CloudAccountDetails])
// tabs, kept separate for the same reason as k8sDetailsSearchFragments above
// — the route needs an accountId the caller must resolve at render time. See
// `baseOptions`/`awsOptions` in src/pages/cloud-account/details/[CloudAccountDetails].jsx.
// `fragment` is appended after `/cloud-account/details/{accountId}#`. The
// account-wide Security/Tools tabs are omitted — they're `disabled: true` in
// that page today. `label` is the hardcoded row title shown to the user;
// `slug` is the slash-joined path used for the row's `type` chip, its
// searchText, and its acronym.
export interface AwsDetailsSearchFragment {
  label: string;
  slug: string;
  fragment: string;
}

export const awsDetailsSearchFragments: AwsDetailsSearchFragment[] = [
  { label: 'AWS Summary', slug: 'aws/summary', fragment: 'summary' },

  { label: 'AWS Optimize - Right Sizing', slug: 'aws/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'AWS Optimize - Configuration', slug: 'aws/optimize/configuration', fragment: 'optimize/configuration' },
  { label: 'AWS Optimize - Security', slug: 'aws/optimize/security', fragment: 'optimize/security' },
  { label: 'AWS Optimize - Infra Upgrade', slug: 'aws/optimize/infra-upgrade', fragment: 'optimize/infra-upgrade' },
  {
    label: 'AWS Optimize - Recommendation Resolution',
    slug: 'aws/optimize/recommendation-resolution',
    fragment: 'optimize/recommendation-resolution',
  },

  { label: 'AWS Services', slug: 'aws/services', fragment: 'services' },

  { label: 'AWS Events', slug: 'aws/troubleshoot/events', fragment: 'events/events' },
  { label: 'AWS Triage Rules', slug: 'aws/troubleshoot/triage-rules', fragment: 'events/triage-rules' },
  {
    label: 'Threshold Suggestions',
    slug: 'aws/troubleshoot/threshold-suggestions',
    fragment: 'events/threshold-suggestions',
  },

  { label: 'Alert Manager', slug: 'aws/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'Cloud Logs', slug: 'aws/monitoring/cloud-logs', fragment: 'monitoring/cloud-logs' },
  { label: 'Metrics', slug: 'aws/monitoring/metrics', fragment: 'monitoring/metrics' },

  { label: 'EC2 Summary', slug: 'aws/ec2/summary', fragment: 'ec2/summary' },
  { label: 'EC2 Optimize', slug: 'aws/ec2/optimize', fragment: 'ec2/optimize' },
  { label: 'EC2 Events', slug: 'aws/ec2/events', fragment: 'ec2/events' },
  { label: 'EC2 Instances', slug: 'aws/ec2/instances', fragment: 'ec2/instances' },

  { label: 'RDS Summary', slug: 'aws/rds/summary', fragment: 'rds/summary' },
  { label: 'RDS Optimize', slug: 'aws/rds/optimize', fragment: 'rds/optimize' },
  { label: 'RDS Events', slug: 'aws/rds/events', fragment: 'rds/events' },
  { label: 'RDS Instances', slug: 'aws/rds/instances', fragment: 'rds/instances' },

  { label: 'S3 Summary', slug: 'aws/s3/summary', fragment: 's3/summary' },
  { label: 'S3 Optimize', slug: 'aws/s3/optimize', fragment: 's3/optimize' },
  { label: 'S3 Events', slug: 'aws/s3/events', fragment: 's3/events' },
  { label: 'S3 Instances', slug: 'aws/s3/instances', fragment: 's3/instances' },

  { label: 'ECS Summary', slug: 'aws/ecs/summary', fragment: 'ecs/summary' },
  { label: 'ECS Optimize', slug: 'aws/ecs/optimize', fragment: 'ecs/optimize' },
  { label: 'ECS Events', slug: 'aws/ecs/events', fragment: 'ecs/events' },
  { label: 'ECS Instances', slug: 'aws/ecs/instances', fragment: 'ecs/instances' },
];

// Azure Cloud Account Details tabs — same route/shape as awsDetailsSearchFragments
// above, see `baseOptions`/`azureOptions` in
// src/pages/cloud-account/details/[CloudAccountDetails].jsx. `label` is the
// hardcoded row title shown to the user; `slug` is the slash-joined path used
// for the row's `type` chip, its searchText, and its acronym.
export interface AzureDetailsSearchFragment {
  label: string;
  slug: string;
  fragment: string;
}

export const azureDetailsSearchFragments: AzureDetailsSearchFragment[] = [
  { label: 'Azure Summary', slug: 'azure/summary', fragment: 'summary' },

  { label: 'Azure Optimize - Right Sizing', slug: 'azure/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'Azure Optimize - Configuration', slug: 'azure/optimize/configuration', fragment: 'optimize/configuration' },
  { label: 'Azure Optimize - Security', slug: 'azure/optimize/security', fragment: 'optimize/security' },
  { label: 'Azure Optimize - Infra Upgrade', slug: 'azure/optimize/infra-upgrade', fragment: 'optimize/infra-upgrade' },
  {
    label: 'Azure Optimize - Recommendation Resolution',
    slug: 'azure/optimize/recommendation-resolution',
    fragment: 'optimize/recommendation-resolution',
  },

  { label: 'Azure Services', slug: 'azure/services', fragment: 'services' },

  { label: 'Azure Events', slug: 'azure/troubleshoot/events', fragment: 'events/events' },
  { label: 'Azure Triage Rules', slug: 'azure/troubleshoot/triage-rules', fragment: 'events/triage-rules' },
  {
    label: 'Threshold Suggestions',
    slug: 'azure/troubleshoot/threshold-suggestions',
    fragment: 'events/threshold-suggestions',
  },

  { label: 'Alert Manager', slug: 'azure/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'Cloud Logs', slug: 'azure/monitoring/cloud-logs', fragment: 'monitoring/cloud-logs' },
  { label: 'Metrics', slug: 'azure/monitoring/metrics', fragment: 'monitoring/metrics' },

  { label: 'VM Summary', slug: 'azure/vm/summary', fragment: 'vm/summary' },
  { label: 'VM Optimize', slug: 'azure/vm/optimize', fragment: 'vm/optimize' },
  { label: 'VM Events', slug: 'azure/vm/events', fragment: 'vm/events' },
  { label: 'VM Instances', slug: 'azure/vm/instances', fragment: 'vm/instances' },

  { label: 'SQL Summary', slug: 'azure/sql/summary', fragment: 'sql/summary' },
  { label: 'SQL Optimize', slug: 'azure/sql/optimize', fragment: 'sql/optimize' },
  { label: 'SQL Events', slug: 'azure/sql/events', fragment: 'sql/events' },
  { label: 'SQL Instances', slug: 'azure/sql/instances', fragment: 'sql/instances' },

  { label: 'SQL Summary', slug: 'azure/sql-mi/summary', fragment: 'sql-mi/summary' },
  { label: 'SQL Optimize', slug: 'azure/sql-mi/optimize', fragment: 'sql-mi/optimize' },
  { label: 'SQL Events', slug: 'azure/sql-mi/events', fragment: 'sql-mi/events' },
  { label: 'SQL Instances', slug: 'azure/sql-mi/instances', fragment: 'sql-mi/instances' },

  { label: 'Blob Summary', slug: 'azure/blob/summary', fragment: 'blob/summary' },
  { label: 'Blob Optimize', slug: 'azure/blob/optimize', fragment: 'blob/optimize' },
  { label: 'Blob Events', slug: 'azure/blob/events', fragment: 'blob/events' },
  { label: 'Blob Instances', slug: 'azure/blob/instances', fragment: 'blob/instances' },
];

// GCP Cloud Account Details tabs — same route/shape as awsDetailsSearchFragments
// above, see `baseOptions`/`gcpOptions` in
// src/pages/cloud-account/details/[CloudAccountDetails].jsx. `monitoring/traces`
// is GCP-only today (Cloud Trace integration, not yet wired for other providers).
// `label` is the hardcoded row title shown to the user; `slug` is the
// slash-joined path used for the row's `type` chip, its searchText, and its
// acronym.
export interface GcpDetailsSearchFragment {
  label: string;
  slug: string;
  fragment: string;
}

export const gcpDetailsSearchFragments: GcpDetailsSearchFragment[] = [
  { label: 'GCP Summary', slug: 'gcp/summary', fragment: 'summary' },

  { label: 'GCP Optimize - Right Sizing', slug: 'gcp/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'GCP Optimize - Configuration', slug: 'gcp/optimize/configuration', fragment: 'optimize/configuration' },
  { label: 'GCP Optimize - Security', slug: 'gcp/optimize/security', fragment: 'optimize/security' },
  { label: 'GCP Optimize - Infra Upgrade', slug: 'gcp/optimize/infra-upgrade', fragment: 'optimize/infra-upgrade' },
  {
    label: 'GCP Optimize - Recommendation Resolution',
    slug: 'gcp/optimize/recommendation-resolution',
    fragment: 'optimize/recommendation-resolution',
  },

  { label: 'GCP Services', slug: 'gcp/services', fragment: 'services' },

  { label: 'GCP Events', slug: 'gcp/troubleshoot/events', fragment: 'events/events' },
  { label: 'GCP Triage Rules', slug: 'gcp/troubleshoot/triage-rules', fragment: 'events/triage-rules' },
  {
    label: 'Threshold Suggestions',
    slug: 'gcp/troubleshoot/threshold-suggestions',
    fragment: 'events/threshold-suggestions',
  },

  { label: 'Alert Manager', slug: 'gcp/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'Cloud Logs', slug: 'gcp/monitoring/cloud-logs', fragment: 'monitoring/cloud-logs' },
  { label: 'Metrics', slug: 'gcp/monitoring/metrics', fragment: 'monitoring/metrics' },
  { label: 'Traces', slug: 'gcp/monitoring/traces', fragment: 'monitoring/traces' },

  { label: 'Compute Engine Summary', slug: 'gcp/compute-engine/summary', fragment: 'compute-engine/summary' },
  { label: 'Compute Engine Optimize', slug: 'gcp/compute-engine/optimize', fragment: 'compute-engine/optimize' },
  { label: 'Compute Engine Events', slug: 'gcp/compute-engine/events', fragment: 'compute-engine/events' },
  { label: 'Compute Engine Instances', slug: 'gcp/compute-engine/instances', fragment: 'compute-engine/instances' },

  { label: 'Cloud SQL Summary', slug: 'gcp/cloud-sql/summary', fragment: 'cloud-sql/summary' },
  { label: 'Cloud SQL Optimize', slug: 'gcp/cloud-sql/optimize', fragment: 'cloud-sql/optimize' },
  { label: 'Cloud SQL Events', slug: 'gcp/cloud-sql/events', fragment: 'cloud-sql/events' },
  { label: 'Cloud SQL Instances', slug: 'gcp/cloud-sql/instances', fragment: 'cloud-sql/instances' },

  { label: 'Cloud Storage Summary', slug: 'gcp/cloud-storage/summary', fragment: 'cloud-storage/summary' },
  { label: 'Cloud Storage Optimize', slug: 'gcp/cloud-storage/optimize', fragment: 'cloud-storage/optimize' },
  { label: 'Cloud Storage Events', slug: 'gcp/cloud-storage/events', fragment: 'cloud-storage/events' },
  { label: 'Cloud Storage Instances', slug: 'gcp/cloud-storage/instances', fragment: 'cloud-storage/instances' },
];
