import { buildNubiOptimizePrompt } from '@utils/nubiPromptBuilder';

const baseContext = {
  ruleName: 'Gcp Cloudsql Disk Utilization Alarm Missing',
  category: 'Configuration',
  severity: 'Critical',
  resourceName: 'demo-postgres-instance',
  resourceType: 'Cloud SQL',
  accountName: 'gcp-dev',
};

describe('buildNubiOptimizePrompt alarm configuration section', () => {
  it('omits the alert configuration section when no alarmConfig is provided', () => {
    const prompt = buildNubiOptimizePrompt(baseContext);
    expect(prompt).not.toContain('Alert Configuration');
  });

  it('renders exact alarm parameters and the effective trigger duration', () => {
    const prompt = buildNubiOptimizePrompt({
      ...baseContext,
      alarmConfig: {
        metric_name: 'DiskUtilization',
        statistic: 'ALIGN_MEAN',
        threshold: 0.8,
        comparison_operator: 'COMPARISON_GT',
        period: 300,
        evaluation_periods: 2,
      },
    });

    expect(prompt).toContain('### Alert Configuration (authoritative)');
    expect(prompt).toContain('**Metric:** DiskUtilization');
    expect(prompt).toContain('**Statistic:** ALIGN_MEAN');
    expect(prompt).toContain('**Condition:** value > 0.8');
    expect(prompt).toContain('**Evaluation period:** 5 min');
    expect(prompt).toContain('the condition must hold for 10 min total (2 consecutive 5 min periods)');
    expect(prompt).toContain('duration (retest window) = 600s, not 300s');
    expect(prompt).toContain('do not substitute typical documentation defaults');
  });

  it('maps AWS-style comparison operators to symbols', () => {
    const prompt = buildNubiOptimizePrompt({
      ...baseContext,
      alarmConfig: {
        metric_name: 'FreeStorageSpace',
        threshold: 1000000,
        comparison_operator: 'LessThanThreshold',
        period: 300,
        evaluation_periods: 2,
      },
    });
    expect(prompt).toContain('**Condition:** value < 1000000');
  });

  it('describes M-of-N evaluation when datapoints_to_alarm is below evaluation_periods', () => {
    const prompt = buildNubiOptimizePrompt({
      ...baseContext,
      alarmConfig: {
        metric_name: 'CPUUtilization',
        threshold: 80,
        comparison_operator: 'GreaterThanThreshold',
        period: 300,
        evaluation_periods: 3,
        datapoints_to_alarm: 2,
      },
    });
    expect(prompt).toContain('at least 2 of 3 evaluation periods (5 min each) must breach within 15 min');
  });

  it('describes a single evaluation period without a duration contrast', () => {
    const prompt = buildNubiOptimizePrompt({
      ...baseContext,
      alarmConfig: {
        metric_name: 'BurstBalance',
        threshold: 20,
        comparison_operator: 'LessThanThreshold',
        period: 300,
        evaluation_periods: 1,
      },
    });
    expect(prompt).toContain('the condition must hold for 5 min (a single evaluation period)');
    expect(prompt).not.toContain('not 300s');
  });

  it('uses the expression label for metric-math alarms', () => {
    const prompt = buildNubiOptimizePrompt({
      ...baseContext,
      alarmConfig: {
        threshold: 90,
        comparison_operator: 'GreaterThanThreshold',
        period: 300,
        evaluation_periods: 2,
        metrics: [
          { label: 'Memory Usage Percentage', expression: 'm1/m2*100', return_data: true },
          { label: 'm1', return_data: false },
        ],
      },
    });
    expect(prompt).toContain('**Metric:** Memory Usage Percentage');
  });

  it('skips the effective trigger line when period information is missing', () => {
    const prompt = buildNubiOptimizePrompt({
      ...baseContext,
      alarmConfig: {
        metric_name: 'DiskUtilization',
        threshold: 0.8,
        comparison_operator: 'COMPARISON_GT',
      },
    });
    expect(prompt).toContain('### Alert Configuration (authoritative)');
    expect(prompt).toContain('**Condition:** value > 0.8');
    expect(prompt).not.toContain('Effective trigger');
  });
});
