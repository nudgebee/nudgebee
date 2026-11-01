import fs from 'fs';
import path from 'path';

import { executorSupportsAccount, scriptLogin, ScriptAccountProvider } from '../scriptRunLocation';

// The same contract runbook-server's executors package is tested against.
const FIXTURE = path.resolve(__dirname, '../../../../../../runbook-server/internal/tasks/scripting/executors/testdata/script_step_rules.json');

interface Rules {
  account_executor: Array<{ executor: string; provider: string; ok: boolean }>;
  script_login: Array<{ script: string; env?: Record<string, string>; login: string }>;
}

const rules: Rules = JSON.parse(fs.readFileSync(FIXTURE, 'utf8'));

// The sidebar resolves only these providers; any other one reaches the app as ''
// (unknown), and the server alone refuses it.
const PROVIDER_TOKEN: Record<string, ScriptAccountProvider> = { K8s: 'k8s', AWS: 'aws', Azure: 'azure', GCP: 'gcp', '': '' };

describe('Run Script rules match runbook-server', () => {
  it.each(rules.account_executor.filter((c) => c.provider in PROVIDER_TOKEN))(
    '$executor on a $provider account → $ok',
    ({ executor, provider, ok }) => {
      expect(executorSupportsAccount(executor, PROVIDER_TOKEN[provider])).toBe(ok);
    }
  );

  it.each(rules.script_login)('login in "$script" → "$login"', ({ script, env, login }) => {
    expect(scriptLogin(script, env)).toBe(login);
  });
});
