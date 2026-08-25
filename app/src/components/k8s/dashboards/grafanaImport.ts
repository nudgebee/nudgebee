import type { DashboardDefinition, Panel, PanelTarget, PanelType } from '@api1/dashboards';
import type { PanelScope } from './panelAccounts';
import { referencedVariables } from './templating';

/**
 * Converts an exported Grafana dashboard into our panel document.
 *
 * The two models are close by design — `grid_pos`, `targets[].expr` and
 * `legend_format` all mirror Grafana — so this is a translation, not an
 * interpretation. What it does NOT do is translate the query: an imported
 * `expr` is PromQL and runs through the account's own metrics provider, which
 * only Prometheus and Chronosphere speak. Importing onto a Datadog account
 * produces panels that save and then fail at render.
 *
 * Everything that cannot be carried over is reported in `warnings` rather than
 * silently dropped — an import that quietly loses four of twelve panels is
 * worse than one that says so.
 */

/** Grafana's grid is 24 columns wide; ours is 12. */
const GRAFANA_GRID_COLUMNS = 24;
const OUR_GRID_COLUMNS = 12;

/**
 * Grafana panel type → ours. `graph` is the pre-8 line chart, `singlestat` the
 * pre-7 stat, `table-old` the pre-7 table; the corpus in
 * `components/dashboards/apps/` spans schema 16 → 39, so all three still turn up.
 */
const PANEL_TYPE_MAP: Record<string, PanelType> = {
  graph: 'timeseries',
  timeseries: 'timeseries',
  'graph-old': 'timeseries',
  stat: 'stat',
  singlestat: 'stat',
  gauge: 'stat',
  bargauge: 'stat',
  table: 'table',
  'table-old': 'table',
  barchart: 'bar',
  text: 'text',
};

/**
 * Datasource types whose query language is not PromQL. A panel on one of these
 * would import as a broken PromQL panel, so it is skipped instead.
 */
const NON_PROMETHEUS_DATASOURCES = new Set([
  'loki',
  'elasticsearch',
  'influxdb',
  'postgres',
  'mysql',
  'mssql',
  'cloudwatch',
  'stackdriver',
  'graphite',
  'tempo',
  'jaeger',
  'zipkin',
  'testdata',
  'grafana-azure-monitor-datasource',
]);

/**
 * Grafana unit id → the suffix our renderer appends. Deliberately partial:
 * `unit` is only ever printed after the number, so ids that imply a SCALE
 * (`percentunit` is 0–1, `decbytes` is base-10) are dropped rather than
 * mislabelling an unscaled value.
 */
const UNIT_MAP: Record<string, string> = {
  bytes: 'B',
  bits: 'b',
  s: 's',
  ms: 'ms',
  ns: 'ns',
  percent: '%',
  ops: 'ops',
  reqps: 'req/s',
  rps: 'req/s',
  cps: '/s',
  wps: '/s',
  iops: 'IOPS',
  Bps: 'B/s',
  binBps: 'B/s',
};

export interface GrafanaImportResult {
  title: string;
  description: string;
  tags: string[];
  definition: DashboardDefinition;
  /** Everything the import could not carry over, in the order it was found. */
  warnings: string[];
}

/** Parses pasted text, with a message aimed at whoever pasted it. */
export function parseGrafanaJson(text: string): any {
  let parsed: any;
  try {
    parsed = JSON.parse(text);
  } catch (err) {
    throw new Error(`That is not valid JSON: ${(err as Error).message}`);
  }
  if (!parsed || typeof parsed !== 'object') {
    throw new Error('Expected a Grafana dashboard object.');
  }
  // Grafana's "Export for sharing externally" and its API both wrap the model.
  const model = parsed.dashboard && typeof parsed.dashboard === 'object' ? parsed.dashboard : parsed;
  if (!Array.isArray(model.panels)) {
    throw new Error('This JSON has no `panels` array — it does not look like a Grafana dashboard.');
  }
  return model;
}

