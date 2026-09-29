import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import DashboardPanel from '../DashboardPanel';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * A threshold step can be about one account, or every account on its own,
 * rather than the number the panel shows. These tests are about what the card
 * says when one of them fires: which frame, which badge, which row.
 */

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

const accounts = [
  { value: 'acc-1', label: 'prod-eu', cloud_provider: 'K8S' },
  { value: 'acc-2', label: 'prod-us', cloud_provider: 'K8S' },
  { value: 'acc-3', label: 'dev', cloud_provider: 'K8S' },
] as AccountOption[];

// 90 + 0 + 10 = 100.
const sampleData = {
  labels: ['10:00'],
  timestamps: [1],
  series: [
    { label: 'prod-eu', accountLabel: 'prod-eu', values: [90] },
    { label: 'prod-us', accountLabel: 'prod-us', values: [0] },
    { label: 'dev', accountLabel: 'dev', values: [10] },
  ],
};

const panelWith = (thresholds: unknown[], h = 8) =>
  ({
    id: 'p1',
    title: 'Pods running',
    type: 'stat',
    datasource: 'metrics',
    account_ids: ['acc-1', 'acc-2', 'acc-3'],
    grid_pos: { x: 0, y: 0, w: 4, h },
    targets: [{ ref_id: 'A', expr: 'sum(up)' }],
    options: { thresholds },
  } as unknown as Panel);

const mount = (thresholds: unknown[], h = 8) =>
  render(
    <DashboardPanel panel={panelWith(thresholds, h)} accounts={accounts} variables={{}} startTime={0} endTime={1} sampleData={sampleData as never} />
  );

const rowsOf = () =>
  within(screen.getByTestId('panel-breakdown-p1'))
    .getAllByTestId('panel-breakdown-row')
    .map((r) => ({ text: r.firstChild?.textContent, threshold: r.getAttribute('data-threshold') }));

describe('a threshold about an account', () => {
  it('fires on the account that crossed, names it, and moves its row to the top', () => {
    mount([{ value: 5, color: 'red', op: 'lte', applies_to: 'every' }]);
    expect(screen.getByTestId('dashboard-panel-p1')).toHaveAttribute('data-threshold', 'red');
    expect(screen.getByTestId('panel-threshold-p1')).toHaveTextContent('prod-us ≤ 5');
    expect(rowsOf()).toEqual([
      { text: 'prod-us', threshold: 'red' },
      { text: 'prod-eu', threshold: null },
      { text: 'dev', threshold: null },
    ]);
  });

  it('says whose number crossed which line, on hover', async () => {
    mount([{ value: 5, color: 'red', op: 'lte', applies_to: 'every' }]);
    fireEvent.mouseOver(screen.getByTestId('panel-threshold-p1'));
    expect(await screen.findByRole('tooltip')).toHaveTextContent('prod-us is at 0.00, at or below the 5 threshold.');
  });

  it('fires a step naming one account only on that account', () => {
    mount([{ value: 5, color: 'amber', applies_to: 'account', account_id: 'acc-3' }]);
    expect(screen.getByTestId('panel-threshold-p1')).toHaveTextContent('dev ≥ 5');
    // prod-eu is at 90, far past 5, and is not what the step is about.
    expect(rowsOf().filter((r) => r.threshold)).toEqual([{ text: 'dev', threshold: 'amber' }]);
  });

  it('still compares a step with no subject against the total, as before', () => {
    mount([{ value: 80, color: 'red' }]);
    expect(screen.getByTestId('panel-threshold-p1')).toHaveTextContent(/^≥ 80$/);
    expect(rowsOf().every((r) => r.threshold === null)).toBe(true);
  });

  it('lists the accounts over a threshold even when there is no room for the rest', () => {
    // h=4 cannot fit three rows, so the card shows the count — and the one in trouble.
    mount([{ value: 5, color: 'red', op: 'lte', applies_to: 'every' }], 4);
    expect(rowsOf()).toEqual([{ text: 'prod-us', threshold: 'red' }]);
    expect(within(screen.getByTestId('panel-stat-p1')).getByText('3 accounts')).toBeInTheDocument();
  });

  it('lists every crossed line in the hover, the one the frame shows first', async () => {
    mount([
      { value: 90, color: 'amber' },
      { value: 5, color: 'red', op: 'lt', applies_to: 'every' },
    ]);
    expect(screen.getByTestId('panel-threshold-p1')).toHaveTextContent('prod-us < 5');
    fireEvent.mouseOver(screen.getByTestId('panel-threshold-p1'));
    const tip = await screen.findByRole('tooltip');
    expect(tip.textContent).toMatch(/prod-us is at 0\.00, below the 5 threshold\..*100 is at or above the 90 threshold\./);
  });
});
