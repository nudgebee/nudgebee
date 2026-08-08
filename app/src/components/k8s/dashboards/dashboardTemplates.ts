import { findPanelTemplate, type PanelTemplate, type TemplateRole } from './panelTemplates';
import { referencedVariables, renderTemplate, type VariableValues } from './templating';

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
 * widget, or if a query references a variable the template does not declare.
 */

export interface TemplateVariable {
  /** Token used in the panels' PromQL, without the `$`. */
  name: string;
  label: string;
  /**
   * Prefilled value. Every variable is used as a regex matcher, so the default
   * is `.*` — the dashboard shows everything until it is narrowed, rather than
   * opening on an empty chart and looking broken.
   */
  defaultValue: string;
  help: string;
}

export interface DashboardTemplate {
  id: string;
  title: string;
  /** One or two sentences: who this is for and what decision it supports. */
  description: string;
  roles: TemplateRole[];
  variables?: TemplateVariable[];
  /** Widget ids in display order; `width` overrides the widget's own. */
  panels: { widget: string; width?: number }[];
}

const NAMESPACE_VARIABLE: TemplateVariable = {
  name: 'namespace',
  label: 'Namespace',
  defaultValue: '.*',
  help: 'Regular expression. Leave as .* for every namespace, or narrow to one — payments, or checkout|cart.',
};

const WORKLOAD_VARIABLE: TemplateVariable = {
  name: 'workload',
  label: 'Workload',
  defaultValue: '.*',
  help: 'Regular expression matching the deployment name. Leave as .* to chart everything in the namespace.',
};

export const DASHBOARD_TEMPLATES: DashboardTemplate[] = [
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
    variables: [NAMESPACE_VARIABLE],
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
    variables: [NAMESPACE_VARIABLE],
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
    variables: [NAMESPACE_VARIABLE, WORKLOAD_VARIABLE],
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

/** The widgets a template is made of, in order. Unknown ids are dropped. */
export function templateWidgets(template: DashboardTemplate): PanelTemplate[] {
  return template.panels.map((p) => findPanelTemplate(p.widget)).filter((w): w is PanelTemplate => Boolean(w));
}

/** Default values for a template's variables, keyed by name. */
export function templateVariableDefaults(template: DashboardTemplate): VariableValues {
  return Object.fromEntries((template.variables || []).map((v) => [v.name, v.defaultValue]));
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
 * Variables are substituted into query expressions only. An entity or trace
 * filter is a literal the backend compares against a typed column, so a
 * `.*` default would filter it down to nothing rather than open it up.
 */
export function buildTemplateDocument(template: DashboardTemplate, values: VariableValues, title?: string) {
  const panels = template.panels
    .map((entry, index) => {
      const widget = findPanelTemplate(entry.widget);
      if (!widget) return null;
      return {
        ...widget.panel,
        id: index + 1,
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
          ...(t.expr ? { expr: renderTemplate(t.expr, values) } : {}),
          ...(t.query ? { query: JSON.parse(JSON.stringify(t.query)) } : {}),
        })),
        grid_pos: { ...widget.panel.grid_pos, ...(entry.width ? { w: entry.width } : {}) },
      };
    })
    .filter(Boolean);

  return {
    title: (title || '').trim() || template.title,
    description: template.description,
    definition: { panels },
  };
}

/** Variable names a template's panels actually reference. Used by its test. */
export function variablesUsedBy(template: DashboardTemplate): string[] {
  const found = new Set<string>();
  for (const widget of templateWidgets(template)) {
    for (const target of widget.panel.targets || []) {
      for (const name of referencedVariables(target.expr || '')) found.add(name);
    }
  }
  return [...found];
}
