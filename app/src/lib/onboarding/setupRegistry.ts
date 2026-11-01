/**
 * The Setup Center registry — every panel, item and piece of copy the card
 * renders, as data.
 *
 * Re-ordering panels, moving an item between them, hiding one, or changing what
 * a module requires is an edit to this file. The only code a new item needs is
 * an entry in `ITEM_EVALUATORS` (`./itemEvaluators`), and then only when
 * "is it connected" cannot be answered by matching its providers against the
 * tenant's integrations — which for thirteen of the fourteen items today, it can.
 *
 * Two rules keep saved progress valid across edits:
 *
 *   1. **Ids never change.** Progress is stored against `metrics`, never
 *      "step 3", so re-ordering cannot corrupt it. To rename an item, edit its
 *      `name`; to replace one, add a new id and set the old to `hidden`.
 *   2. **Counts come from the registry, not the record.** A removed item drops
 *      out of both numbers; a new one shows up as still to do.
 *
 * Copy carries `{brand}` and `{Assistant}` rather than product names. This table
 * evaluates at import, before /api/public/app_config resolves, so a name written
 * here would latch the house brand for the life of the tab on every white-label
 * tenant. The renderer resolves it with `fillBrandTokens`; a unit test guards it.
 */
import type { OnboardingItemDef, OnboardingPanelDef, OnboardingProviderDef } from '@lib/onboardingReadiness';

/**
 * Cloud providers offered as the entry point on the home screen. Route values
 * match the `?cloudProvider=` switch in `pages/accounts/account-form.jsx`;
 * `provider` values match `cloud_provider` on an `accounts_list` row.
 *
 * Kept in step with the `cloud` section of `SECTIONS_CONFIG`
 * (`@components/accounts/integration`) — asserted by a unit test, because the
 * previous home page hard-coded Kubernetes alone and silently fell behind the
 * five other providers the product has supported for months.
 */
export const ONBOARDING_CLOUD_PROVIDERS: Array<{
  route: string;
  provider: string;
  label: string;
  blurb: string;
}> = [
  { route: 'K8S', provider: 'K8s', label: 'Kubernetes', blurb: 'EKS, AKS, GKE, OpenShift' },
  { route: 'AWS', provider: 'AWS', label: 'AWS', blurb: 'EC2, ECS, RDS, S3, EKS' },
  { route: 'GCP', provider: 'GCP', label: 'GCP', blurb: 'Compute Engine, Cloud SQL, GKE' },
  { route: 'AZURE', provider: 'Azure', label: 'Azure', blurb: 'VMs, SQL, Blob, AKS' },
  { route: 'SELFHOSTED', provider: 'SelfHosted', label: 'Self-hosted', blurb: 'Bare metal and VMs' },
];

// `cloudCapable` mirrors `CLOUD_CAPABLE_INTEGRATIONS` in
// `IntegrationDynamicFormModal.jsx` exactly (verified against it and the
// backend's own getMetricsSource/getLogSource/getTraceSource switches in
// api-server/services/observability/service.go) — keep the two in sync.
export const METRICS_PROVIDERS: OnboardingProviderDef[] = [
  { route: 'PROMETHEUS', label: 'Prometheus', types: ['prometheus'] },
  { route: 'DATADOG', label: 'Datadog', types: ['datadog'], cloudCapable: true },
  { route: 'DYNATRACE', label: 'Dynatrace', types: ['dynatrace'], cloudCapable: true },
  { route: 'NEWRELIC', label: 'New Relic', types: ['newrelic'] },
  { route: 'AZURE_APP_INSIGHTS', label: 'Azure App Insights', types: ['azure_app_insights'] },
  { route: 'CHRONOSPHERE', label: 'Chronosphere', types: ['chronosphere'], cloudCapable: true },
  { route: 'ES', label: 'Elasticsearch', types: ['es'], cloudCapable: true },
  { route: 'SOLARWINDS', label: 'SolarWinds', types: ['solarwinds'], cloudCapable: true },
  { route: 'CUBEAPM', label: 'CubeAPM', types: ['cubeapm'] },
  { route: 'SIGNOZ', label: 'SigNoz', types: ['signoz'] },
  { route: 'SPLUNK_ENTERPRISE', label: 'Splunk Enterprise', types: ['splunk_enterprise'] },
];

