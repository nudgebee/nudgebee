import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * Switching a panel onto the Nudgebee datasource starts it on the first table
 * the author may read. When that is a table of standing state — open
 * recommendations — the panel must start with the dashboard's time range off,
 * in what is SAVED as well as in what the switch shows.
 */

const readable = jest.fn((tables: { value: string }[]) => tables);

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
  queryableTables: (tables: { value: string }[]) => readable(tables),
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

const accounts = [{ value: 'acc-1', label: 'aws-prod', cloud_provider: 'AWS' }] as AccountOption[];

const metricsPanel = {
  id: 1,
  title: 'Savings',
  type: 'timeseries',
  datasource: 'metrics',
  account_ids: ['acc-1'],
  grid_pos: { x: 0, y: 0, w: 6, h: 8 },
  targets: [{ ref_id: 'A', expr: 'up' }],
} as unknown as Panel;

async function switchToNudgebeeAndSave() {
  const onSave = jest.fn();
  render(<PanelEditorModal open panel={metricsPanel} isEdit accountOptions={accounts} onClose={jest.fn()} onSave={onSave} />);
  fireEvent.change(screen.getByTestId('panel-datasource-select'), { target: { value: 'nudgebee' } });
  await act(async () => {
    fireEvent.click(screen.getByTestId('panel-save-btn'));
  });
  return onSave.mock.calls[0][0].targets[0];
}

describe('PanelEditorModal — a new Nudgebee panel and the time range', () => {
  afterEach(() => readable.mockImplementation((tables) => tables));

  it('saves no time column when the panel starts on open recommendations', async () => {
    // A custom-role author who can read recommendations and nothing before them.
    readable.mockImplementation((tables) => tables.filter((t) => t.value === 'recommendations_v2'));
    const target = await switchToNudgebeeAndSave();

    expect(target.query.table).toBe('recommendations_v2');
    expect(target.time_column).toBe('');
    expect((document.querySelector('.MuiSwitch-input') as HTMLInputElement).checked).toBe(false);
  });

  it('still follows the time range when the panel starts on events', async () => {
    const target = await switchToNudgebeeAndSave();

    expect(target.query.table).toBe('events_v2');
    expect(target.time_column).toBe('starts_at');
  });
});
