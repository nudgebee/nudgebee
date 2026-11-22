import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import deliveriesApi from '@api1/notification/deliveries';
import DeliveryHistory from '../DeliveryHistory';

/*
 * Admin → Notification Rules → Delivery history: the failing destinations, one row per send reading as
 * its last attempt, and the filters that narrow them.
 */

jest.mock('@api1/notification/deliveries', () => ({
  __esModule: true,
  default: { list: jest.fn(), failingDestinations: jest.fn(), destinations: jest.fn(), listAttempts: jest.fn() },
}));
jest.mock('@api1/user', () => ({ __esModule: true, default: { getUserPreferencesTablePageSize: () => 25 } }));
jest.mock('@context/DataContext', () => ({ useData: () => ({ allCluster: [{ value: 'acct-1', label: 'prod-gke-eu' }] }) }));
jest.mock('@hooks/useTenantBranding', () => ({ useBrandingConfig: () => ({ title: 'Acme Ops' }) }));
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: (props: {
    id: string;
    options: { label: string; value: string }[];
    value: { value: string } | null;
    onSelect: (e: any, v: any) => void;
  }) => (
    <div data-testid={props.id} data-selected={props.value?.value ?? ''}>
      {props.options.map((o) => (
        <button key={o.value} type='button' data-testid={`${props.id}-opt-${o.value}`} onClick={() => props.onSelect({}, o)}>
          {o.label}
        </button>
      ))}
    </div>
  ),
}));
const query: Record<string, string> = {};
jest.mock('next/router', () => ({ useRouter: () => ({ query, push: jest.fn(), replace: jest.fn(), asPath: '/user-management' }) }));

const api = deliveriesApi as unknown as Record<string, jest.Mock>;

const ALERTS_CHANNEL = {
  platform: 'slack',
  team_id: 'T1',
  destination: 'C1',
  last_destination_name: 'alerts',
  last_reason: 'not_in_channel',
  last_at: '2026-10-23T10:42:00Z',
};

beforeEach(() => {
  jest.clearAllMocks();
  Object.keys(query).forEach((k) => delete query[k]);
  api.failingDestinations.mockResolvedValue({ data: [ALERTS_CHANNEL] });
  api.destinations.mockResolvedValue({ data: [ALERTS_CHANNEL] });
  api.list.mockResolvedValue({
    data: {
      total: 1,
      rows: [
        {
          id: 'd1',
          created_at: '2026-10-23T09:58:21Z',
          title: 'Error rate above 2% on checkout',
          kind: 'finding',
          platform: 'slack',
          destination: 'C2',
          destination_name: 'payments',
          status: 'sent',
          attempt: 3,
          message_id: '1700000000.000300',
          team_url: 'https://acme.slack.com/',
          routed_by: JSON.stringify([{ id: 'r1', name: 'Payments team' }]),
          account_id: 'acct-1',
        },
      ],
    },
  });
});

it('lists failing destinations with the reason and the fix, and each send as its last attempt', async () => {
  render(<DeliveryHistory />);

  expect(await screen.findByText('Acme Ops was removed from #alerts. Re-invite it.')).toBeInTheDocument();
  expect(await screen.findByText('Error rate above 2% on checkout')).toBeInTheDocument();
  expect(screen.getByText('Sent on attempt 3.')).toBeInTheDocument();
  expect(screen.getByText('Payments team')).toBeInTheDocument();
  expect(screen.getByTestId('delivery-history-message-d1').querySelector('a')).toHaveAttribute(
    'href',
    'https://acme.slack.com/archives/C2/p1700000000000300'
  );
  expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ fingerprint: undefined, status: undefined }), 25, 0);
});

it("shows a failing destination's deliveries", async () => {
  render(<DeliveryHistory />);

  fireEvent.click(await screen.findByTestId('delivery-history-show-slack-C1'));

  await waitFor(() =>
    expect(api.list).toHaveBeenLastCalledWith(expect.objectContaining({ destination: { platform: 'slack', destination: 'C1' } }), 25, 0)
  );
});

it("opens on an alert's deliveries from the alert page, and lets the filter go", async () => {
  query.alert = 'fp-1';
  query.type = 'summary';
  render(<DeliveryHistory />);

  await waitFor(() =>
    expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ fingerprint: 'fp-1', kinds: ['grouped_anomaly', 'grouped_slo_alert'] }), 25, 0)
  );
  expect(screen.getByTestId('delivery-history-alert-filter')).toHaveTextContent('This alert');
});

it('applies a link whose query arrives after the first render, as it does on a page load', async () => {
  const { rerender } = render(<DeliveryHistory />);
  await waitFor(() => expect(api.list).toHaveBeenCalled());

  query.alert = 'fp-1';
  query.type = 'summary';
  rerender(<DeliveryHistory />);

  await waitFor(() =>
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ fingerprint: 'fp-1', kinds: ['grouped_anomaly', 'grouped_slo_alert'] }),
      25,
      0
    )
  );
});

it('never shows an all-clear when the failing check itself failed', async () => {
  api.failingDestinations.mockResolvedValueOnce({ data: [], error: 'Upstream unreachable' });
  render(<DeliveryHistory />);

  expect(await screen.findByText('Couldn’t check for failing destinations.')).toBeInTheDocument();
  expect(screen.queryByText(/No failing destinations/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByTestId('delivery-history-failing-retry'));

  expect(await screen.findByText('Acme Ops was removed from #alerts. Re-invite it.')).toBeInTheDocument();
  expect(api.failingDestinations).toHaveBeenCalledTimes(2);
});

it('lets a destination on another platform clear the platform filter, and the other way round', async () => {
  render(<DeliveryHistory />);
  await screen.findByTestId('delivery-history-destination-filter-opt-slack:C1');

  fireEvent.click(screen.getByTestId('delivery-history-platform-filter-opt-ms_teams'));
  fireEvent.click(screen.getByTestId('delivery-history-destination-filter-opt-slack:C1'));

  expect(screen.getByTestId('delivery-history-platform-filter')).toHaveAttribute('data-selected', '');
  await waitFor(() =>
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ platforms: undefined, destination: { platform: 'slack', destination: 'C1' } }),
      25,
      0
    )
  );

  fireEvent.click(screen.getByTestId('delivery-history-platform-filter-opt-ms_teams'));

  expect(screen.getByTestId('delivery-history-destination-filter')).toHaveAttribute('data-selected', '');
  await waitFor(() => expect(api.list).toHaveBeenLastCalledWith(expect.objectContaining({ platforms: ['ms_teams'], destination: null }), 25, 0));
});

it('says when the history did not load, and tries again', async () => {
  api.list.mockResolvedValueOnce({ data: { rows: [], total: 0 }, error: 'boom' });
  render(<DeliveryHistory />);

  fireEvent.click(await screen.findByText('Try again'));

  await waitFor(() => expect(api.list).toHaveBeenCalledTimes(2));
});
