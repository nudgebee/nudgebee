// services-server metrics actions (POST /rpc/metrics): the calls behind the k8s workload,
// node, pod and rightsizing utilisation charts and the metrics explorer's autocomplete.
// Each call goes services-server -> relay-server -> the account's agent -> that cluster's
// Prometheus, so this load lands on every tested cluster, not only on services-server.
//
// setup() looks up real workloads, pods, nodes, PVCs and rightsizing containers for each
// account in ACCOUNT_IDS, then every iteration fills one variant from
// services-server-metrics.json with one of them. SWEEP=true runs each variant once per
// account, one at a time, and logs a line per call: use it to check a catalog change.
import http from 'k6/http';
import exec from 'k6/execution';
import { check } from 'k6';
import { Rate } from 'k6/metrics';
import { env, baseUrl, scenarios, thresholds, thinkTime } from './lib.js';

const WINDOW_MS = { '5m': 3e5, '10m': 6e5, '1h': 36e5, '12h': 432e5, '24h': 864e5, '7d': 6048e5, '30d': 2592e6 };

// Which discovered entity each placeholder is filled from.
const FIELDS = {
  workload: ['ns', 'workload', 'kind', 'kind_lower'],
  workload2: ['ns2', 'workload2'],
  pod: ['pod', 'pod_ns', 'pod_workload'],
  node: ['node', 'node_ip'],
  node2: ['node2', 'node_ip2'],
  rs: ['rs_ns', 'rs_workload', 'rs_kind_lower', 'rs_container'],
  pvc: ['pvc', 'pvc_ns'],
};
const ENTITY_OF = {};
for (const [entity, fields] of Object.entries(FIELDS)) for (const f of fields) ENTITY_OF[f] = entity;

const onlyActions = env('ACTIONS', '').split(',').filter(Boolean);
const variants = JSON.parse(open('./services-server-metrics.json'))
  .variants.filter((v) => !onlyActions.length || onlyActions.includes(v.action))
  .map((v) => {
    const needs = new Set();
    JSON.stringify(v.input).replace(/\{\{(\w+)\}\}/g, (_, name) => {
      if (!ENTITY_OF[name]) throw new Error(`${v.id}: unknown placeholder {{${name}}}`);
      needs.add(ENTITY_OF[name]);
      return '';
    });
    // workload before workload2, node before node2, so entityValues() can keep the second one different
    return { ...v, needs: [...needs].sort((a, b) => a.endsWith('2') - b.endsWith('2')) };
  });
if (!variants.length) throw new Error(`no variants for ACTIONS=${onlyActions}`);

const url = baseUrl('SERVICES_SERVER_URL', 'http://services-server:8000');
const tenantId = env('TENANT_ID');
const userId = env('USER_ID', '');
const accountIds = env('ACCOUNT_IDS')
  .split(',')
  .map((s) => s.trim())
  .filter(Boolean);
const poolSize = Number(env('POOL_SIZE', '10'));
const sweep = env('SWEEP', 'false') === 'true';
const headers = {
  'Content-Type': 'application/json',
  'X-ACTION-TOKEN': env('ACTION_API_SERVER_TOKEN', ''),
  'x-tenant-id': tenantId,
  'x-user-id': userId,
};
const sessionVariables = { role: env('ROLE', 'tenant_admin'), tenant_id: tenantId, user_id: userId };
// services-server gives a metrics query 180s before it gives up; wait that long too.
const timeout = '190s';

const names = variants.map((v) => v.id);
const t = thresholds(3000, names);
if (sweep) t.http_req_failed = ['rate<0.01']; // a sweep should report every variant, not abort
// Per-account lines (first 8 characters of the id), overall and per action, so a slow metrics
// store shows up against its own cluster. setup() requests carry no account tag.
const actions = new Set(variants.map((v) => v.action));
for (const account of accountIds) {
  const tag = `account:${account.slice(0, 8)}`;
  t[`http_req_duration{${tag}}`] = [...t.http_req_duration];
  for (const action of actions) t[`http_req_duration{${tag},action:${action}}`] = [...t.http_req_duration];
}
export const options = {
  setupTimeout: '10m',
  scenarios: sweep
    ? { sweep: { executor: 'shared-iterations', exec: 'run', vus: 1, iterations: variants.length * accountIds.length, maxDuration: '2h' } }
    : scenarios('run'),
  thresholds: t,
};

