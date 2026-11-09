// The downloadable sample is built from the tenant's own schema, and must be a
// document the importer accepts: unique refs, every ref declared.

import { buildSampleDeclarations } from '../manualDeclarationsSample';

const name = { name: 'name', required: true };
const SCHEMA = [
  {
    cloud_account_id: 'aws-1',
    account_number: '123456789012',
    cloud_provider: 'AWS',
    kinds: [
      {
        specific_type: 'RDSInstance',
        node_type: 'Database',
        key_fields: [name, { name: 'region', required: true }, { name: 'network', required: true }],
        properties: [{ name: 'engine' }],
      },
      {
        specific_type: 'SQSQueue',
        node_type: 'MessageQueue',
        key_fields: [name, { name: 'region', required: true }],
        properties: [{ name: 'fifo_queue' }],
      },
    ],
  },
  {
    cloud_account_id: 'k8s-1',
    cloud_provider: 'K8s',
    kinds: [
      {
        specific_type: 'KubernetesDeployment',
        node_type: 'Workload',
        key_fields: [name, { name: 'namespace', required: true }],
        properties: [{ name: 'replicas' }, { name: 'annotations', shape: 'map' }],
      },
    ],
  },
  { cloud_provider: 'external', kinds: [{ specific_type: 'Database', node_type: 'Database', key_fields: [name] }] },
];

const refsResolve = (doc) => {
  const refs = doc.nodes.map((n) => n.ref);
  expect(new Set(refs).size).toBe(refs.length);
  for (const d of doc.dependencies) {
    for (const side of [d.source, d.dest]) {
      if (typeof side === 'string') {
        expect(refs).toContain(side);
      } else {
        expect(side.node_type).toBeTruthy();
        expect(side.name).toBeTruthy();
      }
    }
  }
};

it('declares a node per kind of place the tenant has, linked by refs', () => {
  const doc = buildSampleDeclarations(SCHEMA);
  expect(doc.nodes).toHaveLength(3);
  const [outside, cloud, cluster] = doc.nodes;
  expect(outside).toMatchObject({ node_type: 'Database', name: 'billing-db' });
  // The account is named by its number, and the kind needs nothing but name and region.
  expect(cloud).toMatchObject({ cloud_account: '123456789012', specific_type: 'SQSQueue', region: 'us-east-1' });
  expect(cloud.network).toBeUndefined();
  expect(Object.keys(cloud.properties)).toEqual(['fifo_queue']);
  // A Kubernetes account has no number, so its id; a key/value property is an object.
  expect(cluster).toMatchObject({ cloud_account: 'k8s-1', specific_type: 'KubernetesDeployment', namespace: 'payments' });
  expect(cluster.properties.annotations).toEqual({ owner: 'ledger-team' });
  expect(doc.dependencies).toHaveLength(2);
  expect(doc.dependencies[1]).toMatchObject({ source: cluster.ref, dest: cloud.ref, relationship_type: 'PUBLISHES_TO' });
  refsResolve(doc);
  expect(() => JSON.parse(JSON.stringify(doc))).not.toThrow();
});

it('falls back to a node outside any account when nothing is connected', () => {
  for (const schema of [[], undefined, null]) {
    const doc = buildSampleDeclarations(schema);
    expect(doc.nodes).toHaveLength(1);
    expect(doc.dependencies).toHaveLength(1);
    refsResolve(doc);
  }
});

it('adds a network placeholder only when every kind needs one', () => {
  const doc = buildSampleDeclarations([{ ...SCHEMA[0], kinds: [SCHEMA[0].kinds[0]] }]);
  expect(doc.nodes[1]).toMatchObject({ specific_type: 'RDSInstance', network: '<vpc id or name>' });
});
