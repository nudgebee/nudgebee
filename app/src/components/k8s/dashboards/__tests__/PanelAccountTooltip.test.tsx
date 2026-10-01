import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import DashboardPanel from '../DashboardPanel';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * The Account funnel names the account it is showing on hover. Its list is
 * portaled, and React bubbles a portal's events through the component tree, so
 * the list's own focus and pointer moves used to re-open that label on top of
 * the open list. Read from the DOM, not by role: the open list marks the rest of
 * the page aria-hidden, which would hide a stuck label from a role query.
 */

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));

const accounts = [
  { value: 'acc-1', label: 'prod-us', cloud_provider: 'K8S' },
  { value: 'acc-2', label: 'k8s-dev', cloud_provider: 'K8S' },
] as AccountOption[];

const panel = {
  id: 7,
  title: 'Slowest service calls',
  type: 'table',
  datasource: 'traces',
  account_type: 'K8S',
  grid_pos: { x: 0, y: 0, w: 12, h: 8 },
  targets: [{ ref_id: 'A', query: { table: 'traces_groupings_v2', columns: [{ name: 'workload_name' }], limit: 100 } }],
} as unknown as Panel;

const sample = { labels: [], series: [], table: { columns: ['Workload'], rows: [['api']] } };

const label = () => document.querySelector('[role="tooltip"]')?.textContent ?? null;
const pause = (ms: number) => act(() => new Promise((r) => setTimeout(r, ms)));

describe('the Account funnel’s hover label', () => {
  it('names the account on hover, and stays away while the account list is open', async () => {
    render(<DashboardPanel panel={panel} accounts={accounts} variables={{}} startTime={0} endTime={1} sampleData={sample as never} />);
    const trigger = document.getElementById('auto-complete-panel-account-filter-7') as HTMLElement;

    fireEvent.mouseOver(trigger);
    await waitFor(() => expect(label()).toBe('Showing prod-us'));

    fireEvent.click(trigger);
    await waitFor(() => expect(label()).toBeNull());

    // A pointer move over a row of the open list is not a hover on the funnel.
    fireEvent.mouseOver(await screen.findByText('k8s-dev'));
    await pause(400);
    expect(label()).toBeNull();
  });
});