export const LOGS_PROVIDERS: OnboardingProviderDef[] = [
  { route: 'LOKI', label: 'Loki', types: ['loki'] },
  { route: 'ES', label: 'Elasticsearch', types: ['es'], cloudCapable: true },
  { route: 'DATADOG', label: 'Datadog', types: ['datadog'], cloudCapable: true },
  { route: 'SIGNOZ', label: 'SigNoz', types: ['signoz'] },
  { route: 'LOGGLY', label: 'Loggly', types: ['loggly'] },
  { route: 'OBSERVE', label: 'Observe', types: ['observe'], cloudCapable: true },
  { route: 'SOLARWINDS', label: 'SolarWinds', types: ['solarwinds'], cloudCapable: true },
  { route: 'AZURE_APP_INSIGHTS', label: 'Azure App Insights', types: ['azure_app_insights'] },
  { route: 'NEWRELIC', label: 'New Relic', types: ['newrelic'] },
  { route: 'DYNATRACE', label: 'Dynatrace', types: ['dynatrace'], cloudCapable: true },
  { route: 'PINOT', label: 'Pinot', types: ['pinot'] },
  { route: 'SPLUNK_ENTERPRISE', label: 'Splunk Enterprise', types: ['splunk_enterprise'] },
  { route: 'CUBEAPM', label: 'CubeAPM', types: ['cubeapm'] },
];

export const TRACES_PROVIDERS: OnboardingProviderDef[] = [
  { route: 'OTEL', label: 'OpenTelemetry', types: ['otel_clickhouse'] },
  { route: 'JAEGER', label: 'Jaeger', types: ['jaeger'], cloudCapable: true },
  { route: 'DATADOG', label: 'Datadog', types: ['datadog'], cloudCapable: true },
  { route: 'DYNATRACE', label: 'Dynatrace', types: ['dynatrace'], cloudCapable: true },
  { route: 'AZURE_APP_INSIGHTS', label: 'Azure App Insights', types: ['azure_app_insights'] },
  { route: 'CHRONOSPHERE', label: 'Chronosphere', types: ['chronosphere'], cloudCapable: true },
  { route: 'NEWRELIC', label: 'New Relic', types: ['newrelic'] },
  { route: 'SPLUNK_ENTERPRISE', label: 'Splunk Enterprise', types: ['splunk_enterprise'] },
  { route: 'CUBEAPM', label: 'CubeAPM', types: ['cubeapm'] },
  { route: 'GRAFANA_TEMPO', label: 'Grafana Tempo', types: ['grafana_tempo'] },
  { route: 'ES', label: 'Elasticsearch', types: ['es'], cloudCapable: true },
  { route: 'SOLARWINDS', label: 'SolarWinds', types: ['solarwinds'], cloudCapable: true },
];

export const CHAT_PROVIDERS: OnboardingProviderDef[] = [
  { route: 'SLACK', label: 'Slack', types: ['slack'] },
  { route: 'MSTEAMS', label: 'Microsoft Teams', types: ['ms_teams'] },
  { route: 'GOOGLE_CHAT', label: 'Google Chat', types: ['google_chat', 'google_chat_space'] },
  { route: 'DISCORD', label: 'Discord', types: ['discord'] },
];

