import { convertGrafanaDashboard, convertGridPos, normaliseLegendFormat, parseGrafanaJson } from '../grafanaImport';
import type { PanelScope } from '../panelAccounts';

// The repo ships 19 real Grafana exports spanning schemaVersion 16 → 39. They
// are the corpus this converter has to survive, so it is tested against the
// files themselves rather than against hand-written fixtures that would drift.
import otelGolang from '@components/dashboards/apps/otel_golang.json';
import promMongoMetrics from '@components/dashboards/apps/prom_mongo_metrics.json';
import nginx from '@components/dashboards/apps/nginx.json';
import nbClickhouse from '@components/dashboards/apps/nb_clickhouse.json';
import otelNodejs from '@components/dashboards/apps/otel_nodejs.json';

const SCOPE: PanelScope = { account_type: 'K8s', account_ids: [] };
const VALID_TYPES = new Set(['timeseries', 'stat', 'table', 'bar', 'text']);

describe('parseGrafanaJson', () => {
  it('accepts a bare dashboard model and the API-wrapped form', () => {
    // `GET /api/dashboards/uid/:uid` wraps the model; the export button does not.
    expect(parseGrafanaJson('{"panels":[],"title":"x"}').title).toBe('x');
    expect(parseGrafanaJson('{"dashboard":{"panels":[],"title":"y"}}').title).toBe('y');
  });

  it('explains what is wrong rather than throwing a parser error', () => {
    expect(() => parseGrafanaJson('{oops')).toThrow(/not valid JSON/);
    expect(() => parseGrafanaJson('{"title":"x"}')).toThrow(/no `panels` array/);
    expect(() => parseGrafanaJson('"a string"')).toThrow(/Grafana dashboard object/);
  });
});

describe('convertGridPos', () => {
  it('halves Grafana 24-column geometry into our 12', () => {
    expect(convertGridPos({ x: 12, y: 3, w: 12, h: 8 })).toEqual({ x: 6, y: 3, w: 6, h: 8 });
    expect(convertGridPos({ x: 0, y: 0, w: 24, h: 9 })).toEqual({ x: 0, y: 0, w: 12, h: 9 });
  });

  it('never produces a zero width', () => {
    // A 1-column Grafana panel rounds to 0, which our own save-time validation
    // rejects — the import would fail on the last step.
    expect(convertGridPos({ x: 0, y: 0, w: 1, h: 4 }).w).toBe(1);
  });

  it('falls back to full width when the geometry is missing', () => {
    expect(convertGridPos(undefined)).toEqual({ x: 0, y: 0, w: 12, h: 8 });
    expect(convertGridPos({ h: 4 }).w).toBe(12);
  });
});

describe('normaliseLegendFormat', () => {
  it('keeps a Grafana template as-is', () => {
    expect(normaliseLegendFormat('{{pod}}')).toBe('{{pod}}');
    expect(normaliseLegendFormat('rx - {{hostname}}:{{process_port}}')).toBe('rx - {{hostname}}:{{process_port}}');
  });

  it('wraps a bare label name, which is how the legacy dashboards write it', () => {
    // AppDashboard read `legendFormat` as a label to look up on the series;
    // left bare, every series in the panel would be named "pod".
    expect(normaliseLegendFormat('pod')).toBe('{{pod}}');
    expect(normaliseLegendFormat('__name__')).toBe('{{__name__}}');
    expect(normaliseLegendFormat('src_workload_name')).toBe('{{src_workload_name}}');
  });

  it('leaves free text alone', () => {
    expect(normaliseLegendFormat('CPU Time')).toBe('CPU Time');
    expect(normaliseLegendFormat('go.memory.used (total)')).toBe('go.memory.used (total)');
  });

  it('is undefined when there is nothing to name the series with', () => {
    expect(normaliseLegendFormat('')).toBeUndefined();
    expect(normaliseLegendFormat('   ')).toBeUndefined();
    expect(normaliseLegendFormat(undefined)).toBeUndefined();
  });
});

