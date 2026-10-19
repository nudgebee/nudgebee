// Per-provider integration grants (#40997): a custom role can manage the
// integrations of one observability provider (e.g. Datadog) without managing
// every integration in the tenant.
//
// A grant is the synthetic module `integrations.<type>` at class Write, where
// <type> is the stored integration type lowercased (integrations.type, the
// `integration_name` the write actions carry). It covers only
// PROVIDER_GRANT_ACTIONS; listing and the other integration reads stay on the
// tenant-wide `integrations:Read`, which the role editor ticks alongside.
//
// The gateway admits PROVIDER_GRANT_ACTIONS for a holder of ANY provider grant;
// the API server decides the provider (api-server/services/api/
// actions_integration_authz.go, observabilityIntegrationTypes), against the
// stored type when the request names an existing integration by id. The Go
// list and this one are pinned to each other by
// TestObservabilityIntegrationTypesMatchApp.
//
// Not a token scope: the API server never sees an API key's scopes, so a key
// limited to one provider would be enforced at the gateway alone. The token
// catalog does not list these modules and actionAllowedByTokenScopes does not
// match them.

import type { CatalogEntry } from '@lib/permissionCatalog';
import { getCloudProviderLabel } from 'src/utils/common';

// `section` is the provider id on the Integrations → Observability tab
// (SECTIONS_CONFIG in components/accounts/integration.jsx); `type` is what the
// API server stores. They differ for OTEL, whose stored type is otel_clickhouse.
export const OBSERVABILITY_INTEGRATION_PROVIDERS: ReadonlyArray<{ section: string; type: string }> = [
  { section: 'DATADOG', type: 'datadog' },
  { section: 'DYNATRACE', type: 'dynatrace' },
  { section: 'LAST9', type: 'last9' },
  { section: 'LOGGLY', type: 'loggly' },
  { section: 'LOKI', type: 'loki' },
  { section: 'SIGNOZ', type: 'signoz' },
  { section: 'OPENOBSERVE', type: 'openobserve' },
  { section: 'CUBEAPM', type: 'cubeapm' },
  { section: 'OBSERVE', type: 'observe' },
  { section: 'AZURE_APP_INSIGHTS', type: 'azure_app_insights' },
  { section: 'PROMETHEUS', type: 'prometheus' },
  { section: 'CHRONOSPHERE', type: 'chronosphere' },
  { section: 'OTEL', type: 'otel_clickhouse' },
  { section: 'JAEGER', type: 'jaeger' },
  { section: 'NEWRELIC', type: 'newrelic' },
  { section: 'SPLUNK_OBSERVABILITY_PLATFORM', type: 'splunk_observability_platform' },
  { section: 'SPLUNK_ENTERPRISE', type: 'splunk_enterprise' },
  { section: 'SOLARWINDS', type: 'solarwinds' },
  { section: 'GRAFANA_TEMPO', type: 'grafana_tempo' },
  { section: 'ES', type: 'es' },
  { section: 'PINOT', type: 'pinot' },
  { section: 'HIVE', type: 'hive' },
];

export const PROVIDER_GRANT_MODULE_PREFIX = 'integrations.';

// The write actions a provider grant covers. Keep in lockstep with
// providerGrantActions in actions_integration_authz.go.
export const PROVIDER_GRANT_ACTIONS = new Set<string>(['integrations_create_config', 'integrations_delete_config', 'integrations_update_status']);

// What a provider-only role needs besides its provider grants: the integration
// list and the reads behind the add/edit form.
export const PROVIDER_GRANT_REQUIRES = 'integrations:Read';

const PROVIDER_BY_TYPE = new Map(OBSERVABILITY_INTEGRATION_PROVIDERS.map((p) => [p.type, p]));

// The grant module for an integration type, or null when the type has no
// provider grant (webhooks, ticketing, cloud accounts, …).
export function providerGrantModule(integrationType: string | null | undefined): string | null {
  const type = String(integrationType ?? '').toLowerCase();
  return PROVIDER_BY_TYPE.has(type) ? PROVIDER_GRANT_MODULE_PREFIX + type : null;
}

export function isProviderGrantModule(module: string): boolean {
  return module.startsWith(PROVIDER_GRANT_MODULE_PREFIX) && PROVIDER_BY_TYPE.has(module.slice(PROVIDER_GRANT_MODULE_PREFIX.length));
}

// Display name for a provider grant module (`integrations.es` → "Elasticsearch"),
// or null for any other module.
export function providerGrantLabel(module: string): string | null {
  if (!isProviderGrantModule(module)) return null;
  const provider = PROVIDER_BY_TYPE.get(module.slice(PROVIDER_GRANT_MODULE_PREFIX.length))!;
  return getCloudProviderLabel(provider.section);
}

// Gateway half of the check: does the caller hold any provider grant that could
// cover this action? The API server then decides which provider.
export function actionHasProviderGrant(actionName: string, permissions: string[]): boolean {
  if (!PROVIDER_GRANT_ACTIONS.has(actionName)) return false;
  return permissions.some((p) => p.endsWith(':Write') && isProviderGrantModule(p.slice(0, -':Write'.length)));
}

// Role-editor rows for the provider grants, in tab order.
export function providerGrantCatalogEntries(actionComments: Record<string, string | undefined> = {}): CatalogEntry[] {
  const actions = [...PROVIDER_GRANT_ACTIONS].sort().map((name) => ({ name, description: actionComments[name]?.trim() || name }));
  return OBSERVABILITY_INTEGRATION_PROVIDERS.map((p) => ({
    module: PROVIDER_GRANT_MODULE_PREFIX + p.type,
    scope: 'provider' as const,
    classes: ['Write' as const],
    actions: { Write: actions },
    requires: PROVIDER_GRANT_REQUIRES,
  }));
}
