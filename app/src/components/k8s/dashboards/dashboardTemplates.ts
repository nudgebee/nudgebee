import { convertNativeDashboard, panelSourceKey, type ScopeMapping } from './nativeImport';
import { findPanelTemplate, type PanelTemplate, type TemplateRole } from './panelTemplates';
import { panelScope, type PanelScope } from './panelAccounts';

/**
 * Ready-made dashboards, composed from the widget library.
 *
 * A template is a starting point, not a subscription: creating one copies its
 * panels into a dashboard the tenant owns, and nothing links back afterwards.
 * That is why they are authored here rather than seeded as `is_builtin` rows —
 * a built-in cannot be edited, and the first thing anyone does with a template
 * is change it.
 *
 * Panels are named by widget id rather than re-declared, so a query lives in
 * exactly one place. `templates.test.ts` fails the build if an id here has no
 * widget, and if a widget's scope or query stops matching what it claims.
 */

export interface DashboardTemplate {
  id: string;
  title: string;
  /** One or two sentences: who this is for and what decision it supports. */
  description: string;
  roles: TemplateRole[];
  /** Widget ids in display order; `width` overrides the widget's own. */
  panels: { widget: string; width?: number }[];
}

export const DASHBOARD_TEMPLATES: DashboardTemplate[] = [
  /*
   * The role dashboards.
   *
   * One per persona, in the order the org chart reads: what a CTO, a CFO, the
   * two directors and the three ICs each land on. The rule they follow is
   * altitude — a summary dashboard groups (by account, by cluster, by category)
   * and an IC dashboard lists rows, so the same table can serve both without
   * either being the wrong shape.
   *
   * They deliberately overlap: an incident on the CTO's triage summary is the
   * same incident on the SRE's queue, one counted and one listed. That is the
   * point — the conversation between the two is easier when they are looking at
   * the same data at different depths.
   *
   * What a panel CANNOT do is act. Approving a remediation, applying a fix or
   * acknowledging an alert stays on the feature page; the approval and
   * recommendation widgets here show the queue and its age, which is the half a
   * dashboard is good at.
   */
  {
    id: 'cto-overview',
    title: 'CTO / VP Engineering',
    description:
      'The estate on one page: what is anomalous, what is on fire, what it costs, and how much of the response was automated. Grouped by account and cluster, because at this altitude the row is a team, not a pod.',
    roles: ['cto'],
    panels: [
      { widget: 'anomalies-by-type' },
      { widget: 'alert-triage-summary' },
      { widget: 'daily-spend-trend' },
      { widget: 'cost-by-service' },
      { widget: 'cluster-health-overview' },
      { widget: 'automation-task-health' },
      { widget: 'ai-investigation-activity' },
    ],
  },
  {
    id: 'cfo-cloud-cost',
    title: 'CFO / VP Finance',
    description:
      'The bill, and the part of it that is waste. Billed spend by service, region and resource next to the open savings opportunities, so the budget conversation and the fix list are the same page.',
    roles: ['cfo'],
    panels: [
      { widget: 'daily-spend-trend' },
      { widget: 'cost-by-service' },
      { widget: 'cost-by-region' },
      { widget: 'cost-by-resource' },
      { widget: 'savings-by-account' },
      { widget: 'savings-by-rule' },
      { widget: 'automation-task-health' },
    ],
  },
  {
    id: 'director-sre',
    title: 'Director / VP — SRE',
    description:
      'The reliability review pack: what fired and where, which cluster is the noisy one, who is carrying the tickets, and whether the AI and the automation are actually closing things.',
    roles: ['manager', 'sre'],
    panels: [
      { widget: 'alert-triage-summary' },
      { widget: 'issues-by-cluster' },
      { widget: 'ticket-load-by-assignee' },
      { widget: 'automation-task-health' },
      { widget: 'ai-investigation-activity' },
      { widget: 'recent-ai-investigations' },
      { widget: 'pending-approvals' },
    ],
  },
  {
    id: 'director-cloud-ops',
    title: 'Director — Cloud Ops',
    description:
      'Spend, waste and posture for the accounts you own — narrower than the CFO view and closer to the resources. Ends with the two things that quietly break the rest: compliance drift and an agent that stopped reporting.',
    roles: ['manager', 'cloudops'],
    panels: [
      { widget: 'cost-by-service' },
      { widget: 'cost-by-region' },
      { widget: 'savings-by-rule' },
      { widget: 'rightsizing-recommendations' },
      { widget: 'cis-compliance-findings' },
      { widget: 'automation-task-health' },
      { widget: 'agent-health' },
    ],
  },
  {
    id: 'sre-on-call',
    title: 'SRE — On-call',
    description:
      'The working list for a shift: what is open, what the AI has already looked at, what is waiting on your approval, and what started misbehaving in the last window.',
    roles: ['sre'],
    panels: [
      { widget: 'active-incident-queue' },
      { widget: 'recent-ai-investigations' },
      { widget: 'pending-approvals' },
      { widget: 'noisiest-issues' },
      { widget: 'recent-anomalies' },
      { widget: 'issues-by-namespace' },
      { widget: 'pod-restarts-by-namespace' },
    ],
  },
  {
    id: 'devops-platform',
    title: 'DevOps / Platform Engineer',
    description:
      'The cluster as an operator sees it: fleet summary, the nodes underneath, what is failing compliance or carrying CVEs, and what is proposed for right-sizing — with the open incidents in your namespaces at the end.',
    roles: ['devops'],
    panels: [
      { widget: 'cluster-health-overview' },
      { widget: 'node-inventory' },
      { widget: 'cis-compliance-findings' },
      { widget: 'image-vulnerabilities' },
      { widget: 'rightsizing-recommendations' },
      { widget: 'active-incident-queue' },
      { widget: 'ticket-volume-by-status' },
    ],
  },
  {
    id: 'cloud-ops-engineer',
    title: 'Cloud Ops Engineer',
    description:
      'The detail behind the cost dashboards: which resources cost the most, which are worth resizing, what looks anomalous, and what is queued for approval — the work list, not the summary.',
    roles: ['cloudops'],
    panels: [
      { widget: 'cost-by-resource' },
      { widget: 'savings-by-rule' },
      { widget: 'rightsizing-recommendations' },
      { widget: 'top-savings-resources' },
      { widget: 'recent-anomalies' },
      { widget: 'pending-approvals' },
      { widget: 'agent-health' },
    ],
  },
  {
    id: 'executive-overview',
    title: 'Executive Overview',
    description:
      'What the platform costs, what it is wasting, and what is breaking — on one page, with no PromQL. The dashboard to open before a budget or headcount conversation.',
    roles: ['cto', 'manager'],
    panels: [
      { widget: 'savings-by-account' },
      { widget: 'savings-by-rule' },
      { widget: 'top-savings-resources' },
      { widget: 'noisiest-issues' },
      { widget: 'cluster-cpu-utilisation' },
      { widget: 'cluster-memory-utilisation' },
    ],
  },
  {
    id: 'finops-cost-optimisation',
    title: 'FinOps — Cost & Waste',
    description:
      'Every open savings opportunity, cut by account, rule and cloud service, next to the reservation gap that creates most of it. Built to be worked top-down until it is empty.',
    roles: ['cto', 'manager', 'devops'],
    panels: [
      { widget: 'savings-by-rule' },
      { widget: 'savings-by-service' },
      { widget: 'top-savings-resources' },
      { widget: 'cpu-requests-vs-allocatable' },
      { widget: 'memory-requests-vs-allocatable' },
      { widget: 'cluster-cpu-utilisation' },
      { widget: 'cluster-memory-utilisation' },
      { widget: 'critical-recommendations' },
    ],
  },
  {
    id: 'reliability-review',
    title: 'Reliability Review',
    description:
      'The weekly or monthly service-review pack: what fired, whose namespace it was in, what is new, and whether error rate and rollout health moved with it.',
    roles: ['manager', 'sre'],
    panels: [
      { widget: 'noisiest-issues' },
      { widget: 'issues-by-namespace' },
      { widget: 'pod-restarts-by-namespace' },
      { widget: 'new-issues' },
      { widget: 'error-rate-percent' },
      { widget: 'p99-latency-by-service' },
      { widget: 'deployments-unavailable' },
    ],
  },
  {
    id: 'sre-golden-signals',
    title: 'SRE — Golden Signals',
    description:
      'Traffic, errors and latency for every service, then the traces underneath them. Start here when something is slow and you do not yet know what.',
    roles: ['sre', 'developer'],
    panels: [
      { widget: 'request-rate-by-service' },
      { widget: 'error-rate-percent' },
      { widget: 'p99-latency-by-service' },
      { widget: 'p50-latency-by-service' },
      { widget: 'top-endpoints-by-traffic' },
      { widget: 'error-log-volume' },
      { widget: 'slowest-service-calls' },
      { widget: 'failing-service-calls' },
    ],
  },
  {
    id: 'sre-cluster-capacity',
    title: 'SRE — Cluster Capacity',
    description:
      'Saturation across compute, disk and volumes, with reserved-versus-used beside actually-used. The page that answers "can we take another service" and "why is this so expensive".',
    roles: ['sre', 'devops'],
    panels: [
      { widget: 'ready-nodes' },
      { widget: 'unschedulable-nodes' },
      { widget: 'pending-pods', width: 6 },
      { widget: 'cluster-cpu-utilisation' },
      { widget: 'cluster-memory-utilisation' },
      { widget: 'cpu-requests-vs-allocatable' },
      { widget: 'memory-requests-vs-allocatable' },
      { widget: 'node-cpu-utilisation' },
      { widget: 'pods-per-node' },
      { widget: 'node-filesystem-utilisation' },
      { widget: 'pvc-utilisation' },
    ],
  },
  {
    id: 'devops-workload-health',
    title: 'DevOps — Workload Health',
    description:
      'Everything that stops a workload from running: crash loops, image pulls, OOM kills, stuck rollouts, failed jobs and unbound volumes. The first page to open after a deploy.',
    roles: ['devops', 'sre'],
    panels: [
      { widget: 'pending-pods' },
      { widget: 'failed-pods' },
      { widget: 'container-waiting-reasons' },
      { widget: 'oomkilled-containers' },
      { widget: 'pod-restarts-by-namespace' },
      { widget: 'deployments-unavailable' },
      { widget: 'daemonsets-unavailable' },
      { widget: 'failed-jobs' },
      { widget: 'pvc-not-bound' },
      { widget: 'new-issues' },
    ],
  },
  {
    id: 'developer-service-deep-dive',
    title: 'Developer — My Service',
    description:
      'One service, end to end: its pods’ CPU and memory, its restarts, its traffic and latency, and the calls it makes that are slow. Set the namespace and workload when you create it.',
    roles: ['developer', 'sre'],
    panels: [
      { widget: 'workload-cpu-usage' },
      { widget: 'workload-memory-usage' },
      { widget: 'workload-restarts' },
      { widget: 'workload-replicas-available' },
      { widget: 'request-rate-by-service' },
      { widget: 'error-rate-percent' },
      { widget: 'p99-latency-by-service' },
      { widget: 'p50-latency-by-service' },
      { widget: 'slowest-service-calls' },
      { widget: 'new-issues' },
    ],
  },
];

