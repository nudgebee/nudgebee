// Per-integration-type grants on the synthetic module `integrations.<type>`,
// where <type> is the stored integration type lowercased (integrations.type,
// the `integration_name` the write actions carry). One module, two meanings:
//
//   Write — MANAGE (#40997): add, edit, enable/disable and delete the
//     integrations of one observability provider (e.g. Datadog) without
//     managing every integration in the tenant. Offered for observability
//     providers only.
//   Read / Execute — USE (#41037): what a run that acts only through its
//     custom-role grants (an AI agent's run, automations) may do through an
//     integration of the type: Read uses it read-only, Execute also makes
//     changes (and covers read-only use). Checked by the run, not the gateway:
//     llm-server and runbook-server SecurityContext.MayUseIntegration. Write
//     does not imply use, and use does not imply Write.
//
// MANAGE grants cover only PROVIDER_GRANT_ACTIONS; listing and the other
// integration reads stay on the tenant-wide `integrations:Read`, which the role
// editor ticks alongside a Write box.
//
// The gateway admits PROVIDER_GRANT_ACTIONS for a holder of ANY provider grant;
// the API server decides the provider (api-server/services/api/
// actions_integration_authz.go, observabilityIntegrationTypes), against the
// stored type when the request names an existing integration by id. The Go
// list and OBSERVABILITY_INTEGRATION_PROVIDERS are pinned to each other by
// TestObservabilityIntegrationTypesMatchApp, which reads every
// `{ section: '…', type: '…' }` literal in this file — so the use-only list
// below is written `{ type, label }` and stays out of that match.
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
  { section: 'ORION', type: 'orion' },
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

// What a provider-only role needs besides its provider (Write) grants: the
// integration list and the reads behind the add/edit form.
export const PROVIDER_GRANT_REQUIRES = 'integrations:Read';

const PROVIDER_BY_TYPE = new Map(OBSERVABILITY_INTEGRATION_PROVIDERS.map((p) => [p.type, p]));

// Integration types a run can USE that have no MANAGE grant: what llm-server
// tools resolve (the ConfigType of integration-configured tools,
// TicketAllIntegrationTypes, "mcp" for every MCP server) and what
// runbook-server steps name (ticketPlatforms, integration-typed task fields).
// Observability providers are usable too (llm-server checkObservabilityUse), so
// the use rows are both lists together. integrationProviderGrants.test.ts pins
// this list to those server sources.
export const USE_ONLY_INTEGRATION_TYPES: ReadonlyArray<{ type: string; label: string }> = [
  { type: 'argocd', label: 'ArgoCD' },
  { type: 'clickhouse', label: 'ClickHouse' },
  { type: 'freshdesk', label: 'Freshdesk' },
  { type: 'github', label: 'GitHub' },
  { type: 'gitlab', label: 'GitLab' },
  { type: 'jira', label: 'Jira' },
  // One type for every MCP server: the run checks "mcp", not the server.
  { type: 'mcp', label: 'MCP servers' },
  { type: 'mongodb_proxy', label: 'MongoDB' },
  { type: 'mssql', label: 'SQL Server' },
  { type: 'mysql', label: 'MySQL' },
  { type: 'oracle', label: 'Oracle' },
  { type: 'pagerduty', label: 'PagerDuty' },
  { type: 'postgresql', label: 'PostgreSQL' },
  { type: 'rabbitmq', label: 'RabbitMQ' },
  { type: 'redis', label: 'Redis' },
  { type: 'servicenow', label: 'ServiceNow' },
  { type: 'ssh', label: 'SSH' },
  { type: 'zenduty', label: 'ZenDuty' },
];

// Every type a role can grant use of → its display name.
const LABEL_BY_TYPE = new Map<string, string>([
  ...OBSERVABILITY_INTEGRATION_PROVIDERS.map((p): [string, string] => [p.type, getCloudProviderLabel(p.section)]),
  ...USE_ONLY_INTEGRATION_TYPES.map((t): [string, string] => [t.type, t.label]),
]);

// The MANAGE grant module for an integration type, or null when the type has
// no provider grant (webhooks, ticketing, cloud accounts, …).
export function providerGrantModule(integrationType: string | null | undefined): string | null {
  const type = String(integrationType ?? '').toLowerCase();
  return PROVIDER_BY_TYPE.has(type) ? PROVIDER_GRANT_MODULE_PREFIX + type : null;
}

// Whether `module` is an observability provider's grant module — the only
// modules whose Write the gateway honours.
export function isProviderGrantModule(module: string): boolean {
  return module.startsWith(PROVIDER_GRANT_MODULE_PREFIX) && PROVIDER_BY_TYPE.has(module.slice(PROVIDER_GRANT_MODULE_PREFIX.length));
}

// Display name for an integration grant module (`integrations.es` →
// "Elasticsearch", `integrations.mcp` → "MCP servers"), or null for any other
// module.
export function integrationGrantLabel(module: string): string | null {
  if (!module.startsWith(PROVIDER_GRANT_MODULE_PREFIX)) return null;
  return LABEL_BY_TYPE.get(module.slice(PROVIDER_GRANT_MODULE_PREFIX.length)) ?? null;
}

// Gateway half of the check: does the caller hold any provider grant that could
// cover this action? The API server then decides which provider. Use grants
// (Read / Execute) admit nothing here.
export function actionHasProviderGrant(actionName: string, permissions: string[]): boolean {
  if (!PROVIDER_GRANT_ACTIONS.has(actionName)) return false;
  return permissions.some((p) => p.endsWith(':Write') && isProviderGrantModule(p.slice(0, -':Write'.length)));
}

// The USE boxes' tooltip. No RPC action is classified to them, so there is no
// action list to show; this says what the box does instead.
function useNotes(label: string): CatalogEntry['notes'] {
  return {
    Read: `An AI agent or automation running as this role may read through ${label} integrations. Checked when the run uses one.`,
    Execute: `An AI agent or automation running as this role may make changes through ${label} integrations (includes Read data). Checked when the run uses one.`,
  };
}

// Role-editor rows for the integration grants, one per type, sorted by name:
// Read and Execute (use) for every type, Write (manage) for observability
// providers only. Manage does not imply use, so the rows' classes are not
// coupled; only Write brings integrations:Read along.
export function providerGrantCatalogEntries(actionComments: Record<string, string | undefined> = {}): CatalogEntry[] {
  const manageActions = [...PROVIDER_GRANT_ACTIONS].sort().map((name) => ({ name, description: actionComments[name]?.trim() || name }));
  return [...LABEL_BY_TYPE.entries()]
    .sort(([, a], [, b]) => a.localeCompare(b))
    .map(([type, label]): CatalogEntry => {
      const manageable = PROVIDER_BY_TYPE.has(type);
      return {
        module: PROVIDER_GRANT_MODULE_PREFIX + type,
        scope: 'provider',
        classes: manageable ? ['Read', 'Write', 'Execute'] : ['Read', 'Execute'],
        actions: manageable ? { Write: manageActions } : {},
        notes: useNotes(label),
        independentClasses: true,
        ...(manageable ? { requires: { Write: PROVIDER_GRANT_REQUIRES } } : {}),
      };
    });
}
