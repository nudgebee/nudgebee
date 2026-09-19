import React from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * A metrics panel runs every one of its queries, but the editor used to show
 * and write only the first — so an imported "started / completed" panel read as
 * one query, and any edit to it saved the panel with the second one gone.
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
  missingDatasourceGrant: () => null,
  grantTooltip: () => '',
  queryableTables: (tables: unknown[]) => tables,
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => ({ title: 'Brand' }),
  fillBrandTokens: (s: string) => s,
}));

const accounts = [{ value: 'acc-1', label: 'k8s-dev', cloud_provider: 'K8s' }] as AccountOption[];

const panel = (targets: Panel['targets']): Panel =>
  ({
    id: 1,
    title: 'Jobs Started / Completed per Minute',
    type: 'timeseries',
    datasource: 'metrics',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 4 },
    targets,
  } as Panel);

const STARTED = { ref_id: 'started', legend_format: 'started', expr: 'sum(increase(gha_started_jobs_total[15m])) / 15' };
const COMPLETED = { ref_id: 'completed', legend_format: 'completed', expr: 'sum(increase(gha_completed_jobs_total[15m])) / 15' };

const renderEditor = (p: Panel) => {
  const onSave = jest.fn();
  render(<PanelEditorModal open panel={p} isEdit accountOptions={accounts} onClose={jest.fn()} onSave={onSave} />);
  return onSave;
};

const saved = (onSave: jest.Mock): Panel => onSave.mock.calls[0][0];

// The save settles its loader after the parent's promise resolves.
const save = () =>
  act(async () => {
    fireEvent.click(screen.getByTestId('panel-save-btn'));
  });

describe('PanelEditorModal — metrics panel with several queries', () => {
  it('shows every query with its legend', () => {
    renderEditor(panel([STARTED, COMPLETED]));

    expect(screen.getAllByTestId('panel-query-row')).toHaveLength(2);
    expect(screen.getByLabelText(/^Query 1/)).toHaveValue(STARTED.expr);
    expect(screen.getByLabelText(/^Query 2/)).toHaveValue(COMPLETED.expr);
    const legends = screen.getAllByLabelText('Legend');
    expect(legends.map((l) => (l as HTMLInputElement).value)).toEqual(['started', 'completed']);
  });

  it('keeps the second query when the first is edited', async () => {
    const onSave = renderEditor(panel([STARTED, COMPLETED]));

    fireEvent.change(screen.getByLabelText(/^Query 1/), { target: { value: 'sum(gha_started_jobs_total)' } });
    await save();

    expect(saved(onSave).targets).toEqual([{ ...STARTED, expr: 'sum(gha_started_jobs_total)' }, COMPLETED]);
  });

  it('saves an edit to the second query and its legend', async () => {
    const onSave = renderEditor(panel([STARTED, COMPLETED]));

    fireEvent.change(screen.getByLabelText(/^Query 2/), { target: { value: 'sum(gha_completed_jobs_total)' } });
    fireEvent.change(screen.getAllByLabelText('Legend')[1], { target: { value: 'done' } });
    await save();

    expect(saved(onSave).targets).toEqual([STARTED, { ...COMPLETED, expr: 'sum(gha_completed_jobs_total)', legend_format: 'done' }]);
  });

  it('removes a query', async () => {
    const onSave = renderEditor(panel([STARTED, COMPLETED]));

    fireEvent.click(screen.getByRole('button', { name: 'Remove query 1' }));

    // Back to one: the plain field, no legend and nothing to remove.
    expect(screen.getAllByTestId('panel-query-row')).toHaveLength(1);
    expect(screen.getByLabelText(/^Query/)).toHaveValue(COMPLETED.expr);
    expect(screen.queryByLabelText('Legend')).toBeNull();
    expect(screen.queryByRole('button', { name: /Remove query/ })).toBeNull();

    await save();
    expect(saved(onSave).targets).toEqual([COMPLETED]);
  });
});

describe('PanelEditorModal — adding a query', () => {
  it('adds a query under the next free ref id and holds Save until it is filled in', async () => {
    const onSave = renderEditor(panel([{ ref_id: 'A', expr: 'up' }]));

    // A single query is the plain field it always was.
    expect(screen.getByLabelText(/^Query/)).toHaveValue('up');
    expect(screen.queryByLabelText('Legend')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Add query' }));

    const rows = screen.getAllByTestId('panel-query-row');
    expect(rows).toHaveLength(2);
    expect(within(rows[1]).getByLabelText(/^Query 2/)).toHaveValue('');
    // The server refuses a panel with an empty query anywhere in it.
    expect(screen.getByTestId('panel-save-btn')).toBeDisabled();

    fireEvent.change(within(rows[1]).getByLabelText(/^Query 2/), { target: { value: 'down' } });
    fireEvent.change(within(rows[1]).getByLabelText('Legend'), { target: { value: 'down' } });
    await save();

    expect(saved(onSave).targets).toEqual([
      { ref_id: 'A', expr: 'up' },
      { ref_id: 'B', expr: 'down', legend_format: 'down' },
    ]);
  });
});
