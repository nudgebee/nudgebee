import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

/*
 * The author picks which columns a traces or Nudgebee panel's "Filter by column"
 * menu offers. Saved on `options.filter_columns`; none chosen saves no list,
 * which offers every column.
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

// A native <select> stands in for the DS one: its own behaviour is its own tests' business.
jest.mock('@ui/Select', () => ({
  __esModule: true,
  Select: (props: {
    id?: string;
    multiple?: boolean;
    value: unknown;
    options?: { label: string; value: string }[];
    onChange: (v: unknown) => void;
  }) => (
    <select
      data-testid={props.id || `select-${(props.options || []).map((o) => o.value).join('|')}`}
      multiple={props.multiple}
      value={(props.multiple ? props.value || [] : props.value ?? '') as never}
      onChange={(e) => props.onChange(props.multiple ? [...e.target.selectedOptions].map((o) => o.value) : e.target.value)}
    >
      {(props.options || []).map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

const accounts = [{ value: 'acc-1', label: 'k8s-dev', cloud_provider: 'K8s' }] as AccountOption[];

const tracesPanel = (options?: Record<string, unknown>): Panel =>
  ({
    id: 1,
    title: 'Slow spans',
    type: 'table',
    datasource: 'traces',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', query: buildEntityQuery(defaultDraft('traces_v2')) }],
    ...(options ? { options } : {}),
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

const pick = (select: HTMLElement, values: string[]) => {
  for (const option of [...(select as HTMLSelectElement).options]) option.selected = values.includes(option.value);
  fireEvent.change(select);
};

describe('PanelEditorModal — columns viewers can filter by', () => {
  it('offers the table’s text columns and saves the ones picked', async () => {
    const onSave = renderEditor(tracesPanel());
    const field = screen.getByTestId('panel-filter-columns') as HTMLSelectElement;
    const offered = [...field.options].map((o) => o.value);
    expect(offered).toEqual(expect.arrayContaining(['workload_name', 'workload_namespace', 'span_name']));
    // A duration is a range question, which a list of values cannot ask.
    expect(offered).not.toContain('duration_ns');

    pick(field, ['workload_name', 'workload_namespace']);
    await save();
    expect(onSave.mock.calls[0][0].options.filter_columns).toEqual(['workload_name', 'workload_namespace']);
  });

  it('saves no list when none are picked, which offers every column', async () => {
    const onSave = renderEditor(tracesPanel({ filter_columns: ['workload_name'] }));
    const field = screen.getByTestId('panel-filter-columns') as HTMLSelectElement;
    expect([...field.selectedOptions].map((o) => o.value)).toEqual(['workload_name']);

    pick(field, []);
    await save();
    expect(onSave.mock.calls[0][0].options?.filter_columns).toBeUndefined();
  });

  it('drops a picked column the newly chosen table cannot filter by', async () => {
    // destination_name exists on the span table only; the grouping table has no such column.
    const onSave = renderEditor(tracesPanel({ filter_columns: ['destination_name', 'workload_name'] }));
    const table = screen.getAllByRole('combobox').find((s) => [...(s as HTMLSelectElement).options].some((o) => o.value === 'traces_groupings_v2'));
    fireEvent.change(table!, { target: { value: 'traces_groupings_v2' } });
    await save();
    expect(onSave.mock.calls[0][0].options.filter_columns).toEqual(['workload_name']);
  });

  it('shows only the saved names the table offers', () => {
    // An imported panel can carry a name its table does not have; the menu ignores it, so the editor does too.
    renderEditor(tracesPanel({ filter_columns: ['no_such_column', 'workload_name'] }));
    const field = screen.getByTestId('panel-filter-columns') as HTMLSelectElement;
    expect([...field.selectedOptions].map((o) => o.value)).toEqual(['workload_name']);
  });

  it('is not offered on a metrics panel', () => {
    renderEditor({ ...tracesPanel(), datasource: 'metrics', type: 'timeseries', targets: [{ ref_id: 'A', expr: 'up' }] } as Panel);
    expect(screen.queryByTestId('panel-filter-columns')).toBeNull();
  });
});
