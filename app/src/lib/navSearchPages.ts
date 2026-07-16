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

export const navSearchPages: NavSearchPage[] = [
  { group: 'Troubleshoot', label: 'All Events', path: '/troubleshoot#all-events/all' },
  { group: 'Troubleshoot', label: 'Triage Inbox', path: '/troubleshoot#all-events/fingerprint' },
  { group: 'Troubleshoot', label: 'Events group by type', path: '/troubleshoot#all-events/event-type' },
  { group: 'Troubleshoot', label: 'Events group by app', path: '/troubleshoot#all-events/event-app' },
  { group: 'Troubleshoot', label: 'Triage Rules', path: '/troubleshoot#all-events/triage-rules' },
  { group: 'Troubleshoot', label: 'Alert Tuning', path: '/troubleshoot#all-events/threshold-suggestions' },
  { group: 'Troubleshoot', label: 'Event Resolutions', path: '/troubleshoot#all-events/event-resolutions' },
  { group: 'Troubleshoot', label: 'Auto Investigated', path: '/troubleshoot#investigations/auto-investigated' },
  { group: 'Troubleshoot', label: 'Manual Investigated', path: '/troubleshoot#investigations/manual-investigated' },
  { group: 'Troubleshoot', label: 'Knowledge Graph', path: '/troubleshoot#kg' },

  { group: 'Optimize', label: 'Summary', path: '/optimise#summary' },
  { group: 'Optimize', label: 'Recommendations', path: '/optimise#recommendations' },
  { group: 'Optimize', label: 'Resolutions', path: '/optimise#resolutions' },
  { group: 'Optimize', label: 'Auto Optimize - Optimizations', path: '/optimise#auto-optimize/optimizations' },
  { group: 'Optimize', label: 'Auto Optimize - Approvals', path: '/optimise#auto-optimize/approvals' },

  { group: 'Tickets', label: 'All Tickets', path: '/tickets#tickets' },
  { group: 'Tickets', label: 'Assigned to me', path: '/tickets#assigned-me' },

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
// after `/kubernetes/details/{accountId}#`.
export interface K8sDetailsSearchFragment {
  label: string;
  fragment: string;
}

export const k8sDetailsSearchFragments: K8sDetailsSearchFragment[] = [
  { label: 'k8s/summary', fragment: 'summary' },

  { label: 'k8s/optimize/summary', fragment: 'optimize/summary' },
  { label: 'k8s/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'k8s/optimize/auto-scaler', fragment: 'optimize/auto-scaler' },
  { label: 'k8s/optimize/unused-volume', fragment: 'optimize/unused-volume' },
  { label: 'k8s/optimize/best-practices', fragment: 'optimize/best-practices' },
  { label: 'k8s/optimize/abandoned-resources', fragment: 'optimize/abandoned-resources' },
  { label: 'k8s/optimize/pv-rightsizing', fragment: 'optimize/pv-rightsizing' },
  { label: 'k8s/optimize/replica-rightsizing', fragment: 'optimize/replica-rightsizing' },
  { label: 'k8s/optimize/spot-recommendation', fragment: 'optimize/spot-recommendation' },
  { label: 'k8s/optimize/recommendation-resolution', fragment: 'optimize/recommendation-resolution' },

  { label: 'k8s/events/summary', fragment: 'events/summary' },
  { label: 'k8s/events/inbox', fragment: 'events/inbox' },
  { label: 'k8s/events/grouped-events', fragment: 'events/grouped-events' },
  { label: 'k8s/events/pod-errors', fragment: 'events/pod-errors' },
  { label: 'k8s/events/node-errors', fragment: 'events/node-errors' },
  { label: 'k8s/events/app-errors', fragment: 'events/app-errors' },
  { label: 'k8s/events/all-events', fragment: 'events/all-events' },
  { label: 'k8s/events/anomaly', fragment: 'events/anomaly' },
  { label: 'k8s/events/triage-rules', fragment: 'events/triage-rules' },
  { label: 'k8s/events/service-criticality', fragment: 'events/service-criticality' },

  { label: 'k8s/apps-infra/nodes', fragment: 'kubernetes/nodes' },
  { label: 'k8s/apps-infra/applications', fragment: 'kubernetes/applications' },
  { label: 'k8s/apps-infra/pods', fragment: 'kubernetes/pods' },
  { label: 'k8s/apps-infra/namespaces', fragment: 'kubernetes/namespaces' },
  { label: 'k8s/apps-infra/services', fragment: 'kubernetes/services' },
  { label: 'k8s/apps-infra/pvc', fragment: 'kubernetes/pvc' },
  { label: 'k8s/apps-infra/pv', fragment: 'kubernetes/pv' },
  { label: 'k8s/apps-infra/dbms', fragment: 'kubernetes/dbms' },
  { label: 'k8s/apps-infra/queue', fragment: 'kubernetes/queue' },

  { label: 'k8s/monitoring/query-log', fragment: 'monitoring/logs' },
  { label: 'k8s/monitoring/log-groups', fragment: 'monitoring/groups' },
  { label: 'k8s/monitoring/prom-query', fragment: 'monitoring/query' },
  { label: 'k8s/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'k8s/monitoring/service-map', fragment: 'monitoring/service-map' },
  { label: 'k8s/monitoring/traces', fragment: 'monitoring/traces' },
  { label: 'k8s/monitoring/trace-grouping', fragment: 'monitoring/grouping' },
  { label: 'k8s/monitoring/cross-zone', fragment: 'monitoring/cross-zone' },
  { label: 'k8s/monitoring/slo', fragment: 'monitoring/slo' },
  { label: 'k8s/monitoring/grafana', fragment: 'monitoring/grafana' },

  { label: 'k8s/security/image-scan', fragment: 'security/image-scan' },
  { label: 'k8s/security/cis-scan', fragment: 'security/cis-scan' },
  { label: 'k8s/security/sensitive-log', fragment: 'security/sensitive-log' },
  { label: 'k8s/security/cluster-upgrade', fragment: 'security/cluster-upgrade' },
  { label: 'k8s/security/upgrade-planner', fragment: 'security/upgrade-planner' },
  { label: 'k8s/security/ssl-certificate-issues', fragment: 'security/ssl-certificate-issues' },
  { label: 'k8s/security/helm-upgrade', fragment: 'security/helm-upgrade' },
];

// AWS Cloud Account Details (/cloud-account/details/[CloudAccountDetails])
// tabs, kept separate for the same reason as k8sDetailsSearchFragments above
// — the route needs an accountId the caller must resolve at render time. See
// `baseOptions`/`awsOptions` in src/pages/cloud-account/details/[CloudAccountDetails].jsx.
// `fragment` is appended after `/cloud-account/details/{accountId}#`. The
// account-wide Security/Tools tabs are omitted — they're `disabled: true` in
// that page today.
export interface AwsDetailsSearchFragment {
  label: string;
  fragment: string;
}

export const awsDetailsSearchFragments: AwsDetailsSearchFragment[] = [
  { label: 'aws/summary', fragment: 'summary' },

  { label: 'aws/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'aws/optimize/configuration', fragment: 'optimize/configuration' },
  { label: 'aws/optimize/security', fragment: 'optimize/security' },
  { label: 'aws/optimize/infra-upgrade', fragment: 'optimize/infra-upgrade' },
  { label: 'aws/optimize/recommendation-resolution', fragment: 'optimize/recommendation-resolution' },

  { label: 'aws/services', fragment: 'services' },

  { label: 'aws/troubleshoot/events', fragment: 'events/events' },
  { label: 'aws/troubleshoot/triage-rules', fragment: 'events/triage-rules' },
  { label: 'aws/troubleshoot/threshold-suggestions', fragment: 'events/threshold-suggestions' },

  { label: 'aws/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'aws/monitoring/cloud-logs', fragment: 'monitoring/cloud-logs' },
  { label: 'aws/monitoring/metrics', fragment: 'monitoring/metrics' },

  { label: 'aws/ec2/summary', fragment: 'ec2/summary' },
  { label: 'aws/ec2/optimize', fragment: 'ec2/optimize' },
  { label: 'aws/ec2/events', fragment: 'ec2/events' },
  { label: 'aws/ec2/instances', fragment: 'ec2/instances' },

  { label: 'aws/rds/summary', fragment: 'rds/summary' },
  { label: 'aws/rds/optimize', fragment: 'rds/optimize' },
  { label: 'aws/rds/events', fragment: 'rds/events' },
  { label: 'aws/rds/instances', fragment: 'rds/instances' },

  { label: 'aws/s3/summary', fragment: 's3/summary' },
  { label: 'aws/s3/optimize', fragment: 's3/optimize' },
  { label: 'aws/s3/events', fragment: 's3/events' },
  { label: 'aws/s3/instances', fragment: 's3/instances' },

  { label: 'aws/ecs/summary', fragment: 'ecs/summary' },
  { label: 'aws/ecs/optimize', fragment: 'ecs/optimize' },
  { label: 'aws/ecs/events', fragment: 'ecs/events' },
  { label: 'aws/ecs/instances', fragment: 'ecs/instances' },
];

// Azure Cloud Account Details tabs — same route/shape as awsDetailsSearchFragments
// above, see `baseOptions`/`azureOptions` in
// src/pages/cloud-account/details/[CloudAccountDetails].jsx.
export interface AzureDetailsSearchFragment {
  label: string;
  fragment: string;
}

export const azureDetailsSearchFragments: AzureDetailsSearchFragment[] = [
  { label: 'azure/summary', fragment: 'summary' },

  { label: 'azure/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'azure/optimize/configuration', fragment: 'optimize/configuration' },
  { label: 'azure/optimize/security', fragment: 'optimize/security' },
  { label: 'azure/optimize/infra-upgrade', fragment: 'optimize/infra-upgrade' },
  { label: 'azure/optimize/recommendation-resolution', fragment: 'optimize/recommendation-resolution' },

  { label: 'azure/services', fragment: 'services' },

  { label: 'azure/troubleshoot/events', fragment: 'events/events' },
  { label: 'azure/troubleshoot/triage-rules', fragment: 'events/triage-rules' },
  { label: 'azure/troubleshoot/threshold-suggestions', fragment: 'events/threshold-suggestions' },

  { label: 'azure/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'azure/monitoring/cloud-logs', fragment: 'monitoring/cloud-logs' },
  { label: 'azure/monitoring/metrics', fragment: 'monitoring/metrics' },

  { label: 'azure/vm/summary', fragment: 'vm/summary' },
  { label: 'azure/vm/optimize', fragment: 'vm/optimize' },
  { label: 'azure/vm/events', fragment: 'vm/events' },
  { label: 'azure/vm/instances', fragment: 'vm/instances' },

  { label: 'azure/sql/summary', fragment: 'sql/summary' },
  { label: 'azure/sql/optimize', fragment: 'sql/optimize' },
  { label: 'azure/sql/events', fragment: 'sql/events' },
  { label: 'azure/sql/instances', fragment: 'sql/instances' },

  { label: 'azure/sql-mi/summary', fragment: 'sql-mi/summary' },
  { label: 'azure/sql-mi/optimize', fragment: 'sql-mi/optimize' },
  { label: 'azure/sql-mi/events', fragment: 'sql-mi/events' },
  { label: 'azure/sql-mi/instances', fragment: 'sql-mi/instances' },

  { label: 'azure/blob/summary', fragment: 'blob/summary' },
  { label: 'azure/blob/optimize', fragment: 'blob/optimize' },
  { label: 'azure/blob/events', fragment: 'blob/events' },
  { label: 'azure/blob/instances', fragment: 'blob/instances' },
];

// GCP Cloud Account Details tabs — same route/shape as awsDetailsSearchFragments
// above, see `baseOptions`/`gcpOptions` in
// src/pages/cloud-account/details/[CloudAccountDetails].jsx. `monitoring/traces`
// is GCP-only today (Cloud Trace integration, not yet wired for other providers).
export interface GcpDetailsSearchFragment {
  label: string;
  fragment: string;
}

export const gcpDetailsSearchFragments: GcpDetailsSearchFragment[] = [
  { label: 'gcp/summary', fragment: 'summary' },

  { label: 'gcp/optimize/right-sizing', fragment: 'optimize/right-sizing' },
  { label: 'gcp/optimize/configuration', fragment: 'optimize/configuration' },
  { label: 'gcp/optimize/security', fragment: 'optimize/security' },
  { label: 'gcp/optimize/infra-upgrade', fragment: 'optimize/infra-upgrade' },
  { label: 'gcp/optimize/recommendation-resolution', fragment: 'optimize/recommendation-resolution' },

  { label: 'gcp/services', fragment: 'services' },

  { label: 'gcp/troubleshoot/events', fragment: 'events/events' },
  { label: 'gcp/troubleshoot/triage-rules', fragment: 'events/triage-rules' },
  { label: 'gcp/troubleshoot/threshold-suggestions', fragment: 'events/threshold-suggestions' },

  { label: 'gcp/monitoring/alert-manager', fragment: 'monitoring/alert-manager' },
  { label: 'gcp/monitoring/cloud-logs', fragment: 'monitoring/cloud-logs' },
  { label: 'gcp/monitoring/metrics', fragment: 'monitoring/metrics' },
  { label: 'gcp/monitoring/traces', fragment: 'monitoring/traces' },

  { label: 'gcp/compute-engine/summary', fragment: 'compute-engine/summary' },
  { label: 'gcp/compute-engine/optimize', fragment: 'compute-engine/optimize' },
  { label: 'gcp/compute-engine/events', fragment: 'compute-engine/events' },
  { label: 'gcp/compute-engine/instances', fragment: 'compute-engine/instances' },

  { label: 'gcp/cloud-sql/summary', fragment: 'cloud-sql/summary' },
  { label: 'gcp/cloud-sql/optimize', fragment: 'cloud-sql/optimize' },
  { label: 'gcp/cloud-sql/events', fragment: 'cloud-sql/events' },
  { label: 'gcp/cloud-sql/instances', fragment: 'cloud-sql/instances' },

  { label: 'gcp/cloud-storage/summary', fragment: 'cloud-storage/summary' },
  { label: 'gcp/cloud-storage/optimize', fragment: 'cloud-storage/optimize' },
  { label: 'gcp/cloud-storage/events', fragment: 'cloud-storage/events' },
  { label: 'gcp/cloud-storage/instances', fragment: 'cloud-storage/instances' },
];