export const TICKET_PROVIDERS: OnboardingProviderDef[] = [
  { route: 'JIRA', label: 'Jira', types: ['jira'] },
  { route: 'SERVICENOW', label: 'ServiceNow', types: ['servicenow'] },
  { route: 'FRESHDESK', label: 'Freshdesk', types: ['freshdesk'] },
  { route: 'PAGERDUTY', label: 'PagerDuty', types: ['pagerduty'] },
  { route: 'ZENDUTY', label: 'Zenduty', types: ['zenduty'] },
  { route: 'GITHUB', label: 'GitHub Issues', types: ['github'] },
  { route: 'GITLAB', label: 'GitLab Issues', types: ['gitlab'] },
];

export const ALERT_PROVIDERS: OnboardingProviderDef[] = [
  { route: 'PROMETHEUS_ALERTMANAGER_WEBHOOK', label: 'Alertmanager', types: ['prometheus_alertmanager_webhook'] },
  { route: 'DATADOG_WEBHOOK', label: 'Datadog', types: ['datadog_webhook'] },
  { route: 'GRAFANA_WEBHOOK', label: 'Grafana', types: ['grafana_webhook'] },
  { route: 'PAGERDUTY_WEBHOOK', label: 'PagerDuty', types: ['pagerduty_webhook'] },
  { route: 'AZURE_MONITOR_WEBHOOK', label: 'Azure Monitor', types: ['azure_monitor_webhook'] },
  { route: 'GCP_MONITORING_WEBHOOK', label: 'Cloud Monitoring', types: ['gcp_monitoring_webhook'] },
  { route: 'NEWRELIC_WEBHOOK', label: 'New Relic', types: ['newrelic_webhook'] },
  { route: 'DYNATRACE_WEBHOOK', label: 'Dynatrace', types: ['dynatrace_webhook'] },
  { route: 'ZENDUTY_WEBHOOK', label: 'Zenduty', types: ['zenduty_webhook'] },
  { route: 'SERVICENOW_WEBHOOK', label: 'ServiceNow', types: ['servicenow_webhook'] },
  { route: 'SOLARWINDS_WEBHOOK', label: 'SolarWinds', types: ['solarwinds_webhook'] },
  { route: 'ELASTICSEARCH_WEBHOOK', label: 'Elasticsearch', types: ['elasticsearch_webhook'] },
  { route: 'EVENT_WEBHOOK', label: 'Event webhook', types: ['event_webhook'] },
];

export const REPO_PROVIDERS: OnboardingProviderDef[] = [
  { route: 'GITHUB', label: 'GitHub', types: ['github'] },
  { route: 'GITLAB', label: 'GitLab', types: ['gitlab'] },
  { route: 'ARGOCD', label: 'Argo CD', types: ['argocd'] },
];

/** A panel as the registry stores it: items are ids, resolved against ITEMS. */
export interface RegistryPanel extends Omit<OnboardingPanelDef, 'items'> {
  items: string[];
}

export interface SetupModule {
  id: string;
  label: string;
}

/** Bump on any structural change, so a saved record can tell it is older. */
export const REGISTRY_VERSION = 3;

/** Array order is chip order. */
export const MODULES: SetupModule[] = [
  { id: 'ts', label: 'Troubleshoot' },
  { id: 'opt', label: 'Optimize' },
  { id: 'k8s', label: 'Kubernetes' },
  { id: 'auto', label: 'Automation' },
];

/** Array order is card order. */
export const REGISTRY_PANELS: RegistryPanel[] = [
  {
    id: 'accounts',
    title: 'Accounts and clusters',
    sub: 'What you want watched',
    tier: 'essential',
    tagTier: true,
    anyOf: true,
    compact: true,
    items: ['account'],
  },
  {
    id: 'telemetry',
    title: 'Telemetry',
    sub: 'The evidence every investigation reads',
    tier: 'essential',
    anyOf: true,
    items: ['metrics', 'logs', 'traces'],
  },
  {
    id: 'triggers',
    title: 'Alerts and deployments',
    sub: 'What tells us something happened, and what changed',
    tier: 'recommended',
    items: ['alerts', 'repo'],
  },
  {
    id: 'ai',
    title: 'AI',
    sub: 'The model your agents think with',
    tier: 'essential',
    anyOf: true,
    items: ['llm', 'gateway', 'mcp'],
  },
  {
    id: 'smarter',
    title: 'Make your AI agents smarter',
    sub: 'What your agents know about this environment',
    tier: 'essential',
    needs: 'ai',
    items: ['skills', 'prefs', 'rules'],
  },
  {
    id: 'delivery',
    title: 'Notifications and ticketing',
    sub: 'Where findings reach your team, and become tracked work',
    tier: 'recommended',
    items: ['chat', 'ticket'],
  },
];

