import { collectSourceScopes, convertNativeDashboard, isNativeDashboard, panelSourceKey, parseImportJson } from '../nativeImport';

const SCOPE = { account_type: 'K8S', account_ids: [] };

const PANEL = {
  id: 3,
  title: 'Registered Runners',
  description: 'Runners known to GitHub',
  type: 'stat',
  datasource: 'metrics',
  account_type: 'AWS',
  grid_pos: { x: 0, y: 0, w: 3, h: 4 },
  targets: [{ ref_id: 'A', expr: 'sum(runners_registered)' }],
  unit: '',
};

const DASHBOARD = { title: 'GitHub Runners', description: 'ARC pool', definition: { panels: [PANEL] } };

describe('isNativeDashboard', () => {
  it('recognises our own dashboard and panel exports', () => {
    expect(isNativeDashboard(DASHBOARD)).toBe(true);
    expect(isNativeDashboard(PANEL)).toBe(true);
  });

  it('leaves a Grafana model to the Grafana importer', () => {
    // Grafana puts panels at the top level and writes gridPos / an object
    // datasource — neither tell is present here.
    const grafana = { title: 'Node exporter', panels: [{ id: 1, type: 'timeseries', gridPos: { x: 0, y: 0, w: 12, h: 8 } }] };
    expect(isNativeDashboard(grafana)).toBe(false);
    expect(isNativeDashboard(null)).toBe(false);
    expect(isNativeDashboard({ title: 'nothing' })).toBe(false);
  });
});

describe('convertNativeDashboard', () => {
  it('round-trips an exported dashboard', () => {
    const result = convertNativeDashboard(DASHBOARD, SCOPE);
    expect(result.title).toBe('GitHub Runners');
    expect(result.definition.panels).toHaveLength(1);
    expect(result.definition.panels[0]).toMatchObject({
      id: 3,
      title: 'Registered Runners',
      type: 'stat',
      datasource: 'metrics',
      targets: [{ ref_id: 'A', expr: 'sum(runners_registered)' }],
    });
  });

  it('falls back to the importer’s default scope when nothing is mapped, and says so', () => {
    // Account ids are tenant-local, so the file's own scope cannot be trusted
    // in the tenant importing it.
    const result = convertNativeDashboard(DASHBOARD, SCOPE);
    expect(result.definition.panels[0].account_type).toBe('K8S');
    expect(result.warnings.some((w) => w.includes('not mapped'))).toBe(true);
  });

  it('imports a single exported panel as a one-panel dashboard', () => {
    const result = convertNativeDashboard(PANEL, SCOPE);
    expect(result.title).toBe('Registered Runners (imported panel)');
    expect(result.definition.panels).toHaveLength(1);
  });

  it('drops a panel that the server would reject, keeping the rest', () => {
    // The save is all-or-nothing: one panel with no query would take the whole
    // import down with a message about that panel alone.
    const result = convertNativeDashboard(
      { title: 'Mixed', definition: { panels: [PANEL, { ...PANEL, id: 4, title: 'Empty', targets: [] }, { ...PANEL, id: 5, type: 'heatmap' }] } },
      SCOPE
    );
    expect(result.definition.panels.map((p) => p.id)).toEqual([3]);
    expect(result.warnings.some((w) => w.includes('"Empty" was skipped'))).toBe(true);
    expect(result.warnings.some((w) => w.includes('heatmap'))).toBe(true);
  });

  it('keeps an entity panel’s query object', () => {
    // nudgebee / traces panels store a query document, not an expression — a
    // targets filter written for expressions alone would drop them all.
    const entity = {
      ...PANEL,
      type: 'table',
      datasource: 'nudgebee',
      targets: [{ ref_id: 'A', query: { table: 'events' }, time_column: 'created_at' }],
    };
    const result = convertNativeDashboard({ title: 'Events', definition: { panels: [entity] } }, SCOPE);
    expect(result.definition.panels[0].targets).toEqual([{ ref_id: 'A', query: { table: 'events' }, time_column: 'created_at' }]);
  });

  it('carries a text panel with no datasource or account', () => {
    const text = { id: 9, title: 'Runner Pool', type: 'text', content: 'Pool size', grid_pos: { x: 0, y: 0, w: 12, h: 3 } };
    const result = convertNativeDashboard({ title: 'Doc', definition: { panels: [text] } }, SCOPE);
    expect(result.definition.panels[0]).toMatchObject({ type: 'text', content: 'Pool size' });
    expect(result.definition.panels[0].account_type).toBeUndefined();
  });

  it('clamps a hand-edited geometry back into the grid', () => {
    const wide = { ...PANEL, grid_pos: { x: -2, y: 1, w: 40, h: 0 } };
    const result = convertNativeDashboard({ title: 'Wide', definition: { panels: [wide] } }, SCOPE);
    expect(result.definition.panels[0].grid_pos).toEqual({ x: 0, y: 1, w: 12, h: 8 });
  });
});

