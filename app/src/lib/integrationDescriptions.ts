/**
 * One-line "what is this connection for" copy for every integration.
 *
 * Three surfaces render the same sentence so they cannot drift:
 *  1. the integrations catalog tiles (`components/accounts/integration.jsx`),
 *  2. each integration's own listing page header (`ListIntegrations` and the
 *     hand-written `*IntegrationTile` / `CloudAccountTile` pages),
 *  3. the Add/Edit modal for that integration (DS `Modal`'s `subtitle` slot).
 *
 * Copy rules for anything added here:
 *  - One sentence, sentence case, ending in a period. Keep it under ~80
 *    characters — the catalog tile renders it on a single truncated line.
 *  - Say what connecting it buys the operator, not what the vendor is. "Query
 *    Datadog logs, metrics and traces", not "Datadog is an observability suite".
 *  - Brand-free. This is a module-level table, so it evaluates at import —
 *    before `/api/public/app_config` resolves — and a product name written here
 *    would latch the house brand for the life of the tab (see app/CLAUDE.md
 *    § "Brand copy"). If a future entry genuinely needs the name, put `{brand}`
 *    in the string and run it through `fillBrandTokens` inside
 *    `getIntegrationDescription` — not in the table.
 */

// Keyed by the uppercased provider name. The catalog uses uppercase provider
// ids ('PAGERDUTY_WEBHOOK'), the listing pages use the backend integration name
// ('pagerduty_webhook'), and a handful of names differ between the two — those
// are reconciled by INTEGRATION_KEY_ALIASES below.
export const INTEGRATION_DESCRIPTIONS: Record<string, string> = {
  // ── Kubernetes & Cloud ──────────────────────────────────────────
  K8S: 'Onboard an EKS, AKS, GKE or OpenShift cluster and monitor its workloads.',
  AWS: 'Onboard an AWS account for inventory, cost and CloudWatch telemetry.',
  AZURE: 'Onboard an Azure subscription for inventory, cost and monitor data.',
  GCP: 'Onboard a GCP project for inventory, billing and Cloud Monitoring data.',
  CLOUDFOUNDRY: 'Onboard a Cloud Foundry foundation to monitor its orgs, spaces and apps.',
  SELFHOSTED: 'Onboard standalone VMs or bare-metal hosts that sit outside a cloud account.',

  // ── Messaging & Alerting ────────────────────────────────────────
  SLACK: 'Deliver alerts, notifications and assistant replies to Slack channels.',
  MSTEAMS: 'Deliver alerts and notifications to Microsoft Teams channels.',
  GOOGLE_CHAT: 'Deliver alerts and notifications to Google Chat spaces.',
  DISCORD: 'Deliver alerts and notifications to Discord channels.',

  // ── Ticketing / Repos / CI-CD ───────────────────────────────────
  JIRA: 'Create and sync Jira issues from events and recommendations.',
  SERVICENOW: 'Create and sync ServiceNow incidents from events and recommendations.',
  PAGERDUTY: 'Raise and track PagerDuty incidents for events and recommendations.',
  ZENDUTY: 'Raise and track Zenduty incidents for events and recommendations.',
  FRESHDESK: 'Create and sync Freshdesk tickets from events and recommendations.',
  GITHUB: 'Create issues, read repositories and raise pull requests on GitHub.',
  GITLAB: 'Create issues, read repositories and raise merge requests on GitLab.',
  BITBUCKET: 'Read Bitbucket repositories for code-aware investigation.',
  ARGOCD: 'Track Argo CD applications, sync state and rollout health.',

  // ── Webhooks (inbound alert ingestion) ──────────────────────────
  PAGERDUTY_WEBHOOK: 'Receive PagerDuty incidents as events through an inbound webhook.',
  ZENDUTY_WEBHOOK: 'Receive Zenduty incidents as events through an inbound webhook.',
  PROMETHEUS_ALERTMANAGER_WEBHOOK: 'Receive Prometheus Alertmanager alerts as events through a webhook.',
  DATADOG_WEBHOOK: 'Receive Datadog monitor alerts as events through an inbound webhook.',
  AZURE_MONITOR_WEBHOOK: 'Receive Azure Monitor alerts as events through an inbound webhook.',
  SERVICENOW_WEBHOOK: 'Receive ServiceNow incidents as events through an inbound webhook.',
  NEWRELIC_WEBHOOK: 'Receive New Relic alerts as events through an inbound webhook.',
  GRAFANA_WEBHOOK: 'Receive Grafana alerts as events through an inbound webhook.',
  SPLUNK_WEBHOOK: 'Receive Splunk alerts as events through an inbound webhook.',
  GCP_MONITORING_WEBHOOK: 'Receive Google Cloud Monitoring alerts as events through a webhook.',
  DYNATRACE_WEBHOOK: 'Receive Dynatrace problem notifications as events through a webhook.',
  SOLARWINDS_WEBHOOK: 'Receive SolarWinds Observability alerts as events through a webhook.',
  CUBEAPM_WEBHOOK: 'Receive CubeAPM alerts as events through an inbound webhook.',
  ELASTICSEARCH_WEBHOOK: 'Receive Elasticsearch Watcher alerts as events through a webhook.',
  OPENOBSERVE_WEBHOOK: 'Receive OpenObserve alerts as events through an inbound webhook.',
  SIGNOZ_WEBHOOK: 'Receive SigNoz alerts as events through a webhook notification channel.',
  WORKFLOW_WEBHOOK: 'Trigger an automation from any tool through an inbound webhook.',
  EVENT_WEBHOOK: 'Send alerts from any tool that can post JSON, in a documented format, straight into Nudgebee.',

  // ── Databases, queues, caches ───────────────────────────────────
  POSTGRES: 'Run diagnostic SQL against a PostgreSQL database during investigations.',
  MYSQL: 'Run diagnostic SQL against a MySQL database during investigations.',
  CLICKHOUSE: 'Run diagnostic SQL against a ClickHouse cluster during investigations.',
  MSSQL: 'Run diagnostic SQL against a SQL Server database during investigations.',
  ORACLE: 'Run diagnostic SQL against an Oracle database during investigations.',
  REDIS: 'Inspect Redis keyspace, memory and client stats during investigations.',
  RABBITMQ: 'Inspect RabbitMQ queues, consumers and message rates.',
  MONGODB_PROXY: 'Reach a MongoDB deployment through the proxy agent for diagnostics.',
  KAFKA_PROXY: 'Reach Kafka brokers through the proxy agent for topic diagnostics.',

  // ── Docs ────────────────────────────────────────────────────────
  CONFLUENCE: 'Index Confluence pages as knowledge for investigations and answers.',

  // ── Observability ───────────────────────────────────────────────
  DATADOG: 'Query Datadog logs, metrics and traces during investigations.',
  DYNATRACE: 'Query Dynatrace logs, metrics and traces during investigations.',
  NEWRELIC: 'Query New Relic logs, metrics and traces during investigations.',
  SPLUNK_OBSERVABILITY_PLATFORM: 'Query Splunk Observability Cloud metrics and traces.',
  SPLUNK_ENTERPRISE: 'Query Splunk Enterprise searches during investigations.',
  CUBEAPM: 'Query CubeAPM logs, metrics and traces during investigations.',
  SOLARWINDS: 'Query SolarWinds Observability logs, metrics and traces.',
  CHRONOSPHERE: 'Query Chronosphere metrics and traces during investigations.',
  SIGNOZ: 'Query SigNoz logs and metrics during investigations.',
  OPENOBSERVE: 'Query OpenObserve logs, metrics and traces during investigations.',
  OBSERVE: 'Query Observe datasets for logs and metrics during investigations.',
  LOKI: 'Query Grafana Loki log streams during investigations.',
  PROMETHEUS: 'Query Prometheus metrics for charts, alerts and investigations.',
  ES: 'Query Elasticsearch indices for logs and traces during investigations.',
  LOGGLY: 'Query Loggly log searches during investigations.',
  LAST9: 'Query Last9 metrics during investigations.',
  AZURE_APP_INSIGHTS: 'Query Azure Application Insights logs and traces.',
  OTEL: 'Collect OpenTelemetry logs, metrics and traces through the agent.',
  JAEGER: 'Query Jaeger for distributed traces during investigations.',
  PINOT: 'Query an Apache Pinot table as a log source.',
  HIVE: 'Query an Apache Hive table as a log source.',
  GRAFANA_TEMPO: 'Query Grafana Tempo for distributed traces.',

  // ── LLM ─────────────────────────────────────────────────────────
  LLM: 'Choose the LLM provider and models used for AI analysis.',
  LLM_GATEWAY: 'Register the provider credentials the AI Gateway routes requests through.',
  MCP: 'Connect an MCP server to give agents extra tools.',

  // ── Servers & proxies ───────────────────────────────────────────
  SSH: 'Run diagnostic commands on a host over SSH.',
  VM_AGENT: 'Install a proxy agent to reach systems that are not directly reachable.',
  HTTP_PROXY: 'Reach an internal HTTP endpoint through the proxy agent.',
};

// Names that differ between the catalog tiles, the listing pages and the
// backend integration name, folded onto the canonical key above.
const INTEGRATION_KEY_ALIASES: Record<string, string> = {
  POSTGRESQL: 'POSTGRES',
  ELASTICSEARCH: 'ES',
  MS_TEAMS: 'MSTEAMS',
  GOOGLECHAT: 'GOOGLE_CHAT',
  OTEL_CLICKHOUSE: 'OTEL',
  MCP_PROXY: 'MCP',
  SPLUNK: 'SPLUNK_OBSERVABILITY_PLATFORM',
};

/**
 * The one-line description for an integration, or '' when there is none.
 *
 * Accepts either casing and either naming ('pagerduty_webhook', 'ES',
 * 'PAGERDUTY_WEBHOOK'). Returns '' rather than a placeholder so every caller
 * can render the description conditionally and lay out unchanged without one.
 */
export const getIntegrationDescription = (name?: string | null): string => {
  if (!name) {
    return '';
  }
  const key = String(name).toUpperCase();
  return INTEGRATION_DESCRIPTIONS[key] || INTEGRATION_DESCRIPTIONS[INTEGRATION_KEY_ALIASES[key]] || '';
};
