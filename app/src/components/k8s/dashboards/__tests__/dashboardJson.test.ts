import { dashboardJsonText, readDashboardJson, type EditableDashboard } from '../dashboardJson';

const accounts = [{ value: 'acc-1' }, { value: 'acc-2' }];

const current: EditableDashboard = {
  title: 'Fleet',
  description: 'Every cluster',
  definition: {
    time_from: 'now-6h',
    panels: [
      {
        id: 1,
        title: 'CPU',
        type: 'stat',
        datasource: 'metrics',
        account_ids: ['acc-1', 'acc-2'],
        grid_pos: { x: 0, y: 0, w: 4, h: 8 },
        targets: [{ ref_id: 'A', expr: 'sum(cpu)' }],
        unit: '',
        options: { thresholds: [{ value: 80, color: 'red' }] },
      },
      {
        id: 2,
        title: 'Errors',
        type: 'timeseries',
        datasource: 'logs',
        account_type: 'K8S',
        provider: 'ES',
        provider_index: 'logs-*',
        grid_pos: { x: 4, y: 0, w: 8, h: 8 },
        targets: [{ ref_id: 'A', expr: 'level:error' }],
        unit: '',
      },
    ],
  },
} as EditableDashboard;

const read = (value: unknown) => readDashboardJson(typeof value === 'string' ? value : JSON.stringify(value), accounts, current);
const edited = (change: (d: any) => void) => {
  const d = JSON.parse(dashboardJsonText(current));
  change(d);
  return d;
};
const errorsOf = (value: unknown) => {
  const result = read(value);
  return result.ok ? [] : result.errors;
};
const warningsOf = (value: unknown) => {
  const result = read(value);
  return result.ok ? result.warnings : [];
};

describe('the dashboard as JSON', () => {
  it('reads back exactly what it wrote — nothing changed, nothing lost', () => {
    const result = read(dashboardJsonText(current));
    expect(result).toMatchObject({ ok: true, changed: false, warnings: [] });
    // The settings the importer used to drop survive the round trip.
    expect(result.ok && result.dashboard.definition).toMatchObject({
      time_from: 'now-6h',
      panels: [{ options: { thresholds: [{ value: 80, color: 'red' }] } }, { provider: 'ES', provider_index: 'logs-*' }],
    });
  });

  it('is no change when only key order and whitespace differ', () => {
    const d = JSON.parse(dashboardJsonText(current));
    const reversed = (o: Record<string, unknown>) => Object.fromEntries(Object.entries(o).reverse());
    const reordered = { definition: { time_from: 'now-6h', panels: d.definition.panels.map(reversed) }, description: d.description, title: d.title };
    expect(read(JSON.stringify(reordered))).toMatchObject({ ok: true, changed: false });
  });

  it('is a change when a setting changes, and applies it', () => {
    const result = read(edited((d) => (d.definition.panels[0].title = 'CPU used')));
    expect(result).toMatchObject({ ok: true, changed: true });
    expect(result.ok && result.dashboard.definition.panels[0].title).toBe('CPU used');
  });

  it('accepts an export pasted back, account labels and all, without showing them as a change', () => {
    const exported = { ...JSON.parse(dashboardJsonText(current)), accounts: { 'acc-1': { label: 'prod', cloud_provider: 'K8S' } } };
    expect(read(exported)).toMatchObject({ ok: true, changed: false, warnings: [] });
  });

  it('folds the single account panels were once written with, as the server does', () => {
    const legacy = edited((d) => {
      delete d.definition.panels[0].account_ids;
      d.definition.panels[0].account_id = 'acc-1';
    });
    const result = read(legacy);
    expect(result.ok && result.dashboard.definition.panels[0].account_ids).toEqual(['acc-1']);
  });
});

