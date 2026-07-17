import { extractCreateErrorMessage } from '@components/tickets/TicketCreatePopupForm';

jest.mock('@utils/colors');
jest.mock('@components/tickets/TicketFormSection', () => () => null);
jest.mock('@api1/tickets', () => ({ __esModule: true, default: { createTicket: jest.fn() } }));
jest.mock('@ui/Modal', () => ({ Modal: () => null }));
jest.mock('@ui/Button', () => ({ Button: () => null }));
jest.mock('@ui/Toast', () => ({ toast: { error: jest.fn(), success: jest.fn(), warning: jest.fn() } }));

// res is the value TicketCreatePopupForm receives from apiTickets.createTicket:
// { data: <axios response> }, so the GraphQL body sits at res.data.data.
const resWithBody = (body) => ({ data: { data: body } });

describe('extractCreateErrorMessage', () => {
  it('extracts the upstream error from the JSON-encoded gateway error message (issue #34126)', () => {
    const upstreamError =
      'failed to create github ticket: POST https://api.github.com/repos/nudgebee/nudgebee-agent/issues: 403 Repository was archived so is read-only. []';
    const res = resWithBody({
      data: { tickets_create: null },
      errors: [{ message: JSON.stringify({ data: { insert_tickets_one: { id: '', error: upstreamError } } }) }],
    });
    expect(extractCreateErrorMessage(res)).toBe(upstreamError);
  });

  it('returns a plain-string error message as-is', () => {
    const res = resWithBody({
      data: { tickets_create: null },
      errors: [{ message: 'server_gateway_unavailable' }],
    });
    expect(extractCreateErrorMessage(res)).toBe('server_gateway_unavailable');
  });

  it('returns null when the JSON-encoded message has no insert_tickets_one error', () => {
    const res = resWithBody({
      data: { tickets_create: null },
      errors: [{ message: JSON.stringify({ data: { insert_tickets_one: { id: 'abc' } } }) }],
    });
    expect(extractCreateErrorMessage(res)).toBeNull();
  });

  it('returns null when the response has no errors', () => {
    const res = resWithBody({ data: { tickets_create: { data: { insert_tickets_one: { id: 'abc' } } } } });
    expect(extractCreateErrorMessage(res)).toBeNull();
    expect(extractCreateErrorMessage(undefined)).toBeNull();
  });
});
