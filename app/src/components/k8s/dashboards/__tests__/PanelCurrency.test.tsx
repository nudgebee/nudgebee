import React from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import DashboardPanel from '../DashboardPanel';
import type { AccountOption, Panel } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft } from '../entityQuery';

/*
 * A spend row is billed in its account's currency, and the table already had it
 * in a column of its own: "$273,887" sat beside "INR".
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

const accounts = [{ value: 'gcp-prod', label: 'gcp-prod', cloud_provider: 'GCP' }] as AccountOption[];

function spendPanel(extra: Partial<Panel> = {}): Panel {
  return {
    id: 1,
    title: 'Spend',
    type: 'table',
    datasource: 'nudgebee',
    account_ids: ['gcp-prod'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', query: buildEntityQuery(defaultDraft('spend_groupings_v2')) as never }],
    ...extra,
  } as Panel;
}

async function spendRow(service: string) {
  const name = await screen.findByText(service);
  return within(name.closest('tr') as HTMLElement);
}

const show = (panel: Panel) => render(<DashboardPanel panel={panel} accounts={accounts} variables={{}} startTime={1000} endTime={2000} forceLoad />);

describe('a spend table', () => {
  beforeEach(() => executeEntityQuery.mockReset());

  it("uses each row's own currency", async () => {
    executeEntityQuery.mockResolvedValue({
      data: {
        columns: ['resource_service_name', 'spend_amount', 'currency_type'],
        rows: [
          ['Vertex AI', '164751', 'INR'],
          ['EC2', '200', 'USD'],
          ['Mystery', '300', 'not-a-code'],
        ],
      },
    });
    show(spendPanel());
    expect((await spendRow('Vertex AI')).getByText('₹')).toBeTruthy();
    expect((await spendRow('EC2')).getByText('$')).toBeTruthy();
    expect((await spendRow('Mystery')).getByText('$')).toBeTruthy();
  });

  it('still uses the currency when the column is hidden', async () => {
    executeEntityQuery.mockResolvedValue({
      data: { columns: ['resource_service_name', 'spend_amount', 'currency_type'], rows: [['Vertex AI', '164751', 'INR']] },
    });
    show(spendPanel({ options: { columns: [{ name: 'currency_type', visibility: 'hidden' }] } } as Partial<Panel>));
    expect((await spendRow('Vertex AI')).getByText('₹')).toBeTruthy();
    await waitFor(() => expect(screen.queryByText('INR')).toBeNull());
  });

  it('stays on the default symbol when the panel has no currency column', async () => {
    executeEntityQuery.mockResolvedValue({ data: { columns: ['resource_service_name', 'spend_amount'], rows: [['Vertex AI', '164751']] } });
    show(spendPanel());
    expect((await spendRow('Vertex AI')).getByText('$')).toBeTruthy();
  });
});
