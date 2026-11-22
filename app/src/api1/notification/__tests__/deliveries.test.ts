import { queryGraphQL } from '@lib/HttpService';
import deliveriesApi from '@api1/notification/deliveries';

/*
 * An alert's deliveries for its page: the event's own are read by event id, so however many later firings
 * the alert has had, they are never cut off by the alert-wide page of 50.
 */

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: (value: unknown) => JSON.stringify(value) }));
jest.mock('@lib/accessDenied', () => ({ isAccessDenied: () => false }));
const query = queryGraphQL as jest.Mock;

const answer = (data: unknown) => ({ data: { data } });

beforeEach(() => query.mockReset());

it("puts the event's own deliveries first, once each, before the alert's others", async () => {
  query.mockImplementation((_doc: string, operation: string) =>
    Promise.resolve(
      operation === 'EventDeliveries'
        ? answer({ deliveries: { rows: [{ id: 'own-1', event_id: 'e2' }] } })
        : answer({
            deliveries: {
              rows: [
                { id: 'later-1', event_id: 'e3' },
                { id: 'own-1', event_id: 'e2' },
              ],
            },
            history: { rows: [{ first_at: '2026-10-09T08:00:00Z' }] },
          })
    )
  );

  const res = await deliveriesApi.forAlert('fp-1', 'e2');

  expect(res.data.rows.map((d) => d.id)).toEqual(['own-1', 'later-1']);
  expect(res.data.historyStart).toBe('2026-10-09T08:00:00Z');
  expect(res.error).toBeUndefined();
  const eventDoc = query.mock.calls.find(([, operation]) => operation === 'EventDeliveries')[0];
  expect(eventDoc).toContain('"event_id":{"_eq":"e2"}');
  expect(eventDoc).toContain('"fingerprint":{"_eq":"fp-1"}');
});

it('reports the error when either read fails', async () => {
  query.mockImplementation((_doc: string, operation: string) =>
    Promise.resolve(
      operation === 'EventDeliveries' ? { data: { errors: [{ message: 'boom' }] } } : answer({ deliveries: { rows: [] }, history: { rows: [] } })
    )
  );

  const res = await deliveriesApi.forAlert('fp-1', 'e2');

  expect(res.error).toBe('boom');
});
