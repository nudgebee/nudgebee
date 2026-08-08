import { PANEL_TEMPLATES, panelFromTemplate } from '../panelTemplates';
import { buildTemplateDocument, DASHBOARD_TEMPLATES, templateVariableDefaults, templateWidgets, variablesUsedBy } from '../dashboardTemplates';
import { convertNativeDashboard } from '../nativeImport';
import { draftFromQuery, findTable } from '../entityQuery';
import { referencedVariables } from '../templating';

/**
 * The catalogue is data, so these are the checks a type cannot make: that every
 * widget a dashboard names exists, that every widget survives the importer the
 * gallery runs it through, and that no query references a variable nobody
 * declares — which would ship a dashboard querying the literal text `$namespace`.
 */

const SCOPE = { account_type: 'K8S', account_ids: [] };

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

  it('copies a widget without an account, and without sharing state with the library', () => {
    const template = PANEL_TEMPLATES.find((t) => t.panel.datasource === 'nudgebee')!;
    const copy = panelFromTemplate(template, [{ id: 7 } as any]);

    expect(copy.id).toBe(8);
    expect(copy.account_type).toBeUndefined();
    expect(copy.account_ids).toEqual([]);

    (copy.targets![0].query as any).table = 'mutated';
    expect((template.panel.targets![0].query as any).table).not.toBe('mutated');
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

  it('declares every variable its panels reference', () => {
    for (const template of DASHBOARD_TEMPLATES) {
      const declared = new Set((template.variables || []).map((v) => v.name));
      for (const used of variablesUsedBy(template)) {
        expect(declared.has(used)).toBe(true);
      }
    }
  });

  it('converts every panel through the importer, with no warnings', () => {
    for (const template of DASHBOARD_TEMPLATES) {
      const document = buildTemplateDocument(template, templateVariableDefaults(template));
      const converted = convertNativeDashboard(document, SCOPE);

      expect(converted.warnings).toEqual([]);
      expect(converted.definition.panels).toHaveLength(template.panels.length);
      expect(converted.title).toBe(template.title);
      for (const panel of converted.definition.panels) {
        expect(panel.account_type).toBe('K8S');
        // Nothing may reach the server still holding a template token.
        expect(referencedVariables(panel.targets?.[0]?.expr || '')).toEqual([]);
      }
    }
  });

  it('substitutes variable values into the queries', () => {
    const template = DASHBOARD_TEMPLATES.find((t) => (t.variables || []).some((v) => v.name === 'namespace'))!;
    const document = buildTemplateDocument(template, { ...templateVariableDefaults(template), namespace: 'payments' });
    const exprs = document.definition.panels.map((p: any) => p.targets?.[0]?.expr || '').join(' ');

    expect(exprs).toContain('payments');
    expect(exprs).not.toContain('$namespace');
  });

  it('hands out a document that shares no query object with the library', () => {
    // The spread in buildTemplateDocument is shallow, so without a deep copy the
    // object hanging off a module-level constant escapes to the caller and the
    // library is corrupted for the rest of the session.
    const template = DASHBOARD_TEMPLATES.find((t) => templateWidgets(t).some((w) => w.panel.targets?.[0]?.query))!;
    const document = buildTemplateDocument(template, templateVariableDefaults(template));
    const panel = document.definition.panels.find((p: any) => p.targets?.[0]?.query)!;

    (panel as any).targets[0].query.table = 'mutated';
    expect(
      buildTemplateDocument(template, templateVariableDefaults(template)).definition.panels.some(
        (p: any) => p.targets?.[0]?.query?.table === 'mutated'
      )
    ).toBe(false);
  });

  it('takes an overridden title, and falls back to the template’s own', () => {
    const template = DASHBOARD_TEMPLATES[0];
    expect(buildTemplateDocument(template, {}, ' Q3 review ').title).toBe('Q3 review');
    expect(buildTemplateDocument(template, {}, '   ').title).toBe(template.title);
  });
});
