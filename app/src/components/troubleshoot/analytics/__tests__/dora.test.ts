import {
  classifyChangeFailureRate,
  classifyDeploymentFrequency,
  classifyRecoveryMinutes,
  deriveDora,
  formatDeployRate,
  formatRecovery,
} from '../dora';

const base = { deploys: 0, failedChanges: 0, deployedWorkloads: 0, resolvedIssues: 0, medianRecoveryMinutes: 0, windowDays: 30 };

describe('DORA levels', () => {
  it('classifies deployment frequency per workload per day', () => {
    expect(classifyDeploymentFrequency(3)).toBe('Elite');
    expect(classifyDeploymentFrequency(1)).toBe('Elite');
    expect(classifyDeploymentFrequency(1 / 7)).toBe('High');
    expect(classifyDeploymentFrequency(1 / 8)).toBe('Medium');
    expect(classifyDeploymentFrequency(1 / 30)).toBe('Medium');
    expect(classifyDeploymentFrequency(1 / 31)).toBe('Low');
  });

  it('classifies change failure rate with inclusive upper bounds', () => {
    expect(classifyChangeFailureRate(0)).toBe('Elite');
    expect(classifyChangeFailureRate(0.05)).toBe('Elite');
    expect(classifyChangeFailureRate(0.051)).toBe('High');
    expect(classifyChangeFailureRate(0.1)).toBe('High');
    expect(classifyChangeFailureRate(0.15)).toBe('Medium');
    expect(classifyChangeFailureRate(0.16)).toBe('Low');
  });

  it('classifies recovery time in minutes', () => {
    expect(classifyRecoveryMinutes(59)).toBe('Elite');
    expect(classifyRecoveryMinutes(60)).toBe('High');
    expect(classifyRecoveryMinutes(24 * 60)).toBe('Medium');
    expect(classifyRecoveryMinutes(7 * 24 * 60)).toBe('Low');
  });
});

describe('deriveDora', () => {
  it('rates cadence per workload, not tenant-wide', () => {
    // 200 workloads each deploying about monthly: 200 deploys in 30 days is
    // 6.7 a day overall, which must not read as Elite.
    const model = deriveDora({ ...base, deploys: 200, deployedWorkloads: 200 });
    expect(model.deploymentFrequency.perDay).toBeCloseTo(6.67, 1);
    expect(model.deploymentFrequency.level).toBe('Medium');
  });

  it('withholds levels below the sample floor but still reports what it has', () => {
    const model = deriveDora({ ...base, deploys: 4, failedChanges: 1, deployedWorkloads: 2, resolvedIssues: 4, medianRecoveryMinutes: 30 });
    expect(model.deploymentFrequency.level).toBeNull();
    expect(model.changeFailureRate.rate).toBe(0.25);
    expect(model.changeFailureRate.level).toBeNull();
    expect(model.recovery.minutes).toBe(30);
    expect(model.recovery.level).toBeNull();
  });

  it('reports no rate and no recovery when nothing happened', () => {
    const model = deriveDora(base);
    expect(model.deploymentFrequency.perDay).toBe(0);
    expect(model.changeFailureRate.rate).toBeNull();
    expect(model.recovery.minutes).toBeNull();
  });

  it('classifies once there are enough samples, and never reports more failures than deploys', () => {
    const model = deriveDora({ ...base, deploys: 10, failedChanges: 50, deployedWorkloads: 1, resolvedIssues: 6, medianRecoveryMinutes: 45 });
    expect(model.changeFailureRate.rate).toBe(1);
    expect(model.changeFailureRate.level).toBe('Low');
    expect(model.recovery.level).toBe('Elite');
  });

  it('treats missing or non-finite inputs as zero', () => {
    const model = deriveDora({ ...base, deploys: Number.NaN, medianRecoveryMinutes: Number.POSITIVE_INFINITY, resolvedIssues: 9 });
    expect(model.changeFailureRate.rate).toBeNull();
    expect(model.recovery.minutes).toBeNull();
  });
});

describe('formatting', () => {
  it('switches to a weekly rate below one deploy a day', () => {
    expect(formatDeployRate(3.24)).toBe('3.2 per day');
    expect(formatDeployRate(0.5)).toBe('3.5 per week');
  });

  it('formats recovery in the largest sensible unit', () => {
    expect(formatRecovery(42.4)).toBe('42 min');
    expect(formatRecovery(90)).toBe('1.5 hrs');
    expect(formatRecovery(3 * 24 * 60)).toBe('3.0 days');
  });
});
