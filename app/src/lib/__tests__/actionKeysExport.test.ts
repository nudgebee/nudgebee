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

describe('action_keys.json', () => {
  it('matches the gateway classification in every backend copy', () => {
    const want = actionKeysJson();
    for (const file of COPIES) {
      if (process.env.UPDATE_ACTION_KEYS) fs.writeFileSync(file, want);
      expect(fs.readFileSync(file, 'utf8')).toEqual(want);
    }
  });
});
