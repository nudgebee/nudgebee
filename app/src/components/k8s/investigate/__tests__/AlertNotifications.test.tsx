import '@testing-library/jest-dom';
import { act, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import deliveriesApi from '@api1/notification/deliveries';
import { getAllowedNamespaces } from '@lib/auth';
import { AlertNotifications, emptyReason, useAlertDeliveries } from '@components/k8s/investigate/AlertNotifications';

/*
 * The alert page's Notifications section: an alert's deliveries, the current event's marked, and when
 * nothing was sent, why — from the same gates api-server applies before forwarding an alert.
 */

jest.mock('@api1/notification/deliveries', () => ({ __esModule: true, default: { forAlert: jest.fn() } }));
jest.mock('@lib/auth', () => ({ __esModule: true, hasAdminSurfaceAccess: jest.fn(() => true), getAllowedNamespaces: jest.fn(() => null) }));

const forAlert = deliveriesApi.forAlert as jest.Mock;
const ALERT = { id: 'e2', fingerprint: 'fp-1', cloud_account_id: 'acct', priority: 'HIGH', created_at: '2026-10-23T10:40:00Z' };

describe('why nothing was sent', () => {
  it('follows the forwarding gates in order', () => {
    expect(emptyReason({ ...ALERT, source: 'slo', priority: 'MEDIUM' }, null, true).title).toBe('Posted in the hourly SLO summary');
    expect(emptyReason({ ...ALERT, nb_status: 'suppressed', priority: 'MEDIUM' }, null, true).title).toBe('Not posted');
    expect(emptyReason({ ...ALERT, priority: 'MEDIUM' }, null, true).body).toBe(
      'Only alerts that are high priority when they first fire are posted as they happen.'
    );
  });

  it('says an alert older than the history is not in it, whatever its triage status and priority are now', () => {
    const why = emptyReason({ ...ALERT, nb_status: 'SUPPRESSED', priority: 'LOW', created_at: '2026-10-01T00:00:00Z' }, '2026-10-09T08:00:00Z', true);

    expect(why.title).toBe('Not in delivery history');
  });

  it('never blames priority on an INFO alert, which a resolve sets', () => {
    expect(emptyReason({ ...ALERT, priority: 'INFO' }, null, true).title).toBe('No delivery recorded');
  });

  it('says when the alert fired before delivery history starts', () => {
    const why = emptyReason({ ...ALERT, created_at: '2026-10-01T00:00:00Z' }, '2026-10-09T08:00:00Z', true);

    expect(why).toEqual({ title: 'Not in delivery history', body: 'Delivery history starts on 9 Oct 2026, after this alert fired.' });
  });

  it('links to the rules only for readers who can open Admin', () => {
    expect(emptyReason(ALERT, '2026-10-09T08:00:00Z', true).link?.href).toBe('/user-management#notification-rules');
    expect(emptyReason(ALERT, '2026-10-09T08:00:00Z', false).link).toBeUndefined();
  });
});

describe('the section', () => {
  beforeEach(() => forAlert.mockReset());

  const delivery = (id: string, eventId: string, status: string, fields = {}) => ({
    id,
    created_at: '2026-10-23T10:42:00Z',
    event_id: eventId,
    platform: 'slack',
    destination: 'C1',
    destination_name: 'alerts',
    status,
    ...fields,
  });

  it('marks the current event and folds earlier firings', () => {
    const deliveries = {
      visible: true,
      historyStart: null,
      error: '',
      rows: [delivery('d1', 'e2', 'sent'), delivery('d2', 'e2', 'failed', { reason: 'not_in_channel' }), delivery('d0', 'e1', 'sent')],
    };

    render(<AlertNotifications row={ALERT} deliveries={deliveries} />);

    expect(screen.getByText('1 sent, 1 failed')).toBeInTheDocument();
    expect(screen.getAllByText('This event')).toHaveLength(2);
    fireEvent.click(screen.getByTestId('sidebar-notifications-earlier'));
    expect(screen.getAllByText('#alerts · Slack')).toHaveLength(3);
  });

  it('stays hidden for a reader the delivery history refuses', async () => {
    forAlert.mockResolvedValue({ data: { rows: [], historyStart: null }, error: 'forbidden', denied: true });

    const { result } = renderHook(() => useAlertDeliveries(ALERT));

    await waitFor(() => expect(forAlert).toHaveBeenCalledWith('fp-1', 'e2'));
    await act(async () => undefined);
    expect(result.current.visible).toBe(false);
  });

  it("never renders one alert's deliveries under another", async () => {
    forAlert
      .mockResolvedValueOnce({ data: { rows: [delivery('d1', 'e2', 'sent')], historyStart: null } })
      .mockReturnValue(new Promise(() => undefined));
    const renders: { fingerprint?: string; visible: boolean }[] = [];
    const { result, rerender } = renderHook(
      ({ row }: { row?: typeof ALERT }) => {
        const deliveries = useAlertDeliveries(row);
        renders.push({ fingerprint: row?.fingerprint, visible: deliveries.visible });
        return deliveries;
      },
      { initialProps: { row: ALERT as typeof ALERT | undefined } }
    );
    await waitFor(() => expect(result.current.visible).toBe(true));

    rerender({ row: { ...ALERT, id: 'e9', fingerprint: 'fp-2' } });
    rerender({ row: undefined });

    expect(renders.filter((r) => r.visible).map((r) => r.fingerprint)).toEqual(expect.arrayContaining(['fp-1']));
    expect(renders.filter((r) => r.visible && r.fingerprint !== 'fp-1')).toEqual([]);
  });

  it("says when this event sent nothing, and keeps other firings' deliveries folded below", () => {
    const deliveries = {
      visible: true,
      historyStart: null,
      error: '',
      rows: [delivery('d0', 'e1', 'sent', { created_at: '2026-10-22T09:00:00Z' })],
    };

    render(<AlertNotifications row={ALERT} deliveries={deliveries} />);

    expect(screen.getByTestId('sidebar-notifications-empty')).toHaveTextContent('This event: no delivery recorded');
    expect(screen.queryByText('#alerts · Slack')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Show 1 from earlier firings'));
    expect(screen.getByText('#alerts · Slack')).toBeInTheDocument();
    expect(screen.queryAllByText('This event')).toHaveLength(0);
  });

  it("calls a later firing's deliveries other firings, on an older event", () => {
    const deliveries = { visible: true, historyStart: null, error: '', rows: [delivery('d5', 'e5', 'sent')] };

    render(<AlertNotifications row={{ ...ALERT, created_at: '2026-10-20T10:00:00Z' }} deliveries={deliveries} />);

    expect(screen.getByText('Show 1 from other firings')).toBeInTheDocument();
  });

  it('stays hidden, without asking, where the reader sees only some namespaces of the account', () => {
    (getAllowedNamespaces as jest.Mock).mockReturnValueOnce(['payments']);

    const { result } = renderHook(() => useAlertDeliveries(ALERT));

    expect(forAlert).not.toHaveBeenCalled();
    expect(result.current.visible).toBe(false);
  });

  it('never asks for the demo account', () => {
    renderHook(() => useAlertDeliveries({ ...ALERT, cloud_account_id: 'demo' }));

    expect(forAlert).not.toHaveBeenCalled();
  });
});
