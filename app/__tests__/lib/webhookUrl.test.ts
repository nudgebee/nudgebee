import { buildWebhookUrl, getWebhookToken } from '@lib/webhookUrl';

describe('getWebhookToken', () => {
  it('returns the token value from the config-values array', () => {
    expect(
      getWebhookToken([
        { name: 'account_id', value: 'x' },
        { name: 'token', value: 'abc123' },
      ])
    ).toBe('abc123');
  });

  it('returns "" when there is no token entry', () => {
    expect(getWebhookToken([{ name: 'account_id', value: 'x' }])).toBe('');
  });

  it('returns "" for a missing or non-array input', () => {
    expect(getWebhookToken(undefined)).toBe('');
    expect(getWebhookToken(null)).toBe('');
    expect(getWebhookToken('not-an-array' as unknown as [])).toBe('');
  });
});

describe('buildWebhookUrl', () => {
  // Endpoint per webhook type, mirroring webhookConfig in IntegrationDynamicFormModal.
  // Locks the derivation (strip _webhook, _ -> -) against that map so the two can't drift.
  const cases: Array<[string, string]> = [
    ['pagerduty_webhook', 'pagerduty'],
    ['zenduty_webhook', 'zenduty'],
    ['prometheus_alertmanager_webhook', 'prometheus-alertmanager'],
    ['datadog_webhook', 'datadog'],
    ['azure_monitor_webhook', 'azure-monitor'],
    ['gcp_monitoring_webhook', 'gcp-monitoring'],
    ['servicenow_webhook', 'servicenow'],
    ['newrelic_webhook', 'newrelic'],
    ['dynatrace_webhook', 'dynatrace'],
    ['splunk_webhook', 'splunk'],
    ['grafana_webhook', 'grafana'],
    ['solarwinds_webhook', 'solarwinds'],
    ['elasticsearch_webhook', 'elasticsearch'],
    ['openobserve_webhook', 'openobserve'],
    ['workflow_webhook', 'workflow'],
    ['cubeapm_webhook', 'cubeapm'],
    ['signoz_webhook', 'signoz'],
    ['event_webhook', 'event'],
  ];

  it.each(cases)('derives the %s endpoint', (integrationName, endpoint) => {
    expect(buildWebhookUrl(integrationName, 'tok')).toContain(`/api/webhooks/${endpoint}?token=tok`);
  });

  it('URL-encodes special characters in the token', () => {
    expect(buildWebhookUrl('pagerduty_webhook', 'a/b+c=d&e')).toContain(`?token=${encodeURIComponent('a/b+c=d&e')}`);
  });

  it('leaves a hex token unchanged (encoding is a no-op)', () => {
    expect(buildWebhookUrl('pagerduty_webhook', '0f0f0f0f0f0f')).toContain('?token=0f0f0f0f0f0f');
  });

  it('returns "" for a non-webhook integration', () => {
    expect(buildWebhookUrl('pagerduty', 'tok')).toBe('');
    expect(buildWebhookUrl('datadog', 'tok')).toBe('');
  });

  it('returns "" when the token is missing', () => {
    expect(buildWebhookUrl('pagerduty_webhook', '')).toBe('');
  });

  it('returns "" for an empty integration name', () => {
    expect(buildWebhookUrl('', 'tok')).toBe('');
    expect(buildWebhookUrl(undefined, 'tok')).toBe('');
  });
});
