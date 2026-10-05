import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import type { Panel } from '@api1/dashboards';
import DashboardPanel from '../DashboardPanel';
import PanelAccountBreakdown, { breakdownPlacement } from '../PanelAccountBreakdown';
import type { StatRow } from '../panelSeries';

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

// The real dial loads through next/dynamic, which resolves after the test ends.
// This records the one thing the card decides about it: how many rows it makes room for.
jest.mock('../PanelGauge', () => ({
  __esModule: true,
  default: (props: { value?: number; rowsBelow?: number; caption?: string }) => (
    <div data-testid='dial' data-rows-below={props.rowsBelow ?? 0} data-value={props.value}>
      {props.caption}
    </div>
  ),
}));

const panel = (type: string, h: number) => ({ id: 1, title: 'p', type, datasource: 'metrics', grid_pos: { x: 0, y: 0, w: 4, h } } as Panel);

const rows = (n: number): StatRow[] => Array.from({ length: n }, (_, i) => ({ account: `cluster-${i + 1}`, value: i, failed: false }));

const format = (value: number | undefined) => (value === undefined ? '—' : String(value));

describe('breakdownPlacement', () => {
  const place = (type: string, h: number, accounts: number) => breakdownPlacement(panel(type, h), accounts);

  it('lists a default-height stat under its number while they fit there, then beside it', () => {
    expect(place('stat', 8, 6)).toBe('under');
    expect(place('stat', 8, 9)).toBe('beside');
    expect(place('stat', 8, 10)).toBeNull();
  });

  it('fits four accounts beside the number of a short stat, which has no room under it', () => {
    expect(place('stat', 5, 2)).toBe('beside');
    expect(place('stat', 5, 4)).toBe('beside');
    expect(place('stat', 5, 5)).toBeNull();
  });

  it('caps a gauge at three rows under its dial, and never puts them beside it', () => {
    expect(place('gauge', 8, 3)).toBe('under');
    expect(place('gauge', 8, 4)).toBeNull();
    expect(place('gauge', 20, 4)).toBeNull();
  });

  it('gives a gauge no rows when the dial would drop below its minimum', () => {
    expect(place('gauge', 6, 2)).toBeNull();
  });

  it('has nothing to list for one account', () => {
    expect(place('stat', 8, 1)).toBeNull();
  });

  it('reads an absent grid height as the default one', () => {
    expect(breakdownPlacement({ ...panel('stat', 8), grid_pos: undefined } as unknown as Panel, 6)).toBe('under');
  });
});

describe('PanelAccountBreakdown', () => {
  it('lists every account it is given, and says where it sits', () => {
    render(<PanelAccountBreakdown rows={rows(6)} placement='beside' format={format} testId='bd' />);
    expect(screen.getAllByTestId('panel-breakdown-row')).toHaveLength(6);
    expect(screen.getByTestId('bd')).toHaveAttribute('data-placement', 'beside');
  });

  it('tells an account that reported nothing apart from one that did not answer', () => {
    render(
      <PanelAccountBreakdown
        rows={[
          { account: 'prod', value: 5, failed: false },
          { account: 'qa', value: undefined, failed: false },
          { account: 'dev', value: undefined, failed: true },
        ]}
        placement='under'
        format={format}
      />
    );
    expect(screen.getAllByTestId('panel-breakdown-row').map((r) => r.textContent)).toEqual(['prod5', 'qano data', 'devno answer']);
  });
});

