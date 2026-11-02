import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import DashboardPanel from '../DashboardPanel';
import type { AccountOption, Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

/*
 * A `nudgebee` panel reads the query engine, which takes an account LIST in one
 * call. Scoped to several accounts and with nothing picked, it has to ask all
 * of them — the template's "savings by account" asked the first and showed
 * nothing when that one had no savings.
 */

const executeEntityQuery = jest.fn();
const traceV2 = jest.fn();

jest.mock('@api1/dashboards', () => {
  const actual = jest.requireActual('@api1/dashboards');
  return {
    __esModule: true,
    ...actual,
    default: { ...actual.default, executeEntityQuery: (...args: unknown[]) => executeEntityQuery(...args) },
  };
});

jest.mock('@api1/kubernetes/trace', () => ({
  __esModule: true,
  default: {
    traceV2: (...args: unknown[]) => traceV2(...args),
    traceGroupV2: jest.fn(async () => ({ traces_grouping_v3: [] })),
    traceLabelValues: jest.fn(async () => []),
  },
}));

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));

// The panel's account picker, doubled as one button per option (single-select hands back the option).
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: (props: { id: string; options: { value: string }[]; onSelect: (e: unknown, v: unknown) => void }) => (
    <div data-testid={props.id}>
      {props.options.map((o) => (
        <button key={o.value} type='button' data-testid={`${props.id}-opt-${o.value}`} onClick={() => props.onSelect(null, o)} />
      ))}
    </div>
  ),
}));

const accounts = [
  { value: 'aws-demo', label: 'aws-demo', cloud_provider: 'AWS' },
  { value: 'aws-prod', label: 'aws-prod', cloud_provider: 'AWS' },
  { value: 'gcp-prod', label: 'gcp-prod', cloud_provider: 'GCP' },
] as AccountOption[];

function panel(datasource: 'nudgebee' | 'traces', table: string): Panel {
  return {
    id: 1,
    title: 'Savings by account',
    type: 'table',
    datasource,
    account_ids: accounts.map((a) => a.value),
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', query: buildEntityQuery(defaultDraft(table)) as never }],
  } as Panel;
}

const renderPanel = (p: Panel) => render(<DashboardPanel panel={p} accounts={accounts} variables={{}} startTime={1000} endTime={2000} forceLoad />);

const lastAccountIds = () => executeEntityQuery.mock.calls[executeEntityQuery.mock.calls.length - 1][0].account_ids;

describe('a nudgebee panel scoped to several accounts', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    executeEntityQuery.mockResolvedValue({ data: { columns: ['id'], rows: [] } });
    traceV2.mockResolvedValue({ traces_list: [] });
  });

  it('asks every account it is scoped to when the viewer has picked none', async () => {
    renderPanel(panel('nudgebee', 'events_v2'));
    await waitFor(() => expect(executeEntityQuery).toHaveBeenCalled());

    expect(lastAccountIds()).toEqual(['aws-demo', 'aws-prod', 'gcp-prod']);
  });

  it('asks only the account the viewer picks', async () => {
    renderPanel(panel('nudgebee', 'events_v2'));
    await waitFor(() => expect(executeEntityQuery).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('panel-account-filter-1-opt-aws-prod'));
    await waitFor(() => expect(lastAccountIds()).toEqual(['aws-prod']));
  });

  it('leaves a traces panel on its first account — the trace store takes one', async () => {
    renderPanel(panel('traces', 'traces_v2'));
    await waitFor(() => expect(traceV2).toHaveBeenCalled());

    expect(executeEntityQuery).not.toHaveBeenCalled();
    expect(traceV2).toHaveBeenCalledTimes(1);
    expect(traceV2.mock.calls[0][0].accountId).toBe('aws-demo');
  });
});