export function findDashboardTemplate(id: string): DashboardTemplate | undefined {
  return DASHBOARD_TEMPLATES.find((t) => t.id === id);
}

/**
 * Templates whose name or description contains `query`, matched case-insensitively
 * as a substring. The gallery is small and static, so a predictable "contains" is
 * what a user typing a keyword like "cost" or "capacity" expects — no fuzzy
 * matching, no minimum length. A blank or whitespace-only query returns the list
 * unchanged.
 */
export function filterTemplatesBySearch(templates: DashboardTemplate[], query: string): DashboardTemplate[] {
  const q = query.trim().toLowerCase();
  if (!q) return templates;
  return templates.filter((t) => t.title.toLowerCase().includes(q) || t.description.toLowerCase().includes(q));
}

/** The widgets a template is made of, in order. Unknown ids are dropped. */
export function templateWidgets(template: DashboardTemplate): PanelTemplate[] {
  return template.panels.map((p) => findPanelTemplate(p.widget)).filter((w): w is PanelTemplate => Boolean(w));
}

/**
 * The template rendered as a file in this app's own export format, which the
 * native importer then validates and scopes to the chosen accounts.
 *
 * Going through that converter rather than writing panels directly means a
 * template is held to exactly the same rules as a pasted export — a widget that
 * would fail server validation is dropped with a warning the gallery can show,
 * instead of being rejected as a whole dashboard on save.
 *
 * Queries are copied verbatim. Templates carry no variables: a `$name` token is
 * only substituted when a dashboard is opened from a page that supplies one, so
 * a template that shipped them would run the literal text everywhere else. A
 * query meant to be narrowed writes `=~".*"` and is edited in the panel editor.
 */
