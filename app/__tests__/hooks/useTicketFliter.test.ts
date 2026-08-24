import { renderHook, act } from '@testing-library/react';
import useTicketFliter from '@hooks/useTicketFliter';

jest.mock('@assets', () => ({ TicketsIcon: 'tickets-icon.svg' }));
jest.mock('uuid', () => ({ v4: jest.fn(() => 'test-uuid') }));
jest.mock('@lib/encode', () => ({ md5: jest.fn((args) => `md5-${args.join('-')}`) }));
jest.mock('src/utils/common', () => ({
  snakeToTitleCase: jest.fn((s) => s.replace(/_/g, ' ')),
}));

describe('useTicketFliter', () => {
  it('initialises with correct default state', () => {
    const { result } = renderHook(() => useTicketFliter());
    expect(result.current.ticketData).toEqual({});
    expect(result.current.isTicketCreateFormOpen).toBe(false);
    expect(result.current.isSnackBarOpen).toBe(false);
  });

  it('getMenuItem returns a single "Create Ticket" item', () => {
    const { result } = renderHook(() => useTicketFliter());
    const items = result.current.getMenuItem();
    expect(items).toHaveLength(1);
    expect(items[0].label).toBe('Create Ticket');
    expect(items[0].id).toBe(0);
  });

  it('onMenuClick with id=0 sets ticketData and opens form', () => {
    const { result } = renderHook(() => useTicketFliter());
    const menuItem = { id: 0, label: 'Create Ticket' };
    const data = { id: 'row-1', name: 'Test Issue' };

    act(() => result.current.onMenuClick(menuItem, data));

    expect(result.current.ticketData).toEqual(data);
    expect(result.current.isTicketCreateFormOpen).toBe(true);
  });

  it('closeTicketCreateForm closes the form', () => {
    const { result } = renderHook(() => useTicketFliter());
    act(() => result.current.onMenuClick({ id: 0 }, {}));
    expect(result.current.isTicketCreateFormOpen).toBe(true);

    act(() => result.current.closeTicketCreateForm());
    expect(result.current.isTicketCreateFormOpen).toBe(false);
  });

  it('closeSnackBarOpen closes the snackbar', () => {
    const { result } = renderHook(() => useTicketFliter());
    act(() => result.current.handleTicketFailure('Some error'));
    expect(result.current.isSnackBarOpen).toBe(true);

    act(() => result.current.closeSnackBarOpen());
    expect(result.current.isSnackBarOpen).toBe(false);
  });

  it('handleTicketFailure sets error snackbar and opens it', () => {
    const { result } = renderHook(() => useTicketFliter());
    act(() => result.current.handleTicketFailure('Bad request'));
    expect(result.current.snackbarData.message).toBe('Failed! Bad request.');
    expect(result.current.snackbarData.severity).toBe('error');
    expect(result.current.isSnackBarOpen).toBe(true);
  });

  it('getTicketDescription returns empty string for falsy input', () => {
    const { result } = renderHook(() => useTicketFliter());
    expect(result.current.getTicketDescription(null)).toBe('');
    expect(result.current.getTicketDescription(undefined)).toBe('');
  });

  it('getTicketDescription flattens and formats object fields', () => {
    const { result } = renderHook(() => useTicketFliter());
    const description = result.current.getTicketDescription({ event_id: 'E1', status: 'open' });
    expect(description).toContain('event id');
    expect(description).toContain('E1');
    expect(description).toContain('status');
    expect(description).toContain('open');
  });

  it('getTicketDescription flattens nested objects', () => {
    const { result } = renderHook(() => useTicketFliter());
    const description = result.current.getTicketDescription({ outer: { inner_key: 'value' } });
    expect(description).toContain('value');
  });

  it('getTicketReferenceId returns md5 hash when data and stream labels present', () => {
    const { result } = renderHook(() => useTicketFliter());
    const ticketData = {
      data: 'log message',
      stream: { labels: { app: 'myapp', container: 'mycontainer', namespace: 'default' } },
    };
    const refId = result.current.getTicketReferenceId(ticketData);
    expect(refId).toMatch(/^md5-/);
  });

  it('getTicketReferenceId returns uuid when data or stream is missing', () => {
    const { result } = renderHook(() => useTicketFliter());
    const refId = result.current.getTicketReferenceId({});
    expect(refId).toBe('test-uuid');
  });

  it('handleTicketSuccess does not throw', () => {
    const { result } = renderHook(() => useTicketFliter());
    expect(() => result.current.handleTicketSuccess()).not.toThrow();
  });
});