export function convertGrafanaDashboard(model: any, scope: PanelScope): GrafanaImportResult {
  const warnings: string[] = [];
  const panels: Panel[] = [];
  const usedIds = new Set<number>();

  for (const source of flattenPanels(model.panels, warnings)) {
    const panel = convertPanel(source, scope, usedIds, warnings);
    if (panel) panels.push(panel);
  }

  if (panels.length === 0) {
    warnings.push('No panel in this dashboard could be imported.');
  }

  // Variables are substituted only from the host page's context (a workload or
  // pod detail page). On the dashboards tab nothing supplies them, so the query
  // runs against the literal `$var` text — which silently returns nothing.
  const variables = new Set<string>();
  for (const panel of panels) {
    for (const target of panel.targets || []) {
      for (const name of referencedVariables(target.expr || '')) variables.add(name);
    }
  }
  if (variables.size > 0) {
    warnings.push(
      `Queries reference ${[...variables].map((v) => `$${v}`).join(', ')}. Those are filled in only when a dashboard is opened from a page that ` +
        'supplies them — edit the panels to replace them with literal values.'
    );
  }

  return {
    title: typeof model.title === 'string' && model.title.trim() ? model.title.trim() : 'Imported dashboard',
    description: typeof model.description === 'string' ? model.description : '',
    tags: Array.isArray(model.tags) ? model.tags.filter((t: unknown) => typeof t === 'string') : [],
    definition: { panels },
    warnings,
  };
}

/**
 * Grafana groups panels under `row` panels. A COLLAPSED row nests its children
 * in `panels`; an EXPANDED one is an empty marker followed by its children as
 * siblings. We have no row concept, so both forms lose the grouping — the
 * children survive either way, and the row titles are reported once rather than
 * one warning per row (a 54-panel export has a dozen).
 */
function flattenPanels(panels: any[], warnings: string[]): any[] {
  const out: any[] = [];
  const rowTitles: string[] = [];
  for (const panel of panels || []) {
    if (!panel || typeof panel !== 'object') continue;
    if (panel.type === 'row') {
      rowTitles.push(typeof panel.title === 'string' && panel.title.trim() ? panel.title.trim() : 'Untitled');
      // Collapsed rows carry their children; expanded ones are followed by them.
      if (Array.isArray(panel.panels)) out.push(...panel.panels);
      continue;
    }
    out.push(panel);
  }
  if (rowTitles.length > 0) {
    warnings.push(`Rows were flattened — panels are imported without their grouping (${rowTitles.join(', ')}).`);
  }
  return out;
}

function convertPanel(source: any, scope: PanelScope, usedIds: Set<number>, warnings: string[]): Panel | null {
  const title = typeof source.title === 'string' && source.title.trim() ? source.title.trim() : 'Untitled panel';
  const type = PANEL_TYPE_MAP[source.type];
  if (!type) {
    warnings.push(`Panel "${title}" was skipped — ${source.type || 'unknown'} is not a visualisation this build renders.`);
    return null;
  }

  const id = nextPanelId(source.id, usedIds);
  const gridPos = convertGridPos(source.gridPos);

  if (type === 'text') {
    // Grafana moved text content from `content` to `options.content` in v7.
    const content = typeof source.options?.content === 'string' ? source.options.content : typeof source.content === 'string' ? source.content : '';
    return { id, title, description: descriptionOf(source), type, datasource: 'metrics', grid_pos: gridPos, content };
  }

  const datasourceType = datasourceTypeOf(source.datasource);
  if (datasourceType && NON_PROMETHEUS_DATASOURCES.has(datasourceType)) {
    warnings.push(`Panel "${title}" was skipped — it queries ${datasourceType}, and only Prometheus queries can be imported.`);
    return null;
  }

  const targets = convertTargets(source.targets);
  if (targets.length === 0) {
    warnings.push(`Panel "${title}" was skipped — it has no Prometheus query.`);
    return null;
  }

  return {
    id,
    title,
    description: descriptionOf(source),
    type,
    datasource: 'metrics',
    ...scope,
    grid_pos: gridPos,
    targets,
    unit: unitOf(source),
  };
}

