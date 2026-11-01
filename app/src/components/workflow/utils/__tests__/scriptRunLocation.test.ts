import { describeScriptRunLocation, executorSupportsAccount } from '../scriptRunLocation';

describe('executorSupportsAccount', () => {
  it.each([
    ['kubernetes', 'k8s', false],
    ['aws_ssm', 'k8s', false],
    ['azure_run_command', 'k8s', false],
    ['gcp_compute_ssh', 'k8s', false],
    ['aws_ssm', 'azure', false],
    ['agent', 'k8s', true],
    ['ssh', 'k8s', true],
    ['kubernetes', 'aws', true],
    ['kubernetes', 'gcp', true],
    ['aws_ssm', 'aws', true],
    ['kubernetes', '', true],
  ] as const)('%s on a %s account → %s', (executor, provider, ok) => {
    expect(executorSupportsAccount(executor, provider)).toBe(ok);
  });
});

describe('describeScriptRunLocation', () => {
  it('warns when a cluster account is set to run on the Kubernetes executor', () => {
    expect(describeScriptRunLocation('kubernetes', 'k8s', 'prod-cluster', 'Acme')).toEqual({
      tone: 'warning',
      message: expect.stringContaining('Set Executor Type to Agent'),
    });
  });

  it('names the cloud a run-command executor needs', () => {
    expect(describeScriptRunLocation('aws_ssm', 'k8s', 'prod-cluster', 'Acme')?.message).toContain('AWS SSM needs an AWS account');
    expect(describeScriptRunLocation('gcp_compute_ssh', 'azure', 'sub-1', 'Acme')?.message).toBe(
      'GCP Compute SSH needs a GCP account, and sub-1 is not one.'
    );
  });

  it('says a cluster account runs inside the cluster as the agent, by default or when chosen', () => {
    const expected = { tone: 'info', message: "Runs inside cluster prod-cluster, as the Acme agent's service account." };
    expect(describeScriptRunLocation('', 'k8s', 'prod-cluster', 'Acme')).toEqual(expected);
    expect(describeScriptRunLocation('agent', 'k8s', 'prod-cluster', 'Acme')).toEqual(expected);
  });

  it('says a cloud account runs on the platform signed in to that account', () => {
    expect(describeScriptRunLocation(undefined, 'aws', 'aws-main', 'Acme')?.message).toBe('Runs on Acme, signed in to aws-main.');
  });

  it('describes the run-command and SSH executors', () => {
    expect(describeScriptRunLocation('aws_ssm', 'aws', 'aws-main', 'Acme')?.message).toBe(
      'Runs on the target machine through AWS SSM, requested with aws-main.'
    );
    expect(describeScriptRunLocation('ssh', 'k8s', 'prod-cluster', 'Acme')?.message).toContain('over SSH');
  });

  it('stays quiet when the account is unknown', () => {
    expect(describeScriptRunLocation('kubernetes', '', 'x', 'Acme')).toBeNull();
  });
});
