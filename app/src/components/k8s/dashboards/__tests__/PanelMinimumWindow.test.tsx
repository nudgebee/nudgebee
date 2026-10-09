import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import DashboardPanel from '../DashboardPanel';
import type { AccountOption, Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

/*
 * Every dashboard opens on the last hour, and cloud spend is written once a
 * day: a spend panel asked for that hour and said "Nothing came back", which
 * read as "no spend". It asks for the table's minimum window instead, and
 * says so in its header — a week of spend must not pass for an hour's.
 */

const executeEntityQuery = jest.fn();

jest.mock('@api1/dashboards', () => {
  const actual = jest.requireActual('@api1/dashboards');
  return {
    __esModule: true,
    ...actual,
    default: { ...actual.default, executeEntityQuery: (...args: unknown[]) => executeEntityQuery(...args) },
  };
});

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const NOW = Date.UTC(2026, 9, 9, 7, 30);

const accounts = [{ value: 'aws-prod', label: 'aws-prod', cloud_provider: 'AWS' }] as AccountOption[];

function panel(table: string, timeColumn: string): Panel {
  return {
    id: 1,
    title: 'Spend by cloud service',
    type: 'table',
    datasource: 'nudgebee',
    account_ids: ['aws-prod'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', query: buildEntityQuery(defaultDraft(table)) as never, time_column: timeColumn }],
  } as Panel;
}

const renderPanel = (p: Panel, startTime: number) =>
  render(<DashboardPanel panel={p} accounts={accounts} variables={{}} startTime={startTime} endTime={NOW} forceLoad />);

const sentWindow = () => {
  const request = executeEntityQuery.mock.calls[executeEntityQuery.mock.calls.length - 1][0];
  return { start: request.start_time, end: request.end_time };
};

describe('a panel on a table written once a day', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    executeEntityQuery.mockResolvedValue({ data: { columns: ['resource_service_name'], rows: [['AmazonEC2']] } });
  });

  it('asks for seven days of spend when the dashboard is on the last hour, and says so', async () => {
    renderPanel(panel('spend_groupings_v2', 'spend_date'), NOW - HOUR);
    await waitFor(() => expect(executeEntityQuery).toHaveBeenCalled());

    expect(sentWindow()).toEqual({ start: NOW - 7 * DAY, end: NOW });
    expect(screen.getByTestId('panel-window-1')).toHaveTextContent('Showing 7 days');
  });

  it('asks for the range as picked once it is seven days or longer', async () => {
    renderPanel(panel('spend_groupings_v2', 'spend_date'), NOW - 30 * DAY);
    await waitFor(() => expect(executeEntityQuery).toHaveBeenCalled());

    expect(sentWindow()).toEqual({ start: NOW - 30 * DAY, end: NOW });
    expect(screen.queryByTestId('panel-window-1')).not.toBeInTheDocument();
  });

  it('leaves a panel on any other table on the last hour', async () => {
    renderPanel(panel('ticket_groupings_v2', 'created_at'), NOW - HOUR);
    await waitFor(() => expect(executeEntityQuery).toHaveBeenCalled());

    expect(sentWindow()).toEqual({ start: NOW - HOUR, end: NOW });
    expect(screen.queryByTestId('panel-window-1')).not.toBeInTheDocument();
  });
});