const emptyResponse = new Rate('metrics_empty_response');

function post(path, action, input, tags) {
  const body = JSON.stringify({ action: { name: action }, input, request_query: '', session_variables: sessionVariables });
  return http.post(`${url}${path}`, body, { headers, timeout, tags: { action, ...tags } });
}

function setupRows(action, input) {
  const res = post('/rpc/query', action, input, { name: `setup:${action}` });
  if (res.status !== 200) throw new Error(`setup ${action}: HTTP ${res.status} ${String(res.body).slice(0, 300)}`);
  return res.json('rows') || [];
}

// One instant PromQL query through metrics_list; returns the label sets of its series.
function setupSeries(account, query) {
  const end = Date.now();
  const input = { request: { account_id: account, queries: { A: query }, instant: true, start_time: end - 3e5, end_time: end } };
  const res = post('/rpc/metrics', 'metrics_list', input, { name: 'setup:metrics_list' });
  if (res.status !== 200) {
    console.warn(`account ${account}: setup query failed, HTTP ${res.status} ${String(res.body).slice(0, 300)}`);
    return [];
  }
  const result = (res.json('results') || [])[0] || {};
  return (result.payload || []).map((s) => s.metric || {});
}

function shuffle(list) {
  const a = [...list];
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [a[i], a[j]] = [a[j], a[i]];
  }
  return a;
}

function discover(account) {
  const active = { is_active: { _eq: true } };
  // A random sample of the 50 largest: ordering alone picks mostly DaemonSets on a small cluster.
  const candidates = shuffle(
    setupRows('k8s_workloads_v2', {
      columns: ['name', 'namespace', 'kind'],
      where: { account_id: { _eq: account }, ...active, kind: { _in: ['Deployment', 'StatefulSet', 'DaemonSet'] }, ready_pods: { _gt: 0 } },
      order_by: [{ column: 'ready_pods', order: 'desc' }],
      limit: 50,
    }).map((w) => ({ ns: w.namespace, workload: w.name, kind: w.kind, kind_lower: w.kind.toLowerCase() }))
  );
  const workloads = candidates.slice(0, poolSize);

  const pods = setupRows('k8s_pods_v2', {
    columns: ['name', 'namespace', 'workload_name'],
    where: { account_id: { _eq: account }, ...active, status: { _eq: 'Running' } },
    order_by: [{ column: 'creation_time', order: 'desc' }],
    limit: poolSize,
  }).map((p) => ({ pod: p.name, pod_ns: p.namespace, pod_workload: p.workload_name || p.name }));

  // A node without an IP would turn instance=~"{{node_ip}}.*" into every instance.
  const nodeRows = setupRows('k8s_nodes_v2', {
    columns: ['name', 'internal_ip'],
    where: { cloud_account_id: { _eq: account }, ...active },
    order_by: [{ column: 'updated_at', order: 'desc' }],
    limit: 50,
  });
  const nodes = nodeRows
    .filter((n) => n.internal_ip)
    .slice(0, poolSize)
    .map((n) => ({ node: n.name, node_ip: n.internal_ip }));
  const noIp = nodeRows.filter((n) => !n.internal_ip).length;
  if (noIp) console.warn(`account ${account}: ${noIp} of ${nodeRows.length} active nodes have no internal IP; skipped`);

  // Rightsizing sizes one container of a Deployment or StatefulSet.
  const rs = [];
  for (const w of candidates.filter((w) => w.kind !== 'DaemonSet').slice(0, 5)) {
    const series = setupSeries(
      account,
      `max by (container) (container_memory_working_set_bytes{namespace="${w.ns}", pod=~"${w.workload}-.*", container!=""})`
    );
    const container = series.map((s) => s.container).find((c) => c && c !== 'POD');
    if (container) rs.push({ rs_ns: w.ns, rs_workload: w.workload, rs_kind_lower: w.kind_lower, rs_container: container });
    else console.warn(`account ${account}: no container series for ${w.kind} ${w.ns}/${w.workload}, not a rightsizing target`);
  }

  const pvcs = setupSeries(account, 'max by (namespace, persistentvolumeclaim) (kubelet_volume_stats_used_bytes)')
    .filter((s) => s.persistentvolumeclaim)
    .slice(0, poolSize)
    .map((s) => ({ pvc: s.persistentvolumeclaim, pvc_ns: s.namespace }));

  const pool = {
    workload: workloads,
    // entityValues() prefers a second entity that differs from the first, else reuses it.
    workload2: workloads.map((w) => ({ ns2: w.ns, workload2: w.workload })),
    pod: pods,
    node: nodes,
    node2: nodes.map((n) => ({ node2: n.node, node_ip2: n.node_ip })),
    rs,
    pvc: pvcs,
  };
  console.log(
    `account ${account}: ${workloads.length} workloads, ${pods.length} pods, ${nodes.length} nodes, ${rs.length} rightsizing containers, ${pvcs.length} PVCs`
  );
  return pool;
}