/** Keyed by stable id. The key is the id; `id` is kept on the object so a
 *  resolved item still carries it without the reader having to thread the key. */
export const ITEMS: Record<string, OnboardingItemDef> = {
  account: {
    id: 'account',
    need: { ts: 'req', opt: 'req', k8s: 'req', auto: 'req' },
    name: 'Cluster or cloud account',
    required: true,
    modules: ['k8s', 'cost', 'ts', 'ops'],
    purpose: 'Used by every module to scope findings, cost and inventory to your environment.',
    why: 'Every finding, cost number and investigation is scoped to an account. Until one exists there is nothing for {brand} to watch.',
    meta: [
      ['When', 'Always, and first. Nothing else can be configured until an account exists.'],
      ['Effort', 'Kubernetes: one Helm command. Cloud: a read-only role, about 5 minutes.'],
    ],
    impact: [['Everything', 'With no account and no demo to fall back on, the product has nothing of yours to show.']],
    providers: ONBOARDING_CLOUD_PROVIDERS.map(({ route, label, blurb }) => ({ route, label, blurb })),
  },
  metrics: {
    id: 'metrics',
    need: { ts: 'req', opt: 'req', k8s: 'req', auto: 'req' },
    name: 'Metrics',
    required: true,
    modules: ['k8s', 'cost', 'ts'],
    purpose: 'Used by Cost optimisation and Troubleshooting to right-size workloads and spot spikes.',
    why: 'Utilisation and saturation are the baseline every recommendation and anomaly is measured against. Without a series to read, there is nothing to compare today against.',
    meta: [
      ['When', 'Always. Rightsizing, anomaly detection and every utilisation chart read from it.'],
      ['Effort', 'About 2 minutes. The endpoint and a read-only token.'],
    ],
    impact: [
      ['Rightsizing', 'No baseline, so no recommendation can be computed.'],
      ['Anomaly detection', 'Nothing to learn a normal range from.'],
      ['Query Metrics', 'Nothing to plot.'],
    ],
    providers: METRICS_PROVIDERS,
  },
  logs: {
    id: 'logs',
    need: { ts: 'req', opt: 'na', k8s: 'opt', auto: 'req' },
    name: 'Logs',
    required: true,
    modules: ['ts', 'k8s'],
    purpose: 'Used by Troubleshooting to pull the errors behind an alert.',
    why: 'The lines a workload actually wrote are usually where the root cause is stated outright. Without them an investigation stops at the symptom.',
    meta: [
      ['When', 'Always, unless you genuinely do not collect logs anywhere.'],
      ['Effort', 'About 2 minutes. The endpoint and a read-only token.'],
    ],
    impact: [
      ['Query Logs', 'No source to query — the tab is inert.'],
      ['Recent Errors', 'Nothing to group errors from.'],
      ['Root cause', 'Reduced to events alone.'],
    ],
    providers: LOGS_PROVIDERS,
  },
  traces: {
    id: 'traces',
    need: { ts: 'req', opt: 'na', k8s: 'opt', auto: 'req' },
    name: 'Traces',
    optional: true,
    modules: ['ts', 'k8s'],
    purpose: 'Used by Troubleshooting to follow one slow request across services.',
    why: 'A trace is the only way to attribute latency to a specific hop. Plenty of teams genuinely do not collect them, and that is fine.',
    meta: [
      ['When', 'You run distributed services and need to know which hop is slow.'],
      ['Effort', 'About 2 minutes. An endpoint and a read-only token, or re-run the agent with the collector enabled.'],
    ],
    impact: [
      ['Service Map', 'Stays empty.'],
      ['Traces tab', 'Nothing to open.'],
      ['Root cause', 'Falls back to workload-level reasoning. Everything else keeps working.'],
    ],
    providers: TRACES_PROVIDERS,
  },
  llm: {
    id: 'llm',
    need: { ts: 'req', opt: 'opt', k8s: 'opt', auto: 'req' },
    name: 'LLM provider',
    required: true,
    direct: true,
    modules: ['ts', 'ops'],
    purpose: 'Used by every agent to read your data and write the finding.',
    why: 'The agentic planners that reproduce, root-cause and propose fixes all run through an LLM. Nothing in the AI surface works until one is configured.',
    meta: [
      ['When', 'Always, if you want anything in the AI surface to answer.'],
      ['Effort', 'About 1 minute. An API key, or point us at a gateway you already run.'],
    ],
    unlocks: ['Automated root-cause analysis', 'Ask {Assistant}', 'Runbook and remediation suggestions', 'Incident summaries'],
    impact: [
      ['Investigations', 'Every automated investigation returns nothing.'],
      ['Ask {Assistant}', 'The assistant cannot answer — it has no model behind it.'],
      ['Auto-pilot', 'Cannot propose or run a remediation.'],
      ['Incident summaries', 'Stay unwritten.'],
    ],
    providers: [{ route: 'LLM', label: 'LLM provider', types: ['llm'] }],
  },
  gateway: {
    id: 'gateway',
    need: { ts: 'opt', opt: 'opt', k8s: 'opt', auto: 'opt' },
    name: 'AI gateway',
    required: true,
    alt: true,
    direct: true,
    modules: ['ts', 'ops'],
    purpose: 'Used instead of a provider when your team routes all model traffic through one endpoint.',
    why: 'A gateway satisfies model access on its own. Teams that centralise keys, quotas or audit through one endpoint connect this and skip the provider.',
    meta: [
      ['When', 'You already run a gateway such as LiteLLM, Portkey or Bedrock in front of your models.'],
      ['Effort', 'About 1 minute. The endpoint and a token.'],
    ],
    impact: [['Nothing extra', 'Connect this or a provider. Either one satisfies model access.']],
    providers: [{ route: 'LLM_GATEWAY', label: 'AI gateway', types: ['llm_gateway'] }],
  },
  mcp: {
    id: 'mcp',
    need: { ts: 'opt', opt: 'na', k8s: 'na', auto: 'opt' },
    name: 'MCP servers',
    optional: true,
    direct: true,
    modules: ['ops', 'ts'],
    purpose: 'Used by agents to call your own tools during an investigation.',
    why: 'MCP lets an agent reach a system we do not integrate with directly, using a server you control.',
    meta: [
      ['When', 'You want agents to reach an internal tool during an investigation.'],
      ['Effort', 'About 2 minutes per server. A URL and a token.'],
    ],
    impact: [['Custom tools', 'Agents work from what we integrate with, and nothing of your own.']],
    providers: [{ route: 'MCP', label: 'MCP server', types: ['mcp'] }],
  },
  skills: {
    id: 'skills',
    need: { ts: 'req', opt: 'opt', k8s: 'opt', auto: 'req' },
    name: 'Skills',
    required: true,
    // One target, no alternatives to choose between — the row's Add button
    // opens it directly, the same as the AI panel's own single-provider items.
    direct: true,
    modules: ['ts', 'cost', 'ops', 'k8s'],
    purpose: 'Used by every agent to apply your runbooks and naming instead of generic advice.',
    why: 'An LLM with no environment context answers from a plausible guess. Skills are how you tell it once what "prod" means here, who owns what, and which fix actually worked — instead of repeating it every time you ask.',
    meta: [
      ['When', 'Straight after the LLM. This is the single biggest difference between a generic answer and a useful one.'],
      ['Effort', 'A few minutes. Open AI & Tools from the nav rail, go to Knowledge → Knowledge Base, and add a source.'],
    ],
    unlocks: [
      'Investigations that use your naming, ownership and thresholds',
      'Fewer discovery calls before an agent can start',
      'Known fixes applied to repeat incidents automatically',
      'Answers grounded in this account, not a plausible generic one',
    ],
    impact: [
      ['Accuracy', 'Every investigation starts from a generic guess about what "prod" means here.'],
      ['Speed', 'Each agent re-runs list-clusters, list-namespaces and list-accounts before it can begin.'],
      ['Repeat incidents', 'A known fix for a recurring alert is re-derived from scratch every time it fires.'],
      ['Cost', 'More tool calls and more tokens per investigation, rediscovering the same facts.'],
    ],
    // Not an `integrations_list` row, so nothing here is matched against tenant
    // integrations the way a real provider is (see `skillsEvaluator` in
    // `itemEvaluators.ts`) — this exists only so the Add button has somewhere
    // to send the user. `SetupCenter`'s `openIntegration` special-cases this
    // route to open the real Knowledge Base surface instead of an integration
    // form; keep the two in sync if this route ever changes.
    providers: [{ route: 'KNOWLEDGE_BASE', label: 'Knowledge base' }],
  },
  prefs: {
    id: 'prefs',
    need: { ts: 'opt', opt: 'opt', k8s: 'opt', auto: 'opt' },
    name: 'Preferences',
    planned: true,
    // Held back from the UI: a real Preferences feature already exists
    // (the b-Cortex "Preferences" tab), but it's scoped per-user/per-tenant,
    // not per-account like every other item on this card — so "connected"
    // has no settled meaning here yet. See docs/setup-center-build-spec.md §9.
    hidden: true,
    modules: ['ops', 'ts'],
    purpose: 'Used by agents to match your units, tone and escalation habits.',
    why: 'Preferences are the small standing choices an agent should not have to ask about twice.',
    meta: [
      ['When', 'After skills. Nothing here changes what an agent can find, only how it reports it.'],
      ['Effort', 'Not built yet.'],
    ],
    impact: [['Nothing today', 'Ships after skills. Listed here so the shape of this panel is not a surprise later.']],
    providers: [],
  },
  rules: {
    id: 'rules',
    need: { ts: 'opt', opt: 'opt', k8s: 'opt', auto: 'opt' },
    name: 'Rules',
    planned: true,
    // Held with Preferences, same open question: what "connected" means for
    // a fact that isn't scoped to one account. See docs/setup-center-build-spec.md §9.
    hidden: true,
    modules: ['ops'],
    purpose: 'Used by Ops automation to decide what an agent may do on its own.',
    why: 'Rules are the guardrails: what an agent may change unattended, what needs an approval, and what it must never touch.',
    meta: [
      ['When', 'Before you let automation act unattended.'],
      ['Effort', 'Not built yet.'],
    ],
    impact: [['Nothing today', 'Ships after skills. Until then every automation asks for approval.']],
    providers: [],
  },
  chat: {
    id: 'chat',
    need: { ts: 'opt', opt: 'opt', k8s: 'opt', auto: 'opt' },
    name: 'Chat',
    required: true,
    modules: ['ops', 'ts'],
    purpose: 'Used by every module to deliver findings where your team already is.',
    why: 'On-call lives in chat. Delivering findings there — and answering back from the same thread — is where most of the day-to-day value lands.',
    meta: [
      ['When', 'Your team has a Slack, Teams, Google Chat or Discord workspace. Almost everyone does.'],
      ['Effort', 'About 1 minute. One approval in your workspace, nothing to copy or paste.'],
    ],
    unlocks: ['Alert and finding delivery', 'ChatOps bot: query state, run runbooks, ack alerts', 'Approval prompts for automation'],
    impact: [
      ['Notification Rules', 'Rules can be written but have nowhere to deliver.'],
      ['ChatOps', 'No channel to query state or run a runbook from.'],
      ['Automation approvals', 'Prompts have no one to ask.'],
    ],
    providers: CHAT_PROVIDERS,
  },
  ticket: {
    id: 'ticket',
    need: { ts: 'opt', opt: 'opt', k8s: 'na', auto: 'opt' },
    name: 'Ticketing',
    required: true,
    modules: ['ts', 'ops'],
    purpose: 'Used by Ops automation to file the fix and write the root cause back.',
    why: 'Findings only matter once they become tracked work. This is also how root-cause write-backs reach the ticket your team already opened.',
    meta: [
      ['When', 'Your team tracks work somewhere other than here, which is nearly always.'],
      ['Effort', 'About 3 minutes. A URL, a token and the project to file into.'],
    ],
    unlocks: ['One-click ticket from a finding', 'RCA write-back onto existing tickets', 'Bidirectional status sync'],
    impact: [
      ['Tickets', 'The whole section stays empty.'],
      ['Create ticket', 'The action on a finding has no target.'],
      ['RCA write-back', 'Root causes stay inside the product.'],
    ],
    providers: TICKET_PROVIDERS,
  },
  alerts: {
    id: 'alerts',
    need: { ts: 'req', opt: 'na', k8s: 'opt', auto: 'req' },
    name: 'Alerts and webhooks',
    required: true,
    modules: ['ts', 'ops'],
    purpose: 'Used by Ops automation to start an investigation the moment something fires.',
    why: 'Incidents normally arrive from the alert pipeline you already run. Forwarding them lets {brand} start investigating the moment one fires.',
    meta: [
      ['When', 'You already run Alertmanager, Datadog, PagerDuty or similar. If nothing pages your team today, skip it until something does.'],
      [
        'Effort',
        'About 3 minutes. We give you a URL to paste into your alert tool — no credentials of yours leave your side, because the traffic is inbound.',
      ],
    ],
    unlocks: ['Auto-investigation on alert', 'Alert-to-incident grouping', 'Triage rules'],
    impact: [
      ['Triage Rules', 'Nothing arrives for them to match on.'],
      ['Auto-investigation', 'Starts only when someone opens the UI, not when the alert fires.'],
      ['Incident grouping', 'Cannot correlate your alerts into one incident.'],
    ],
    providers: ALERT_PROVIDERS,
  },
  repo: {
    id: 'repo',
    need: { ts: 'req', opt: 'opt', k8s: 'opt', auto: 'req' },
    name: 'Code and deploys',
    required: true,
    modules: ['ts', 'k8s'],
    purpose: 'Used by Troubleshooting to tie a regression to the deploy that caused it.',
    why: 'Most production incidents trace back to a change. Repository and deploy history lets the analysis name the commit or rollout that caused one.',
    meta: [
      ['When', 'Your incidents tend to follow deploys. If you ship rarely, this matters less.'],
      ['Effort', 'About 2 minutes. One OAuth approval, read-only.'],
    ],
    unlocks: ['Code analysis on an incident', 'Deploy-to-incident correlation', 'Change-aware root cause'],
    impact: [
      ['Code analysis', 'Cannot open the code behind a failing service.'],
      ['Deploy correlation', 'Root cause stops at "this workload broke".'],
      ['Knowledge Graph', 'Loses the code and deploy edges.'],
    ],
    providers: REPO_PROVIDERS,
  },
};

export const SETUP_REGISTRY = {
  version: REGISTRY_VERSION,
  modules: MODULES,
  panels: REGISTRY_PANELS,
  items: ITEMS,
};
