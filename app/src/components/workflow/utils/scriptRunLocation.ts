// Where a Run Script step runs and as whom, which executors its account allows,
// and whether its script signs itself in. Mirrors CheckAccountSupports and
// ScriptLogin in runbook-server/internal/tasks/scripting/executors, which refuse
// the pairs on save and flag the login code on validate. Both sides are tested
// against executors/testdata/script_step_rules.json — keep them in step.

/** Provider token from the sidebar's resolveAccountProviderType; '' = unknown. */
export type ScriptAccountProvider = 'k8s' | 'aws' | 'azure' | 'gcp' | '';

const ACCOUNT_PROVIDERS_FOR: Record<string, ScriptAccountProvider[]> = {
  kubernetes: ['aws', 'azure', 'gcp'],
  aws_ssm: ['aws'],
  azure_run_command: ['azure'],
  gcp_compute_ssh: ['gcp'],
};

const ACCOUNT_NOUN: Record<string, string> = { aws: 'an AWS', azure: 'an Azure', gcp: 'a GCP' };

const EXECUTOR_LABEL: Record<string, string> = {
  aws_ssm: 'AWS SSM',
  azure_run_command: 'Azure Run Command',
  gcp_compute_ssh: 'GCP Compute SSH',
};

/** Whether a step whose account has `provider` can run on `executor`. Unknown sides are allowed. */
export const executorSupportsAccount = (executor: string, provider: ScriptAccountProvider): boolean => {
  const wanted = ACCOUNT_PROVIDERS_FOR[executor];
  return !provider || !wanted || wanted.includes(provider);
};

// The executor the server picks when the step names none (NewExecutor).
const defaultExecutorFor = (provider: ScriptAccountProvider): string => (provider === 'k8s' ? 'agent' : 'kubernetes');

export interface ScriptRunLocation {
  tone: 'info' | 'warning';
  message: string;
}

/**
 * One line telling the author where the step will run and which identity it
 * uses, or why it cannot run. Null when the account is unknown (none picked,
 * a template, or a provider this does not describe).
 */
export const describeScriptRunLocation = (
  executorType: string | undefined,
  provider: ScriptAccountProvider,
  accountName: string,
  brand: string
): ScriptRunLocation | null => {
  if (!provider) return null;
  const executor = executorType || defaultExecutorFor(provider);

  if (!executorSupportsAccount(executor, provider)) {
    const needed = ACCOUNT_NOUN[ACCOUNT_PROVIDERS_FOR[executor][0]];
    if (provider === 'k8s' && executor === 'kubernetes') {
      return {
        tone: 'warning',
        message: `${accountName} is a Kubernetes cluster, which has no cloud credentials for the Kubernetes executor to use. Set Executor Type to Agent to run the script inside that cluster.`,
      };
    }
    if (provider === 'k8s') {
      return {
        tone: 'warning',
        message: `${EXECUTOR_LABEL[executor]} needs ${needed} account, but ${accountName} is a Kubernetes cluster. Choose that account, or set Executor Type to Agent to run the script inside the cluster.`,
      };
    }
    return { tone: 'warning', message: `${EXECUTOR_LABEL[executor]} needs ${needed} account, and ${accountName} is not one.` };
  }

  switch (executor) {
    case 'agent':
      return provider === 'k8s' ? { tone: 'info', message: `Runs inside cluster ${accountName}, as the ${brand} agent's service account.` } : null;
    case 'kubernetes':
      return { tone: 'info', message: `Runs on ${brand}, signed in to ${accountName}.` };
    case 'aws_ssm':
    case 'azure_run_command':
    case 'gcp_compute_ssh':
      return { tone: 'info', message: `Runs on the target machine through ${EXECUTOR_LABEL[executor]}, requested with ${accountName}.` };
    case 'ssh':
      return { tone: 'info', message: 'Runs on the target host over SSH, as the user in the SSH integration.' };
    default:
      return null;
  }
};

// Ways a script signs itself in. The step already runs as the account it names
// (or, on the agent, inside the cluster), so a login either fails or silently
// swaps to an identity nobody chose.
const LOGIN_PATTERNS: Array<[string, RegExp]> = [
  ['sts assume-role', /\bsts\s+assume-role\b/i],
  ['a hard-coded role ARN', /arn:aws:iam::\d{12}:role\//],
  ['aws eks update-kubeconfig', /\bupdate-kubeconfig\b/i],
  ['az login', /\baz\s+login\b/i],
  ['gcloud auth', /\bgcloud\s+auth\s+(activate-service-account|login)\b/i],
  ['unsetting the in-cluster Kubernetes settings', /\bunset\b[^\n;]*\bKUBERNETES_SERVICE_HOST\b/],
  ['AWS access keys', /\bAWS_(ACCESS_KEY_ID|SECRET_ACCESS_KEY|SESSION_TOKEN)\s*=/],
];

const AWS_KEY_NAMES = new Set(['AWS_ACCESS_KEY_ID', 'AWS_SECRET_ACCESS_KEY', 'AWS_SESSION_TOKEN']);

/** How a script, or the environment it is given, signs itself in — '' when it does not. */
export const scriptLogin = (script: string | undefined, env?: Record<string, unknown> | null): string => {
  const text = script || '';
  for (const [what, re] of LOGIN_PATTERNS) {
    if (re.test(text)) return what;
  }
  if (env && typeof env === 'object' && Object.keys(env).some((k) => AWS_KEY_NAMES.has(k.toUpperCase()))) {
    return 'AWS access keys';
  }
  return '';
};
