import {
  buildFilterExpression,
  canonicalizeFilterExpression,
  parseFilterExpression,
  structuredFieldDescription,
  structuredFieldLabel,
} from '../eventFilter';

const ACCOUNT_UUID = '4f1c2b7e-8a90-4c31-9f2d-1b6ac5e70d34';

describe('buildFilterExpression', () => {
  it('compiles the cluster picker value onto event.cloud_account_id, not event.cluster', () => {
    expect(buildFilterExpression({ cluster: ACCOUNT_UUID })).toBe(`{{ event.cloud_account_id == "${ACCOUNT_UUID}" }}`);
  });

  it('keeps the namespace filter on subject_namespace regardless of provider', () => {
    expect(buildFilterExpression({ namespace: 'AmazonEC2' })).toBe('{{ event.subject_namespace == "AmazonEC2" }}');
  });

  it('joins every set field and skips the empty ones', () => {
    expect(buildFilterExpression({ event_type: 'KubePodCrashLooping', cluster: '', namespace: 'payments', source: '', priority: 'HIGH' })).toBe(
      '{{ event.event_type == "KubePodCrashLooping" and event.subject_namespace == "payments" and event.priority == "HIGH" }}'
    );
  });

  it('returns an empty string when nothing is set', () => {
    expect(buildFilterExpression({})).toBe('');
  });
});

describe('canonicalizeFilterExpression', () => {
  it('rewrites a legacy uuid-valued event.cluster onto event.cloud_account_id', () => {
    expect(canonicalizeFilterExpression(`{{ event.cluster == "${ACCOUNT_UUID}" }}`)).toBe(`{{ event.cloud_account_id == "${ACCOUNT_UUID}" }}`);
  });

  it('leaves a name-valued event.cluster alone — hand-authored filters match on the name', () => {
    const filter = '{{ event.cluster == "prod-us-east-1" }}';
    expect(canonicalizeFilterExpression(filter)).toBe(filter);
  });

  it('tolerates an empty filter', () => {
    expect(canonicalizeFilterExpression('')).toBe('');
  });
});

describe('parseFilterExpression', () => {
  it('round-trips what buildFilterExpression produced', () => {
    const values = { event_type: 'KubePodCrashLooping', cluster: ACCOUNT_UUID, namespace: 'payments', source: 'prometheus', priority: 'HIGH' };
    expect(parseFilterExpression(buildFilterExpression(values))).toEqual(values);
  });

  it('adopts a legacy uuid-valued event.cluster into the structured cluster field', () => {
    expect(parseFilterExpression(`{{ event.cluster == "${ACCOUNT_UUID}" and event.subject_namespace == "payments" }}`)).toEqual({
      cluster: ACCOUNT_UUID,
      namespace: 'payments',
    });
  });

  it('does not adopt a name-valued event.cluster, so the sidebar keeps it in advanced mode', () => {
    expect(parseFilterExpression('{{ event.cluster == "prod-us-east-1" }}')).toEqual({});
  });

  it('returns nothing for a non-template filter', () => {
    expect(parseFilterExpression('event.cluster == "prod"')).toEqual({});
  });
});

describe('structuredFieldLabel / structuredFieldDescription', () => {
  it('labels the overloaded fields for cloud accounts', () => {
    expect(structuredFieldLabel('namespace', 'AWS')).toBe('Service');
    expect(structuredFieldLabel('cluster', 'GCP')).toBe('Cloud Account');
    expect(structuredFieldDescription('namespace', 'AWS')).toBe('Cloud service (e.g. AmazonEC2, AWS_RDS)');
  });

  it('keeps the kubernetes wording for K8s accounts and when the provider is unknown', () => {
    expect(structuredFieldLabel('namespace', 'K8s')).toBe('Namespace');
    expect(structuredFieldLabel('cluster', 'K8s')).toBe('Cluster');
    expect(structuredFieldLabel('namespace', '')).toBe('Namespace');
    expect(structuredFieldDescription('cluster', undefined)).toBe('Kubernetes cluster');
  });

  it('falls back to the static label for fields that mean the same thing everywhere', () => {
    expect(structuredFieldLabel('source', 'AWS')).toBe('Source');
    expect(structuredFieldLabel('priority', 'AWS')).toBe('Priority');
    expect(structuredFieldDescription('source', 'AWS')).toBe('');
  });
});