describe('what an edit leaves alone', () => {
  // A text panel that used to be a table keeps the table's accounts and settings
  // — the panel editor clears only thresholds on a type change — and a panel of
  // a type this build does not render is still a stored panel.
  const leftovers: EditableDashboard = {
    ...current,
    definition: {
      ...current.definition,
      panels: [
        current.definition.panels[0],
        {
          id: 3,
          title: 'Notes (was a table)',
          type: 'text',
          datasource: 'nudgebee',
          account_ids: ['acc-2'],
          grid_pos: { x: 0, y: 8, w: 12, h: 4 },
          unit: 'bytes',
          provider: 'ES',
          content: 'hello',
          options: { columns: [{ name: 'foo' }] },
        },
        { id: 4, title: 'Future', type: 'piechart', datasource: 'metrics', account_ids: ['acc-1'], grid_pos: { x: 0, y: 12, w: 6, h: 8 } },
      ] as any,
    },
  };

  it('keeps every setting of a panel the edit did not touch, whatever the panel is', () => {
    const d = JSON.parse(dashboardJsonText(leftovers));
    d.definition.panels[0].title = 'CPU used';
    const result = readDashboardJson(JSON.stringify(d), accounts, leftovers);
    expect(result.ok).toBe(true);
    const [, notes, future] = result.ok ? result.dashboard.definition.panels : [];
    expect(notes).toEqual(leftovers.definition.panels[1]);
    expect(future).toEqual(leftovers.definition.panels[2]);
  });

  it('shows the dashboard exactly as stored — nothing is dropped from the current side', () => {
    const shown = JSON.parse(dashboardJsonText(leftovers)).definition.panels;
    expect(shown[1]).toMatchObject({
      datasource: 'nudgebee',
      account_ids: ['acc-2'],
      unit: 'bytes',
      provider: 'ES',
      options: { columns: [{ name: 'foo' }] },
    });
    expect(shown[2]).toMatchObject({ id: 4, type: 'piechart' });
  });

  it('leaves out empty settings as the server does, so an explicit blank is no change', () => {
    const d = JSON.parse(dashboardJsonText(current));
    d.definition.panels[0].description = '';
    d.definition.panels[0].provider = '';
    d.definition.panels[0].targets[0].hide = false;
    expect(read(d)).toMatchObject({ ok: true, changed: false });
  });
});

describe('what the JSON editor refuses', () => {
  it('says what is wrong with text that is not JSON', () => {
    expect(errorsOf('{ "title": ')[0]).toMatch(/^That is not valid JSON/);
  });

  it('sends a Grafana dashboard or a single panel to Import', () => {
    expect(errorsOf({ title: 'Node exporter', panels: [] })[0]).toMatch(/Grafana dashboard goes through Import/);
    expect(errorsOf(current.definition.panels[0])[0]).toMatch(/single panel/);
  });

  it('needs a title', () => {
    expect(errorsOf(edited((d) => (d.title = '  ')))).toContain('The dashboard needs a "title".');
  });

  it('refuses a panel the importer would have dropped, instead of losing it', () => {
    expect(errorsOf(edited((d) => (d.definition.panels[1].type = 'piechart')))[0]).toMatch(/Panel "Errors" was skipped/);
  });

  it('refuses a panel that names no accounts', () => {
    const unscoped = edited((d) => delete d.definition.panels[0].account_ids);
    expect(errorsOf(unscoped)).toContain('Panel "CPU" names no accounts — set "account_type" or "account_ids".');
  });

  it('refuses an account the viewer cannot use — Save would be refused for it', () => {
    expect(errorsOf(edited((d) => (d.definition.panels[0].account_ids = ['acc-1', 'acc-9'])))[0]).toMatch(/not available to you here: acc-9/);
  });

  it('refuses two panels with one id, rather than renumbering one behind the author’s back', () => {
    expect(errorsOf(edited((d) => (d.definition.panels[1].id = 1)))).toContain('2 panels share the id 1. Give each panel its own.');
  });
});

describe('what the JSON editor warns about', () => {
  it('names a setting the server would drop', () => {
    expect(warningsOf(edited((d) => (d.definition.panels[0].colour = 'red')))).toContain(
      'Panel "CPU": "colour" is not a panel setting, so it is dropped.'
    );
    expect(warningsOf(edited((d) => (d.owner = 'me')))).toContain('"owner" is not a dashboard setting, so it is ignored.');
  });

  it('says when a panel’s new id will break links to it', () => {
    expect(warningsOf(edited((d) => (d.definition.panels[0].id = 7)))).toContain('Panel "CPU" now has id 7 (was 1) — links to it will stop working.');
  });

  it('notes template variables only when the edit brings them in', () => {
    const withVariable = edited((d) => (d.definition.panels[0].targets[0].expr = 'sum(cpu{namespace="$namespace"})'));
    expect(warningsOf(withVariable)[0]).toMatch(/^Queries reference \$namespace/);
    const alreadyThere = {
      ...current,
      definition: {
        ...current.definition,
        panels: [
          { ...current.definition.panels[0], targets: [{ ref_id: 'A', expr: 'sum(cpu{namespace="$namespace"})' }] },
          current.definition.panels[1],
        ],
      },
    };
    const renamed = JSON.parse(dashboardJsonText(alreadyThere));
    renamed.title = 'Renamed';
    const result = readDashboardJson(JSON.stringify(renamed), accounts, alreadyThere);
    expect(result.ok && result.warnings).toEqual([]);
  });

  it('does not trip over a query whose expression is not text', () => {
    const odd = edited((d) => d.definition.panels[0].targets.push({ ref_id: 'B', expr: 123 }));
    expect(() => read(odd)).not.toThrow();
  });

  it('says nothing about a panel that was simply deleted', () => {
    expect(warningsOf(edited((d) => d.definition.panels.splice(0, 1)))).toEqual([]);
  });
});