function convertTargets(targets: any): PanelTarget[] {
  const out: PanelTarget[] = [];
  for (const [i, target] of (Array.isArray(targets) ? targets : []).entries()) {
    const expr = typeof target?.expr === 'string' ? target.expr.trim() : '';
    // A target with no `expr` is either a non-Prometheus query (`rawSql`,
    // `query`) or a Grafana expression node; neither is portable.
    if (!expr) continue;
    const legend = normaliseLegendFormat(target.legendFormat);
    out.push({
      ref_id: typeof target.refId === 'string' && target.refId ? target.refId : String.fromCharCode(65 + i),
      expr,
      ...(legend ? { legend_format: legend } : {}),
      ...(target.hide ? { hide: true } : {}),
    });
  }
  return out;
}

/**
 * Grafana's `legendFormat` is a template — `{{pod}}`. The dashboards in this
 * repo also use the older convention of a bare label NAME (`pod`, `__name__`),
 * which the legacy AppDashboard renderer read off the series directly. Both
 * become `{{name}}`, which is what our series labeller resolves; free text
 * ("CPU Time") is left alone and stays the literal legend.
 */
export function normaliseLegendFormat(legendFormat: unknown): string | undefined {
  if (typeof legendFormat !== 'string') return undefined;
  const trimmed = legendFormat.trim();
  if (!trimmed) return undefined;
  if (trimmed.includes('{{')) return trimmed;
  if (/^[A-Za-z_][A-Za-z0-9_.]*$/.test(trimmed)) return `{{${trimmed}}}`;
  return trimmed;
}

/** 24-column Grafana geometry → our 12. */
export function convertGridPos(gridPos: any) {
  const scale = OUR_GRID_COLUMNS / GRAFANA_GRID_COLUMNS;
  const rawWidth = Number(gridPos?.w);
  // A panel with no width is full width in Grafana's renderer too.
  const width = Number.isFinite(rawWidth) && rawWidth > 0 ? Math.round(rawWidth * scale) : OUR_GRID_COLUMNS;
  return {
    x: Math.floor((Number(gridPos?.x) || 0) * scale),
    y: Number(gridPos?.y) || 0,
    // Rounding a 1-column Grafana panel gives 0, which our validation rejects.
    w: Math.min(Math.max(width, 1), OUR_GRID_COLUMNS),
    h: Number(gridPos?.h) || 8,
  };
}

/** `yaxes[0].format` is the pre-7 location; `fieldConfig.defaults.unit` the modern one. */
function unitOf(source: any): string {
  const raw = source?.fieldConfig?.defaults?.unit ?? source?.yaxes?.[0]?.format;
  return typeof raw === 'string' ? UNIT_MAP[raw] || '' : '';
}

function descriptionOf(source: any): string {
  return typeof source?.description === 'string' ? source.description : '';
}

/**
 * A panel's datasource is `{type, uid}` in modern exports, a name or a
 * `${DS_PROMETHEUS}` input reference in older ones. Only an explicit type is
 * conclusive; a name is ambiguous and treated as Prometheus, matching the
 * legacy renderer's default.
 */
function datasourceTypeOf(datasource: any): string {
  if (datasource && typeof datasource === 'object' && typeof datasource.type === 'string') {
    return datasource.type.toLowerCase();
  }
  return '';
}

/**
 * Keeps Grafana's panel id when it is usable — panel ids appear in `repeat` and
 * in links — and otherwise assigns the next free one. Ids only have to be
 * unique inside one dashboard.
 */
function nextPanelId(sourceId: unknown, used: Set<number>): number {
  const id = Number(sourceId);
  if (Number.isInteger(id) && id > 0 && !used.has(id)) {
    used.add(id);
    return id;
  }
  let next = 1;
  while (used.has(next)) next++;
  used.add(next);
  return next;
}