export function setup() {
  const pools = {};
  for (const account of accountIds) pools[account] = discover(account);
  const runnable = variants.filter((v) => accountIds.some((a) => v.needs.every((e) => pools[a][e].length)));
  const missing = variants.filter((v) => !runnable.includes(v)).map((v) => v.id);
  if (missing.length) console.warn(`no account has the entities for: ${missing.join(', ')}`);
  console.log(`${runnable.length} of ${variants.length} variants runnable across ${accountIds.length} accounts`);
  return { pools };
}

const pick = (list) => list[Math.floor(Math.random() * list.length)];

function fill(input, values) {
  return JSON.parse(JSON.stringify(input).replace(/\{\{(\w+)\}\}/g, (_, name) => JSON.stringify(String(values[name])).slice(1, -1)));
}

// Picks one entity per needed kind; the second workload/node differs from the first.
function entityValues(pool, needs) {
  const values = {};
  const chosen = {};
  for (const e of needs) {
    let list = pool[e];
    if (e === 'workload2' && chosen.workload) list = list.filter((w) => w.workload2 !== chosen.workload.workload || w.ns2 !== chosen.workload.ns);
    if (e === 'node2' && chosen.node) list = list.filter((n) => n.node2 !== chosen.node.node);
    chosen[e] = pick(list.length ? list : pool[e]);
    Object.assign(values, chosen[e]);
  }
  return values;
}

function seriesCount(action, body) {
  if (action === 'metrics_list' || action === 'metrics_aggregate_utilisation') {
    return (body.results || []).reduce((n, r) => n + (r.payload || []).length, 0);
  }
  return Array.isArray(body) ? body.length : 0;
}

export function run(data) {
  let variant;
  let account;
  if (sweep) {
    const i = exec.scenario.iterationInTest;
    variant = variants[Math.floor(i / accountIds.length)];
    account = accountIds[i % accountIds.length];
    if (!variant.needs.every((e) => data.pools[account][e].length)) {
      console.log(`sweep ${variant.id} ${account.slice(0, 8)} skipped: no ${variant.needs.filter((e) => !data.pools[account][e].length).join('/')}`);
      return;
    }
  } else {
    variant = pick(variants);
    const accounts = accountIds.filter((a) => variant.needs.every((e) => data.pools[a][e].length));
    if (!accounts.length) return; // setup() already warned about this variant
    account = pick(accounts);
  }

  const request = { account_id: account, ...fill(variant.input, entityValues(data.pools[account], variant.needs)) };
  if (variant.window) {
    request.end_time = Date.now();
    request.start_time = request.end_time - WINDOW_MS[variant.window];
  }
  const res = post('/rpc/metrics', variant.action, { request }, { name: variant.id, account: account.slice(0, 8) });

  let body = null;
  try {
    body = res.json();
  } catch (_) {
    // non-JSON body: the status check below reports it
  }
  const keyErrors = ((body && body.results) || []).filter((r) => r.error).map((r) => `${r.query_key}: ${r.error}`);
  const series = res.status === 200 && body ? seriesCount(variant.action, body) : 0;
  if (res.status === 200 && body && !keyErrors.length) emptyResponse.add(series === 0, { name: variant.id });
  check(res, {
    'status 200': (r) => r.status === 200,
    'JSON body': (r) => r.status !== 200 || body !== null,
    'no per-key errors': () => keyErrors.length === 0,
  });
  if (sweep) {
    const detail =
      res.status === 200
        ? `series=${series}${keyErrors.length ? ` key_errors=${keyErrors.join(' | ').slice(0, 300)}` : ''}`
        : String(res.body).slice(0, 300);
    console.log(`sweep ${variant.id} ${account.slice(0, 8)} status=${res.status} ms=${Math.round(res.timings.duration)} ${detail}`);
  }
  thinkTime();
}