describe('convertGrafanaDashboard', () => {
  it('carries the dashboard identity across', () => {
    const result = convertGrafanaDashboard(otelGolang, SCOPE);
    expect(result.title).toBe((otelGolang as any).title);
    expect(result.definition.panels.length).toBeGreaterThan(0);
  });

  it('applies the chosen account scope to every panel', () => {
    // Grafana JSON names no account, so the importer supplies one — without it
    // every panel fails our save-time validation.
    const result = convertGrafanaDashboard(otelGolang, { account_ids: ['a1'], account_type: undefined });
    for (const panel of result.definition.panels) {
      expect(panel.account_ids).toEqual(['a1']);
      expect(panel.account_type).toBeUndefined();
    }
  });

  it('flattens rows and reports it', () => {
    // prom_mongo_metrics is organised entirely into rows; we have no row
    // concept, so their children are hoisted.
    const result = convertGrafanaDashboard(promMongoMetrics, SCOPE);
    expect(result.warnings.some((w) => /flattened/.test(w))).toBe(true);
    expect(result.definition.panels.every((p) => p.type !== ('row' as any))).toBe(true);
  });

  it('skips visualisations this build cannot draw, naming them', () => {
    // nginx.json carries a heatmap.
    const result = convertGrafanaDashboard(nginx, SCOPE);
    const skipped = result.warnings.filter((w) => /was skipped/.test(w));
    expect(skipped.some((w) => /heatmap/.test(w))).toBe(true);
  });

  it('maps the pre-7 panel types the older exports still use', () => {
    // otel_nodejs is schema 18: `graph` + `singlestat`.
    const types = new Set(convertGrafanaDashboard(otelNodejs, SCOPE).definition.panels.map((p) => p.type));
    expect(types.has('timeseries')).toBe(true);
    expect(types.has('stat')).toBe(true);
  });

  it('warns about variables nothing on this page can fill', () => {
    // otel_golang queries $namespaceName / $podNameFilter, which only a
    // workload/pod detail page supplies.
    const result = convertGrafanaDashboard(otelGolang, SCOPE);
    expect(result.warnings.some((w) => w.includes('$namespaceName'))).toBe(true);
  });

  it('says so when nothing could be imported', () => {
    const result = convertGrafanaDashboard({ title: 'empty', panels: [] }, SCOPE);
    expect(result.definition.panels).toEqual([]);
    expect(result.warnings).toContain('No panel in this dashboard could be imported.');
  });

  it('skips a panel whose datasource is not Prometheus', () => {
    const result = convertGrafanaDashboard(
      {
        title: 'mixed',
        panels: [
          { id: 1, type: 'timeseries', title: 'loki panel', datasource: { type: 'loki' }, targets: [{ refId: 'A', expr: '{app="x"}' }] },
          { id: 2, type: 'timeseries', title: 'prom panel', datasource: { type: 'prometheus' }, targets: [{ refId: 'A', expr: 'up' }] },
        ],
      },
      SCOPE
    );
    expect(result.definition.panels.map((p) => p.title)).toEqual(['prom panel']);
    expect(result.warnings.some((w) => /loki/.test(w))).toBe(true);
  });

  it('drops targets that carry no expression, and the panel if none remain', () => {
    const result = convertGrafanaDashboard(
      {
        title: 'sql',
        panels: [
          { id: 1, type: 'table', title: 'sql panel', targets: [{ refId: 'A', rawSql: 'select 1' }] },
          {
            id: 2,
            type: 'timeseries',
            title: 'half',
            targets: [
              { refId: 'A', rawSql: 'select 1' },
              { refId: 'B', expr: 'up' },
            ],
          },
        ],
      },
      SCOPE
    );
    expect(result.definition.panels.map((p) => p.title)).toEqual(['half']);
    expect(result.definition.panels[0].targets).toHaveLength(1);
    expect(result.definition.panels[0].targets?.[0].ref_id).toBe('B');
  });

  it('reads text-panel content from both the modern and legacy field', () => {
    const result = convertGrafanaDashboard(
      {
        title: 'text',
        panels: [
          { id: 1, type: 'text', title: 'modern', options: { content: 'new' } },
          { id: 2, type: 'text', title: 'legacy', content: 'old' },
        ],
      },
      SCOPE
    );
    expect(result.definition.panels.map((p) => p.content)).toEqual(['new', 'old']);
  });

  it('maps units it can print and drops the ones that imply a rescale', () => {
    const result = convertGrafanaDashboard(
      {
        title: 'units',
        panels: [
          { id: 1, type: 'timeseries', title: 'b', fieldConfig: { defaults: { unit: 'bytes' } }, targets: [{ refId: 'A', expr: 'up' }] },
          { id: 2, type: 'graph', title: 'legacy', yaxes: [{ format: 's' }], targets: [{ refId: 'A', expr: 'up' }] },
          // 0–1 in Grafana; we do not rescale, so "0.85 %" would be a lie.
          { id: 3, type: 'timeseries', title: 'ratio', fieldConfig: { defaults: { unit: 'percentunit' } }, targets: [{ refId: 'A', expr: 'up' }] },
          { id: 4, type: 'timeseries', title: 'short', fieldConfig: { defaults: { unit: 'short' } }, targets: [{ refId: 'A', expr: 'up' }] },
        ],
      },
      SCOPE
    );
    expect(result.definition.panels.map((p) => p.unit)).toEqual(['B', 's', '', '']);
  });

  it('never emits a duplicate panel id', () => {
    const result = convertGrafanaDashboard(
      {
        title: 'dupes',
        panels: [
          { id: 5, type: 'timeseries', title: 'a', targets: [{ refId: 'A', expr: 'up' }] },
          { id: 5, type: 'timeseries', title: 'b', targets: [{ refId: 'A', expr: 'up' }] },
          { type: 'timeseries', title: 'no id', targets: [{ refId: 'A', expr: 'up' }] },
        ],
      },
      SCOPE
    );
    const ids = result.definition.panels.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

// Every shipped export must convert into a document our own save-time
// validation would accept. This is the check that catches a schema variant the
// hand-written cases above never thought of.
describe('the shipped Grafana exports all convert to a valid definition', () => {
  const CORPUS: [string, any][] = [
    ['otel_golang', otelGolang],
    ['prom_mongo_metrics', promMongoMetrics],
    ['nginx', nginx],
    ['nb_clickhouse', nbClickhouse],
    ['otel_nodejs', otelNodejs],
  ];

  it.each(CORPUS)('%s', (_name, model) => {
    const result = convertGrafanaDashboard(model, SCOPE);
    expect(result.definition.panels.length).toBeGreaterThan(0);

    const ids = new Set<number>();
    for (const panel of result.definition.panels) {
      // Mirrors ValidateDefinition in api-server/services/dashboard/service.go.
      expect(panel.title.length).toBeGreaterThan(0);
      expect(VALID_TYPES.has(panel.type)).toBe(true);
      expect(ids.has(panel.id)).toBe(false);
      ids.add(panel.id);
      expect(panel.grid_pos.w).toBeGreaterThanOrEqual(1);
      expect(panel.grid_pos.w).toBeLessThanOrEqual(12);
      if (panel.type === 'text') continue;
      expect(panel.account_type || (panel.account_ids || []).length > 0).toBeTruthy();
      expect(panel.targets?.length).toBeGreaterThan(0);
      for (const target of panel.targets || []) {
        expect(target.expr?.trim().length).toBeGreaterThan(0);
        expect(target.ref_id.length).toBeGreaterThan(0);
      }
    }
  });
});
