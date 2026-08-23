import { parseUniqueKey, decodeFilterOptions, computeFilterOptionsClientSide } from '../kgFilterCascade';

// A small v2 (columnar) payload. unique_key = {provider}:{account}:{location}:{NodeType}:{hierarchy}:{name}
// NOTE n4's key segment is "clusterX" (a k8s cluster name) but its account is ACC-A — the account
// cascade must join on node_account_map (node_id -> account), not on the key's account segment.
const ACC_A = '11111111-1111-1111-1111-111111111111';
const ACC_B = '22222222-2222-2222-2222-222222222222';

// filter buckets: [account_idx, node_type_idx, specific_type_idx]
// node_type_idx -> sorted(keys(node_types)) = [ComputeInstance(0), Service(1), Workload(2)]
// specific_type_idx -> specific_type_dict below
const payload = {
  account_ids: [ACC_A, ACC_B],
  node_types: {
    ComputeInstance: ['EC2Instance'],
    Service: ['KubernetesService'],
    Workload: ['KubernetesDeployment', 'KubernetesStatefulSet'],
  },
  label_keys: ['app', 'env'],
  attribute_keys: ['name', 'region'],
  specific_type_dict: ['KubernetesDeployment', 'KubernetesStatefulSet', 'KubernetesService', 'EC2Instance'],
  cluster_dict: ['prod', 'clusterX'],
  node_keys: [
    'k8s:acctA:loc:Workload:h:dep1',
    'k8s:acctA:loc:Workload:h:sts1',
    'aws:acctB:loc:Service:h:svc1',
    'k8s:clusterX:loc:Workload:h:dep2',
    'aws:acctA:loc:ComputeInstance:h:ec2a',
  ],
  node_ids: ['u1', 'u2', 'u3', 'u4', 'u5'],
  node_account_idx: [0, 0, 1, 0, 0],
  node_specific_type_idx: [0, 1, 2, 0, 3],
  node_cluster_idx: [0, 0, -1, 1, -1],
  node_bucket_idx: [0, 1, 2, 0, 3],
  filter_buckets: [
    [0, 2, 0],
    [0, 2, 1],
    [1, 1, 2],
    [0, 0, 3],
  ],
  label_key_buckets: [[0, 1], [0]], // "app" on filter buckets {0,1}; "env" on {0}
  attribute_key_buckets: [
    [0, 1, 2, 3],
    [0, 1, 2, 3],
  ], // "name","region" on all filter buckets
  node_count: 5,
};

const keys = (r) => Object.keys(r.nodeIdMap).sort();
const labels = (r) => r.labelMap.map((x) => x.label).sort();
const attrs = (r) => r.attributeMap.map((x) => x.label).sort();

describe('parseUniqueKey', () => {
  it('splits a canonical 6-part key', () => {
    expect(parseUniqueKey('aws:acctA:us-east-1:Workload:ns:dep1')).toEqual({
      provider: 'aws',
      account: 'acctA',
      location: 'us-east-1',
      nodeType: 'Workload',
      hierarchy: 'ns',
      name: 'dep1',
    });
  });
  it('returns null for non-canonical keys', () => {
    expect(parseUniqueKey('legacy:flow:key')).toBeNull();
    expect(parseUniqueKey(undefined)).toBeNull();
  });
});

describe('decodeFilterOptions', () => {
  const d = decodeFilterOptions(payload);
  it('rebuilds the node maps from columns + dicts', () => {
    expect(d.nodeIdMap).toEqual({
      'k8s:acctA:loc:Workload:h:dep1': 'u1',
      'k8s:acctA:loc:Workload:h:sts1': 'u2',
      'aws:acctB:loc:Service:h:svc1': 'u3',
      'k8s:clusterX:loc:Workload:h:dep2': 'u4',
      'aws:acctA:loc:ComputeInstance:h:ec2a': 'u5',
    });
    expect(d.nodeClusterMap).toEqual({
      'k8s:acctA:loc:Workload:h:dep1': 'prod',
      'k8s:acctA:loc:Workload:h:sts1': 'prod',
      'k8s:clusterX:loc:Workload:h:dep2': 'clusterX',
    });
    expect(d.nodeSpecificTypeMap['aws:acctA:loc:ComputeInstance:h:ec2a']).toBe('EC2Instance');
    // account join map is keyed by node id (raw account uuid value)
    expect(d.nodeAccountMap).toEqual({ u1: ACC_A, u2: ACC_A, u3: ACC_B, u4: ACC_A, u5: ACC_A });
  });
  it('exposes the merged node_types as list + map, and bucket coverage', () => {
    expect(d.nodeTypeList.sort()).toEqual(['ComputeInstance', 'Service', 'Workload']);
    expect(d.specificTypesByNodeType.Workload).toEqual(['KubernetesDeployment', 'KubernetesStatefulSet']);
    expect(d.labelKeys).toEqual(['app', 'env']);
    expect(d.nodeBucketId).toEqual({
      'k8s:acctA:loc:Workload:h:dep1': 0,
      'k8s:acctA:loc:Workload:h:sts1': 1,
      'aws:acctB:loc:Service:h:svc1': 2,
      'k8s:clusterX:loc:Workload:h:dep2': 0,
      'aws:acctA:loc:ComputeInstance:h:ec2a': 3,
    });
  });
});

describe('computeFilterOptionsClientSide (over a decoded baseline)', () => {
  const baseline = decodeFilterOptions(payload);

  it('no filter → all nodes, all keys', () => {
    const r = computeFilterOptionsClientSide(baseline, {});
    expect(Object.keys(r.nodeIdMap)).toHaveLength(5);
    expect(labels(r)).toEqual(['app', 'env']);
    expect(attrs(r)).toEqual(['name', 'region']);
  });

  it('scopes nodes by broad node type', () => {
    const r = computeFilterOptionsClientSide(baseline, { broadNodeTypes: ['Workload'] });
    expect(keys(r)).toEqual(['k8s:acctA:loc:Workload:h:dep1', 'k8s:acctA:loc:Workload:h:sts1', 'k8s:clusterX:loc:Workload:h:dep2']);
  });

  it('scopes nodes by specific type', () => {
    const r = computeFilterOptionsClientSide(baseline, { specificTypes: ['KubernetesDeployment'] });
    expect(keys(r)).toEqual(['k8s:acctA:loc:Workload:h:dep1', 'k8s:clusterX:loc:Workload:h:dep2']);
  });

  it('scopes by account via node_id (k8s cluster node included)', () => {
    const r = computeFilterOptionsClientSide(baseline, { accountIds: [ACC_A] });
    expect(keys(r)).toEqual([
      'aws:acctA:loc:ComputeInstance:h:ec2a',
      'k8s:acctA:loc:Workload:h:dep1',
      'k8s:acctA:loc:Workload:h:sts1',
      'k8s:clusterX:loc:Workload:h:dep2',
    ]);
  });

  it('Option B: label/attr keys scope to the filtered node set (account ACC-B → no labels)', () => {
    const r = computeFilterOptionsClientSide(baseline, { accountIds: [ACC_B] });
    expect(keys(r)).toEqual(['aws:acctB:loc:Service:h:svc1']);
    // "app"/"env" only exist on ACC-A Workloads → hidden; attributes exist everywhere → shown.
    expect(labels(r)).toEqual([]);
    expect(attrs(r)).toEqual(['name', 'region']);
  });

  it('Option B: Workload selection keeps both labels (present on Workload filter buckets)', () => {
    const r = computeFilterOptionsClientSide(baseline, { broadNodeTypes: ['Workload'] });
    expect(labels(r)).toEqual(['app', 'env']);
  });
});