describe('the breakdown on a stat or gauge card', () => {
  // Three accounts answering; the panel's own number is their sum, 100.
  const sampleData = {
    labels: ['10:00'],
    timestamps: [1],
    series: [
      { label: 'prod-eu', accountLabel: 'prod-eu', values: [90] },
      { label: 'prod-us', accountLabel: 'prod-us', values: [0] },
      { label: 'dev', accountLabel: 'dev', values: [10] },
    ],
  };
  const card = (type: string, h: number) => ({ ...panel(type, h), id: 'p1', targets: [{ ref_id: 'A', expr: 'sum(up)' }] } as unknown as Panel);
  const mount = (type: string, h: number, data: object = sampleData) =>
    render(<DashboardPanel panel={card(type, h)} accounts={[]} variables={{}} startTime={0} endTime={1} sampleData={data as never} />);
  const accountsIn = (el: HTMLElement) =>
    within(el)
      .getAllByTestId('panel-breakdown-row')
      .map((r) => r.textContent);

  it('lists each account under the total, without a hover', () => {
    mount('stat', 8);
    const stat = screen.getByTestId('panel-stat-p1');
    expect(within(stat).getByText('100')).toBeInTheDocument();
    expect(accountsIn(within(stat).getByTestId('panel-breakdown-p1'))).toEqual(['prod-eu90.00', 'prod-us0.00', 'dev10.00']);
  });

  it('lists every account the panel asked — including the ones with no data, or no answer', () => {
    mount('stat', 8, { ...sampleData, series: sampleData.series.slice(0, 1), emptyAccounts: ['prod-us'], failedAccounts: ['dev'] });
    expect(accountsIn(screen.getByTestId('panel-breakdown-p1'))).toEqual(['prod-eu90.00', 'prod-usno data', 'devno answer']);
    // The list says how many there are and which reported, so no count line repeats it.
    expect(screen.queryByText(/reporting|accounts$/)).not.toBeInTheDocument();
  });

  it('says how many reported when the accounts are not listed', () => {
    mount('stat', 4, { ...sampleData, series: sampleData.series.slice(0, 2), emptyAccounts: ['dev'] });
    expect(screen.queryByTestId('panel-breakdown-p1')).not.toBeInTheDocument();
    expect(screen.getByText('2 of 3 reporting')).toBeInTheDocument();
  });

  it('keeps a panel that got one answer from several accounts a breakdown, not a single-account card', () => {
    mount('stat', 8, { ...sampleData, series: sampleData.series.slice(0, 1), emptyAccounts: ['prod-us', 'dev'] });
    expect(accountsIn(screen.getByTestId('panel-breakdown-p1'))).toEqual(['prod-eu90.00', 'prod-usno data', 'devno data']);
  });

  it('lists them under a gauge dial too', () => {
    mount('gauge', 8);
    const gauge = screen.getByTestId('panel-gauge-p1');
    expect(accountsIn(within(gauge).getByTestId('panel-breakdown-p1'))).toHaveLength(3);
    // The dial gives up the rows' height rather than the panel growing to hold them.
    expect(within(gauge).getByTestId('dial')).toHaveAttribute('data-rows-below', '3');
  });

  it('counts the accounts under a gauge that cannot fit them all, and keeps the full dial', () => {
    const four = { ...sampleData, series: [...sampleData.series, { label: 'staging', accountLabel: 'staging', values: [5] }] };
    mount('gauge', 8, four);
    const gauge = screen.getByTestId('panel-gauge-p1');
    expect(within(gauge).queryByTestId('panel-breakdown-p1')).not.toBeInTheDocument();
    const dial = within(gauge).getByTestId('dial');
    expect(dial).toHaveAttribute('data-rows-below', '0');
    // The count says the dial is a mean: 4 accounts at 90, 0, 10 and 5 would
    // otherwise read 26 as if it were one cluster's figure, or their sum.
    expect(dial).toHaveTextContent('Average of 4 accounts');
  });

  it("shows a gauge the accounts' average, and a stat their sum, from the same answers", () => {
    mount('gauge', 8);
    expect(within(screen.getByTestId('panel-gauge-p1')).getByTestId('dial')).toHaveAttribute('data-value', String(100 / 3));
  });

  it('adds a gauge up instead when the panel says so', () => {
    const summing = { ...card('gauge', 8), options: { combine: 'sum' } } as unknown as Panel;
    render(<DashboardPanel panel={summing} accounts={[]} variables={{}} startTime={0} endTime={1} sampleData={sampleData as never} />);
    expect(within(screen.getByTestId('panel-gauge-p1')).getByTestId('dial')).toHaveAttribute('data-value', '100');
  });

  it('averages a stat instead when the panel says so', () => {
    const averaging = { ...card('stat', 4), options: { combine: 'avg' } } as unknown as Panel;
    render(<DashboardPanel panel={averaging} accounts={[]} variables={{}} startTime={0} endTime={1} sampleData={sampleData as never} />);
    expect(within(screen.getByTestId('panel-stat-p1')).getByText('33.33')).toBeInTheDocument();
    expect(screen.getByText('Average of 3 accounts')).toBeInTheDocument();
  });

  it("lists a short stat's accounts beside its number, where there is room for them", () => {
    mount('stat', 5);
    const list = within(screen.getByTestId('panel-stat-p1')).getByTestId('panel-breakdown-p1');
    expect(list).toHaveAttribute('data-placement', 'beside');
    expect(accountsIn(list)).toEqual(['prod-eu90.00', 'prod-us0.00', 'dev10.00']);
  });

  it('leaves a panel too short for a row exactly as it was — the list is on hover', () => {
    mount('stat', 4);
    expect(screen.queryByTestId('panel-breakdown-p1')).not.toBeInTheDocument();
    expect(screen.getByText('3 accounts')).toBeInTheDocument();
  });

  it('lists every account in the View modal, even when the card had no room', () => {
    mount('stat', 4);
    fireEvent.click(screen.getByRole('button', { name: 'More actions' }));
    fireEvent.click(screen.getByRole('menuitem', { name: /View/, hidden: true }));
    expect(accountsIn(within(screen.getByTestId('panel-view-p1')).getByTestId('panel-breakdown-p1'))).toHaveLength(3);
  });

  it('draws no list on a single-account panel', () => {
    mount('stat', 8, { ...sampleData, series: [{ label: 'up', values: [4] }] });
    expect(screen.queryByTestId('panel-breakdown-p1')).not.toBeInTheDocument();
  });
});
