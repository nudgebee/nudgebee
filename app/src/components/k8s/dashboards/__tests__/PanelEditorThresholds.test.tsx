import React from 'react';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
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

  it('is offered on a chart too, with a hold per step, and not on a table', () => {
    renderEditor(panel('timeseries'));
    expect(screen.getByText('Thresholds')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Add threshold'));
    expect(screen.getByTestId('threshold-for-0')).toBeInTheDocument();
    cleanup();
    renderEditor(panel('table'));
    expect(screen.queryByText('Thresholds')).toBeNull();
  });

  it('stores the hold in seconds and holds Save for one outside two minutes to an hour', async () => {
    const onSave = renderEditor(panel('timeseries', [{ value: 80, color: 'red' }]));
    fireEvent.change(screen.getByTestId('threshold-for-0'), { target: { value: '1' } });
    expect(screen.getByTestId('threshold-bad-hold')).toBeInTheDocument();
    await save();
    expect(onSave).not.toHaveBeenCalled();

    fireEvent.change(screen.getByTestId('threshold-for-0'), { target: { value: '5' } });
    expect(screen.queryByTestId('threshold-bad-hold')).toBeNull();
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red', for: 300 }]);
  });

  it('takes a hold in whole minutes', async () => {
    const onSave = renderEditor(panel('timeseries', [{ value: 80, color: 'red' }]));
    fireEvent.change(screen.getByTestId('threshold-for-0'), { target: { value: '2.5' } });
    expect(screen.getByTestId('threshold-bad-hold')).toHaveTextContent('whole number of minutes');
    await save();
    expect(onSave).not.toHaveBeenCalled();
  });

  it('clears an imported hold it cannot read, in one click', async () => {
    const onSave = renderEditor(panel('timeseries', [{ value: 80, color: 'red', for: '300' as unknown as number }]));
    expect(screen.getByTestId('threshold-bad-hold')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Clear this hold'));
    expect(screen.queryByTestId('threshold-bad-hold')).toBeNull();
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red' }]);
  });

  /*
   * A stat has no hold field, but JSON edit and import can still put one on a
   * step. It must neither block Save nor stay invisible.
   */
  it('flags a hold on a stat, saves anyway, and removes it on request', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'red', for: 59 }]));
    expect(screen.getByTestId('threshold-ignored-hold')).toBeInTheDocument();
    expect(screen.queryByTestId('threshold-bad-hold')).toBeNull();
    fireEvent.click(screen.getByText('Remove hold'));
    expect(screen.queryByTestId('threshold-ignored-hold')).toBeNull();
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red' }]);
  });

  it('names each column once above the rows, and flags a bad field on the field itself', () => {
    renderEditor(panel('timeseries', [{ value: 80, color: 'red' }]));
    for (const name of ['When', 'Value', 'Hold', '(min)', 'Colour']) expect(screen.getByText(name)).toBeInTheDocument();
    fireEvent.change(screen.getByTestId('threshold-for-0'), { target: { value: '1' } });
    expect(screen.getByText('2–60 min')).toBeInTheDocument();
    fireEvent.change(screen.getByTestId('threshold-for-0'), { target: { value: '2.5' } });
    expect(screen.getByText('Whole minutes')).toBeInTheDocument();
  });

  it('has no hold field on a stat, which judges one number', () => {
    renderEditor(panel('stat', [{ value: 80, color: 'red' }]));
    expect(screen.queryByTestId('threshold-for-0')).toBeNull();
  });

  it('flags a new step with no number only once Save is tried, then stores it', async () => {
    const onSave = renderEditor(panel('gauge'));

    fireEvent.click(screen.getByRole('button', { name: 'Add threshold' }));
    // Still being filled in: no error yet.
    expect(screen.queryByText('Enter a number')).toBeNull();

    // An empty step would save as config that draws nothing.
    await save();
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByText('Enter a number')).toBeInTheDocument();

    fireEvent.change(screen.getByTestId('threshold-value-0'), { target: { value: '80' } });
    expect(screen.queryByText('Enter a number')).toBeNull();

    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red' }]);
  });

  it('reads each step back as a sentence, with its hold when it has one', () => {
    renderEditor(
      panel('timeseries', [
        { value: 50000000, color: 'red', for: 300 },
        { value: 20000000, color: 'amber' },
      ])
    );
    expect(screen.getByTestId('threshold-sentence-0')).toHaveTextContent(
      'Turns the panel red when any line stays at or above 50,000,000 for 5 minutes in a row.'
    );
    expect(screen.getByTestId('threshold-sentence-1')).toHaveTextContent('Turns the panel amber as soon as any line is at or above 20,000,000.');
  });

  it('says nothing about a hold the field is flagging, rather than "as soon as"', () => {
    renderEditor(panel('timeseries', [{ value: 80, color: 'red', for: 60 }]));
    expect(screen.queryByTestId('threshold-sentence-0')).toBeNull();
  });

  it('names the account in the sentence when a chart step is scoped to one', () => {
    const two = [
      { value: 'acc-1', label: 'k8s-dev', cloud_provider: 'K8s' },
      { value: 'acc-2', label: 'k8s-prod', cloud_provider: 'K8s' },
    ] as AccountOption[];
    const p = {
      ...panel('timeseries', [{ value: 80, color: 'red', applies_to: 'account', account_id: 'acc-2' }]),
      account_ids: ['acc-1', 'acc-2'],
    } as unknown as Panel;
    render(<PanelEditorModal open panel={p} isEdit accountOptions={two} onClose={jest.fn()} onSave={jest.fn()} />);
    expect(screen.getByTestId('threshold-sentence-0')).toHaveTextContent('as soon as any k8s-prod line is at or above 80');
  });

  // Visible, not merely present: the closed picker hides the name, and that rule must not reach the list.
  it('names each colour in the open list, not only its swatch', async () => {
    renderEditor(panel('stat', [{ value: 80, color: 'red' }]));
    const trigger = document.getElementById('threshold-color-0') as HTMLElement;
    expect(within(trigger).getByText('Red')).not.toBeVisible();
    fireEvent.click(trigger);
    const list = await screen.findByRole('listbox', { hidden: true });
    for (const name of ['Red', 'Amber', 'Green', 'Blue']) expect(within(list).getByText(name)).toBeVisible();
  });

  it('spells out each comparison in the open list, and shows only the symbol when closed', async () => {
    renderEditor(panel('stat', [{ value: 80, color: 'red' }]));
    const trigger = document.getElementById('threshold-op-0') as HTMLElement;
    expect(within(trigger).getByText('at or above')).not.toBeVisible();
    fireEvent.click(trigger);
    const list = await screen.findByRole('listbox', { hidden: true });
    for (const phrase of ['at or above', 'above', 'at or below', 'below']) expect(within(list).getByText(phrase)).toBeVisible();
  });

  it('says nothing about a step that has no number yet', () => {
    renderEditor(panel('gauge'));
    fireEvent.click(screen.getByRole('button', { name: 'Add threshold' }));
    expect(screen.queryByTestId('threshold-sentence-0')).toBeNull();
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

    // A chart offers thresholds, but a stat's rule about one total is not a rule
    // about twenty lines, so the step does not carry across.
    expect(screen.getByText('Thresholds')).toBeInTheDocument();
    expect(screen.queryByText(/No thresholds/)).toBeNull();
    await save();
    expect(saved(onSave).options).not.toHaveProperty('thresholds');
  });

  it('keeps the steps between a stat and a gauge, which judge the same number', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'red' }]));
    fireEvent.click(screen.getByRole('button', { name: /^Stat/ }));
    fireEvent.click(await screen.findByText('Gauge'));
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red' }]);
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

  it('refuses to save a step whose colour nothing can draw', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'chartreuse' }] as unknown as PanelThresholdStep[]));

    await save();
    expect(onSave).not.toHaveBeenCalled();
  });

  it('clearing a value blocks Save rather than quietly reading as zero', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 80, color: 'red' }]));

    fireEvent.change(screen.getByTestId('threshold-value-0'), { target: { value: '' } });
    expect(screen.getByTestId('threshold-value-0')).toHaveValue(null);
    await save();
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByText('Enter a number')).toBeInTheDocument();
  });
});

