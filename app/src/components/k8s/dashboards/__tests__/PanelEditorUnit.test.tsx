import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

/*
 * A panel's unit is what follows every number it draws — "45 %", "12 pods". It
 * used to arrive only with a template, an import or a JSON edit, so the editor
 * showed a panel whose numbers carried a suffix it had no field for.
 */

jest.mock('../PanelPreview', () => ({
  __esModule: true,
  default: () => <div data-testid='panel-preview' />,
  PREVIEW_RAIL_WIDTH: 400,
  usePreviewRange: () => ({ start: 0, end: 1 }),
}));

jest.mock('../PanelProviderRow', () => ({ __esModule: true, default: () => null }));

jest.mock('../panelProviders', () => ({
  ...jest.requireActual('../panelProviders'),
  usePanelProviders: () => ({ loading: false, entries: [], total: 0 }),
  useEsIndexes: () => ({ loading: false, indexes: [] }),
}));

jest.mock('../panelAccess', () => ({
  ...jest.requireActual('../panelAccess'),
  missingDatasourceGrant: () => null,
  missingTableGrant: () => null,
  grantTooltip: () => '',
  queryableTables: (tables: unknown[]) => tables,
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => ({ title: 'Brand' }),
  fillBrandTokens: (s: string) => s,
}));

const accounts = [{ value: 'acc-1', label: 'k8s-dev', cloud_provider: 'K8s' }] as AccountOption[];

const metricsPanel = (type: string, unit?: string): Panel =>
  ({
    id: 1,
    title: 'CPU used',
    type,
    datasource: 'metrics',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 4 },
    targets: [{ ref_id: 'A', expr: 'sum(cpu)' }],
    ...(unit === undefined ? {} : { unit }),
  } as unknown as Panel);

const renderEditor = (p: Panel) => {
  const onSave = jest.fn();
  render(<PanelEditorModal open panel={p} isEdit accountOptions={accounts} onClose={jest.fn()} onSave={onSave} />);
  return onSave;
};

const save = () =>
  act(async () => {
    fireEvent.click(screen.getByTestId('panel-save-btn'));
  });

const unitField = () => document.getElementById('panel-unit-input') as HTMLInputElement | null;

describe('PanelEditorModal — unit', () => {
  it('shows the unit the panel already draws with', () => {
    renderEditor(metricsPanel('stat', '%'));
    expect(unitField()?.value).toBe('%');
  });

  it('lets a panel that has none be given one', async () => {
    const onSave = renderEditor(metricsPanel('stat'));
    expect(unitField()?.value).toBe('');

    fireEvent.change(unitField() as HTMLInputElement, { target: { value: 'req/s' } });
    await save();
    expect(onSave.mock.calls[0][0].unit).toBe('req/s');
  });

  it('saves the unit without the spaces around it', async () => {
    const onSave = renderEditor(metricsPanel('timeseries', 'ms'));
    fireEvent.change(unitField() as HTMLInputElement, { target: { value: '  pods ' } });
    await save();
    expect(onSave.mock.calls[0][0].unit).toBe('pods');
  });

  it('can take the unit away', async () => {
    const onSave = renderEditor(metricsPanel('gauge', '%'));
    fireEvent.change(unitField() as HTMLInputElement, { target: { value: '' } });
    await save();
    expect(onSave.mock.calls[0][0].unit).toBe('');
  });

  it('leaves a panel with no unit as it was when the field is not touched', async () => {
    const onSave = renderEditor(metricsPanel('stat'));
    await save();
    expect('unit' in onSave.mock.calls[0][0]).toBe(false);
  });

  it('is not offered on a bar chart, which draws no unit', () => {
    renderEditor(metricsPanel('bar', '%'));
    expect(unitField()).toBeNull();
  });

  it('is not offered on a text panel, which draws no number', () => {
    // A text panel keeps `metrics` as its datasource, so the type has to rule it out.
    renderEditor({ ...metricsPanel('text', '%'), content: 'Runbook: restart the pod.' } as unknown as Panel);
    expect(unitField()).toBeNull();
  });

  it('is not offered where nothing reads it', () => {
    // A traces panel draws a table of the query's own columns.
    renderEditor({
      ...metricsPanel('table'),
      datasource: 'traces',
      targets: [{ ref_id: 'A', query: buildEntityQuery(defaultDraft('traces_v2')) }],
    } as unknown as Panel);
    expect(unitField()).toBeNull();
  });
});
