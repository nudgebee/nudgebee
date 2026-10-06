import React from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * A stat or gauge over several accounts shows ONE number, and whether it is
 * the accounts' sum or their average is a choice the card cannot show. A gauge
 * averages by default — its dial reads as a percentage — and a stat adds up;
 * the editor lets the author say otherwise, and only where there are accounts
 * to combine.
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

const accounts = [
  { value: 'acc-1', label: 'k8s-dev', cloud_provider: 'K8s' },
  { value: 'acc-2', label: 'k8s-prod', cloud_provider: 'K8s' },
] as AccountOption[];

const metricsPanel = (type: string, account_ids: string[], options?: Record<string, unknown>): Panel =>
  ({
    id: 1,
    title: 'CPU used',
    type,
    datasource: 'metrics',
    account_ids,
    grid_pos: { x: 0, y: 0, w: 6, h: 4 },
    targets: [{ ref_id: 'A', expr: 'avg(cpu)' }],
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

/** The select's trigger; its text is the chosen option's label. */
const combineField = () => document.getElementById('panel-combine-select');
const chosen = () => combineField()?.textContent;
const pick = async (option: string) => {
  fireEvent.click(combineField() as HTMLElement);
  fireEvent.click(within(await screen.findByRole('listbox', { hidden: true })).getByText(option));
};

describe('PanelEditorModal — across accounts', () => {
  it('offers a multi-account gauge its default, the average', () => {
    renderEditor(metricsPanel('gauge', ['acc-1', 'acc-2']));
    expect(chosen()).toBe('Average');
  });

  it('offers a multi-account stat its default, the sum', () => {
    renderEditor(metricsPanel('stat', ['acc-1', 'acc-2']));
    expect(chosen()).toBe('Sum');
  });

  it('shows the choice the panel was saved with', () => {
    renderEditor(metricsPanel('gauge', ['acc-1', 'acc-2'], { combine: 'sum' }));
    expect(chosen()).toBe('Sum');
  });

  it('saves the choice beside the other options, and leaves them as they were', async () => {
    const onSave = renderEditor(metricsPanel('stat', ['acc-1', 'acc-2'], { thresholds: [{ value: 80, color: 'red' }] }));
    await pick('Average');
    await save();
    expect(onSave.mock.calls[0][0].options).toEqual({ thresholds: [{ value: 80, color: 'red' }], combine: 'avg' });
  });

  it('writes nothing for a panel whose choice was never touched', async () => {
    const onSave = renderEditor(metricsPanel('gauge', ['acc-1', 'acc-2']));
    await save();
    expect(onSave.mock.calls[0][0].options?.combine).toBeUndefined();
  });

  it('is not offered on a single-account panel, where sum and average are the same number', () => {
    renderEditor(metricsPanel('gauge', ['acc-1']));
    expect(combineField()).toBeNull();
  });

  it('stays changeable on a single-account panel that already carries a choice', () => {
    renderEditor(metricsPanel('gauge', ['acc-1'], { combine: 'sum' }));
    expect(chosen()).toBe('Sum');
  });

  it('is dropped with the thresholds when the panel stops being a stat or gauge', async () => {
    const onSave = renderEditor(metricsPanel('gauge', ['acc-1', 'acc-2'], { combine: 'sum' }));
    fireEvent.click(screen.getByRole('button', { name: /^Gauge/ }));
    fireEvent.click(await screen.findByText('Time series'));
    await save();
    expect(onSave.mock.calls[0][0].options?.combine).toBeUndefined();
  });

  it('is not offered on a time series, which draws every account as its own line', () => {
    renderEditor(metricsPanel('timeseries', ['acc-1', 'acc-2']));
    expect(combineField()).toBeNull();
  });
});
