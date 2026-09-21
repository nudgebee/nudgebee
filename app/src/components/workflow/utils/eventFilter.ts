export const STRUCTURED_FILTER_FIELDS = [
  { filterType: 'event_type', eventField: 'event_type', label: 'Event Type' },
  // The cluster picker stores the cloud-account UUID, but `event.cluster` carries a NAME
  // (cluster name for k8s, account name for cloud), so a UUID compared against it never
  // matches. Filter on `event.cloud_account_id` instead — the same mapping the optimization
  // trigger already uses (runbook-server/internal/storage/workflow_dao.go buildOptimizationFilter),
  // and a field the event publisher guarantees is non-empty.
  { filterType: 'cluster', eventField: 'cloud_account_id', label: 'Cluster' },
  { filterType: 'namespace', eventField: 'subject_namespace', label: 'Namespace' },
  { filterType: 'source', eventField: 'source', label: 'Source' },
  { filterType: 'priority', eventField: 'priority', label: 'Priority' },
] as const;

// Filters saved before the fix compare the picker's UUID against `event.cluster`. Rewrite
// those onto `event.cloud_account_id` so they still open in the structured UI. Only the UUID
// form is rewritten — a name-valued `event.cluster` was authored by hand or by the AI builder
// and is correct as-is, so it stays untouched and the sidebar keeps it in advanced mode.
const LEGACY_CLUSTER_UUID_RE = /event\.cluster(\s*==\s*)"([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"/g;

export const canonicalizeFilterExpression = (filter: string): string =>
  (filter || '').replace(LEGACY_CLUSTER_UUID_RE, 'event.cloud_account_id$1"$2"');

// `cluster` and `subject_namespace` are overloaded columns: k8s producers write a cluster
// name and a namespace, cloud-collector writes the cloud account name and the cloud service
// name (collector-server/cloud-collector/account/etl_events.go prepareEventForDB). The filter
// field is identical either way — only what we call it changes.
export const isCloudProvider = (cloudProvider?: string): boolean => !!cloudProvider && cloudProvider !== 'K8s';

const CLOUD_FIELD_LABELS: Record<string, { label: string; description: string }> = {
  cluster: { label: 'Cloud Account', description: 'Cloud account' },
  namespace: { label: 'Service', description: 'Cloud service (e.g. AmazonEC2, AWS_RDS)' },
};

const K8S_FIELD_DESCRIPTIONS: Record<string, string> = {
  cluster: 'Kubernetes cluster',
  namespace: 'Kubernetes namespace',
};

export const structuredFieldLabel = (filterType: string, cloudProvider?: string): string => {
  if (isCloudProvider(cloudProvider) && CLOUD_FIELD_LABELS[filterType]) {
    return CLOUD_FIELD_LABELS[filterType].label;
  }
  return STRUCTURED_FILTER_FIELDS.find((f) => f.filterType === filterType)?.label ?? '';
};

export const structuredFieldDescription = (filterType: string, cloudProvider?: string): string => {
  if (isCloudProvider(cloudProvider) && CLOUD_FIELD_LABELS[filterType]) {
    return CLOUD_FIELD_LABELS[filterType].description;
  }
  return K8S_FIELD_DESCRIPTIONS[filterType] ?? '';
};

export const buildFilterExpression = (currentValues: Record<string, string>, overrides?: Record<string, string>): string => {
  const conditions: string[] = [];
  for (const field of STRUCTURED_FILTER_FIELDS) {
    const val = overrides?.[field.filterType] ?? currentValues[field.filterType] ?? '';
    if (val) {
      const sanitized = val.replace(/"/g, "'");
      conditions.push(`event.${field.eventField} == "${sanitized}"`);
    }
  }
  if (conditions.length === 0) {
    return '';
  }
  return `{{ ${conditions.join(' and ')} }}`;
};

export const parseFilterExpression = (filter: string): Record<string, string> => {
  const result: Record<string, string> = {};
  if (!filter?.includes('{{')) {
    return result;
  }
  const canonical = canonicalizeFilterExpression(filter);

  for (const field of STRUCTURED_FILTER_FIELDS) {
    const regex = new RegExp(`event\\.${field.eventField}\\s*==\\s*"([^"]*)"`, 'i');
    const match = regex.exec(canonical);
    if (match) {
      result[field.filterType] = match[1];
    }
  }
  return result;
};
