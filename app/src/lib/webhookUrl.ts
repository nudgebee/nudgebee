/**
 * Inbound-webhook receiver URL for a webhook integration row.
 *
 * The endpoint segment follows the `/api/webhooks/{endpoint}` route convention:
 * an integration's endpoint is its name with the `_webhook` suffix dropped and
 * underscores turned into hyphens (`pagerduty_webhook` -> `pagerduty`,
 * `prometheus_alertmanager_webhook` -> `prometheus-alertmanager`). That is the
 * same mapping `webhookConfig` in IntegrationDynamicFormModal spells out
 * per-integration, so deriving it here keeps the table's URL identical to the
 * one the Add/Edit modal shows after a webhook is created — without a second
 * copy of that map to drift.
 */

type ConfigValue = { name?: string; value?: string };

// The shared secret that identifies a webhook, pulled from the row's config
// values. Webhook tokens are returned in the listing unmasked (unlike provider
// credentials), which is what lets us build the URL client-side.
export const getWebhookToken = (configValues: ConfigValue[] | undefined | null): string => {
  if (!Array.isArray(configValues)) return '';
  return configValues.find((c) => c?.name === 'token')?.value || '';
};

export const buildWebhookUrl = (integrationName: string | undefined | null, token: string): string => {
  if (!integrationName || !integrationName.endsWith('_webhook') || !token) return '';
  // Origin is only known in the browser; skip during SSR (the row re-renders on the client).
  if (typeof window === 'undefined') return '';
  const endpoint = integrationName.replace(/_webhook$/, '').replace(/_/g, '-');
  // Encode the token: the receiver reads it with url.Query().Get("token"), which
  // decodes `+` to a space — so a token with URL-special chars must be encoded to
  // survive the round trip. Tokens are hex today (a no-op here), so this stays
  // byte-identical to the URL the modal shows while guarding the general case.
  return `${window.location.origin}/api/webhooks/${endpoint}?token=${encodeURIComponent(token)}`;
};

export const buildWebhookBaseUrl = (integrationName: string | undefined | null): string => {
  if (!integrationName || !integrationName.endsWith('_webhook')) return '';
  if (typeof window === 'undefined') return '';
  const endpoint = integrationName.replace(/_webhook$/, '').replace(/_/g, '-');
  return `${window.location.origin}/api/webhooks/${endpoint}`;
};

// Alertmanager receiver that sends the token as `Authorization: Bearer` rather
// than in the URL: Alertmanager logs the full webhook URL on every failed
// delivery, but never http_config.authorization. A receiver alone receives
// nothing, so the snippet also carries the child route; `continue: true` keeps
// the user's existing receivers getting the same alerts.
export const buildAlertmanagerReceiverSnippet = (url: string, token: string): string => {
  return `route:
  routes:
    # Add as the first child of your existing route.
    - receiver: 'nudgebee'
      continue: true

receivers:
  - name: 'nudgebee'
    webhook_configs:
      - url: '${url}'
        send_resolved: true
        http_config:
          authorization:
            type: Bearer
            credentials: '${token}'`;
};
