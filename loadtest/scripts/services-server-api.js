// services-server API pods, called directly the way app's RPC gateway calls them
// (bypassing app). Each iteration replays one action from services-server-actions.json,
// picked by weight. The default mix is read-only; add recorded payloads for more coverage.
import http from 'k6/http';
import { check } from 'k6';
import { env, baseUrl, scenarios, thresholds, thinkTime, weightedPicker } from './lib.js';

const actions = JSON.parse(open('./services-server-actions.json'));
const pick = weightedPicker(actions);

const url = baseUrl('SERVICES_SERVER_URL', 'http://services-server:8000');
const tenantId = env('TENANT_ID');
// Without USER_ID the server builds a tenant-admin context; with it, the per-user
// RBAC lookup runs on every request, as it does for real UI traffic.
const userId = env('USER_ID', '');
const headers = {
  'Content-Type': 'application/json',
  'X-ACTION-TOKEN': env('ACTION_API_SERVER_TOKEN', ''),
  'x-tenant-id': tenantId,
  'x-user-id': userId,
};
const sessionVariables = { role: env('ROLE', 'tenant_admin'), tenant_id: tenantId, user_id: userId };

export const options = {
  scenarios: scenarios('run'),
  thresholds: thresholds(1500, actions.map((a) => a.name)),
};

export function run() {
  const a = pick();
  const body = JSON.stringify({ action: { name: a.name }, input: a.input, request_query: '', session_variables: sessionVariables });
  const res = http.post(`${url}${a.path}`, body, { headers, tags: { name: a.name } });
  check(res, {
    'status 200': (r) => r.status === 200,
    'no errors in body': (r) => r.status !== 200 || !(r.json('errors') || []).length,
  });
  thinkTime();
}
