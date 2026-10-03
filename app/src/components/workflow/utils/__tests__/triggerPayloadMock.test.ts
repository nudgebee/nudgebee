import {
  buildEventTriggerMock,
  buildOptimizationTriggerMock,
  buildTriggerMock,
  eventAccountOverrides,
  flattenPayloadFields,
  EMITTED_LIFECYCLE_PHASES,
} from '../triggerPayloadMock';

describe('buildEventTriggerMock', () => {
  it('carries the keys the publish path adds to the event row', () => {
    const payload = buildEventTriggerMock('event.created');

    // lifecycle_phase is stamped by publishToWorkflows; has_evidences replaces the
    // stripped evidences blob; event_type is backfilled from aggregation_key.
    expect(payload.lifecycle_phase).toBe('event.created');
    expect(payload.has_evidences).toBe(true);
    expect(payload.event_type).toBe(payload.aggregation_key);
    expect(payload).not.toHaveProperty('evidences');
  });

  it('adds the analysis_* fields only on the investigation phases', () => {
    expect(buildEventTriggerMock('event.created')).not.toHaveProperty('analysis_status');

    const completed = buildEventTriggerMock('investigation.completed');
    expect(completed.analysis_status).toBe('COMPLETED');
    expect(completed.analysis_summary).toBeTruthy();

    // The producer sends the same keys with empty values on failure, plus a reason.
    const failed = buildEventTriggerMock('investigation.failed');
    expect(failed.analysis_status).toBe('FAILED');
    expect(failed.analysis_summary).toBe('');
    expect(failed.analysis_status_reason).toBeTruthy();
  });

  it('folds the trigger filter values into the sample', () => {
    const payload = buildEventTriggerMock('event.created', {
      event_type: 'KubeDeploymentReplicasMismatch',
      cluster: 'staging-eu',
      subject_namespace: 'billing',
      source: 'anomaly-detector',
      priority: 'LOW',
    });

    expect(payload.event_type).toBe('KubeDeploymentReplicasMismatch');
    expect(payload.aggregation_key).toBe('KubeDeploymentReplicasMismatch');
    expect(payload.cluster).toBe('staging-eu');
    expect(payload.subject_namespace).toBe('billing');
    expect(payload.source).toBe('anomaly-detector');
    expect(payload.priority).toBe('LOW');
  });

  it('ignores empty override values so the sample stays populated', () => {
    const payload = buildEventTriggerMock('event.created', { cluster: '', priority: '' });

    expect(payload.cluster).toBeTruthy();
    expect(payload.priority).toBeTruthy();
  });
});

describe('eventAccountOverrides', () => {
  const accounts = [
    { label: 'prod-eks', value: '11111111-1111-1111-1111-111111111111' },
    { label: 'aws-billing', value: '22222222-2222-2222-2222-222222222222' },
  ];

  it('puts the picked account id in cloud_account_id and its name in cluster', () => {
    const payload = buildEventTriggerMock('event.created', eventAccountOverrides('22222222-2222-2222-2222-222222222222', accounts));

    expect(payload.cloud_account_id).toBe('22222222-2222-2222-2222-222222222222');
    expect(payload.cluster).toBe('aws-billing');
  });

  it('follows the picker when the account changes', () => {
    const first = buildEventTriggerMock('event.created', eventAccountOverrides(accounts[0].value, accounts));
    const second = buildEventTriggerMock('event.created', eventAccountOverrides(accounts[1].value, accounts));

    expect(first.cloud_account_id).not.toBe(second.cloud_account_id);
    expect(second.cluster).toBe('aws-billing');
  });

  it('still carries the picked id while the account list has not loaded', () => {
    const payload = buildEventTriggerMock('event.created', eventAccountOverrides(accounts[1].value, []));

    expect(payload.cloud_account_id).toBe(accounts[1].value);
    expect(payload.cluster).toBeTruthy();
  });

  it('keeps the sample values when no account is picked', () => {
    const payload = buildEventTriggerMock('event.created', eventAccountOverrides('', accounts));

    expect(payload.cloud_account_id).toBeTruthy();
    expect(payload.cluster).toBeTruthy();
  });
});

describe('buildOptimizationTriggerMock', () => {
  // Pins the exact key set recommendation_poller.go publishes. A mismatch here
  // means the mock and the real payload have diverged.
  it('has exactly the keys the recommendation poller builds', () => {
    expect(Object.keys(buildOptimizationTriggerMock()).sort()).toEqual(
      [
        'account_id',
        'category',
        'cloud_account_id',
        'cluster',
        'estimated_savings',
        'event_type',
        'recommendation_id',
        'resource_id',
        'rule_name',
        'severity',
        'status',
        'tenant_id',
      ].sort()
    );
  });

  it('fires on new Open recommendations', () => {
    const payload = buildOptimizationTriggerMock();

    expect(payload.event_type).toBe('optimization.recommendation');
    expect(payload.status).toBe('Open');
    // The poller sets account_id from the recommendation's cloud account.
    expect(payload.account_id).toBe(payload.cloud_account_id);
  });

  it('folds the trigger filter values into the sample', () => {
    const payload = buildOptimizationTriggerMock({
      category: 'InfraUpgrade',
      rule_name: 'AKS Node Pool Upgrade',
      cloud_account_id: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee',
    });

    expect(payload.category).toBe('InfraUpgrade');
    expect(payload.rule_name).toBe('AKS Node Pool Upgrade');
    expect(payload.cloud_account_id).toBe('aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee');
    expect(payload.account_id).toBe('aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee');
  });
});

describe('buildTriggerMock', () => {
  it('returns null for trigger types the simulator does not cover', () => {
    expect(buildTriggerMock('schedule', 'event.created')).toBeNull();
    expect(buildTriggerMock('webhook', 'event.created')).toBeNull();
    expect(buildTriggerMock(undefined, 'event.created')).toBeNull();
  });

  it('routes event and optimization to their builders', () => {
    expect(buildTriggerMock('event', 'event.created')?.aggregation_key).toBeTruthy();
    expect(buildTriggerMock('optimization', 'event.created')?.event_type).toBe('optimization.recommendation');
  });
});

describe('EMITTED_LIFECYCLE_PHASES', () => {
  // Only these three have a producer in api-server today. Offering more would
  // advertise triggers that never fire.
  it('lists only the phases that are actually emitted', () => {
    expect(EMITTED_LIFECYCLE_PHASES.map((p) => p.value)).toEqual(['event.created', 'investigation.completed', 'investigation.failed']);
  });
});

describe('flattenPayloadFields', () => {
  it('walks nested objects into dotted paths and leaves arrays whole', () => {
    const fields = flattenPayloadFields({
      cluster: 'prod',
      score: 82,
      missing: null,
      labels: { team: 'payments' },
      tags: ['a', 'b'],
    });

    expect(fields).toEqual([
      { path: 'cluster', type: 'string', example: 'prod' },
      { path: 'score', type: 'number', example: '82' },
      { path: 'missing', type: 'null', example: 'null' },
      { path: 'labels.team', type: 'string', example: 'payments' },
      { path: 'tags', type: 'array', example: '["a","b"]' },
    ]);
  });

  it('exposes the nested event fields as copyable paths', () => {
    const paths = flattenPayloadFields(buildEventTriggerMock('event.created')).map((f) => f.path);

    expect(paths).toContain('labels.team');
    expect(paths).toContain('score_factors.blast_radius');
    expect(paths).toContain('lifecycle_phase');
  });
});
