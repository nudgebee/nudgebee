// A sample declarations document built from the tenant's own schema
// (kg_get_manual_node_schema), so a user downloads something that names their
// accounts and types instead of placeholders they must look up.

const PLACEHOLDER = (what) => `<${what}>`;

// The first kind whose identity needs nothing beyond these fields, else the first kind.
const pickKind = (kinds, allowed) =>
  (kinds ?? []).find((k) => (k.key_fields ?? []).every((f) => !f.required || allowed.includes(f.name))) ?? (kinds ?? [])[0];

const uniqueRef = (taken, base) => {
  let ref = base;
  for (let i = 2; taken.has(ref); i++) {
    ref = `${base}-${i}`;
  }
  taken.add(ref);
  return ref;
};

const firstProperty = (kind, shape) => (kind.properties ?? []).find((p) => (p.shape ?? '') === shape);

export const buildSampleDeclarations = (schema) => {
  const places = Array.isArray(schema) ? schema : [];
  const refs = new Set();
  const nodes = [];
  const dependencies = [];

  const outsideRef = uniqueRef(refs, 'billing-db');
  nodes.push({
    ref: outsideRef,
    node_type: 'Database',
    name: 'billing-db',
    namespace: 'finance',
    description: 'Outside any connected account: an on-prem database',
    labels: { team: 'finance' },
    properties: { engine: 'postgres' },
  });

  const cloud = places.find((p) => p.cloud_account_id && p.cloud_provider !== 'K8s' && p.kinds?.length);
  let cloudRef = null;
  if (cloud) {
    const kind = pickKind(cloud.kinds, ['name', 'region']);
    const node = {
      cloud_account: cloud.account_number || cloud.cloud_account_id,
      specific_type: kind.specific_type,
      name: `example-${kind.specific_type.toLowerCase()}`,
      region: 'us-east-1',
    };
    if ((kind.key_fields ?? []).some((f) => f.name === 'network' && f.required)) {
      node.network = PLACEHOLDER('vpc id or name');
    }
    const scalar = firstProperty(kind, '');
    if (scalar) {
      node.properties = { [scalar.name]: PLACEHOLDER(scalar.name) };
    }
    cloudRef = uniqueRef(refs, node.name);
    nodes.push({ ref: cloudRef, ...node });
  }

  const cluster = places.find((p) => p.cloud_account_id && p.cloud_provider === 'K8s' && p.kinds?.length);
  let clusterRef = null;
  if (cluster) {
    const kind = pickKind(cluster.kinds, ['name', 'namespace']);
    const node = {
      cloud_account: cluster.account_number || cluster.cloud_account_id,
      specific_type: kind.specific_type,
      name: 'ledger-worker',
      namespace: 'payments',
    };
    const map = firstProperty(kind, 'map');
    if (map) {
      node.properties = { [map.name]: { owner: 'ledger-team' } };
    }
    clusterRef = uniqueRef(refs, node.name);
    nodes.push({ ref: clusterRef, ...node });
  }

  dependencies.push({
    source: { node_type: 'Workload', name: PLACEHOLDER('a workload'), namespace: PLACEHOLDER('its namespace'), cluster: PLACEHOLDER('its cluster') },
    dest: outsideRef,
    relationship_type: 'CALLS',
    notes: 'A node already in the graph calls a node of this document',
  });
  if (clusterRef && cloudRef) {
    dependencies.push({ source: clusterRef, dest: cloudRef, relationship_type: 'PUBLISHES_TO', notes: 'Both sides are nodes of this document' });
  }
  return { nodes, dependencies };
};
