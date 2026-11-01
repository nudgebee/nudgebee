import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import apiKubernetes from '@api1/kubernetes';
import { EventOccurrenceRows, EventStateRows, useEventOccurrence } from '@components/k8s/investigate/EventStateRows';

// The sidebar wires the hook into the rows; the tests do the same.
const Occurrence = ({ row }) => <EventOccurrenceRows occurrence={useEventOccurrence(row)} accountId={row.cloud_account_id} />;

jest.mock('next/router', () => ({
  useRouter: () => ({ push: jest.fn(), replace: jest.fn(), query: {}, pathname: '/investigate', asPath: '/investigate' }),
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sEventGroupings: jest.fn(),
  },
}));

const row = {
  id: 'e1',
  cloud_account_id: 'acc-1',
  fingerprint: 'fp-1',
  status: 'CLOSED',
  starts_at: '2026-09-14 09:00:00',
  ends_at: '2026-09-14 09:05:00',
  created_at: '2026-09-14 09:00:02.197584',
};

describe('EventStateRows', () => {
  beforeEach(() => jest.clearAllMocks());

  // created_at carries microseconds and the filter only milliseconds, so the forward lookup must
  // start past the event's own created_at or it counts the event itself.
  it('shows the lifecycle state', () => {
    render(<EventStateRows row={row} />);
    // A CLOSED event's State row is the Alert Status control, which labels itself
    // "Closed"; the duration sits beside it.
    expect(screen.getByText('Closed')).toBeInTheDocument();
    expect(screen.getByText('lasted 5m')).toBeInTheDocument();
  });
});

describe('event occurrences', () => {
  beforeEach(() => jest.clearAllMocks());

  // created_at carries microseconds and the filter only milliseconds, so the forward lookup must
  // start past the event's own created_at or it counts the event itself.
  it('counts the same fingerprint before and after this event, and places it in the run', async () => {
    apiKubernetes.getK8sEventGroupings
      // earlier (ascending: the oldest first)
      .mockResolvedValueOnce({
        data: {
          event_groupings: [{ id: 'e0', status: 'CLOSED', starts_at: '2026-09-13 02:03:00' }],
          event_groupings_aggregate: { aggregate: { count: 3 } },
        },
      })
      // later (descending: the newest first)
      .mockResolvedValueOnce({
        data: {
          event_groupings: [{ id: 'e9', status: 'FIRING', starts_at: '2026-09-14 10:00:00' }],
          event_groupings_aggregate: { aggregate: { count: 3 } },
        },
      });
    render(<Occurrence row={row} />);

    const [earlier, later] = apiKubernetes.getK8sEventGroupings.mock.calls;
    expect(earlier[2].fingerprint).toBe('fp-1');
    expect(earlier[2].account_id).toBe('acc-1');
    // Ends at this event (plus the microsecond slack), so the count includes it.
    expect(earlier[2].end_date.toISOString()).toBe('2026-09-14T09:00:03.197Z');
    expect(earlier[5]).toEqual({ name: 'starts_at', order: 'asc' });
    // Grouped on starts_at: without a unit the query engine truncates it to the day,
    // so same-day firings tie and "Last one" reads as midnight.
    const toTheSecond = [{ name: 'starts_at', expr: 'date_unit', args: ['second'] }];
    expect(earlier[6]).toEqual(toTheSecond);
    expect(later[6]).toEqual(toTheSecond);
    // One millisecond later, so an event on the boundary is not counted twice.
    expect(later[2].start_date.toISOString()).toBe('2026-09-14T09:00:03.198Z');

    expect(await screen.findByText('Firings')).toBeInTheDocument();
    expect(screen.getByTestId('event-occurrence')).toHaveTextContent('3rd of 6 since 13 Sep');
    expect(screen.getByTestId('event-occurrence').querySelector('a')).toHaveAttribute('href', '/investigate?id=e0&accountId=acc-1');
    expect(screen.getByText('Last one')).toBeInTheDocument();
    expect(screen.getByTestId('event-occurrence-latest').querySelector('a')).toHaveAttribute('href', '/investigate?id=e9&accountId=acc-1');
  });

  it('shows nothing when this is the only firing', async () => {
    apiKubernetes.getK8sEventGroupings.mockResolvedValue({
      data: { event_groupings: [], event_groupings_aggregate: { aggregate: { count: 0 } } },
    });
    render(<Occurrence row={row} />);
    await waitFor(() => expect(apiKubernetes.getK8sEventGroupings).toHaveBeenCalled());
    expect(screen.queryByTestId('event-occurrence')).not.toBeInTheDocument();
  });

  // Demo groupings are canned mock data; counting them would invent recurrences.
  it('does not look up occurrences on the demo account', () => {
    render(<Occurrence row={{ ...row, cloud_account_id: 'demo' }} />);
    expect(apiKubernetes.getK8sEventGroupings).not.toHaveBeenCalled();
  });
});
