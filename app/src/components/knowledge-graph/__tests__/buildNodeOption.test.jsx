// Node filter options (#41140): type-ahead finds a node by its type, as the badge
// shows it or as one word, not only by name/namespace/cluster/region.
import { buildNodeOption } from '../KnowledgeGraph';

const DAEMONSET = 'k8s:dev-cluster:us-central1:Workload:monitoring:node-exporter';
const DEPLOYMENT = 'k8s:dev-cluster:us-central1:Workload:payments:checkout-api';
const EC2 = 'aws:acct-a:us-east-1:ComputeInstance:vpc-0abc:web-1';
const IAM_ROLE = 'aws:acct-a:global:IAMRole:none:ec2-ssm-role';

const clusterMap = { [DAEMONSET]: 'dev-cluster', [DEPLOYMENT]: 'dev-cluster' };
const specificTypeMap = {
  [DAEMONSET]: 'KubernetesDaemonSet',
  [DEPLOYMENT]: 'KubernetesDeployment',
  [EC2]: 'EC2Instance',
};

const options = [DAEMONSET, DEPLOYMENT, EC2, IAM_ROLE].map((key) => buildNodeOption(key, key, clusterMap, specificTypeMap, {}));

// Same haystack FilterDropdown searches: the trimmed query against the visible label plus `searchText`.
const search = (q) => options.filter((o) => `${o.label} ${o.searchText || ''}`.toLowerCase().includes(q.trim().toLowerCase())).map((o) => o.value);

it('keeps the badge on the specific type', () => {
  expect(options.map((o) => o.badge)).toEqual(['Daemon Set', 'Deployment', 'EC2Instance', 'IAMRole']);
});

it.each([
  ['daemonset', [DAEMONSET]],
  ['Daemon Set', [DAEMONSET]],
  ['DAEMONSET', [DAEMONSET]],
  ['deployment', [DEPLOYMENT]],
  ['EC2 Instance', [EC2]],
  ['IAM Role', [IAM_ROLE]],
])('finds a node by its type: %s', (q, expected) => {
  expect(search(q)).toEqual(expected);
});

// Membership only: FilterDropdown ranks name matches (the IAM role) above searchText-only ones.
it('finds EC2 instances by "EC2", as well as names that contain it', () => {
  expect(search('EC2').sort()).toEqual([EC2, IAM_ROLE].sort());
});

it.each([
  ['node-exporter', [DAEMONSET]],
  ['  CHECKOUT-API  ', [DEPLOYMENT]],
  ['payments', [DEPLOYMENT]],
  ['dev-cluster', [DAEMONSET, DEPLOYMENT]],
  ['us-east-1', [EC2]],
])('still finds a node by name, namespace, cluster or region: %s', (q, expected) => {
  expect(search(q)).toEqual(expected);
});