export function buildTemplateDocument(template: DashboardTemplate, title?: string, scopes: PanelScope[] = []) {
  // Unresolvable ids are dropped FIRST, so `index` below counts resolved panels
  // — the same thing `templateWidgets` counts, and therefore the same thing the
  // caller indexed its `scopes` by. Reading `scopes[index]` off the raw
  // `template.panels` instead would shift every scope after a missing widget
  // onto the wrong panel, and the last one onto nothing at all.
  const resolved = template.panels
    .map((entry) => ({ entry, widget: findPanelTemplate(entry.widget) }))
    .filter((r): r is { entry: DashboardTemplate['panels'][number]; widget: PanelTemplate } => Boolean(r.widget));

  const panels = resolved.map(({ entry, widget }, index) => {
    return {
      ...widget.panel,
      id: index + 1,
      // Scope is PER PANEL, not per dashboard: a savings table reads the query
      // engine across every account while the chart beside it reads one
      // cluster's Prometheus, and forcing both onto one account type would
      // break whichever of the two lost. The panel model has always allowed
      // this — `account_type` / `account_ids` live on the Panel — so a
      // dashboard mixing providers needs no new concept.
      ...(scopes[index] || {}),
      // `query` is deep-copied for the same reason `panelFromTemplate` does
      // it: the spread is shallow, so without this the object hanging off a
      // module-level constant escapes into the converted document and on into
      // whatever the caller does with it. Nothing mutates it on today's path —
      // the created dashboard is re-read from the save response — but the
      // library would be corrupted for the rest of the session by the first
      // caller that did, and that is not a failure anyone would trace back
      // here.
      targets: (widget.panel.targets || []).map((t) => ({
        ...t,
        ...(t.query ? { query: JSON.parse(JSON.stringify(t.query)) } : {}),
      })),
      grid_pos: { ...widget.panel.grid_pos, ...(entry.width ? { w: entry.width } : {}) },
    };
  });

  return {
    title: (title || '').trim() || template.title,
    description: template.description,
    definition: { panels },
  };
}

/**
 * A template and its per-panel scopes, converted into the panels a save takes.
 *
 * The conversion goes through the native importer so a template is held to the
 * same rules as a pasted export — a panel that would fail server validation is
 * dropped with a warning the caller can show, rather than rejecting the whole
 * dashboard on save.
 *
 * That importer applies ONE default scope to every panel, which is right for a
 * Grafana file and the opposite of what a template needs now that scope is per
 * panel. Its mapping table is the supported way to say "this panel keeps the
 * scope it arrived with" — the same path a same-tenant re-import takes — so
 * panels naming different providers survive conversion intact. The default is
 * therefore unreachable, and deliberately empty: a panel with no scope at all
 * should be caught by the caller, not silently given someone else's account.
 */
export function convertTemplate(template: DashboardTemplate, title: string, scopes: PanelScope[]) {
  const document = buildTemplateDocument(template, title, scopes);
  const mappings: ScopeMapping = {};
  for (const panel of document.definition.panels as any[]) {
    const key = panelSourceKey(panel);
    if (key) mappings[key] = panelScope(panel.account_type || '', panel.account_ids || []);
  }
  return convertNativeDashboard(document, { account_type: undefined, account_ids: [] }, mappings);
}
