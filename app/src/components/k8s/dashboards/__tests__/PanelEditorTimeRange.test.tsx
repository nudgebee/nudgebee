import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

/*
 * "Use the dashboard's time range" is stored beside a table panel's query, as
 * the target's time column. The editor used to rebuild its switch from the
 * table's default instead, so a panel saved with the range off opened with it
 * on — and the next edit anywhere in the query wrote the default back.
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

/** The library's Node inventory, as stored: 25 rows and whatever time column it was saved with. */
const nodesPanel = (timeColumn?: string): Panel =>
  ({
    id: 1,
    title: 'Node inventory',
    type: 'table',
    datasource: 'nudgebee',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 12, h: 10 },
    targets: [
      {
        ref_id: 'A',
        query: buildEntityQuery({ ...defaultDraft('k8s_nodes_v2'), limit: 25 }),
        ...(timeColumn === undefined ? {} : { time_column: timeColumn }),
      },
    ],
  } as unknown as Panel);

const renderEditor = (p: Panel) => {
  const onSave = jest.fn();
  render(<PanelEditorModal open panel={p} isEdit accountOptions={accounts} onClose={jest.fn()} onSave={onSave} />);
  return onSave;
};

const rangeSwitch = () => document.querySelector('.MuiSwitch-input') as HTMLInputElement;
const setRows = (rows: string) => fireEvent.change(screen.getByDisplayValue('25'), { target: { value: rows } });
const save = () =>
  act(async () => {
    fireEvent.click(screen.getByTestId('panel-save-btn'));
  });
const saved = (onSave: jest.Mock) => onSave.mock.calls[0][0].targets[0];

describe('PanelEditorModal — a table panel keeps its time-range setting', () => {
  it('opens a panel saved with the range off with the switch off', () => {
    renderEditor(nodesPanel());
    expect(rangeSwitch().checked).toBe(false);
  });

  it('leaves the range off when something else in the query changes', async () => {
    const onSave = renderEditor(nodesPanel());
    setRows('30');
    await save();

    expect(saved(onSave).query.limit).toBe(30);
    expect(saved(onSave).time_column || '').toBe('');
  });

  it('keeps the column a panel was saved on, rather than the table’s first', async () => {
    const onSave = renderEditor(nodesPanel('updated_at'));
    expect(rangeSwitch().checked).toBe(true);
    setRows('30');
    await save();

    expect(saved(onSave).time_column).toBe('updated_at');
  });

  it('starts a new panel on the table default rather than on off', async () => {
    // Nothing is stored for a panel that has no query yet, so there is no setting to restore.
    const unwritten = { ...nodesPanel(), targets: [{ ref_id: 'A' }] } as unknown as Panel;
    renderEditor(unwritten);
    expect(rangeSwitch().checked).toBe(true);
  });

  it('starts a blank panel switched onto the Nudgebee datasource with the range on', async () => {
    const blank = {
      ...nodesPanel(),
      title: 'New',
      type: 'timeseries',
      datasource: 'metrics',
      targets: [{ ref_id: 'A', expr: '' }],
    } as unknown as Panel;
    const onSave = renderEditor(blank);
    fireEvent.change(screen.getByTestId('panel-datasource-select'), { target: { value: 'nudgebee' } });
    expect(rangeSwitch().checked).toBe(true);
    await save();

    expect(saved(onSave).time_column).toBe('starts_at');
  });

  it('still switches the range on when the author asks for it', async () => {
    const onSave = renderEditor(nodesPanel());
    fireEvent.click(rangeSwitch());
    await save();

    expect(saved(onSave).time_column).toBe('node_creation_time');
  });
});
