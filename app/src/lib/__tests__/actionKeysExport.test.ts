import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { classifyAction, permissionKey } from '../permissionCatalog';

// The gateway classifies each action into the grant key it forwards as
// action.permission. A caller that reaches a backend without the gateway
// (llm-server, runbook-server, internal jobs) sends no key, so each backend
// carries this table and looks the key up by action name: every handler then
// checks the one key the role editor grants, whoever called it.
//
// Regenerate after changing the catalog or actions.yaml:
//   UPDATE_ACTION_KEYS=1 npx jest src/lib/__tests__/actionKeysExport.test.ts
const REPO = path.join(process.cwd(), '..');
const COPIES = [
  path.join(REPO, 'api-server/services/security/action_keys.json'),
  path.join(REPO, 'runbook-server/services/security/action_keys.json'),
];

function readActionNames(p: string): string[] {
  const doc = yaml.load(fs.readFileSync(p, 'utf8')) as { actions?: Array<{ name?: string }> };
  return (doc.actions ?? []).map((a) => a.name ?? '').filter(Boolean);
}

function actionKeysJson(): string {
  const names = [...new Set([...readActionNames('src/lib/actions.yaml'), ...readActionNames('src/ee/actions.yaml')])].sort();
  // A non-grantable action is listed with an empty key, so a backend can tell
  // it from an unknown one and refuse a key a caller sends for it.
  const keys: Record<string, string> = {};
  for (const name of names) {
    const c = classifyAction(name);
    keys[name] = c ? permissionKey(c.module, c.class) : '';
  }
  return JSON.stringify(keys, null, 2) + '\n';
}

// The actions actions.yaml flags `account_enforced`, with the backend whose
// handler the flag vouches for. Each backend's TestAccountEnforcedActionsHaveA
// BoundGrantCase reads this file and requires a bound-grant test case for every
// action of its own; a flag on any other backend fails here, since no test there
// would pin it. Regenerated with action_keys.json (same env var).
const ENFORCED_COPIES = [
  path.join(REPO, 'api-server/services/security/account_enforced_actions.json'),
  path.join(REPO, 'runbook-server/services/security/account_enforced_actions.json'),
];

const SERVICE_OF_HANDLER: Array<[string, string]> = [
  ['{{SERVICE_API_SERVER_URL}}', 'api-server'],
  ['{{WORKFLOW_SERVER_URL}}', 'runbook-server'],
];

function enforcedService(name: string, handler: string): string {
  const hit = SERVICE_OF_HANDLER.find(([prefix]) => handler.startsWith(prefix));
  if (!hit) throw new Error(`${name} is account_enforced but its handler ${handler} has no bound-grant test table`);
  return hit[1];
}

type FlaggedYaml = { actions?: Array<{ name?: string; definition?: { handler?: string; account_enforced?: boolean } }> };

function accountEnforcedJson(): string {
  const out: Record<string, string> = {};
  for (const file of ['src/lib/actions.yaml', 'src/ee/actions.yaml']) {
    const doc = yaml.load(fs.readFileSync(file, 'utf8')) as FlaggedYaml;
    for (const a of doc.actions ?? []) {
      if (a.name && a.definition?.account_enforced) out[a.name] = enforcedService(a.name, a.definition.handler ?? '');
    }
  }
  const sorted = Object.fromEntries(Object.entries(out).sort(([a], [b]) => a.localeCompare(b)));
  return JSON.stringify(sorted, null, 2) + '\n';
}

describe('account_enforced_actions.json', () => {
  it('matches the account_enforced flags in every backend copy', () => {
    const want = accountEnforcedJson();
    for (const file of ENFORCED_COPIES) {
      if (process.env.UPDATE_ACTION_KEYS) fs.writeFileSync(file, want);
      expect(fs.readFileSync(file, 'utf8')).toEqual(want);
    }
  });

  it('refuses a flag on a backend with no bound-grant test table', () => {
    expect(() => enforcedService('llm_ask', '{{LLM_SERVER_URL}}/rpc/llm')).toThrow(/no bound-grant test table/);
    expect(enforcedService('metrics_list', '{{SERVICE_API_SERVER_URL}}/rpc/metrics')).toBe('api-server');
    expect(enforcedService('bees_list', '{{WORKFLOW_SERVER_URL}}/bees/list')).toBe('runbook-server');
  });
});

describe('action_keys.json', () => {
  it('matches the gateway classification in every backend copy', () => {
    const want = actionKeysJson();
    for (const file of COPIES) {
      if (process.env.UPDATE_ACTION_KEYS) fs.writeFileSync(file, want);
      expect(fs.readFileSync(file, 'utf8')).toEqual(want);
    }
  });
});
