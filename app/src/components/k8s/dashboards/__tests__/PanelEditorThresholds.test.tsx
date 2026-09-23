import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel, PanelThresholdStep } from '@api1/dashboards';

/*
 * Thresholds are authored on the two visualisations that show one number, and
 * stored on `options` — which the server keeps as an opaque map, so the editor
 * is the only thing standing between an author and a step nothing can draw.
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

const panel = (type: string, thresholds?: PanelThresholdStep[]): Panel =>
  ({
    id: 1,
    title: 'CPU used',
    type,
    datasource: 'metrics',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 4 },
    targets: [{ ref_id: 'A', expr: 'sum(cpu)' }],
    ...(thresholds ? { options: { thresholds } } : {}),
  } as unknown as Panel);

const renderEditor = (p: Panel) => {
  const onSave = jest.fn();
  render(<PanelEditorModal open panel={p} isEdit accountOptions={accounts} onClose={jest.fn()} onSave={onSave} />);
  return onSave;
};

const saved = (onSave: jest.Mock): Panel => onSave.mock.calls[0][0];

const save = () =>
  act(async () => {
    fireEvent.click(screen.getByTestId('panel-save-btn'));
  });

describe('PanelEditorModal — thresholds', () => {
  it('offers them on the visualisations that show one number, and nowhere else', () => {
    renderEditor(panel('stat'));
    expect(screen.getByText('Thresholds')).toBeInTheDocument();
  });

  it('is not offered on a chart, which has a value per series', () => {
    renderEditor(panel('timeseries'));
    expect(screen.queryByText('Thresholds')).toBeNull();
  });

  it('holds Save until a new step has a number, then stores it', async () => {
    const onSave = renderEditor(panel('gauge'));

    fireEvent.click(screen.getByRole('button', { name: 'Add threshold' }));
    // An empty step would save as config that draws nothing.
    expect(screen.getByTestId('panel-save-btn')).toBeDisabled();
    expect(screen.getByText(/Every threshold needs a number/)).toBeInTheDocument();

    fireEvent.change(screen.getByTestId('threshold-value-0'), { target: { value: '80' } });
    expect(screen.getByTestId('panel-save-btn')).not.toBeDisabled();

    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red' }]);
  });

  it('forgets the list entirely when its last step is removed', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'red' }]));

    fireEvent.click(screen.getByRole('button', { name: 'Remove threshold 1' }));
    await save();

    // Not `[]` — a panel that configures no threshold should read as one that
    // has never heard of them.
    expect(saved(onSave).options).not.toHaveProperty('thresholds');
  });

  it('keeps each step separate while one is being edited', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'amber' }]));

    fireEvent.click(screen.getByRole('button', { name: 'Add threshold' }));
    fireEvent.change(screen.getByTestId('threshold-value-1'), { target: { value: '90' } });
    await save();

    expect(saved(onSave).options?.thresholds).toEqual([
      { value: 80, color: 'amber' },
      { value: 90, color: 'red' },
    ]);
  });

  /*
   * Kept, the steps would be config nothing renders — and would colour the panel
   * again if it were ever switched back, long after the numbers were forgotten.
   */
  it('drops the steps when the panel becomes something that cannot show them', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'red' }]));

    // The visualisation picker. Its options are queried by text: the popup is
    // a portalled MUI menu, which testing-library reads as hidden, so
    // `getByRole('option')` finds nothing there.
    fireEvent.click(screen.getByRole('button', { name: /^Stat/ }));
    fireEvent.click(await screen.findByText('Time series'));

    expect(screen.queryByText('Thresholds')).toBeNull();
    await save();
    expect(saved(onSave).options).not.toHaveProperty('thresholds');
  });

  /*
   * The import flow passes a pasted dashboard's `options` through verbatim, so a
   * hand-edited or corrupted file can put a `null` in the list. Reading `.value`
   * off it threw, and the throw took the modal down BEFORE the row's Remove
   * button rendered — leaving the author with a panel they could not open to fix.
   */
  it('opens on a list an import filled with junk, instead of crashing', async () => {
    const onSave = renderEditor(panel('stat', [null, { value: 80, color: 'red' }, 'red'] as unknown as PanelThresholdStep[]));

    expect(screen.getByText('Thresholds')).toBeInTheDocument();
    expect(screen.getByTestId('threshold-value-0')).toHaveValue(80);
    expect(screen.queryByTestId('threshold-value-1')).toBeNull();

    // And the junk is gone the moment the list is touched at all.
    fireEvent.change(screen.getByTestId('threshold-value-0'), { target: { value: '85' } });
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 85, color: 'red' }]);
  });

  /*
   * The field holds the TEXT typed, not text re-derived from the parsed number.
   * Deriving it rewrote the input mid-entry, and a type='number' input drops the
   * partial value it was holding when that happens — in Chromium, typing 1.05
   * landed as 15 and -0.5 as 0.5. jsdom does not sanitise a number input, so it
   * cannot reproduce the drop; what it CAN pin is the cause, which is whether
   * the field's text survives a value that formats differently.
   */
  it('leaves the number as typed rather than reformatting it mid-entry', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'red' }]));

    const field = screen.getByTestId('threshold-value-0') as HTMLInputElement;
    fireEvent.change(field, { target: { value: '1.50' } });
    // Not '1.5': reformatting here is exactly what truncated the entry.
    expect(field.value).toBe('1.50');

    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 1.5, color: 'red' }]);
  });

  it('re-seeds a field when the row it belongs to changes underneath it', () => {
    renderEditor(
      panel('stat', [
        { value: 80, color: 'red' },
        { value: 90, color: 'amber' },
      ])
    );

    expect((screen.getByTestId('threshold-value-0') as HTMLInputElement).value).toBe('80');
    fireEvent.click(screen.getByRole('button', { name: 'Remove threshold 1' }));
    // Row 0 is now the step that was row 1 — the field has to follow it.
    expect((screen.getByTestId('threshold-value-0') as HTMLInputElement).value).toBe('90');
    expect(screen.queryByTestId('threshold-value-1')).toBeNull();
  });

  it('holds Save for a step whose colour nothing can draw', () => {
    renderEditor(panel('stat', [{ value: 80, color: 'chartreuse' }] as unknown as PanelThresholdStep[]));

    expect(screen.getByTestId('panel-save-btn')).toBeDisabled();
  });

  it('clearing a value blocks Save rather than quietly reading as zero', () => {
    renderEditor(panel('stat', [{ value: 80, color: 'red' }]));

    expect(screen.getByTestId('panel-save-btn')).not.toBeDisabled();
    fireEvent.change(screen.getByTestId('threshold-value-0'), { target: { value: '' } });
    expect(screen.getByTestId('panel-save-btn')).toBeDisabled();
    expect(screen.getByTestId('threshold-value-0')).toHaveValue(null);
  });
});