describe('collectSourceScopes', () => {
  const MULTI = {
    title: 'Two accounts',
    definition: {
      panels: [
        { ...PANEL, id: 1, account_type: undefined, account_ids: ['acc-a'] },
        { ...PANEL, id: 2, account_type: undefined, account_ids: ['acc-a'] },
        { ...PANEL, id: 3, account_type: undefined, account_ids: ['acc-b'] },
        { ...PANEL, id: 4, account_type: 'GCP', account_ids: [] },
        { id: 5, title: 'Note', type: 'text', content: 'hi', grid_pos: { x: 0, y: 0, w: 12, h: 3 } },
      ],
    },
    accounts: { 'acc-a': { label: 'prod-payments', cloud_provider: 'AWS' }, 'acc-b': { label: 'staging', cloud_provider: 'AWS' } },
  };

  it('groups panels by the scope they share, in file order', () => {
    const scopes = collectSourceScopes(MULTI);
    expect(scopes.map((s) => [s.label, s.panelCount])).toEqual([
      ['prod-payments', 2],
      ['staging', 1],
      ['All GCP', 1],
    ]);
  });

  it('names accounts from the file’s own accounts block, falling back to the id', () => {
    const withoutLabels = { ...MULTI, accounts: undefined };
    expect(collectSourceScopes(withoutLabels)[0].label).toBe('acc-a');
  });

  it('ignores text panels, which name no accounts', () => {
    expect(collectSourceScopes(MULTI).some((s) => s.label === 'Note')).toBe(false);
  });

  it('treats the same accounts in a different order as one scope', () => {
    expect(panelSourceKey({ account_ids: ['b', 'a'] })).toBe(panelSourceKey({ account_ids: ['a', 'b'] }));
    expect(panelSourceKey({ account_type: 'AWS' })).toBe('type:AWS');
    expect(panelSourceKey({ type: 'text' })).toBe('');
  });

  it('applies a mapping per scope and defaults the rest', () => {
    const result = convertNativeDashboard(MULTI, { account_type: 'K8S', account_ids: [] }, { 'ids:acc-a': { account_ids: ['local-1'] } });
    const byId = Object.fromEntries(result.definition.panels.map((p) => [p.id, p]));
    // Mapped scope reaches only the panels that shared it…
    expect(byId[1].account_ids).toEqual(['local-1']);
    expect(byId[2].account_ids).toEqual(['local-1']);
    // …and everything unmapped falls back to the default, as before mapping.
    expect(byId[3].account_type).toBe('K8S');
    expect(byId[4].account_type).toBe('K8S');
    expect(result.warnings.some((w) => w.includes('not mapped'))).toBe(true);
  });

  it('says nothing about defaults when every scope is mapped', () => {
    const result = convertNativeDashboard(
      MULTI,
      { account_type: 'K8S', account_ids: [] },
      {
        'ids:acc-a': { account_ids: ['local-1'] },
        'ids:acc-b': { account_ids: ['local-2'] },
        'type:GCP': { account_type: 'GCP', account_ids: [] },
      }
    );
    expect(result.warnings.some((w) => w.includes('not mapped'))).toBe(false);
  });
});

describe('parseImportJson', () => {
  it('unwraps the `dashboard` envelope Grafana exports use', () => {
    expect(parseImportJson('{"dashboard":{"title":"Wrapped","panels":[]}}')).toEqual({ title: 'Wrapped', panels: [] });
  });

  it('reports malformed JSON in terms of the paste', () => {
    expect(() => parseImportJson('{oops')).toThrow(/not valid JSON/);
    expect(() => parseImportJson('"a string"')).toThrow(/Expected a dashboard object/);
  });
});
