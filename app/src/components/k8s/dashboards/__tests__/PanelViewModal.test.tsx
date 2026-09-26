import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import DashboardPanel from '../DashboardPanel';
import type { Panel } from '@api1/dashboards';

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

// Records the height each chart was drawn at, so the test can tell the grid's plot from the modal's.
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    Line: (props: { chartLabel: string[]; minHeight: number }) => (
      <div data-testid='line-chart' data-height={props.minHeight}>
        {props.chartLabel.join(' | ')}
      </div>
    ),
    Bar: () => <div data-testid='bar-chart' />,
  },
}));

const panel = {
  id: 'p1',
  title: 'Pod restarts',
  description: 'Restarts across the fleet',
  type: 'timeseries',
  datasource: 'metrics',
  targets: [],
} as unknown as Panel;

const sampleData = {
  labels: ['10:00', '10:05'],
  timestamps: [1, 2],
  series: [{ label: 'a-very-long-series-name-that-a-narrow-panel-would-clip', values: [1, 2] }],
};

describe('DashboardPanel View', () => {
  it('opens the whole panel in a modal, drawn from the data it already has', () => {
    render(<DashboardPanel panel={panel} accounts={[]} variables={{}} startTime={0} endTime={1} sampleData={sampleData as never} />);
    expect(screen.queryByTestId('panel-view-p1')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'More actions' }));
    // The menu is a MUI Menu, which marks the rest of the tree aria-hidden while it is open.
    fireEvent.click(screen.getByRole('menuitem', { name: /View/, hidden: true }));

    const view = screen.getByTestId('panel-view-p1');
    const chart = within(view).getByTestId('line-chart');
    expect(chart).toHaveTextContent('a-very-long-series-name-that-a-narrow-panel-would-clip');
    expect(Number(chart.getAttribute('data-height'))).toBeGreaterThan(160);
    expect(screen.getByRole('dialog')).toHaveTextContent('Pod restarts');
    expect(screen.getByRole('dialog')).toHaveTextContent('Restarts across the fleet');
  });
});
