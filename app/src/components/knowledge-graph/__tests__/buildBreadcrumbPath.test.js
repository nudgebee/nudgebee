// Path chips (#41142): a node seeded from the Node filter arrives as the filter
// option — unique key as displayLabel, name as label, no name/type/account — and
// breadcrumb navigation keeps only { label, value }. Each chip must still read
// name, type, account and unique key from the graph's record, as a chip from a
// canvas click does.
import { buildBreadcrumbPath } from '../KnowledgeGraph';

const API_KEY = 'k8s:dev-cluster::Workload:shop:api';
const DB_KEY = 'k8s:dev-cluster::Workload:shop:db';
const CACHE_KEY = 'k8s:dev-cluster::Workload:shop:cache';

// Shape of the graph's uniqueServiceKeys records (what a canvas click selects).
const record = (id, name, uniqueKey) => ({
  label: uniqueKey,
  displayLabel: `${name} (Workload)`,
  value: id,
  name,
  nodeType: 'Workload',
  uniqueKey,
  accountName: 'dev-cluster',
  accountId: 'acc-1',
});
const nodesById = new Map([
  ['n-api', record('n-api', 'api', API_KEY)],
  ['n-cache', record('n-cache', 'cache', CACHE_KEY)],
  ['n-db', record('n-db', 'db', DB_KEY)],
]);
// api — cache — db
const adjacencyMap = new Map([
  ['n-api', new Set(['n-cache'])],
  ['n-cache', new Set(['n-api', 'n-db'])],
  ['n-db', new Set(['n-cache'])],
]);
const edgeTypeLookup = new Map([
  ['n-api->n-cache', 'CALLS'],
  ['n-cache->n-db', 'CALLS'],
]);

// Shape of a Node-filter option (buildNodeOption).
const filterOption = { label: 'api', displayLabel: API_KEY, value: 'n-api', badge: 'Workload', searchText: API_KEY };

const tooltipFields = (n) => ({
  displayLabel: n.displayLabel,
  name: n.name,
  nodeType: n.nodeType,
  uniqueKey: n.uniqueKey,
  accountName: n.accountName,
});

it('gives a chip seeded from the Node filter the graph node name, type, account and key', () => {
  const [chip] = buildBreadcrumbPath([filterOption], adjacencyMap, nodesById, edgeTypeLookup);
  expect(tooltipFields(chip)).toEqual({
    displayLabel: 'api (Workload)',
    name: 'api',
    nodeType: 'Workload',
    uniqueKey: API_KEY,
    accountName: 'dev-cluster',
  });
  // label/value round-trip into the Node filter's draft state unchanged.
  expect(chip).toMatchObject({ label: 'api', value: 'n-api', isUserSelected: true });
});

it('restores the fields on chips kept as { label, value } after breadcrumb navigation', () => {
  const path = buildBreadcrumbPath(
    [
      { label: 'api', value: 'n-api' },
      { label: DB_KEY, value: 'n-db' },
    ],
    adjacencyMap,
    nodesById,
    edgeTypeLookup
  );
  expect(path.map((n) => [n.displayLabel, n.uniqueKey, n.isUserSelected, n.edgeFromPrev])).toEqual([
    ['api (Workload)', API_KEY, true, null],
    ['cache (Workload)', CACHE_KEY, false, 'CALLS'],
    ['db (Workload)', DB_KEY, true, 'CALLS'],
  ]);
});

it('leaves a selected node the graph has not loaded as given', () => {
  const [chip] = buildBreadcrumbPath([filterOption], new Map(), new Map(), new Map());
  expect(chip).toEqual({ ...filterOption, isUserSelected: true, edgeFromPrev: null });
});

it('marks a selected node with no path from the previous one as disconnected', () => {
  const isolated = new Map([...nodesById, ['n-x', record('n-x', 'x', 'k8s:dev-cluster::Workload:shop:x')]]);
  const path = buildBreadcrumbPath([filterOption, { label: 'x', value: 'n-x' }], adjacencyMap, isolated, edgeTypeLookup);
  expect(path[1]).toMatchObject({ name: 'x', isDisconnected: true, edgeFromPrev: null });
});
