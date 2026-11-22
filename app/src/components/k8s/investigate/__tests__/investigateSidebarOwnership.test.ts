import { assignableOwnerRung, buildOwnershipLevels } from '../InvestigateSidebar';

// The chain builder is the whole risk surface of ownership in the sidebar: which rungs
// get asked for is decided here, and asking for the wrong one fails silently (it just
// resolves to nothing, or to somebody else's owner). Tested directly rather than
// through a render, because the sidebar takes ~17 props and none of them affect this.
describe('buildOwnershipLevels', () => {
  const k8s = { isK8s: true, cloudResourceId: 'res-1', cloudAccountId: 'acct-1', namespace: 'nudgebee' };
  const cloud = { isK8s: false, cloudResourceId: 'res-1', cloudAccountId: 'acct-1', namespace: 'EC2' };

  const shape = (levels: ReturnType<typeof buildOwnershipLevels>) => levels.map((l) => [l.resourceType, l.resourceKey]);

  it('walks workload → namespace → cloud account for a k8s event', () => {
    expect(shape(buildOwnershipLevels(k8s))).toEqual([
      ['workload', 'res-1'],
      ['namespace', 'acct-1/nudgebee'],
      ['cloud_account', 'acct-1'],
    ]);
  });

  it('never builds a namespace rung for a cloud event', () => {
    // subject_namespace is populated for cloud events too, but it holds the cloud
    // service name (EC2, RDS) — a namespace rung built from it would ask the resolver
    // for `acct-1/EC2`, which is meaningless. This is the regression this exists for.
    const levels = buildOwnershipLevels(cloud);
    expect(shape(levels)).toEqual([
      ['cloud_resource', 'res-1'],
      ['cloud_account', 'acct-1'],
    ]);
    expect(levels.some((l) => l.resourceType === 'namespace')).toBe(false);
    expect(JSON.stringify(levels)).not.toContain('EC2');
  });

  it('falls back to namespace and account when a k8s event has no resource id', () => {
    // cloud_resource_id is best-effort — the collector only sets it when the event
    // matched a known resource, so this is a common case, not an edge one.
    expect(shape(buildOwnershipLevels({ ...k8s, cloudResourceId: null }))).toEqual([
      ['namespace', 'acct-1/nudgebee'],
      ['cloud_account', 'acct-1'],
    ]);
  });

  it('falls back to the account alone when a cloud event has no resource id', () => {
    expect(shape(buildOwnershipLevels({ ...cloud, cloudResourceId: undefined }))).toEqual([['cloud_account', 'acct-1']]);
  });

  it('skips the namespace rung when a k8s event has no namespace', () => {
    expect(shape(buildOwnershipLevels({ ...k8s, namespace: '' }))).toEqual([
      ['workload', 'res-1'],
      ['cloud_account', 'acct-1'],
    ]);
  });

  it('returns an empty chain when there is no account, so nothing is requested', () => {
    expect(buildOwnershipLevels({ isK8s: true, cloudResourceId: null, cloudAccountId: null, namespace: null })).toEqual([]);
  });

  it('would build the wrong chain if a cloud event were mislabelled as k8s', () => {
    // Documents why the sidebar waits for `sourceKnown` before resolving. The page
    // defaults source to 'kubernetes' until the account list loads, so a cloud event
    // briefly reports isK8s — and with subject_namespace holding a service name, that
    // produces a namespace rung keyed `<account>/EC2`. The guard is in the caller;
    // this pins what it is guarding against.
    const mislabelled = buildOwnershipLevels({ ...cloud, isK8s: true });
    expect(shape(mislabelled)).toContainEqual(['namespace', 'acct-1/EC2']);
  });

  it('always ends at the cloud account, so there is a rung to resolve', () => {
    for (const input of [k8s, cloud, { ...k8s, cloudResourceId: null }, { ...cloud, cloudResourceId: null }]) {
      const levels = buildOwnershipLevels(input);
      expect(levels[levels.length - 1].resourceType).toBe('cloud_account');
    }
  });
});

// Which rung "Assign owner" may write to. The sidebar offers it for the investigated
// resource only — the two ways that goes wrong are both silent, so they are pinned
// here: an owner set one level up lands on everything under it, and an owner set on a
// resource the resolver no longer holds is saved and then never shown.
describe('assignableOwnerRung', () => {
  const rung = (resourceType: string, extra = {}) => ({ level: resourceType, resourceType, resourceKey: 'k', own: null, ...extra });

  it('offers the workload a k8s event is about', () => {
    const levels = [rung('workload'), rung('namespace'), rung('cloud_account')];
    expect(assignableOwnerRung(levels)).toBe(levels[0]);
  });

  it('offers the cloud resource a cloud event is about', () => {
    const levels = [rung('cloud_resource'), rung('cloud_account')];
    expect(assignableOwnerRung(levels)).toBe(levels[0]);
  });

  it('offers nothing when the event only identifies a namespace or an account', () => {
    expect(assignableOwnerRung([rung('namespace'), rung('cloud_account')])).toBeNull();
    expect(assignableOwnerRung([rung('cloud_account')])).toBeNull();
  });

  it('offers nothing for a resource the resolver no longer holds as active', () => {
    expect(assignableOwnerRung([rung('workload', { noActiveResource: true }), rung('namespace')])).toBeNull();
    expect(assignableOwnerRung([rung('cloud_resource', { noActiveResource: true })])).toBeNull();
  });

  it('offers nothing before the chain exists', () => {
    expect(assignableOwnerRung([])).toBeNull();
    expect(assignableOwnerRung(undefined)).toBeNull();
  });
});