describe('PanelEditorModal — what a threshold compares, and against which account', () => {
  const fleet = [
    { value: 'acc-1', label: 'prod-eu', cloud_provider: 'K8s' },
    { value: 'acc-2', label: 'prod-us', cloud_provider: 'K8s' },
    { value: 'acc-3', label: 'dev', cloud_provider: 'K8s' },
  ] as AccountOption[];

  const fleetPanel = (thresholds: PanelThresholdStep[]): Panel =>
    ({ ...panel('stat', thresholds), account_ids: ['acc-1', 'acc-2', 'acc-3'] } as unknown as Panel);

  const renderFleet = (thresholds: PanelThresholdStep[]) => {
    const onSave = jest.fn();
    render(<PanelEditorModal open panel={fleetPanel(thresholds)} isEdit accountOptions={fleet} onClose={jest.fn()} onSave={onSave} />);
    return onSave;
  };

  /** Opens a picker by its id and chooses an option by its text — the popup is a portalled menu. */
  const pick = async (id: string, option: string) => {
    fireEvent.click(document.getElementById(id) as HTMLElement);
    fireEvent.click(within(await screen.findByRole('listbox', { hidden: true })).getByText(option));
  };

  it('stores a comparison other than the default, and leaves the default unwritten', async () => {
    const onSave = renderEditor(panel('stat', [{ value: 2, color: 'red' }]));
    await pick('threshold-op-0', 'at or below');
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 2, color: 'red', op: 'lte' }]);
  });

  it('has no account picker on a single-account panel, where every subject is the same number', () => {
    renderEditor(panel('stat', [{ value: 80, color: 'red' }]));
    expect(document.getElementById('threshold-op-0')).not.toBeNull();
    expect(document.getElementById('threshold-applies-0')).toBeNull();
  });

  it('scopes a step to every account', async () => {
    const onSave = renderFleet([{ value: 80, color: 'red' }]);
    await pick('threshold-applies-0', 'Every account');
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red', applies_to: 'every' }]);
  });

  it('scopes a step to one of the panel’s accounts, by id', async () => {
    const onSave = renderFleet([{ value: 80, color: 'red' }]);
    await pick('threshold-applies-0', 'prod-us');
    await save();
    expect(saved(onSave).options?.thresholds).toEqual([{ value: 80, color: 'red', applies_to: 'account', account_id: 'acc-2' }]);
  });

  it('writes a step back to the shown number as a step that never had a subject', async () => {
    const onSave = renderFleet([{ value: 80, color: 'red', applies_to: 'account', account_id: 'acc-2' }]);
    await pick('threshold-applies-0', 'Shown number');
    await save();
    expect(JSON.parse(JSON.stringify(saved(onSave).options?.thresholds))).toEqual([{ value: 80, color: 'red' }]);
  });

  it('offers one "Account not available" however many steps name the missing account', () => {
    renderFleet([
      { value: 80, color: 'red', applies_to: 'account', account_id: 'acc-9' },
      { value: 90, color: 'amber', applies_to: 'account', account_id: 'acc-9' },
    ]);
    fireEvent.click(document.getElementById('threshold-applies-0') as HTMLElement);
    return screen.findByRole('listbox', { hidden: true }).then((list) => expect(within(list).getAllByText('Account not available')).toHaveLength(1));
  });

  it('spells out an overlap the author would not guess the outcome of, without holding Save', () => {
    renderFleet([
      { value: 80, color: 'red' },
      { value: 90, color: 'green', op: 'lte' },
    ]);
    expect(screen.getByTestId('threshold-overlaps')).toHaveTextContent('Steps 1 and 2 both apply from 80 to 90 — Red wins there');
    expect(screen.getByTestId('panel-save-btn')).not.toBeDisabled();
  });

  it('flags a step naming an account the panel no longer covers, without holding Save', () => {
    renderFleet([{ value: 80, color: 'red', applies_to: 'account', account_id: 'acc-9' }]);
    expect(screen.getByTestId('threshold-dangling')).toHaveTextContent('never fires');
    expect(screen.getByTestId('panel-save-btn')).not.toBeDisabled();
  });
});
