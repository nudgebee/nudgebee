import { load } from 'js-yaml';
import { buildAlertmanagerReceiverSnippet, buildWebhookBaseUrl, buildWebhookUrl, getWebhookToken } from '@lib/webhookUrl';

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
    ['orion_webhook', 'orion'],
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

describe('buildWebhookBaseUrl', () => {
  it('returns base webhook url without token query string', () => {
    expect(buildWebhookBaseUrl('prometheus_alertmanager_webhook')).toContain('/api/webhooks/prometheus-alertmanager');
    expect(buildWebhookBaseUrl('prometheus_alertmanager_webhook')).not.toContain('?token=');
  });

  it('returns "" for a non-webhook integration or invalid name', () => {
    expect(buildWebhookBaseUrl('prometheus_alertmanager')).toBe('');
    expect(buildWebhookBaseUrl('')).toBe('');
    expect(buildWebhookBaseUrl(undefined)).toBe('');
  });
});

describe('buildAlertmanagerReceiverSnippet', () => {
  it('formats receiver snippet with authorization credentials', () => {
    const url = 'https://example.com/api/webhooks/prometheus-alertmanager';
    const token = 'secret-token-123';
    const snippet = buildAlertmanagerReceiverSnippet(url, token);
    expect(snippet).toContain(`url: '${url}'`);
    expect(snippet).toContain(`credentials: '${token}'`);
    expect(snippet).toContain('http_config:');
    expect(snippet).toContain('authorization:');
  });

  // Parsed as Alertmanager would read it: the token only in the Bearer header,
  // and a child route, since a receiver no route points at receives nothing.
  it('is valid YAML that routes to a Bearer-authenticated receiver', () => {
    const url = 'https://example.com/api/webhooks/prometheus-alertmanager?cluster=acme';
    const config = load(buildAlertmanagerReceiverSnippet(url, 'tok')) as {
      route: { routes: { receiver: string; continue: boolean }[] };
      receivers: { name: string; webhook_configs: { url: string; send_resolved: boolean; http_config: unknown }[] }[];
    };
    expect(config.route.routes).toEqual([{ receiver: 'nudgebee', continue: true }]);
    expect(config.receivers).toEqual([
      {
        name: 'nudgebee',
        webhook_configs: [{ url, send_resolved: true, http_config: { authorization: { type: 'Bearer', credentials: 'tok' } } }],
      },
    ]);
  });
});
