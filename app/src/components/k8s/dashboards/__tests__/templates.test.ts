import { defaultWidgetScope, PANEL_TEMPLATES, panelFromTemplate, TEMPLATE_ROLES, WIDGET_CATEGORIES, widgetAccountKind } from '../panelTemplates';
import { buildTemplateDocument, convertTemplate, DASHBOARD_TEMPLATES, templateWidgets } from '../dashboardTemplates';
import { draftFromQuery, findTable } from '../entityQuery';
import { referencedVariables } from '../templating';

/**
 * The catalogue is data, so these are the checks a type cannot make: that every
 * widget a dashboard names exists, that every widget survives the importer the
 * gallery runs it through, and that no query references a variable nobody
 * declares — which would ship a dashboard querying the literal text `$namespace`.
 */

/** A tenant with one cluster and two cloud accounts — enough to tell the kinds apart. */
const ACCOUNTS = [
  { label: 'prod-cluster', value: 'k1', cloud_provider: 'K8S', kind: 'kubernetes' },
  { label: 'prod-aws', value: 'a1', cloud_provider: 'AWS', kind: 'cloud' },
  { label: 'prod-gcp', value: 'g1', cloud_provider: 'GCP', kind: 'cloud' },
];

describe('widget library', () => {
  it('has unique ids', () => {
    const ids = PANEL_TEMPLATES.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it('gives every widget exactly one target', () => {
    // The panel editor rewrites target A in place, so a second target would be
    // dropped the first time someone opened the panel.
    for (const template of PANEL_TEMPLATES) {
      expect(template.panel.targets).toHaveLength(1);
    }
  });

  it('puts every widget in a category the picker actually renders', () => {
    // The picker iterates WIDGET_CATEGORIES, not the widgets — a category left
    // out of that list hides every widget in it, silently and with no error.
    for (const template of PANEL_TEMPLATES) {
      expect(WIDGET_CATEGORIES).toContain(template.category);
    }
  });

  it('keeps variables out of entity and trace queries', () => {
    // Those filters are literals compared against a typed column, so a `.*`
    // default would filter the panel down to nothing rather than open it up.
    for (const template of PANEL_TEMPLATES) {
      if (template.panel.datasource === 'metrics') continue;
      expect(JSON.stringify(template.panel.targets)).not.toMatch(/\$\w/);
    }
  });

  it('builds entity queries the builder can read back', () => {
    for (const template of PANEL_TEMPLATES) {
      const query = template.panel.targets?.[0]?.query;
      if (!query) continue;
      const draft = draftFromQuery(query);
      const table = findTable(draft.table);
      expect(table.value).toBe((query as any).table);
      expect(table.datasource).toBe(template.panel.datasource);
      // Every column the widget selects must exist on the table it queries.
      for (const name of draft.columns) {
        expect(table.columns.some((c) => c.name === name)).toBe(true);
      }
    }
  });

  it('copies a widget pre-scoped, and shares no state with the library', () => {
    const template = PANEL_TEMPLATES.find((t) => t.id === 'noisiest-issues')!;
    const copy = panelFromTemplate(template, [{ id: 7 } as any], ACCOUNTS);

    expect(copy.id).toBe(8);
    // An events widget is about the cluster, and one provider is a type.
    expect(copy).toMatchObject({ account_type: 'K8S', account_ids: [] });

    (copy.targets![0].query as any).table = 'mutated';
    expect((template.panel.targets![0].query as any).table).not.toBe('mutated');
  });

  it('scopes a PromQL widget to the cluster provider, by kind rather than by name', () => {
    const template = PANEL_TEMPLATES.find((t) => t.panel.datasource === 'metrics')!;
    expect(panelFromTemplate(template, [], ACCOUNTS)).toMatchObject({ account_type: 'K8S', account_ids: [] });

    // Renaming the provider must not break the rule — `kind` is what identifies
    // a cluster, and 'K8S' is backend vocabulary we do not own.
    const renamed = ACCOUNTS.map((a) => (a.kind === 'kubernetes' ? { ...a, cloud_provider: 'KUBERNETES' } : a));
    expect(panelFromTemplate(template, [], renamed)).toMatchObject({ account_type: 'KUBERNETES' });
  });

  it('spans every cloud provider for a cost widget, which only the query engine can do', () => {
    const template = PANEL_TEMPLATES.find((t) => t.id === 'savings-by-account')!;
    // Two cloud providers cannot be one `account_type`, so this is the one case
    // that resolves to ids — and it covers ALL of them, not the first connected.
    expect(defaultWidgetScope(template, ACCOUNTS)).toEqual({ account_type: undefined, account_ids: ['a1', 'g1'] });

    // One cloud provider still fits in a type, which keeps resolving at render.
    const awsOnly = ACCOUNTS.filter((a) => a.cloud_provider !== 'GCP');
    expect(defaultWidgetScope(template, awsOnly)).toEqual({ account_type: 'AWS', account_ids: [] });
  });

  it('never pins individual accounts for a single-provider widget', () => {
    for (const template of PANEL_TEMPLATES) {
      const scope = defaultWidgetScope(template, ACCOUNTS);
      if (scope.account_type) expect(scope.account_ids).toEqual([]);
    }
  });

  it('leaves a widget unscoped when nothing fits, rather than guessing', () => {
    const template = PANEL_TEMPLATES.find((t) => t.panel.datasource === 'metrics')!;
    const cloudOnly = ACCOUNTS.filter((a) => a.kind !== 'kubernetes');
    expect(defaultWidgetScope(template, cloudOnly)).toEqual({ account_type: undefined, account_ids: [] });
    expect(defaultWidgetScope(template, [])).toEqual({ account_type: undefined, account_ids: [] });
  });

  it('puts the cost widgets on cloud accounts, and nothing else', () => {
    for (const template of PANEL_TEMPLATES) {
      // What a widget is ABOUT decides this, not its datasource — savings and
      // spend live against cloud accounts, K8s events and PromQL against the
      // cluster. `any` is the third case: audits, anomalies and approvals belong
      // to whichever account the change or detection touched.
      const kind = widgetAccountKind(template);
      if (template.category === 'Cost') expect(kind).toBe('cloud');
      else expect(kind).not.toBe('cloud');
    }
  });

  it('spans every account only where the query engine can', () => {
    // `any` resolves to accounts of several providers at once, which only the
    // query engine accepts — every other datasource resolves ONE provider's
    // integration, and would silently take whichever came first.
    for (const template of PANEL_TEMPLATES) {
      if (widgetAccountKind(template) === 'any') expect(template.panel.datasource).toBe('nudgebee');
    }

    const audit = PANEL_TEMPLATES.find((t) => t.id === 'audit-log')!;
    expect(defaultWidgetScope(audit, ACCOUNTS)).toEqual({ account_type: undefined, account_ids: ['k1', 'a1', 'g1'] });
  });

  it('leaves the time range off for a table with nothing to filter it on', () => {
    // A current-state snapshot (clusters) and a grouping whose only date column
    // is a max() (CIS) cannot take a WHERE on time. Shipping them with the range
    // on would filter every row out.
    for (const template of PANEL_TEMPLATES) {
      const query = template.panel.targets?.[0]?.query as any;
      if (!query) continue;
      if (findTable(query.table).timeColumns.length === 0) {
        expect(template.panel.targets?.[0]?.time_column || '').toBe('');
      }
    }
  });
});

describe('dashboard templates', () => {
  it('has unique ids and names every widget it uses', () => {
    const ids = DASHBOARD_TEMPLATES.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);

    for (const template of DASHBOARD_TEMPLATES) {
      // templateWidgets drops what it cannot resolve, so a shortfall IS the
      // missing-widget failure.
      expect(templateWidgets(template)).toHaveLength(template.panels.length);
    }
  });

  it('gives every role somewhere to land', () => {
    // The gallery filters by role. A role with no dashboard is a persona that
    // opens the picker and is told there is nothing for them.
    for (const role of TEMPLATE_ROLES) {
      const forRole = DASHBOARD_TEMPLATES.filter((t) => t.roles.includes(role.value));
      expect(forRole.length).toBeGreaterThan(0);
      // And every widget in it must be one that role would recognise as theirs.
      for (const template of forRole) expect(template.panels.length).toBeGreaterThanOrEqual(5);
    }
  });

  it('ships no template variables at all', () => {
    // A `$name` token is only substituted when a dashboard is opened from a page
    // that supplies one. A template has no such page, so a token here would run
    // as literal text and the panel would quietly find nothing.
    for (const template of DASHBOARD_TEMPLATES) {
      for (const widget of templateWidgets(template)) {
        expect(referencedVariables(widget.panel.targets?.[0]?.expr || '')).toEqual([]);
      }
    }
  });

  it('converts every panel through the importer, with no warnings', () => {
    for (const template of DASHBOARD_TEMPLATES) {
      const scopes = templateWidgets(template).map((w) => defaultWidgetScope(w, ACCOUNTS));
      const converted = convertTemplate(template, '', scopes);

      expect(converted.warnings).toEqual([]);
      expect(converted.definition.panels).toHaveLength(template.panels.length);
      expect(converted.title).toBe(template.title);
      for (const panel of converted.definition.panels) {
        // Every panel is scoped, and none was silently given the empty default.
        expect(Boolean(panel.account_type) || Boolean(panel.account_ids?.length)).toBe(true);
        // Nothing may reach the server still holding a template token.
        expect(referencedVariables(panel.targets?.[0]?.expr || '')).toEqual([]);
      }
    }
  });

  it('keeps each panel on its own scope instead of collapsing onto one', () => {
    // The whole point of per-panel scope: a template mixing PromQL with findings
    // must come out with a cluster-scoped chart NEXT TO a tenant-wide table.
    const template = DASHBOARD_TEMPLATES.find((t) => t.id === 'executive-overview')!;
    const widgets = templateWidgets(template);
    const converted = convertTemplate(
      template,
      '',
      widgets.map((w) => defaultWidgetScope(w, ACCOUNTS))
    );

    const byTitle = new Map(converted.definition.panels.map((p) => [p.title, p]));
    const promql = converted.definition.panels.find((p) => p.datasource === 'metrics')!;
    const findings = converted.definition.panels.find((p) => p.datasource === 'nudgebee')!;

    expect(byTitle.size).toBe(converted.definition.panels.length);
    expect(promql).toMatchObject({ account_type: 'K8S' });
    // The findings panel here is a cost table, so it spans both cloud providers.
    expect(findings.account_type).toBeUndefined();
    expect(findings.account_ids).toEqual(['a1', 'g1']);
  });

  it('honours a hand-picked scope over the derived one', () => {
    const template = DASHBOARD_TEMPLATES.find((t) => t.id === 'executive-overview')!;
    const widgets = templateWidgets(template);
    // Everything onto one AWS account, which is what a row-by-row override does.
    const scopes = widgets.map(() => ({ account_type: undefined, account_ids: ['a1'] }));
    const converted = convertTemplate(template, '', scopes);

    expect(converted.warnings).toEqual([]);
    for (const panel of converted.definition.panels) {
      expect(panel.account_ids).toEqual(['a1']);
    }
  });

  it('hands out a document that shares no query object with the library', () => {
    // The spread in buildTemplateDocument is shallow, so without a deep copy the
    // object hanging off a module-level constant escapes to the caller and the
    // library is corrupted for the rest of the session.
    const template = DASHBOARD_TEMPLATES.find((t) => templateWidgets(t).some((w) => w.panel.targets?.[0]?.query))!;
    const document = buildTemplateDocument(template);
    const panel = document.definition.panels.find((p: any) => p.targets?.[0]?.query)!;

    (panel as any).targets[0].query.table = 'mutated';
    expect(buildTemplateDocument(template).definition.panels.some((p: any) => p.targets?.[0]?.query?.table === 'mutated')).toBe(false);
  });

  it('keeps scopes aligned with panels when a widget id does not resolve', () => {
    // `templateWidgets` drops what it cannot resolve, so a caller's scopes array
    // is indexed by RESOLVED panel. Indexing the raw panel list instead shifts
    // every scope after the gap onto the wrong panel and leaves the last one
    // unscoped — which the server rejects.
    const template = {
      id: 'test-gap',
      title: 'Gap',
      description: '',
      roles: [] as any,
      panels: [{ widget: 'savings-by-rule' }, { widget: 'no-such-widget' }, { widget: 'cluster-cpu-utilisation' }],
    };
    const scopes = [
      { account_type: undefined, account_ids: ['first'] },
      { account_type: undefined, account_ids: ['second'] },
    ];

    const panels = buildTemplateDocument(template, '', scopes).definition.panels as any[];

    expect(panels).toHaveLength(2);
    expect(panels[0].title).toBe('Savings opportunity by rule');
    expect(panels[0].account_ids).toEqual(['first']);
    // The panel AFTER the gap takes the second scope, not the third slot.
    expect(panels[1].title).toBe('Cluster CPU utilisation');
    expect(panels[1].account_ids).toEqual(['second']);
    // Ids stay unique and contiguous over the resolved panels.
    expect(panels.map((p) => p.id)).toEqual([1, 2]);
  });

  it('takes an overridden title, and falls back to the template’s own', () => {
    const template = DASHBOARD_TEMPLATES[0];
    expect(buildTemplateDocument(template, ' Q3 review ').title).toBe('Q3 review');
    expect(buildTemplateDocument(template, '   ').title).toBe(template.title);
  });
});
